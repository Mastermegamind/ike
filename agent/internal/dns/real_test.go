package dns

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
)

// TestRealPowerDNS runs a real PowerDNS server with the bind backend and
// checks that the zones Ike writes load, sign and answer queries.
// IKE_REAL_DNS=1 go test ./internal/dns -run RealPowerDNS
func TestRealPowerDNS(t *testing.T) {
	if os.Getenv("IKE_REAL_DNS") == "" {
		t.Skip("set IKE_REAL_DNS=1 (needs pdns-server and pdns-backend-bind)")
	}
	dir := t.TempDir()
	conf := filepath.Join(dir, "pdns.conf")
	pdnsConf := strings.Join([]string{
		"launch=bind", "bind-config=" + filepath.Join(dir, "ike-zones.conf"), "bind-dnssec-db=" + filepath.Join(dir, "dnssec.sqlite3"),
		"local-address=127.0.0.1", "local-port=15353", "socket-dir=" + dir, "guardian=no", "daemon=no", "disable-syslog=yes",
		"loglevel=4", "primary=yes", "default-soa-content=ns1.host.ng. hostmaster.@ 0 10800 3600 604800 3600",
	}, "\n") + "\n"
	must(t, os.WriteFile(conf, []byte(pdnsConf), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "ike-zones.conf"), nil, 0o644))
	// wrappers so the manager's plain "pdnsutil" and "pdns_control" use this config
	bin := filepath.Join(dir, "bin")
	must(t, os.MkdirAll(bin, 0o755))
	for _, p := range []string{"pdnsutil", "pdns_control"} {
		real, err := exec.LookPath(p)
		if err != nil {
			t.Skip(p + " not installed")
		}
		must(t, os.WriteFile(filepath.Join(bin, p), []byte("#!/bin/sh\nexec "+real+" --config-dir="+dir+" \"$@\"\n"), 0o755))
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	if out, err := exec.Command("pdnsutil", "create-bind-db", filepath.Join(dir, "dnssec.sqlite3")).CombinedOutput(); err != nil {
		t.Fatalf("create-bind-db: %v %s", err, out)
	}
	srv := exec.Command("pdns_server", "--config-dir="+dir)
	srv.Stdout, srv.Stderr = os.Stderr, os.Stderr
	must(t, srv.Start())
	defer func() { _ = srv.Process.Kill(); _ = srv.Wait() }()
	time.Sleep(1500 * time.Millisecond)

	cfg := config.Default()
	cfg.Root, cfg.DNSServer = "/", "powerdns"
	cfg.DNSDir, cfg.PdnsBindConf = filepath.Join(dir, "dns"), filepath.Join(dir, "ike-zones.conf")
	cfg.PublicIPv4, cfg.PublicIPv6 = "203.0.113.10", "2001:db8::10"
	keys, err := secrets.Open(filepath.Join(dir, "master.key"))
	must(t, err)
	m := New(&kit.Env{Cfg: cfg, Run: runner.Exec{}}, keys)
	ctx := context.Background()
	_, err = m.UpdateSettings(ctx, SettingsUpdate{Nameservers: []Nameserver{{Host: "ns1.host.ng"}, {Host: "ns2.host.ng"}}, SOAEmail: "hostmaster@host.ng"})
	must(t, err)
	z, err := m.Create(ctx, "shop", CreateZone{Name: "shop.ng", Template: "website-email"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "dkim._domainkey", Type: "TXT", Content: "v=DKIM1; k=rsa; p=" + strings.Repeat("A", 400)})
	must(t, err)
	_, err = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "_sip._tcp", Type: "SRV", Priority: 10, Weight: 5, Port: 5060, Content: "sip"})
	must(t, err)
	_, err = m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "@", Type: "NAPTR", Content: `100 10 "S" "SIP+D2U" "" _sip._udp`})
	must(t, err)

	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, n, "127.0.0.1:15353")
	}}
	m.resolver = func(string) Resolver { return r }
	ips, err := r.LookupIP(ctx, "ip4", "shop.ng")
	if err != nil || len(ips) != 1 || ips[0].String() != "203.0.113.10" {
		t.Fatalf("A: %v %v", ips, err)
	}
	mx, err := r.LookupMX(ctx, "shop.ng")
	if err != nil || len(mx) != 1 || mx[0].Host != "mail.shop.ng." {
		t.Fatalf("MX: %v %v", mx, err)
	}
	txt, err := r.LookupTXT(ctx, "dkim._domainkey.shop.ng")
	if err != nil || len(txt) != 1 || len(txt[0]) != len("v=DKIM1; k=rsa; p=")+400 {
		t.Fatalf("TXT: %v %v", txt, err)
	}
	// DNSSEC signs and returns a DS
	z, err = m.UpdateZone(ctx, "shop", "shop.ng", ZoneSettings{DNSSEC: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(z.DS) == 0 {
		t.Fatal("no DS record after signing")
	}
	t.Logf("DS: %v", z.DS)
	// a broken zone is refused and the old one keeps answering
	before, _ := os.ReadFile(filepath.Join(cfg.DNSDir, "zones", "shop.ng.zone"))
	if _, err := m.AddRecord(ctx, "shop", "shop.ng", Record{Name: "www", Type: "A", Content: "1.2.3.4"}); err == nil {
		t.Fatal("CNAME conflict accepted")
	}
	after, _ := os.ReadFile(filepath.Join(cfg.DNSDir, "zones", "shop.ng.zone"))
	if string(before) != string(after) {
		t.Fatal("zone file changed after refused change")
	}
	// ACME challenge is visible on the server
	if err := m.SetACMEChallenge(ctx, "*.shop.ng", "challenge-value-0123456789abcdefghij"); err != nil {
		t.Fatal(err)
	}
	txt, err = r.LookupTXT(ctx, "_acme-challenge.shop.ng")
	if err != nil || len(txt) != 1 {
		t.Fatalf("ACME TXT: %v %v", txt, err)
	}
	must(t, m.CleanupACME(ctx, "shop.ng"))
	if _, err := r.LookupTXT(ctx, "_acme-challenge.shop.ng"); err == nil {
		t.Fatal("challenge still served")
	}
	// delete removes the zone from the server
	must(t, m.Delete(ctx, "shop", "shop.ng"))
	time.Sleep(300 * time.Millisecond)
	if _, err := r.LookupIP(ctx, "ip4", "shop.ng"); err == nil {
		t.Fatal("zone still served after delete")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
