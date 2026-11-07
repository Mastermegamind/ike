package sites

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// OpenLiteSpeed serves the "openlitespeed" stack. Each website gets one
// vhost config in OLSSitesDir; sites.conf (included from httpd_config.conf
// by the installer) declares every Ike website and maps its names on the
// port 80 and 443 listeners. PHP runs in Ike's own PHP-FPM pools, reached as
// an external FastCGI app, so PHP versions, limits and per-account users
// work the same as on Nginx. Redirects and rewrites use OpenLiteSpeed's
// Apache-compatible rewrite engine, so they are the same rules Apache gets.

var olsFuncs = template.FuncMap{"join": strings.Join}

var olsVhostTmpl = template.Must(template.New("ols").Funcs(olsFuncs).Parse(`# Managed by Ike. Manual changes will be overwritten.
docRoot                   {{.Root}}/
enableGzip                1

errorlog {{.LogDir}}/error.log {
  useServer               0
  logLevel                WARN
  rollingSize             50M
}

accesslog {{.LogDir}}/access.log {
  useServer               0
  logFormat               "%h %l %u %t \"%r\" %>s %b \"%{Referer}i\" \"%{User-Agent}i\""
  rollingSize             50M
  keepDays                14
}

index  {
  useServer               0
  indexFiles              {{if eq .Runtime "php"}}index.php, {{end}}index.html, index.htm
  autoIndex               0
}
{{range .ErrorCodeList}}
errorpage {{.}} {
  url                     /__ike_error/{{.}}.html
}
{{- end}}
{{- if ne .Status "active"}}

errorpage 503 {
  url                     /__ike_status.html
}
{{- end}}
{{- if eq .Runtime "php"}}

# PHP {{.Version}} through Ike's PHP-FPM pool for {{.User}}
extprocessor {{.OLSApp}} {
  type                    fcgi
  address                 uds:/{{.PHPSocket}}
  maxConns                {{.PHPWorkers}}
  initTimeout             60
  retryTimeout            0
  respBuffer              0
  persistConn             1
  autoStart               0
}

scripthandler  {
  add                     fcgi:{{.OLSApp}} php
}
{{- else if .Proxied}}

# {{.Runtime}} on 127.0.0.1:{{.ActivePort}}
extprocessor {{.OLSApp}} {
  type                    proxy
  address                 127.0.0.1:{{.ActivePort}}
  maxConns                200
  initTimeout             60
  retryTimeout            0
  respBuffer              0
}
{{- end}}
{{- if .TLS}}

vhssl  {
  keyFile                 {{.KeyFile}}
  certFile                {{.CertFile}}
  certChain               1
{{- if .HTTP3}}
  enableQuic              1
{{- end}}
}
{{- end}}
{{- range .AuthRules}}

realm {{$.OLSApp}}_auth_{{.ID}} {
  userDB  {
    location              {{.File}}
  }
}

context exp:{{.Regex}} {
  location                {{$.Root}}/
  allowBrowse             1
  realm                   {{$.OLSApp}}_auth_{{.ID}}
  authName                "{{.Realm}}"
  required                valid-user
{{- if $.Proxied}}
  type                    proxy
  handler                 {{$.OLSApp}}
{{- end}}
}
{{- end}}

context /.well-known/acme-challenge/ {
  location                {{.AcmeDir}}/.well-known/acme-challenge/
  allowBrowse             1
}

context /__ike_error/ {
  location                {{.ErrorsDir}}/
  allowBrowse             1
}
{{- if ne .Status "active"}}

context /__ike_status.html {
  location                {{.StatusPage}}
  allowBrowse             1
  extraHeaders            <<<END_extraHeaders
set Retry-After 3600
  END_extraHeaders
}
{{- end}}

context / {
{{- if .Proxied}}
  type                    proxy
  handler                 {{.OLSApp}}
  addDefaultCharset       off
{{- else}}
  location                {{.Root}}/
  allowBrowse             1
{{- end}}
{{- if .OLSHeaders}}
  extraHeaders            <<<END_extraHeaders
{{- range .OLSHeaders}}
{{.}}
{{- end}}
  END_extraHeaders
{{- end}}
}

rewrite  {
  enable                  1
  autoLoadHtaccess        {{if .Proxied}}0{{else}}1{{end}}
  rules                   <<<END_rules
{{- range .OLSRules}}
{{.}}
{{- end}}
  END_rules
}
`))

// olsIndexTmpl declares every Ike website and maps it on the listeners.
var olsIndexTmpl = template.Must(template.New("olsindex").Funcs(olsFuncs).Parse(`# Managed by Ike. Manual changes will be overwritten.
# Included from {{.Main}}.
{{range .Sites}}
virtualhost {{.Name}} {
  vhRoot                  {{.Root}}/
  configFile              {{.Conf}}
  allowSymbolLink         1
  enableScript            1
  restrained              1
  setUIDMode              0
}
{{end}}
virtualhost ike-default {
  vhRoot                  {{.AcmeDir}}/
  configFile              {{.DefaultConf}}
  allowSymbolLink         0
  enableScript            0
  restrained              1
}

listener ike-http {
  address                 *:80
  secure                  0
  # Certificate checks for names without a website here (mail.<domain>)
  map                     ike-default *
{{- range .Sites}}
  map                     {{.Name}} {{join .Names ", "}}
{{- end}}
}

listener ike-https {
  address                 *:443
  secure                  1
  keyFile                 {{.DefaultKey}}
  certFile                {{.DefaultCert}}
  certChain               1
{{- range .Sites}}{{if .TLS}}
  map                     {{.Name}} {{join .Names ", "}}
{{- end}}{{end}}
}
`))

type olsSite struct {
	Name, Root, Conf string
	Names            []string
	TLS              bool
}

func (m *Manager) olsConf(domain string) string {
	return filepath.Join(m.cfg.OLSSitesDir, domain+".conf")
}
func (m *Manager) olsIndex() string { return filepath.Join(m.cfg.OLSSitesDir, "sites.conf") }

func (m *Manager) applyOLSLayout(s *Site, L *layout) {
	if L.WebServer != WebOLS {
		return
	}
	L.OLSApp = "ike_" + strings.ReplaceAll(L.PoolName, "-", "_")
	L.OLSFile = m.olsConf(s.Domain)
	var rules []string
	acme := `RewriteCond %{REQUEST_URI} !^/\.well-known/acme-challenge/`
	if L.RedirectNames != "" {
		scheme := "%{REQUEST_SCHEME}"
		if L.ForceHTTPS {
			scheme = "https"
		}
		rules = append(rules, "# Other names for this website go to "+L.CanonicalHost+".", acme,
			"RewriteCond %{HTTP_HOST} !^"+strings.ReplaceAll(L.CanonicalHost, ".", `\.`)+"(:\\d+)?$ [NC]",
			"RewriteRule ^ "+scheme+"://"+L.CanonicalHost+"%{REQUEST_URI} [R=301,L]")
	}
	if L.TLS && L.ForceHTTPS {
		rules = append(rules, acme, "RewriteCond %{HTTPS} !=on", "RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=301,L]")
	}
	if L.Status != StatusActive {
		rules = append(rules, "# Website is "+L.Status+".", acme, `RewriteCond %{REQUEST_URI} !^/(__ike_status\.html|__ike_error/)`)
		for _, ip := range L.AllowIPsRegex {
			rules = append(rules, "RewriteCond %{REMOTE_ADDR} !^"+ip+"$")
		}
		rules = append(rules, "RewriteRule ^ - [R=503,L]")
	}
	rules = append(rules, "# Block dotfiles except certificate checks", `RewriteRule (^|/)\.(?!well-known/) - [F,L]`)
	if len(L.ApacheRules) > 0 {
		rules = append(rules, "# Redirects and rewrites")
		rules = append(rules, L.ApacheRules...)
	}
	if L.SPA {
		rules = append(rules, "# Single-page app: unknown paths get index.html", `RewriteCond %{REQUEST_URI} !^/(\.well-known/|__ike_)`,
			"RewriteCond %{DOCUMENT_ROOT}%{REQUEST_URI} !-f", "RewriteCond %{DOCUMENT_ROOT}%{REQUEST_URI} !-d", "RewriteRule ^ /index.html [L]")
	}
	L.OLSRules = rules
	if L.HSTS {
		L.OLSHeaders = append(L.OLSHeaders, "set Strict-Transport-Security \"max-age=31536000\"")
	}
	if L.Staging {
		L.OLSHeaders = append(L.OLSHeaders, "set X-Robots-Tag \"noindex, nofollow\"")
	}
}

// writeOLSIndex rewrites sites.conf from every OpenLiteSpeed website, with
// `with` in place of its saved version (it may not be saved yet) and
// `without` left out (being deleted).
func (m *Manager) writeOLSIndex(with *layout, without string) error {
	list, _ := m.List()
	byDomain := map[string]olsSite{}
	add := func(L layout) {
		byDomain[L.Domain] = olsSite{Name: "ike-" + L.Domain, Root: L.SiteDir, Conf: L.OLSFile,
			Names: append(append([]string{}, L.ServerList...), L.RedirectList...), TLS: L.TLS}
	}
	for i := range list {
		s := &list[i]
		if s.WebServer == WebOLS && s.Domain != without {
			add(m.layout(s))
		}
	}
	if with != nil {
		add(*with)
	}
	var sites []olsSite
	for _, s := range byDomain {
		sites = append(sites, s)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Name < sites[j].Name })
	var b bytes.Buffer
	if err := olsIndexTmpl.Execute(&b, map[string]any{
		"Main": filepath.Join(m.cfg.OLSDir, "conf", "httpd_config.conf"), "Sites": sites,
		"DefaultKey": filepath.Join(m.cfg.OLSSitesDir, "default.key"), "DefaultCert": filepath.Join(m.cfg.OLSSitesDir, "default.crt"),
		"AcmeDir": m.cfg.AcmeDir, "DefaultConf": filepath.Join(m.cfg.OLSSitesDir, "default-vhost.conf"),
	}); err != nil {
		return err
	}
	if err := os.MkdirAll(m.p(m.cfg.OLSSitesDir), 0o750); err != nil {
		return err
	}
	// The catch-all host answers certificate checks only.
	def := "# Managed by Ike: certificate checks for names without a website here.\ndocRoot                   " + m.cfg.AcmeDir + "/\n" +
		"rewrite  {\n  enable                  1\n  rules                   <<<END_rules\nRewriteCond %{REQUEST_URI} !^/\\.well-known/acme-challenge/\nRewriteRule ^ - [F,L]\n  END_rules\n}\n"
	if err := writeFile(m.p(filepath.Join(m.cfg.OLSSitesDir, "default-vhost.conf")), []byte(def), 0o640); err != nil {
		return err
	}
	if err := m.chownWebGroup(filepath.Join(m.cfg.OLSSitesDir, "default-vhost.conf")); err != nil {
		return err
	}
	if err := writeFile(m.p(m.olsIndex()), b.Bytes(), 0o640); err != nil {
		return err
	}
	return m.chownWebGroup(m.olsIndex())
}

func (m *Manager) olsBin(name string) string { return filepath.Join(m.cfg.OLSDir, "bin", name) }

// olsReload checks the whole configuration and restarts OpenLiteSpeed
// gracefully (it finishes open requests first).
func (m *Manager) olsReload(ctx context.Context) error {
	if out, err := m.run.Run(ctx, m.olsBin("openlitespeed"), "-t"); err != nil {
		return fmt.Errorf("OpenLiteSpeed rejected the configuration: %s", firstLine(string(out), err))
	}
	_, err := m.run.Run(ctx, m.olsBin("lswsctrl"), "restart")
	return err
}

func (m *Manager) writeOLSVhost(ctx context.Context, L layout) error {
	if len(L.AuthRules) > 0 {
		for _, a := range L.AuthRules {
			if len(a.IPs) > 0 {
				return fmt.Errorf("OpenLiteSpeed can't let addresses into a password-protected folder without the password; remove the addresses from %s", a.Regex)
			}
		}
	}
	if L.WAFNginx || L.WAFApache {
		return fmt.Errorf("the web application firewall needs Nginx or Apache")
	}
	if L.Balanced {
		return fmt.Errorf("load-balanced websites need Nginx, Apache or Caddy")
	}
	old, oldErr := os.ReadFile(m.p(L.OLSFile))
	oldIndex, indexErr := os.ReadFile(m.p(m.olsIndex()))
	if err := m.render(olsVhostTmpl, L, L.OLSFile, 0o640); err != nil {
		return err
	}
	if err := m.chownWebGroup(L.OLSFile); err != nil {
		return err
	}
	if err := m.writeOLSIndex(&L, ""); err != nil {
		return err
	}
	if err := m.olsReload(ctx); err != nil {
		if oldErr == nil {
			_ = writeFile(m.p(L.OLSFile), old, 0o640)
		} else {
			_ = os.Remove(m.p(L.OLSFile))
		}
		if indexErr == nil {
			_ = writeFile(m.p(m.olsIndex()), oldIndex, 0o640)
		}
		return err
	}
	return nil
}

func (m *Manager) removeOLSVhost(ctx context.Context, L layout) {
	if _, err := os.Stat(m.p(L.OLSFile)); err != nil {
		return
	}
	_ = os.Remove(m.p(L.OLSFile))
	_ = m.writeOLSIndex(nil, L.Domain)
	_ = m.olsReload(ctx)
}

func firstLine(out string, err error) string {
	if l, _, _ := strings.Cut(strings.TrimSpace(out), "\n"); l != "" {
		return l
	}
	return err.Error()
}
