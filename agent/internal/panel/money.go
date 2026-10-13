package panel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/panel/billing"
	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
)

const overduePrefix = "Overdue invoice "

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

func (s *Server) providers() map[string]billing.Provider {
	if s.Providers != nil {
		return s.Providers()
	}
	var set store.BillingSettings
	s.Store.View(func(d *store.Data) { set = d.Billing })
	return billing.FromSettings(set)
}

// --------------------------------------------------------------- settings --

const mask = "••••••••"

func maskSecret(v string) string {
	if v == "" {
		return ""
	}
	return mask
}

func (s *Server) getBillingSettings(w http.ResponseWriter, r *http.Request) {
	var out store.BillingSettings
	s.Store.View(func(d *store.Data) {
		out = d.Billing
		out.Providers = map[string]store.ProviderSettings{}
		for k, p := range d.Billing.Providers {
			p.SecretKey, p.WebhookSecret = maskSecret(p.SecretKey), maskSecret(p.WebhookSecret)
			out.Providers[k] = p
		}
	})
	writeJSON(w, 200, map[string]any{"settings": out, "webhook_urls": map[string]string{
		"bachs": s.PublicURL + "/webhooks/bachs", "paystack": s.PublicURL + "/webhooks/paystack", "flutterwave": s.PublicURL + "/webhooks/flutterwave",
	}})
}

func (s *Server) putBillingSettings(w http.ResponseWriter, r *http.Request) {
	var in store.BillingSettings
	if err := decode(r, &in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	in.Currency = strings.ToUpper(in.Currency)
	if !currencyRe.MatchString(in.Currency) {
		fail(w, 400, "Currency must be a 3-letter code like NGN, GHS, KES or USD.")
		return
	}
	if in.GraceDays < 0 || in.GraceDays > 90 {
		fail(w, 400, "Grace period must be 0-90 days.")
		return
	}
	err := s.Store.Update(func(d *store.Data) error {
		for name, p := range in.Providers {
			if name != "bachs" && name != "paystack" && name != "flutterwave" {
				return fmt.Errorf("unknown payment provider %q", name)
			}
			old := d.Billing.Providers[name]
			if p.SecretKey == mask || p.SecretKey == "" {
				p.SecretKey = old.SecretKey // masked or empty means "keep"
			}
			if p.WebhookSecret == mask || p.WebhookSecret == "" {
				p.WebhookSecret = old.WebhookSecret
			}
			if p.Enabled && p.SecretKey == "" {
				return fmt.Errorf("enter the secret key for %s", name)
			}
			if p.Enabled && name != "paystack" && p.WebhookSecret == "" {
				return fmt.Errorf("enter the webhook secret for %s, so payments can be verified", name)
			}
			in.Providers[name] = p
		}
		for name, old := range d.Billing.Providers {
			if _, ok := in.Providers[name]; !ok {
				in.Providers[name] = old
			}
		}
		d.Billing = in
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	s.getBillingSettings(w, r)
}

// --------------------------------------------------------------- invoices --

func (s *Server) visibleInvoices(p *store.Person) []store.Invoice {
	acc := s.accessFor(p)
	var out []store.Invoice
	s.Store.View(func(d *store.Data) {
		for _, inv := range d.Invoices {
			if acc.has(inv.Account) {
				out = append(out, *inv)
			}
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if out == nil {
		out = []store.Invoice{}
	}
	return out
}

func (s *Server) listInvoices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.visibleInvoices(personFrom(r)))
}

func (s *Server) createInvoice(w http.ResponseWriter, r *http.Request) {
	p := personFrom(r)
	if p.Role == store.User {
		fail(w, 403, "Invoices are created by your hosting provider.")
		return
	}
	var req struct {
		Account     string `json:"account"`
		Description string `json:"description"`
		Currency    string `json:"currency"`
		Amount      string `json:"amount"` // "15000.00"
		DueInDays   int    `json:"due_in_days"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if !s.accessFor(p).has(req.Account) {
		fail(w, 403, "That account isn't one of yours.")
		return
	}
	var def string
	s.Store.View(func(d *store.Data) { def = d.Billing.Currency })
	if req.Currency == "" {
		req.Currency = def
	}
	req.Currency = strings.ToUpper(req.Currency)
	if !currencyRe.MatchString(req.Currency) {
		fail(w, 400, "Currency must be a 3-letter code.")
		return
	}
	amt, err := billing.ParseDecimal(req.Amount, req.Currency)
	if err != nil || amt <= 0 {
		fail(w, 400, "Enter an amount greater than zero, like 15000.00.")
		return
	}
	desc := strings.TrimSpace(req.Description)
	if desc == "" || len(desc) > 200 {
		fail(w, 400, "Add a short description (up to 200 characters).")
		return
	}
	if req.DueInDays <= 0 {
		req.DueInDays = 7
	}
	var inv store.Invoice
	_ = s.Store.Update(func(d *store.Data) error {
		n := &store.Invoice{ID: d.NewInvoiceID(), Account: req.Account, Description: desc, Currency: req.Currency, Amount: amt,
			Status: "unpaid", DueAt: time.Now().UTC().Add(time.Duration(req.DueInDays) * 24 * time.Hour), CreatedAt: time.Now().UTC(), CreatedBy: p.Username}
		d.Invoices = append(d.Invoices, n)
		inv = *n
		return nil
	})
	writeJSON(w, 201, inv)
}

func (s *Server) checkout(w http.ResponseWriter, r *http.Request) {
	p := personFrom(r)
	var req struct {
		Provider string `json:"provider"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err.Error())
		return
	}
	prov, ok := s.providers()[req.Provider]
	if !ok {
		fail(w, 400, "That payment method isn't set up on this server.")
		return
	}
	var inv store.Invoice
	var err error
	s.Store.View(func(d *store.Data) {
		var x *store.Invoice
		if x, err = d.Invoice(r.PathValue("id")); err == nil {
			inv = *x
		}
	})
	if err != nil {
		failErr(w, err)
		return
	}
	if !s.accessFor(p).has(inv.Account) {
		fail(w, 403, "That invoice isn't one of yours.")
		return
	}
	if inv.Status != "unpaid" {
		fail(w, 400, "This invoice is already "+inv.Status+".")
		return
	}
	ret := s.PublicURL + "/#/billing?invoice=" + inv.ID
	co, err := prov.CreateCheckout(r.Context(), &inv, billing.Customer{Email: p.Email, Name: p.Name}, ret)
	if err != nil {
		slog.Error("checkout failed", "provider", req.Provider, "invoice", inv.ID, "err", err)
		fail(w, 502, "The payment provider couldn't start a checkout: "+err.Error())
		return
	}
	_ = s.Store.Update(func(d *store.Data) error {
		x, err := d.Invoice(inv.ID)
		if err != nil {
			return err
		}
		x.Provider, x.ProviderRef, x.CheckoutURL = req.Provider, co.Ref, co.URL
		return nil
	})
	writeJSON(w, 200, map[string]string{"checkout_url": co.URL})
}

func (s *Server) voidInvoice(w http.ResponseWriter, r *http.Request) {
	p := personFrom(r)
	if p.Role == store.User {
		fail(w, 403, "Only your hosting provider can cancel an invoice.")
		return
	}
	acc := s.accessFor(p)
	err := s.Store.Update(func(d *store.Data) error {
		inv, err := d.Invoice(r.PathValue("id"))
		if err != nil {
			return err
		}
		if !acc.has(inv.Account) {
			return errForbidden
		}
		if inv.Status == "paid" {
			return errors.New("a paid invoice can't be cancelled; refund it with the payment provider")
		}
		inv.Status = "void"
		return nil
	})
	if err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "void"})
}

func (s *Server) markPaid(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.settle(r.Context(), func(d *store.Data) (*store.Invoice, error) { return d.Invoice(id) }, "manual", "manual:"+id+":"+time.Now().String(), -1, ""); err != nil {
		failErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "paid"})
}

// settle marks an invoice paid exactly once and reactivates an account that
// was suspended for being overdue.
func (s *Server) settle(ctx context.Context, find func(d *store.Data) (*store.Invoice, error), provider, eventID string, amount int64, currency string) error {
	var account string
	var reactivate bool
	err := s.Store.Update(func(d *store.Data) error {
		inv, err := find(d)
		if err != nil {
			return err
		}
		if !d.SeenEvent(eventID) || inv.Status == "paid" {
			return errDuplicate
		}
		if amount >= 0 && (amount < inv.Amount || !strings.EqualFold(currency, inv.Currency)) {
			return fmt.Errorf("payment of %s %s doesn't cover invoice %s (%s %s)", billing.Decimal(amount, currency), currency,
				inv.ID, billing.Decimal(inv.Amount, inv.Currency), inv.Currency)
		}
		now := time.Now().UTC()
		inv.Status, inv.PaidAt = "paid", &now
		if provider != "manual" {
			inv.Provider = provider
		}
		account = inv.Account
		reactivate = true
		return nil
	})
	if errors.Is(err, errDuplicate) {
		return nil
	}
	if err != nil {
		return err
	}
	slog.Info("invoice paid", "account", account, "provider", provider)
	if reactivate {
		var acct struct {
			Suspended     bool   `json:"suspended"`
			SuspendReason string `json:"suspend_reason"`
		}
		if err := s.Agent.Get(ctx, "/v1/accounts/"+account, &acct); err == nil && acct.Suspended && strings.HasPrefix(acct.SuspendReason, overduePrefix) {
			if err := s.Agent.Do(ctx, "POST", "/v1/accounts/"+account+"/unsuspend", nil, nil); err == nil && s.Notifier != nil {
				s.Notifier.Send(ctx, account+" reactivated", "Payment received; the account is back online.")
			}
		}
	}
	return nil
}

var errDuplicate = errors.New("duplicate")

// webhook receives payment notifications. It answers 200 for anything it has
// verified (even if ignored) so providers stop retrying, and 400 for bad signatures.
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("provider")
	prov, ok := s.providers()[name]
	if !ok {
		fail(w, 404, "unknown provider")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		fail(w, 400, "unreadable body")
		return
	}
	pay, err := prov.ParseWebhook(r.Context(), r.Header, body)
	if errors.Is(err, billing.ErrBadSignature) {
		slog.Warn("webhook rejected", "provider", name, "ip", clientIP(r), "err", err)
		fail(w, 400, "invalid signature")
		return
	}
	if err != nil {
		slog.Error("webhook error", "provider", name, "err", err)
		fail(w, 500, "could not verify payment") // provider retries
		return
	}
	if pay == nil {
		writeJSON(w, 200, map[string]string{"status": "ignored"})
		return
	}
	err = s.settle(r.Context(), func(d *store.Data) (*store.Invoice, error) { return d.InvoiceByRef(name, pay.Ref) },
		name, pay.EventID, pay.Amount, pay.Currency)
	if err != nil {
		slog.Error("webhook could not settle", "provider", name, "ref", pay.Ref, "err", err)
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 200, map[string]string{"status": "unknown reference"})
			return
		}
		fail(w, 422, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

// EnforceOverdue suspends accounts whose invoices are past due plus the grace period.
func (s *Server) EnforceOverdue(ctx context.Context, now time.Time) {
	var set store.BillingSettings
	type overdue struct{ account, id string }
	var due []overdue
	s.Store.View(func(d *store.Data) {
		set = d.Billing
		for _, inv := range d.Invoices {
			if inv.Status == "unpaid" && now.After(inv.DueAt.Add(time.Duration(d.Billing.GraceDays)*24*time.Hour)) {
				due = append(due, overdue{inv.Account, inv.ID})
			}
		}
	})
	if !set.AutoSuspend {
		return
	}
	done := map[string]bool{}
	for _, o := range due {
		if done[o.account] {
			continue
		}
		done[o.account] = true
		var acct struct {
			Suspended bool `json:"suspended"`
		}
		if err := s.Agent.Get(ctx, "/v1/accounts/"+o.account, &acct); err != nil || acct.Suspended {
			continue
		}
		if err := s.Agent.Do(ctx, "POST", "/v1/accounts/"+o.account+"/suspend", map[string]string{"reason": overduePrefix + o.id}, nil); err == nil {
			slog.Info("suspended for overdue invoice", "account", o.account, "invoice", o.id)
			if s.Notifier != nil {
				s.Notifier.Send(ctx, o.account+" suspended", "Invoice "+o.id+" is overdue. Paying it reactivates the account automatically.")
			}
		}
	}
}

// ----------------------------------------------------------------- alerts --

func (s *Server) getAlertSettings(w http.ResponseWriter, r *http.Request) {
	var a store.AlertSettings
	s.Store.View(func(d *store.Data) { a = d.Alerts })
	a.Telegram.BotToken = maskSecret(a.Telegram.BotToken)
	a.WhatsApp.Token = maskSecret(a.WhatsApp.Token)
	a.Email.Password = maskSecret(a.Email.Password)
	writeJSON(w, 200, a)
}

func (s *Server) putAlertSettings(w http.ResponseWriter, r *http.Request) {
	var in store.AlertSettings
	if err := decode(r, &in); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if in.UptimeIntervalMin < 0 || in.UptimeIntervalMin > 1440 {
		fail(w, 400, "Uptime interval must be 0-1440 minutes.")
		return
	}
	_ = s.Store.Update(func(d *store.Data) error {
		keep := func(n, o string) string {
			if n == mask || n == "" {
				return o
			}
			return n
		}
		in.Telegram.BotToken = keep(in.Telegram.BotToken, d.Alerts.Telegram.BotToken)
		in.WhatsApp.Token = keep(in.WhatsApp.Token, d.Alerts.WhatsApp.Token)
		in.Email.Password = keep(in.Email.Password, d.Alerts.Email.Password)
		d.Alerts = in
		return nil
	})
	s.getAlertSettings(w, r)
}

func (s *Server) testAlert(w http.ResponseWriter, r *http.Request) {
	if s.Notifier == nil {
		fail(w, 500, "alerts aren't configured")
		return
	}
	res := s.Notifier.Send(r.Context(), "Test alert from Ike", "If you can read this, alerts reach you here.")
	if len(res) == 0 {
		fail(w, 400, "Turn on at least one channel first.")
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) alertEvents(w http.ResponseWriter, r *http.Request) {
	var out []any
	if s.Monitor != nil {
		for _, e := range s.Monitor.Events() {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []any{}
	}
	writeJSON(w, 200, out)
}
