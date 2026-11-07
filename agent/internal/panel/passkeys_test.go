package panel

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/panel/webauthn/softkey"
)

func named(b *browser) (*browser, string) {
	u, _ := url.Parse(b.base)
	b.host = "localhost:" + u.Port()
	return b, "http://" + b.host
}

func TestPasskeys(t *testing.T) {
	e := setup(t)
	admin, origin := named(e.browser(e.admin))
	if c := admin.login("marvellous", "admin-password-1", ""); c != 200 {
		t.Fatalf("login: %d", c)
	}

	// By IP address the browser can't use passkeys at all.
	ipb := e.browser(e.admin)
	ipb.login("marvellous", "admin-password-1", "")
	var out map[string]any
	if c := ipb.do("POST", "/api/v1/me/passkeys/options", nil, &out); c != 400 || !strings.Contains(errText(out), "not by an IP address") {
		t.Fatalf("options by IP: %d %v", c, out)
	}

	// Add a passkey: options, then the authenticator's answer and the password.
	key := softkey.New()
	key.Count = 1
	var opts softkey.Options
	if c := admin.do("POST", "/api/v1/me/passkeys/options", nil, &opts); c != 200 || opts.PublicKey.Challenge == "" || opts.PublicKey.RP.ID != "localhost" {
		t.Fatalf("options: %d %+v", c, opts)
	}
	body := key.Register(opts, origin)
	body["name"] = "YubiKey"
	body["password"] = "wrong-password-1"
	if c := admin.do("POST", "/api/v1/me/passkeys", body, &out); c == 201 {
		t.Fatal("added a passkey with the wrong password")
	}
	// The challenge was used up by that attempt.
	body["password"] = "admin-password-1"
	if c := admin.do("POST", "/api/v1/me/passkeys", body, &out); c != 400 || !strings.Contains(errText(out), "expired") {
		t.Fatalf("reused challenge: %d %v", c, out)
	}
	admin.do("POST", "/api/v1/me/passkeys/options", nil, &opts)
	body = key.Register(opts, origin)
	body["name"], body["password"] = "YubiKey", "admin-password-1"
	if c := admin.do("POST", "/api/v1/me/passkeys", body, &out); c != 201 || out["name"] != "YubiKey" {
		t.Fatalf("add: %d %v", c, out)
	}
	id := out["id"].(string)
	// Options now exclude it, so the same key can't be added twice.
	var raw map[string]map[string]any
	admin.do("POST", "/api/v1/me/passkeys/options", nil, &raw)
	if ex, _ := raw["publicKey"]["excludeCredentials"].([]any); len(ex) != 1 {
		t.Fatalf("excludeCredentials = %v", raw["publicKey"]["excludeCredentials"])
	}
	var list []map[string]any
	if admin.do("GET", "/api/v1/me/passkeys", nil, &list); len(list) != 1 || list[0]["public_key"] != nil {
		t.Fatalf("list: %v", list)
	}
	if c := admin.do("PATCH", "/api/v1/me/passkeys/"+id, map[string]string{"name": "Office key"}, &out); c != 200 || out["name"] != "Office key" {
		t.Fatalf("rename: %d %v", c, out)
	}

	// Sign in with it: no password, no code.
	signIn := func(b *browser, k *softkey.Key, org string) (int, map[string]any) {
		var o softkey.Options
		if c := b.do("POST", "/api/v1/session/passkey/options", map[string]any{}, &o); c != 200 {
			t.Fatalf("login options: %d", c)
		}
		var res map[string]any
		return b.do("POST", "/api/v1/session/passkey", k.SignIn(o, org), &res), res
	}
	fresh, org2 := named(e.browser(e.admin))
	if c, res := signIn(fresh, key, org2); c != 200 || res["username"] != "marvellous" {
		t.Fatalf("passkey sign-in: %d %v", c, res)
	}
	if c := fresh.do("GET", "/api/v1/session", nil, nil); c != 200 {
		t.Fatalf("session after passkey sign-in: %d", c)
	}

	// A look-alike site gets nothing: the origin is checked.
	phish, _ := named(e.browser(e.admin))
	if c, _ := signIn(phish, key, "https://panel-login.example"); c != 401 {
		t.Fatalf("wrong origin: %d", c)
	}
	// A passkey the panel doesn't know.
	if c, res := signIn(phish, softkey.New(), org2); c != 401 || !strings.Contains(errText(res), "doesn't know") {
		t.Fatalf("unknown key: %d %v", c, res)
	}
	// A replayed answer fails: the challenge works once.
	var o softkey.Options
	phish.do("POST", "/api/v1/session/passkey/options", map[string]any{}, &o)
	ans := key.SignIn(o, org2)
	if c := phish.do("POST", "/api/v1/session/passkey", ans, nil); c != 200 {
		t.Fatalf("first use: %d", c)
	}
	if c := e.browser(e.admin).do("POST", "/api/v1/session/passkey", ans, nil); c != 401 {
		t.Fatalf("replay: %d", c)
	}

	// An account owner's passkey can't sign in on the admin port.
	admin.do("POST", "/api/v1/accounts", map[string]string{"name": "shop"}, nil)
	admin.do("POST", "/api/v1/people", map[string]any{"username": "ada", "email": "ada@shop.ng", "password": "owner-password-1", "role": "user", "accounts": []string{"shop"}}, nil)
	owner, ownerOrigin := named(e.browser(e.panel))
	owner.login("ada", "owner-password-1", "")
	ukey := softkey.New()
	owner.do("POST", "/api/v1/me/passkeys/options", nil, &opts)
	ub := ukey.Register(opts, ownerOrigin)
	ub["password"] = "owner-password-1"
	if c := owner.do("POST", "/api/v1/me/passkeys", ub, &out); c != 201 || out["synced"] != false {
		t.Fatalf("owner add: %d %v", c, out)
	}
	adminPort, adminOrigin := named(e.browser(e.admin))
	if c, res := signIn(adminPort, ukey, adminOrigin); c != 401 || !strings.Contains(errText(res), "sign-in page is for") {
		t.Fatalf("user passkey on admin port: %d %v", c, res)
	}
	// Someone else's passkey can't be removed or renamed.
	if c := owner.do("DELETE", "/api/v1/me/passkeys/"+id, nil, nil); c != 404 {
		t.Fatalf("delete someone else's: %d", c)
	}

	// API tokens can't add or remove passkeys.
	var tok map[string]any
	admin.do("POST", "/api/v1/tokens", map[string]any{"name": "ci"}, &tok)
	if c := tokenCall(t, e.admin.URL, tok["token"].(string), "DELETE", "/api/v1/me/passkeys/"+id, nil, nil); c != 403 {
		t.Fatalf("token removed a passkey: %d", c)
	}

	// A counter that goes backwards turns the passkey off (it is at 4 now).
	key.Count = 2
	if c, res := signIn(named2(e), key, org2); c != 401 || !strings.Contains(errText(res), "copied") {
		t.Fatalf("cloned key: %d %v", c, res)
	}
	admin.do("GET", "/api/v1/me/passkeys", nil, &list)
	if list[0]["disabled"] != true {
		t.Fatalf("cloned key still on: %v", list)
	}

	// A disabled person can't sign in with a passkey either.
	if c := admin.do("DELETE", "/api/v1/me/passkeys/"+id, nil, nil); c != 200 {
		t.Fatalf("delete: %d", c)
	}
	if c, _ := signIn(named2(e), key, org2); c != 401 {
		t.Fatalf("removed key still signs in: %d", c)
	}
	admin.do("PATCH", "/api/v1/people/ada", map[string]any{"disabled": true}, nil)
	ob, oo := named(e.browser(e.panel))
	if c, _ := signIn(ob, ukey, oo); c != 401 {
		t.Fatalf("disabled person signed in: %d", c)
	}
}

func named2(e *env) *browser { b, _ := named(e.browser(e.admin)); return b }
