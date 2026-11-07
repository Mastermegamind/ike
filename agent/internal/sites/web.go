package sites

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/YOUR-ORG/ike/agent/internal/mail"
)

// Per-website web server choices.
const (
	WebNginx       = "nginx"
	WebApache      = "apache"
	WebNginxApache = "nginx+apache"
	WebCaddy       = "caddy"
	WebOLS         = "openlitespeed"
)

func (L layout) usesNginx() bool {
	return L.WebServer == WebNginx || L.WebServer == WebNginxApache || L.WebServer == ""
}
func (L layout) usesApache() bool { return L.WebServer == WebApache || L.WebServer == WebNginxApache }
func (L layout) usesCaddy() bool  { return L.WebServer == WebCaddy }
func (L layout) usesOLS() bool    { return L.WebServer == WebOLS }

// webServices are the systemd units serving a website.
func (L layout) webServices() []string {
	switch {
	case L.usesCaddy():
		return []string{"caddy"}
	case L.usesOLS():
		return []string{"lsws"}
	}
	var out []string
	if L.usesNginx() {
		out = append(out, "nginx")
	}
	if L.usesApache() {
		out = append(out, "apache2")
	}
	return out
}

// writeWeb writes and loads every web server config a website uses: Apache
// first (when used) so Nginx never proxies to a missing vhost.
func (m *Manager) writeWeb(ctx context.Context, L layout) error {
	if L.usesApache() {
		if err := m.writeApacheVhost(ctx, L); err != nil {
			return fmt.Errorf("configure Apache: %w", err)
		}
	}
	if L.usesNginx() {
		if err := m.writeNginxVhost(ctx, L); err != nil {
			return fmt.Errorf("configure Nginx: %w", err)
		}
	}
	if L.usesCaddy() {
		if err := m.writeCaddySite(ctx, L); err != nil {
			return fmt.Errorf("configure Caddy: %w", err)
		}
	}
	if L.usesOLS() {
		if err := m.writeOLSVhost(ctx, L); err != nil {
			return fmt.Errorf("configure OpenLiteSpeed: %w", err)
		}
	}
	return nil
}

// removeWeb removes the configs of web servers L uses that next doesn't
// (nil: all of them).
func (m *Manager) removeWeb(ctx context.Context, L layout, next *layout) {
	gone := func(uses func(layout) bool) bool { return uses(L) && (next == nil || !uses(*next)) }
	if gone(layout.usesNginx) {
		m.removeNginxVhost(ctx, L)
	}
	if gone(layout.usesApache) {
		m.removeApacheVhost(ctx, L)
	}
	if gone(layout.usesCaddy) {
		m.removeCaddySite(ctx, L)
	}
	if gone(layout.usesOLS) {
		m.removeOLSVhost(ctx, L)
	}
}

// ---------------------------------------------------------------- Caddy

func (m *Manager) caddyFile(domain string) string {
	return filepath.Join(m.cfg.CaddySitesDir, domain+".caddy")
}

func (m *Manager) caddyReload(ctx context.Context) error {
	if out, err := m.run.Run(ctx, "caddy", "validate", "--adapter", "caddyfile", "--config", m.cfg.CaddyConfig); err != nil {
		return fmt.Errorf("Caddy rejected the configuration: %s", caddyError(string(out), err))
	}
	_, err := m.run.Run(ctx, "systemctl", "reload", "caddy")
	return err
}

// caddyError picks the line that says what's wrong out of caddy's log output.
func caddyError(out string, err error) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "Error:") {
			return strings.TrimSpace(l)
		}
	}
	return firstLine(out, err)
}

func (m *Manager) writeCaddySite(ctx context.Context, L layout) error {
	if L.WAFNginx || L.WAFApache {
		return fmt.Errorf("the web application firewall needs Nginx or Apache")
	}
	path := m.caddyFile(L.Domain)
	if L.TLS {
		if err := m.copyCaddyCert(L); err != nil {
			return err
		}
	}
	old, oldErr := os.ReadFile(m.p(path))
	if err := m.render(caddyTmpl, L, path, 0o640); err != nil {
		return err
	}
	if err := m.chownWebGroup(path); err != nil {
		return err
	}
	if err := m.caddyReload(ctx); err != nil {
		if oldErr == nil {
			_ = writeFile(m.p(path), old, 0o640)
		} else {
			_ = os.Remove(m.p(path))
		}
		return err
	}
	return nil
}

// copyCaddyCert gives Caddy (running as its own user) a readable copy of
// the website's certificate and key: root:<web group>, 0640.
func (m *Manager) copyCaddyCert(L layout) error {
	dir := filepath.Dir(L.CaddyCert)
	if err := os.MkdirAll(m.p(dir), 0o750); err != nil {
		return err
	}
	if err := m.chownWebGroup(dir); err != nil {
		return err
	}
	for src, dst := range map[string]string{L.CertFile: L.CaddyCert, L.KeyFile: L.CaddyKey} {
		b, err := os.ReadFile(m.p(src))
		if err != nil {
			return fmt.Errorf("read the certificate: %w", err)
		}
		if err := writeFile(m.p(dst), b, 0o640); err != nil {
			return err
		}
		if err := m.chownWebGroup(dst); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) removeCaddySite(ctx context.Context, L layout) {
	path := m.caddyFile(L.Domain)
	if _, err := os.Stat(m.p(path)); err != nil {
		return
	}
	_ = os.Remove(m.p(path))
	_ = os.RemoveAll(m.p(m.caddyCertDir(L.Domain)))
	_ = m.caddyReload(ctx)
}

// ---------------------------------------------------------------- password checks for Caddy

var authFileRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{2,252}/[0-9a-f]{8}$`)

// authCache remembers recent good checks so a page with many files doesn't
// run SHA-512 crypt for every request. Keyed by file, user, password hash
// and the stored hash, so a changed or removed password stops matching.
type authCache struct {
	mu   sync.Mutex
	seen map[string]bool
}

func (c *authCache) ok(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[key]
}

func (c *authCache) add(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil || len(c.seen) > 4096 {
		c.seen = map[string]bool{}
	}
	c.seen[key] = true
}

// AuthHandler answers Caddy's forward_auth for password-protected folders:
// GET /check?file=<domain>/<path id>&realm=... with the visitor's
// Authorization header. 2xx lets the request through; 401 asks for a password.
func (m *Manager) AuthHandler() http.Handler {
	cache := &authCache{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		file, realm := q.Get("file"), q.Get("realm")
		if !authFileRe.MatchString(file) || strings.Contains(file, "..") {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !realmRe.MatchString(realm) {
			realm = "Restricted area"
		}
		deny := func() {
			w.Header().Set("WWW-Authenticate", `Basic realm="`+realm+`", charset="UTF-8"`)
			http.Error(w, "401 Unauthorized", http.StatusUnauthorized)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user == "" {
			deny()
			return
		}
		data, err := os.ReadFile(m.p(filepath.Join(htpasswdRoot, file)))
		if err != nil {
			deny()
			return
		}
		for _, line := range strings.Split(string(data), "\n") {
			name, hash, found := strings.Cut(strings.TrimSpace(line), ":")
			if !found || name != user {
				continue
			}
			key := file + "\x00" + user + "\x00" + hash + "\x00" + kitHash(pass)
			if cache.ok(key) || mail.CheckPassword(hash, pass) {
				cache.add(key)
				w.WriteHeader(http.StatusNoContent)
				return
			}
			break
		}
		deny()
	})
}

func kitHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// webReloadCommand reloads the server's web servers after certbot renews a
// certificate (its --deploy-hook).
func (m *Manager) webReloadCommand() string {
	switch m.cfg.WebStack {
	case "apache":
		return "systemctl reload apache2"
	case "caddy":
		return CaddyDeployHook
	case "openlitespeed":
		return m.olsBin("lswsctrl") + " restart"
	}
	return "systemctl reload nginx"
}

func webServerName(stack string) string {
	return map[string]string{"nginx": "Nginx", "apache": "Apache", "hybrid": "Nginx and Apache", "caddy": "Caddy", "openlitespeed": "OpenLiteSpeed"}[stack]
}
