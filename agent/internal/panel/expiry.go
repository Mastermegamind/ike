package panel

// Expiry dates in one place: registered domains, SSL certificates and hosting
// terms, filtered to what the signed-in person can see. Domains and
// certificates come from the agent; hosting comes from billing (an active
// subscription's next due date) or a date the admin or reseller set for
// accounts billed somewhere else.

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/client"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

// ExpiryItem is one thing that runs out.
type ExpiryItem struct {
	Kind     string    `json:"kind"` // domain | ssl | hosting
	Name     string    `json:"name"` // domain, website, or account
	Account  string    `json:"account"`
	Expires  time.Time `json:"expires,omitzero"` // zero: not known
	DaysLeft int       `json:"days_left"`
	// State: ok (more than 30 days), soon (30 or fewer), urgent (7 or fewer),
	// expired, or unknown.
	State      string `json:"state"`
	Source     string `json:"source,omitempty"` // domain: zone|website|manual; ssl: letsencrypt|custom; hosting: subscription|manual
	AutoRenews bool   `json:"auto_renews"`
	Detail     string `json:"detail,omitempty"`
	// Hosting: the unpaid invoice that decides, and when the account is
	// suspended if it stays unpaid (billing's grace days, when auto-suspend is on).
	InvoiceID  string    `json:"invoice_id,omitempty"`
	SuspendsOn time.Time `json:"suspends_on,omitzero"`
	// Domains: in the connected registrar account (so Ike can renew it).
	AtRegistrar bool `json:"at_registrar,omitempty"`
}

// ExpirySummary counts what needs attention.
type ExpirySummary struct {
	Expired  int `json:"expired"`
	Within7  int `json:"within_7"`
	Within30 int `json:"within_30"`
	Unknown  int `json:"unknown"`
}

func expiryState(expires time.Time, days int) string {
	switch {
	case expires.IsZero():
		return "unknown"
	case time.Until(expires) < 0:
		return "expired"
	case days <= 7:
		return "urgent"
	case days <= 30:
		return "soon"
	}
	return "ok"
}

func daysUntil(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return int(time.Until(t).Hours() / 24)
}

type agentExpiry struct {
	Domains []struct {
		Domain      string    `json:"domain"`
		Account     string    `json:"account"`
		Source      string    `json:"source"`
		Registrar   string    `json:"registrar"`
		Expires     time.Time `json:"expires"`
		AutoRenew   bool      `json:"auto_renew"`
		AtRegistrar bool      `json:"at_registrar"`
		Error       string    `json:"error"`
		RenewDays   int       `json:"renew_days_before"`
		RenewError  string    `json:"renew_error"`
	} `json:"domains"`
	Certificates []struct {
		Domain    string    `json:"domain"`
		Account   string    `json:"account"`
		Provider  string    `json:"provider"`
		Issuer    string    `json:"issuer"`
		ExpiresAt time.Time `json:"expires_at"`
		Problem   string    `json:"problem"`
	} `json:"certificates"`
}

// expiries builds the list for acc from one server's agent.
func (s *Server) expiries(ctx context.Context, agent *client.Client, acc access) ([]ExpiryItem, error) {
	var raw agentExpiry
	if err := agent.Get(ctx, "/v1/expiry/server", &raw); err != nil {
		return nil, err
	}
	out := []ExpiryItem{}
	for _, d := range raw.Domains {
		if !acc.has(d.Account) {
			continue
		}
		it := ExpiryItem{Kind: "domain", Name: d.Domain, Account: d.Account, Expires: d.Expires, Source: d.Source, AutoRenews: d.AutoRenew, AtRegistrar: d.AtRegistrar}
		switch {
		case d.RenewError != "":
			it.Detail = "Automatic renewal failed: " + d.RenewError
		case d.AutoRenew:
			it.Detail = fmt.Sprintf("Renewed through the registrar %d days before it expires", d.RenewDays)
		case d.Error != "" && d.Expires.IsZero():
			it.Detail = d.Error
		case d.Source == "manual":
			it.Detail = "Renewal date set by hand"
		case d.Registrar != "":
			it.Detail = "Registered with " + d.Registrar
		}
		out = append(out, it)
	}
	for _, c := range raw.Certificates {
		if !acc.has(c.Account) {
			continue
		}
		it := ExpiryItem{Kind: "ssl", Name: c.Domain, Account: c.Account, Expires: c.ExpiresAt, Source: c.Provider,
			AutoRenews: c.Provider == "letsencrypt", Detail: c.Problem}
		if it.Detail == "" && it.AutoRenews {
			it.Detail = "Let's Encrypt renews it about 30 days before it expires"
		} else if it.Detail == "" {
			it.Detail = "Uploaded certificate (" + c.Issuer + "); upload a renewed one before it expires"
		}
		out = append(out, it)
	}
	out = append(out, s.hostingExpiries(acc)...)
	for i := range out {
		out[i].DaysLeft = daysUntil(out[i].Expires)
		out[i].State = expiryState(out[i].Expires, out[i].DaysLeft)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Expires.IsZero() != b.Expires.IsZero() {
			return !a.Expires.IsZero()
		}
		if !a.Expires.Equal(b.Expires) {
			return a.Expires.Before(b.Expires)
		}
		return a.Kind+a.Name < b.Kind+b.Name
	})
	return out, nil
}

// hostingExpiries are hosting terms. For a subscription, the oldest unpaid
// invoice decides (subscriptions invoice ahead of the due date and move on to
// the next period at once); without one it's the next due date. Accounts
// billed elsewhere use a date set by hand.
func (s *Server) hostingExpiries(acc access) []ExpiryItem {
	out := []ExpiryItem{}
	s.Store.View(func(d *store.Data) {
		now := time.Now()
		grace := func(due time.Time) time.Time {
			if !d.Billing.AutoSuspend {
				return time.Time{}
			}
			return due.AddDate(0, 0, d.Billing.GraceDays)
		}
		subscribed := map[string]bool{}
		for _, sub := range d.Subscriptions {
			if sub.Status != "active" || !acc.has(sub.Account) || subscribed[sub.Account] {
				continue
			}
			subscribed[sub.Account] = true
			it := ExpiryItem{Kind: "hosting", Name: sub.Account, Account: sub.Account, Expires: sub.NextDue, Source: "subscription", AutoRenews: true,
				Detail: fmt.Sprintf("Renews every %s at %s; the invoice comes 7 days before", sub.Interval, formatMoney(sub.Price, sub.Currency))}
			var oldest *store.Invoice
			for _, inv := range d.Invoices {
				if inv.Account == sub.Account && inv.Status == "unpaid" && (oldest == nil || inv.DueAt.Before(oldest.DueAt)) {
					oldest = inv
				}
			}
			if oldest != nil {
				it.Expires, it.InvoiceID = oldest.DueAt, oldest.ID
				if oldest.DueAt.Before(now) {
					it.Detail = fmt.Sprintf("Invoice %s (%s) has been unpaid since %s", oldest.ID, formatMoney(oldest.Amount, oldest.Currency), oldest.DueAt.Format("2 Jan 2006"))
				} else {
					it.Detail = fmt.Sprintf("Invoice %s (%s) is due on %s", oldest.ID, formatMoney(oldest.Amount, oldest.Currency), oldest.DueAt.Format("2 Jan 2006"))
				}
			}
			it.SuspendsOn = grace(it.Expires)
			if !it.SuspendsOn.IsZero() {
				it.Detail += fmt.Sprintf("; suspends on %s if unpaid", it.SuspendsOn.Format("2 Jan 2006"))
			}
			out = append(out, it)
		}
		for a, until := range d.HostingExpiry {
			if subscribed[a] || !acc.has(a) || until.IsZero() {
				continue
			}
			out = append(out, ExpiryItem{Kind: "hosting", Name: a, Account: a, Expires: until, Source: "manual",
				Detail: "End date set by your hosting provider"})
		}
	})
	return out
}

func summarize(items []ExpiryItem) ExpirySummary {
	var sm ExpirySummary
	for _, it := range items {
		switch it.State {
		case "expired":
			sm.Expired++
		case "urgent":
			sm.Within7++
			sm.Within30++
		case "soon":
			sm.Within30++
		case "unknown":
			sm.Unknown++
		}
	}
	return sm
}

// GET /api/v1/expiry[?kind=domain|ssl|hosting][&account=]
func (s *Server) listExpiry(w http.ResponseWriter, r *http.Request) {
	acc := s.accessFor(personFrom(r))
	items, err := s.expiries(r.Context(), s.agentFor(r), acc)
	if err != nil {
		failErr(w, err)
		return
	}
	kind, account := r.URL.Query().Get("kind"), r.URL.Query().Get("account")
	if kind != "" || account != "" {
		keep := items[:0]
		for _, it := range items {
			if (kind == "" || it.Kind == kind) && (account == "" || it.Account == account) {
				keep = append(keep, it)
			}
		}
		items = keep
	}
	writeJSON(w, 200, map[string]any{"items": items, "summary": summarize(items)})
}

// PUT /api/v1/expiry/hosting/{account} {"expires":"YYYY-MM-DD"} sets the end
// of an account's hosting when it isn't billed by Ike; "" clears it.
func (s *Server) setHostingExpiry(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	account := r.PathValue("account")
	if me.Role.Customer() || !s.accessFor(me).has(account) {
		failErr(w, errForbidden)
		return
	}
	var req struct {
		Expires string `json:"expires"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	var until time.Time
	if req.Expires != "" {
		t, err := time.Parse("2006-01-02", req.Expires)
		if err != nil {
			fail(w, 400, "expires must be a date like 2027-03-31")
			return
		}
		if t.Year() < 2000 || t.Year() > 2100 {
			fail(w, 400, "that date is out of range")
			return
		}
		until = t.Add(23*time.Hour + 59*time.Minute) // the end of that day
	}
	if err := s.Store.Update(func(d *store.Data) error {
		if until.IsZero() {
			delete(d.HostingExpiry, account)
		} else {
			d.HostingExpiry[account] = until
		}
		// a new date gets its own reminders
		for k := range d.ExpiryAlerts {
			if strings.HasPrefix(k, "hosting:"+account+":") {
				delete(d.ExpiryAlerts, k)
			}
		}
		return nil
	}); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"account": account, "expires": req.Expires})
}

// POST /api/v1/expiry/domains/{domain}/refresh asks the registry now, for a
// domain the person can see.
func (s *Server) refreshDomainExpiry(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(r.PathValue("domain"))
	acc := s.accessFor(personFrom(r))
	agent := s.agentFor(r)
	if !acc.all {
		items, err := s.expiries(r.Context(), agent, acc)
		if err != nil {
			failErr(w, err)
			return
		}
		mine := false
		for _, it := range items {
			if it.Kind == "domain" && (it.Name == domain || strings.HasSuffix(domain, "."+it.Name)) {
				mine = true
			}
		}
		if !mine {
			failErr(w, errForbidden)
			return
		}
	}
	var out any
	if err := agent.Do(r.Context(), "POST", "/v1/expiry/domains/"+client.PathEscape(domain)+"/refresh", nil, &out); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// expiryReminderDays are when admins hear about domains and hosting running
// out on their alert channels (certificates have their own alerts).
var expiryReminderDays = []int{30, 14, 7, 3, 1, 0}

// SendExpiryReminders tells the admin's alert channels about domains and
// hosting reaching a reminder day, once per day mark and expiry date.
func (s *Server) SendExpiryReminders(ctx context.Context) {
	if s.Notifier == nil {
		return
	}
	items, err := s.expiries(ctx, s.Agent, access{all: true})
	if err != nil {
		return
	}
	type due struct {
		key  string
		mark int
		it   ExpiryItem
	}
	var send []due
	live := map[string]bool{}
	s.Store.View(func(d *store.Data) {
		for _, it := range items {
			if it.Kind == "ssl" || it.Expires.IsZero() {
				continue
			}
			key := it.Kind + ":" + it.Name + ":" + it.Expires.UTC().Format("2006-01-02")
			live[key] = true
			mark := -1
			for _, m := range expiryReminderDays {
				if it.DaysLeft <= m && (mark == -1 || m < mark) {
					mark = m
				}
			}
			if mark == -1 {
				continue
			}
			if last, ok := d.ExpiryAlerts[key]; ok && last <= mark {
				continue
			}
			send = append(send, due{key, mark, it})
		}
	})
	for _, x := range send {
		s.Notifier.Send(ctx, expiryTitle(x.it), expiryBody(x.it))
	}
	_ = s.Store.Update(func(d *store.Data) error {
		for _, x := range send {
			d.ExpiryAlerts[x.key] = x.mark
		}
		for k := range d.ExpiryAlerts { // renewed or gone
			if !live[k] {
				delete(d.ExpiryAlerts, k)
			}
		}
		return nil
	})
}

func expiryTitle(it ExpiryItem) string {
	what := map[string]string{"domain": "Domain " + it.Name, "hosting": "Hosting for " + it.Name}[it.Kind]
	switch {
	case it.State == "expired":
		return what + " has expired"
	case it.DaysLeft == 0:
		return what + " expires today"
	case it.DaysLeft == 1:
		return what + " expires tomorrow"
	}
	return fmt.Sprintf("%s expires in %d days", what, it.DaysLeft)
}

func expiryBody(it ExpiryItem) string {
	b := "Expires " + it.Expires.Format("Mon 2 Jan 2006")
	if it.Account != "" && it.Account != it.Name {
		b += " (account " + it.Account + ")"
	}
	b += "."
	if it.Detail != "" {
		b += " " + it.Detail + "."
	}
	switch it.Kind {
	case "domain":
		b += " Renew it with the registrar, or the websites and email on it stop working."
	case "hosting":
		b += " See Billing in the panel."
	}
	return b
}

// domainOwnerAccount finds the account a registered domain belongs to, among
// the ones acc can see ("" when none).
func (s *Server) domainOwnerAccount(r *http.Request, acc access, domain string) string {
	items, err := s.expiries(r.Context(), s.agentFor(r), acc)
	if err != nil {
		return ""
	}
	for _, it := range items {
		if it.Kind == "domain" && it.Name == domain {
			return it.Account
		}
	}
	return ""
}

// PUT /api/v1/expiry/domains/{domain} {"auto_renew","years","days_before","manual_expires"}.
// Renewing spends the admin's registrar balance, so only admins switch
// auto-renew; resellers may set renewal dates for their customers' domains.
func (s *Server) setDomainRenewal(w http.ResponseWriter, r *http.Request) {
	me := personFrom(r)
	domain := strings.ToLower(r.PathValue("domain"))
	var req struct {
		AutoRenew     *bool   `json:"auto_renew"`
		Years         *int    `json:"years"`
		DaysBefore    *int    `json:"days_before"`
		ManualExpires *string `json:"manual_expires"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if me.Role != store.Admin {
		if me.Role != store.Reseller || req.AutoRenew != nil || req.Years != nil || req.DaysBefore != nil ||
			s.domainOwnerAccount(r, s.accessFor(me), domain) == "" {
			failErr(w, errForbidden)
			return
		}
	}
	var out any
	if err := s.agentFor(r).Do(r.Context(), "PUT", "/v1/expiry/domains/"+client.PathEscape(domain), req, &out); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}

// POST /api/v1/expiry/domains/{domain}/renew {"years"}: admins only.
func (s *Server) renewDomain(w http.ResponseWriter, r *http.Request) {
	if personFrom(r).Role != store.Admin {
		failErr(w, errForbidden)
		return
	}
	var req struct {
		Years int `json:"years"`
	}
	if r.ContentLength != 0 && !decodeBody(w, r, &req) {
		return
	}
	var out any
	if err := s.agentFor(r).Do(r.Context(), "POST", "/v1/expiry/domains/"+client.PathEscape(strings.ToLower(r.PathValue("domain")))+"/renew", req, &out); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, out)
}
