package dns

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

func testManager(t *testing.T) (*Manager, *runner.DryRun) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	cfg.DNSServer, cfg.PublicIPv4, cfg.PublicIPv6, cfg.Hostname = "powerdns", "203.0.113.10", "2001:db8::10", "server.host.ng"
	run := &runner.DryRun{Responses: map[string]string{
		"pdnsutil show-zone": "ID = 1, CSK, flags = 257, tag = 12345, algo = 13, bits = 256\nDS = shop.ng. IN DS 12345 13 2 ABCDEF0123456789 ; ( SHA256 digest )\nDS = shop.ng. IN DS 12345 13 4 99 ; ( SHA-384 digest )",
	}}
	keys, err := secrets.Open(filepath.Join(root, "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(&kit.Env{Cfg: cfg, Run: run}, keys)
	return m, run
}

func TestRecordValidation(t *testing.T) {
	good := []Record{
		{Name: "@", Type: "A", Content: "203.0.113.5"},
		{Name: "www", Type: "cname", Content: "@"},
		{Name: "@", Type: "MX", Content: "mail", Priority: 10},
		{Name: "_dmarc", Type: "TXT", Content: `"v=DMARC1; p=none"`},
		{Name: "_sip._tcp", Type: "SRV", Content: "sip.example.com.", Priority: 10, Weight: 5, Port: 5060},
		{Name: "@", Type: "CAA", Content: `0 issue letsencrypt.org`},
		{Name: "*.dev", Type: "AAAA", Content: "2001:DB8::1"},
		{Name: "_443._tcp", Type: "TLSA", Content: "3 1 1 abcdef12"},
		{Name: "@", Type: "NAPTR", Content: `100 10 "S" "SIP+D2U" "" _sip._udp.example.com.`},
	}
	for _, r := range good {
		r := r
		if err := r.Validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Record{
		{Name: "@", Type: "A", Content: "2001:db8::1"},
		{Name: "@", Type: "CNAME", Content: "x.com"},
		{Name: "a..b", Type: "A", Content: "1.2.3.4"},
		{Name: "a.*", Type: "A", Content: "1.2.3.4"},
		{Name: "x", Type: "MX", Content: "bad host!", Priority: 1},
		{Name: "x", Type: "TXT", Content: "line\nbreak"},
		{Name: "x", Type: "SRV", Content: "a.com"},
		{Name: "x", Type: "CAA", Content: `0 bogus "x"`},
		{Name: "x", Type: "HINFO", Content: "x"},
		{Name: "x", Type: "MX", Content: "a.com", Proxied: true},
		{Name: "x", Type: "A", Content: "1.2.3.4", TTL: 5},
	}
	for _, r := range bad {
		r := r
		if err := r.Validate(); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
	r := Record{Name: "@", Type: "AAAA", Content: "2001:DB8:0::1"}
	_ = r.Validate()
	if r.Content != "2001:db8::1" || r.TTL != 3600 {
		t.Errorf("not normalised: %+v", r)
	}
}

func TestRenderParseRoundTrip(t *testing.T) {
	long := strings.Repeat("k", 300)
	z := &Zone{Name: "shop.ng", TTL: 3600, SOAEmail: "admin@shop.ng", Serial: 2026101401, Refresh: 10800, Retry: 3600, Expire: 1209600, Minimum: 300,
		Nameservers: []string{"ns1.host.ng", "ns2.host.ng"},
		Records: []Record{
			{Name: "@", Type: "A", TTL: 300, Content: "203.0.113.5"},
			{Name: "www", Type: "CNAME", TTL: 300, Content: "@"},
			{Name: "@", Type: "MX", TTL: 3600, Content: "mail", Priority: 10},
			{Name: "@", Type: "TXT", TTL: 3600, Content: `v=spf1 a mx "q" ~all`},
			{Name: "dkim._domainkey", Type: "TXT", TTL: 3600, Content: "v=DKIM1; k=rsa; p=" + long},
			{Name: "_sip._tcp", Type: "SRV", TTL: 3600, Content: "sip.other.com.", Priority: 1, Weight: 2, Port: 5060},
			{Name: "@", Type: "CAA", TTL: 3600, Content: `0 issue "letsencrypt.org"`},
		}}
	text := Render(z)
	for _, want := range []string{"$ORIGIN shop.ng.", "@ 3600 IN SOA ns1.host.ng. admin.shop.ng. (", "@ 3600 IN NS ns2.host.ng.",
		"www 300 IN CNAME shop.ng.", "@ 3600 IN MX 10 mail.shop.ng.", `"v=spf1 a mx \"q\" ~all"`,
		"_sip._tcp 3600 IN SRV 1 2 5060 sip.other.com.", `k" "k`} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	recs, ns, skipped, err := Parse(text, "shop.ng")
	if err != nil || len(skipped) > 0 {
		t.Fatalf("parse: %v %v", err, skipped)
	}
	if len(ns) != 2 || len(recs) != len(z.Records) {
		t.Fatalf("got %d records, %v ns", len(recs), ns)
	}
	for _, want := range z.Records {
		w := want
		_ = w.Validate()
		found := false
		for _, r := range recs {
			if Same(r, w) {
				found = true
			}
		}
		if !found {
			t.Errorf("record lost in round trip: %+v", want)
		}
	}
}

func TestParseCpanelStyleZone(t *testing.T) {
	text := `; cPanel first:11.110
$TTL 14400
example.com.	86400	IN	SOA	ns1.host.com.	admin.host.com.	(
				2024010101	;Serial Number
				3600	;refresh
				1800	;retry
				1209600	;expire
				86400	)
example.com.	86400	IN	NS	ns1.host.com.
example.com.	14400	IN	A	192.0.2.1
mail	IN	CNAME	example.com.
www	14400	IN	CNAME	example.com.
example.com.	14400	IN	MX	0	example.com.
default._domainkey	14400	IN	TXT	("v=DKIM1; k=rsa; " "p=MIIBIjAN")
ftp 1h IN A 192.0.2.1
	IN AAAA 2001:db8::5
$GENERATE 1-5 host$ A 10.0.0.$
`
	recs, ns, skipped, err := Parse(text, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || len(skipped) != 1 {
		t.Fatalf("ns %v skipped %v", ns, skipped)
	}
	var dkim, ftp6, mx bool
	for _, r := range recs {
		if r.Name == "default._domainkey" && r.Content == "v=DKIM1; k=rsa; p=MIIBIjAN" {
			dkim = true
		}
		if r.Name == "ftp" && r.Type == "AAAA" && r.TTL == 14400 {
			ftp6 = true
		}
		if r.Type == "MX" && r.Content == "@" && r.Priority == 0 {
			mx = true
		}
	}
	if !dkim || !ftp6 || !mx {
		t.Fatalf("dkim=%v ftp6=%v mx=%v %+v", dkim, ftp6, mx, recs)
	}
}

func TestCreateLocalZoneAndDNSSEC(t *testing.T) {
	m, run := testManager(t)
	ctx := context.Background()
	if _, err := m.UpdateSettings(ctx, SettingsUpdate{Nameservers: []Nameserver{{Host: "ns1.host.ng", IPv4: "203.0.113.10"}, {Host: "ns2.host.ng", IPv4: "203.0.113.11"}},
		Secondaries: []string{"198.51.100.7"}}); err != nil {
		t.Fatal(err)
	}
	z, err := m.Create(ctx, "shop", CreateZone{Name: "Shop.NG", Template: "website-email"})
	if err != nil {
		t.Fatal(err)
	}
	if z.Backend != "local" || len(z.Nameservers) != 2 || z.Serial < 2026000000 {
		t.Fatalf("zone: %+v", z)
	}
	conf := readFile(t, filepath.Join(m.env.Cfg.Root, m.env.Cfg.PdnsBindConf))
	if !strings.Contains(conf, `zone "shop.ng" {`) || !strings.Contains(conf, "/var/lib/ike/dns/zones/shop.ng.zone") {
		t.Fatalf("bind conf:\n%s", conf)
	}
	zf := readFile(t, filepath.Join(m.env.Cfg.Root, m.zoneFile("shop.ng")))
	for _, want := range []string{"@ 3600 IN A 203.0.113.10", "@ 3600 IN MX 10 mail.shop.ng.", "mail 3600 IN AAAA 2001:db8::10", "v=spf1 a mx ip4:203.0.113.10 ip6:2001:db8::10 ~all"} {
		if !strings.Contains(zf, want) {
			t.Errorf("zone file missing %q:\n%s", want, zf)
		}
	}
	hist := strings.Join(run.History(), "\n")
	for _, want := range []string{"pdns_control rediscover", "pdns_control bind-reload-now shop.ng", "pdnsutil check-zone shop.ng",
		"pdnsutil set-meta shop.ng ALSO-NOTIFY 198.51.100.7", "pdnsutil set-meta shop.ng ALLOW-AXFR-FROM 198.51.100.7", "pdns_control notify shop.ng"} {
		if !strings.Contains(hist, want) {
			t.Errorf("missing command %q", want)
		}
	}
	// glue for ns inside the zone is added automatically
	z2, err := m.Create(ctx, "hostco", CreateZone{Name: "host.ng"})
	if err != nil {
		t.Fatal(err)
	}
	if len(z2.Records) != 2 {
		t.Fatalf("glue records: %+v", z2.Records)
	}
	// another account can't create a subdomain of shop.ng
	if _, err := m.Create(ctx, "evil", CreateZone{Name: "pay.shop.ng"}); err == nil {
		t.Fatal("overlapping zone accepted")
	}
	// DNSSEC
	z, err = m.UpdateZone(ctx, "shop", "shop.ng", ZoneSettings{DNSSEC: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(z.DS) != 1 || z.DS[0] != "12345 13 2 ABCDEF0123456789" {
		t.Fatalf("DS: %v", z.DS)
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "pdnsutil secure-zone shop.ng") {
		t.Fatal("zone not signed")
	}
	// records: add, conflict, delete
	z, err = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "blog", Type: "A", Content: "203.0.113.20"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "blog", Type: "CNAME", Content: "x.com."}); err == nil {
		t.Fatal("CNAME next to A accepted")
	}
	if _, err := m.AddRecord(ctx, "other", "shop.ng", Record{Name: "x", Type: "A", Content: "1.2.3.4"}); err == nil {
		t.Fatal("other account changed the zone")
	}
	var id string
	for _, r := range z.Records {
		if r.Name == "blog" {
			id = r.ID
		}
	}
	if z, err = m.DeleteRecord(ctx, "shop", "shop.ng", id); err != nil {
		t.Fatal(err)
	}
	// failed check rolls back the zone file
	before := readFile(t, filepath.Join(m.env.Cfg.Root, m.zoneFile("shop.ng")))
	run.FailOn = "pdnsutil check-zone"
	if _, err := m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "x", Type: "A", Content: "1.2.3.4"}); err == nil {
		t.Fatal("expected failure")
	}
	run.FailOn = ""
	if after := readFile(t, filepath.Join(m.env.Cfg.Root, m.zoneFile("shop.ng"))); after != before {
		t.Fatal("zone file not restored")
	}
	if got, _ := m.Get("shop", "shop.ng"); len(got.Records) != len(z.Records) {
		t.Fatal("state changed after a failed publish")
	}
	// templates: Google replaces MX and SPF
	z, err = m.ApplyTemplate(ctx, "shop", "shop.ng", "google", "", "")
	if err != nil {
		t.Fatal(err)
	}
	mx := 0
	for _, r := range z.Records {
		if r.Type == "MX" {
			mx++
			if r.Content != "smtp.google.com." {
				t.Fatalf("old MX kept: %+v", r)
			}
		}
	}
	if mx != 1 {
		t.Fatalf("MX count %d", mx)
	}
	// secondary zone
	sz, err := m.Create(ctx, "shop", CreateZone{Name: "backup.ng", Kind: "secondary", Primaries: []string{"198.51.100.1"}})
	if err != nil {
		t.Fatal(err)
	}
	conf = readFile(t, filepath.Join(m.env.Cfg.Root, m.env.Cfg.PdnsBindConf))
	if !strings.Contains(conf, "type slave;\n  masters { 198.51.100.1; };") {
		t.Fatalf("secondary conf:\n%s", conf)
	}
	if _, err := m.AddRecord(ctx, "shop", sz.Name, Record{Name: "x", Type: "A", Content: "1.2.3.4"}); err == nil {
		t.Fatal("secondary zone edited")
	}
	if err := m.Delete(ctx, "shop", "backup.ng"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, filepath.Join(m.env.Cfg.Root, m.env.Cfg.PdnsBindConf)), "backup.ng") {
		t.Fatal("deleted zone still listed")
	}
	// ACME records come and go
	if err := m.SetACMEChallenge(ctx, "*.shop.ng", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, filepath.Join(m.env.Cfg.Root, m.zoneFile("shop.ng"))), "_acme-challenge 60 IN TXT") {
		t.Fatal("challenge not published")
	}
	if err := m.CleanupACME(ctx, "shop.ng"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, filepath.Join(m.env.Cfg.Root, m.zoneFile("shop.ng"))), "_acme-challenge") {
		t.Fatal("challenge not removed")
	}
	if !m.CanIssueWildcard("*.shop.ng") || m.CanIssueWildcard("*.nothere.com") {
		t.Fatal("CanIssueWildcard")
	}
	// managed mail records replace each other
	if _, err := m.SetManaged(ctx, "dkim", "shop.ng", []Record{{Name: "ike._domainkey", Type: "TXT", Content: "v=DKIM1; p=AAA"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetManaged(ctx, "dkim", "shop.ng", []Record{{Name: "ike._domainkey", Type: "TXT", Content: "v=DKIM1; p=BBB"}}); err != nil {
		t.Fatal(err)
	}
	z, _ = m.Get("", "shop.ng")
	n := 0
	for _, r := range z.Records {
		if r.Managed == "dkim" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d dkim records", n)
	}
}

// fakeProvider records calls.
type fakeProvider struct {
	recs    []Record
	created []Record
	deleted []string
}

func (f *fakeProvider) EnsureZone(context.Context, string) (string, []string, error) {
	return "zid", []string{"ana.ns.cloudflare.com", "bob.ns.cloudflare.com"}, nil
}
func (f *fakeProvider) List(context.Context, string, string) ([]Record, error) { return f.recs, nil }
func (f *fakeProvider) Create(_ context.Context, _, _ string, r Record) error {
	f.created = append(f.created, r)
	return nil
}
func (f *fakeProvider) Delete(_ context.Context, _, _, id string) error {
	f.deleted = append(f.deleted, id)
	return nil
}
func (f *fakeProvider) DeleteZone(context.Context, string) error { return nil }

func TestProviderSync(t *testing.T) {
	m, _ := testManager(t)
	fp := &fakeProvider{recs: []Record{
		{ID: "r1", Name: "@", Type: "NS", TTL: 3600, Content: "ana.ns.cloudflare.com."},
		{ID: "r2", Name: "old", Type: "A", TTL: 3600, Content: "192.0.2.9"},
		{ID: "r3", Name: "@", Type: "A", TTL: 3600, Content: "203.0.113.10"},
	}}
	m.providers = func(string) (Provider, error) { return fp, nil }
	z, err := m.Create(context.Background(), "shop", CreateZone{Name: "cf.ng", Backend: "cloudflare", Template: "website"})
	if err != nil {
		t.Fatal(err)
	}
	if z.ProviderID != "zid" || z.Nameservers[0] != "ana.ns.cloudflare.com" {
		t.Fatalf("%+v", z)
	}
	if len(fp.deleted) != 1 || fp.deleted[0] != "r2" {
		t.Fatalf("deleted %v", fp.deleted)
	}
	for _, c := range fp.created {
		if c.Type == "A" && c.Name == "@" {
			t.Fatal("existing record re-created")
		}
	}
	if len(fp.created) != 4 { // AAAA, www, 2×CAA
		t.Fatalf("created %+v", fp.created)
	}
	if _, err := m.UpdateZone(context.Background(), "shop", "cf.ng", ZoneSettings{DNSSEC: true}); err == nil {
		t.Fatal("DNSSEC on provider accepted")
	}
}

// fakeHTTP answers by URL substring.
type fakeHTTP struct {
	answers map[string]string
	got     []*http.Request
	bodies  []string
}

func (f *fakeHTTP) Do(r *http.Request) (*http.Response, error) {
	f.got = append(f.got, r)
	b := ""
	if r.Body != nil {
		bb, _ := io.ReadAll(r.Body)
		b = string(bb)
	}
	f.bodies = append(f.bodies, b)
	for k, v := range f.answers {
		if strings.Contains(r.URL.String()+"?"+b, k) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(v)), Header: http.Header{}}, nil
		}
	}
	return &http.Response{StatusCode: 404, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("{}")), Header: http.Header{}}, nil
}

func TestRDAPAndNamecheap(t *testing.T) {
	m, _ := testManager(t)
	fh := &fakeHTTP{answers: map[string]string{
		"rdap.org/domain/shop.ng": `{"events":[{"eventAction":"registration","eventDate":"2020-01-01T00:00:00Z"},{"eventAction":"expiration","eventDate":"2027-03-04T05:06:07Z"}],
			"status":["client transfer prohibited"],"nameservers":[{"ldhName":"NS1.HOST.NG"}],
			"entities":[{"roles":["registrar"],"vcardArray":["vcard",[["version",{},"text","4.0"],["fn",{},"text","Whogohost Ltd"]]]}]}`,
		"Command=namecheap.domains.check": `<?xml version="1.0"?><ApiResponse Status="OK"><Errors/><CommandResponse Type="namecheap.domains.check">
			<DomainCheckResult Domain="shop.com" Available="true" IsPremiumName="false" PremiumRegistrationPrice="0"/>
			<DomainCheckResult Domain="google.com" Available="false" IsPremiumName="false"/></CommandResponse></ApiResponse>`,
		"Command=namecheap.domains.dns.setCustom": `<ApiResponse Status="ERROR"><Errors><Error Number="1011150">Invalid request IP: 1.2.3.4</Error></Errors></ApiResponse>`,
	}}
	m.http = fh
	reg, err := m.LookupRegistration(context.Background(), "www.shop.ng")
	if err != nil {
		t.Fatal(err)
	}
	if reg.Registrar != "Whogohost Ltd" || reg.Expires.Year() != 2027 || reg.Nameservers[0] != "ns1.host.ng" {
		t.Fatalf("%+v", reg)
	}
	if _, err := m.SetRegistrar(Registrar{Kind: "namecheap", APIUser: "marvellous", Username: "marvellous", APIKey: "0123456789abcdef0123",
		Contact: Contact{FirstName: "Marvellous", LastName: "Okwor", Address1: "1 Marina", City: "Lagos", State: "Lagos", PostalCode: "101001",
			Country: "NG", Phone: "+234.8031234567", Email: "a@b.ng"}}); err != nil {
		t.Fatal(err)
	}
	av, err := m.CheckAvailability(context.Background(), []string{"shop.com", "google.com"})
	if err != nil || len(av) != 2 || !av[0].Available || av[1].Available {
		t.Fatalf("%+v %v", av, err)
	}
	last := fh.bodies[len(fh.bodies)-1]
	if !strings.Contains(last, "ClientIp=203.0.113.10") || !strings.Contains(last, "ApiKey=0123456789abcdef0123") {
		t.Fatalf("request body: %s", last)
	}
	err = m.SetRegistrarNameservers(context.Background(), "shop.com", []string{"ns1.host.ng", "ns2.host.ng"})
	if err == nil || !strings.Contains(err.Error(), "allow-list 203.0.113.10") {
		t.Fatalf("expected IP allow-list hint, got %v", err)
	}
	ps, _ := m.Settings()
	b, _ := json.Marshal(ps)
	if strings.Contains(string(b), "0123456789abcdef") {
		t.Fatal("API key leaked in settings")
	}
}

func TestRegistrable(t *testing.T) {
	for in, want := range map[string]string{"a.b.shop.com.ng": "shop.com.ng", "www.example.com": "example.com", "example.co.uk": "example.co.uk", "ng": "ng"} {
		if got := registrable(in); got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
}

// stubResolver returns fixed answers.
type stubResolver struct{ ip map[string][]string }

func (s stubResolver) LookupIP(_ context.Context, _, host string) ([]net.IP, error) {
	var out []net.IP
	for _, v := range s.ip[host] {
		out = append(out, net.ParseIP(v))
	}
	if len(out) == 0 {
		return nil, &net.DNSError{Err: "no such host", IsNotFound: true}
	}
	return out, nil
}
func (stubResolver) LookupCNAME(context.Context, string) (string, error) { return "shop.ng.", nil }
func (stubResolver) LookupMX(context.Context, string) ([]*net.MX, error) {
	return []*net.MX{{Host: "mail.shop.ng.", Pref: 10}}, nil
}
func (stubResolver) LookupTXT(context.Context, string) ([]string, error) { return nil, nil }
func (stubResolver) LookupNS(context.Context, string) ([]*net.NS, error) {
	return []*net.NS{{Host: "ns1.host.ng."}, {Host: "ns2.host.ng."}}, nil
}
func (stubResolver) LookupSRV(context.Context, string, string, string) (string, []*net.SRV, error) {
	return "", nil, nil
}
func (stubResolver) LookupAddr(context.Context, string) ([]string, error) {
	return []string{"server.host.ng."}, nil
}

func TestPropagationAndReverse(t *testing.T) {
	m, _ := testManager(t)
	m.resolver = func(string) Resolver {
		return stubResolver{ip: map[string][]string{"shop.ng": {"203.0.113.10"}, "server.host.ng": {"203.0.113.10"}}}
	}
	ctx := context.Background()
	_, _ = m.UpdateSettings(ctx, SettingsUpdate{Nameservers: []Nameserver{{Host: "ns1.host.ng"}, {Host: "ns2.host.ng"}}})
	if _, err := m.Create(ctx, "shop", CreateZone{Name: "shop.ng"}); err != nil {
		t.Fatal(err)
	}
	_, _ = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "@", Type: "A", Content: "203.0.113.10"})
	_, _ = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "@", Type: "MX", Content: "mail", Priority: 10})
	_, _ = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "new", Type: "A", Content: "203.0.113.11"})
	p, err := m.Check(ctx, "shop", "shop.ng")
	if err != nil {
		t.Fatal(err)
	}
	if !p.Delegation {
		t.Fatalf("delegation: %+v", p)
	}
	got := map[string]bool{}
	for _, r := range p.Records {
		got[r.Name+" "+r.Type] = r.Propagated
	}
	if !got["@ A"] || !got["@ MX"] || got["new A"] {
		t.Fatalf("%+v", got)
	}
	rc := m.ReverseDNS(ctx)
	if len(rc) != 2 || !rc[0].OK {
		t.Fatalf("%+v", rc)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := kitRead(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func kitRead(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
