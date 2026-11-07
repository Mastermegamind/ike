package dns

// Domain renewals. Domains in the connected Namecheap account can be renewed
// by Ike itself when they come within a set number of days of expiring
// (Namecheap's API renews but can't switch its own auto-renew). Domains
// registered elsewhere can be given their renewal date by hand, for registries
// that don't answer RDAP or registrars that report it late.

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// Renewal is how one registered domain is kept alive.
type Renewal struct {
	// AutoRenew renews through the registrar when DaysBefore is reached.
	AutoRenew  bool `json:"auto_renew"`
	Years      int  `json:"years,omitempty"`       // per renewal (default 1)
	DaysBefore int  `json:"days_before,omitempty"` // default 30
	// ManualExpires is a date set by hand; it wins over the registry's.
	ManualExpires time.Time `json:"manual_expires,omitzero"`
	LastAttempt   time.Time `json:"last_attempt,omitzero"`
	LastRenewed   time.Time `json:"last_renewed,omitzero"`
	LastError     string    `json:"last_error,omitempty"`
}

// registrarState is what the registrar account says, refreshed daily.
type registrarState struct {
	Domains map[string]registrarEntry `json:"domains"`
	Synced  time.Time                 `json:"synced,omitzero"`
}

type registrarEntry struct {
	Expires time.Time `json:"expires,omitzero"`
	Expired bool      `json:"expired,omitempty"`
}

func (m *Manager) renewalStore() *kit.Store[map[string]*Renewal] {
	m.renewOnce.Do(func() {
		m.renewals = &kit.Store[map[string]*Renewal]{Path: m.env.P(filepath.Join(m.env.Cfg.DNSDir, "renewals.json"))}
		m.atRegistrar = &kit.Store[registrarState]{Path: m.env.P(filepath.Join(m.env.Cfg.DNSDir, "registrar-domains.json"))}
	})
	return m.renewals
}

func (m *Manager) registrarStore() *kit.Store[registrarState] {
	m.renewalStore()
	return m.atRegistrar
}

func (m *Manager) hasRegistrar() bool {
	r, err := m.RegistrarSettings()
	return err == nil && r != nil
}

// RenewalRequest changes how a domain is renewed. Nil fields stay as they are.
type RenewalRequest struct {
	AutoRenew     *bool   `json:"auto_renew"`
	Years         *int    `json:"years"`
	DaysBefore    *int    `json:"days_before"`
	ManualExpires *string `json:"manual_expires"` // YYYY-MM-DD, "" clears
}

// SetRenewal changes a domain's renewal settings. Auto-renew needs the domain
// in the connected registrar account.
func (m *Manager) SetRenewal(domain string, req RenewalRequest) (*Renewal, error) {
	domain = registrable(strings.ToLower(strings.TrimSpace(domain)))
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	if req.Years != nil && (*req.Years < 1 || *req.Years > 10) {
		return nil, fmt.Errorf("renew for 1-10 years")
	}
	if req.DaysBefore != nil && (*req.DaysBefore < 1 || *req.DaysBefore > 90) {
		return nil, fmt.Errorf("renew 1-90 days before it expires")
	}
	var manual time.Time
	if req.ManualExpires != nil && *req.ManualExpires != "" {
		t, err := time.Parse("2006-01-02", *req.ManualExpires)
		if err != nil || t.Year() < 2000 || t.Year() > 2100 {
			return nil, fmt.Errorf("the renewal date must look like 2027-03-31")
		}
		manual = t.Add(23*time.Hour + 59*time.Minute)
	}
	if req.AutoRenew != nil && *req.AutoRenew {
		if !m.hasRegistrar() {
			return nil, fmt.Errorf("connect a Namecheap account under DNS settings first")
		}
		reg, _ := m.registrarStore().Load()
		if _, ok := reg.Domains[domain]; !ok && !reg.Synced.IsZero() {
			return nil, fmt.Errorf("%s isn't in the connected Namecheap account; renew it where it's registered", domain)
		}
	}
	var out Renewal
	_, err := m.renewalStore().Update(func(all *map[string]*Renewal) error {
		if *all == nil {
			*all = map[string]*Renewal{}
		}
		r := (*all)[domain]
		if r == nil {
			r = &Renewal{}
		}
		if req.AutoRenew != nil {
			r.AutoRenew = *req.AutoRenew
			r.LastError = ""
		}
		if req.Years != nil {
			r.Years = *req.Years
		}
		if req.DaysBefore != nil {
			r.DaysBefore = *req.DaysBefore
		}
		if req.ManualExpires != nil {
			r.ManualExpires = manual
		}
		if !r.AutoRenew && r.ManualExpires.IsZero() && r.LastRenewed.IsZero() {
			delete(*all, domain)
		} else {
			(*all)[domain] = r
		}
		out = *r
		return nil
	})
	return &out, err
}

// syncRegistrar records which domains the registrar account holds and
// their expiry dates.
func (m *Manager) syncRegistrar(ctx context.Context) error {
	if !m.hasRegistrar() {
		return nil
	}
	list, err := m.RegistrarDomains(ctx)
	if err != nil {
		return err
	}
	st := registrarState{Domains: map[string]registrarEntry{}, Synced: kit.Now()}
	for _, d := range list {
		e := registrarEntry{Expired: d.Expired}
		// Namecheap writes MM/DD/YYYY
		if t, err := time.Parse("01/02/2006", d.Expires); err == nil {
			e.Expires = t.Add(23*time.Hour + 59*time.Minute)
		}
		st.Domains[strings.ToLower(d.Name)] = e
	}
	_, err = m.registrarStore().Update(func(s *registrarState) error { *s = st; return nil })
	return err
}

// applyRenewals fills renewal facts into the expiry list.
func (m *Manager) applyRenewals(list []DomainExpiry) []DomainExpiry {
	ren, _ := m.renewalStore().Load()
	reg, _ := m.registrarStore().Load()
	for i := range list {
		e := &list[i]
		if at, ok := reg.Domains[e.Domain]; ok {
			e.AtRegistrar = true
			if !at.Expires.IsZero() && (e.Expires.IsZero() || e.Error != "") {
				e.Expires, e.Error = at.Expires, ""
			}
		}
		if r := ren[e.Domain]; r != nil {
			e.AutoRenew = r.AutoRenew && e.AtRegistrar
			e.RenewDaysBefore = r.daysBefore()
			e.RenewError = r.LastError
			if !r.ManualExpires.IsZero() {
				e.Expires, e.Error, e.Source = r.ManualExpires, "", "manual"
			}
		}
		e.DaysLeft = daysLeft(e.Expires)
	}
	return list
}

func (r *Renewal) daysBefore() int {
	if r.DaysBefore == 0 {
		return 30
	}
	return r.DaysBefore
}

func (r *Renewal) years() int {
	if r.Years == 0 {
		return 1
	}
	return r.Years
}

// RenewNow renews a domain at the registrar now, then refreshes its date.
func (m *Manager) RenewNow(ctx context.Context, domain string, years int) (map[string]any, error) {
	domain = registrable(strings.ToLower(strings.TrimSpace(domain)))
	if years == 0 {
		years = 1
	}
	res, err := m.Renew(ctx, domain, years)
	_, _ = m.renewalStore().Update(func(all *map[string]*Renewal) error {
		if *all == nil {
			*all = map[string]*Renewal{}
		}
		r := (*all)[domain]
		if r == nil {
			r = &Renewal{}
			(*all)[domain] = r
		}
		r.LastAttempt = kit.Now()
		if err != nil {
			r.LastError = err.Error()
		} else {
			r.LastError, r.LastRenewed = "", kit.Now()
			r.ManualExpires = time.Time{} // the registry knows the new date
		}
		return nil
	})
	if err != nil {
		m.env.Publish("dns.domain.renew.failed", domain, map[string]any{"error": err.Error()})
		return nil, err
	}
	_ = m.syncRegistrar(ctx)
	_, _ = m.RefreshDomain(ctx, domain)
	return res, nil
}

// autoRenewPass renews the domains that asked for it and are close enough to
// expiring. Each domain is tried at most once a day.
func (m *Manager) autoRenewPass(ctx context.Context) {
	if !m.hasRegistrar() {
		return
	}
	if err := m.syncRegistrar(ctx); err != nil {
		slog.Warn("registrar domains not refreshed", "err", err)
	}
	ren, _ := m.renewalStore().Load()
	reg, _ := m.registrarStore().Load()
	for domain, r := range ren {
		at, ok := reg.Domains[domain]
		if !r.AutoRenew || !ok || at.Expires.IsZero() {
			continue
		}
		if daysLeft(at.Expires) > r.daysBefore() || time.Since(r.LastAttempt) < 20*time.Hour {
			continue
		}
		if _, err := m.RenewNow(ctx, domain, r.years()); err != nil {
			slog.Warn("automatic renewal failed", "domain", domain, "err", err)
		}
	}
}
