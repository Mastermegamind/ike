package panel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/api"
	"github.com/YOUR-ORG/ike/agent/internal/client"
	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/firewall"
	"github.com/YOUR-ORG/ike/agent/internal/jobs"
	"github.com/YOUR-ORG/ike/agent/internal/panel/auth"
	"github.com/YOUR-ORG/ike/agent/internal/panel/billing"
	"github.com/YOUR-ORG/ike/agent/internal/panel/store"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/updates"
)

type env struct {
	t        *testing.T
	srv      *Server
	panel    *httptest.Server
	admin    *httptest.Server
	reseller *httptest.Server
	st       *store.Store
}

func setup(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc/php/8.3/fpm/pool.d"), 0o755)
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	r := &runner.DryRun{}
	jm := jobs.NewManager()
	ag := &api.Server{Sites: sites.NewManager(cfg, r), Config: cfg, Runner: r, Jobs: jm,
		Firewall: &firewall.Manager{Run: r, StateFile: filepath.Join(root, "fw.json"), Protected: cfg.ProtectedPorts},
		Updates:  &updates.Manager{Run: r, Jobs: jm, Root: root}}
	agentHTTP := httptest.NewServer(ag.Handler())
	t.Cleanup(agentHTTP.Close)

	st, err := store.Open(filepath.Join(root, "panel.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, _ := auth.HashPassword("admin-password-1")
	_ = st.Update(func(d *store.Data) error {
		return d.AddPerson(&store.Person{Username: "andre", Email: "andre@ike.ng", Role: store.Admin, PasswordHash: h})
	})
	s := &Server{Store: st, Agent: &client.Client{BaseURL: agentHTTP.URL, HTTP: http.DefaultClient}, PublicURL: "https://panel.test"}
	e := &env{t: t, srv: s, st: st}
	e.panel = httptest.NewServer(s.Handler(Listener{Name: "panel", Roles: []store.Role{store.User, store.Reseller, store.Admin}}))
	e.reseller = httptest.NewServer(s.Handler(Listener{Name: "reseller", Roles: []store.Role{store.Reseller, store.Admin}}))
	e.admin = httptest.NewServer(s.Handler(Listener{Name: "admin", Roles: []store.Role{store.Admin}}))
	t.Cleanup(func() { e.panel.Close(); e.reseller.Close(); e.admin.Close() })
	return e
}

type browser struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (e *env) browser(srv *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: e.t, base: srv.URL, c: &http.Client{Jar: jar}}
}

func (b *browser) do(method, path string, body any, out any) int {
	b.t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, b.base+path, rd)
	req.Header.Set("X-Requested-With", "ike")
	resp, err := b.c.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	return resp.StatusCode
}

func (b *browser) login(user, pass, code string) int {
	return b.do("POST", "/api/v1/session", map[string]string{"username": user, "password": pass, "code": code}, nil)
}

func TestLoginAnd2FA(t *testing.T) {
	e := setup(t)
	b := e.browser(e.admin)
	if code := b.login("andre", "wrong-password-x", ""); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code := b.do("GET", "/api/v1/sites", nil, nil); code != 401 {
		t.Fatalf("unauthenticated access: %d", code)
	}
	if code := b.login("andre", "admin-password-1", ""); code != 200 {
		t.Fatalf("login: %d", code)
	}
	var setup struct {
		Secret string `json:"secret"`
	}
	b.do("POST", "/api/v1/me/2fa/setup", nil, &setup)
	if setup.Secret == "" {
		t.Fatal("no secret")
	}
	var en struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if code := b.do("POST", "/api/v1/me/2fa/enable", map[string]string{"code": "000000"}, nil); code != 400 {
		t.Errorf("wrong 2FA code accepted: %d", code)
	}
	if code := b.do("POST", "/api/v1/me/2fa/enable", map[string]string{"code": auth.TOTPCode(setup.Secret, time.Now())}, &en); code != 200 || len(en.RecoveryCodes) != 8 {
		t.Fatalf("enable: %d %+v", code, en)
	}

	b2 := e.browser(e.admin)
	var res map[string]any
	if code := b2.do("POST", "/api/v1/session", map[string]string{"username": "andre", "password": "admin-password-1"}, &res); code != 401 || res["two_factor_required"] != true {
		t.Fatalf("2FA not required: %d %v", code, res)
	}
	if code := b2.login("andre", "admin-password-1", en.RecoveryCodes[0]); code != 200 {
		t.Fatalf("recovery code: %d", code)
	}
	if code := e.browser(e.admin).login("andre", "admin-password-1", en.RecoveryCodes[0]); code != 401 {
		t.Error("recovery code reused")
	}

	// CSRF: state-changing requests need the custom header.
	req, _ := http.NewRequest("POST", e.admin.URL+"/api/v1/accounts", strings.NewReader(`{"name":"x"}`))
	for _, c := range b2.c.Jar.Cookies(req.URL) {
		req.AddCookie(c)
	}
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != 403 {
		t.Errorf("missing CSRF header: %d", resp.StatusCode)
	}
}

func TestThrottleLogins(t *testing.T) {
	e := setup(t)
	b := e.browser(e.admin)
	for i := 0; i < 10; i++ {
		b.login("andre", "nope-nope-nope", "")
	}
	if code := b.login("andre", "admin-password-1", ""); code != 429 {
		t.Errorf("expected lockout, got %d", code)
	}
}

func TestRolesAndScoping(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	if code := admin.do("POST", "/api/v1/people", map[string]any{"username": "webafrica", "email": "ops@webafrica.ng", "role": "reseller",
		"password": "reseller-pass-1", "max_accounts": 2}, nil); code != 201 {
		t.Fatalf("create reseller: %d", code)
	}
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "adminown"}, nil)

	res := e.browser(e.reseller)
	if code := res.login("webafrica", "reseller-pass-1", ""); code != 200 {
		t.Fatalf("reseller login: %d", code)
	}
	if code := e.browser(e.admin).login("webafrica", "reseller-pass-1", ""); code != 401 {
		t.Errorf("reseller signed in on the admin port: %d", code)
	}
	for _, n := range []string{"adaeze", "kelechi"} {
		if code := res.do("POST", "/api/v1/accounts", map[string]string{"name": n}, nil); code != 201 {
			t.Fatalf("reseller create %s: %d", n, code)
		}
	}
	var msg map[string]string
	if code := res.do("POST", "/api/v1/accounts", map[string]string{"name": "third"}, &msg); code != 403 || !strings.Contains(msg["error"], "2 accounts") {
		t.Errorf("quota not enforced: %d %v", code, msg)
	}
	var accts []map[string]any
	res.do("GET", "/api/v1/accounts", nil, &accts)
	if len(accts) != 2 {
		t.Errorf("reseller sees %d accounts", len(accts))
	}
	if code := res.do("GET", "/api/v1/firewall", nil, nil); code != 403 {
		t.Errorf("reseller reached firewall: %d", code)
	}
	if code := res.do("POST", "/api/v1/sites", map[string]string{"domain": "shop.adaeze.com", "account": "adaeze"}, nil); code != 201 {
		t.Fatalf("reseller create site: %d", code)
	}
	if code := res.do("POST", "/api/v1/sites", map[string]string{"domain": "x.ng", "account": "adminown"}, nil); code != 403 {
		t.Errorf("reseller used someone else's account: %d", code)
	}
	if code := res.do("POST", "/api/v1/people", map[string]any{"username": "ada", "email": "ada@shop.adaeze.com", "role": "user",
		"password": "user-pass-123", "accounts": []string{"adaeze"}}, nil); code != 201 {
		t.Fatalf("reseller create user: %d", code)
	}
	if code := res.do("POST", "/api/v1/people", map[string]any{"username": "boss", "email": "b@x.ng", "role": "admin", "password": "user-pass-123"}, nil); code != 403 {
		t.Errorf("reseller created an admin: %d", code)
	}

	user := e.browser(e.panel)
	if code := user.login("ada", "user-pass-123", ""); code != 200 {
		t.Fatalf("user login: %d", code)
	}
	if code := e.browser(e.reseller).login("ada", "user-pass-123", ""); code != 401 {
		t.Error("account owner signed in on the reseller port")
	}
	var list []map[string]any
	user.do("GET", "/api/v1/sites", nil, &list)
	if len(list) != 1 || list[0]["domain"] != "shop.adaeze.com" {
		t.Errorf("user sites %+v", list)
	}
	if code := user.do("DELETE", "/api/v1/accounts/adaeze?with_sites=true", nil, nil); code != 403 {
		t.Errorf("user deleted their account: %d", code)
	}
	if code := user.do("POST", "/api/v1/accounts/adaeze/cron", map[string]any{"schedule": "@daily", "command": "php x.php"}, nil); code != 201 {
		t.Errorf("user cron: %d", code)
	}
	if code := user.do("GET", "/api/v1/accounts/kelechi", nil, nil); code != 403 {
		t.Errorf("user saw another account: %d", code)
	}
	if code := user.do("PUT", "/api/v1/sites/shop.adaeze.com/status", map[string]any{"status": "suspended"}, nil); code != 403 {
		t.Errorf("user suspended a site: %d", code)
	}
	if code := user.do("PUT", "/api/v1/sites/shop.adaeze.com/status", map[string]any{"status": "maintenance"}, nil); code != 200 {
		t.Errorf("user maintenance: %d", code)
	}
	if code := user.do("GET", "/api/v1/updates", nil, nil); code != 403 {
		t.Errorf("user reached updates: %d", code)
	}
}

func TestBillingSuspendAndPay(t *testing.T) {
	e := setup(t)
	paystackAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/transaction/initialize":
			w.Write([]byte(`{"status":true,"data":{"authorization_url":"https://checkout.paystack.com/x","reference":"REF-1"}}`))
		case strings.HasPrefix(r.URL.Path, "/transaction/verify/REF-1"):
			w.Write([]byte(`{"data":{"status":"success","amount":1500000,"currency":"NGN"}}`))
		}
	}))
	defer paystackAPI.Close()
	e.srv.Providers = func() map[string]billing.Provider {
		return map[string]billing.Provider{"paystack": billing.Paystack{SecretKey: "sk_test_k", BaseURL: paystackAPI.URL}}
	}
	_ = e.st.Update(func(d *store.Data) error { d.Billing.AutoSuspend, d.Billing.GraceDays = true, 0; return nil })

	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "naijabites"}, nil)
	var inv store.Invoice
	if code := admin.do("POST", "/api/v1/billing/invoices", map[string]any{"account": "naijabites", "description": "Business plan, October",
		"amount": "15000.00", "due_in_days": 1}, &inv); code != 201 || inv.Amount != 1500000 || inv.Currency != "NGN" {
		t.Fatalf("invoice: %d %+v", code, inv)
	}
	var co map[string]string
	if code := admin.do("POST", "/api/v1/billing/invoices/"+inv.ID+"/checkout", map[string]string{"provider": "paystack"}, &co); code != 200 || co["checkout_url"] == "" {
		t.Fatalf("checkout: %d %v", code, co)
	}

	// Overdue → suspended.
	e.srv.EnforceOverdue(context.Background(), time.Now().Add(48*time.Hour))
	var acct map[string]any
	admin.do("GET", "/api/v1/accounts/naijabites", nil, &acct)
	if acct["suspended"] != true || !strings.HasPrefix(acct["suspend_reason"].(string), "Overdue invoice "+inv.ID) {
		t.Fatalf("not suspended: %+v", acct)
	}

	// Bad signature is rejected.
	body := []byte(`{"event":"charge.success","data":{"id":991,"reference":"REF-1","status":"success"}}`)
	post := func(sig string) int {
		req, _ := http.NewRequest("POST", e.panel.URL+"/webhooks/paystack", bytes.NewReader(body))
		req.Header.Set("x-paystack-signature", sig)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("bad"); code != 400 {
		t.Errorf("bad signature: %d", code)
	}
	mac := hmac.New(sha512.New, []byte("sk_test_k"))
	mac.Write(body)
	if code := post(hex.EncodeToString(mac.Sum(nil))); code != 200 {
		t.Fatalf("webhook: %d", code)
	}
	if code := post(hex.EncodeToString(mac.Sum(nil))); code != 200 {
		t.Errorf("duplicate delivery should be accepted quietly: %d", code)
	}
	var invs []store.Invoice
	admin.do("GET", "/api/v1/billing/invoices", nil, &invs)
	if invs[0].Status != "paid" {
		t.Errorf("invoice %+v", invs[0])
	}
	admin.do("GET", "/api/v1/accounts/naijabites", nil, &acct)
	if acct["suspended"] != false {
		t.Errorf("account not reactivated: %+v", acct)
	}
}

func TestSecretsAreMasked(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	set := map[string]any{"currency": "NGN", "grace_days": 7, "auto_suspend": true, "providers": map[string]any{
		"bachs": map[string]any{"enabled": true, "secret_key": "sk_live_secret", "webhook_secret": "whsec_secret"}}}
	var out map[string]any
	if code := admin.do("PUT", "/api/v1/billing/settings", set, &out); code != 200 {
		t.Fatalf("%d %v", code, out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "sk_live_secret") || !strings.Contains(string(raw), "/webhooks/bachs") {
		t.Errorf("secret leaked or webhook URL missing: %s", raw)
	}
	// Saving the masked value keeps the real secret.
	set["providers"].(map[string]any)["bachs"].(map[string]any)["secret_key"] = mask
	admin.do("PUT", "/api/v1/billing/settings", set, nil)
	var kept string
	e.st.View(func(d *store.Data) { kept = d.Billing.Providers["bachs"].SecretKey })
	if kept != "sk_live_secret" {
		t.Errorf("secret lost: %q", kept)
	}
}
