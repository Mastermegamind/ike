package dns

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// Zone is one domain's DNS.
type Zone struct {
	Name    string `json:"name"`
	Account string `json:"account"`
	Kind    string `json:"kind"`    // primary | secondary
	Backend string `json:"backend"` // local | cloudflare | digitalocean
	// ProviderID is the zone's ID at the provider.
	ProviderID string `json:"provider_id,omitempty"`

	// Secondary zones: the primary servers to transfer from.
	Primaries []string `json:"primaries,omitempty"`
	// Primary zones: secondaries to notify, and addresses allowed to transfer.
	AlsoNotify    []string `json:"also_notify,omitempty"`
	AllowTransfer []string `json:"allow_transfer,omitempty"`

	Nameservers []string `json:"nameservers"`
	TTL         int      `json:"ttl"`
	SOAEmail    string   `json:"soa_email"`
	Refresh     int      `json:"refresh"`
	Retry       int      `json:"retry"`
	Expire      int      `json:"expire"`
	Minimum     int      `json:"minimum"`
	Serial      uint32   `json:"serial"`

	DNSSEC bool     `json:"dnssec"`
	DS     []string `json:"ds,omitempty"` // give these to the registrar

	Records []Record `json:"records"`

	Registration *Registration `json:"registration,omitempty"`
	LastError    string        `json:"last_error,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
}

// Nameserver is one of the server admin's own name servers (ns1.example.com).
type Nameserver struct {
	Host string `json:"host"`
	IPv4 string `json:"ipv4,omitempty"`
	IPv6 string `json:"ipv6,omitempty"`
}

// Settings are the server admin's DNS choices.
type Settings struct {
	Nameservers []Nameserver `json:"nameservers"`
	SOAEmail    string       `json:"soa_email"`
	DefaultTTL  int          `json:"default_ttl"`
	// Secondaries get NOTIFY and may transfer every primary zone.
	Secondaries    []string `json:"secondaries"`
	DefaultBackend string   `json:"default_backend"` // local | cloudflare | digitalocean
	// Provider credentials, sealed with the master key.
	CloudflareToken   string     `json:"cloudflare_token,omitempty"`
	CloudflareAccount string     `json:"cloudflare_account,omitempty"`
	DigitalOceanToken string     `json:"digitalocean_token,omitempty"`
	Registrar         *Registrar `json:"registrar,omitempty"`
	// ExpiryReminders are the days before expiry to warn at.
	ExpiryReminders []int `json:"expiry_reminders"`
}

// PublicSettings hides credentials.
type PublicSettings struct {
	Settings
	CloudflareToken   string `json:"cloudflare_token"`
	DigitalOceanToken string `json:"digitalocean_token"`
	LocalServer       bool   `json:"local_server"`
	ServerIPv4        string `json:"server_ipv4"`
	ServerIPv6        string `json:"server_ipv6"`
}

type Manager struct {
	env  *kit.Env
	keys *secrets.Keyring
	mu   sync.Mutex
	set  kit.Store[Settings]
	// providers is replaced in tests.
	providers func(backend string) (Provider, error)
	// lookup is replaced in tests (propagation, ACME waits).
	resolver func(server string) Resolver
	// http fetches RDAP data and talks to the registrar; replaced in tests.
	http httpDoer
	// rdapPause spaces out registry lookups (politeness); 0 in tests.
	rdapPause time.Duration

	// WebsiteDomains lists the domains websites answer to (set by the API
	// server), so domains whose DNS is elsewhere get expiry dates too.
	WebsiteDomains func() []DomainRef
	regOnce        sync.Once
	regs           *kit.Store[map[string]*cachedReg]
	renewOnce      sync.Once
	renewals       *kit.Store[map[string]*Renewal]
	atRegistrar    *kit.Store[registrarState]
}

func New(env *kit.Env, keys *secrets.Keyring) *Manager {
	m := &Manager{env: env, keys: keys}
	m.set.Path = env.P(filepath.Join(env.Cfg.DNSDir, "settings.json"))
	m.providers = m.provider
	m.resolver = netResolver
	m.http = defaultHTTP()
	m.rdapPause = 2 * time.Second
	if keys != nil {
		keys.Register("dns", m.reseal)
	}
	return m
}

func (m *Manager) stateDir() string { return m.env.P(filepath.Join(m.env.Cfg.DNSDir, "state")) }
func (m *Manager) zoneFile(z string) string {
	return filepath.Join(m.env.Cfg.DNSDir, "zones", z+".zone")
}
func (m *Manager) secondaryFile(z string) string {
	return filepath.Join(m.env.Cfg.DNSDir, "secondary", z+".zone")
}

// Local reports whether this server runs its own DNS server.
func (m *Manager) Local() bool { return m.env.Cfg.DNSServer == "powerdns" }

func (m *Manager) Settings() (PublicSettings, error) {
	s, err := m.settings()
	ps := PublicSettings{Settings: s, LocalServer: m.Local(), ServerIPv4: m.env.Cfg.PublicIPv4, ServerIPv6: m.env.Cfg.PublicIPv6}
	ps.Registrar = nil
	if s.CloudflareToken != "" {
		ps.CloudflareToken = "set"
	}
	if s.DigitalOceanToken != "" {
		ps.DigitalOceanToken = "set"
	}
	return ps, err
}

func (m *Manager) settings() (Settings, error) {
	s, err := m.set.Load()
	if s.DefaultTTL == 0 {
		s.DefaultTTL = 3600
	}
	if s.DefaultBackend == "" {
		s.DefaultBackend = "local"
		if !m.Local() {
			s.DefaultBackend = ""
		}
	}
	if s.SOAEmail == "" {
		s.SOAEmail = m.env.Cfg.AdminEmail
		if s.SOAEmail == "" {
			s.SOAEmail = "hostmaster@" + strings.TrimPrefix(m.env.Cfg.Hostname, "www.")
		}
	}
	if len(s.ExpiryReminders) == 0 {
		s.ExpiryReminders = []int{30, 14, 7, 1}
	}
	return s, err
}

// SettingsUpdate is what the admin can change. Empty tokens keep the stored
// ones; "-" removes them.
type SettingsUpdate struct {
	Nameservers       []Nameserver `json:"nameservers"`
	SOAEmail          string       `json:"soa_email"`
	DefaultTTL        int          `json:"default_ttl"`
	Secondaries       []string     `json:"secondaries"`
	DefaultBackend    string       `json:"default_backend"`
	CloudflareToken   string       `json:"cloudflare_token"`
	CloudflareAccount string       `json:"cloudflare_account"`
	DigitalOceanToken string       `json:"digitalocean_token"`
	ExpiryReminders   []int        `json:"expiry_reminders"`
}

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]{1,64}@[A-Za-z0-9.-]{1,253}$`)
var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_.:-]{8,200}$`)

func (m *Manager) UpdateSettings(ctx context.Context, u SettingsUpdate) (PublicSettings, error) {
	if len(u.Nameservers) > 6 {
		return PublicSettings{}, fmt.Errorf("at most 6 name servers")
	}
	for i := range u.Nameservers {
		n := &u.Nameservers[i]
		n.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(n.Host), "."))
		if err := validate.Domain(n.Host); err != nil {
			return PublicSettings{}, fmt.Errorf("name server %q: %w", n.Host, err)
		}
		if n.IPv4 != "" && !isIP(n.IPv4, 4) {
			return PublicSettings{}, fmt.Errorf("%s: %q is not an IPv4 address", n.Host, n.IPv4)
		}
		if n.IPv6 != "" && !isIP(n.IPv6, 6) {
			return PublicSettings{}, fmt.Errorf("%s: %q is not an IPv6 address", n.Host, n.IPv6)
		}
	}
	if u.SOAEmail != "" && !emailRe.MatchString(u.SOAEmail) {
		return PublicSettings{}, fmt.Errorf("SOA email is not an email address")
	}
	if u.DefaultTTL != 0 && (u.DefaultTTL < 60 || u.DefaultTTL > 86400) {
		return PublicSettings{}, fmt.Errorf("default TTL must be 60-86400 seconds")
	}
	if err := ipList(u.Secondaries); err != nil {
		return PublicSettings{}, err
	}
	switch u.DefaultBackend {
	case "", "local", "cloudflare", "digitalocean":
	default:
		return PublicSettings{}, fmt.Errorf("backend must be local, cloudflare or digitalocean")
	}
	if u.DefaultBackend == "local" && !m.Local() {
		return PublicSettings{}, fmt.Errorf("this server has no DNS server; install PowerDNS or use a provider")
	}
	for _, t := range []string{u.CloudflareToken, u.DigitalOceanToken} {
		if t != "" && t != "-" && !tokenRe.MatchString(t) {
			return PublicSettings{}, fmt.Errorf("API token has unexpected characters")
		}
	}
	if u.CloudflareAccount != "" && !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(u.CloudflareAccount) {
		return PublicSettings{}, fmt.Errorf("Cloudflare account ID is 32 hex characters")
	}
	for _, d := range u.ExpiryReminders {
		if d < 1 || d > 120 {
			return PublicSettings{}, fmt.Errorf("reminders are 1-120 days before expiry")
		}
	}
	seal := func(cur *string, v string) error {
		switch v {
		case "":
			return nil
		case "-":
			*cur = ""
			return nil
		}
		s, err := m.keys.Seal(v)
		*cur = s
		return err
	}
	_, err := m.set.Update(func(s *Settings) error {
		s.Nameservers, s.SOAEmail, s.DefaultTTL, s.Secondaries = u.Nameservers, u.SOAEmail, u.DefaultTTL, u.Secondaries
		s.DefaultBackend, s.CloudflareAccount, s.ExpiryReminders = u.DefaultBackend, u.CloudflareAccount, u.ExpiryReminders
		if err := seal(&s.CloudflareToken, u.CloudflareToken); err != nil {
			return err
		}
		return seal(&s.DigitalOceanToken, u.DigitalOceanToken)
	})
	if err != nil {
		return PublicSettings{}, err
	}
	// Name server or secondary changes touch every local zone.
	if m.Local() {
		if err := m.republishAll(ctx); err != nil {
			slog.Warn("republishing zones after settings change", "err", err)
		}
	}
	return m.Settings()
}

func (m *Manager) reseal(open, seal func(string) (string, error)) error {
	_, err := m.set.Update(func(s *Settings) error {
		for _, p := range []*string{&s.CloudflareToken, &s.DigitalOceanToken} {
			v, err := open(*p)
			if err != nil {
				return err
			}
			if *p, err = seal(v); err != nil {
				return err
			}
		}
		if s.Registrar != nil {
			v, err := open(s.Registrar.APIKey)
			if err != nil {
				return err
			}
			if s.Registrar.APIKey, err = seal(v); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func isIP(s string, v int) bool {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	return (v == 4 && a.Is4()) || (v == 6 && a.Is6() && !a.Is4In6())
}

func ipList(l []string) error {
	if len(l) > 20 {
		return fmt.Errorf("at most 20 addresses")
	}
	for _, s := range l {
		if _, err := netip.ParseAddr(s); err != nil {
			if _, err := netip.ParsePrefix(s); err != nil {
				return fmt.Errorf("%q is not an IP address or range", s)
			}
		}
	}
	return nil
}

func (m *Manager) load(name string) (*Zone, error) {
	if err := validate.Domain(name); err != nil {
		return nil, err
	}
	var z Zone
	if err := kit.ReadJSON(filepath.Join(m.stateDir(), name+".json"), &z); err != nil {
		if errors.Is(err, kit.ErrNotFound) {
			return nil, fmt.Errorf("DNS zone %s %w", name, kit.ErrNotFound)
		}
		return nil, err
	}
	return &z, nil
}

func (m *Manager) save(z *Zone) error {
	z.UpdatedAt = kit.Now()
	return kit.WriteJSON(filepath.Join(m.stateDir(), z.Name+".json"), z)
}

// All returns every zone (admin view), sorted by name.
func (m *Manager) All() ([]Zone, error) {
	ents, err := os.ReadDir(m.stateDir())
	if errors.Is(err, os.ErrNotExist) {
		return []Zone{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Zone{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var z Zone
		if kit.ReadJSON(filepath.Join(m.stateDir(), e.Name()), &z) == nil {
			out = append(out, z)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// List returns one account's zones.
func (m *Manager) List(account string) ([]Zone, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	all, err := m.All()
	out := []Zone{}
	for _, z := range all {
		if z.Account == account {
			out = append(out, z)
		}
	}
	return out, err
}

// Get returns a zone that belongs to account ("" for the admin).
func (m *Manager) Get(account, name string) (*Zone, error) {
	z, err := m.load(strings.ToLower(name))
	if err != nil {
		return nil, err
	}
	if account != "" && z.Account != account {
		return nil, fmt.Errorf("DNS zone %s %w", name, kit.ErrNotFound)
	}
	return z, nil
}

// CreateZone is a request for a new zone.
type CreateZone struct {
	Name      string   `json:"name"`
	Backend   string   `json:"backend"`   // default: the admin's default
	Kind      string   `json:"kind"`      // primary (default) | secondary
	Primaries []string `json:"primaries"` // secondary zones
	Template  string   `json:"template"`  // website | website-email | google | microsoft | zoho | empty
	// IP the website records point at; default: this server.
	IPv4 string `json:"ipv4"`
	IPv6 string `json:"ipv6"`
}

func nextSerial(old uint32, now time.Time) uint32 {
	day, _ := strconv.ParseUint(now.Format("20060102")+"00", 10, 32)
	if uint32(day) > old {
		return uint32(day)
	}
	return old + 1
}

func (m *Manager) Create(ctx context.Context, account string, req CreateZone) (*Zone, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	name := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Name), "."))
	if err := validate.Domain(name); err != nil {
		return nil, err
	}
	set, err := m.settings()
	if err != nil {
		return nil, err
	}
	if req.Backend == "" {
		req.Backend = set.DefaultBackend
	}
	if req.Kind == "" {
		req.Kind = "primary"
	}
	switch req.Backend {
	case "local":
		if !m.Local() {
			return nil, fmt.Errorf("this server has no DNS server; choose Cloudflare or DigitalOcean")
		}
	case "cloudflare", "digitalocean":
		if req.Kind == "secondary" {
			return nil, fmt.Errorf("secondary zones are only possible on this server's own DNS server")
		}
	case "":
		return nil, fmt.Errorf("no DNS server is installed and no DNS provider is set up yet")
	default:
		return nil, fmt.Errorf("backend must be local, cloudflare or digitalocean")
	}
	if req.Kind != "primary" && req.Kind != "secondary" {
		return nil, fmt.Errorf("kind must be primary or secondary")
	}
	if req.Kind == "secondary" {
		if len(req.Primaries) == 0 {
			return nil, fmt.Errorf("a secondary zone needs the primary server's address")
		}
		for _, p := range req.Primaries {
			if !isIP(p, 4) && !isIP(p, 6) {
				return nil, fmt.Errorf("%q is not an IP address", p)
			}
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.load(name); err == nil {
		return nil, fmt.Errorf("DNS zone %s %w", name, kit.ErrExists)
	}
	// A subdomain of a zone someone else owns would let them hijack it.
	all, _ := m.All()
	for _, o := range all {
		if (strings.HasSuffix(name, "."+o.Name) || strings.HasSuffix(o.Name, "."+name)) && o.Account != account {
			return nil, fmt.Errorf("%s overlaps %s, which belongs to another account", name, o.Name)
		}
	}
	z := &Zone{
		Name: name, Account: account, Kind: req.Kind, Backend: req.Backend, Primaries: req.Primaries,
		TTL: set.DefaultTTL, SOAEmail: set.SOAEmail, Refresh: 10800, Retry: 3600, Expire: 1209600, Minimum: 300,
		Serial: nextSerial(0, kit.Now()), Records: []Record{}, CreatedAt: kit.Now(),
	}
	for _, n := range set.Nameservers {
		z.Nameservers = append(z.Nameservers, n.Host)
	}
	if len(z.Nameservers) == 0 && req.Backend == "local" {
		h := m.env.Cfg.Hostname
		if h == "" {
			h = "ns1." + name
		}
		z.Nameservers = []string{h}
	}
	if req.Kind == "primary" && req.Template != "" && req.Template != "empty" {
		recs, err := m.templateRecords(req.Template, name, req.IPv4, req.IPv6)
		if err != nil {
			return nil, err
		}
		z.Records = mergeTemplate(nil, recs, req.Template)
	}
	glue(z, set)
	if err := m.publish(ctx, z, nil); err != nil {
		return nil, err
	}
	if err := m.save(z); err != nil {
		return nil, err
	}
	m.env.Publish("dns.zone.created", name, map[string]any{"account": account, "backend": z.Backend})
	return z, nil
}

// glue adds A/AAAA records for the admin's own name servers that live inside
// this zone (ns1.example.com in example.com).
func glue(z *Zone, set Settings) {
	for _, n := range set.Nameservers {
		if !strings.HasSuffix(n.Host, "."+z.Name) {
			continue
		}
		label := strings.TrimSuffix(n.Host, "."+z.Name)
		for _, v := range []struct{ t, ip string }{{"A", n.IPv4}, {"AAAA", n.IPv6}} {
			if v.ip == "" {
				continue
			}
			r := Record{Name: label, Type: v.t, TTL: z.TTL, Content: v.ip, Managed: "nameserver"}
			z.Records = upsertManaged(z.Records, r)
		}
	}
}

// upsertManaged replaces the managed record with the same name, type and tag.
func upsertManaged(recs []Record, r Record) []Record {
	if r.ID == "" {
		r.ID = kit.RandomHex(6)
	}
	for i, o := range recs {
		if o.Managed == r.Managed && o.Name == r.Name && o.Type == r.Type {
			r.ID = o.ID
			recs[i] = r
			return recs
		}
	}
	return append(recs, r)
}

func (m *Manager) Delete(ctx context.Context, account, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, err := m.Get(account, name)
	if err != nil {
		return err
	}
	switch z.Backend {
	case "local":
		_ = os.Remove(filepath.Join(m.stateDir(), z.Name+".json"))
		if err := m.writeBindConf(); err != nil {
			return err
		}
		_, _ = m.env.Run.Run(ctx, "pdns_control", "rediscover")
		_ = os.Remove(m.env.P(m.zoneFile(z.Name)))
		_ = os.Remove(m.env.P(m.secondaryFile(z.Name)))
	default:
		p, err := m.providers(z.Backend)
		if err != nil {
			return err
		}
		if z.ProviderID != "" {
			if err := p.DeleteZone(ctx, z.ProviderID); err != nil {
				return fmt.Errorf("%s: %w", z.Backend, err)
			}
		}
		_ = os.Remove(filepath.Join(m.stateDir(), z.Name+".json"))
	}
	m.env.Publish("dns.zone.deleted", name, map[string]any{"account": z.Account})
	return nil
}

// change loads a zone, applies fn, publishes and saves it. On a failed
// publish nothing is saved and the previous zone is published again.
func (m *Manager) change(ctx context.Context, account, name string, fn func(z *Zone) error) (*Zone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	z, err := m.Get(account, name)
	if err != nil {
		return nil, err
	}
	if z.Kind == "secondary" {
		return nil, fmt.Errorf("%s is a secondary zone; change it on its primary server", name)
	}
	old := *z
	old.Records = append([]Record(nil), z.Records...)
	if err := fn(z); err != nil {
		return nil, err
	}
	if len(z.Records) > 2000 {
		return nil, fmt.Errorf("a zone can hold at most 2000 records")
	}
	if err := checkConflicts(z.Records); err != nil {
		return nil, err
	}
	z.Serial = nextSerial(z.Serial, kit.Now())
	if err := m.publish(ctx, z, &old); err != nil {
		z.LastError = err.Error()
		return nil, err
	}
	z.LastError = ""
	if err := m.save(z); err != nil {
		return nil, err
	}
	m.env.Publish("dns.zone.changed", z.Name, map[string]any{"account": z.Account, "serial": z.Serial})
	return z, nil
}

// checkConflicts enforces DNS rules the server would reject later: a CNAME
// can't share its name with other records.
func checkConflicts(recs []Record) error {
	cname := map[string]bool{}
	other := map[string]bool{}
	for _, r := range recs {
		if r.Type == "CNAME" {
			if cname[r.Name] || other[r.Name] {
				return fmt.Errorf("%s has a CNAME and other records; a CNAME must be the only record for its name", r.Name)
			}
			cname[r.Name] = true
		} else {
			if cname[r.Name] {
				return fmt.Errorf("%s has a CNAME and other records; a CNAME must be the only record for its name", r.Name)
			}
			other[r.Name] = true
		}
	}
	return nil
}

// AddRecord adds one record.
func (m *Manager) AddRecord(ctx context.Context, account, zone string, r Record) (*Zone, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	r.ID, r.Managed = kit.RandomHex(6), ""
	return m.change(ctx, account, zone, func(z *Zone) error {
		for _, o := range z.Records {
			if Same(o, r) {
				return fmt.Errorf("that record already exists")
			}
		}
		z.Records = append(z.Records, r)
		return nil
	})
}

// UpdateRecord replaces one record by ID.
func (m *Manager) UpdateRecord(ctx context.Context, account, zone, id string, r Record) (*Zone, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return m.change(ctx, account, zone, func(z *Zone) error {
		for i, o := range z.Records {
			if o.ID == id {
				r.ID, r.Managed = id, ""
				z.Records[i] = r
				return nil
			}
		}
		return fmt.Errorf("record %s %w", id, kit.ErrNotFound)
	})
}

func (m *Manager) DeleteRecord(ctx context.Context, account, zone, id string) (*Zone, error) {
	return m.change(ctx, account, zone, func(z *Zone) error {
		for i, o := range z.Records {
			if o.ID == id {
				z.Records = append(z.Records[:i], z.Records[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("record %s %w", id, kit.ErrNotFound)
	})
}

// ReplaceRecords sets the full record list (bulk edit, import).
func (m *Manager) ReplaceRecords(ctx context.Context, account, zone string, recs []Record) (*Zone, error) {
	for i := range recs {
		if err := recs[i].Validate(); err != nil {
			return nil, fmt.Errorf("record %d: %w", i+1, err)
		}
		if recs[i].ID == "" {
			recs[i].ID = kit.RandomHex(6)
		}
	}
	return m.change(ctx, account, zone, func(z *Zone) error {
		// keep Ike's own records (mail, DKIM, name server glue) unless replaced
		keep := []Record{}
		for _, o := range z.Records {
			if o.Managed != "" {
				found := false
				for _, n := range recs {
					if n.ID == o.ID {
						found = true
					}
				}
				if !found {
					keep = append(keep, o)
				}
			}
		}
		z.Records = append(keep, recs...)
		return nil
	})
}

// ZoneSettings are the per-zone options.
type ZoneSettings struct {
	TTL           int      `json:"ttl"`
	SOAEmail      string   `json:"soa_email"`
	Nameservers   []string `json:"nameservers"`
	AlsoNotify    []string `json:"also_notify"`
	AllowTransfer []string `json:"allow_transfer"`
	DNSSEC        bool     `json:"dnssec"`
}

func (m *Manager) UpdateZone(ctx context.Context, account, zone string, s ZoneSettings) (*Zone, error) {
	if s.TTL != 0 && (s.TTL < 60 || s.TTL > 86400) {
		return nil, fmt.Errorf("TTL must be 60-86400 seconds")
	}
	if s.SOAEmail != "" && !emailRe.MatchString(s.SOAEmail) {
		return nil, fmt.Errorf("SOA email is not an email address")
	}
	for i, n := range s.Nameservers {
		s.Nameservers[i] = strings.ToLower(strings.TrimSuffix(n, "."))
		if err := validate.Domain(s.Nameservers[i]); err != nil {
			return nil, fmt.Errorf("name server %q: %w", n, err)
		}
	}
	if err := ipList(s.AlsoNotify); err != nil {
		return nil, err
	}
	if err := ipList(s.AllowTransfer); err != nil {
		return nil, err
	}
	return m.change(ctx, account, zone, func(z *Zone) error {
		if s.DNSSEC && z.Backend != "local" {
			if z.Backend == "cloudflare" {
				return fmt.Errorf("turn on DNSSEC in the Cloudflare dashboard; Cloudflare signs the zone itself")
			}
			return fmt.Errorf("DigitalOcean DNS does not support DNSSEC")
		}
		if (len(s.AlsoNotify) > 0 || len(s.AllowTransfer) > 0) && z.Backend != "local" {
			return fmt.Errorf("zone transfers are only possible on this server's own DNS server")
		}
		if s.TTL != 0 {
			z.TTL = s.TTL
		}
		if s.SOAEmail != "" {
			z.SOAEmail = s.SOAEmail
		}
		if len(s.Nameservers) > 0 && z.Backend == "local" {
			z.Nameservers = s.Nameservers
		}
		z.AlsoNotify, z.AllowTransfer, z.DNSSEC = s.AlsoNotify, s.AllowTransfer, s.DNSSEC
		return nil
	})
}

// ApplyTemplate adds a template's records. Email templates replace the zone's
// MX records and SPF so mail goes to one place.
func (m *Manager) ApplyTemplate(ctx context.Context, account, zone, tmpl, ipv4, ipv6 string) (*Zone, error) {
	recs, err := m.templateRecords(tmpl, zone, ipv4, ipv6)
	if err != nil {
		return nil, err
	}
	return m.change(ctx, account, zone, func(z *Zone) error {
		z.Records = mergeTemplate(z.Records, recs, tmpl)
		return nil
	})
}

// Import adds records from a BIND zone file.
func (m *Manager) Import(ctx context.Context, account, zone, text string, replace bool) (*Zone, []string, error) {
	if len(text) > 2<<20 {
		return nil, nil, fmt.Errorf("zone file is larger than 2 MB")
	}
	recs, _, skipped, err := Parse(text, zone)
	if err != nil {
		return nil, nil, err
	}
	for i := range recs {
		recs[i].ID = kit.RandomHex(6)
	}
	z, err := m.change(ctx, account, zone, func(z *Zone) error {
		if replace {
			keep := []Record{}
			for _, o := range z.Records {
				if o.Managed != "" {
					keep = append(keep, o)
				}
			}
			z.Records = keep
		}
		for _, r := range recs {
			dup := false
			for _, o := range z.Records {
				if Same(o, r) {
					dup = true
				}
			}
			if !dup {
				z.Records = append(z.Records, r)
			}
		}
		return nil
	})
	return z, skipped, err
}

// Export returns the zone as a BIND file.
func (m *Manager) Export(account, zone string) (string, error) {
	z, err := m.Get(account, zone)
	if err != nil {
		return "", err
	}
	return Render(z), nil
}

// SetManaged adds or replaces records Ike owns under a tag (mail, dkim,
// acme) in whichever zone covers fqdn. It is a no-op when no zone does.
func (m *Manager) SetManaged(ctx context.Context, tag string, fqdn string, recs []Record) (bool, error) {
	z := m.ZoneFor(fqdn)
	if z == nil {
		return false, nil
	}
	for i := range recs {
		if err := recs[i].Validate(); err != nil {
			return false, err
		}
		recs[i].Managed = tag
	}
	_, err := m.change(ctx, "", z.Name, func(z *Zone) error {
		out := []Record{}
		for _, o := range z.Records {
			if o.Managed == tag {
				continue
			}
			out = append(out, o)
		}
		for _, r := range recs {
			r.ID = kit.RandomHex(6)
			// a user-made record with the same data replaces nothing; skip the duplicate
			dup := false
			for _, o := range out {
				if Same(o, r) {
					dup = true
				}
			}
			if !dup {
				out = append(out, r)
			}
		}
		z.Records = out
		return nil
	})
	return err == nil, err
}

// ZoneFor finds the most specific primary zone that contains fqdn.
func (m *Manager) ZoneFor(fqdn string) *Zone {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	all, _ := m.All()
	var best *Zone
	for i := range all {
		z := &all[i]
		if z.Kind != "primary" {
			continue
		}
		if fqdn == z.Name || strings.HasSuffix(fqdn, "."+z.Name) {
			if best == nil || len(z.Name) > len(best.Name) {
				best = z
			}
		}
	}
	return best
}

// RelativeTo returns fqdn as a name relative to zone ("@" for the apex).
func RelativeTo(fqdn, zone string) string {
	fqdn = strings.ToLower(strings.TrimSuffix(fqdn, "."))
	if fqdn == zone {
		return "@"
	}
	return strings.TrimSuffix(fqdn, "."+zone)
}

// ---- publishing ----

func (m *Manager) publish(ctx context.Context, z *Zone, old *Zone) error {
	switch z.Backend {
	case "local":
		return m.publishLocal(ctx, z, old)
	default:
		return m.publishProvider(ctx, z)
	}
}

func (m *Manager) republishAll(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	set, _ := m.settings()
	all, err := m.All()
	if err != nil {
		return err
	}
	var errs []error
	for i := range all {
		z := &all[i]
		if z.Backend != "local" || z.Kind != "primary" {
			continue
		}
		if len(set.Nameservers) > 0 {
			z.Nameservers = nil
			for _, n := range set.Nameservers {
				z.Nameservers = append(z.Nameservers, n.Host)
			}
		}
		glue(z, set)
		z.Serial = nextSerial(z.Serial, kit.Now())
		if err := m.publishLocal(ctx, z, nil); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", z.Name, err))
			continue
		}
		_ = m.save(z)
	}
	return errors.Join(errs...)
}

// writeBindConf lists every local zone for PowerDNS's bind backend.
func (m *Manager) writeBindConf() error {
	all, err := m.All()
	if err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("// Written by Ike. Zones are managed in the panel.\n")
	for _, z := range all {
		if z.Backend != "local" {
			continue
		}
		if z.Kind == "secondary" {
			fmt.Fprintf(&b, "zone \"%s\" {\n  type slave;\n  masters { %s; };\n  file \"%s\";\n};\n",
				z.Name, strings.Join(z.Primaries, "; "), m.secondaryFile(z.Name))
			continue
		}
		fmt.Fprintf(&b, "zone \"%s\" {\n  type master;\n  file \"%s\";\n};\n", z.Name, m.zoneFile(z.Name))
	}
	return kit.WriteFile(m.env.P(m.env.Cfg.PdnsBindConf), []byte(b.String()), 0o644)
}

func (m *Manager) chownPdns(path string) {
	if m.env.Cfg.DryRun {
		return
	}
	g, err := user.LookupGroup("pdns")
	if err != nil {
		return
	}
	gid, _ := strconv.Atoi(g.Gid)
	_ = os.Chown(path, 0, gid)
}

func (m *Manager) publishLocal(ctx context.Context, z *Zone, old *Zone) error {
	if !m.Local() {
		return fmt.Errorf("this server has no DNS server")
	}
	set, _ := m.settings()
	path := m.env.P(m.zoneFile(z.Name))
	prev, prevErr := os.ReadFile(path)
	// The zone must be in the state dir for writeBindConf to list it.
	isNew := false
	if _, err := os.Stat(filepath.Join(m.stateDir(), z.Name+".json")); errors.Is(err, os.ErrNotExist) {
		isNew = true
		if err := m.save(z); err != nil {
			return err
		}
	}
	undo := func() {
		if isNew {
			_ = os.Remove(filepath.Join(m.stateDir(), z.Name+".json"))
			_ = m.writeBindConf()
			_, _ = m.env.Run.Run(ctx, "pdns_control", "rediscover")
		}
		if prevErr == nil {
			_ = kit.WriteFile(path, prev, 0o640)
			m.chownPdns(path)
			_, _ = m.env.Run.Run(ctx, "pdns_control", "bind-reload-now", z.Name)
		} else if isNew {
			_ = os.Remove(path)
		}
	}
	if z.Kind == "primary" {
		if err := kit.WriteFile(path, []byte(Render(z)), 0o640); err != nil {
			undo()
			return err
		}
		m.chownPdns(path)
	} else {
		dir := m.env.P(filepath.Dir(m.secondaryFile(z.Name)))
		_ = os.MkdirAll(dir, 0o770)
		m.chownPdns(dir)
	}
	if err := m.writeBindConf(); err != nil {
		undo()
		return err
	}
	if isNew {
		if _, err := m.env.Run.Run(ctx, "pdns_control", "rediscover"); err != nil {
			undo()
			return fmt.Errorf("PowerDNS did not pick up the zone: %w", err)
		}
	}
	if z.Kind == "secondary" {
		_, _ = m.env.Run.Run(ctx, "pdns_control", "retrieve", z.Name)
		return nil
	}
	if _, err := m.env.Run.Run(ctx, "pdns_control", "bind-reload-now", z.Name); err != nil {
		undo()
		return fmt.Errorf("PowerDNS could not load the zone: %w", err)
	}
	if out, err := m.env.Run.Run(ctx, "pdnsutil", "check-zone", z.Name); err != nil {
		undo()
		return fmt.Errorf("the zone has errors, nothing was changed: %s", lastLines(string(out), 6))
	}
	// Transfers and notifications: zone settings plus the admin's secondaries.
	notify := uniq(append(append([]string{}, set.Secondaries...), z.AlsoNotify...))
	axfr := uniq(append(append([]string{}, set.Secondaries...), z.AllowTransfer...))
	if old == nil || !sameList(old.AlsoNotify, z.AlsoNotify) || isNew || len(set.Secondaries) > 0 {
		_, _ = m.env.Run.Run(ctx, "pdnsutil", append([]string{"set-meta", z.Name, "ALSO-NOTIFY"}, notify...)...)
		_, _ = m.env.Run.Run(ctx, "pdnsutil", append([]string{"set-meta", z.Name, "ALLOW-AXFR-FROM"}, axfr...)...)
	}
	if err := m.dnssec(ctx, z, old); err != nil {
		return err
	}
	if len(notify) > 0 {
		_, _ = m.env.Run.Run(ctx, "pdns_control", "notify", z.Name)
	}
	return nil
}

func (m *Manager) dnssec(ctx context.Context, z *Zone, old *Zone) error {
	was := old != nil && old.DNSSEC
	switch {
	case z.DNSSEC && !was:
		if _, err := m.env.Run.Run(ctx, "pdnsutil", "secure-zone", z.Name); err != nil {
			if !strings.Contains(err.Error(), "already") {
				return fmt.Errorf("could not sign the zone: %w", err)
			}
		}
		_, _ = m.env.Run.Run(ctx, "pdnsutil", "set-nsec3", z.Name, "1 0 0 -", "narrow")
	case !z.DNSSEC && was:
		if _, err := m.env.Run.Run(ctx, "pdnsutil", "disable-dnssec", z.Name); err != nil {
			return err
		}
		z.DS = nil
	}
	if z.DNSSEC {
		out, err := m.env.Run.Run(ctx, "pdnsutil", "show-zone", z.Name)
		if err == nil {
			z.DS = parseDS(string(out))
		}
	}
	return nil
}

var dsRe = regexp.MustCompile(`IN\s+DS\s+(\d+\s+\d+\s+2\s+[0-9a-fA-F]+)`)

// parseDS keeps the SHA-256 DS records from pdnsutil show-zone.
func parseDS(out string) []string {
	var ds []string
	for _, m := range dsRe.FindAllStringSubmatch(out, -1) {
		ds = append(ds, m[1])
	}
	return uniq(ds)
}

func uniq(l []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range l {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, "\n")
}

// RepublishAll writes every local zone again (after a server restore or a
// name server change).
func (m *Manager) RepublishAll(ctx context.Context) error {
	if !m.Local() {
		return nil
	}
	if err := m.writeBindConf(); err != nil {
		return err
	}
	_, _ = m.env.Run.Run(ctx, "pdns_control", "rediscover")
	return m.republishAll(ctx)
}
