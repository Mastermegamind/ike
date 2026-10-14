package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/jobs"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
)

type testAPI struct {
	t   *testing.T
	h   http.Handler
	run *runner.DryRun
	srv *Server
}

func newTestAPI(t *testing.T, tweak func(*config.Config)) *testAPI {
	t.Helper()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = t.TempDir(), true
	cfg.DNSServer, cfg.PublicIPv4, cfg.Hostname = "powerdns", "203.0.113.10", "server.host.ng"
	if tweak != nil {
		tweak(&cfg)
	}
	run := &runner.DryRun{Responses: map[string]string{"id -u": "1001"}}
	mgr := sites.NewManager(cfg, run)
	srv := &Server{Sites: mgr, Jobs: jobs.NewManager(), Runner: run, Config: cfg}
	if err := srv.Setup(); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateAccount(context.Background(), "shop"); err != nil {
		t.Fatal(err)
	}
	return &testAPI{t: t, h: srv.Handler(), run: run, srv: srv}
}

// call sends a request as the admin (role "") or with a role header, and
// decodes the JSON answer into out.
func (a *testAPI) call(method, path string, body any, role string, out any) int {
	a.t.Helper()
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	if role != "" {
		req.Header.Set("X-Ike-Role", role)
	}
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	if out != nil && w.Code < 300 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			a.t.Fatalf("%s %s: %v\n%s", method, path, err, w.Body.String())
		}
	}
	if w.Code >= 300 && out == nil {
		a.t.Logf("%s %s → %d %s", method, path, w.Code, strings.TrimSpace(w.Body.String()))
	}
	return w.Code
}

func TestDNSRoutes(t *testing.T) {
	a := newTestAPI(t, nil)
	if c := a.call("PUT", "/v1/dns", map[string]any{"nameservers": []map[string]string{{"host": "ns1.host.ng"}, {"host": "ns2.host.ng"}}}, "user", nil); c != 403 {
		t.Fatalf("account owner changed DNS settings: %d", c)
	}
	if c := a.call("PUT", "/v1/dns", map[string]any{"nameservers": []map[string]string{{"host": "ns1.host.ng"}, {"host": "ns2.host.ng"}},
		"cloudflare_token": "abcdefgh12345678"}, "", nil); c != 200 {
		t.Fatalf("settings: %d", c)
	}
	var info map[string]any
	a.call("GET", "/v1/dns", nil, "", &info)
	b, _ := json.Marshal(info)
	if strings.Contains(string(b), "abcdefgh12345678") || !strings.Contains(string(b), `"cloudflare_token":"set"`) {
		t.Fatalf("token handling: %s", b)
	}
	var z map[string]any
	if c := a.call("POST", "/v1/accounts/shop/dns/zones", map[string]any{"name": "shop.ng", "template": "website"}, "user", &z); c != 201 {
		t.Fatalf("create zone: %d", c)
	}
	if c := a.call("POST", "/v1/accounts/shop/dns/zones", map[string]any{"name": "x.ng", "kind": "secondary", "primaries": []string{"1.2.3.4"}}, "user", nil); c == 201 {
		t.Fatal("account owner made a secondary zone")
	}
	if c := a.call("POST", "/v1/accounts/shop/dns/zones/shop.ng/records", map[string]any{"name": "api", "type": "A", "content": "203.0.113.50"}, "user", &z); c != 201 {
		t.Fatalf("add record: %d", c)
	}
	if c := a.call("GET", "/v1/accounts/nobody/dns/zones/shop.ng", nil, "", nil); c != 404 {
		t.Fatalf("zone visible under another account: %d", c)
	}
	req := httptest.NewRequest("GET", "/v1/accounts/shop/dns/zones/shop.ng/export", nil)
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "api 3600 IN A 203.0.113.50") {
		t.Fatalf("export:\n%s", w.Body.String())
	}
	var imp map[string]any
	if c := a.call("POST", "/v1/accounts/shop/dns/zones/shop.ng/import", map[string]any{"zone_file": "blog 300 IN A 203.0.113.60\nweird 300 IN HINFO a b\n"}, "user", &imp); c != 200 {
		t.Fatalf("import: %d", c)
	}
	if sk, _ := imp["skipped"].([]any); len(sk) != 1 {
		t.Fatalf("skipped: %v", imp["skipped"])
	}
	if c := a.call("POST", "/v1/dns/acme", map[string]string{"domain": "shop.ng", "value": "abcdefghijklmnopqrstuvwxyz"}, "user", nil); c != 403 {
		t.Fatalf("acme as user: %d", c)
	}
	if c := a.call("POST", "/v1/dns/acme", map[string]string{"domain": "shop.ng", "value": "abcdefghijklmnopqrstuvwxyz"}, "", nil); c != 200 {
		t.Fatalf("acme: %d", c)
	}
	var evs []map[string]any
	a.call("GET", "/v1/events", nil, "", &evs)
	if len(evs) < 3 || evs[0]["type"] != "dns.zone.created" {
		t.Fatalf("events: %v", evs)
	}
	if c := a.call("DELETE", "/v1/accounts/shop/dns/zones/shop.ng", nil, "user", nil); c != 200 {
		t.Fatalf("delete: %d", c)
	}
}

func TestServicesAndSecrets(t *testing.T) {
	a := newTestAPI(t, nil)
	if c := a.call("POST", "/v1/services/ike-agent/stop", nil, "", nil); c != 400 {
		t.Fatalf("stopping ike-agent: %d", c)
	}
	if c := a.call("POST", "/v1/services/ssh/stop", nil, "", nil); c != 400 {
		t.Fatalf("ssh is not in the list: %d", c)
	}
	if c := a.call("POST", "/v1/services/nginx/restart", nil, "", nil); c != 200 {
		t.Fatalf("restart nginx: %d", c)
	}
	if c := a.call("POST", "/v1/services/nginx/stop", nil, "reseller", nil); c != 403 {
		t.Fatalf("reseller stopped nginx: %d", c)
	}
	if !strings.Contains(strings.Join(a.run.History(), "\n"), "systemctl restart nginx") {
		t.Fatal("no restart")
	}
	var st map[string]any
	if c := a.call("POST", "/v1/secrets/rotate", nil, "", &st); c != 200 || st["keys"].(float64) != 1 {
		t.Fatalf("rotate: %d %v", c, st)
	}
}

func TestMailRoutes(t *testing.T) {
	a := newTestAPI(t, func(c *config.Config) { c.MailServer, c.SpamFilter = "postfix", "rspamd" })
	var zone map[string]any
	a.call("POST", "/v1/accounts/shop/dns/zones", map[string]any{"name": "shop.ng"}, "user", &zone)
	var d map[string]any
	if c := a.call("POST", "/v1/accounts/shop/mail/domains", map[string]string{"domain": "shop.ng"}, "user", &d); c != 201 {
		t.Fatalf("add domain %d", c)
	}
	// mail records went into the zone
	a.call("GET", "/v1/accounts/shop/dns/zones/shop.ng", nil, "", &zone)
	b, _ := json.Marshal(zone)
	for _, want := range []string{`"type":"MX"`, `ike._domainkey`, `"managed":"mail:shop.ng"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("zone missing %s", want)
		}
	}
	var mb map[string]any
	if c := a.call("POST", "/v1/accounts/shop/mail/domains/shop.ng/mailboxes", map[string]any{"local": "info"}, "user", &mb); c != 201 || mb["password"] == "" {
		t.Fatalf("mailbox %d %v", c, mb)
	}
	if c := a.call("PUT", "/v1/mail", map[string]any{}, "user", nil); c != 403 {
		t.Fatalf("user changed mail settings: %d", c)
	}
	if c := a.call("GET", "/v1/mail/queue", nil, "user", nil); c != 403 {
		t.Fatalf("user saw the whole queue: %d", c)
	}
	var job map[string]any
	if c := a.call("POST", "/v1/accounts/shop/mail/domains/shop.ng/tls", nil, "user", &job); c != 202 {
		t.Fatalf("tls job %d", c)
	}
	a.waitJob(job["id"].(string))
	var recs []map[string]any
	a.call("GET", "/v1/accounts/shop/mail/domains/shop.ng/records", nil, "user", &recs)
	if len(recs) < 6 {
		t.Fatalf("records %v", recs)
	}
}

func (a *testAPI) waitJob(id string) map[string]any {
	a.t.Helper()
	for i := 0; i < 200; i++ {
		var j map[string]any
		a.call("GET", "/v1/jobs/"+id, nil, "", &j)
		if j["status"] != "running" {
			if j["status"] != "done" {
				a.t.Fatalf("job failed: %v", j)
			}
			return j
		}
		time.Sleep(20 * time.Millisecond)
	}
	a.t.Fatal("job still running")
	return nil
}
