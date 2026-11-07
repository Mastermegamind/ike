package dns

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

func connectNamecheap(t *testing.T, m *Manager) {
	t.Helper()
	if _, err := m.SetRegistrar(Registrar{Kind: "namecheap", APIUser: "marvellous", Username: "marvellous", APIKey: "0123456789abcdef0123",
		Contact: Contact{FirstName: "Marvellous", LastName: "Okwor", Address1: "1 Marina", City: "Lagos", State: "Lagos", PostalCode: "101001",
			Country: "NG", Phone: "+234.8031234567", Email: "a@b.ng"}}); err != nil {
		t.Fatal(err)
	}
}

func ncList(expires time.Time) string {
	return `<?xml version="1.0"?><ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.getList"><DomainGetListResult>
		<Domain Name="shop.ng" Expires="` + expires.Format("01/02/2006") + `" IsExpired="false" IsLocked="false" AutoRenew="false"/>
		</DomainGetListResult></CommandResponse></ApiResponse>`
}

func TestAutoRenewAndManualDates(t *testing.T) {
	m, _ := testManager(t)
	m.rdapPause = 0
	bus := kit.NewBus()
	m.env.Events = bus
	ctx := context.Background()
	if _, err := m.Create(ctx, "shop", CreateZone{Name: "shop.ng"}); err != nil {
		t.Fatal(err)
	}
	m.WebsiteDomains = func() []DomainRef { return []DomainRef{{"blog.other.ng", "blog"}} }
	yes, no, two := true, false, 2
	// auto-renew needs a registrar
	if _, err := m.SetRenewal("shop.ng", RenewalRequest{AutoRenew: &yes}); err == nil {
		t.Fatal("auto-renew without a registrar")
	}
	connectNamecheap(t, m)
	soon := time.Now().Add(20 * 24 * time.Hour)
	fh := &fakeHTTP{answers: map[string]string{
		"Command=namecheap.domains.getList": ncList(soon),
		"Command=namecheap.domains.renew": `<ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.renew">
			<DomainRenewResult DomainName="shop.ng" Renew="true" ChargedAmount="8.88"/></CommandResponse></ApiResponse>`,
	}}
	m.http = fh
	if err := m.syncRegistrar(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRenewal("other.ng", RenewalRequest{AutoRenew: &yes}); err == nil || !strings.Contains(err.Error(), "isn't in the connected") {
		t.Fatalf("domain elsewhere accepted: %v", err)
	}
	if _, err := m.SetRenewal("shop.ng", RenewalRequest{AutoRenew: &yes, Years: &two}); err != nil {
		t.Fatal(err)
	}
	// a manual date for the domain registered elsewhere
	date := time.Now().AddDate(1, 0, 0).Format("2006-01-02")
	if _, err := m.SetRenewal("blog.other.ng", RenewalRequest{ManualExpires: &date}); err != nil {
		t.Fatal(err)
	}
	bad := "31/12/2027"
	if _, err := m.SetRenewal("other.ng", RenewalRequest{ManualExpires: &bad}); err == nil {
		t.Fatal("bad date accepted")
	}
	list, _ := m.DomainExpiries()
	by := map[string]DomainExpiry{}
	for _, e := range list {
		by[e.Domain] = e
	}
	if s := by["shop.ng"]; !s.AtRegistrar || !s.AutoRenew || s.Expires.IsZero() || s.RenewDaysBefore != 30 {
		t.Fatalf("%+v", s)
	}
	if o := by["other.ng"]; o.Source != "manual" || o.Expires.Format("2006-01-02") != date || o.Error != "" {
		t.Fatalf("%+v", o)
	}

	// 20 days left, renews at 30: renewed for 2 years, once
	renews := func() int {
		n := 0
		for _, b := range fh.bodies {
			if strings.Contains(b, "Command=namecheap.domains.renew") {
				n++
				if !strings.Contains(b, "Years=2") || !strings.Contains(b, "DomainName=shop.ng") {
					t.Fatalf("renew request: %s", b)
				}
			}
		}
		return n
	}
	m.autoRenewPass(ctx)
	m.autoRenewPass(ctx)
	if renews() != 1 {
		t.Fatalf("%d renewals", renews())
	}
	// a failure is reported (alerts pick up *.failed) and kept for the panel
	fh.answers["Command=namecheap.domains.renew"] = `<ApiResponse Status="ERROR"><Errors><Error Number="2528166">Insufficient funds</Error></Errors></ApiResponse>`
	_, _ = m.renewalStore().Update(func(all *map[string]*Renewal) error { (*all)["shop.ng"].LastAttempt = time.Time{}; return nil })
	m.autoRenewPass(ctx)
	failed := false
	for _, e := range bus.Since(0, 100) {
		if e.Type == "dns.domain.renew.failed" && e.Level == "bad" {
			failed = true
		}
	}
	list, _ = m.DomainExpiries()
	if !failed || !strings.Contains(list[0].RenewError+list[1].RenewError, "Insufficient funds") {
		t.Fatalf("failure not reported: %v %+v", failed, list)
	}
	// switching off stops it
	if _, err := m.SetRenewal("shop.ng", RenewalRequest{AutoRenew: &no}); err != nil {
		t.Fatal(err)
	}
	_, _ = m.renewalStore().Update(func(all *map[string]*Renewal) error { (*all)["shop.ng"].LastAttempt = time.Time{}; return nil })
	before := renews()
	m.autoRenewPass(ctx)
	if renews() != before {
		t.Fatal("renewed while off")
	}
}
