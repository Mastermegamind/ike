package sites

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
)

func TestHealthAssistantReadsLogs(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "naija")
	if _, err := m.Create(ctx, CreateRequest{Domain: "naija.ng", Account: "naija", Version: "7.4"}); err != nil {
		t.Fatal(err)
	}
	php := filepath.Join(root, "home/naija/web/naija.ng/logs/php-error.log")
	_ = os.WriteFile(php, []byte(`[13-Oct-2026 10:01:02 UTC] PHP Warning:  Undefined variable $x in /home/naija/web/naija.ng/public_html/x.php on line 3
[13-Oct-2026 10:05:00 UTC] PHP Fatal error:  Uncaught Error: Call to undefined function create_function() in /home/naija/web/naija.ng/public_html/wp-content/plugins/old-slider/slider.php:44
`), 0o640)
	d, err := m.Diagnose(ctx, "naija.ng")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Summary, "old-slider") {
		t.Errorf("summary should blame the plugin: %q", d.Summary)
	}
	var titles []string
	for _, f := range d.Findings {
		titles = append(titles, f.Title)
	}
	all := strings.Join(titles, " | ")
	for _, want := range []string{"PHP 7.4 no longer gets security fixes", "PHP is logging warnings", "old-slider"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in %s", want, all)
		}
	}
	for _, line := range []string{
		"2026/10/13 [error] connect() failed (111: Connection refused) while connecting to upstream, upstream: \"http://127.0.0.1:3001/\"",
		"Error: Cannot find module 'express'",
		"ModuleNotFoundError: No module named 'flask'",
	} {
		if len(scanLogs(line)) != 1 {
			t.Errorf("no rule matched %q", line)
		}
	}
}

func TestStagingCloneAndPush(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "shop")
	if _, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop"}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "home/shop/web/shop.ng/public_html/wp-config.php"), []byte("<?php"), 0o640)
	_ = os.MkdirAll(filepath.Join(root, "opt/ike/bin"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "opt/ike/bin/wp"), nil, 0o755)
	logs := []string{}
	res, err := m.CreateStaging(ctx, "shop.ng", func(f string, a ...any) { logs = append(logs, f) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Staging != "staging.shop.ng" || !res.WordPress || res.Database == "" {
		t.Fatalf("%+v", res)
	}
	vh := read(t, root, "etc/nginx/ike/sites/staging.shop.ng.conf")
	has(t, "staging vhost", vh, `X-Robots-Tag "noindex, nofollow"`)
	cmds := strings.Join(r.History(), "\n")
	has(t, "staging cmds", cmds, "cp -a --no-dereference /home/shop/web/shop.ng/public_html/. /home/shop/web/staging.shop.ng/public_html/",
		"db export", "search-replace //shop.ng //staging.shop.ng", "--prompt=value")
	if _, err := m.CreateStaging(ctx, "staging.shop.ng", func(string, ...any) {}); err == nil {
		t.Error("staging of staging allowed")
	}
	backup, err := m.PushStaging(ctx, "staging.shop.ng", func(string, ...any) {})
	if err != nil || !strings.Contains(backup, "ike-before-push-shop.ng") {
		t.Fatalf("%v %s", err, backup)
	}
	has(t, "push cmds", strings.Join(r.History(), "\n"), "rsync -a --exclude=/wp-config.php --exclude=/.env")
	if _, err := m.PushStaging(ctx, "shop.ng", func(string, ...any) {}); err == nil {
		t.Error("pushing a live site allowed")
	}
}

func TestLimits(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "heavy")
	addRuntime(t, root, "node", "22", "node")
	if _, err := m.Create(ctx, CreateRequest{Domain: "a.heavy.ng", Account: "heavy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "api.heavy.ng", Account: "heavy", Runtime: "node"}); err != nil {
		t.Fatal(err)
	}
	a, err := m.SetLimits(ctx, "heavy", Limits{CPUPercent: 150, MemoryMB: 1024, MaxProcesses: 64, PHPWorkers: 8, DiskMB: 5000})
	if err != nil {
		t.Fatal(err)
	}
	if a.Limits.CPUPercent != 150 {
		t.Error("limits not saved")
	}
	has(t, "slice", read(t, root, "etc/systemd/system/ike-acct-heavy.slice"), "CPUQuota=150%", "MemoryMax=1024M", "TasksMax=64")
	has(t, "pool", read(t, root, "etc/php/8.3/fpm/pool.d/ike-heavy-"+siteID("a.heavy.ng")+".conf"), "pm.max_children = 8", "memory_limit] = 128M")
	has(t, "unit", read(t, root, "etc/systemd/system/ike-app-heavy-"+siteID("api.heavy.ng")+".service"), "Slice=ike-acct-heavy.slice", "StartLimitBurst=10")
	for _, bad := range []Limits{{CPUPercent: -1}, {MemoryMB: 10}, {MaxProcesses: 3}, {PHPWorkers: 500}} {
		if _, err := m.SetLimits(ctx, "heavy", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestAppHealthCheckRestarts(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "app")
	addRuntime(t, root, "node", "22", "node")
	healthy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !healthy {
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	_, portStr, _ := net.SplitHostPort(srv.Listener.Addr().String())
	port, _ := strconv.Atoi(portStr)

	s, err := m.Create(ctx, CreateRequest{Domain: "api.app.ng", Account: "app", Runtime: "node"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetHealthCheck("api.app.ng", &HealthCheck{Path: "/health;rm", IntervalSec: 10}); err == nil {
		t.Error("bad path accepted")
	}
	if _, err := m.SetHealthCheck("api.app.ng", &HealthCheck{Path: "/health", IntervalSec: 10, FailThreshold: 2}); err != nil {
		t.Fatal(err)
	}
	// Point the stored site at the test server's port.
	s, _ = m.Get("api.app.ng")
	s.Port = port
	_ = m.writeJSON(m.layout(s).StateFile, s)

	now := time.Now()
	m.healthTick(ctx, now)
	healthy = false
	m.healthTick(ctx, now.Add(11*time.Second))
	m.healthTick(ctx, now.Add(22*time.Second))
	if !strings.Contains(strings.Join(r.History(), "\n"), "systemctl restart ike-app-app-"+siteID("api.app.ng")) {
		t.Fatal("unhealthy app was not restarted")
	}
	ev := m.HealthEvents()
	if len(ev) < 2 || ev[0].Kind != "restarted" {
		t.Errorf("events %+v", ev)
	}
	healthy = true
	m.healthTick(ctx, now.Add(33*time.Second))
	m.healthTick(ctx, now.Add(44*time.Second))
}

func TestDriftDetectAndReconcile(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "drift")
	if _, err := m.Create(ctx, CreateRequest{Domain: "drift.ng", Account: "drift"}); err != nil {
		t.Fatal(err)
	}
	if d := m.DetectDrift(); len(d) != 0 {
		t.Fatalf("fresh site drifted: %+v", d)
	}
	vh := filepath.Join(root, "etc/nginx/ike/sites/drift.ng.conf")
	orig, _ := os.ReadFile(vh)
	_ = os.WriteFile(vh, bytes.Replace(orig, []byte("client_max_body_size 65m;"), []byte("client_max_body_size 2g;"), 1), 0o644)
	_ = os.Remove(filepath.Join(root, "etc/php/8.3/fpm/pool.d/ike-drift-"+siteID("drift.ng")+".conf"))
	d := m.DetectDrift()
	if len(d) != 2 {
		t.Fatalf("want 2 drifts, got %+v", d)
	}
	var diff string
	for _, x := range d {
		if x.Kind == "nginx" {
			diff = x.Diff
		}
	}
	has(t, "diff", diff, "-     client_max_body_size 65m;", "+     client_max_body_size 2g;")
	fixed, err := m.Reconcile(ctx, nil)
	if err != nil || len(fixed) != 2 {
		t.Fatalf("%v %+v", err, fixed)
	}
	if now, _ := os.ReadFile(vh); !bytes.Equal(now, orig) {
		t.Error("vhost not restored")
	}
	has(t, "reload", strings.Join(r.History(), "\n"), "nginx -t", "systemctl reload php8.3-fpm")
	if d := m.DetectDrift(); len(d) != 0 {
		t.Errorf("still drifted: %+v", d)
	}
}

func TestEnvEditor(t *testing.T) {
	m, r, _ := newTestManager(t)
	mustAccount(t, m, "envy")
	if _, err := m.Create(ctx, CreateRequest{Domain: "envy.ng", Account: "envy"}); err != nil {
		t.Fatal(err)
	}
	vars := []EnvVar{{"APP_KEY", "base64:abc'def"}, {"DB_PASSWORD", "p@ss $word"}}
	if err := m.SetEnv(ctx, "envy.ng", vars); err != nil {
		t.Fatal(err)
	}
	got, err := m.GetEnv("envy.ng")
	if err != nil || len(got) != 2 || got[1].Value != "p@ss $word" {
		t.Fatalf("%v %+v", err, got)
	}
	for _, c := range r.History() {
		if strings.Contains(c, "p@ss") {
			t.Error("secret on the command line")
		}
	}
	for _, bad := range [][]EnvVar{{{"1BAD", "x"}}, {{"PORT", "80"}}, {{"A", "x\nB=y"}}, {{"A", "1"}, {"A", "2"}}} {
		if err := m.SetEnv(ctx, "envy.ng", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestSecurityScanAndUsageParsing(t *testing.T) {
	m, _, _ := newTestManager(t)
	mustAccount(t, m, "sec")
	if _, err := m.Create(ctx, CreateRequest{Domain: "sec.ng", Account: "sec", Version: "7.4"}); err != nil {
		t.Fatal(err)
	}
	rep := m.SecurityScan(ctx)
	if rep.Score <= 0 || rep.Score > 100 || len(rep.Checks) == 0 {
		t.Fatalf("%+v", rep)
	}
	var ids []string
	for _, c := range rep.Checks {
		ids = append(ids, c.ID)
	}
	has(t, "checks", strings.Join(ids, ","), "php-eol", "https")

	dir := t.TempDir()
	log := filepath.Join(dir, "access.log")
	_ = os.WriteFile(log, []byte(`1.2.3.4 - - [13/Oct/2026:10:00:00 +0100] "GET / HTTP/2.0" 200 1000 "-" "x"
1.2.3.4 - - [13/Oct/2026:10:00:01 +0100] "GET /a HTTP/2.0" 404 500 "-" "x"
`), 0o644)
	sum, off := readNewBytes(log, 0)
	if sum != 1500 || off == 0 {
		t.Errorf("sum=%v off=%d", sum, off)
	}
	sum, _ = readNewBytes(log, off)
	if sum != 0 {
		t.Error("re-counted old lines")
	}
	ticks, _, procs := procTotals("/proc")
	_ = ticks
	_ = procs
	m.SampleUsage(ctx, time.Now())
	if u := m.Usage(); len(u) != 1 || u[0].Account != "sec" {
		t.Errorf("usage %+v", u)
	}
}

func TestInspectCpanelBackup(t *testing.T) {
	m, _, root := newTestManager(t, func(c *config.Config) {})
	dir := filepath.Join(root, "var/lib/ike/imports")
	_ = os.MkdirAll(dir, 0o755)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string) {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	add("cpmove-ada_shop/userdata/main", `---
addon_domains:
  adashop.ng: adashop.ada.com
cp_php_magic_include_path.conf: 0
main_domain: ada.com
parked_domains:
  - ada.ng
sub_domains:
  - adashop.ada.com
  - blog.ada.com
`)
	add("cpmove-ada_shop/userdata/ada.com", "documentroot: /home/ada/public_html\nphpversion: ea-php83\n")
	add("cpmove-ada_shop/userdata/adashop.ng", "phpversion: ea-php71\n")
	add("cpmove-ada_shop/mysql/ada_wp.sql", "-- dump")
	add("cpmove-ada_shop/mysql/ada_shop.create", "CREATE DATABASE")
	add("cpmove-ada_shop/homedir/etc/ada.com/passwd", "info:x:1:1::/home/ada/mail/ada.com/info:/bin/false\nsales:x:1:1::/x:/bin/false\n")
	add("cpmove-ada_shop/cron/ada_shop", "MAILTO=\"\"\n*/5 * * * * php wp-cron.php\n")
	add("cpmove-ada_shop/homedir/public_html/index.php", strings.Repeat("x", 2048))
	_ = tw.Close()
	_ = gz.Close()
	_ = os.WriteFile(filepath.Join(dir, "cpmove-ada_shop.tar.gz"), buf.Bytes(), 0o600)

	p, err := m.InspectCpanelBackup("cpmove-ada_shop.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if p.CpanelUser != "ada_shop" || p.Account != "adashop" || p.MainDomain != "ada.com" || p.CronJobs != 1 {
		t.Fatalf("%+v", p)
	}
	var doms []string
	for _, s := range p.Websites {
		doms = append(doms, s.Domain+":"+s.Kind+":"+s.PHP)
	}
	got := strings.Join(doms, ",")
	if got != "ada.com:main:8.3,adashop.ng:addon:7.1,blog.ada.com:subdomain:,ada.ng:alias:" {
		t.Errorf("websites: %s", got)
	}
	if strings.Join(p.Databases, ",") != "ada_shop,ada_wp" || len(p.Mailboxes) != 2 {
		t.Errorf("dbs %v mail %v", p.Databases, p.Mailboxes)
	}
	if !strings.Contains(strings.Join(p.Warnings, " "), "PHP 7.1") {
		t.Errorf("warnings %v", p.Warnings)
	}
	if _, err := m.InspectCpanelBackup("../../etc/passwd"); err == nil {
		t.Error("path traversal accepted")
	}
}
