// Package mail runs email hosting: Postfix receives and sends, Dovecot stores
// mailboxes (IMAP, POP3, LMTP, quotas, sieve), Rspamd filters spam, signs DKIM
// and asks ClamAV about attachments, and Roundcube is the webmail.
//
// Ike keeps domains, mailboxes and aliases as JSON and writes the lookup
// tables Postfix and Dovecot read:
//
//	/etc/ike/mail/domains     texthash  virtual_mailbox_domains
//	/etc/ike/mail/mailboxes   texthash  virtual_mailbox_maps
//	/etc/ike/mail/aliases     texthash  virtual_alias_maps
//	/etc/ike/mail/senders     texthash  smtpd_sender_login_maps
//	/etc/ike/mail/sni         lmdb -F   tls_server_sni_maps
//	/etc/ike/mail/users       Dovecot passwd-file (passdb and userdb)
//	/etc/dovecot/conf.d/99-ike-sni.conf  per-domain certificates
package mail

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/secrets"
	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// Domain is one email domain.
type Domain struct {
	Domain    string    `json:"domain"`
	Account   string    `json:"account"`
	Enabled   bool      `json:"enabled"`
	CatchAll  string    `json:"catch_all,omitempty"` // address that gets mail for unknown users
	Selector  string    `json:"dkim_selector"`
	DKIMKey   bool      `json:"dkim"`
	TLS       bool      `json:"tls"` // mail.<domain> has its own certificate
	Mailboxes []Mailbox `json:"mailboxes"`
	Aliases   []Alias   `json:"aliases"`
	CreatedAt time.Time `json:"created_at"`
	// MaxMailboxes and QuotaMB cap the domain (0 = unlimited); set by
	// hosting packages.
	MaxMailboxes int `json:"max_mailboxes,omitempty"`
}

// Mailbox is one email account.
type Mailbox struct {
	Local        string        `json:"local"` // the part before @
	Name         string        `json:"name,omitempty"`
	PasswordHash string        `json:"password_hash"`
	QuotaMB      int           `json:"quota_mb"` // 0 = unlimited
	Enabled      bool          `json:"enabled"`
	SendOnly     bool          `json:"send_only,omitempty"` // no IMAP/POP3, for apps that send mail
	AutoReply    *AutoReply    `json:"auto_reply,omitempty"`
	Forward      []string      `json:"forward,omitempty"` // also deliver copies here
	KeepCopy     bool          `json:"keep_copy"`         // keep a copy when forwarding
	CreatedAt    time.Time     `json:"created_at"`
	Usage        *MailboxUsage `json:"usage,omitempty"`
}

type MailboxUsage struct {
	UsedMB   float64   `json:"used_mb"`
	Messages int       `json:"messages"`
	At       time.Time `json:"at"`
}

// AutoReply is a vacation message (Sieve).
type AutoReply struct {
	Enabled bool   `json:"enabled"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	From    string `json:"from,omitempty"`  // YYYY-MM-DD
	Until   string `json:"until,omitempty"` // YYYY-MM-DD
	Days    int    `json:"days"`            // reply to the same sender at most once per N days
}

// Alias (forwarder) delivers mail for an address to other addresses.
type Alias struct {
	From string   `json:"from"` // local part, or "*" for catch-all (use Domain.CatchAll)
	To   []string `json:"to"`
}

// Settings are server-wide mail options.
type Settings struct {
	// Relay sends outgoing mail through a provider (port 25 is often blocked).
	Relay           Relay  `json:"relay"`
	MaxMessageMB    int    `json:"max_message_mb"`
	HourlySendLimit int    `json:"hourly_send_limit"` // per mailbox, against hijacked passwords
	SpamThreshold   int    `json:"spam_threshold"`    // Rspamd score to move to Junk
	RejectThreshold int    `json:"reject_threshold"`  // Rspamd score to reject
	Antivirus       bool   `json:"antivirus"`
	Greylisting     bool   `json:"greylisting"`
	WebmailPort     int    `json:"webmail_port"`
	DefaultQuotaMB  int    `json:"default_quota_mb"`
	DMARCPolicy     string `json:"dmarc_policy"` // none | quarantine | reject
}

type Relay struct {
	Provider string `json:"provider"` // direct | brevo | mailgun | ses | sendgrid | postmark | custom
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password,omitempty"` // sealed
}

type Manager struct {
	env  *kit.Env
	keys *secrets.Keyring
	mu   sync.Mutex
	set  kit.Store[Settings]
	// dovecot24 switches config syntax for Dovecot 2.4 (Ubuntu 26.04).
	dovecot24 bool
	vmailUID  int
	vmailGID  int
	// OwnerOf reports which account holds a website or DNS zone for a domain
	// ("" when nobody does). Set by the API server.
	OwnerOf func(domain string) string
	// Zone publishes mail DNS records when the domain's zone is here.
	Zone ZoneWriter
	// probe dials remote hosts (port 25 check); replaced in tests.
	probe func(ctx context.Context, addr string) error
	// lookup is used by deliverability checks; replaced in tests.
	lookup Lookup
}

// ZoneWriter is the part of the DNS manager mail needs.
type ZoneWriter interface {
	SetManagedTXT(ctx context.Context, tag, domain string, recs []DNSRecord) (bool, error)
}

// DNSRecord is a record mail wants published, relative to the domain.
type DNSRecord struct {
	Name, Type, Content string
	Priority            int
	Port, Weight        int
}

func New(env *kit.Env, keys *secrets.Keyring) *Manager {
	m := &Manager{env: env, keys: keys, vmailUID: 5000, vmailGID: 5000}
	m.set.Path = env.P(filepath.Join(env.Cfg.MailState, "settings.json"))
	m.probe = dialProbe
	m.lookup = netLookup{}
	if keys != nil {
		keys.Register("mail", m.reseal)
	}
	return m
}

// Detect reads the Dovecot version and the vmail user. Called at start-up.
func (m *Manager) Detect(ctx context.Context) {
	if !m.Installed() || m.env.Cfg.DryRun {
		return
	}
	if out, err := m.env.Run.Run(ctx, "dovecot", "--version"); err == nil {
		v := strings.TrimSpace(string(out))
		m.dovecot24 = strings.HasPrefix(v, "2.4") || strings.HasPrefix(v, "3.")
	}
	if uid, gid, err := m.env.IDs("vmail"); err == nil {
		m.vmailUID, m.vmailGID = uid, gid
	}
}

// Installed reports whether mail hosting was chosen at install time.
func (m *Manager) Installed() bool { return m.env.Cfg.MailServer == "postfix" }

func (m *Manager) need() error {
	if !m.Installed() {
		return fmt.Errorf("email hosting %w; run the installer again and choose mail", kit.ErrNotInstalled)
	}
	return nil
}

func (m *Manager) domainsDir() string { return m.env.P(filepath.Join(m.env.Cfg.MailState, "domains")) }
func (m *Manager) conf(name string) string {
	return m.env.P(filepath.Join(m.env.Cfg.MailConfDir, name))
}

// MaildirOf is the mailbox folder (absolute system path).
func (m *Manager) MaildirOf(domain, local string) string {
	return filepath.Join(m.env.Cfg.MailDir, domain, local)
}

var localRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9._+-]{0,62}[a-z0-9])?$`)
var addrRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,253}\.[A-Za-z]{2,}$`)

func validLocal(l string) error {
	if !localRe.MatchString(l) || strings.Contains(l, "..") {
		return fmt.Errorf("%q: use lowercase letters, digits, dots, dashes, plus or underscore", l)
	}
	return nil
}

func (m *Manager) load(domain string) (*Domain, error) {
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	var d Domain
	if err := kit.ReadJSON(filepath.Join(m.domainsDir(), domain+".json"), &d); err != nil {
		if errors.Is(err, kit.ErrNotFound) {
			return nil, fmt.Errorf("email domain %s %w", domain, kit.ErrNotFound)
		}
		return nil, err
	}
	return &d, nil
}

func (m *Manager) save(d *Domain) error {
	return kit.WriteJSON(filepath.Join(m.domainsDir(), d.Domain+".json"), d)
}

// AllDomains returns every email domain.
func (m *Manager) AllDomains() ([]Domain, error) {
	ents, err := os.ReadDir(m.domainsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []Domain{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Domain{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var d Domain
		if kit.ReadJSON(filepath.Join(m.domainsDir(), e.Name()), &d) == nil {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// PublicDomain hides password hashes.
type PublicDomain struct {
	Domain
	WebmailURL string `json:"webmail_url"`
	MailHost   string `json:"mail_host"`
}

func (m *Manager) public(d Domain) PublicDomain {
	for i := range d.Mailboxes {
		d.Mailboxes[i].PasswordHash = ""
	}
	set, _ := m.Settings()
	return PublicDomain{Domain: d, MailHost: "mail." + d.Domain,
		WebmailURL: fmt.Sprintf("https://mail.%s:%d/", d.Domain, set.WebmailPort)}
}

// Domains lists an account's email domains ("" = every domain, admin).
func (m *Manager) Domains(account string) ([]PublicDomain, error) {
	if account != "" {
		if err := m.env.CheckAccount(account); err != nil {
			return nil, err
		}
	}
	all, err := m.AllDomains()
	out := []PublicDomain{}
	for _, d := range all {
		if account == "" || d.Account == account {
			out = append(out, m.public(d))
		}
	}
	return out, err
}

func (m *Manager) get(account, domain string) (*Domain, error) {
	d, err := m.load(strings.ToLower(domain))
	if err != nil {
		return nil, err
	}
	if account != "" && d.Account != account {
		return nil, fmt.Errorf("email domain %s %w", domain, kit.ErrNotFound)
	}
	return d, nil
}

func (m *Manager) Domain(account, domain string) (*PublicDomain, error) {
	d, err := m.get(account, domain)
	if err != nil {
		return nil, err
	}
	p := m.public(*d)
	return &p, nil
}

// AddDomain turns on email for a domain.
func (m *Manager) AddDomain(ctx context.Context, account, domain string) (*PublicDomain, error) {
	if err := m.need(); err != nil {
		return nil, err
	}
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	if m.OwnerOf != nil {
		if o := m.OwnerOf(domain); o != "" && o != account {
			return nil, fmt.Errorf("%s belongs to another account", domain)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.load(domain); err == nil {
		return nil, fmt.Errorf("email domain %s %w", domain, kit.ErrExists)
	}
	d := &Domain{Domain: domain, Account: account, Enabled: true, Selector: "ike", Mailboxes: []Mailbox{}, Aliases: []Alias{}, CreatedAt: kit.Now()}
	if err := m.writeDKIMKey(ctx, d); err != nil {
		return nil, err
	}
	d.DKIMKey = true
	if err := m.save(d); err != nil {
		return nil, err
	}
	if err := m.apply(ctx); err != nil {
		_ = os.Remove(filepath.Join(m.domainsDir(), domain+".json"))
		_ = m.apply(ctx)
		return nil, err
	}
	m.env.Publish("mail.domain.created", domain, map[string]any{"account": account})
	// DNS: add MX, SPF, DKIM, DMARC when the zone lives here.
	if _, err := m.PublishDNS(ctx, account, domain); err != nil {
		m.env.Publish("mail.dns.warning", domain, map[string]any{"error": err.Error()})
	}
	p := m.public(*d)
	return &p, nil
}

// RemoveDomain turns email off. Mailboxes are kept on disk unless deleteMail.
func (m *Manager) RemoveDomain(ctx context.Context, account, domain string, deleteMail bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, err := m.get(account, domain)
	if err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(m.domainsDir(), d.Domain+".json")); err != nil {
		return err
	}
	if err := m.apply(ctx); err != nil {
		return err
	}
	_, _ = m.env.Run.Run(ctx, "rm", "-f", m.dkimKeyPath(d))
	if deleteMail {
		_, _ = m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", filepath.Join(m.env.Cfg.MailDir, d.Domain))
	}
	m.env.Publish("mail.domain.deleted", domain, map[string]any{"account": d.Account})
	return nil
}

// change edits a domain under the lock and rewrites the maps.
func (m *Manager) change(ctx context.Context, account, domain string, fn func(d *Domain) error) (*PublicDomain, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, err := m.get(account, domain)
	if err != nil {
		return nil, err
	}
	old, _ := m.load(d.Domain)
	if err := fn(d); err != nil {
		return nil, err
	}
	if err := m.save(d); err != nil {
		return nil, err
	}
	if err := m.apply(ctx); err != nil {
		_ = m.save(old)
		_ = m.apply(ctx)
		return nil, err
	}
	p := m.public(*d)
	return &p, nil
}

// DomainOptions are per-domain switches.
type DomainOptions struct {
	Enabled  bool   `json:"enabled"`
	CatchAll string `json:"catch_all"`
}

func (m *Manager) UpdateDomain(ctx context.Context, account, domain string, o DomainOptions) (*PublicDomain, error) {
	if o.CatchAll != "" && !addrRe.MatchString(o.CatchAll) {
		return nil, fmt.Errorf("catch-all must be an email address")
	}
	return m.change(ctx, account, domain, func(d *Domain) error {
		d.Enabled, d.CatchAll = o.Enabled, strings.ToLower(o.CatchAll)
		return nil
	})
}

// NewMailbox is a request to create a mailbox.
type NewMailbox struct {
	Local    string `json:"local"`
	Name     string `json:"name"`
	Password string `json:"password"` // empty: generate one and return it once
	QuotaMB  int    `json:"quota_mb"`
	SendOnly bool   `json:"send_only"`
}

type CreatedMailbox struct {
	Address  string `json:"address"`
	Password string `json:"password,omitempty"`
	IMAPHost string `json:"imap_host"`
	SMTPHost string `json:"smtp_host"`
}

var nameRe = regexp.MustCompile(`^[\p{L}\p{N} .,'()-]{0,80}$`)

func checkPassword(pw string) error {
	if len(pw) < 10 || len(pw) > 200 {
		return fmt.Errorf("password must be 10-200 characters")
	}
	if strings.ContainsAny(pw, "\n\r:") {
		return fmt.Errorf("password can't contain line breaks or colons")
	}
	return nil
}

func (m *Manager) CreateMailbox(ctx context.Context, account, domain string, req NewMailbox) (*CreatedMailbox, error) {
	if err := m.need(); err != nil {
		return nil, err
	}
	req.Local = strings.ToLower(strings.TrimSpace(req.Local))
	if err := validLocal(req.Local); err != nil {
		return nil, err
	}
	if !nameRe.MatchString(req.Name) {
		return nil, fmt.Errorf("name has unusual characters")
	}
	pw, generated := req.Password, false
	if pw == "" {
		pw, generated = kit.Password(16), true
	}
	if err := checkPassword(pw); err != nil {
		return nil, err
	}
	set, _ := m.Settings()
	if req.QuotaMB == 0 {
		req.QuotaMB = set.DefaultQuotaMB
	}
	if req.QuotaMB < 0 || req.QuotaMB > 1024*1024 {
		return nil, fmt.Errorf("quota must be 0 (unlimited) to 1048576 MB")
	}
	var dom string
	_, err := m.change(ctx, account, domain, func(d *Domain) error {
		dom = d.Domain
		if d.MaxMailboxes > 0 && len(d.Mailboxes) >= d.MaxMailboxes {
			return fmt.Errorf("your plan allows %d mailboxes on %s", d.MaxMailboxes, d.Domain)
		}
		for _, mb := range d.Mailboxes {
			if mb.Local == req.Local {
				return fmt.Errorf("%s@%s %w", req.Local, d.Domain, kit.ErrExists)
			}
		}
		for _, a := range d.Aliases {
			if a.From == req.Local {
				return fmt.Errorf("%s@%s is already a forwarder", req.Local, d.Domain)
			}
		}
		d.Mailboxes = append(d.Mailboxes, Mailbox{Local: req.Local, Name: req.Name, PasswordHash: HashPassword(pw),
			QuotaMB: req.QuotaMB, Enabled: true, SendOnly: req.SendOnly, KeepCopy: true, CreatedAt: kit.Now()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.env.Publish("mail.mailbox.created", req.Local+"@"+dom, map[string]any{"account": account})
	out := &CreatedMailbox{Address: req.Local + "@" + dom, IMAPHost: "mail." + dom, SMTPHost: "mail." + dom}
	if generated {
		out.Password = pw
	}
	return out, nil
}

var importedHashRe = regexp.MustCompile(`^\$(1|5|6)\$[./A-Za-z0-9$=]{8,200}$`)

// CreateMailboxHashed adds a mailbox that keeps the password it had on
// another server: a glibc crypt hash ($6$…, $5$… or $1$…) from a cPanel
// shadow file. Dovecot checks it with its CRYPT scheme, so the owner signs
// in with the old password.
func (m *Manager) CreateMailboxHashed(ctx context.Context, account, domain, local, hash string, quotaMB int) error {
	if err := m.need(); err != nil {
		return err
	}
	local = strings.ToLower(strings.TrimSpace(local))
	if err := validLocal(local); err != nil {
		return err
	}
	if !importedHashRe.MatchString(hash) {
		return fmt.Errorf("%s@%s: the password hash isn't a crypt hash Ike can use", local, domain)
	}
	if quotaMB < 0 || quotaMB > 1024*1024 {
		quotaMB = 0
	}
	var dom string
	_, err := m.change(ctx, account, domain, func(d *Domain) error {
		dom = d.Domain
		if d.hasMailbox(local) {
			return fmt.Errorf("%s@%s %w", local, d.Domain, kit.ErrExists)
		}
		d.Mailboxes = append(d.Mailboxes, Mailbox{Local: local, PasswordHash: "{CRYPT}" + hash, QuotaMB: quotaMB,
			Enabled: true, KeepCopy: true, CreatedAt: kit.Now()})
		return nil
	})
	if err == nil {
		m.env.Publish("mail.mailbox.created", local+"@"+dom, map[string]any{"account": account, "imported": true})
	}
	return err
}

// MailboxUpdate changes a mailbox. Empty password keeps the current one.
type MailboxUpdate struct {
	Name      string     `json:"name"`
	Password  string     `json:"password"`
	QuotaMB   int        `json:"quota_mb"`
	Enabled   bool       `json:"enabled"`
	SendOnly  bool       `json:"send_only"`
	Forward   []string   `json:"forward"`
	KeepCopy  bool       `json:"keep_copy"`
	AutoReply *AutoReply `json:"auto_reply"`
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func (m *Manager) UpdateMailbox(ctx context.Context, account, domain, local string, u MailboxUpdate) (*PublicDomain, error) {
	if u.Password != "" {
		if err := checkPassword(u.Password); err != nil {
			return nil, err
		}
	}
	if !nameRe.MatchString(u.Name) {
		return nil, fmt.Errorf("name has unusual characters")
	}
	if u.QuotaMB < 0 || u.QuotaMB > 1024*1024 {
		return nil, fmt.Errorf("quota must be 0 (unlimited) to 1048576 MB")
	}
	if len(u.Forward) > 10 {
		return nil, fmt.Errorf("forward to at most 10 addresses")
	}
	for i, f := range u.Forward {
		u.Forward[i] = strings.ToLower(strings.TrimSpace(f))
		if !addrRe.MatchString(u.Forward[i]) {
			return nil, fmt.Errorf("%q is not an email address", f)
		}
	}
	if a := u.AutoReply; a != nil {
		if len(a.Subject) > 200 || len(a.Body) > 8000 || strings.ContainsAny(a.Subject, "\r\n") {
			return nil, fmt.Errorf("auto-reply subject is one line up to 200 characters; body up to 8000")
		}
		if (a.From != "" && !dateRe.MatchString(a.From)) || (a.Until != "" && !dateRe.MatchString(a.Until)) {
			return nil, fmt.Errorf("dates look like 2026-12-24")
		}
		if a.Days == 0 {
			a.Days = 1
		}
		if a.Days < 1 || a.Days > 30 {
			return nil, fmt.Errorf("reply to the same sender every 1-30 days")
		}
	}
	return m.change(ctx, account, domain, func(d *Domain) error {
		for i := range d.Mailboxes {
			mb := &d.Mailboxes[i]
			if mb.Local != local {
				continue
			}
			mb.Name, mb.QuotaMB, mb.Enabled, mb.SendOnly = u.Name, u.QuotaMB, u.Enabled, u.SendOnly
			mb.Forward, mb.KeepCopy, mb.AutoReply = u.Forward, u.KeepCopy || len(u.Forward) == 0, u.AutoReply
			if u.Password != "" {
				mb.PasswordHash = HashPassword(u.Password)
			}
			return m.writeSieve(ctx, d.Domain, *mb)
		}
		return fmt.Errorf("mailbox %s@%s %w", local, d.Domain, kit.ErrNotFound)
	})
}

func (m *Manager) DeleteMailbox(ctx context.Context, account, domain, local string, deleteMail bool) error {
	var dom string
	_, err := m.change(ctx, account, domain, func(d *Domain) error {
		dom = d.Domain
		for i, mb := range d.Mailboxes {
			if mb.Local == local {
				d.Mailboxes = append(d.Mailboxes[:i], d.Mailboxes[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("mailbox %s@%s %w", local, d.Domain, kit.ErrNotFound)
	})
	if err != nil {
		return err
	}
	if deleteMail {
		_, _ = m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", m.MaildirOf(dom, local))
	}
	m.env.Publish("mail.mailbox.deleted", local+"@"+dom, map[string]any{"account": account})
	return nil
}

// SetAliases replaces a domain's forwarders.
func (m *Manager) SetAliases(ctx context.Context, account, domain string, aliases []Alias) (*PublicDomain, error) {
	if len(aliases) > 500 {
		return nil, fmt.Errorf("at most 500 forwarders per domain")
	}
	for i := range aliases {
		a := &aliases[i]
		a.From = strings.ToLower(strings.TrimSpace(a.From))
		if err := validLocal(a.From); err != nil {
			return nil, err
		}
		if len(a.To) == 0 || len(a.To) > 20 {
			return nil, fmt.Errorf("%s: forward to 1-20 addresses", a.From)
		}
		for j, t := range a.To {
			a.To[j] = strings.ToLower(strings.TrimSpace(t))
			if !addrRe.MatchString(a.To[j]) {
				return nil, fmt.Errorf("%q is not an email address", t)
			}
		}
	}
	return m.change(ctx, account, domain, func(d *Domain) error {
		seen := map[string]bool{}
		for _, mb := range d.Mailboxes {
			seen[mb.Local] = true
		}
		for _, a := range aliases {
			if seen[a.From] {
				return fmt.Errorf("%s@%s is a mailbox; use its forwarding settings instead", a.From, d.Domain)
			}
			seen[a.From] = true
		}
		d.Aliases = aliases
		return nil
	})
}

// VerifyLogin checks a mailbox password (used by the webmail single sign-on
// and the API's mailbox password change).
func (m *Manager) VerifyLogin(address, password string) bool {
	local, domain, ok := strings.Cut(strings.ToLower(address), "@")
	if !ok {
		return false
	}
	d, err := m.load(domain)
	if err != nil {
		return false
	}
	for _, mb := range d.Mailboxes {
		if mb.Local == local && mb.Enabled {
			return CheckPassword(mb.PasswordHash, password)
		}
	}
	return false
}

// ---- settings ----

func (m *Manager) Settings() (Settings, error) {
	s, err := m.set.Load()
	if s.MaxMessageMB == 0 {
		s.MaxMessageMB = 50
	}
	if s.HourlySendLimit == 0 {
		s.HourlySendLimit = 300
	}
	if s.SpamThreshold == 0 {
		s.SpamThreshold = 6
	}
	if s.RejectThreshold == 0 {
		s.RejectThreshold = 15
	}
	if s.WebmailPort == 0 {
		s.WebmailPort = 2096
	}
	if s.DefaultQuotaMB == 0 {
		s.DefaultQuotaMB = 2048
	}
	if s.DMARCPolicy == "" {
		s.DMARCPolicy = "quarantine"
	}
	if s.Relay.Provider == "" {
		s.Relay.Provider = "direct"
	}
	return s, err
}

// PublicSettings hides the relay password.
type PublicSettings struct {
	Settings
	HasRelayPassword bool   `json:"has_relay_password"`
	Installed        bool   `json:"installed"`
	Webmail          bool   `json:"webmail"`
	SpamFilter       bool   `json:"spam_filter"`
	Hostname         string `json:"hostname"`
}

func (m *Manager) PublicSettings() (PublicSettings, error) {
	s, err := m.Settings()
	p := PublicSettings{Settings: s, HasRelayPassword: s.Relay.Password != "", Installed: m.Installed(),
		Webmail: m.env.Cfg.Webmail != "", SpamFilter: m.env.Cfg.SpamFilter != "", Hostname: m.env.Cfg.Hostname}
	p.Relay.Password = ""
	return p, err
}

func (m *Manager) reseal(open, seal func(string) (string, error)) error {
	_, err := m.set.Update(func(s *Settings) error {
		v, err := open(s.Relay.Password)
		if err != nil {
			return err
		}
		s.Relay.Password, err = seal(v)
		return err
	})
	return err
}
