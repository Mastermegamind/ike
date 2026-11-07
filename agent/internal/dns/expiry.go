package dns

// Domain expiry for every domain the server hosts: DNS zones keep their
// registration data in the zone, and websites whose DNS lives elsewhere get
// theirs from RDAP too, cached here and refreshed daily.

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// DomainRef is a domain a website answers to.
type DomainRef struct {
	Domain  string
	Account string
}

// DomainExpiry is when one registered domain runs out.
type DomainExpiry struct {
	Domain      string    `json:"domain"` // the registered domain (shop.com.ng, not www.shop.com.ng)
	Account     string    `json:"account"`
	Source      string    `json:"source"` // zone (DNS hosted here) | website (DNS elsewhere)
	Registrar   string    `json:"registrar,omitempty"`
	Expires     time.Time `json:"expires,omitzero"` // zero: not known
	DaysLeft    int       `json:"days_left"`
	AutoRenew   bool      `json:"auto_renew,omitempty"`
	AtRegistrar bool      `json:"at_registrar,omitempty"`
	CheckedAt   time.Time `json:"checked_at,omitzero"`
	Error       string    `json:"error,omitempty"`
	// RenewDaysBefore is when Ike renews it (with AutoRenew); RenewError is
	// why the last automatic renewal failed.
	RenewDaysBefore int    `json:"renew_days_before,omitempty"`
	RenewError      string `json:"renew_error,omitempty"`
}

type cachedReg struct {
	Registration
	Account string `json:"account"`
}

func (m *Manager) regCache() *kit.Store[map[string]*cachedReg] {
	m.regOnce.Do(func() {
		m.regs = &kit.Store[map[string]*cachedReg]{Path: m.env.P(filepath.Join(m.env.Cfg.DNSDir, "registrations.json"))}
	})
	return m.regs
}

// unregistrable are suffixes nobody registers (test and local names).
var unregistrable = []string{".test", ".local", ".localhost", ".internal", ".example", ".invalid", ".lan", ".home.arpa"}

// websiteOnly lists the registered domains of websites whose DNS zone isn't
// on this server, with the account of the first website seen.
func (m *Manager) websiteOnly() []DomainRef {
	if m.WebsiteDomains == nil {
		return nil
	}
	zones := map[string]bool{}
	if all, err := m.All(); err == nil {
		for _, z := range all {
			zones[registrable(z.Name)] = true
		}
	}
	seen := map[string]bool{}
	var out []DomainRef
	for _, d := range m.WebsiteDomains() {
		name := strings.TrimPrefix(strings.ToLower(d.Domain), "*.")
		if validate.Domain(name) != nil {
			continue
		}
		skip := false
		for _, s := range unregistrable {
			if strings.HasSuffix(name, s) {
				skip = true
			}
		}
		r := registrable(name)
		if skip || zones[r] || seen[r] || !strings.Contains(r, ".") {
			continue
		}
		seen[r] = true
		out = append(out, DomainRef{Domain: r, Account: d.Account})
	}
	return out
}

func daysLeft(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return int(time.Until(t).Hours() / 24)
}

// DomainExpiries lists every registered domain the server hosts, soonest first.
func (m *Manager) DomainExpiries() ([]DomainExpiry, error) {
	out := []DomainExpiry{}
	all, err := m.All()
	if err != nil {
		return nil, err
	}
	for _, z := range all {
		if registrable(z.Name) != z.Name || z.Kind == "secondary" {
			continue
		}
		e := DomainExpiry{Domain: z.Name, Account: z.Account, Source: "zone"}
		if r := z.Registration; r != nil {
			e.Registrar, e.Expires, e.AutoRenew, e.AtRegistrar, e.CheckedAt, e.Error = r.Registrar, r.Expires, r.AutoRenew, r.AtRegistrar, r.CheckedAt, r.Error
		} else {
			e.Error = "Not checked yet."
		}
		e.DaysLeft = daysLeft(e.Expires)
		out = append(out, e)
	}
	cache, _ := m.regCache().Load()
	for _, d := range m.websiteOnly() {
		e := DomainExpiry{Domain: d.Domain, Account: d.Account, Source: "website"}
		if c := cache[d.Domain]; c != nil {
			e.Registrar, e.Expires, e.CheckedAt, e.Error = c.Registrar, c.Expires, c.CheckedAt, c.Error
		} else {
			e.Error = "Not checked yet."
		}
		e.DaysLeft = daysLeft(e.Expires)
		out = append(out, e)
	}
	out = m.applyRenewals(out)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Expires.IsZero() != b.Expires.IsZero() {
			return !a.Expires.IsZero()
		}
		if !a.Expires.Equal(b.Expires) {
			return a.Expires.Before(b.Expires)
		}
		return a.Domain < b.Domain
	})
	return out, nil
}

// RefreshDomain asks the registry about one domain now. It works for zones
// and for websites whose DNS lives elsewhere.
func (m *Manager) RefreshDomain(ctx context.Context, domain string) (*DomainExpiry, error) {
	domain = registrable(strings.ToLower(strings.TrimSpace(domain)))
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	if z := m.ZoneFor(domain); z != nil && z.Name == domain {
		if _, err := m.RefreshRegistration(ctx, z.Account, z.Name); err != nil {
			return nil, err
		}
	} else {
		var ref *DomainRef
		for _, d := range m.websiteOnly() {
			if d.Domain == domain {
				ref = &d
				break
			}
		}
		if ref == nil {
			return nil, fmt.Errorf("%s %w on this server", domain, kit.ErrNotFound)
		}
		m.refreshCached(ctx, *ref)
	}
	all, err := m.DomainExpiries()
	if err != nil {
		return nil, err
	}
	for _, e := range all {
		if e.Domain == domain {
			return &e, nil
		}
	}
	return nil, fmt.Errorf("%s %w", domain, kit.ErrNotFound)
}

// refreshCached looks a website's domain up and keeps the answer.
func (m *Manager) refreshCached(ctx context.Context, d DomainRef) *cachedReg {
	reg, err := m.LookupRegistration(ctx, d.Domain)
	var kept *cachedReg
	_, _ = m.regCache().Update(func(c *map[string]*cachedReg) error {
		if *c == nil {
			*c = map[string]*cachedReg{}
		}
		old := (*c)[d.Domain]
		if err != nil {
			if old == nil {
				old = &cachedReg{}
			}
			old.Error, old.CheckedAt, old.Account = err.Error(), kit.Now(), d.Account
			(*c)[d.Domain] = old
			kept = old
			return nil
		}
		n := &cachedReg{Registration: *reg, Account: d.Account}
		if old != nil {
			n.LastReminder = old.LastReminder
		}
		(*c)[d.Domain] = n
		kept = n
		return nil
	})
	return kept
}

// websiteExpiryPass refreshes website domains (not more than daily) and
// publishes dns.domain.expiring at the reminder days, like zones get.
func (m *Manager) websiteExpiryPass(ctx context.Context, reminders []int) {
	refs := m.websiteOnly()
	cache, _ := m.regCache().Load()
	for _, d := range refs {
		c := cache[d.Domain]
		if c == nil || time.Since(c.CheckedAt) > 20*time.Hour {
			c = m.refreshCached(ctx, d)
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.rdapPause):
			}
		}
		if c == nil || c.Expires.IsZero() {
			continue
		}
		left := daysLeft(c.Expires)
		remind := dueReminder(reminders, left, c.LastReminder)
		reset := left > 120 && c.LastReminder != 0
		if remind == 0 && !reset {
			continue
		}
		if remind != 0 {
			m.env.Publish("dns.domain.expiring", d.Domain, map[string]any{
				"account": d.Account, "days_left": left, "expires": c.Expires, "registrar": c.Registrar, "dns": "elsewhere"})
		}
		_, _ = m.regCache().Update(func(cm *map[string]*cachedReg) error {
			if x := (*cm)[d.Domain]; x != nil {
				x.LastReminder = remind // 0 again once renewed
			}
			return nil
		})
	}
	// forget domains no website uses any more
	keep := map[string]bool{}
	for _, d := range refs {
		keep[d.Domain] = true
	}
	_, _ = m.regCache().Update(func(cm *map[string]*cachedReg) error {
		for k := range *cm {
			if !keep[k] {
				delete(*cm, k)
			}
		}
		return nil
	})
}

// dueReminder is the most urgent reminder day already reached and not yet
// sent (0 when none), so a domain found 9 days from expiry gets one 14-day
// reminder rather than a 30-day one now and a 14-day one tomorrow.
func dueReminder(reminders []int, left, last int) int {
	due := 0
	for _, r := range reminders {
		if left <= r && (last == 0 || last > r) && (due == 0 || r < due) {
			due = r
		}
	}
	return due
}
