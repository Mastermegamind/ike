package sites

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
)

func TestCaddyAndOLSRendering(t *testing.T) {
	for _, stack := range []string{"caddy", "openlitespeed"} {
		m, r, root := newTestManager(t, func(c *config.Config) { c.WebStack, c.DefaultWebServer = stack, stack })
		mustAccount(t, m, "shop")
		s, err := m.Create(ctx, CreateRequest{Domain: "shop.test", Account: "shop", Runtime: "php", Version: "8.3"})
		if err != nil {
			t.Fatalf("%s: %v", stack, err)
		}
		if s.WebServer != stack {
			t.Fatalf("web server = %q", s.WebServer)
		}
		if _, err := m.SetRedirects(ctx, "shop.test", []Redirect{{Kind: "redirect", Match: "prefix", From: "/old", To: "/new", Code: 301},
			{Kind: "rewrite", Match: "exact", From: "/feed", To: "/index.php?feed=rss"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := m.SetStatus(ctx, "shop.test", SetStatusRequest{Status: StatusMaintenance, AllowIPs: []string{"192.0.2.9"}}); err != nil {
			t.Fatal(err)
		}
		L := m.layout(s)
		cmds := strings.Join(r.History(), "\n")
		switch stack {
		case "caddy":
			b, err := os.ReadFile(filepath.Join(root, m.caddyFile("shop.test")))
			if err != nil {
				t.Fatal(err)
			}
			conf := string(b)
			for _, want := range []string{"http://shop.test, http://www.shop.test {", "import ike-" + L.PoolName, "php_fastcgi unix/" + L.PHPSocket,
				"@ike_r0 path_regexp ike_r0 `^/old(/.*)?$`", "redir @ike_r0 /new{re.ike_r0.1} 301", "rewrite @ike_r1 /index.php?feed=rss",
				"not remote_ip 192.0.2.9", "error @ike_closed 503"} {
				if !strings.Contains(conf, want) {
					t.Errorf("caddy config is missing %q:\n%s", want, conf)
				}
			}
			if !strings.Contains(cmds, "caddy validate --adapter caddyfile --config /etc/caddy/Caddyfile") || !strings.Contains(cmds, "systemctl reload caddy") || strings.Contains(cmds, "nginx") {
				t.Errorf("commands:\n%s", cmds)
			}
		case "openlitespeed":
			b, err := os.ReadFile(filepath.Join(root, m.olsConf("shop.test")))
			if err != nil {
				t.Fatal(err)
			}
			conf := string(b)
			for _, want := range []string{"docRoot                   " + L.Root + "/", "address                 uds:/" + L.PHPSocket,
				"add                     fcgi:" + L.OLSApp + " php", "RewriteRule ^/old(/.*)?$ /new$1 [R=301,L,QSD]",
				"RewriteCond %{REMOTE_ADDR} !^192\\.0\\.2\\.9$", "RewriteRule ^ - [R=503,L]", "errorpage 503"} {
				if !strings.Contains(conf, want) {
					t.Errorf("OpenLiteSpeed config is missing %q:\n%s", want, conf)
				}
			}
			idx, _ := os.ReadFile(filepath.Join(root, m.olsIndex()))
			if !strings.Contains(string(idx), "map                     ike-shop.test shop.test, www.shop.test") {
				t.Errorf("listener map:\n%s", idx)
			}
			if !strings.Contains(cmds, "/usr/local/lsws/bin/openlitespeed -t") || !strings.Contains(cmds, "lswsctrl restart") {
				t.Errorf("commands:\n%s", cmds)
			}
			// Not supported on OpenLiteSpeed: addresses that skip the password.
			if _, err := m.SetAuthUser(ctx, "shop.test", "ada", "ada password 1"); err != nil {
				t.Fatal(err)
			}
			if _, err := m.SetProtectedPaths(ctx, "shop.test", []ProtectedPath{{Path: "/admin/", AllowIPs: []string{"10.0.0.0/8"}}}); err == nil || !strings.Contains(err.Error(), "OpenLiteSpeed") {
				t.Errorf("IP bypass on OpenLiteSpeed: %v", err)
			}
		}
		if _, err := m.SetWAF(ctx, "shop.test", WAFSettings{Mode: "block"}); err == nil {
			t.Errorf("%s: WAF accepted", stack)
		}
		if err := m.Delete(ctx, "shop.test", true); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{m.caddyFile("shop.test"), m.olsConf("shop.test")} {
			if _, err := os.Stat(filepath.Join(root, f)); err == nil {
				t.Errorf("%s left %s behind", stack, f)
			}
		}
		if stack == "openlitespeed" {
			idx, _ := os.ReadFile(filepath.Join(root, m.olsIndex()))
			if strings.Contains(string(idx), "shop.test") {
				t.Errorf("deleted website still mapped:\n%s", idx)
			}
		}
	}
}

func TestCaddyAuthHandler(t *testing.T) {
	m, _, root := newTestManager(t)
	dir := filepath.Join(root, htpasswdRoot, "shop.test")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "0a1b2c3d"), []byte("ada:"+cryptHash("right password")+"\n"), 0o640)
	h := m.AuthHandler()
	try := func(file, user, pass string) (int, string) {
		req, _ := http.NewRequest("GET", "/check?file="+file+"&realm=Staff+only", nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		w := &recorder{h: http.Header{}}
		h.ServeHTTP(w, req)
		return w.code, w.h.Get("WWW-Authenticate")
	}
	if c, a := try("shop.test/0a1b2c3d", "", ""); c != 401 || a != `Basic realm="Staff only", charset="UTF-8"` {
		t.Errorf("no password: %d %q", c, a)
	}
	if c, _ := try("shop.test/0a1b2c3d", "ada", "wrong"); c != 401 {
		t.Errorf("wrong password: %d", c)
	}
	for i := 0; i < 2; i++ { // the second time comes from the cache
		if c, _ := try("shop.test/0a1b2c3d", "ada", "right password"); c != 204 {
			t.Errorf("right password: %d", c)
		}
	}
	for _, bad := range []string{"../../etc/shadow", "shop.test/../x", "shop.test/users", "/etc/passwd"} {
		if c, _ := try(bad, "ada", "right password"); c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
}

type recorder struct {
	h    http.Header
	code int
}

func (r *recorder) Header() http.Header         { return r.h }
func (r *recorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *recorder) WriteHeader(c int)           { r.code = c }

// TestRealCaddy runs the generated configs in a real Caddy: static files,
// a proxied app, HTTPS with a forced redirect, other names, redirects,
// error pages, maintenance and password-protected folders (checked by the
// agent over a unix socket).
func TestRealCaddy(t *testing.T) {
	if os.Getenv("IKE_REAL_HTTP") != "1" {
		t.Skip("set IKE_REAL_HTTP=1 on a throwaway machine with caddy installed")
	}
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy isn't installed")
	}
	for _, d := range []string{"/etc/caddy/ike", "/var/lib/ike/sites", "/etc/ike/htpasswd", "/etc/ike/ssl/cad.test"} {
		_ = os.RemoveAll(d)
	}
	cfg := config.Default()
	cfg.Root, cfg.DryRun = "/", true
	cfg.WebStack, cfg.DefaultWebServer = "caddy", "caddy"
	base := t.TempDir()
	_ = os.Chmod(filepath.Dir(base), 0o755)
	_ = os.Chmod(base, 0o755)
	cfg.AccountsDir, cfg.HomeBase, cfg.CronDir = filepath.Join(base, "acc"), filepath.Join(base, "home"), filepath.Join(base, "cron")
	cfg.AuthSocket = filepath.Join(base, "auth.sock")
	cfg.CaddyConfig = filepath.Join(base, "Caddyfile")
	_ = os.WriteFile(cfg.CaddyConfig, []byte("{\n\tauto_https off\n\tadmin off\n\tstorage file_system "+base+"/data\n}\nimport /etc/caddy/ike/*.caddy\n"), 0o644)
	m, _, _ := newTestManagerAt(t, cfg)
	mustAccount(t, m, "cad")

	// The app behind the proxied website.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "app saw "+r.Host+r.URL.Path+" proto="+r.Header.Get("X-Forwarded-Proto"))
	})}
	go func() { _ = app.Serve(ln) }()
	defer app.Close()
	appPort := ln.Addr().(*net.TCPAddr).Port

	if _, err := m.Create(ctx, CreateRequest{Domain: "cad.test", Account: "cad", Runtime: "static"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "app.cad.test", Account: "cad", Runtime: "proxy", Port: appPort}); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get("cad.test")
	L := m.layout(s)
	for p, body := range map[string]string{"index.html": "home", "new/page.html": "new page", "admin/index.html": "admin area",
		"admin/reports/q3.html": "q3 report", ".env": "SECRET=1"} {
		f := filepath.Join(L.DocRoot, p)
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		_ = os.WriteFile(f, []byte(body), 0o644)
	}
	_ = os.WriteFile(filepath.Join(L.ErrorsDir, "404.html"), []byte("custom not found"), 0o644)
	if _, err := m.SetRedirects(ctx, "cad.test", []Redirect{{Kind: "redirect", Match: "prefix", From: "/old", To: "/new", Code: 301},
		{Kind: "rewrite", Match: "exact", From: "/latest", To: "/new/page.html"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDomains(ctx, "cad.test", DomainsRequest{WWW: "root", Aliases: []string{"cad-alias.test"}, RedirectAliases: true}); err != nil {
		t.Fatal(err)
	}
	cert, key := selfSigned(t, "cad.test", "www.cad.test", "cad-alias.test")
	if _, err := m.SetCustomCertificate(ctx, "cad.test", CustomCertRequest{Certificate: cert, PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	for _, u := range [][2]string{{"ada", "ada password"}, {"tunde", "tunde password"}} {
		if _, err := m.SetAuthUser(ctx, "cad.test", u[0], u[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.SetProtectedPaths(ctx, "cad.test", []ProtectedPath{
		{Path: "/admin/", Realm: "Staff only", AllowIPs: []string{"10.200.0.0/16"}},
		{Path: "/admin/reports/", Realm: "Finance", Users: []string{"tunde"}},
	}); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("chmod", "-R", "a+rX", base, cfg.PagesDir, cfg.AcmeDir, "/etc/caddy/ike").Run()

	// The agent's password checks.
	_ = os.Remove(cfg.AuthSocket)
	al, err := net.Listen("unix", cfg.AuthSocket)
	if err != nil {
		t.Fatal(err)
	}
	authSrv := &http.Server{Handler: m.AuthHandler()}
	go func() { _ = authSrv.Serve(al) }()
	defer authSrv.Close()

	if out, err := exec.Command("caddy", "validate", "--adapter", "caddyfile", "--config", cfg.CaddyConfig).CombinedOutput(); err != nil {
		b, _ := os.ReadFile("/etc/caddy/ike/cad.test.caddy")
		t.Fatalf("caddy validate: %v\n%s\n%s", err, out, b)
	}
	_ = os.MkdirAll("/var/log/ike/cad.test", 0o755)
	_ = os.MkdirAll("/var/log/ike/app.cad.test", 0o755)
	caddy := exec.Command("caddy", "run", "--adapter", "caddyfile", "--config", cfg.CaddyConfig)
	caddy.Env = append(os.Environ(), "HOME="+base, "XDG_DATA_HOME="+base+"/data", "XDG_CONFIG_HOME="+base+"/config")
	logf, _ := os.Create(filepath.Join(base, "caddy.log"))
	caddy.Stdout, caddy.Stderr = logf, logf
	if err := caddy.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = caddy.Process.Kill(); _, _ = caddy.Process.Wait() }()
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", "127.0.0.1:443"); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	tr.DialContext = func(c context.Context, network, addr string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(addr)
		return (&net.Dialer{}).DialContext(c, network, "127.0.0.1:"+port)
	}
	cl := &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(url, user, pass string) (int, http.Header, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", url, nil)
		if user != "" {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header, string(b)
	}
	check := func(url, user, pass string, want int, body string) {
		t.Helper()
		code, h, got := get(url, user, pass)
		if code != want || (body != "" && !strings.Contains(got, body)) {
			lb, _ := os.ReadFile(filepath.Join(base, "caddy.log"))
			t.Errorf("%s as %q: %d %q (location %q), want %d %q\n%s", url, user, code, strings.TrimSpace(got), h.Get("Location"), want, body, lastLines(string(lb), 5))
		}
	}
	location := func(url string) string { _, h, _ := get(url, "", ""); return h.Get("Location") }

	check("http://cad.test/", "", "", 301, "")
	if l := location("http://cad.test/x?y=1"); l != "https://cad.test/x?y=1" {
		t.Errorf("force HTTPS went to %q", l)
	}
	if l := location("http://www.cad.test/a"); l != "https://cad.test/a" {
		t.Errorf("www went to %q", l)
	}
	if l := location("https://cad-alias.test/a"); l != "https://cad.test/a" {
		t.Errorf("alias went to %q", l)
	}
	check("https://cad.test/", "", "", 200, "home")
	check("https://cad.test/.env", "", "", 403, "")
	check("https://cad.test/missing", "", "", 404, "custom not found")
	if l := location("https://cad.test/old/page.html?x=1"); l != "/new/page.html" {
		t.Errorf("prefix redirect went to %q", l)
	}
	check("https://cad.test/latest", "", "", 200, "new page")
	check("https://cad.test/admin/", "", "", 401, "")
	if _, h, _ := get("https://cad.test/admin/", "", ""); !strings.Contains(h.Get("WWW-Authenticate"), `"Staff only"`) {
		t.Errorf("realm %q", h.Get("WWW-Authenticate"))
	}
	check("https://cad.test/admin/", "ada", "wrong", 401, "")
	check("https://cad.test/admin/", "ada", "ada password", 200, "admin area")
	check("https://cad.test/admin/reports/q3.html", "ada", "ada password", 401, "")
	if _, h, _ := get("https://cad.test/admin/reports/q3.html", "", ""); !strings.Contains(h.Get("WWW-Authenticate"), `"Finance"`) {
		t.Errorf("the longer path should decide the realm, got %q", h.Get("WWW-Authenticate"))
	}
	check("https://cad.test/admin/reports/q3.html", "tunde", "tunde password", 200, "q3 report")
	check("http://app.cad.test/hello", "", "", 200, "app saw app.cad.test/hello proto=http")

	// Maintenance: visitors get the status page with 503; certificate checks still work.
	if _, err := m.SetStatus(ctx, "cad.test", SetStatusRequest{Status: StatusMaintenance}); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command("chmod", "-R", "a+rX", "/etc/caddy/ike").Run()
	if out, err := exec.Command("caddy", "validate", "--adapter", "caddyfile", "--config", cfg.CaddyConfig).CombinedOutput(); err != nil {
		t.Fatalf("caddy validate: %v\n%s", err, out)
	}
	_ = caddy.Process.Kill()
	_, _ = caddy.Process.Wait()
	caddy = exec.Command("caddy", "run", "--adapter", "caddyfile", "--config", cfg.CaddyConfig)
	caddy.Env = append(os.Environ(), "HOME="+base, "XDG_DATA_HOME="+base+"/data", "XDG_CONFIG_HOME="+base+"/config")
	caddy.Stdout, caddy.Stderr = logf, logf
	if err := caddy.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	code, h, body := get("https://cad.test/", "", "")
	if code != 503 || h.Get("Retry-After") != "3600" || !strings.Contains(strings.ToLower(body), "back") {
		t.Errorf("maintenance: %d retry=%q %q", code, h.Get("Retry-After"), body)
	}
	_ = os.MkdirAll(filepath.Join(cfg.AcmeDir, ".well-known", "acme-challenge"), 0o755)
	_ = os.WriteFile(filepath.Join(cfg.AcmeDir, ".well-known", "acme-challenge", "tok"), []byte("tok.thumb"), 0o644)
	check("http://cad.test/.well-known/acme-challenge/tok", "", "", 200, "tok.thumb")
}
