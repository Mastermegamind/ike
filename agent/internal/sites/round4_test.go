package sites

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
)

func selfSigned(t *testing.T, names ...string) (certPEM, keyPEM string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(90 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func TestDomainsAliasesAndWWW(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "naija")
	if _, err := m.Create(ctx, CreateRequest{Domain: "naija.ng", Account: "naija"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "other.ng", Account: "naija"}); err != nil {
		t.Fatal(err)
	}
	s, err := m.SetDomains(ctx, "naija.ng", DomainsRequest{Aliases: []string{"naija.com", "*.naija.ng", "shop.naija.ng"}, WWW: "root"})
	if err != nil {
		t.Fatal(err)
	}
	vh := read(t, root, "etc/nginx/ike/sites/naija.ng.conf")
	has(t, "vhost", vh, "server_name naija.ng naija.com *.naija.ng shop.naija.ng;",
		"server_name www.naija.ng;", "return 301 $scheme://naija.ng$request_uri;")
	if len(s.Aliases) != 3 {
		t.Fatalf("aliases %v", s.Aliases)
	}
	// Aliases redirect to the main domain.
	if _, err := m.SetDomains(ctx, "naija.ng", DomainsRequest{Aliases: []string{"naija.com"}, WWW: "www", RedirectAliases: true}); err != nil {
		t.Fatal(err)
	}
	vh = read(t, root, "etc/nginx/ike/sites/naija.ng.conf")
	has(t, "vhost", vh, "server_name naija.ng naija.com;", "server_name www.naija.ng;", "return 301 $scheme://www.naija.ng$request_uri;")

	for _, bad := range []DomainsRequest{
		{Aliases: []string{"other.ng"}},            // another website's domain
		{Aliases: []string{"www.other.ng"}},        // another website's www
		{Aliases: []string{"naija.ng"}},            // itself
		{Aliases: []string{"*.com"}},               // too broad
		{Aliases: []string{"a.*.ng"}},              // wildcard in the middle
		{Aliases: []string{"x.ng", "x.ng"}},        // twice
		{Aliases: []string{"bad domain.ng"}},       // space
		{Aliases: []string{"evil.ng;include /"}},   // config injection
		{Aliases: []string{"ok.ng"}, WWW: "maybe"}, // unknown www mode
	} {
		if _, err := m.SetDomains(ctx, "naija.ng", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	// A new website can't take a name that's already an alias.
	if _, err := m.Create(ctx, CreateRequest{Domain: "naija.com", Account: "naija"}); err == nil {
		t.Error("created a website for an existing alias")
	}
}

func TestRedirectsAndRewrites(t *testing.T) {
	m, _, root := newTestManager(t, func(c *config.Config) { c.WebStack = "hybrid"; c.DefaultWebServer = "nginx" })
	mustAccount(t, m, "redir")
	if _, err := m.Create(ctx, CreateRequest{Domain: "redir.ng", Account: "redir"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "ap.redir.ng", Account: "redir", WebServer: "nginx+apache"}); err != nil {
		t.Fatal(err)
	}
	rules := []Redirect{
		{From: "/old-page", To: "/new-page"},
		{Match: "prefix", From: "/blog", To: "https://blog.redir.ng", Code: 308, KeepQuery: true},
		{Match: "regex", From: `^/p/([0-9]+)$`, To: "/product?id=$1", Code: 302},
		{Kind: "rewrite", Match: "prefix", From: "/api", To: "/index.php", KeepQuery: true},
		{From: "/promo%20page", To: "https://example.com/a%20b"},
	}
	s, err := m.SetRedirects(ctx, "redir.ng", rules)
	if err != nil {
		t.Fatal(err)
	}
	if s.Redirects[0].ID == "" || s.Redirects[0].Code != 301 {
		t.Errorf("defaults not filled: %+v", s.Redirects[0])
	}
	vh := read(t, root, "etc/nginx/ike/sites/redir.ng.conf")
	has(t, "nginx", vh,
		"location = /old-page { return 301 /new-page; }",
		`location ~ ^/blog(/.*)?$ { return 308 https://blog.redir.ng$1$is_args$args; }`,
		`location ~ ^/p/([0-9]+)$ { return 302 /product?id=$1; }`,
		`rewrite ^/api(/.*)?$ /index.php$1 last;`,
	)
	// Redirects come before the dotfile block so they win over regex locations.
	if strings.Index(vh, "location = /old-page") > strings.Index(vh, `location ~ /\.`) {
		t.Error("redirects must be listed before other locations")
	}

	// Apache-served website: rules land in Nginx (in front), not Apache.
	if _, err := m.SetRedirects(ctx, "ap.redir.ng", rules[:1]); err != nil {
		t.Fatal(err)
	}
	if ap := read(t, root, "etc/apache2/ike/sites/ap.redir.ng.conf"); strings.Contains(ap, "RewriteRule ^/old-page") {
		t.Error("hybrid Apache got the redirect too")
	}

	for _, bad := range [][]Redirect{
		{{From: "old", To: "/x"}},                                   // not a path
		{{From: "/a b", To: "/x"}},                                  // space
		{{From: "/a", To: "/x; return 200"}},                        // injection
		{{From: "/a", To: "/x\nroot /"}},                            // newline
		{{From: "/a", To: "/{x}"}},                                  // braces
		{{From: "/a", To: "/x", Code: 200}},                         // not a redirect code
		{{Match: "regex", From: "/no-anchor", To: "/x"}},            // regex must start with ^
		{{Match: "regex", From: "^/(", To: "/x"}},                   // invalid regex
		{{From: "/a", To: "/$1"}},                                   // $1 without regex
		{{Match: "regex", From: "^/a$", To: "/$host"}},              // only $1-$9
		{{Kind: "rewrite", From: "/a", To: "https://elsewhere.ng"}}, // rewrite must stay on site
		{{From: "/a", To: "/x?y=1", KeepQuery: true}},               // conflicting query
		{{From: "/a", To: "/x"}, {From: "/a", To: "/y"}},            // duplicate
		{{Match: "prefix", From: "/", To: "https://x.ng"}},          // whole site
		{{From: "/a", To: "javascript:alert(1)"}},                   // not a URL
	} {
		if _, err := m.SetRedirects(ctx, "redir.ng", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestApacheOnlyRedirectsAndHTTPS(t *testing.T) {
	m, _, root := newTestManager(t, func(c *config.Config) { c.WebStack = "apache"; c.ApacheListen = "*:80" })
	mustAccount(t, m, "apsite")
	if _, err := m.Create(ctx, CreateRequest{Domain: "apsite.ng", Account: "apsite"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetRedirects(ctx, "apsite.ng", []Redirect{{From: "/old", To: "/new"}, {Match: "prefix", From: "/files", To: "/docs"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDomains(ctx, "apsite.ng", DomainsRequest{WWW: "root"}); err != nil {
		t.Fatal(err)
	}
	cert, key := selfSigned(t, "apsite.ng", "www.apsite.ng")
	if _, err := m.SetCustomCertificate(ctx, "apsite.ng", CustomCertRequest{Certificate: cert, PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	ap := read(t, root, "etc/apache2/ike/sites/apsite.ng.conf")
	has(t, "apache", ap,
		`RewriteRule ^/old$ /new [R=301,L,QSD]`,
		`RewriteRule ^/files(/.*)?$ /docs$1 [R=301,L,QSD]`,
		"<VirtualHost *:443>", "SSLEngine on", "SSLCertificateFile /etc/ike/ssl/apsite.ng/fullchain.pem",
		"Protocols h2 http/1.1", "RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=301,L]",
		"ServerName www.apsite.ng", "RewriteRule ^ https://apsite.ng%{REQUEST_URI} [R=301,L]")
	if strings.Count(ap, "<VirtualHost") != 4 { // redirect :80 and :443, main :80 and :443
		t.Errorf("want 4 vhosts, got %d\n%s", strings.Count(ap, "<VirtualHost"), ap)
	}
}

func TestCustomCertificateHTTP2AndHTTP3(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "secure")
	if _, err := m.Create(ctx, CreateRequest{Domain: "secure.ng", Account: "secure"}); err != nil {
		t.Fatal(err)
	}
	cert, key := selfSigned(t, "secure.ng", "www.secure.ng")
	other, otherKey := selfSigned(t, "elsewhere.ng")
	if _, err := m.SetCustomCertificate(ctx, "secure.ng", CustomCertRequest{Certificate: other, PrivateKey: otherKey}); err == nil {
		t.Error("accepted a certificate for another domain")
	}
	if _, err := m.SetCustomCertificate(ctx, "secure.ng", CustomCertRequest{Certificate: cert, PrivateKey: otherKey}); err == nil {
		t.Error("accepted a mismatched key")
	}

	// Older Nginx (1.24): http2 is a listen parameter, no HTTP/3.
	m.SetWebFeatures(parseNginxV("nginx version: nginx/1.24.0\nbuilt with OpenSSL"))
	s, err := m.SetCustomCertificate(ctx, "secure.ng", CustomCertRequest{Certificate: cert, PrivateKey: key})
	if err != nil {
		t.Fatal(err)
	}
	if s.SSL.Provider != "custom" || !s.SSL.ForceHTTPS || len(s.SSL.Names) != 2 {
		t.Errorf("ssl %+v", s.SSL)
	}
	vh := read(t, root, "etc/nginx/ike/sites/secure.ng.conf")
	has(t, "nginx", vh, "listen 443 ssl http2;", "ssl_certificate     /etc/ike/ssl/secure.ng/fullchain.pem",
		"return 301 https://$host$request_uri;")
	if strings.Contains(vh, "quic") {
		t.Error("HTTP/3 rendered on an Nginx without QUIC")
	}
	if _, err := m.UpdateSSL(ctx, "secure.ng", SSLOptions{HTTP3: ptr(true)}); err == nil {
		t.Error("HTTP/3 allowed without QUIC support")
	}
	if _, err := m.UpdateSSL(ctx, "secure.ng", SSLOptions{ForceHTTPS: ptr(false), HSTS: ptr(true)}); err == nil {
		t.Error("HSTS allowed without forced HTTPS")
	}

	// Newer Nginx with QUIC.
	m.SetWebFeatures(parseNginxV("nginx version: nginx/1.27.3\nconfigure arguments: --with-http_v2_module --with-http_v3_module"))
	if _, err := m.UpdateSSL(ctx, "secure.ng", SSLOptions{HTTP3: ptr(true), HSTS: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	vh = read(t, root, "etc/nginx/ike/sites/secure.ng.conf")
	has(t, "nginx h3", vh, "listen 443 ssl;", "http2 on;", "listen 443 quic;", `Alt-Svc 'h3=":443"; ma=86400'`,
		`Strict-Transport-Security "max-age=31536000"`)
	if strings.Count(vh, "fastcgi_pass") != 1 {
		t.Error("with forced HTTPS only the 443 server should serve PHP")
	}
	certs := m.Certificates()
	if len(certs) != 1 || certs[0].DaysLeft < 80 || certs[0].Problem != "" {
		t.Errorf("certs %+v", certs)
	}

	// Removing HTTPS deletes the uploaded files and goes back to port 80 only.
	if _, err := m.RemoveSSL(ctx, "secure.ng"); err != nil {
		t.Fatal(err)
	}
	if vh = read(t, root, "etc/nginx/ike/sites/secure.ng.conf"); strings.Contains(vh, "443") {
		t.Error("443 still configured")
	}
	_ = r
}

func TestLetsEncryptIssue(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "lets")
	if _, err := m.Create(ctx, CreateRequest{Domain: "lets.ng", Account: "lets"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetDomains(ctx, "lets.ng", DomainsRequest{Aliases: []string{"lets.com", "*.lets.ng"}}); err != nil {
		t.Fatal(err)
	}
	var lines []string
	log := func(f string, a ...any) { lines = append(lines, f) }
	if _, err := m.IssueCertificate(ctx, "lets.ng", IssueRequest{Email: "not-an-email"}, log); err == nil {
		t.Error("accepted a bad email")
	}
	s, err := m.IssueCertificate(ctx, "lets.ng", IssueRequest{Email: "andre@megamind.ng", IncludeAliases: true}, log)
	if err != nil {
		t.Fatal(err)
	}
	has(t, "certbot", strings.Join(r.History(), "\n"),
		"certbot certonly --webroot -w /var/lib/ike/acme --cert-name lets.ng --non-interactive --agree-tos --keep-until-expiring --expand --deploy-hook systemctl reload nginx -m andre@megamind.ng -d lets.ng -d www.lets.ng -d lets.com")
	if strings.Contains(strings.Join(r.History(), "\n"), "-d *.lets.ng") {
		t.Error("wildcard sent to HTTP-01")
	}
	if s.SSL == nil || s.SSL.Provider != "letsencrypt" {
		t.Fatalf("ssl %+v", s.SSL)
	}
	// No certificate file (dry run): the vhost must not reference it.
	if vh := read(t, root, "etc/nginx/ike/sites/lets.ng.conf"); strings.Contains(vh, "ssl_certificate") {
		t.Error("vhost points at a missing certificate")
	}
	has(t, "acme", read(t, root, "etc/nginx/ike/sites/lets.ng.conf"), "root /var/lib/ike/acme;")
}

func TestPHPSettings(t *testing.T) {
	m, _, root := newTestManager(t)
	mustAccount(t, m, "phpx")
	if _, err := m.Create(ctx, CreateRequest{Domain: "phpx.ng", Account: "phpx"}); err != nil {
		t.Fatal(err)
	}
	pool := "etc/php/8.3/fpm/pool.d/ike-phpx-" + siteID("phpx.ng") + ".conf"
	has(t, "default pool", read(t, root, pool), "pm = ondemand", "pm.max_children = 5", "disable_functions",
		"memory_limit] = 256M", "max_execution_time] = 60")

	if _, err := m.SetPHPSettings(ctx, "phpx.ng", PHPSettings{PM: "dynamic", MaxChildren: 10, MemoryLimitMB: 512,
		UploadMaxMB: 200, PostMaxMB: 210, MaxExecutionTime: 120, DisplayErrors: true, AllowShellFunctions: true, Timezone: "Africa/Lagos"}); err != nil {
		t.Fatal(err)
	}
	p := read(t, root, pool)
	has(t, "pool", p, "pm = dynamic", "pm.max_children = 10", "pm.start_servers = 1", "pm.max_spare_servers = 5",
		"upload_max_filesize] = 200M", "post_max_size] = 210M", "memory_limit] = 512M", "display_errors] = on",
		"date.timezone] = Africa/Lagos", "max_execution_time] = 120")
	if strings.Contains(p, "disable_functions") {
		t.Error("shell functions still disabled")
	}
	has(t, "nginx", read(t, root, "etc/nginx/ike/sites/phpx.ng.conf"), "client_max_body_size 211m;", "fastcgi_read_timeout 300;")

	// Account limits cap what a website can ask for.
	if _, err := m.SetLimits(ctx, "phpx", Limits{PHPWorkers: 4, MemoryMB: 1024}); err != nil {
		t.Fatal(err)
	}
	has(t, "capped", read(t, root, pool), "pm.max_children = 4")
	for _, bad := range []PHPSettings{
		{MaxChildren: 8},                     // over the account's 4
		{MemoryLimitMB: 2048},                // over the account's memory
		{PM: "turbo"},                        // unknown
		{UploadMaxMB: 100, PostMaxMB: 50},    // post smaller than upload
		{Timezone: "Mars/Olympus"},           // unknown zone
		{Timezone: "UTC; disable_functions"}, // injection
		{PM: "dynamic", MaxChildren: 4, StartServers: 4, MinSpareServers: 3, MaxSpareServers: 2}, // inconsistent
	} {
		if _, err := m.SetPHPSettings(ctx, "phpx.ng", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestNewAppRuntimesAndProxy(t *testing.T) {
	m, r, root := newTestManager(t)
	addRuntime(t, root, "java", "21", "java")
	addRuntime(t, root, "go", "1.24", "go")
	addRuntime(t, root, "dart", "3.9", "dart")
	mustAccount(t, m, "poly")
	for _, c := range []CreateRequest{
		{Domain: "java.poly.ng", Account: "poly", Runtime: "java"},
		{Domain: "go.poly.ng", Account: "poly", Runtime: "go"},
		{Domain: "dart.poly.ng", Account: "poly", Runtime: "dart"},
		{Domain: "box.poly.ng", Account: "poly", Runtime: "proxy", Port: 8088},
	} {
		if _, err := m.Create(ctx, c); err != nil {
			t.Fatalf("%s: %v", c.Domain, err)
		}
	}
	key := func(d string) string { return "poly-" + siteID(d) }
	has(t, "java start", read(t, root, "etc/ike/apps/"+key("java.poly.ng")+".sh"),
		`export JAVA_HOME="/opt/ike/runtimes/java/21"`, "exec java Main.java")
	has(t, "java unit", read(t, root, "etc/systemd/system/ike-app-"+key("java.poly.ng")+".service"), "MemoryMax=2G")
	has(t, "go start", read(t, root, "etc/ike/apps/"+key("go.poly.ng")+".sh"), "export GOTOOLCHAIN=local", "exec ./app")
	has(t, "dart start", read(t, root, "etc/ike/apps/"+key("dart.poly.ng")+".sh"), `export PUB_CACHE="/home/poly/.pub-cache"`)
	has(t, "samples", read(t, root, "home/poly/web/go.poly.ng/app/main.go"), "package main")
	_ = read(t, root, "home/poly/web/dart.poly.ng/app/bin/server.dart")
	has(t, "go build", strings.Join(r.History(), "\n"), "runuser -u poly -- /opt/ike/runtimes/go/1.24/bin/go build -o app .")
	has(t, "proxy", read(t, root, "etc/nginx/ike/sites/box.poly.ng.conf"), "proxy_pass http://127.0.0.1:8088;")

	if _, err := m.Create(ctx, CreateRequest{Domain: "j17.poly.ng", Account: "poly", Runtime: "java", Version: "17"}); err == nil {
		t.Error("Java 17 isn't installed")
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "p.poly.ng", Account: "poly", Runtime: "proxy"}); err == nil {
		t.Error("proxy without a port")
	}
	if _, err := m.Create(ctx, CreateRequest{Domain: "k.poly.ng", Account: "poly", Runtime: "kotlin"}); err == nil {
		t.Error("unknown runtime")
	}
	rt := InstalledRuntimes(root, "/opt/ike/runtimes")
	if len(rt["java"]) != 1 || len(rt["go"]) != 1 || len(rt["dart"]) != 1 {
		t.Errorf("runtimes %v", rt)
	}
}

func ptr[T any](v T) *T { return &v }
