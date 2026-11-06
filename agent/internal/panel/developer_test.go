package panel

import (
	"strings"
	"testing"
)

func TestDeveloperRole(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("andre", "admin-password-1", "")
	for _, a := range []string{"shop", "blog", "other"} {
		admin.do("POST", "/api/v1/accounts", map[string]string{"name": a}, nil)
	}
	admin.do("POST", "/api/v1/sites", map[string]string{"domain": "shop.ng", "account": "shop"}, nil)
	admin.do("POST", "/api/v1/sites", map[string]string{"domain": "other.ng", "account": "other"}, nil)
	// An account owner with shop and blog.
	if c := admin.do("POST", "/api/v1/people", map[string]any{"username": "ada", "email": "ada@shop.ng", "password": "owner-password-1", "role": "user",
		"accounts": []string{"shop", "blog"}}, nil); c != 201 {
		t.Fatalf("create owner: %d", c)
	}
	owner := e.browser(e.panel)
	if c := owner.login("ada", "owner-password-1", ""); c != 200 {
		t.Fatalf("owner login: %d", c)
	}
	// Owners invite developers only, and only to their own accounts.
	for _, bad := range []struct {
		body map[string]any
		code int
		want string
	}{
		{map[string]any{"username": "eve", "email": "e@x.ng", "password": "developer-pass-1", "role": "reseller"}, 403, "only invite developers"},
		{map[string]any{"username": "eve", "email": "e@x.ng", "password": "developer-pass-1", "role": "developer", "accounts": []string{"other"},
			"areas": map[string]string{"sites": "write"}}, 403, "isn't one of yours"},
		{map[string]any{"username": "eve", "email": "e@x.ng", "password": "developer-pass-1", "role": "developer", "accounts": []string{"shop"},
			"areas": map[string]string{"billing": "write"}}, 400, "can't be given"},
		{map[string]any{"username": "eve", "email": "e@x.ng", "password": "developer-pass-1", "role": "developer", "accounts": []string{"shop"}}, 400, "at least one area"},
	} {
		var out map[string]any
		if c := owner.do("POST", "/api/v1/people", bad.body, &out); c != bad.code || !strings.Contains(strings.ToLower(errText(out)), bad.want) {
			t.Errorf("%v: %d %v, want %d %q", bad.body, c, out, bad.code, bad.want)
		}
	}
	if c := owner.do("POST", "/api/v1/people", map[string]any{"username": "kelechi", "email": "k@dev.ng", "password": "developer-pass-1", "role": "developer",
		"accounts": []string{"shop"}, "areas": map[string]string{"sites": "write", "files": "write", "databases": "read"}}, nil); c != 201 {
		t.Fatalf("invite developer: %d", c)
	}
	// The developer signs in where account owners do, never on the admin port.
	if c := e.browser(e.admin).login("kelechi", "developer-pass-1", ""); c == 200 {
		t.Fatal("developer signed in on the admin port")
	}
	dev := e.browser(e.panel)
	if c := dev.login("kelechi", "developer-pass-1", ""); c != 200 {
		t.Fatalf("developer login: %d", c)
	}
	var accts []map[string]any
	dev.do("GET", "/api/v1/accounts", nil, &accts)
	if len(accts) != 1 || accts[0]["name"] != "shop" {
		t.Fatalf("developer sees accounts %v", accts)
	}
	checks := []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/api/v1/sites/shop.ng", nil, 200},                                               // sites: write
		{"PUT", "/api/v1/sites/shop.ng/redirects", []any{}, 200},                                 // ... can change a website
		{"GET", "/api/v1/accounts/shop/cron", nil, 200},                                          // cron counts as sites
		{"POST", "/api/v1/sites", map[string]string{"domain": "new.ng", "account": "shop"}, 403}, // but not add one
		{"DELETE", "/api/v1/sites/shop.ng", nil, 403},                                            // or delete one
		{"GET", "/api/v1/accounts/shop/files?path=", nil, 200},                                   // files: write
		{"GET", "/api/v1/accounts/shop/dbs", nil, 200},                                           // databases: read
		{"POST", "/api/v1/accounts/shop/dbs", map[string]string{"engine": "postgres", "name": "x"}, 403},
		{"GET", "/api/v1/accounts/shop/mail/domains", nil, 403}, // no mail area
		{"GET", "/api/v1/sites/other.ng", nil, 403},             // another account
		{"POST", "/api/v1/accounts/shop/suspend", map[string]string{}, 403},
		{"PUT", "/api/v1/accounts/shop/ssh", map[string]string{"mode": "shell"}, 403},
		{"GET", "/api/v1/billing/invoices", nil, 403},
		{"GET", "/api/v1/packages", nil, 403},
		{"POST", "/api/v1/people", map[string]any{"username": "zed", "email": "z@z.ng", "password": "zzzzzzzzzzzz1", "role": "developer"}, 403},
		{"GET", "/api/v1/updates", nil, 403},
		{"PUT", "/api/v1/me/branding", map[string]string{"product_name": "Mine"}, 403},
		{"GET", "/api/v1/session", nil, 200},
	}
	for _, c := range checks {
		if got := dev.do(c.method, c.path, c.body, nil); got != c.want {
			t.Errorf("developer %s %s = %d, want %d", c.method, c.path, got, c.want)
		}
	}
	// A developer's API token is limited by the developer's areas too.
	var tok map[string]any
	if c := dev.do("POST", "/api/v1/tokens", map[string]any{"name": "ci"}, &tok); c != 201 {
		t.Fatalf("developer token: %d", c)
	}
	if c := tokenCall(t, e.panel.URL, tok["token"].(string), "GET", "/api/v1/accounts/shop/mail/domains", nil, nil); c != 403 {
		t.Fatalf("developer's token reached mail: %d", c)
	}
	if c := tokenCall(t, e.panel.URL, tok["token"].(string), "GET", "/api/v1/sites/shop.ng", nil, nil); c != 200 {
		t.Fatalf("developer's token: %d", c)
	}
	// The owner sees and manages their developer; another owner doesn't.
	var people []map[string]any
	owner.do("GET", "/api/v1/people", nil, &people)
	found := false
	for _, p := range people {
		found = found || p["username"] == "kelechi"
	}
	if !found {
		t.Fatal("owner can't see the developer they invited")
	}
	if c := owner.do("PATCH", "/api/v1/people/kelechi", map[string]any{"areas": map[string]string{"sites": "read"}}, nil); c != 200 {
		t.Fatalf("owner changes areas: %d", c)
	}
	if c := dev.do("PUT", "/api/v1/sites/shop.ng/redirects", []any{}, nil); c != 403 {
		t.Fatalf("after sites became read-only: %d", c)
	}
	admin.do("POST", "/api/v1/people", map[string]any{"username": "tunde", "email": "t@x.ng", "password": "owner-password-2", "role": "user", "accounts": []string{"other"}}, nil)
	other := e.browser(e.panel)
	other.login("tunde", "owner-password-2", "")
	if c := other.do("DELETE", "/api/v1/people/kelechi", nil, nil); c != 403 {
		t.Fatalf("another owner removed the developer: %d", c)
	}
	if c := owner.do("PATCH", "/api/v1/people/kelechi", map[string]any{"role": "admin"}, nil); c != 403 {
		t.Fatalf("owner promoted the developer: %d", c)
	}
	if c := owner.do("DELETE", "/api/v1/people/kelechi", nil, nil); c != 200 {
		t.Fatalf("owner removes developer: %d", c)
	}
	if c := dev.do("GET", "/api/v1/sites/shop.ng", nil, nil); c != 401 {
		t.Fatalf("removed developer still signed in: %d", c)
	}
}

func errText(m map[string]any) string {
	if s, ok := m["error"].(string); ok {
		return s
	}
	return ""
}
