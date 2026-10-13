package sites

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Website status.
const (
	StatusActive      = "active"
	StatusMaintenance = "maintenance"
	StatusSuspended   = "suspended"
)

// ErrorCodes get a branded page. 3xx codes are redirects, not error pages.
var ErrorCodes = []int{400, 401, 403, 404, 405, 408, 410, 413, 429, 500, 502, 503, 504}

var errorText = map[int][2]string{
	400: {"Bad request", "The request couldn't be understood. Check the address and try again."},
	401: {"Sign-in required", "This page needs you to sign in first."},
	403: {"Access denied", "You don't have permission to view this page."},
	404: {"Page not found", "The page you're looking for doesn't exist or has moved."},
	405: {"Method not allowed", "This page doesn't accept that kind of request."},
	408: {"Request timed out", "The server waited too long for your request. Try again."},
	410: {"Page removed", "This page has been removed and won't be coming back."},
	413: {"Upload too large", "The file you sent is bigger than this site allows."},
	429: {"Too many requests", "You've sent a lot of requests in a short time. Wait a moment and try again."},
	500: {"Something went wrong", "The site hit an error while loading this page. Try again in a few minutes."},
	502: {"Site unavailable", "The site's application isn't responding right now. Try again in a few minutes."},
	503: {"Temporarily unavailable", "The site can't handle the request right now. Try again shortly."},
	504: {"Site took too long", "The site's application didn't respond in time. Try again in a few minutes."},
}

// One self-contained page design for every status and error page. Admins can
// edit the generated files in PagesDir; Ike never overwrites an existing file.
var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Title}}</title>
<style>
:root{--bg:#f3f4f9;--ink:#1e2142;--muted:#5d6182;--accent:#a8323e}
@media (prefers-color-scheme:dark){:root{--bg:#121428;--ink:#e9eaf6;--muted:#a2a6c8;--accent:#f0808c}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:var(--bg);color:var(--ink);font:16px/1.55 system-ui,-apple-system,"Segoe UI",sans-serif}
main{max-width:34rem}
.code{font-weight:700;font-size:clamp(4rem,14vw,7rem);line-height:1;letter-spacing:-.04em;color:var(--accent)}
h1{font-size:1.6rem;line-height:1.2;margin:.6rem 0 .5rem}
p{margin:0;color:var(--muted)}
</style>
</head>
<body>
<main>
{{if .Code}}<div class="code">{{.Code}}</div>{{end}}
<h1>{{.Title}}</h1>
<p>{{.Text}}</p>
</main>
</body>
</html>
`))

type pageData struct {
	Code        int
	Title, Text string
}

func renderPage(d pageData) []byte {
	var b bytes.Buffer
	_ = pageTmpl.Execute(&b, d)
	return b.Bytes()
}

// defaultPages returns every server-wide page by relative path.
func defaultPages() map[string][]byte {
	out := map[string][]byte{
		"maintenance.html": renderPage(pageData{Title: "Back soon", Text: "We're doing some maintenance on this site. Please check back in a little while."}),
		"suspended.html":   renderPage(pageData{Title: "Site unavailable", Text: "This website is currently unavailable. If you own it, contact your hosting provider."}),
		"default/index.php": []byte(`<?php /* Starter page created by Ike. Replace it with your website. */ ?>
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{domain}}</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f3f4f9;color:#1e2142}main{text-align:center;padding:24px}code{background:#e3e5f0;padding:2px 6px;border-radius:6px}</style>
</head>
<body><main>
<h1>{{domain}} is ready</h1>
<p>Running PHP <?= htmlspecialchars(PHP_VERSION) ?>. Upload your files to <code>public_html</code>.</p>
</main></body>
</html>
`),
		"default/index.html": []byte(`<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{domain}}</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f3f4f9;color:#1e2142}main{text-align:center;padding:24px}code{background:#e3e5f0;padding:2px 6px;border-radius:6px}</style>
</head>
<body><main><h1>{{domain}} is ready</h1><p>Upload your files to <code>public_html</code>.</p></main></body>
</html>
`),
	}
	for _, c := range ErrorCodes {
		t := errorText[c]
		out[fmt.Sprintf("errors/%d.html", c)] = renderPage(pageData{Code: c, Title: t[0], Text: t[1]})
	}
	return out
}

// EnsurePages writes any missing server-wide page into PagesDir. Existing
// files (customized by the admin) are left alone.
func (m *Manager) EnsurePages() error {
	for rel, body := range defaultPages() {
		path := m.p(filepath.Join(m.cfg.PagesDir, rel))
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := writeFile(path, body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// starterPage returns the admin's starter page for a runtime with {{domain}} filled in.
func (m *Manager) starterPage(name, domain string) []byte {
	b, err := os.ReadFile(m.p(filepath.Join(m.cfg.PagesDir, "default", name)))
	if err != nil {
		b = defaultPages()["default/"+name]
	}
	return bytes.ReplaceAll(b, []byte("{{domain}}"), []byte(domain))
}

// copyErrorPages gives a new website its own editable copies of the error pages.
func (m *Manager) copyErrorPages(L layout) error {
	for _, c := range ErrorCodes {
		name := fmt.Sprintf("%d.html", c)
		b, err := os.ReadFile(m.p(filepath.Join(m.cfg.PagesDir, "errors", name)))
		if err != nil {
			b = defaultPages()["errors/"+name]
		}
		dst := filepath.Join(L.ErrorsDir, name)
		if err := writeFile(m.p(dst), b, 0o640); err != nil {
			return err
		}
		if err := m.chown(dst, L.User, true); err != nil {
			return err
		}
	}
	return nil
}

// SetStatusRequest puts a website into maintenance, suspends it, or reactivates it.
type SetStatusRequest struct {
	Status   string   `json:"status"`
	AllowIPs []string `json:"allow_ips,omitempty"` // maintenance only: these IPs still see the real site
}

// SetStatus changes a website's status.
func (m *Manager) SetStatus(ctx context.Context, domain string, req SetStatusRequest) (*Site, error) {
	switch req.Status {
	case StatusActive, StatusMaintenance, StatusSuspended:
	default:
		return nil, fmt.Errorf("status must be active, maintenance or suspended")
	}
	if len(req.AllowIPs) > 20 {
		return nil, fmt.Errorf("at most 20 allowed IP addresses")
	}
	for _, ip := range req.AllowIPs {
		if net.ParseIP(ip) == nil {
			return nil, fmt.Errorf("%q is not an IP address", ip)
		}
	}
	if req.Status != StatusMaintenance {
		req.AllowIPs = nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	return m.setStatus(ctx, site, req.Status, req.AllowIPs)
}

func (m *Manager) setStatus(ctx context.Context, site *Site, status string, allow []string) (*Site, error) {
	old := *site
	site.Status, site.AllowIPs = status, allow
	L := m.layout(site)
	oldL := m.layout(&old)

	if L.usesNginx() {
		if err := m.writeNginxVhost(ctx, L); err != nil {
			_ = m.writeNginxVhost(context.Background(), oldL)
			return nil, err
		}
	} else if err := m.writeApacheVhost(ctx, L); err != nil {
		_ = m.writeApacheVhost(context.Background(), oldL)
		return nil, err
	}
	// Suspended apps stop using resources; reactivated ones start again.
	if L.IsApp {
		action := "start"
		if status == StatusSuspended {
			action = "stop"
		}
		if _, err := m.run.Run(ctx, "systemctl", action, L.UnitName); err != nil {
			slog.Warn("app state change failed", "domain", site.Domain, "action", action, "err", err)
		}
	}
	if err := m.writeJSON(L.StateFile, site); err != nil {
		return nil, err
	}
	slog.Info("site status changed", "domain", site.Domain, "status", status)
	return site, nil
}

// statusOf normalizes an empty status (older state files) to active.
func statusOf(s string) string {
	if s == "" {
		return StatusActive
	}
	return strings.ToLower(s)
}
