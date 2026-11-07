package panel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/panel/alerts"
	"github.com/Mastermegamind/ike/agent/internal/panel/auth"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

func TestExpiryView(t *testing.T) {
	e := setup(t)
	now := time.Now().UTC()
	h, _ := auth.HashPassword("owner-password-1")
	_ = e.st.Update(func(d *store.Data) error {
		if err := d.AddPerson(&store.Person{Username: "ngozi", Email: "n@shop.ng", Role: store.User, Accounts: []string{"shop"}, PasswordHash: h}); err != nil {
			return err
		}
		d.Billing.AutoSuspend, d.Billing.GraceDays = true, 7
		d.Subscriptions = append(d.Subscriptions,
			&store.Subscription{ID: "sub1", Account: "shop", Price: 1500000, Currency: "NGN", Interval: "month", NextDue: now.Add(20 * 24 * time.Hour), Status: "active"},
			&store.Subscription{ID: "sub2", Account: "blog", Price: 500000, Currency: "NGN", Interval: "year", NextDue: now.Add(5 * 24 * time.Hour), Status: "active"})
		d.Invoices = append(d.Invoices, &store.Invoice{ID: "INV-0007", Account: "shop", Amount: 1500000, Currency: "NGN", Status: "unpaid", DueAt: now.Add(-2 * 24 * time.Hour)})
		return nil
	})

	admin := e.browser(e.admin)
	if admin.login("marvellous", "admin-password-1", "") != 200 {
		t.Fatal("login")
	}
	// an account billed elsewhere gets a date by hand
	if code := admin.do("PUT", "/api/v1/expiry/hosting/legacy", map[string]string{"expires": "31-12-2027"}, nil); code != 400 {
		t.Fatalf("bad date: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/expiry/hosting/legacy", map[string]string{"expires": now.AddDate(0, 0, 90).Format("2006-01-02")}, nil); code != 200 {
		t.Fatalf("set date: %d", code)
	}
	type view struct {
		Items   []ExpiryItem  `json:"items"`
		Summary ExpirySummary `json:"summary"`
	}
	var all view
	if code := admin.do("GET", "/api/v1/expiry", nil, &all); code != 200 || len(all.Items) != 3 {
		t.Fatalf("%d %+v", code, all)
	}
	by := map[string]ExpiryItem{}
	for _, it := range all.Items {
		by[it.Name] = it
	}
	shop := by["shop"]
	// the overdue invoice decides, not the next renewal
	if shop.State != "expired" || !strings.Contains(shop.Detail, "INV-0007") || !strings.Contains(shop.Detail, "suspends on") || shop.InvoiceID != "INV-0007" || shop.SuspendsOn.Sub(shop.Expires) != 7*24*time.Hour || !strings.Contains(shop.Detail, "₦15,000.00") {
		t.Fatalf("%+v", shop)
	}
	if b := by["blog"]; b.State != "urgent" || b.Source != "subscription" || !b.AutoRenews || b.SuspendsOn.IsZero() {
		t.Fatalf("%+v", b)
	}
	if l := by["legacy"]; l.Source != "manual" || l.State != "ok" || l.DaysLeft < 89 {
		t.Fatalf("%+v", l)
	}
	if all.Summary.Expired != 1 || all.Summary.Within7 != 1 || all.Summary.Within30 != 1 {
		t.Fatalf("%+v", all.Summary)
	}
	if all.Items[0].Name != "shop" {
		t.Fatal("not soonest first")
	}

	// an account owner sees only their own, and can't set dates
	owner := e.browser(e.panel)
	if owner.login("ngozi", "owner-password-1", "") != 200 {
		t.Fatal("owner login")
	}
	var mine view
	owner.do("GET", "/api/v1/expiry", nil, &mine)
	if len(mine.Items) != 1 || mine.Items[0].Account != "shop" {
		t.Fatalf("%+v", mine.Items)
	}
	if code := owner.do("PUT", "/api/v1/expiry/hosting/shop", map[string]string{"expires": "2030-01-01"}, nil); code != 403 {
		t.Fatalf("owner set a date: %d", code)
	}
	if code := owner.do("POST", "/api/v1/expiry/domains/other.ng/refresh", nil, nil); code != 403 {
		t.Fatalf("owner refreshed someone else's domain: %d", code)
	}
	var filtered view
	admin.do("GET", "/api/v1/expiry?kind=hosting&account=blog", nil, &filtered)
	if len(filtered.Items) != 1 || filtered.Items[0].Name != "blog" {
		t.Fatalf("%+v", filtered.Items)
	}

	// reminders: once per day mark, nothing for "legacy" (90 days away)
	var mu sync.Mutex
	var sent []string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		sent = append(sent, string(b))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer tg.Close()
	_ = e.st.Update(func(d *store.Data) error {
		d.Alerts.Telegram.Enabled, d.Alerts.Telegram.BotToken, d.Alerts.Telegram.ChatID = true, "123:abc", "42"
		return nil
	})
	e.srv.Notifier = &alerts.Notifier{Settings: func() (a store.AlertSettings) { e.st.View(func(d *store.Data) { a = d.Alerts }); return }, TelegramBase: tg.URL}
	e.srv.SendExpiryReminders(context.Background())
	e.srv.SendExpiryReminders(context.Background())
	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(sent, "\n")
	if len(sent) != 2 || !strings.Contains(joined, "Hosting for shop has expired") || !strings.Contains(joined, "Hosting for blog expires in") {
		t.Fatalf("%d alerts:\n%s", len(sent), joined)
	}
}

// A subscription invoices 7 days ahead and moves NextDue on at once; the
// invoice's due date is still when hosting runs out.
func TestHostingExpiryFollowsInvoice(t *testing.T) {
	e := setup(t)
	now := time.Now().UTC()
	_ = e.st.Update(func(d *store.Data) error {
		d.Subscriptions = append(d.Subscriptions, &store.Subscription{ID: "s", Account: "shop", Price: 100, Currency: "NGN", Interval: "month",
			NextDue: now.AddDate(0, 1, 5), Status: "active"})
		d.Invoices = append(d.Invoices, &store.Invoice{ID: "INV-0009", Account: "shop", Amount: 100, Currency: "NGN", Status: "unpaid", DueAt: now.Add(5 * 24 * time.Hour)})
		return nil
	})
	items := e.srv.hostingExpiries(access{all: true})
	if len(items) != 1 || items[0].InvoiceID != "INV-0009" || !strings.Contains(items[0].Detail, "is due on") || !items[0].SuspendsOn.IsZero() {
		t.Fatalf("%+v", items)
	}
}

func TestExpiryState(t *testing.T) {
	now := time.Now()
	cases := []struct {
		t    time.Time
		want string
	}{{time.Time{}, "unknown"}, {now.Add(-time.Hour), "expired"}, {now.Add(3 * 24 * time.Hour), "urgent"},
		{now.Add(20 * 24 * time.Hour), "soon"}, {now.Add(90 * 24 * time.Hour), "ok"}}
	for _, c := range cases {
		if got := expiryState(c.t, daysUntil(c.t)); got != c.want {
			t.Errorf("%v: %s, want %s", c.t, got, c.want)
		}
	}
}

func TestCustomerReminders(t *testing.T) {
	e := setup(t)
	now := time.Now().UTC()
	h, _ := auth.HashPassword("owner-password-1")
	type mail struct{ from, to, subject, body string }
	var got []mail
	e.srv.MailHook = func(from, to, subject, body string) error {
		got = append(got, mail{from, to, subject, body})
		return nil
	}
	_ = e.st.Update(func(d *store.Data) error {
		_ = d.AddPerson(&store.Person{Username: "ngozi", Name: "Ngozi", Email: "n@shop.ng", Role: store.User, Accounts: []string{"shop"}, PasswordHash: h})
		_ = d.AddPerson(&store.Person{Username: "dev1", Email: "d@shop.ng", Role: store.Developer, Accounts: []string{"shop"}, PasswordHash: h})
		d.AccountOf["shop"] = "webafrica"
		d.Branding["webafrica"] = &store.Branding{ProductName: "WebAfrica Hosting", SupportEmail: "help@webafrica.ng", Hostnames: []string{"panel.webafrica.ng"}}
		d.Billing.AutoSuspend, d.Billing.GraceDays = true, 3
		d.Subscriptions = append(d.Subscriptions, &store.Subscription{ID: "s", Account: "shop", Price: 1500000, Currency: "NGN", Interval: "month", NextDue: now.AddDate(0, 1, 6), Status: "active"})
		d.Invoices = append(d.Invoices, &store.Invoice{ID: "INV-0011", Account: "shop", Amount: 1500000, Currency: "NGN", Status: "unpaid", DueAt: now.Add(6*24*time.Hour + time.Hour)})
		d.HostingExpiry["legacy"] = now.AddDate(0, 3, 0) // nobody owns it: no email
		return nil
	})
	// off until the admin turns it on
	e.srv.SendCustomerReminders(context.Background())
	if len(got) != 0 {
		t.Fatal("sent while off")
	}
	_ = e.st.Update(func(d *store.Data) error {
		d.Alerts.Customers.Enabled = true
		d.Alerts.Email.Host, d.Alerts.Email.From = "smtp.example.ng", "billing@example.ng"
		return nil
	})
	e.srv.SendCustomerReminders(context.Background())
	e.srv.SendCustomerReminders(context.Background())
	if len(got) != 1 {
		t.Fatalf("%d emails: %+v", len(got), got)
	}
	m := got[0]
	for _, want := range []string{"Hello Ngozi", "expires on", "Invoice INV-0011", "Pay now: https://panel.webafrica.ng/#/billing?invoice=INV-0011",
		"suspended on", "help@webafrica.ng", "WebAfrica Hosting"} {
		if !strings.Contains(m.body, want) {
			t.Errorf("body missing %q:\n%s", want, m.body)
		}
	}
	if m.to != "n@shop.ng" || m.from != "WebAfrica Hosting" || !strings.HasPrefix(m.subject, "Your hosting for shop expires in 6 days") {
		t.Fatalf("%+v", m)
	}
	// paid: the reminder record goes, nothing more is sent
	_ = e.st.Update(func(d *store.Data) error {
		d.Invoices[len(d.Invoices)-1].Status = "paid"
		return nil
	})
	e.srv.SendCustomerReminders(context.Background())
	var left int
	e.st.View(func(d *store.Data) { left = len(d.CustomerAlerts) })
	if left != 0 || len(got) != 1 {
		t.Fatalf("records %d, emails %d", left, len(got))
	}
}
