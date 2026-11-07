package sites

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// SSLSettings is a website's HTTPS setup.
type SSLSettings struct {
	Provider   string    `json:"provider"` // letsencrypt | custom
	Names      []string  `json:"names"`    // host names the certificate covers
	Issuer     string    `json:"issuer,omitempty"`
	IssuedAt   time.Time `json:"issued_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	ForceHTTPS bool      `json:"force_https"`
	HSTS       bool      `json:"hsts"`
	HTTP2      bool      `json:"http2"`
	HTTP3      bool      `json:"http3"`
}

// WebFeatures are what the installed Nginx can do.
type WebFeatures struct {
	NginxVersion string `json:"nginx_version"`
	H2Directive  bool   `json:"h2_directive"` // nginx 1.25.1+: "http2 on;" instead of "listen ... http2"
	HTTP3        bool   `json:"http3"`        // built with the QUIC module
}

var nginxVersionRe = regexp.MustCompile(`nginx/(\d+)\.(\d+)\.(\d+)`)

// DetectWebFeatures asks Nginx which HTTP versions it supports.
func (m *Manager) DetectWebFeatures(ctx context.Context) WebFeatures {
	out, _ := m.run.Run(ctx, "nginx", "-V")
	m.web = parseNginxV(string(out))
	return m.web
}

// SetWebFeatures overrides detection (tests, or servers without Nginx).
func (m *Manager) SetWebFeatures(f WebFeatures) { m.web = f }

// Features returns what was detected.
func (m *Manager) Features() WebFeatures { return m.web }

func parseNginxV(out string) WebFeatures {
	f := WebFeatures{}
	if v := nginxVersionRe.FindStringSubmatch(out); v != nil {
		f.NginxVersion = v[1] + "." + v[2] + "." + v[3]
		a, _ := strconv.Atoi(v[1])
		b, _ := strconv.Atoi(v[2])
		c, _ := strconv.Atoi(v[3])
		f.H2Directive = a > 1 || (a == 1 && (b > 25 || (b == 25 && c >= 1)))
	}
	f.HTTP3 = strings.Contains(out, "--with-http_v3_module")
	return f
}

func (m *Manager) certPaths(s *Site) (cert, key string) {
	if s.SSL != nil && s.SSL.Provider == "custom" {
		d := filepath.Join(m.cfg.SSLDir, s.Domain)
		return filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
	}
	d := filepath.Join(m.cfg.LetsEncryptDir, "live", s.Domain)
	return filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
}

// applySSLLayout turns HTTPS on in the templates when the certificate exists.
func (m *Manager) applySSLLayout(s *Site, L *layout) {
	if s.SSL == nil {
		return
	}
	cert, key := m.certPaths(s)
	if _, err := os.Stat(m.p(cert)); err != nil {
		return // never point a web server at a missing file: it would refuse to start
	}
	L.TLS, L.CertFile, L.KeyFile = true, cert, key
	L.ForceHTTPS, L.HSTS = s.SSL.ForceHTTPS, s.SSL.HSTS && s.SSL.ForceHTTPS
	L.HTTP2 = s.SSL.HTTP2
	L.H2Directive = m.web.H2Directive
	// Caddy always offers HTTP/3 with HTTPS; OpenLiteSpeed has QUIC built in.
	L.HTTP3 = s.SSL.HTTP3 && ((m.web.HTTP3 && L.usesNginx()) || L.usesOLS())
	L.ApacheTLS = L.WebServer == "apache"
}

// IssueRequest asks Let's Encrypt for a certificate.
type IssueRequest struct {
	Email          string `json:"email,omitempty"`
	IncludeAliases bool   `json:"include_aliases,omitempty"`
	// Wildcard adds *.domain. It needs the domain's DNS zone in Ike.
	Wildcard bool `json:"wildcard,omitempty"`
}

// DNSValidation reports whether Ike can publish DNS-01 challenges for a name
// (its zone is managed here). Set by the API server when DNS is available.
var DNSValidation func(name string) bool

// DNSHookCommand is what certbot runs to publish and remove challenges.
var DNSHookCommand = "/usr/local/bin/ike-agent dns-hook"

var emailRe = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)

// IssueCertificate gets (or renews and expands) a Let's Encrypt certificate
// with the HTTP-01 challenge, then turns HTTPS on. It runs as a job.
func (m *Manager) IssueCertificate(ctx context.Context, domain string, req IssueRequest, log func(string, ...any)) (*Site, error) {
	if req.Email == "" {
		req.Email = m.cfg.AdminEmail
	}
	if req.Email != "" && !emailRe.MatchString(req.Email) {
		return nil, fmt.Errorf("%q isn't a valid email address", req.Email)
	}
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if statusOf(site.Status) == StatusSuspended {
		return nil, fmt.Errorf("%s is suspended; reactivate it first", domain)
	}
	names := []string{domain}
	if wwwMode(site.WWW) != "none" {
		names = append(names, "www."+domain)
	}
	canDNS := func(n string) bool { return DNSValidation != nil && DNSValidation(n) }
	useDNS := false
	if req.Wildcard {
		if !canDNS("*." + domain) {
			return nil, fmt.Errorf("a wildcard certificate needs %s's DNS zone to be managed here; add it under DNS first", domain)
		}
		names = append(names, "*."+domain)
		useDNS = true
	}
	if req.IncludeAliases {
		for _, a := range site.Aliases {
			if strings.HasPrefix(a, "*.") {
				if !canDNS(a) {
					log("Skipping %s: wildcard certificates need the domain's DNS zone to be managed here.", a)
					continue
				}
				useDNS = true
			}
			names = append(names, a)
		}
	}
	if useDNS {
		// One certbot run uses one challenge type: every name must be in a zone here.
		var keep []string
		for _, n := range names {
			if canDNS(n) {
				keep = append(keep, n)
			} else {
				log("Skipping %s: its DNS zone isn't managed here, and this certificate is validated through DNS.", n)
			}
		}
		names = keep
		log("Validating through DNS (DNS-01): Ike adds and removes the _acme-challenge records itself.")
	}
	// Names without DNS records would make the whole request fail.
	var ok []string
	for _, n := range names {
		if m.cfg.DryRun || useDNS {
			ok = append(ok, n)
			continue
		}
		if addrs, err := net.DefaultResolver.LookupHost(ctx, n); err != nil || len(addrs) == 0 {
			if n == domain {
				return nil, fmt.Errorf("%s has no DNS record yet. Point it at this server, wait for DNS to update, then try again", n)
			}
			log("Skipping %s: it has no DNS record yet.", n)
			continue
		} else {
			log("%s points to %s", n, strings.Join(addrs, ", "))
		}
		ok = append(ok, n)
	}
	if err := os.MkdirAll(m.p(filepath.Join(m.cfg.AcmeDir, ".well-known", "acme-challenge")), 0o755); err != nil {
		return nil, err
	}
	reload := m.webReloadCommand()
	args := []string{"certonly", "--webroot", "-w", m.cfg.AcmeDir, "--cert-name", domain,
		"--non-interactive", "--agree-tos", "--keep-until-expiring", "--expand", "--deploy-hook", reload}
	if useDNS {
		args = []string{"certonly", "--manual", "--preferred-challenges", "dns",
			"--manual-auth-hook", DNSHookCommand + " auth", "--manual-cleanup-hook", DNSHookCommand + " cleanup",
			"--cert-name", domain, "--non-interactive", "--agree-tos", "--keep-until-expiring", "--expand", "--deploy-hook", reload}
	}
	if req.Email != "" {
		args = append(args, "-m", req.Email)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	for _, n := range ok {
		args = append(args, "-d", n)
	}
	log("Asking Let's Encrypt for a certificate for %s", strings.Join(ok, ", "))
	if _, err := m.run.Exec(ctx, runner.Command{Name: "certbot", Args: args, Output: logWriter(log), Timeout: 5 * time.Minute}); err != nil {
		if useDNS {
			return nil, fmt.Errorf("Let's Encrypt didn't issue the certificate. Check that the domain's name servers at the registrar are the ones shown under DNS: %w", err)
		}
		return nil, fmt.Errorf("Let's Encrypt didn't issue the certificate. Check that every name points to this server and port 80 is open: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err = m.Get(domain)
	if err != nil {
		return nil, err
	}
	old := *site
	ssl := SSLSettings{Provider: "letsencrypt", HTTP2: true, ForceHTTPS: true}
	if site.SSL != nil {
		ssl.ForceHTTPS, ssl.HSTS, ssl.HTTP2, ssl.HTTP3 = site.SSL.ForceHTTPS, site.SSL.HSTS, site.SSL.HTTP2, site.SSL.HTTP3
	}
	site.SSL = &ssl
	cert, _ := m.certPaths(site)
	if info, err := readCert(m.p(cert)); err == nil {
		ssl.Names, ssl.Issuer, ssl.IssuedAt, ssl.ExpiresAt = info.DNSNames, info.Issuer.CommonName, info.NotBefore, info.NotAfter
	} else if !m.cfg.DryRun {
		return nil, fmt.Errorf("certbot finished but the certificate can't be read: %w", err)
	} else {
		ssl.Names = ok
	}
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	log("HTTPS is on for %s", domain)
	return site, nil
}

// CustomCertRequest uploads a certificate bought elsewhere.
type CustomCertRequest struct {
	Certificate string `json:"certificate"` // PEM, leaf first, then the chain
	PrivateKey  string `json:"private_key"` // PEM
}

// SetCustomCertificate installs an uploaded certificate after checking it
// matches the key, covers the domain and hasn't expired.
func (m *Manager) SetCustomCertificate(ctx context.Context, domain string, req CustomCertRequest) (*Site, error) {
	if len(req.Certificate) > 64<<10 || len(req.PrivateKey) > 16<<10 {
		return nil, fmt.Errorf("certificate or key is too large")
	}
	pair, err := tls.X509KeyPair([]byte(req.Certificate), []byte(req.PrivateKey))
	if err != nil {
		return nil, fmt.Errorf("the certificate and private key don't match or aren't valid PEM: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("can't read the certificate: %w", err)
	}
	if err := leaf.VerifyHostname(domain); err != nil {
		return nil, fmt.Errorf("the certificate doesn't cover %s (it covers %s)", domain, strings.Join(leaf.DNSNames, ", "))
	}
	if time.Now().After(leaf.NotAfter) {
		return nil, fmt.Errorf("the certificate expired on %s", leaf.NotAfter.Format("2 Jan 2006"))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	old := *site
	dir := filepath.Join(m.cfg.SSLDir, domain)
	if err := os.MkdirAll(m.p(dir), 0o700); err != nil {
		return nil, err
	}
	if err := writeFile(m.p(filepath.Join(dir, "fullchain.pem")), []byte(strings.TrimSpace(req.Certificate)+"\n"), 0o644); err != nil {
		return nil, err
	}
	if err := writeFile(m.p(filepath.Join(dir, "privkey.pem")), []byte(strings.TrimSpace(req.PrivateKey)+"\n"), 0o600); err != nil {
		return nil, err
	}
	ssl := SSLSettings{Provider: "custom", HTTP2: true, ForceHTTPS: true}
	if site.SSL != nil {
		ssl.ForceHTTPS, ssl.HSTS, ssl.HTTP2, ssl.HTTP3 = site.SSL.ForceHTTPS, site.SSL.HSTS, site.SSL.HTTP2, site.SSL.HTTP3
	}
	ssl.Names, ssl.Issuer, ssl.IssuedAt, ssl.ExpiresAt = leaf.DNSNames, leaf.Issuer.CommonName, leaf.NotBefore, leaf.NotAfter
	site.SSL = &ssl
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	return site, nil
}

// SSLOptions toggles HTTPS behaviour. Nil fields stay as they are.
type SSLOptions struct {
	ForceHTTPS *bool `json:"force_https,omitempty"`
	HSTS       *bool `json:"hsts,omitempty"`
	HTTP2      *bool `json:"http2,omitempty"`
	HTTP3      *bool `json:"http3,omitempty"`
}

// UpdateSSL changes HTTPS options for a website that has a certificate.
func (m *Manager) UpdateSSL(ctx context.Context, domain string, o SSLOptions) (*Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.SSL == nil {
		return nil, fmt.Errorf("%s has no certificate yet", domain)
	}
	old := *site
	ssl := *site.SSL
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&ssl.ForceHTTPS, o.ForceHTTPS)
	set(&ssl.HSTS, o.HSTS)
	set(&ssl.HTTP2, o.HTTP2)
	set(&ssl.HTTP3, o.HTTP3)
	if ssl.HSTS && !ssl.ForceHTTPS {
		return nil, fmt.Errorf("HSTS needs Force HTTPS: browsers would otherwise refuse the plain HTTP version")
	}
	if ssl.HTTP3 && !m.web.HTTP3 && m.cfg.WebStack != "caddy" && m.cfg.WebStack != "openlitespeed" {
		return nil, fmt.Errorf("this server's Nginx wasn't built with HTTP/3 (QUIC). Install Nginx 1.25+ from nginx.org to use it")
	}
	if ssl.HTTP3 && m.cfg.WebStack == "apache" {
		return nil, fmt.Errorf("HTTP/3 needs Nginx; this server serves websites with Apache alone")
	}
	site.SSL = &ssl
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	return site, nil
}

// RemoveSSL turns HTTPS off and deletes the certificate.
func (m *Manager) RemoveSSL(ctx context.Context, domain string) (*Site, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	if site.SSL == nil {
		return site, nil
	}
	old := *site
	site.SSL = nil
	if err := m.applyWeb(ctx, site, &old); err != nil {
		return nil, err
	}
	m.deleteCertificate(ctx, &old)
	return site, nil
}

func (m *Manager) deleteCertificate(ctx context.Context, s *Site) {
	if s.SSL == nil {
		return
	}
	if s.SSL.Provider == "custom" {
		_ = os.RemoveAll(m.p(filepath.Join(m.cfg.SSLDir, s.Domain)))
		return
	}
	_, _ = m.run.Run(ctx, "certbot", "delete", "--cert-name", s.Domain, "--non-interactive")
}

// CertInfo summarizes one website's certificate for expiry monitoring.
type CertInfo struct {
	Domain    string    `json:"domain"`
	Provider  string    `json:"provider"`
	Names     []string  `json:"names"`
	Issuer    string    `json:"issuer"`
	ExpiresAt time.Time `json:"expires_at"`
	DaysLeft  int       `json:"days_left"`
	Problem   string    `json:"problem,omitempty"`
}

// Certificates lists every website certificate with days left. Let's Encrypt
// certificates renew 30 days before expiry, so fewer days left means renewal
// is failing.
func (m *Manager) Certificates() []CertInfo {
	sites, _ := m.List()
	out := []CertInfo{}
	for i := range sites {
		s := &sites[i]
		if s.SSL == nil {
			continue
		}
		ci := CertInfo{Domain: s.Domain, Provider: s.SSL.Provider, Names: s.SSL.Names, Issuer: s.SSL.Issuer, ExpiresAt: s.SSL.ExpiresAt}
		cert, _ := m.certPaths(s)
		if c, err := readCert(m.p(cert)); err == nil {
			ci.ExpiresAt, ci.Names, ci.Issuer = c.NotAfter, c.DNSNames, c.Issuer.CommonName
		} else if !m.cfg.DryRun {
			ci.Problem = "The certificate file is missing, so HTTPS is off."
		}
		ci.DaysLeft = int(time.Until(ci.ExpiresAt).Hours() / 24)
		switch {
		case ci.Problem != "":
		case ci.DaysLeft < 0:
			ci.Problem = "Expired. Visitors see a security warning."
		case s.SSL.Provider == "letsencrypt" && ci.DaysLeft < 20:
			ci.Problem = "Automatic renewal seems to be failing. Check that the domain still points here."
		case s.SSL.Provider == "custom" && ci.DaysLeft < 21:
			ci.Problem = "Expires soon. Upload a renewed certificate."
		}
		out = append(out, ci)
	}
	return out
}

func readCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("no PEM certificate found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// logWriter adapts a job logger to an io.Writer, one call per line.
func logWriter(log func(string, ...any)) io.Writer { return &lineLog{log: log} }

type lineLog struct {
	log func(string, ...any)
	buf []byte
}

func (l *lineLog) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := strings.IndexByte(string(l.buf), '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimRight(string(l.buf[:i]), "\r"); line != "" {
			l.log("%s", line)
		}
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 4096 { // a very long line without newlines
		l.log("%s", string(l.buf))
		l.buf = nil
	}
	return len(p), nil
}
