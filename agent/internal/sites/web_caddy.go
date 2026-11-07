package sites

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
)

// Caddy serves the "caddy" stack. Ike keeps issuing the certificates (so the
// SSL page, uploaded certificates and expiry alerts work the same), so the
// main Caddyfile turns automatic HTTPS off and every website names its
// certificate files. One file per website in CaddySitesDir, imported by the
// main Caddyfile the installer writes.
//
// Inside each website, a "route" block keeps Ike's order: certificate
// checks, the suspended/maintenance page, hidden files, redirects, password
// checks, then the website itself. Password-protected folders are checked by
// the agent over a local socket (forward_auth), against the same password
// files Nginx and Apache read.

var caddyFuncs = template.FuncMap{
	"dir":  filepath.Dir,
	"base": filepath.Base,
	"addrs": func(scheme string, names []string) string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = scheme + "://" + n
		}
		return strings.Join(out, ", ")
	},
	"q": func(s string) string { return "`" + s + "`" },
}

var caddyTmpl = template.Must(template.New("caddy").Funcs(caddyFuncs).Parse(`# Managed by Ike. Manual changes will be overwritten.

(ike-{{.SiteKey}}) {
	log {
		output file {{.LogDir}}/access.log {
			roll_size 50MiB
			roll_keep 5
		}
	}
	request_body {
		max_size {{.BodyLimitMB}}MB
	}
{{- if .Staging}}
	# Staging copy: keep it out of search results.
	header X-Robots-Tag "noindex, nofollow"
{{- end}}

	# Error pages from the website's own errors/ folder.
	handle_errors {
{{- if ne .Status "active"}}
		@ike_status expression ` + "`{err.status_code} == 503`" + `
		handle @ike_status {
			header Retry-After 3600
			root * {{dir .StatusPage}}
			rewrite * /{{base .StatusPage}}
			file_server
		}
{{- end}}
		handle {
			root * {{.ErrorsDir}}
			rewrite * /{err.status_code}.html
			file_server
		}
	}

	route {
		handle /.well-known/acme-challenge/* {
			root * {{.AcmeDir}}
			file_server
		}
{{- if ne .Status "active"}}

		# Website is {{.Status}}.
		@ike_closed {
{{- if .AllowIPs}}
			not remote_ip{{range .AllowIPs}} {{.}}{{end}}
{{- else}}
			path *
{{- end}}
		}
		error @ike_closed 503
{{- end}}

		# Block dotfiles (.git, .env, .htaccess ...)
		@ike_dot path_regexp ike_dot ` + "`/\\.`" + `
		error @ike_dot 403
{{- if .CaddyRedirects}}

		# Redirects and rewrites
{{- range .CaddyRedirects}}
		{{.}}
{{- end}}
{{- end}}
{{- range .CaddyAuth}}

		# Password for {{.Path}}
		@ike_auth_{{.ID}} {
			path_regexp {{q .Regex}}
{{- if .Longer}}
			not path_regexp {{q .Longer}}
{{- end}}
{{- if .IPs}}
			not remote_ip{{range .IPs}} {{.}}{{end}}
{{- end}}
		}
		forward_auth @ike_auth_{{.ID}} unix/{{$.AuthSocket}} {
			uri /check?file={{.FileID}}&realm={{urlquery .Realm}}
		}
{{- end}}
{{- if .Balanced}}

		# Load balancer
		reverse_proxy {
			to{{range .CaddyBackends}} {{.}}{{end}}
			lb_policy {{.CaddyPolicy}}
			lb_try_duration 5s
			fail_duration {{.CaddyFailSeconds}}s
			max_fails {{.CaddyMaxFails}}
{{- if ne .UpstreamHost "$host"}}
			header_up Host {{.UpstreamHost}}
{{- end}}
{{- if eq .UpstreamScheme "https"}}
			transport http {
				tls
			}
{{- end}}
		}
{{- else if .Proxied}}

		# {{.Runtime}} on 127.0.0.1:{{.ActivePort}}
		reverse_proxy 127.0.0.1:{{.ActivePort}}
{{- else}}

		root * {{.Root}}
		encode gzip
{{- if eq .Runtime "php"}}
		php_fastcgi unix/{{.PHPSocket}} {
			read_timeout {{.PHPReadTimeout}}s
		}
{{- end}}
		@ike_static path_regexp ike_static ` + "`\\.(css|js|mjs|jpg|jpeg|png|gif|webp|avif|svg|ico|woff2?)$`" + `
		header @ike_static Cache-Control "public, max-age=2592000"
{{- if .SPA}}
		# Single-page app: unknown paths get index.html.
		try_files {path} {path}/ /index.html
{{- end}}
		file_server
{{- end}}
	}
}
{{- if .RedirectNames}}

# Other names for this website go to {{.CanonicalHost}}.
{{addrs "http" .RedirectList}} {
{{- template "redirBody" .}}
}
{{- if .TLS}}

{{addrs "https" .RedirectList}} {
	tls {{.CaddyCert}} {{.CaddyKey}}
{{- template "redirBody" .}}
}
{{- end}}
{{- end}}

{{addrs "http" .ServerList}} {
{{- if and .TLS .ForceHTTPS}}
	handle /.well-known/acme-challenge/* {
		root * {{.AcmeDir}}
		file_server
	}
	handle {
		redir https://{host}{uri} 301
	}
{{- else}}
	import ike-{{.SiteKey}}
{{- end}}
}
{{- if .TLS}}

{{addrs "https" .ServerList}} {
	tls {{.CaddyCert}} {{.CaddyKey}}
{{- if .HSTS}}
	header Strict-Transport-Security "max-age=31536000"
{{- end}}
	import ike-{{.SiteKey}}
}
{{- end}}
{{define "redirBody"}}
	handle /.well-known/acme-challenge/* {
		root * {{.AcmeDir}}
		file_server
	}
	handle {
		redir {{if .ForceHTTPS}}https{{else}}{scheme}{{end}}://{{.CanonicalHost}}{uri} 301
	}
{{- end}}
`))

// caddyCertDir holds the copy of a website's certificate Caddy reads. The
// certbot renewal hook (CaddyDeployHook) refreshes it.
func (m *Manager) caddyCertDir(domain string) string {
	return filepath.Join(m.cfg.CaddySitesDir, "certs", domain)
}

// CaddyDeployHook is the certbot --deploy-hook on Caddy servers, written by
// the installer: it copies a renewed certificate where Caddy reads it and
// reloads Caddy.
const CaddyDeployHook = "/usr/local/lib/ike/caddy-deploy-hook"

// caddyAuth is one protected folder for Caddy. Only the longest matching
// folder applies, so each rule leaves out the longer folders inside it.
type caddyAuth struct {
	ID, Path, Regex, Longer, Realm, FileID string
	IPs                                    []string
}

var groupRefRe = regexp.MustCompile(`\$(\d)`)

// renderCaddyRedirects turns redirect rules into Caddy directives. Regex
// groups ($1) become placeholders of the rule's named matcher.
func renderCaddyRedirects(rules []Redirect) []string {
	var out []string
	for i, r := range rules {
		re, suffix := r.pattern()
		name := fmt.Sprintf("ike_r%d", i)
		to := groupRefRe.ReplaceAllString(r.To+suffix, "{re."+name+".$1}")
		out = append(out, fmt.Sprintf("@%s path_regexp %s `%s`", name, name, re))
		if r.Kind == "rewrite" {
			if !r.KeepQuery && !strings.Contains(to, "?") {
				to += "?"
			}
			out = append(out, fmt.Sprintf("rewrite @%s %s", name, to))
			continue
		}
		if r.KeepQuery {
			to += "{?query}"
		}
		out = append(out, fmt.Sprintf("redir @%s %s %d", name, to, r.Code))
	}
	return out
}

func (m *Manager) applyCaddyLayout(s *Site, L *layout) {
	if L.WebServer != WebCaddy {
		return
	}
	L.AuthSocket = m.cfg.AuthSocket
	if L.TLS {
		// Caddy reads certificates as its own user, so it gets a copy it may read.
		d := m.caddyCertDir(s.Domain)
		L.CaddyCert, L.CaddyKey = filepath.Join(d, "fullchain.pem"), filepath.Join(d, "privkey.pem")
	}
	L.CaddyRedirects = renderCaddyRedirects(s.Redirects)
	// AuthRules are shortest first; a rule must not apply where a longer
	// (more specific) folder has its own rule.
	for i, a := range L.AuthRules {
		var longer []string
		for _, b := range L.AuthRules[i+1:] {
			longer = append(longer, "(?:"+b.Regex+")")
		}
		path := "/"
		for _, p := range s.Protection.Paths {
			if p.ID == a.ID {
				path = p.Path
			}
		}
		L.CaddyAuth = append(L.CaddyAuth, caddyAuth{ID: a.ID, Path: path, Regex: a.Regex, Longer: strings.Join(longer, "|"),
			Realm: a.Realm, FileID: s.Domain + "/" + a.ID, IPs: a.IPs})
	}
	if L.Balanced && s.Balancer != nil {
		b := s.Balancer
		for _, be := range b.Backends {
			if !be.Down {
				L.CaddyBackends = append(L.CaddyBackends, be.Address)
			}
		}
		L.CaddyPolicy = map[string]string{"least_conn": "least_conn", "ip_hash": "ip_hash", "random": "random_choose 2"}[b.Method]
		if L.CaddyPolicy == "" {
			L.CaddyPolicy = "round_robin"
		}
		L.CaddyMaxFails, L.CaddyFailSeconds = b.MaxFails, b.FailSeconds
		if L.CaddyMaxFails < 1 {
			L.CaddyMaxFails = 1
		}
		if L.CaddyFailSeconds < 1 {
			L.CaddyFailSeconds = 10
		}
	}
}
