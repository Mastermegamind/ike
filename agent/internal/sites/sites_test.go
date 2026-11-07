package sites

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

var ctx = context.Background()

func newTestManager(t *testing.T, mutate ...func(*config.Config)) (*Manager, *runner.DryRun, string) {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	for _, f := range mutate {
		f(&cfg)
	}
	for _, v := range []string{"5.6", "7.4", "8.3"} {
		if err := os.MkdirAll(filepath.Join(root, "etc/php", v, "fpm/pool.d"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r := &runner.DryRun{}
	m := NewManager(cfg, r)
	if err := m.EnsurePages(); err != nil {
		t.Fatal(err)
	}
	return m, r, root
}

func mustAccount(t *testing.T, m *Manager, name string) {
	t.Helper()
	if _, err := m.CreateAccount(ctx, name); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, root, p string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, p))
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}

func has(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s missing %q\n---\n%s", what, w, body)
		}
	}
}

func addRuntime(t *testing.T, root, rt, version, bin string) {
	t.Helper()
	dir := filepath.Join(root, "opt/ike/runtimes", rt, version, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, bin), nil, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAccountWithManyWebsites(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "adaeze")
	cmds := strings.Join(r.History(), "\n")
	has(t, "commands", cmds, "useradd --create-home --home-dir /home/adaeze --shell /usr/sbin/nologin --user-group adaeze")
	if fi, _ := os.Stat(filepath.Join(root, "home/adaeze")); fi.Mode().Perm() != 0o751 {
		t.Errorf("home mode %v", fi.Mode().Perm())
	}

	a, err := m.Create(ctx, CreateRequest{Domain: "shop.adaeze.com", Account: "adaeze"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Create(ctx, CreateRequest{Domain: "legacy.adaeze.com", Account: "adaeze", Version: "5.6"})
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != "8.3" || b.Version != "5.6" || a.Status != StatusActive || a.WebServer != "nginx" {
		t.Fatalf("unexpected sites %+v %+v", a, b)
	}

	keyA := "adaeze-" + siteID("shop.adaeze.com")
	vhost := read(t, root, "etc/nginx/ike/sites/shop.adaeze.com.conf")
	has(t, "vhost", vhost,
		"server_name shop.adaeze.com www.shop.adaeze.com;",
		"root /home/adaeze/web/shop.adaeze.com/public_html;",
		"fastcgi_pass unix:/run/php/php8.3-fpm-"+keyA+".sock;",
		"error_page 400 401 403 404 405 408 410 413 429 500 502 503 504 /__ike_error/$status.html;",
		"try_files /home/adaeze/web/shop.adaeze.com/errors/$status.html /etc/ike/pages/errors/$status.html =404;")
	if strings.Contains(vhost, "return 503") {
		t.Error("active site must not be gated")
	}
	pool := read(t, root, "etc/php/8.3/fpm/pool.d/ike-"+keyA+".conf")
	has(t, "pool", pool, "["+keyA+"]", "user = adaeze", "open_basedir] = /home/adaeze/web/shop.adaeze.com:/home/adaeze/tmp")
	read(t, root, "etc/php/5.6/fpm/pool.d/ike-adaeze-"+siteID("legacy.adaeze.com")+".conf")

	// Starter page and per-site error pages.
	has(t, "starter", read(t, root, "home/adaeze/web/shop.adaeze.com/public_html/index.php"), "shop.adaeze.com is ready", "PHP_VERSION")
	has(t, "404 page", read(t, root, "home/adaeze/web/shop.adaeze.com/errors/404.html"), "Page not found")

	accts, _ := m.Accounts()
	if len(accts) != 1 || accts[0].Sites != 2 {
		t.Errorf("Accounts() = %+v", accts)
	}

	// Deleting one website leaves the account and the other website.
	if err := m.Delete(ctx, "legacy.adaeze.com", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "home/adaeze/web/legacy.adaeze.com")); !os.IsNotExist(err) {
		t.Error("website folder not removed")
	}
	if _, err := m.Get("shop.adaeze.com"); err != nil {
		t.Error("other website was affected")
	}

	// Account deletion is refused while it has websites, unless asked.
	if err := m.DeleteAccount(ctx, "adaeze", false); err == nil || !strings.Contains(err.Error(), "shop.adaeze.com") {
		t.Errorf("expected refusal naming the website, got %v", err)
	}
	if err := m.DeleteAccount(ctx, "adaeze", true); err != nil {
		t.Fatal(err)
	}
	if list, _ := m.List(); len(list) != 0 {
		t.Errorf("websites left: %+v", list)
	}
}

func TestCreateValidation(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "owner")
	addRuntime(t, root, "node", "22", "node")
	if _, err := m.CreateAccount(ctx, "owner"); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate account: %v", err)
	}
	if _, err := m.CreateAccount(ctx, "root"); err == nil {
		t.Error("reserved account name accepted")
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "a.com", Account: "owner"}); err != nil {
		t.Fatal(err)
	}
	bad := []CreateRequest{
		{Domain: "a.com", Account: "owner"},      // duplicate
		{Domain: "b.com", Account: "nobodyhere"}, // no such account
		{Domain: "bad domain.com", Account: "owner"},
		{Domain: "b.com", Account: "owner", Version: "8.3;id"},
		{Domain: "b.com", Account: "owner", Version: "8.1"}, // not installed
		{Domain: "b.com", Account: "owner", Runtime: "ruby"},
		{Domain: "b.com", Account: "owner", Runtime: "node", Version: "../22"},
		{Domain: "b.com", Account: "owner", Runtime: "python"}, // none installed
		{Domain: "b.com", Account: "owner", Runtime: "node", StartCommand: "npm start\nUser=root"},
		{Domain: "b.com", Account: "owner", WebServer: "nginx+apache"}, // not enabled on this stack
	}
	for _, req := range bad {
		if _, err := m.Create(ctx, req); err == nil {
			t.Errorf("expected error for %+v", req)
		}
	}
}

func TestNodePythonStaticSites(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "dev")
	addRuntime(t, root, "node", "20", "node")
	addRuntime(t, root, "node", "22", "node")
	addRuntime(t, root, "python", "3.12", "python3")

	api, err := m.Create(ctx, CreateRequest{Domain: "api.dev.ng", Account: "dev", Runtime: "node"})
	if err != nil {
		t.Fatal(err)
	}
	if api.Version != "22" || api.Port != 3001 || api.StartCommand != "node server.js" {
		t.Fatalf("unexpected %+v", api)
	}
	key := "dev-" + siteID("api.dev.ng")
	has(t, "node vhost", read(t, root, "etc/nginx/ike/sites/api.dev.ng.conf"), "proxy_pass http://127.0.0.1:3001;", "error_page 502 504 ")
	has(t, "start script", read(t, root, "etc/ike/apps/"+key+".sh"), "export PORT=3001", "/opt/ike/runtimes/node/22/bin", "exec node server.js")
	has(t, "unit", read(t, root, "etc/systemd/system/ike-app-"+key+".service"),
		"User=dev", "NoNewPrivileges=yes", "ProtectSystem=strict", "BindPaths=/home/dev")
	has(t, "commands", strings.Join(r.History(), "\n"), "systemctl enable --now ike-app-"+key)

	py, err := m.Create(ctx, CreateRequest{Domain: "py.dev.ng", Account: "dev", Runtime: "python"})
	if err != nil || py.Port != 3002 {
		t.Fatalf("python: %v %+v", err, py)
	}
	has(t, "commands", strings.Join(r.History(), "\n"),
		"runuser -u dev -- /opt/ike/runtimes/python/3.12/bin/python3 -m venv /home/dev/web/py.dev.ng/app/.venv")

	st, err := m.Create(ctx, CreateRequest{Domain: "docs.dev.ng", Account: "dev", Runtime: "static"})
	if err != nil || st.Port != 0 || st.Version != "" {
		t.Fatalf("static: %v %+v", err, st)
	}
	sv := read(t, root, "etc/nginx/ike/sites/docs.dev.ng.conf")
	if strings.Contains(sv, "fastcgi_pass") || strings.Contains(sv, "proxy_pass") {
		t.Errorf("static vhost wrong:\n%s", sv)
	}

	up, err := m.Update(ctx, "api.dev.ng", UpdateRequest{Version: "20", StartCommand: "npm run serve"})
	if err != nil || up.Version != "20" {
		t.Fatalf("update: %v %+v", err, up)
	}
	has(t, "script", read(t, root, "etc/ike/apps/"+key+".sh"), "/node/20/bin", "exec npm run serve")
	if err := m.AppAction(ctx, "api.dev.ng", "kill"); err == nil {
		t.Error("invalid action accepted")
	}
}

func TestSwitchPHPVersion(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "legacy")
	if _, err := m.Create(ctx, CreateRequest{Domain: "old.com", Account: "legacy", Version: "8.3"}); err != nil {
		t.Fatal(err)
	}
	key := "legacy-" + siteID("old.com")
	s, err := m.Update(ctx, "old.com", UpdateRequest{Version: "5.6"})
	if err != nil || s.Version != "5.6" {
		t.Fatalf("%v %+v", err, s)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/php/8.3/fpm/pool.d/ike-"+key+".conf")); !os.IsNotExist(err) {
		t.Error("old pool not removed")
	}
	has(t, "vhost", read(t, root, "etc/nginx/ike/sites/old.com.conf"), "php5.6-fpm-"+key+".sock")
	if _, err := m.Update(ctx, "old.com", UpdateRequest{StartCommand: "node x"}); err == nil {
		t.Error("start command accepted for PHP site")
	}
}

func TestApacheStacks(t *testing.T) {
	// Hybrid: Nginx in front, each site picks.
	m, r, root := newTestManager(t, func(c *config.Config) { c.WebStack, c.DefaultWebServer = "hybrid", "nginx+apache" })
	mustAccount(t, m, "wpress")
	s, err := m.Create(ctx, CreateRequest{Domain: "blog.wp.ng", Account: "wpress"})
	if err != nil || s.WebServer != "nginx+apache" {
		t.Fatalf("%v %+v", err, s)
	}
	has(t, "nginx", read(t, root, "etc/nginx/ike/sites/blog.wp.ng.conf"), "proxy_pass http://127.0.0.1:8080;", "try_files $uri @apache;")
	has(t, "apache", read(t, root, "etc/apache2/ike/sites/blog.wp.ng.conf"),
		"<VirtualHost 127.0.0.1:8080>", "AllowOverride All",
		`SetHandler "proxy:unix:/run/php/php8.3-fpm-wpress-`+siteID("blog.wp.ng")+`.sock|fcgi://localhost"`,
		"ErrorDocument 404 /__ike_error/404.html")
	has(t, "commands", strings.Join(r.History(), "\n"), "apache2ctl configtest", "systemctl reload apache2")

	// Switch to plain Nginx: Apache vhost goes away.
	if _, err := m.Update(ctx, "blog.wp.ng", UpdateRequest{WebServer: "nginx"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/apache2/ike/sites/blog.wp.ng.conf")); !os.IsNotExist(err) {
		t.Error("apache vhost should be removed")
	}
	if _, err := m.Update(ctx, "blog.wp.ng", UpdateRequest{WebServer: "apache"}); err == nil {
		t.Error("apache-only mode accepted on hybrid stack")
	}

	// Apache-only stack: no Nginx vhost at all.
	m2, _, root2 := newTestManager(t, func(c *config.Config) { c.WebStack, c.ApacheListen = "apache", "*:80" })
	mustAccount(t, m2, "apweb")
	if _, err := m2.Create(ctx, CreateRequest{Domain: "a.ap.ng", Account: "apweb"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root2, "etc/nginx/ike/sites/a.ap.ng.conf")); !os.IsNotExist(err) {
		t.Error("nginx vhost written on apache-only stack")
	}
	has(t, "apache", read(t, root2, "etc/apache2/ike/sites/a.ap.ng.conf"), "<VirtualHost *:80>")
}

func TestMaintenanceAndSuspension(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "shop")
	addRuntime(t, root, "node", "22", "node")
	if _, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "api.shop.ng", Account: "shop", Runtime: "node"}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.SetStatus(ctx, "shop.ng", SetStatusRequest{Status: "maintenance", AllowIPs: []string{"203.0.113.9"}}); err != nil {
		t.Fatal(err)
	}
	has(t, "maintenance vhost", read(t, root, "etc/nginx/ike/sites/shop.ng.conf"),
		`if ($remote_addr = "203.0.113.9") { set $ike_open 1; }`, "return 503;", "try_files /etc/ike/pages/maintenance.html =503;")
	if _, err := m.SetStatus(ctx, "shop.ng", SetStatusRequest{Status: "maintenance", AllowIPs: []string{"1.2.3.4\"; return 200"}}); err == nil {
		t.Error("bad IP accepted")
	}

	a, err := m.SuspendAccount(ctx, "shop", "Unpaid invoice")
	if err != nil || !a.Suspended {
		t.Fatalf("%v %+v", err, a)
	}
	has(t, "suspended vhost", read(t, root, "etc/nginx/ike/sites/shop.ng.conf"), "try_files /etc/ike/pages/suspended.html =503;")
	has(t, "commands", strings.Join(r.History(), "\n"), "systemctl stop ike-app-shop-"+siteID("api.shop.ng"))
	if _, err := m.SetSSHMode(ctx, "shop", SSHShell); err == nil {
		t.Error("SSH enabled on suspended account")
	}
	if err := m.AppAction(ctx, "api.shop.ng", "start"); err == nil {
		t.Error("app started on suspended site")
	}

	if _, err := m.UnsuspendAccount(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	s, _ := m.Get("shop.ng")
	if s.Status != StatusMaintenance {
		t.Errorf("previous status not restored: %s", s.Status)
	}
	api, _ := m.Get("api.shop.ng")
	if api.Status != StatusActive {
		t.Errorf("api status: %s", api.Status)
	}
}

func TestCronJobs(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "cronny")
	j, err := m.AddCronJob("cronny", CronRequest{Schedule: "*/15 * * * *", Command: "php web/x.com/public_html/cron.php", Comment: "Queue"})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := m.AddCronJob("cronny", CronRequest{Schedule: "@daily", Command: `date +%F >> backup.log`, Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	f := read(t, root, "etc/cron.d/ike-cronny")
	has(t, "cron file", f,
		"*/15 * * * * cronny cd ~ && php web/x.com/public_html/cron.php",
		`# disabled: @daily cronny cd ~ && date +\%F >> backup.log`, "# Queue")

	for _, bad := range []CronRequest{
		{Schedule: "* * * *", Command: "x"},
		{Schedule: "61 * * * *", Command: "x"},
		{Schedule: "* * * * *", Command: "echo hi\n* * * * * root rm -rf /"},
		{Schedule: "* * * * * root", Command: "x"},
		{Schedule: "* * * * *", Command: ""},
	} {
		if _, err := m.AddCronJob("cronny", bad); err == nil {
			t.Errorf("accepted bad job %+v", bad)
		}
	}

	if _, err := m.SuspendAccount(ctx, "cronny", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/cron.d/ike-cronny")); !os.IsNotExist(err) {
		t.Error("cron still active while suspended")
	}
	if _, err := m.UnsuspendAccount(ctx, "cronny"); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteCronJob("cronny", j.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(read(t, root, "etc/cron.d/ike-cronny"), "cron.php") {
		t.Error("deleted job still installed")
	}
}

func testKey(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	for _, part := range [][]byte{[]byte("ssh-ed25519"), pub} {
		l := make([]byte, 4)
		binary.BigEndian.PutUint32(l, uint32(len(part)))
		blob = append(append(blob, l...), part...)
	}
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " " + comment
}

func TestParsePublicKey(t *testing.T) {
	good := testKey(t, "marvellous@laptop")
	k, err := ParsePublicKey(good)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.Comment != "marvellous@laptop" {
		t.Errorf("unexpected key %+v", k)
	}
	b64 := strings.Fields(good)[1]
	for _, s := range []string{
		"", "ssh-ed25519", `command="rm -rf /" ` + good, "ssh-rsa " + b64, "ssh-ed25519 not-base64!!",
		good + "\nssh-ed25519 " + b64, "ssh-ed25519 " + b64 + " evil;comment", "ssh-dss " + b64,
	} {
		if _, err := ParsePublicKey(s); err == nil {
			t.Errorf("ParsePublicKey(%q) expected error", s)
		}
	}
}

func TestAccountSSH(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "sshuser")
	a, err := m.SetSSHMode(ctx, "sshuser", SSHSFTP)
	if err != nil || a.SSH != SSHSFTP {
		t.Fatalf("%v %+v", err, a)
	}
	has(t, "commands", strings.Join(r.History(), "\n"), "usermod --shell /usr/sbin/nologin --groups ike-sftp sshuser")
	if _, err := m.SetSSHMode(ctx, "sshuser", "root"); err == nil {
		t.Error("invalid mode accepted")
	}
	key := testKey(t, "laptop")
	k, err := m.AddSSHKey("sshuser", key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddSSHKey("sshuser", key); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate key: %v", err)
	}
	has(t, "keys file", read(t, root, "etc/ssh/ike/keys/sshuser"), strings.Fields(key)[1])
	if err := m.RemoveSSHKey("sshuser", k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if keys, _ := m.ListSSHKeys("sshuser"); len(keys) != 0 {
		t.Errorf("key not removed: %+v", keys)
	}
}

func TestCustomPagesAreKept(t *testing.T) {
	m, _, root := newTestManager(t)
	custom := filepath.Join(root, "etc/ike/pages/errors/404.html")
	if err := os.WriteFile(custom, []byte("<h1>Our own 404</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.EnsurePages(); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "etc/ike/pages/errors/404.html") != "<h1>Our own 404</h1>" {
		t.Error("admin's page was overwritten")
	}
	mustAccount(t, m, "brand")
	if _, err := m.Create(ctx, CreateRequest{Domain: "brand.ng", Account: "brand"}); err != nil {
		t.Fatal(err)
	}
	if read(t, root, "home/brand/web/brand.ng/errors/404.html") != "<h1>Our own 404</h1>" {
		t.Error("new website didn't get the admin's branded page")
	}
}

func TestDatabasesAndWordPress(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "blog")
	db, err := m.CreateDatabase(ctx, "blog", "main", "Main DB")
	if err != nil || db.Name != "blog_main" || len(db.Password) != 32 {
		t.Fatalf("%v %+v", err, db)
	}
	for _, c := range r.History() {
		if strings.Contains(c, db.Password) {
			t.Fatalf("password leaked into command line: %s", c)
		}
	}
	if !strings.Contains(strings.Join(r.Stdin, "\n"), "CREATE DATABASE `blog_main`") {
		t.Error("SQL not sent on stdin")
	}
	for _, bad := range []string{"", "Main", "a-b", "x`; DROP DATABASE mysql; --", strings.Repeat("a", 17)} {
		if _, err := m.CreateDatabase(ctx, "blog", bad, ""); err == nil {
			t.Errorf("bad db name %q accepted", bad)
		}
	}

	if _, err := m.Create(ctx, CreateRequest{Domain: "blog.ng", Account: "blog"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "opt/ike/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "opt/ike/bin/wp"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := m.InstallWordPress(ctx, "blog.ng", WordPressRequest{Title: "Ada's Blog", AdminUser: "ada", AdminEmail: "ada@blog.ng"})
	if err != nil {
		t.Fatal(err)
	}
	cmds := strings.Join(r.History(), "\n")
	has(t, "wp commands", cmds, "runuser -u blog -- /usr/bin/php8.3 /opt/ike/bin/wp --path=/home/blog/web/blog.ng/public_html",
		"core download", "config create --dbname=blog_wp", "--prompt=dbpass", "core install --url=http://blog.ng")
	if strings.Contains(cmds, res.AdminPassword) || strings.Contains(cmds, res.Database.Password) {
		t.Error("secret passed as an argument")
	}
	if _, err := m.InstallWordPress(ctx, "blog.ng", WordPressRequest{Title: "x", AdminUser: "admin", AdminEmail: "a@b.co"}); err == nil {
		t.Error("username admin accepted")
	}
	dbs, _ := m.Databases("blog")
	if len(dbs) != 2 {
		t.Errorf("databases: %+v", dbs)
	}
	if err := m.DeleteDatabase(ctx, "blog", "blog_main"); err != nil {
		t.Fatal(err)
	}
}

func TestLogsAndPermissions(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "logs")
	if _, err := m.Create(ctx, CreateRequest{Domain: "logs.ng", Account: "logs"}); err != nil {
		t.Fatal(err)
	}
	var lines []string
	for i := 1; i <= 300; i++ {
		lines = append(lines, "GET /page"+strings.Repeat("x", i%3)+" 200")
	}
	if err := os.WriteFile(filepath.Join(root, "var/log/ike/logs.ng/access.log"), []byte(strings.Join(lines, "\n")+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	out, err := m.SiteLog("logs.ng", "access", 10)
	if err != nil || len(strings.Split(out, "\n")) != 10 {
		t.Fatalf("tail: %v %q", err, out)
	}
	// A symlink in the user-controlled logs folder must not be followed.
	php := filepath.Join(root, "home/logs/web/logs.ng/logs/php-error.log")
	if err := os.Symlink("/etc/passwd", php); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SiteLog("logs.ng", "php", 10); err == nil {
		t.Error("followed a symlinked log")
	}
	if _, err := m.SiteLog("logs.ng", "../../etc/shadow", 10); err == nil {
		t.Error("arbitrary log kind accepted")
	}

	stats, err := m.FixPermissions(ctx, "logs.ng")
	if err != nil {
		t.Fatal(err)
	}
	if stats["skipped_links"] != 1 {
		t.Errorf("stats %+v", stats)
	}
	has(t, "perm commands", strings.Join(r.History(), "\n"),
		"runuser -u logs -- find /home/logs/web/logs.ng/public_html -type d -exec chmod 2750 {} +",
		"-name wp-config.php -o -name .env")
}
