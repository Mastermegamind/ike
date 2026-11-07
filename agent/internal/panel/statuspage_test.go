package panel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/panel/alerts"
	"github.com/Mastermegamind/ike/agent/internal/panel/auth"
	"github.com/Mastermegamind/ike/agent/internal/panel/store"
)

func TestStatusPages(t *testing.T) {
	e := setup(t)
	admin := e.browser(e.admin)
	admin.login("marvellous", "admin-password-1", "")
	for _, a := range []string{"shop", "blog"} {
		admin.do("POST", "/api/v1/accounts", map[string]string{"name": a}, nil)
	}
	for _, s := range []map[string]string{{"domain": "shop.ng", "account": "shop", "runtime": "static"}, {"domain": "blog.ng", "account": "blog", "runtime": "static"}} {
		if code := admin.do("POST", "/api/v1/sites", s, nil); code != 201 {
			t.Fatalf("site %v: %d", s, code)
		}
	}
	h, _ := auth.HashPassword("reseller-pass-1")
	_ = e.st.Update(func(d *store.Data) error {
		_ = d.AddPerson(&store.Person{Username: "webafrica", Email: "r@w.ng", Role: store.Reseller, PasswordHash: h, MaxAccounts: 5})
		_ = d.AddPerson(&store.Person{Username: "ngozi", Email: "n@shop.ng", Role: store.User, Accounts: []string{"shop"}, PasswordHash: h})
		d.AccountOf["shop"] = "webafrica"
		d.Branding["webafrica"] = &store.Branding{ProductName: "WebAfrica Hosting", PrimaryColor: "#0a7f5a"}
		return nil
	})
	// shop.ng answers, blog.ng is broken
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "blog.ng" {
			w.WriteHeader(502)
		}
	}))
	defer web.Close()
	e.srv.Monitor = &alerts.Monitor{LocalURL: web.URL, Targets: func(context.Context) ([]alerts.Target, error) {
		return []alerts.Target{{Domain: "shop.ng", Status: "active"}, {Domain: "blog.ng", Status: "active"}}, nil
	}}
	for i := 0; i < 3; i++ {
		e.srv.Monitor.Check(context.Background())
	}

	res := e.browser(e.reseller)
	if res.login("webafrica", "reseller-pass-1", "") != 200 {
		t.Fatal("reseller login")
	}
	var got struct {
		Websites []string `json:"websites"`
	}
	res.do("GET", "/api/v1/status-page", nil, &got)
	if strings.Join(got.Websites, ",") != "shop.ng" {
		t.Fatalf("reseller sees %v", got.Websites)
	}
	if code := res.do("PUT", "/api/v1/status-page", map[string]any{"enabled": true, "slug": "webafrica", "entries": []map[string]string{{"domain": "blog.ng"}}}, nil); code != 400 {
		t.Fatalf("listed someone else's website: %d", code)
	}
	var put struct {
		URL string `json:"url"`
	}
	if code := res.do("PUT", "/api/v1/status-page", map[string]any{"enabled": true, "slug": "webafrica", "intro": "Our customers' websites",
		"entries": []map[string]string{{"domain": "shop.ng", "name": "Shop <b>NG</b>"}}}, &put); code != 200 || !strings.HasSuffix(put.URL, "/status/webafrica") {
		t.Fatalf("%d %+v", code, put)
	}
	// the admin can't take the reseller's address; customers have no page
	if code := admin.do("PUT", "/api/v1/status-page", map[string]any{"enabled": true, "slug": "webafrica", "entries": []map[string]string{}}, nil); code != 409 {
		t.Fatalf("slug taken: %d", code)
	}
	if code := admin.do("PUT", "/api/v1/status-page", map[string]any{"enabled": true, "slug": "megamind", "entries": []map[string]string{{"domain": "shop.ng"}, {"domain": "blog.ng", "name": "Blog"}}}, nil); code != 200 {
		t.Fatalf("admin page: %d", code)
	}
	owner := e.browser(e.panel)
	owner.login("ngozi", "reseller-pass-1", "")
	if code := owner.do("GET", "/api/v1/status-page", nil, nil); code != 403 {
		t.Fatalf("customer: %d", code)
	}

	get := func(path string) (int, string) {
		resp, err := http.Get(e.panel.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	code, body := get("/status/webafrica")
	if code != 200 || !strings.Contains(body, "WebAfrica Hosting status") || !strings.Contains(body, "Shop &lt;b&gt;NG&lt;/b&gt;") ||
		!strings.Contains(body, "All websites are up") || !strings.Contains(body, "100% uptime") || !strings.Contains(body, "#0a7f5a") {
		t.Fatalf("%d\n%s", code, body)
	}
	_, body = get("/status/megamind")
	if !strings.Contains(body, "1 of 2 websites is down") || !strings.Contains(body, "Down since") {
		t.Fatalf("%s", body)
	}
	code, body = get("/status/megamind.json")
	if code != 200 || !strings.Contains(body, `"all_up":false`) || strings.Contains(body, "blog.ng") {
		t.Fatalf("json %d: %s", code, body)
	}
	if code, _ := get("/status/nobody"); code != 404 {
		t.Fatalf("unknown page: %d", code)
	}
	// history survives a restart
	snap := e.srv.Monitor.Snapshot()
	m2 := &alerts.Monitor{}
	m2.Restore(snap)
	if u := m2.Uptimes([]string{"shop.ng"}); u[0].Percent != 100 {
		t.Fatalf("%+v", u)
	}
}
