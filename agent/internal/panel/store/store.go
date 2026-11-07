// Package store keeps the control panel's own data — people, roles, invoices,
// billing and alert settings — in one JSON file (mode 0600). Server state
// (accounts, websites, …) stays in the agent; this only records who may touch what.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Role string

const (
	Admin    Role = "admin"
	Reseller Role = "reseller"
	User     Role = "user"
	// Developer works inside some hosting accounts, in chosen areas only
	// (Areas), and never on billing, the accounts themselves or the server.
	Developer Role = "developer"
)

// DeveloperAreas are the areas a developer can be given.
var DeveloperAreas = []string{"sites", "dns", "mail", "databases", "files", "backups", "containers", "security"}

// Customer is true for account owners and developers: people who work
// inside hosting accounts rather than running the server or reselling.
func (r Role) Customer() bool { return r == User || r == Developer }

var (
	ErrNotFound = errors.New("not found")
	ErrExists   = errors.New("already exists")
	usernameRe  = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,31}$`)
	emailRe     = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[A-Za-z]{2,}$`)
)

// Person is someone who signs in to the panel.
type Person struct {
	Username      string   `json:"username"`
	Email         string   `json:"email"`
	Name          string   `json:"name,omitempty"`
	Role          Role     `json:"role"`
	PasswordHash  string   `json:"password_hash"`
	TOTPSecret    string   `json:"totp_secret,omitempty"`
	TOTPEnabled   bool     `json:"totp_enabled"`
	TOTPLast      uint64   `json:"totp_last,omitempty"`
	RecoveryCodes []string `json:"recovery_codes,omitempty"` // hashed, one-time
	Reseller      string   `json:"reseller,omitempty"`       // for users created by a reseller
	Accounts      []string `json:"accounts,omitempty"`       // hosting accounts a user may manage
	MaxAccounts   int      `json:"max_accounts,omitempty"`   // reseller quota (0 = none allowed)
	// Reseller totals across all their accounts (0 = unlimited).
	MaxSites       int       `json:"max_sites,omitempty"`
	MaxDomains     int       `json:"max_domains,omitempty"` // extra domains and DNS zones
	MaxDiskMB      int       `json:"max_disk_mb,omitempty"`
	MaxBandwidthGB int       `json:"max_bandwidth_gb,omitempty"`
	Disabled       bool      `json:"disabled"`
	CreatedAt      time.Time `json:"created_at"`
	LastLoginAt    time.Time `json:"last_login_at,omitempty"`
	// Developers: what they may do in their accounts (area → read | write),
	// and who invited them (an account owner; resellers use Reseller).
	Areas     map[string]string `json:"areas,omitempty"`
	InvitedBy string            `json:"invited_by,omitempty"`
	// Passkeys sign in without a password or code. WebAuthnID is the random
	// user handle the person's authenticators store (base64url).
	Passkeys   []Passkey `json:"passkeys,omitempty"`
	WebAuthnID string    `json:"webauthn_id,omitempty"`

	// Set for the length of one request made with a limited API token.
	Token *APIToken `json:"-"`
}

// Public is a Person without secrets, for API responses.
type Public struct {
	Username       string            `json:"username"`
	Email          string            `json:"email"`
	Name           string            `json:"name,omitempty"`
	Role           Role              `json:"role"`
	TOTPEnabled    bool              `json:"totp_enabled"`
	Reseller       string            `json:"reseller,omitempty"`
	Accounts       []string          `json:"accounts"`
	MaxAccounts    int               `json:"max_accounts,omitempty"`
	Disabled       bool              `json:"disabled"`
	CreatedAt      time.Time         `json:"created_at"`
	LastLoginAt    time.Time         `json:"last_login_at,omitempty"`
	MaxSites       int               `json:"max_sites,omitempty"`
	MaxDomains     int               `json:"max_domains,omitempty"`
	MaxDiskMB      int               `json:"max_disk_mb,omitempty"`
	MaxBandwidthGB int               `json:"max_bandwidth_gb,omitempty"`
	Areas          map[string]string `json:"areas,omitempty"`
	InvitedBy      string            `json:"invited_by,omitempty"`
	Passkeys       []PasskeyInfo     `json:"passkeys"`
}

// Passkey is a WebAuthn credential (the public half; the private key never
// leaves the person's device or password manager).
type Passkey struct {
	ID         string    `json:"id"` // credential id, base64url
	Name       string    `json:"name"`
	PublicKey  []byte    `json:"public_key"` // PKIX DER
	Alg        int64     `json:"alg"`
	SignCount  uint32    `json:"sign_count"`
	Synced     bool      `json:"synced"`             // backup eligible: lives in a password manager or phone account
	Disabled   bool      `json:"disabled,omitempty"` // its counter went backwards (possibly copied)
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

// PasskeyInfo is what the panel shows about a passkey.
type PasskeyInfo struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Synced     bool      `json:"synced"`
	Disabled   bool      `json:"disabled,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitzero"`
}

// PasskeyInfos lists the person's passkeys without key material.
func (p *Person) PasskeyInfos() []PasskeyInfo {
	out := []PasskeyInfo{}
	for _, k := range p.Passkeys {
		out = append(out, PasskeyInfo{k.ID, k.Name, k.Synced, k.Disabled, k.CreatedAt, k.LastUsedAt})
	}
	return out
}

// PersonByPasskey finds who registered a credential id.
func (d *Data) PersonByPasskey(id string) (*Person, int) {
	for _, p := range d.People {
		for i, k := range p.Passkeys {
			if k.ID == id {
				return p, i
			}
		}
	}
	return nil, -1
}

func (p *Person) Public() Public {
	acc := p.Accounts
	if acc == nil {
		acc = []string{}
	}
	return Public{p.Username, p.Email, p.Name, p.Role, p.TOTPEnabled, p.Reseller, acc, p.MaxAccounts, p.Disabled, p.CreatedAt, p.LastLoginAt,
		p.MaxSites, p.MaxDomains, p.MaxDiskMB, p.MaxBandwidthGB, p.Areas, p.InvitedBy, p.PasskeyInfos()}
}

// CheckDeveloperAreas validates a developer's areas.
func CheckDeveloperAreas(areas map[string]string) error {
	if len(areas) == 0 {
		return errors.New("give the developer at least one area, such as sites")
	}
	ok := map[string]bool{}
	for _, a := range DeveloperAreas {
		ok[a] = true
	}
	for a, l := range areas {
		if !ok[a] {
			return fmt.Errorf("developers can't be given %q; choose from %s", a, strings.Join(DeveloperAreas, ", "))
		}
		if l != "read" && l != "write" {
			return fmt.Errorf("%s access is read or write", a)
		}
	}
	return nil
}

// Invoice is a bill for a hosting account. Amounts are in minor units (kobo, cents).
type Invoice struct {
	ID          string     `json:"id"`
	Account     string     `json:"account"`
	Description string     `json:"description"`
	Currency    string     `json:"currency"`
	Amount      int64      `json:"amount"`
	Status      string     `json:"status"` // unpaid | paid | void
	DueAt       time.Time  `json:"due_at"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   string     `json:"created_by"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	Provider    string     `json:"provider,omitempty"`     // bachs | paystack | flutterwave
	ProviderRef string     `json:"provider_ref,omitempty"` // checkout id / reference
	CheckoutURL string     `json:"checkout_url,omitempty"`
}

// ProviderSettings holds one gateway's keys.
type ProviderSettings struct {
	Enabled       bool   `json:"enabled"`
	SecretKey     string `json:"secret_key,omitempty"`     // Bachs sk_..., Paystack sk_..., Flutterwave FLWSECK-...
	WebhookSecret string `json:"webhook_secret,omitempty"` // Bachs whsec_..., Flutterwave secret hash
	Sandbox       bool   `json:"sandbox,omitempty"`        // Bachs sandbox host
}

type BillingSettings struct {
	Currency    string                      `json:"currency"`     // default NGN
	GraceDays   int                         `json:"grace_days"`   // days after due before auto-suspend
	AutoSuspend bool                        `json:"auto_suspend"` // suspend overdue accounts
	Providers   map[string]ProviderSettings `json:"providers"`    // bachs, paystack, flutterwave
}

type AlertSettings struct {
	Telegram struct {
		Enabled  bool   `json:"enabled"`
		BotToken string `json:"bot_token,omitempty"`
		ChatID   string `json:"chat_id"`
	} `json:"telegram"`
	WhatsApp struct {
		Enabled       bool   `json:"enabled"`
		Token         string `json:"token,omitempty"`
		PhoneNumberID string `json:"phone_number_id"`
		To            string `json:"to"`
		Template      string `json:"template,omitempty"` // approved template for messages outside the 24 h window
		APIVersion    string `json:"api_version,omitempty"`
	} `json:"whatsapp"`
	Email struct {
		Enabled  bool   `json:"enabled"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password,omitempty"`
		From     string `json:"from"`
		To       string `json:"to"`
	} `json:"email"`
	// SMS sends the same alerts as text messages (Termii or Africa's Talking).
	SMS struct {
		Enabled  bool   `json:"enabled"`
		Provider string `json:"provider"` // termii | africastalking
		APIKey   string `json:"api_key,omitempty"`
		Username string `json:"username,omitempty"` // Africa's Talking app username
		SenderID string `json:"sender_id,omitempty"`
		To       string `json:"to"` // comma-separated numbers, +234...
	} `json:"sms"`
	// Customers get expiry reminders by email (through the SMTP server above)
	// for their own domains, certificates and hosting.
	Customers struct {
		Enabled bool  `json:"enabled"`
		Days    []int `json:"days,omitempty"` // default 30, 14, 7, 1
	} `json:"customers"`
	UptimeIntervalMin int `json:"uptime_interval_min"` // 0 = off
}

type data struct {
	People      map[string]*Person `json:"people"`
	AccountOf   map[string]string  `json:"account_reseller"` // hosting account -> reseller username ("" = admin)
	Invoices    []*Invoice         `json:"invoices"`
	NextInvoice int                `json:"next_invoice"`
	Billing     BillingSettings    `json:"billing"`
	Alerts      AlertSettings      `json:"alerts"`
	Events      []string           `json:"processed_events"` // webhook ids already handled
	JobOwner    map[string]string  `json:"job_owner"`
	Servers     []*RemoteServer    `json:"servers"`
	JoinTokens  []JoinToken        `json:"join_tokens"`
	Clusters    []*DBCluster       `json:"clusters"`

	APITokens      []*APIToken                   `json:"api_tokens"`
	Packages       []*Package                    `json:"packages"`
	AccountPackage map[string]string             `json:"account_package"` // account → package ID
	Subscriptions  []*Subscription               `json:"subscriptions"`
	Branding       map[string]*Branding          `json:"branding"`  // "" admin, or reseller username
	Bandwidth      map[string]map[string]float64 `json:"bandwidth"` // "2026-10" → account → MB
	BWDays         map[string]BWDay              `json:"bw_days"`
	BWSuspended    map[string]string             `json:"bw_suspended"` // account → month it was suspended for bandwidth
	NotifRead      map[string]int64              `json:"notif_read"`   // username → last read event ID
	SSOTokens      []SSOToken                    `json:"sso_tokens"`
	// HostingExpiry is when an account's hosting ends, for accounts billed
	// outside Ike (subscriptions have their own due dates).
	HostingExpiry map[string]time.Time `json:"hosting_expiry"`
	// ExpiryAlerts remembers the last reminder day sent per item and date.
	ExpiryAlerts map[string]int `json:"expiry_alerts"`
	// CustomerAlerts remembers expiry emails sent to account owners.
	CustomerAlerts map[string]int `json:"customer_alerts"`
}

type Store struct {
	path string
	mu   sync.Mutex
	d    data
}

func (d *data) initMaps() {
	if d.AccountPackage == nil {
		d.AccountPackage = map[string]string{}
	}
	if d.Branding == nil {
		d.Branding = map[string]*Branding{}
	}
	if d.Bandwidth == nil {
		d.Bandwidth = map[string]map[string]float64{}
	}
	if d.BWDays == nil {
		d.BWDays = map[string]BWDay{}
	}
	if d.BWSuspended == nil {
		d.BWSuspended = map[string]string{}
	}
	if d.NotifRead == nil {
		d.NotifRead = map[string]int64{}
	}
	if d.HostingExpiry == nil {
		d.HostingExpiry = map[string]time.Time{}
	}
	if d.ExpiryAlerts == nil {
		d.ExpiryAlerts = map[string]int{}
	}
	if d.CustomerAlerts == nil {
		d.CustomerAlerts = map[string]int{}
	}
}

func Open(path string) (*Store, error) {
	s := &Store{path: path}
	s.d = data{People: map[string]*Person{}, AccountOf: map[string]string{}, Invoices: []*Invoice{}, NextInvoice: 1,
		Billing: BillingSettings{Currency: "NGN", GraceDays: 7, Providers: map[string]ProviderSettings{}}, JobOwner: map[string]string{}}
	s.d.Alerts.UptimeIntervalMin = 5
	s.d.initMaps()
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if s.d.People == nil {
		s.d.People = map[string]*Person{}
	}
	if s.d.AccountOf == nil {
		s.d.AccountOf = map[string]string{}
	}
	if s.d.JobOwner == nil {
		s.d.JobOwner = map[string]string{}
	}
	if s.d.Billing.Providers == nil {
		s.d.Billing.Providers = map[string]ProviderSettings{}
	}
	s.d.initMaps()
	return s, nil
}

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Update runs fn with exclusive access and saves if it returns nil.
func (s *Store) Update(fn func(d *Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn((*Data)(&s.d)); err != nil {
		return err
	}
	return s.save()
}

// View runs fn with read access.
func (s *Store) View(fn func(d *Data)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn((*Data)(&s.d))
}

// Data is the exported view used inside Update/View.
type Data data

func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return fmt.Errorf("username must be 3-32 lowercase letters, numbers, dots, dashes or underscores, starting with a letter")
	}
	return nil
}

func ValidateEmail(e string) error {
	if !emailRe.MatchString(e) {
		return fmt.Errorf("enter a valid email address")
	}
	return nil
}

func (d *Data) Person(username string) (*Person, error) {
	p, ok := d.People[strings.ToLower(username)]
	if !ok {
		return nil, fmt.Errorf("person %s %w", username, ErrNotFound)
	}
	return p, nil
}

func (d *Data) AddPerson(p *Person) error {
	p.Username = strings.ToLower(p.Username)
	if err := ValidateUsername(p.Username); err != nil {
		return err
	}
	if err := ValidateEmail(p.Email); err != nil {
		return err
	}
	if _, ok := d.People[p.Username]; ok {
		return fmt.Errorf("%s %w", p.Username, ErrExists)
	}
	switch p.Role {
	case Admin, Reseller, User:
	case Developer:
		if err := CheckDeveloperAreas(p.Areas); err != nil {
			return err
		}
		if len(p.Accounts) == 0 {
			return errors.New("choose the accounts the developer works on")
		}
	default:
		return fmt.Errorf("role must be admin, reseller, user or developer")
	}
	p.CreatedAt = time.Now().UTC()
	d.People[p.Username] = p
	return nil
}

func (d *Data) PeopleList() []Public {
	out := make([]Public, 0, len(d.People))
	for _, p := range d.People {
		out = append(out, p.Public())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out
}

func (d *Data) AdminCount() int {
	n := 0
	for _, p := range d.People {
		if p.Role == Admin && !p.Disabled {
			n++
		}
	}
	return n
}

// ResellerAccounts counts hosting accounts owned by a reseller.
func (d *Data) ResellerAccounts(reseller string) int {
	n := 0
	for _, r := range d.AccountOf {
		if r == reseller {
			n++
		}
	}
	return n
}

// NewInvoiceID returns INV-000001, INV-000002, ...
func (d *Data) NewInvoiceID() string {
	id := fmt.Sprintf("INV-%06d", d.NextInvoice)
	d.NextInvoice++
	return id
}

func (d *Data) Invoice(id string) (*Invoice, error) {
	for _, inv := range d.Invoices {
		if inv.ID == id {
			return inv, nil
		}
	}
	return nil, fmt.Errorf("invoice %s %w", id, ErrNotFound)
}

func (d *Data) InvoiceByRef(provider, ref string) (*Invoice, error) {
	for _, inv := range d.Invoices {
		if inv.Provider == provider && inv.ProviderRef == ref && ref != "" {
			return inv, nil
		}
	}
	return nil, fmt.Errorf("invoice for %s reference %s %w", provider, ref, ErrNotFound)
}

// SeenEvent records a webhook id; it returns false if it was already processed.
func (d *Data) SeenEvent(id string) bool {
	for _, e := range d.Events {
		if e == id {
			return false
		}
	}
	d.Events = append(d.Events, id)
	if len(d.Events) > 5000 {
		d.Events = d.Events[len(d.Events)-5000:]
	}
	return true
}
