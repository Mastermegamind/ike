package sites

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// TestRealWebServerSyntax renders every kind of website and asks the real
// Nginx and Apache to check the configs. It writes under / so it only runs
// in a throwaway machine or container:
//
//	IKE_REAL_SYNTAX=1 go test ./internal/sites -run RealWebServer
func TestRealWebServerSyntax(t *testing.T) {
	if os.Getenv("IKE_REAL_SYNTAX") != "1" {
		t.Skip("set IKE_REAL_SYNTAX=1 on a throwaway machine with nginx and apache2 installed")
	}
	for _, stack := range []string{"hybrid", "apache"} {
		t.Run(stack, func(t *testing.T) {
			realSyntax(t, stack)
		})
	}
}

func realSyntax(t *testing.T, stack string) {
	_ = os.RemoveAll("/etc/nginx/ike/sites")
	_ = os.RemoveAll("/etc/apache2/ike/sites")
	_ = os.RemoveAll("/var/lib/ike/sites")
	cfg := config.Default()
	cfg.Root, cfg.DryRun = "/", true
	cfg.WebStack, cfg.DefaultWebServer = stack, "nginx"
	cfg.AccountsDir = t.TempDir()
	cfg.HomeBase = t.TempDir()
	cfg.CronDir = t.TempDir()
	if stack == "apache" {
		cfg.ApacheListen = "*:80"
	}
	for _, v := range []string{"8.3"} {
		_ = os.MkdirAll(filepath.Join("/etc/php", v, "fpm/pool.d"), 0o755)
	}
	m, _, _ := newTestManagerAt(t, cfg)
	m.SetWebFeatures(WebFeatures{NginxVersion: "1.24.0"})
	mustAccount(t, m, "syn")
	_ = os.MkdirAll("/opt/ike/runtimes/node/22/bin", 0o755)
	_ = os.WriteFile("/opt/ike/runtimes/node/22/bin/node", nil, 0o755)

	lb := &BalancerSettings{Method: "least_conn", Backends: []Backend{{Address: "10.0.0.5:80", Weight: 3}, {Address: "10.0.0.6:8080"}, {Address: "10.0.0.7:80", Backup: true}}}
	reqs := []CreateRequest{{Domain: "php.syn.ng", Account: "syn"}, {Domain: "static.syn.ng", Account: "syn", Runtime: "static"},
		{Domain: "lb.syn.ng", Account: "syn", Runtime: "balancer", Balancer: lb}}
	if stack == "hybrid" {
		reqs = append(reqs, CreateRequest{Domain: "ap.syn.ng", Account: "syn", WebServer: "nginx+apache"},
			CreateRequest{Domain: "node.syn.ng", Account: "syn", Runtime: "node"},
			CreateRequest{Domain: "box.syn.ng", Account: "syn", Runtime: "proxy", Port: 9000})
	}
	for _, r := range reqs {
		if _, err := m.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	rules := []Redirect{{From: "/old", To: "/new"}, {Match: "prefix", From: "/blog", To: "https://blog.syn.ng", KeepQuery: true, Code: 308},
		{Match: "regex", From: `^/p/([0-9]+)$`, To: "/product?id=$1", Code: 302}, {Kind: "rewrite", Match: "prefix", From: "/api", To: "/index.php"},
		{From: "/a%20b", To: "https://x.ng/c%20d"}}
	if _, err := m.SetRedirects(ctx, "php.syn.ng", rules); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDomains(ctx, "php.syn.ng", DomainsRequest{Aliases: []string{"syn.com", "*.php.syn.ng"}, WWW: "www", RedirectAliases: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetStatus(ctx, "static.syn.ng", SetStatusRequest{Status: "maintenance", AllowIPs: []string{"203.0.113.9"}}); err != nil {
		t.Fatal(err)
	}
	cert, key := selfSigned(t, "php.syn.ng", "www.php.syn.ng", "syn.com")
	if _, err := m.SetCustomCertificate(ctx, "php.syn.ng", CustomCertRequest{Certificate: cert, PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSSL(ctx, "php.syn.ng", SSLOptions{HSTS: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	cert2, key2 := selfSigned(t, "static.syn.ng")
	if _, err := m.SetCustomCertificate(ctx, "static.syn.ng", CustomCertRequest{Certificate: cert2, PrivateKey: key2}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSSL(ctx, "static.syn.ng", SSLOptions{ForceHTTPS: ptr(false)}); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if stack == "hybrid" {
		fp, _ := os.ReadFile("/etc/nginx/fastcgi_params")
		_ = os.WriteFile(filepath.Join(dir, "fastcgi_params"), fp, 0o644)
		conf := `pid ` + dir + `/nginx.pid;
error_log ` + dir + `/error.log;
events {}
http {
    map $http_upgrade $connection_upgrade_ike { default upgrade; '' close; }
    include /etc/nginx/ike/sites/*.conf;
}
`
		_ = os.WriteFile(filepath.Join(dir, "nginx.conf"), []byte(conf), 0o644)
		out, err := exec.Command("nginx", "-t", "-c", filepath.Join(dir, "nginx.conf")).CombinedOutput()
		// Containers without IPv6 can't bind [::]:80; the syntax check has passed by then.
		if err != nil && !(strings.Contains(string(out), "syntax is ok") && strings.Contains(string(out), "Address family not supported")) {
			t.Fatalf("nginx -t: %v\n%s", err, out)
		}
	}
	if _, err := exec.LookPath("apache2"); err == nil {
		mods := []string{"mpm_event", "authz_core", "alias", "rewrite", "headers", "ssl", "http2", "proxy", "proxy_http",
			"proxy_fcgi", "proxy_wstunnel", "setenvif", "dir", "mime", "socache_shmcb", "proxy_balancer", "slotmem_shm", "lbmethod_byrequests", "lbmethod_bybusyness"}
		conf := "ServerRoot /etc/apache2\nPidFile " + dir + "/apache.pid\nMutex file:" + dir + "\nListen 18080\nErrorLog " + dir + "/apache.log\n"
		for _, mod := range mods {
			conf += "LoadModule " + mod + "_module /usr/lib/apache2/modules/mod_" + mod + ".so\n"
		}
		conf += "IncludeOptional /etc/apache2/ike/sites/*.conf\n"
		_ = os.WriteFile(filepath.Join(dir, "apache.conf"), []byte(conf), 0o644)
		if out, err := exec.Command("apache2", "-t", "-f", filepath.Join(dir, "apache.conf")).CombinedOutput(); err != nil {
			t.Fatalf("apache2 -t: %v\n%s", err, out)
		}
	}
}

func newTestManagerAt(t *testing.T, cfg config.Config) (*Manager, any, string) {
	t.Helper()
	m := NewManager(cfg, &runner.DryRun{})
	if err := m.EnsurePages(); err != nil {
		t.Fatal(err)
	}
	return m, nil, cfg.Root
}
