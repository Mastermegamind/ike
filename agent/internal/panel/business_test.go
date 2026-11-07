package panel

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/panel/auth"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

// api calls the panel with an API token instead of a session.
func tokenCall(t *testing.T, base, token, method, path string, body any, out any) int {
	t.Helper()
	var rd io.Reader
	if body != nil {
		j, _ := json.Marshal(body)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequest(method, base+path, rd)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	if resp.StatusCode >= 300 {
		t.Logf("%s %s → %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return resp.StatusCode
}

func addReseller(t *testing.T, e *env, name string, maxAccounts, maxSites int) {
	t.Helper()
	h, _ := auth.HashPassword("reseller-password-1")
	if err := e.st.Update(func(d *store.Data) error {
		return d.AddPerson(&store.Person{Username: name, Email: name + "@ike.ng", Role: store.Reseller, PasswordHash: h, MaxAccounts: maxAccounts, MaxSites: maxSites})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAPITokens(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	var tok map[string]any
	if c := admin.do("POST", "/api/v1/tokens", map[string]any{"name": "ike CLI"}, &tok); c != 201 {
		t.Fatalf("create token: %d", c)
	}
	full := tok["token"].(string)
	if !strings.HasPrefix(full, "ike_") {
		t.Fatalf("token %q", full)
	}
	var accts []map[string]any
	if c := tokenCall(t, e.admin.URL, full, "GET", "/api/v1/accounts", nil, &accts); c != 200 {
		t.Fatalf("token GET: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, full, "POST", "/api/v1/accounts", map[string]string{"name": "viatoken"}, nil); c != 201 {
		t.Fatalf("token POST: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, "ike_wrong", "GET", "/api/v1/accounts", nil, nil); c != 401 {
		t.Fatalf("bad token: %d", c)
	}
	var ro map[string]any
	admin.do("POST", "/api/v1/tokens", map[string]any{"name": "monitoring", "read_only": true}, &ro)
	if c := tokenCall(t, e.admin.URL, ro["token"].(string), "POST", "/api/v1/accounts", map[string]string{"name": "nope"}, nil); c != 403 {
		t.Fatalf("read-only token wrote: %d", c)
	}
	var list []map[string]any
	admin.do("GET", "/api/v1/tokens", nil, &list)
	if len(list) != 2 {
		t.Fatalf("tokens: %v", list)
	}
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), full[4:]) || strings.Contains(string(b), "hash") {
		t.Fatal("token list leaks secrets")
	}
	if c := admin.do("DELETE", "/api/v1/tokens/"+tok["id"].(string), nil, nil); c != 200 {
		t.Fatalf("delete: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, full, "GET", "/api/v1/accounts", nil, nil); c != 401 {
		t.Fatalf("deleted token still works: %d", c)
	}
}

func TestPackagesProvisioningAndQuotas(t *testing.T) {
	e := setup(t)
	addReseller(t, e, "webafrica", 5, 2)
	res := e.browser(e.reseller)
	if c := res.login("webafrica", "reseller-password-1", ""); c != 200 {
		t.Fatalf("reseller login: %d", c)
	}
	var pkg store.Package
	if c := res.do("POST", "/api/v1/packages", map[string]any{"name": "Starter", "price": 250000, "currency": "NGN", "interval": "month",
		"limits": map[string]any{"sites": 1, "mailboxes": 5, "bandwidth_gb": 10, "disk_mb": 2048}}, &pkg); c != 201 {
		t.Fatalf("package: %d", c)
	}
	if pkg.Owner != "webafrica" {
		t.Fatalf("owner %q", pkg.Owner)
	}
	var tok map[string]any
	res.do("POST", "/api/v1/tokens", map[string]any{"name": "WHMCS"}, &tok)
	token := tok["token"].(string)

	// WHMCS creates an account with a website and the package
	var out map[string]any
	if c := tokenCall(t, e.reseller.URL, token, "POST", "/api/v1/provision/accounts", map[string]any{
		"account": "chidi", "domain": "chidi.ng", "package": "Starter", "email": "chidi@mail.ng"}, &out); c != 201 {
		t.Fatalf("provision: %d %v", c, out)
	}
	pw, _ := out["password"].(string)
	if len(pw) < 12 || out["package"] != "Starter" {
		t.Fatalf("provision answer %v", out)
	}
	// the customer can sign in and sees only their account
	cust := e.browser(e.panel)
	if c := cust.login("chidi", pw, ""); c != 200 {
		t.Fatalf("customer login: %d", c)
	}
	var sites []map[string]any
	cust.do("GET", "/api/v1/sites", nil, &sites)
	if len(sites) != 1 || sites[0]["domain"] != "chidi.ng" {
		t.Fatalf("customer sites %v", sites)
	}
	// package: one website only
	if c := cust.do("POST", "/api/v1/sites", map[string]string{"domain": "second.ng", "account": "chidi"}, nil); c != 403 {
		t.Fatalf("second site past the package limit: %d", c)
	}
	// limits were sent to the agent
	var acct map[string]any
	cust.do("GET", "/api/v1/accounts/chidi", nil, &acct)
	if l, _ := acct["limits"].(map[string]any); l == nil || l["disk_mb"].(float64) != 2048 {
		t.Fatalf("limits not applied: %v", acct["limits"])
	}
	// reseller total: 2 websites across their accounts
	if c := tokenCall(t, e.reseller.URL, token, "POST", "/api/v1/provision/accounts", map[string]any{
		"account": "bola", "domain": "bola.ng", "email": "bola@mail.ng", "password": "bola-password-123"}, nil); c != 201 {
		t.Fatalf("second account: %d", c)
	}
	if c := res.do("POST", "/api/v1/sites", map[string]string{"domain": "bola2.ng", "account": "bola"}, nil); c != 403 {
		t.Fatalf("third site past reseller total: %d", c)
	}
	// failed provisioning rolls back: the account must not be left behind
	if c := tokenCall(t, e.reseller.URL, token, "POST", "/api/v1/provision/accounts", map[string]any{
		"account": "ngozi", "domain": "ngozi.ng", "email": "n@mail.ng"}, nil); c != 403 {
		t.Fatalf("over reseller site total: %d", c)
	}
	var accts []map[string]any
	res.do("GET", "/api/v1/accounts", nil, &accts)
	for _, a := range accts {
		if a["name"] == "ngozi" {
			t.Fatal("rolled-back account still exists")
		}
	}
	// SSO link signs the customer in once
	var sso map[string]string
	if c := tokenCall(t, e.reseller.URL, token, "POST", "/api/v1/provision/accounts/chidi/sso", nil, &sso); c != 200 {
		t.Fatalf("sso: %d", c)
	}
	path := strings.TrimPrefix(sso["url"], "https://panel.test")
	b := e.browser(e.panel)
	if c := b.do("GET", path, nil, nil); c != 200 {
		t.Fatalf("sso login: %d", c)
	}
	var me map[string]any
	if b.do("GET", "/api/v1/session", nil, &me); me["username"] != "chidi" {
		t.Fatalf("sso session: %v", me)
	}
	if c := e.browser(e.panel).do("GET", path, nil, nil); c != 410 {
		t.Fatalf("sso link reused: %d", c)
	}
	// suspend, password, terminate; other resellers' accounts are off limits
	if c := tokenCall(t, e.reseller.URL, token, "POST", "/api/v1/provision/accounts/chidi/suspend", map[string]string{"reason": "Unpaid"}, nil); c != 200 {
		t.Fatalf("suspend: %d", c)
	}
	if c := tokenCall(t, e.reseller.URL, token, "PUT", "/api/v1/provision/accounts/chidi/password", map[string]string{"password": "new-password-456"}, nil); c != 200 {
		t.Fatalf("password: %d", c)
	}
	if c := e.browser(e.panel).login("chidi", "new-password-456", ""); c != 200 {
		t.Fatalf("new password: %d", c)
	}
	addReseller(t, e, "other", 5, 0)
	ob := e.browser(e.reseller)
	ob.login("other", "reseller-password-1", "")
	var otok map[string]any
	ob.do("POST", "/api/v1/tokens", map[string]any{"name": "x"}, &otok)
	if c := tokenCall(t, e.reseller.URL, otok["token"].(string), "DELETE", "/api/v1/provision/accounts/chidi", nil, nil); c != 403 {
		t.Fatalf("other reseller terminated chidi: %d", c)
	}
	var term map[string]any
	if c := tokenCall(t, e.reseller.URL, token, "DELETE", "/api/v1/provision/accounts/chidi", nil, &term); c != 200 {
		t.Fatalf("terminate: %d", c)
	}
	if c := e.browser(e.panel).login("chidi", "new-password-456", ""); c == 200 {
		t.Fatal("login of a terminated account still works")
	}
	// customers can't use the provisioning API
	var ctok map[string]any
	bb := e.browser(e.panel)
	bb.login("bola", "bola-password-123", "")
	bb.do("POST", "/api/v1/tokens", map[string]any{"name": "mine"}, &ctok)
	if c := tokenCall(t, e.panel.URL, ctok["token"].(string), "GET", "/api/v1/provision/packages", nil, nil); c != 403 {
		t.Fatalf("customer used provisioning: %d", c)
	}
}

func TestSubscriptionsAndBandwidth(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "shop"}, nil)
	var pkg store.Package
	admin.do("POST", "/api/v1/packages", map[string]any{"name": "Business", "price": 1500000, "interval": "month",
		"limits": map[string]any{"bandwidth_gb": 1}}, &pkg)
	if c := admin.do("PUT", "/api/v1/account-packages/shop", map[string]string{"package": pkg.ID}, nil); c != 200 {
		t.Fatalf("assign: %d", c)
	}
	due := time.Now().Add(3 * 24 * time.Hour).UTC()
	var sub store.Subscription
	if c := admin.do("POST", "/api/v1/billing/subscriptions", map[string]any{"account": "shop", "package_id": pkg.ID, "first_due": due}, &sub); c != 201 {
		t.Fatalf("subscribe: %d", c)
	}
	if c := admin.do("POST", "/api/v1/billing/subscriptions", map[string]any{"account": "shop", "package_id": pkg.ID}, nil); c != 400 {
		t.Fatalf("second active subscription: %d", c)
	}
	e.srv.RunSubscriptions(t.Context(), time.Now())
	e.srv.RunSubscriptions(t.Context(), time.Now()) // not billed twice
	var invs []store.Invoice
	admin.do("GET", "/api/v1/billing/invoices", nil, &invs)
	n := 0
	for _, inv := range invs {
		if inv.CreatedBy == "subscription:"+sub.ID {
			n++
			if inv.Amount != 1500000 || !strings.Contains(inv.Description, "Business hosting") {
				t.Fatalf("invoice %+v", inv)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d subscription invoices", n)
	}
	// bandwidth: over 1 GB this month suspends, next month lifts it
	month := time.Now().UTC().Format("2006-01")
	_ = e.st.Update(func(d *store.Data) error {
		d.Bandwidth[month] = map[string]float64{"shop": 2048}
		return nil
	})
	e.srv.TrackBandwidth(t.Context(), time.Now())
	var acct map[string]any
	admin.do("GET", "/api/v1/accounts/shop", nil, &acct)
	if acct["suspended"] != true || !strings.Contains(acct["suspend_reason"].(string), "bandwidth") {
		t.Fatalf("not suspended for bandwidth: %v", acct)
	}
	var bw map[string]any
	admin.do("GET", "/api/v1/bandwidth", nil, &bw)
	if rows, _ := bw["accounts"].([]any); len(rows) != 1 || rows[0].(map[string]any)["suspended_for_bandwidth"] != true {
		t.Fatalf("bandwidth view: %v", bw)
	}
	e.srv.TrackBandwidth(t.Context(), time.Now().AddDate(0, 1, 0))
	admin.do("GET", "/api/v1/accounts/shop", nil, &acct)
	if acct["suspended"] != false {
		t.Fatalf("still suspended next month: %v", acct)
	}
}

func TestBranding(t *testing.T) {
	e := setup(t)
	addReseller(t, e, "webafrica", 5, 0)
	res := e.browser(e.reseller)
	res.login("webafrica", "reseller-password-1", "")
	if c := res.do("PUT", "/api/v1/me/branding", map[string]any{"product_name": "WebAfrica Panel", "primary_color": "#0a7f5a",
		"logo": "data:image/svg+xml;base64,PHN2Zz4=", "hostnames": []string{"panel.webafrica.ng"}}, nil); c != 400 {
		t.Fatalf("SVG logo accepted: %d", c)
	}
	if c := res.do("PUT", "/api/v1/me/branding", map[string]any{"product_name": "WebAfrica Panel", "primary_color": "#0a7f5a",
		"hostnames": []string{"panel.webafrica.ng"}, "hide_powered_by": true}, nil); c != 200 {
		t.Fatalf("branding: %d", c)
	}
	req, _ := http.NewRequest("GET", e.panel.URL+"/api/v1/branding", nil)
	req.Host = "panel.webafrica.ng:2083"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var b store.Branding
	_ = json.NewDecoder(resp.Body).Decode(&b)
	resp.Body.Close()
	if b.ProductName != "WebAfrica Panel" || b.PrimaryColor != "#0a7f5a" || len(b.Hostnames) != 0 {
		t.Fatalf("branding by host: %+v", b)
	}
	var def store.Branding
	e.browser(e.panel).do("GET", "/api/v1/branding", nil, &def)
	if def.ProductName != "Ike" {
		t.Fatalf("default branding: %+v", def)
	}
	// another reseller can't take the same hostname
	addReseller(t, e, "rival", 5, 0)
	rb := e.browser(e.reseller)
	rb.login("rival", "reseller-password-1", "")
	if c := rb.do("PUT", "/api/v1/me/branding", map[string]any{"product_name": "Rival", "hostnames": []string{"panel.webafrica.ng"}}, nil); c != 400 {
		t.Fatalf("hostname taken over: %d", c)
	}
}

func TestResellerDomainAndAllocationQuotas(t *testing.T) {
	e := setup(t)
	addReseller(t, e, "webafrica", 5, 0)
	_ = e.st.Update(func(d *store.Data) error {
		p, _ := d.Person("webafrica")
		p.MaxDomains, p.MaxDiskMB = 2, 3000
		return nil
	})
	res := e.browser(e.reseller)
	res.login("webafrica", "reseller-password-1", "")
	res.do("POST", "/api/v1/accounts", map[string]string{"name": "ada"}, nil)
	res.do("POST", "/api/v1/sites", map[string]string{"domain": "ada.ng", "account": "ada"}, nil)
	if c := res.do("PUT", "/api/v1/sites/ada.ng/domains", map[string]any{"aliases": []string{"ada.com", "ada.org"}}, nil); c != 200 {
		t.Fatalf("2 domains: %d", c)
	}
	if c := res.do("PUT", "/api/v1/sites/ada.ng/domains", map[string]any{"aliases": []string{"ada.com", "ada.org", "ada.net"}}, nil); c != 403 {
		t.Fatalf("3 domains past the reseller total: %d", c)
	}
	var big, small store.Package
	res.do("POST", "/api/v1/packages", map[string]any{"name": "Big", "limits": map[string]any{"disk_mb": 5000}}, &big)
	res.do("POST", "/api/v1/packages", map[string]any{"name": "Small", "limits": map[string]any{"disk_mb": 2000}}, &small)
	if c := res.do("PUT", "/api/v1/account-packages/ada", map[string]string{"package": big.ID}, nil); c != 403 {
		t.Fatalf("package bigger than the reseller's disk: %d", c)
	}
	if c := res.do("PUT", "/api/v1/account-packages/ada", map[string]string{"package": small.ID}, nil); c != 200 {
		t.Fatalf("small package: %d", c)
	}
	res.do("POST", "/api/v1/accounts", map[string]string{"name": "bisi"}, nil)
	if c := res.do("PUT", "/api/v1/account-packages/bisi", map[string]string{"package": small.ID}, nil); c != 403 {
		t.Fatalf("second package past 3000 MB total: %d", c)
	}
}

func TestLimitedTokens(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "shop"}, nil)
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "other"}, nil)
	var tok map[string]any
	if c := admin.do("POST", "/api/v1/tokens", map[string]any{"name": "dns bot", "limited": true, "scopes": map[string]string{"dns": "write", "sites": "read"},
		"accounts": []string{"shop"}}, &tok); c != 201 {
		t.Fatalf("create: %d", c)
	}
	tk := tok["token"].(string)
	var accts []map[string]any
	if c := tokenCall(t, e.admin.URL, tk, "GET", "/api/v1/accounts", nil, &accts); c != 403 {
		t.Fatalf("accounts area not in scope: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, tk, "GET", "/api/v1/sites", nil, nil); c != 200 {
		t.Fatalf("sites read: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, tk, "POST", "/api/v1/sites", map[string]string{"domain": "x.ng", "account": "shop"}, nil); c != 403 {
		t.Fatalf("sites write with read scope: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, tk, "GET", "/api/v1/accounts/other/dns/zones", nil, nil); c != 403 {
		t.Fatalf("account outside the token: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, tk, "GET", "/api/v1/updates", nil, nil); c != 403 {
		t.Fatalf("server area: %d", c)
	}
	if c := tokenCall(t, e.admin.URL, tk, "POST", "/api/v1/tokens", map[string]any{"name": "escalate"}, nil); c != 403 {
		t.Fatalf("token minted another token: %d", c)
	}
	var ipTok map[string]any
	admin.do("POST", "/api/v1/tokens", map[string]any{"name": "office", "allow_ips": []string{"198.51.100.0/24"}}, &ipTok)
	if c := tokenCall(t, e.admin.URL, ipTok["token"].(string), "GET", "/api/v1/sites", nil, nil); c != 403 {
		t.Fatalf("IP allow-list ignored: %d", c)
	}
	if c := admin.do("POST", "/api/v1/tokens", map[string]any{"name": "bad", "limited": true, "scopes": map[string]string{"nuclear": "write"}}, nil); c != 400 {
		t.Fatalf("unknown area accepted: %d", c)
	}
	if areaOf("/api/v1/accounts/shop/mail/domains") != "mail" || areaOf("/api/v1/sites/x.ng/waf") != "security" || areaOf("/api/v1/jobs/1") != "" {
		t.Fatal("areaOf")
	}
}
