package dns

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// Registration is what the domain registry says about a zone's domain.
type Registration struct {
	Registrar    string    `json:"registrar,omitempty"`
	Expires      time.Time `json:"expires,omitempty"`
	Status       []string  `json:"status,omitempty"`
	Nameservers  []string  `json:"nameservers,omitempty"`
	CheckedAt    time.Time `json:"checked_at"`
	Error        string    `json:"error,omitempty"`
	LastReminder int       `json:"last_reminder,omitempty"` // days-left value last warned about
	// AtRegistrar is true when the domain is in the connected registrar account.
	AtRegistrar bool `json:"at_registrar,omitempty"`
	AutoRenew   bool `json:"auto_renew,omitempty"`
}

// ---- RDAP (expiry, registrar, name servers) ----

var rdapBase = "https://rdap.org/domain/"

// LookupRegistration asks RDAP about a domain.
func (m *Manager) LookupRegistration(ctx context.Context, domain string) (*Registration, error) {
	reg := &Registration{CheckedAt: kit.Now()}
	req, _ := http.NewRequestWithContext(ctx, "GET", rdapBase+url.PathEscape(registrable(domain)), nil)
	req.Header.Set("Accept", "application/rdap+json")
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("the registry has no record of %s (not registered, or its registry has no RDAP service)", domain)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("RDAP lookup failed: %s", resp.Status)
	}
	var d struct {
		Events []struct {
			Action string `json:"eventAction"`
			Date   string `json:"eventDate"`
		} `json:"events"`
		Status      []string `json:"status"`
		Nameservers []struct {
			LDH string `json:"ldhName"`
		} `json:"nameservers"`
		Entities []struct {
			Roles []string        `json:"roles"`
			VCard json.RawMessage `json:"vcardArray"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&d); err != nil {
		return nil, fmt.Errorf("RDAP answer unreadable: %w", err)
	}
	for _, e := range d.Events {
		if e.Action == "expiration" {
			if t, err := time.Parse(time.RFC3339, e.Date); err == nil {
				reg.Expires = t.UTC()
			}
		}
	}
	reg.Status = d.Status
	for _, n := range d.Nameservers {
		reg.Nameservers = append(reg.Nameservers, strings.ToLower(strings.TrimSuffix(n.LDH, ".")))
	}
	for _, e := range d.Entities {
		for _, r := range e.Roles {
			if r == "registrar" {
				reg.Registrar = vcardFN(e.VCard)
			}
		}
	}
	return reg, nil
}

// vcardFN pulls the "fn" value out of a jCard.
func vcardFN(raw json.RawMessage) string {
	var v []any
	if json.Unmarshal(raw, &v) != nil || len(v) < 2 {
		return ""
	}
	props, _ := v[1].([]any)
	for _, p := range props {
		f, _ := p.([]any)
		if len(f) >= 4 && f[0] == "fn" {
			s, _ := f[3].(string)
			return s
		}
	}
	return ""
}

// twoLevel lists common second-level public suffixes.
var twoLevel = map[string]bool{
	"co.uk": true, "org.uk": true, "me.uk": true, "com.ng": true, "org.ng": true, "net.ng": true, "edu.ng": true,
	"gov.ng": true, "name.ng": true, "com.au": true, "net.au": true, "org.au": true, "co.za": true, "org.za": true,
	"com.br": true, "co.nz": true, "co.in": true, "com.gh": true, "co.ke": true, "com.mx": true, "co.jp": true,
	"com.sg": true, "com.cn": true, "com.tr": true, "co.il": true, "com.ar": true,
}

// registrable returns the registered domain: shop.example.co.uk → example.co.uk.
func registrable(d string) string {
	l := strings.Split(strings.ToLower(strings.TrimSuffix(d, ".")), ".")
	if len(l) <= 2 {
		return strings.Join(l, ".")
	}
	if twoLevel[strings.Join(l[len(l)-2:], ".")] {
		return strings.Join(l[len(l)-3:], ".")
	}
	return strings.Join(l[len(l)-2:], ".")
}

func splitSLD(d string) (sld, tld string) {
	r := registrable(d)
	i := strings.Index(r, ".")
	return r[:i], r[i+1:]
}

// RefreshRegistration updates one zone's registration data now.
func (m *Manager) RefreshRegistration(ctx context.Context, account, zone string) (*Zone, error) {
	z, err := m.Get(account, zone)
	if err != nil {
		return nil, err
	}
	if registrable(z.Name) != z.Name {
		return nil, fmt.Errorf("%s is a subdomain; registration belongs to %s", z.Name, registrable(z.Name))
	}
	reg, err := m.LookupRegistration(ctx, z.Name)
	m.mu.Lock()
	defer m.mu.Unlock()
	z, lerr := m.load(z.Name)
	if lerr != nil {
		return nil, lerr
	}
	if err != nil {
		if z.Registration == nil {
			z.Registration = &Registration{}
		}
		z.Registration.Error, z.Registration.CheckedAt = err.Error(), kit.Now()
	} else {
		if z.Registration != nil {
			reg.LastReminder, reg.AtRegistrar, reg.AutoRenew = z.Registration.LastReminder, z.Registration.AtRegistrar, z.Registration.AutoRenew
		}
		z.Registration = reg
	}
	return z, m.save(z)
}

// RunExpiryChecks refreshes registration data daily and publishes
// dns.domain.expiring events at the configured reminder days.
func (m *Manager) RunExpiryChecks(ctx context.Context) {
	t := time.NewTimer(2 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.expiryPass(ctx)
		t.Reset(24 * time.Hour)
	}
}

func (m *Manager) expiryPass(ctx context.Context) {
	set, _ := m.settings()
	all, _ := m.All()
	for _, z := range all {
		if registrable(z.Name) != z.Name {
			continue
		}
		nz, err := m.RefreshRegistration(ctx, "", z.Name)
		if err != nil || nz.Registration == nil || nz.Registration.Expires.IsZero() {
			continue
		}
		left := int(time.Until(nz.Registration.Expires).Hours() / 24)
		if d := dueReminder(set.ExpiryReminders, left, nz.Registration.LastReminder); d != 0 {
			m.env.Publish("dns.domain.expiring", z.Name, map[string]any{
				"account": z.Account, "days_left": left, "expires": nz.Registration.Expires, "registrar": nz.Registration.Registrar})
			m.mu.Lock()
			if fz, err := m.load(z.Name); err == nil && fz.Registration != nil {
				fz.Registration.LastReminder = d
				_ = m.save(fz)
			}
			m.mu.Unlock()
		}
		if left > 120 && nz.Registration.LastReminder != 0 { // renewed
			m.mu.Lock()
			if fz, err := m.load(z.Name); err == nil && fz.Registration != nil {
				fz.Registration.LastReminder = 0
				_ = m.save(fz)
			}
			m.mu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.rdapPause): // be polite to RDAP servers
		}
	}
	m.websiteExpiryPass(ctx, set.ExpiryReminders)
	slog.Info("domain expiry check finished", "zones", len(all))
}

// ---- Namecheap registrar ----

// Registrar is the connected registrar account.
type Registrar struct {
	Kind     string  `json:"kind"` // namecheap
	APIUser  string  `json:"api_user"`
	APIKey   string  `json:"api_key"` // sealed
	Username string  `json:"username"`
	ClientIP string  `json:"client_ip"` // must be allow-listed at Namecheap
	Sandbox  bool    `json:"sandbox"`
	Contact  Contact `json:"contact"`
}

// Contact is used for registrant, admin, tech and billing.
type Contact struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Organization string `json:"organization,omitempty"`
	Address1     string `json:"address1"`
	City         string `json:"city"`
	State        string `json:"state"`
	PostalCode   string `json:"postal_code"`
	Country      string `json:"country"` // ISO 2-letter
	Phone        string `json:"phone"`   // +234.8031234567
	Email        string `json:"email"`
}

type PublicRegistrar struct {
	Kind     string  `json:"kind"`
	APIUser  string  `json:"api_user"`
	APIKey   string  `json:"api_key"` // "set" or ""
	Username string  `json:"username"`
	ClientIP string  `json:"client_ip"`
	Sandbox  bool    `json:"sandbox"`
	Contact  Contact `json:"contact"`
}

var (
	ncUserRe  = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,40}$`)
	phoneRe   = regexp.MustCompile(`^\+[0-9]{1,3}\.[0-9]{4,14}$`)
	countryRe = regexp.MustCompile(`^[A-Z]{2}$`)
	plainRe   = regexp.MustCompile(`^[\p{L}\p{N} .,'#/&()-]{0,120}$`)
)

func (c Contact) validate() error {
	for _, f := range []struct{ n, v string }{{"first name", c.FirstName}, {"last name", c.LastName}, {"address", c.Address1},
		{"city", c.City}, {"state", c.State}, {"postal code", c.PostalCode}} {
		if f.v == "" || !plainRe.MatchString(f.v) {
			return fmt.Errorf("contact %s is missing or has unusual characters", f.n)
		}
	}
	if !plainRe.MatchString(c.Organization) {
		return fmt.Errorf("contact organization has unusual characters")
	}
	if !countryRe.MatchString(c.Country) {
		return fmt.Errorf("country is a 2-letter code like NG")
	}
	if !phoneRe.MatchString(c.Phone) {
		return fmt.Errorf("phone looks like +234.8031234567")
	}
	if !emailRe.MatchString(c.Email) {
		return fmt.Errorf("contact email is not valid")
	}
	return nil
}

func (m *Manager) RegistrarSettings() (*PublicRegistrar, error) {
	s, err := m.settings()
	if err != nil || s.Registrar == nil {
		return nil, err
	}
	r := s.Registrar
	p := &PublicRegistrar{Kind: r.Kind, APIUser: r.APIUser, Username: r.Username, ClientIP: r.ClientIP, Sandbox: r.Sandbox, Contact: r.Contact}
	if r.APIKey != "" {
		p.APIKey = "set"
	}
	if p.ClientIP == "" {
		p.ClientIP = m.env.Cfg.PublicIPv4
	}
	return p, nil
}

func (m *Manager) SetRegistrar(r Registrar) (*PublicRegistrar, error) {
	if r.Kind == "" {
		_, err := m.set.Update(func(s *Settings) error { s.Registrar = nil; return nil })
		return nil, err
	}
	if r.Kind != "namecheap" {
		return nil, fmt.Errorf("Namecheap is the supported registrar")
	}
	if !ncUserRe.MatchString(r.APIUser) || !ncUserRe.MatchString(r.Username) {
		return nil, fmt.Errorf("API user and username are your Namecheap username")
	}
	if r.ClientIP == "" {
		r.ClientIP = m.env.Cfg.PublicIPv4
	}
	if !isIP(r.ClientIP, 4) {
		return nil, fmt.Errorf("client IP must be this server's IPv4 address, allow-listed in Namecheap's API settings")
	}
	if err := r.Contact.validate(); err != nil {
		return nil, err
	}
	_, err := m.set.Update(func(s *Settings) error {
		if r.APIKey == "" && s.Registrar != nil {
			r.APIKey = s.Registrar.APIKey
		} else if r.APIKey != "" {
			if !regexp.MustCompile(`^[A-Za-z0-9]{16,64}$`).MatchString(r.APIKey) {
				return fmt.Errorf("API key is 16-64 letters and digits")
			}
			sealed, err := m.keys.Seal(r.APIKey)
			if err != nil {
				return err
			}
			r.APIKey = sealed
		}
		if r.APIKey == "" {
			return fmt.Errorf("enter the Namecheap API key")
		}
		s.Registrar = &r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return m.RegistrarSettings()
}

type ncResponse struct {
	Status string `xml:"Status,attr"`
	Errors []struct {
		Number string `xml:"Number,attr"`
		Text   string `xml:",chardata"`
	} `xml:"Errors>Error"`
	Command struct {
		Inner []byte `xml:",innerxml"`
	} `xml:"CommandResponse"`
}

func (m *Manager) namecheap(ctx context.Context, command string, params url.Values, out any) error {
	s, err := m.settings()
	if err != nil {
		return err
	}
	r := s.Registrar
	if r == nil {
		return fmt.Errorf("connect a registrar account in DNS settings first")
	}
	key, err := m.keys.Unseal(r.APIKey)
	if err != nil {
		return err
	}
	base := "https://api.namecheap.com/xml.response"
	if r.Sandbox {
		base = "https://api.sandbox.namecheap.com/xml.response"
	}
	if params == nil {
		params = url.Values{}
	}
	params.Set("ApiUser", r.APIUser)
	params.Set("ApiKey", key)
	params.Set("UserName", r.Username)
	params.Set("ClientIp", r.ClientIP)
	params.Set("Command", command)
	req, _ := http.NewRequestWithContext(ctx, "POST", base, strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("Namecheap: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var nr ncResponse
	if err := xml.Unmarshal(b, &nr); err != nil {
		return fmt.Errorf("Namecheap answer unreadable (%s)", resp.Status)
	}
	if nr.Status != "OK" {
		msg := "request failed"
		if len(nr.Errors) > 0 {
			msg = strings.TrimSpace(nr.Errors[0].Text)
			if nr.Errors[0].Number == "1011150" || strings.Contains(msg, "IP") {
				msg += " — allow-list " + r.ClientIP + " under Profile → Tools → API Access at Namecheap"
			}
		}
		return fmt.Errorf("Namecheap: %s", msg)
	}
	if out != nil {
		return xml.Unmarshal(append(append([]byte("<r>"), nr.Command.Inner...), []byte("</r>")...), out)
	}
	return nil
}

// Availability is one result of a domain search.
type Availability struct {
	Domain    string `json:"domain"`
	Available bool   `json:"available"`
	Premium   bool   `json:"premium"`
	Price     string `json:"price,omitempty"`
}

func (m *Manager) CheckAvailability(ctx context.Context, domains []string) ([]Availability, error) {
	if len(domains) == 0 || len(domains) > 20 {
		return nil, fmt.Errorf("check 1-20 domains at a time")
	}
	for i, d := range domains {
		domains[i] = strings.ToLower(strings.TrimSpace(d))
		if err := validate.Domain(domains[i]); err != nil {
			return nil, err
		}
	}
	var res struct {
		Results []struct {
			Domain    string `xml:"Domain,attr"`
			Available string `xml:"Available,attr"`
			Premium   string `xml:"IsPremiumName,attr"`
			Price     string `xml:"PremiumRegistrationPrice,attr"`
		} `xml:"DomainCheckResult"`
	}
	if err := m.namecheap(ctx, "namecheap.domains.check", url.Values{"DomainList": {strings.Join(domains, ",")}}, &res); err != nil {
		return nil, err
	}
	out := []Availability{}
	for _, r := range res.Results {
		a := Availability{Domain: r.Domain, Available: r.Available == "true", Premium: r.Premium == "true"}
		if a.Premium {
			a.Price = r.Price
		}
		out = append(out, a)
	}
	return out, nil
}

// RegisterRequest buys a domain through the registrar.
type RegisterRequest struct {
	Domain  string `json:"domain"`
	Years   int    `json:"years"`
	Account string `json:"account"` // create the DNS zone for this account too
	// UseOwnNameservers points the domain at this server's (or the
	// provider's) name servers.
	UseOwnNameservers bool `json:"use_own_nameservers"`
}

func (m *Manager) Register(ctx context.Context, req RegisterRequest) (map[string]any, error) {
	if err := validate.Domain(req.Domain); err != nil {
		return nil, err
	}
	if req.Years < 1 || req.Years > 10 {
		return nil, fmt.Errorf("register for 1-10 years")
	}
	s, err := m.settings()
	if err != nil || s.Registrar == nil {
		return nil, fmt.Errorf("connect a registrar account in DNS settings first")
	}
	c := s.Registrar.Contact
	p := url.Values{"DomainName": {req.Domain}, "Years": {strconv.Itoa(req.Years)}, "AddFreeWhoisguard": {"yes"}, "WGEnabled": {"yes"}}
	for _, role := range []string{"Registrant", "Tech", "Admin", "AuxBilling"} {
		p.Set(role+"FirstName", c.FirstName)
		p.Set(role+"LastName", c.LastName)
		if c.Organization != "" {
			p.Set(role+"OrganizationName", c.Organization)
		}
		p.Set(role+"Address1", c.Address1)
		p.Set(role+"City", c.City)
		p.Set(role+"StateProvince", c.State)
		p.Set(role+"PostalCode", c.PostalCode)
		p.Set(role+"Country", c.Country)
		p.Set(role+"Phone", c.Phone)
		p.Set(role+"EmailAddress", c.Email)
	}
	var ns []string
	if req.UseOwnNameservers {
		for _, n := range s.Nameservers {
			ns = append(ns, n.Host)
		}
		if len(ns) >= 2 {
			p.Set("Nameservers", strings.Join(ns, ","))
		}
	}
	var res struct {
		R struct {
			Domain     string `xml:"Domain,attr"`
			Registered string `xml:"Registered,attr"`
			Charged    string `xml:"ChargedAmount,attr"`
		} `xml:"DomainCreateResult"`
	}
	if err := m.namecheap(ctx, "namecheap.domains.create", p, &res); err != nil {
		return nil, err
	}
	if res.R.Registered != "true" {
		return nil, fmt.Errorf("Namecheap did not register %s", req.Domain)
	}
	m.env.Publish("dns.domain.registered", req.Domain, map[string]any{"years": req.Years, "charged": res.R.Charged, "account": req.Account})
	out := map[string]any{"domain": req.Domain, "registered": true, "charged": res.R.Charged}
	if req.Account != "" {
		if z, err := m.Create(ctx, req.Account, CreateZone{Name: req.Domain, Template: "website"}); err == nil {
			out["zone"] = z
			if !req.UseOwnNameservers || len(ns) < 2 {
				if err := m.SetRegistrarNameservers(ctx, req.Domain, z.Nameservers); err != nil {
					out["nameserver_error"] = err.Error()
				}
			}
		} else {
			out["zone_error"] = err.Error()
		}
	}
	return out, nil
}

func (m *Manager) Renew(ctx context.Context, domain string, years int) (map[string]any, error) {
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	if years < 1 || years > 10 {
		return nil, fmt.Errorf("renew for 1-10 years")
	}
	var res struct {
		R struct {
			Renew   string `xml:"Renew,attr"`
			Charged string `xml:"ChargedAmount,attr"`
		} `xml:"DomainRenewResult"`
	}
	if err := m.namecheap(ctx, "namecheap.domains.renew", url.Values{"DomainName": {domain}, "Years": {strconv.Itoa(years)}}, &res); err != nil {
		return nil, err
	}
	m.env.Publish("dns.domain.renewed", domain, map[string]any{"years": years, "charged": res.R.Charged})
	return map[string]any{"domain": domain, "renewed": res.R.Renew == "true", "charged": res.R.Charged}, nil
}

// SetRegistrarNameservers points a domain at name servers.
func (m *Manager) SetRegistrarNameservers(ctx context.Context, domain string, ns []string) error {
	if len(ns) < 2 {
		return fmt.Errorf("registrars need at least two name servers")
	}
	for _, n := range ns {
		if err := validate.Domain(strings.TrimSuffix(n, ".")); err != nil {
			return err
		}
	}
	sld, tld := splitSLD(domain)
	return m.namecheap(ctx, "namecheap.domains.dns.setCustom", url.Values{"SLD": {sld}, "TLD": {tld}, "Nameservers": {strings.Join(ns, ",")}}, nil)
}

// CreateGlue registers a child name server (ns1.example.com → IP) at the
// registrar, needed when a domain uses name servers inside itself.
func (m *Manager) CreateGlue(ctx context.Context, host, ip string) error {
	if err := validate.Domain(host); err != nil {
		return err
	}
	if !isIP(ip, 4) {
		return fmt.Errorf("glue needs an IPv4 address")
	}
	sld, tld := splitSLD(host)
	return m.namecheap(ctx, "namecheap.domains.ns.create", url.Values{"SLD": {sld}, "TLD": {tld}, "Nameserver": {host}, "IP": {ip}}, nil)
}

// RegistrarDomain is one domain in the registrar account.
type RegistrarDomain struct {
	Name      string `json:"name"`
	Expires   string `json:"expires"`
	AutoRenew bool   `json:"auto_renew"`
	Expired   bool   `json:"expired"`
	Locked    bool   `json:"locked"`
}

func (m *Manager) RegistrarDomains(ctx context.Context) ([]RegistrarDomain, error) {
	var res struct {
		Domains []struct {
			Name      string `xml:"Name,attr"`
			Expires   string `xml:"Expires,attr"`
			AutoRenew string `xml:"AutoRenew,attr"`
			Expired   string `xml:"IsExpired,attr"`
			Locked    string `xml:"IsLocked,attr"`
		} `xml:"DomainGetListResult>Domain"`
	}
	if err := m.namecheap(ctx, "namecheap.domains.getList", url.Values{"PageSize": {"100"}}, &res); err != nil {
		return nil, err
	}
	out := []RegistrarDomain{}
	for _, d := range res.Domains {
		out = append(out, RegistrarDomain{Name: d.Name, Expires: d.Expires, AutoRenew: d.AutoRenew == "true", Expired: d.Expired == "true", Locked: d.Locked == "true"})
	}
	return out, nil
}
