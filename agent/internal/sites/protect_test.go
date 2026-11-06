package sites

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/mail"
)

func TestPasswordProtection(t *testing.T) {
	m, _, root := newTestManager(t, func(c *config.Config) { c.WebStack, c.DefaultWebServer = "hybrid", "nginx" })
	mustAccount(t, m, "shop")
	if _, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop"}); err != nil {
		t.Fatal(err)
	}
	// A path can't be protected before anyone can sign in.
	if _, err := m.SetProtectedPaths(ctx, "shop.ng", []ProtectedPath{{Path: "/admin/"}}); err == nil || !strings.Contains(err.Error(), "add a user first") {
		t.Fatalf("want 'add a user first', got %v", err)
	}
	pw, err := m.SetAuthUser(ctx, "shop.ng", "ada", "")
	if err != nil || len(pw) < 16 {
		t.Fatalf("generated password %q, %v", pw, err)
	}
	if _, err := m.SetAuthUser(ctx, "shop.ng", "tunde", "correct horse"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		p    ProtectedPath
		want string
	}{
		{ProtectedPath{Path: "/a b/"}, "not a path"},
		{ProtectedPath{Path: "/x/../etc/"}, "not a path"},
		{ProtectedPath{Path: "/.well-known/acme-challenge/"}, "used by Ike"},
		{ProtectedPath{Path: "/x/", Realm: `a"b`}, "sign-in message"},
		{ProtectedPath{Path: "/x/", Users: []string{"nobody"}}, "isn't a user"},
		{ProtectedPath{Path: "/x/", AllowIPs: []string{"10.0.0.300"}}, "not an IP"},
	} {
		if _, err := m.SetProtectedPaths(ctx, "shop.ng", []ProtectedPath{bad.p}); err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Errorf("%+v: want %q, got %v", bad.p, bad.want, err)
		}
	}
	if _, err := m.SetAuthUser(ctx, "shop.ng", "x", "short"); err == nil {
		t.Error("a 5-character password was accepted")
	}
	if _, err := m.SetAuthUser(ctx, "shop.ng", "bad:name", "long enough"); err == nil {
		t.Error("a user name with ':' was accepted (it would break the htpasswd file)")
	}

	pr, err := m.SetProtectedPaths(ctx, "shop.ng", []ProtectedPath{
		{Path: "/admin", Realm: "Shop admin", AllowIPs: []string{"203.0.113.0/24", "198.51.100.7"}},
		{Path: "/admin/reports/", Users: []string{"tunde"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pr.Paths[0].Path != "/admin/reports/" {
		t.Fatalf("paths should be stored longest first: %+v", pr.Paths)
	}
	adminID, reportsID := pr.Paths[1].ID, pr.Paths[0].ID

	vhost := read(t, root, "etc/nginx/ike/sites/shop.ng.conf")
	has(t, "nginx", vhost,
		`geo $ike_auth_ok_`+siteID("shop.ng")+`_`+adminID+` {`, `203.0.113.0/24 1;`, `198.51.100.7 1;`,
		`if ($uri ~ "^/admin(/|$)") { set $ike_auth_rule `+adminID+`; }`,
		`if ($uri ~ "^/admin/reports(/|$)") { set $ike_auth_rule `+reportsID+`; }`,
		`set $ike_auth "Shop admin"; set $ike_auth_file /etc/ike/htpasswd/shop.ng/`+adminID+`;`,
		`if ($ike_auth_ip = 1) { set $ike_auth off; }`, "auth_basic $ike_auth;", "auth_basic_user_file $ike_auth_file;")
	// The shorter path is matched first so the longer one wins.
	if strings.Index(vhost, "^/admin(/|$)") > strings.Index(vhost, "^/admin/reports(/|$)") {
		t.Error("/admin must be checked before /admin/reports/")
	}
	if strings.Count(vhost, "auth_basic off;") < 2 {
		t.Error("certificate checks and error pages must stay open")
	}

	// Per-path files: /admin/ lets both users in, /admin/reports/ only tunde.
	admin := read(t, root, "etc/ike/htpasswd/shop.ng/"+adminID)
	reports := read(t, root, "etc/ike/htpasswd/shop.ng/"+reportsID)
	if !strings.Contains(admin, "ada:$6$") || !strings.Contains(admin, "tunde:$6$") || strings.Contains(reports, "ada:") || !strings.Contains(reports, "tunde:") {
		t.Fatalf("admin file:\n%s\nreports file:\n%s", admin, reports)
	}
	line := strings.Split(strings.TrimSpace(reports), "\n")[0]
	if !mail.CheckPassword(strings.SplitN(line, ":", 2)[1], "correct horse") {
		t.Fatal("stored hash doesn't match the password")
	}
	if st, _ := os.Stat(filepath.Join(root, "etc/ike/htpasswd/shop.ng", adminID)); st.Mode().Perm() != 0o640 {
		t.Fatalf("htpasswd mode %v", st.Mode().Perm())
	}
	// The API view never carries hashes.
	got, _ := m.GetProtection("shop.ng")
	if b := fmt.Sprintf("%+v", got); strings.Contains(b, "$6$") {
		t.Fatal("hash leaked into the protection view")
	}

	// The last named user of a path can't be removed: the path would open up.
	if err := m.DeleteAuthUser(ctx, "shop.ng", "tunde"); err == nil || !strings.Contains(err.Error(), "only user allowed") {
		t.Fatalf("want refusal, got %v", err)
	}
	// Password change keeps the user and rewrites the files.
	if _, err := m.SetAuthUser(ctx, "shop.ng", "ada", "new password 1"); err != nil {
		t.Fatal(err)
	}
	if a := read(t, root, "etc/ike/htpasswd/shop.ng/"+adminID); a == admin {
		t.Fatal("password change didn't reach the path file")
	}
	if err := m.DeleteAuthUser(ctx, "shop.ng", "ada"); err != nil {
		t.Fatal(err)
	}
	if a := read(t, root, "etc/ike/htpasswd/shop.ng/"+adminID); strings.Contains(a, "ada:") {
		t.Fatal("deleted user still in the path file")
	}

	// Apache alone: <LocationMatch> blocks, challenges stay open.
	if _, err := m.Update(ctx, "shop.ng", UpdateRequest{WebServer: "nginx+apache"}); err != nil {
		t.Fatal(err)
	}
	ap := read(t, root, "etc/apache2/ike/sites/shop.ng.conf")
	if strings.Contains(ap, "AuthType Basic") {
		t.Fatal("behind Nginx, Nginx checks passwords; Apache must not ask again")
	}

	// Removing every path and user removes the files and the config.
	if _, err := m.SetProtectedPaths(ctx, "shop.ng", nil); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteAuthUser(ctx, "shop.ng", "tunde"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/ike/htpasswd/shop.ng")); !os.IsNotExist(err) {
		t.Fatal("htpasswd folder left behind")
	}
	if v := read(t, root, "etc/nginx/ike/sites/shop.ng.conf"); strings.Contains(v, "auth_basic") {
		t.Fatal("auth left in the vhost")
	}
}

func TestPasswordProtectionApacheOnly(t *testing.T) {
	m, _, root := newTestManager(t, func(c *config.Config) { c.WebStack, c.DefaultWebServer, c.ApacheListen = "apache", "apache", "*:80" })
	mustAccount(t, m, "shop")
	if _, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAuthUser(ctx, "shop.ng", "ada", "long enough"); err != nil {
		t.Fatal(err)
	}
	pr, err := m.SetProtectedPaths(ctx, "shop.ng", []ProtectedPath{{Path: "/", Realm: "Coming soon", AllowIPs: []string{"203.0.113.9"}}})
	if err != nil {
		t.Fatal(err)
	}
	ap := read(t, root, "etc/apache2/ike/sites/shop.ng.conf")
	has(t, "apache", ap, `<LocationMatch "^/">`, `AuthName "Coming soon"`, "AuthUserFile /etc/ike/htpasswd/shop.ng/"+pr.Paths[0].ID,
		"Require valid-user", "Require ip 203.0.113.9", `<LocationMatch "^/(\.well-known/acme-challenge/|__ike_error/|__ike_status\.html$)">`)
}

// TestRealPasswordProtection starts real Nginx, then real Apache, on port 80
// and signs in with curl-like requests. It writes under /etc and binds port 80,
// so it only runs on a throwaway machine:
//
//	IKE_REAL_HTTP=1 go test ./internal/sites -run RealPasswordProtection
func TestRealPasswordProtection(t *testing.T) {
	if os.Getenv("IKE_REAL_HTTP") != "1" {
		t.Skip("set IKE_REAL_HTTP=1 on a throwaway machine with nginx and apache2 installed")
	}
	for _, stack := range []string{"nginx", "apache"} {
		t.Run(stack, func(t *testing.T) { realProtection(t, stack) })
	}
}

func realProtection(t *testing.T, stack string) {
	for _, d := range []string{"/etc/nginx/ike/sites", "/etc/apache2/ike/sites", "/var/lib/ike/sites", "/etc/ike/htpasswd"} {
		_ = os.RemoveAll(d)
	}
	cfg := config.Default()
	cfg.Root, cfg.DryRun = "/", true
	cfg.WebStack, cfg.DefaultWebServer = "hybrid", "nginx"
	if stack == "apache" {
		cfg.WebStack, cfg.DefaultWebServer, cfg.ApacheListen = "apache", "apache", "*:80"
	}
	base := t.TempDir()
	// Go makes the test's temp parent 0700; the web server's workers must get through.
	_ = os.Chmod(filepath.Dir(base), 0o755)
	_ = os.Chmod(base, 0o755)
	cfg.AccountsDir, cfg.HomeBase, cfg.CronDir = filepath.Join(base, "acc"), filepath.Join(base, "home"), filepath.Join(base, "cron")
	m, _, _ := newTestManagerAt(t, cfg)
	m.SetWebFeatures(WebFeatures{NginxVersion: "1.24.0"})
	mustAccount(t, m, "prot")
	if _, err := m.Create(ctx, CreateRequest{Domain: "prot.test", Account: "prot", Runtime: "static"}); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get("prot.test")
	L := m.layout(s)
	for p, body := range map[string]string{"index.html": "home", "admin/index.html": "admin area", "admin/reports/q3.html": "q3 report", "office/index.html": "office"} {
		f := filepath.Join(L.DocRoot, p)
		_ = os.MkdirAll(filepath.Dir(f), 0o755)
		_ = os.WriteFile(f, []byte(body), 0o644)
	}
	// Dry run skips the chown that gives the web server's group access to
	// homes and pages; open them for reading instead.
	_ = exec.Command("chmod", "-R", "a+rX", base, cfg.PagesDir, cfg.AcmeDir).Run()
	for _, u := range [][2]string{{"ada", "ada password"}, {"tunde", "tunde password"}} {
		if _, err := m.SetAuthUser(ctx, "prot.test", u[0], u[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.SetProtectedPaths(ctx, "prot.test", []ProtectedPath{
		{Path: "/admin/", Realm: "Staff only", AllowIPs: []string{"10.200.0.0/16"}},
		{Path: "/admin/reports/", Realm: "Finance", Users: []string{"tunde"}},
		{Path: "/office/", Realm: "Office", AllowIPs: []string{"127.0.0.1"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Dry run skips chown: let the web server's group read the user files.
	_ = exec.Command("chgrp", "-R", "www-data", "/etc/ike/htpasswd").Run()

	dir := t.TempDir()
	// Test machines without IPv6 can't bind [::]:80; Ike's real config keeps it.
	if _, err := os.Stat("/proc/net/if_inet6"); err != nil {
		for _, f := range []string{"/etc/nginx/ike/sites/prot.test.conf"} {
			b, _ := os.ReadFile(f)
			var keep []string
			for _, l := range strings.Split(string(b), "\n") {
				if !strings.Contains(l, "listen [::]") {
					keep = append(keep, l)
				}
			}
			_ = os.WriteFile(f, []byte(strings.Join(keep, "\n")), 0o644)
		}
	}
	var stop func()
	if stack == "nginx" {
		conf := "user www-data;\npid " + dir + "/nginx.pid;\nerror_log " + dir + "/error.log;\nevents {}\nhttp {\n    access_log off;\n" +
			"    map $http_upgrade $connection_upgrade_ike { default upgrade; '' close; }\n    include /etc/nginx/ike/sites/*.conf;\n}\n"
		_ = os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644)
		_ = os.MkdirAll("/var/log/ike/prot.test", 0o755)
		if out, err := exec.Command("nginx", "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput(); err != nil {
			t.Fatalf("start nginx: %v\n%s", err, out)
		}
		stop = func() { _ = exec.Command("nginx", "-c", filepath.Join(dir, "nginx.conf"), "-s", "stop").Run() }
	} else {
		mods := []string{"mpm_event", "authz_core", "authz_host", "authz_user", "authn_core", "authn_file", "auth_basic", "alias", "rewrite", "headers",
			"setenvif", "dir", "mime"}
		conf := "ServerRoot /etc/apache2\nPidFile " + dir + "/apache.pid\nMutex file:" + dir + "\nListen 80\nErrorLog " + dir + "/apache.log\nUser www-data\nGroup www-data\n" +
			"TypesConfig /etc/mime.types\n"
		for _, mod := range mods {
			conf += "LoadModule " + mod + "_module /usr/lib/apache2/modules/mod_" + mod + ".so\n"
		}
		conf += "IncludeOptional /etc/apache2/ike/sites/*.conf\n"
		_ = os.WriteFile(filepath.Join(dir, "apache.conf"), []byte(conf), 0o644)
		_ = os.MkdirAll("/var/log/ike/prot.test", 0o755)
		if out, err := exec.Command("apache2", "-f", filepath.Join(dir, "apache.conf"), "-k", "start").CombinedOutput(); err != nil {
			t.Fatalf("start apache: %v\n%s", err, out)
		}
		stop = func() {
			_ = exec.Command("apache2", "-f", filepath.Join(dir, "apache.conf"), "-k", "stop").Run()
			time.Sleep(500 * time.Millisecond)
		}
	}
	defer stop()
	time.Sleep(700 * time.Millisecond)

	get := func(path, user, pass string) (int, string, string) {
		req, _ := http.NewRequest("GET", "http://127.0.0.1"+path, nil)
		req.Host = "prot.test"
		if user != "" {
			req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, resp.Header.Get("WWW-Authenticate"), string(b)
	}
	check := func(path, user, pass string, want int, body string) {
		t.Helper()
		code, auth, got := get(path, user, pass)
		if code != want || (body != "" && !strings.Contains(got, body)) {
			t.Errorf("%s %s as %q: %d %q (auth %q), want %d %q", stack, path, user, code, strings.TrimSpace(got), auth, want, body)
		}
	}
	check("/", "", "", 200, "home")
	check("/admin/", "", "", 401, "")
	if _, auth, _ := get("/admin/", "", ""); !strings.Contains(auth, `"Staff only"`) {
		t.Errorf("%s: realm %q", stack, auth)
	}
	check("/admin/", "ada", "wrong", 401, "")
	check("/admin/", "ada", "ada password", 200, "admin area")
	check("/admin/reports/q3.html", "ada", "ada password", 401, "")
	if _, auth, _ := get("/admin/reports/q3.html", "", ""); !strings.Contains(auth, `"Finance"`) {
		t.Errorf("%s: the longer path should decide the realm, got %q", stack, auth)
	}
	check("/admin/reports/q3.html", "tunde", "tunde password", 200, "q3 report")
	check("/adminx", "", "", 404, "") // /admin must not swallow /adminx
	check("/.well-known/acme-challenge/none", "", "", 404, "")
	check("/office/", "", "", 200, "office") // this test's address is let in without a password
}
