package panel

// Expiry emails to account owners: their domains, uploaded certificates and
// hosting, at the reminder days the admin picked (30, 14, 7 and 1 by
// default), in the brand of whoever sells them hosting, with a Pay now link
// for hosting. Sent through the alert SMTP server; each person gets one email
// per item, expiry date and reminder day.

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Mastermegamind/ike/agent/internal/panel/alerts"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

var defaultCustomerDays = []int{30, 14, 7, 1}

// customerMail sends one email (replaced in tests).
func (s *Server) customerMail(set store.AlertSettings, fromName, to, subject, body string) error {
	if s.MailHook != nil {
		return s.MailHook(fromName, to, subject, body)
	}
	return alerts.SendMail(set, fromName, to, subject, body)
}

// owners are the people who own an account (not developers or resellers).
func owners(d *store.Data, account string) []*store.Person {
	var out []*store.Person
	for _, p := range d.People {
		if p.Role != store.User || p.Email == "" || p.Disabled {
			continue
		}
		for _, a := range p.Accounts {
			if a == account {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// brandFor is the brand an account's owner knows: their reseller's, or the
// admin's. base is where they open the panel.
func (s *Server) brandFor(d *store.Data, account string) (name, support, base string) {
	name, base = "Ike", s.PublicURL
	for _, key := range []string{d.AccountOf[account], ""} {
		b := d.Branding[key]
		if b == nil {
			continue
		}
		if b.ProductName != "" {
			name = b.ProductName
		}
		support = b.SupportEmail
		if len(b.Hostnames) > 0 {
			base = "https://" + b.Hostnames[0]
		}
		break
	}
	return name, support, strings.TrimRight(base, "/")
}

// customerWants says whether an owner needs to hear about an item: hosting
// always (someone pays), domains and certificates only when nothing renews
// them by itself.
func customerWants(it ExpiryItem) bool {
	switch it.Kind {
	case "hosting":
		return true
	case "domain", "ssl":
		return !it.AutoRenews
	}
	return false
}

// SendCustomerReminders emails account owners about what of theirs runs out.
func (s *Server) SendCustomerReminders(ctx context.Context) {
	var set store.AlertSettings
	s.Store.View(func(d *store.Data) { set = d.Alerts })
	if !set.Customers.Enabled || set.Email.Host == "" || set.Email.From == "" {
		return
	}
	days := set.Customers.Days
	if len(days) == 0 {
		days = defaultCustomerDays
	}
	items, err := s.expiries(ctx, s.Agent, access{all: true})
	if err != nil {
		slog.Warn("customer reminders skipped", "err", err)
		return
	}
	type mailOut struct {
		key, to, from, subject, body string
		mark                         int
	}
	var out []mailOut
	live := map[string]bool{}
	s.Store.View(func(d *store.Data) {
		for _, it := range items {
			if it.Expires.IsZero() || !customerWants(it) {
				continue
			}
			mark := -1
			for _, m := range days {
				if it.DaysLeft <= m && (mark == -1 || m < mark) {
					mark = m
				}
			}
			for _, p := range owners(d, it.Account) {
				key := p.Username + ":" + it.Kind + ":" + it.Name + ":" + it.Expires.UTC().Format("2006-01-02")
				live[key] = true
				if mark == -1 {
					continue
				}
				if last, ok := d.CustomerAlerts[key]; ok && last <= mark {
					continue
				}
				brand, support, base := s.brandFor(d, it.Account)
				subject, body := customerMessage(it, p, brand, support, base)
				out = append(out, mailOut{key: key, to: p.Email, from: brand, subject: subject, body: body, mark: mark})
			}
		}
	})
	sent := map[string]int{}
	for _, m := range out {
		if err := s.customerMail(set, m.from, m.to, m.subject, m.body); err != nil {
			slog.Warn("expiry email not sent", "to", m.to, "err", err)
			continue // tried again on the next pass
		}
		sent[m.key] = m.mark
	}
	_ = s.Store.Update(func(d *store.Data) error {
		for k, v := range sent {
			d.CustomerAlerts[k] = v
		}
		for k := range d.CustomerAlerts { // renewed, paid or gone
			if !live[k] {
				delete(d.CustomerAlerts, k)
			}
		}
		return nil
	})
}

func customerMessage(it ExpiryItem, p *store.Person, brand, support, base string) (string, string) {
	when := "on " + it.Expires.Format("Monday 2 January 2006")
	var what, subject string
	switch it.Kind {
	case "hosting":
		what = "Your hosting for " + it.Name
	case "domain":
		what = "Your domain " + it.Name
	case "ssl":
		what = "The SSL certificate for " + it.Name
	}
	switch {
	case it.State == "expired":
		subject = what + " has expired"
	case it.DaysLeft == 0:
		subject = what + " expires today"
	case it.DaysLeft == 1:
		subject = what + " expires tomorrow"
	default:
		subject = fmt.Sprintf("%s expires in %d days", what, it.DaysLeft)
	}
	var b strings.Builder
	name := p.Name
	if name == "" {
		name = p.Username
	}
	fmt.Fprintf(&b, "Hello %s,\n\n", name)
	if it.State == "expired" {
		fmt.Fprintf(&b, "%s expired %s.\n", what, when)
	} else {
		fmt.Fprintf(&b, "%s expires %s.\n", what, when)
	}
	switch it.Kind {
	case "hosting":
		if !it.SuspendsOn.IsZero() {
			fmt.Fprintf(&b, "If it isn't paid, the account is suspended on %s and your websites and email stop working until it is.\n", it.SuspendsOn.Format("2 January 2006"))
		}
		pay := base + "/#/billing"
		if it.InvoiceID != "" {
			fmt.Fprintf(&b, "\nInvoice %s is waiting.\n", it.InvoiceID)
			pay += "?invoice=" + it.InvoiceID
		}
		fmt.Fprintf(&b, "\nPay now: %s\n", pay)
	case "domain":
		b.WriteString("\nRenew it with your domain registrar before then, or your websites and email on it stop working.\n")
		fmt.Fprintf(&b, "\nSee all your dates: %s/#/expiry\n", base)
	case "ssl":
		b.WriteString("\nUpload a renewed certificate before then, or visitors see a security warning.\n")
		fmt.Fprintf(&b, "\nOpen the website: %s/#/sites/%s\n", base, it.Name)
	}
	if support != "" {
		fmt.Fprintf(&b, "\nQuestions? Write to %s.\n", support)
	}
	fmt.Fprintf(&b, "\n%s\n", brand)
	return subject, b.String()
}
