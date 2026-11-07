package dns

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

func rdapAnswer(expires time.Time) string {
	return `{"events":[{"eventAction":"expiration","eventDate":"` + expires.UTC().Format(time.RFC3339) +
		`"}],"entities":[{"roles":["registrar"],"vcardArray":["vcard",[["fn",{},"text","Whogohost Ltd"]]]}]}`
}

func TestDomainExpiriesIncludeWebsites(t *testing.T) {
	m, _ := testManager(t)
	m.rdapPause = 0
	bus := kit.NewBus()
	m.env.Events = bus
	ctx := context.Background()
	if _, err := m.Create(ctx, "shop", CreateZone{Name: "shop.ng"}); err != nil {
		t.Fatal(err)
	}
	soon := time.Now().Add(10 * 24 * time.Hour)
	later := time.Now().Add(300 * 24 * time.Hour)
	m.http = &fakeHTTP{answers: map[string]string{
		"rdap.org/domain/shop.ng":      rdapAnswer(later),
		"rdap.org/domain/other.com.ng": rdapAnswer(soon),
	}}
	m.WebsiteDomains = func() []DomainRef {
		return []DomainRef{
			{"www.shop.ng", "shop"},       // its zone is here: comes from the zone
			{"blog.other.com.ng", "blog"}, // DNS elsewhere
			{"other.com.ng", "blog"},      // same registered domain once
			{"*.gone.ng", "blog"},         // registry doesn't know it
			{"site.test", "blog"},         // never registered
		}
	}
	m.expiryPass(ctx)
	got, err := m.DomainExpiries()
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DomainExpiry{}
	for _, e := range got {
		by[e.Domain] = e
	}
	if len(got) != 3 || by["site.test"].Domain != "" {
		t.Fatalf("%+v", got)
	}
	o := by["other.com.ng"]
	if o.Source != "website" || o.Account != "blog" || o.Registrar != "Whogohost Ltd" || o.DaysLeft < 9 || o.DaysLeft > 10 {
		t.Fatalf("%+v", o)
	}
	if got[0].Domain != "other.com.ng" {
		t.Fatalf("not soonest first: %+v", got)
	}
	if z := by["shop.ng"]; z.Source != "zone" || z.Expires.IsZero() {
		t.Fatalf("%+v", z)
	}
	if g := by["gone.ng"]; g.Error == "" || !g.Expires.IsZero() {
		t.Fatalf("%+v", g)
	}
	// one reminder at the 14-day mark, not repeated on the next pass
	count := func() int {
		n := 0
		for _, e := range bus.Since(0, 100) {
			if e.Type == "dns.domain.expiring" && e.Subject == "other.com.ng" {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("%d reminders", count())
	}
	m.expiryPass(ctx)
	if count() != 1 {
		t.Fatalf("reminder repeated: %d", count())
	}
	// refresh on demand
	if e, err := m.RefreshDomain(ctx, "blog.other.com.ng"); err != nil || e.Domain != "other.com.ng" {
		t.Fatalf("%+v %v", e, err)
	}
	if _, err := m.RefreshDomain(ctx, "unknown.ng"); err == nil {
		t.Fatal("refreshed a domain the server doesn't host")
	}
	// a domain no website uses any more is forgotten
	m.WebsiteDomains = func() []DomainRef { return nil }
	m.expiryPass(ctx)
	if got, _ := m.DomainExpiries(); len(got) != 1 || !strings.HasPrefix(got[0].Domain, "shop") {
		t.Fatalf("%+v", got)
	}
}
