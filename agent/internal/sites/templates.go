package sites

import "text/template"

// Every value inserted into these templates has already passed validation, so
// domains, users and versions cannot contain spaces, quotes, braces or newlines.
// The only free-form value, StartCommand, is single-line and only ever runs as
// the account's own user.

// Nginx vhost. The website body has three shapes:
//   - proxied:      forward everything to an app, container or local port
//   - nginx+apache: serve static files, proxy the rest to Apache (.htaccess works)
//   - nginx:        serve files and PHP directly
//
// Around it: an optional server block that redirects other names to the main
// one, the port 80 server (or a redirect to HTTPS), and the HTTPS server.
var nginxTmpl = template.Must(template.New("nginx").Parse(`# Managed by Ike. Manual changes will be overwritten.
{{- range .AuthRules}}{{if .IPs}}

# Addresses that open {{.Regex}} without a password
geo {{.Geo}} {
    default 0;
{{- range .IPs}}
    {{.}} 1;
{{- end}}
}
{{- end}}{{end}}
{{- if .NginxUpstream}}

# Load balancer pool
{{.NginxUpstream}}
{{- end}}
{{- if .RedirectNames}}

# Other names for this website go to {{.CanonicalHost}}.
server {
    listen 80;
    listen [::]:80;
{{- if .TLS}}
{{template "tlsListen" .}}
{{- end}}
    server_name {{.RedirectNames}};

    location ^~ /.well-known/acme-challenge/ {
        root {{.AcmeDir}};
    }
    location / {
        return 301 {{if .ForceHTTPS}}https{{else}}$scheme{{end}}://{{.CanonicalHost}}$request_uri;
    }
}
{{- end}}

server {
    listen 80;
    listen [::]:80;
    server_name {{.ServerNames}};
{{- if and .TLS .ForceHTTPS}}

    access_log {{.LogDir}}/access.log;
    error_log  {{.LogDir}}/error.log;

    location ^~ /.well-known/acme-challenge/ {
        root {{.AcmeDir}};
    }
    location / {
        return 301 https://$host$request_uri;
    }
}
{{- else}}
{{template "site" .}}
}
{{- end}}
{{- if .TLS}}

server {
{{template "tlsListen" .}}
    server_name {{.ServerNames}};
{{- if .HSTS}}
    add_header Strict-Transport-Security "max-age=31536000" always;
{{- end}}
{{- if .HTTP3}}
    add_header Alt-Svc 'h3=":443"; ma=86400' always;
{{- end}}
{{template "site" .}}
}
{{- end}}
{{define "tlsListen"}}    listen 443 ssl{{if and .HTTP2 (not .H2Directive)}} http2{{end}};
    listen [::]:443 ssl{{if and .HTTP2 (not .H2Directive)}} http2{{end}};
{{- if .HTTP3}}
    listen 443 quic;
    listen [::]:443 quic;
{{- end}}
{{- if .H2Directive}}
    http2 {{if .HTTP2}}on{{else}}off{{end}};
{{- end}}
    ssl_certificate     {{.CertFile}};
    ssl_certificate_key {{.KeyFile}};
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:ike_ssl:10m;
    ssl_session_timeout 1d;
    ssl_session_tickets off;{{end}}
{{define "site"}}
    # Logs live outside the user's home so a user cannot point them at system files.
    access_log {{.LogDir}}/access.log;
    error_log  {{.LogDir}}/error.log;

    client_max_body_size {{.BodyLimitMB}}m;
{{- if .AuthRules}}

    # Password protection: the longest matching path decides.
    set $ike_auth off;
    set $ike_auth_rule "";
{{- range .AuthRules}}
    if ($uri ~ "{{.Regex}}") { set $ike_auth_rule {{.ID}}; }
{{- end}}
{{- range .AuthRules}}
    if ($ike_auth_rule = {{.ID}}) { set $ike_auth "{{.Realm}}"; set $ike_auth_file {{.File}}; }
{{- if .IPs}}
    if ($ike_auth_rule = {{.ID}}) { set $ike_auth_ip {{.Geo}}; }
{{- end}}
{{- end}}
{{- if .AuthAnyIPs}}
    if ($ike_auth_ip = 1) { set $ike_auth off; }
{{- end}}
    auth_basic $ike_auth;
    auth_basic_user_file $ike_auth_file;
{{- end}}
{{- if .WAFNginx}}

    # Web application firewall (ModSecurity with the OWASP Core Rule Set)
    modsecurity on;
    modsecurity_rules_file {{.WAFRulesFile}};
{{- end}}
{{- if .Staging}}

    # Staging copy: keep it out of search results.
    add_header X-Robots-Tag "noindex, nofollow" always;
{{- end}}

    location ^~ /.well-known/acme-challenge/ {
        root {{.AcmeDir}};
        allow all;
{{- if .AuthRules}}
        auth_basic off;
{{- end}}
    }
{{- if .NginxRedirects}}

    # Redirects
{{- range .NginxRedirects}}
    {{.}}
{{- end}}
{{- end}}

    # Block dotfiles (.git, .env, .htaccess ...)
    location ~ /\. {
        deny all;
    }

    # Error pages: the website's own errors/ folder first, then the server-wide set.
    error_page {{.ErrorCodes}} /__ike_error/$status.html;
    location ^~ /__ike_error/ {
        internal;
{{- if .AuthRules}}
        auth_basic off;
{{- end}}
        root /;
        try_files {{.ErrorsDir}}/$status.html {{.PagesDir}}/errors/$status.html =404;
    }
{{- if ne .Status "active"}}

    # Website is {{.Status}}.
    set $ike_open 0;
    if ($uri ~ "^/(\.well-known/acme-challenge/|__ike_status\.html$)") { set $ike_open 1; }
{{- range .AllowIPs}}
    if ($remote_addr = "{{.}}") { set $ike_open 1; }
{{- end}}
    if ($ike_open = 0) { return 503; }
    error_page 503 /__ike_status.html;
    location = /__ike_status.html {
        internal;
{{- if .AuthRules}}
        auth_basic off;
{{- end}}
        root /;
        add_header Retry-After 3600 always;
        try_files {{.StatusPage}} =503;
    }
{{- end}}
{{- if .NginxRewrites}}

    # Rewrites
{{- range .NginxRewrites}}
    {{.}}
{{- end}}
{{- end}}
{{- if .Balanced}}

    # Load balancer: spread over the backends in {{.UpstreamName}}
    location / {
        proxy_pass {{.UpstreamScheme}}://{{.UpstreamName}};
        proxy_http_version 1.1;
        proxy_set_header Host {{.UpstreamHost}};
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade_ike;
        proxy_next_upstream error timeout http_502 http_503 http_504;
        proxy_next_upstream_tries 3;
        proxy_connect_timeout 5s;
        proxy_read_timeout 300;
    }
{{- else if .Proxied}}

    # {{.Runtime}} on 127.0.0.1:{{.ActivePort}}
    location / {
        proxy_pass http://127.0.0.1:{{.ActivePort}};
{{template "proxyHeaders"}}
    }
{{- else if eq .WebServer "nginx+apache"}}

    root {{.Root}};

    # Static files straight from Nginx; everything else goes to Apache.
    location ~* \.(css|js|mjs|jpg|jpeg|png|gif|webp|avif|svg|ico|woff2?|ttf|mp4|webm|pdf|zip)$ {
        expires 30d;
        access_log off;
        try_files $uri @apache;
    }
    location / {
        proxy_pass http://{{.ApacheListen}};
{{template "proxyHeaders"}}
    }
    location @apache {
        proxy_pass http://{{.ApacheListen}};
{{template "proxyHeaders"}}
    }
{{- else}}

    root {{.Root}};
    index{{if eq .Runtime "php"}} index.php{{end}} index.html index.htm;

    location / {
        try_files $uri $uri/ {{if eq .Runtime "php"}}/index.php?$args{{else if .SPA}}/index.html{{else}}=404{{end}};
    }
{{- if eq .Runtime "php"}}

    location ~ \.php$ {
        try_files $uri =404;
        fastcgi_pass unix:{{.PHPSocket}};
        fastcgi_index index.php;
        include fastcgi_params;
        # realpath: a deployment switches releases atomically under PHP's cache.
        fastcgi_param SCRIPT_FILENAME $realpath_root$fastcgi_script_name;
        fastcgi_param DOCUMENT_ROOT $realpath_root;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_read_timeout {{.PHPReadTimeout}};
    }
{{- end}}

    location ~* \.(css|js|mjs|jpg|jpeg|png|gif|webp|avif|svg|ico|woff2?)$ {
        expires 30d;
        access_log off;
    }
{{- end}}
{{- end}}
{{define "proxyHeaders"}}        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade_ike;
        proxy_read_timeout 300;{{end}}`))

// Apache vhost, used for "apache" (Apache alone on port 80, and 443 with a
// certificate) and "nginx+apache" (Apache behind Nginx on 127.0.0.1:8080,
// where Nginx handles HTTPS).
var apacheTmpl = template.Must(template.New("apache").Parse(`# Managed by Ike. Manual changes will be overwritten.
{{- if .RedirectNames}}
{{- range .ApacheRedirectListens}}

# Other names for this website go to {{$.CanonicalHost}}.
<VirtualHost {{.}}>
    ServerName {{index $.RedirectList 0}}
{{- range slice $.RedirectList 1}}
    ServerAlias {{.}}
{{- end}}
{{- if eq . "*:443"}}
    SSLEngine on
    SSLCertificateFile {{$.CertFile}}
    SSLCertificateKeyFile {{$.KeyFile}}
{{- end}}
    Alias /.well-known/acme-challenge/ {{$.AcmeDir}}/.well-known/acme-challenge/
    <Directory {{$.AcmeDir}}>
        Require all granted
    </Directory>
    RewriteEngine On
    RewriteCond %{REQUEST_URI} !^/\.well-known/acme-challenge/
    RewriteRule ^ {{if $.ForceHTTPS}}https{{else}}%{REQUEST_SCHEME}{{end}}://{{$.CanonicalHost}}%{REQUEST_URI} [R=301,L]
</VirtualHost>
{{- end}}
{{- end}}

<VirtualHost {{.ApacheListen}}>
{{template "names" .}}
{{- if and .ApacheTLS .ForceHTTPS}}

    ErrorLog {{.LogDir}}/apache-error.log
    CustomLog {{.LogDir}}/apache-access.log combined
    Alias /.well-known/acme-challenge/ {{.AcmeDir}}/.well-known/acme-challenge/
    <Directory {{.AcmeDir}}>
        Require all granted
    </Directory>
    RewriteEngine On
    RewriteCond %{REQUEST_URI} !^/\.well-known/acme-challenge/
    RewriteRule ^ https://%{HTTP_HOST}%{REQUEST_URI} [R=301,L]
{{- else}}
{{template "body" .}}
{{- end}}
</VirtualHost>
{{- if .ApacheTLS}}

<VirtualHost *:443>
{{template "names" .}}
    SSLEngine on
    SSLCertificateFile {{.CertFile}}
    SSLCertificateKeyFile {{.KeyFile}}
{{- if .HTTP2}}
    Protocols h2 http/1.1
{{- end}}
{{- if .HSTS}}
    Header always set Strict-Transport-Security "max-age=31536000"
{{- end}}
{{template "body" .}}
</VirtualHost>
{{- end}}
{{define "names"}}    ServerName {{index .ServerList 0}}
{{- range slice .ServerList 1}}
    ServerAlias {{.}}
{{- end}}{{end}}
{{define "body"}}
    ErrorLog {{.LogDir}}/apache-error.log
    CustomLog {{.LogDir}}/apache-access.log combined
{{- if .WAFApache}}

    # Web application firewall (ModSecurity with the OWASP Core Rule Set)
    <IfModule security2_module>
        SecRuleEngine {{if .WAFBlock}}On{{else}}DetectionOnly{{end}}
{{- range .WAFDisabled}}
        SecRuleRemoveById {{.}}
{{- end}}
    </IfModule>
{{- end}}
{{- if not .ApacheTLS}}

    # HTTPS ends at Nginx in front; tell PHP the original scheme.
    SetEnvIf X-Forwarded-Proto "^https$" HTTPS=on
{{- end}}
{{- if .Staging}}
    Header always set X-Robots-Tag "noindex, nofollow"
{{- end}}

    Alias /.well-known/acme-challenge/ {{.AcmeDir}}/.well-known/acme-challenge/
    <Directory {{.AcmeDir}}>
        Require all granted
    </Directory>

    # Error pages from the website's own errors/ folder.
    Alias /__ike_error/ {{.ErrorsDir}}/
    <Directory {{.ErrorsDir}}>
        Require all granted
    </Directory>
{{- range .ErrorCodeList}}
    ErrorDocument {{.}} /__ike_error/{{.}}.html
{{- end}}
    RewriteEngine On
{{- if ne .Status "active"}}

    # Website is {{.Status}}.
    Alias /__ike_status.html {{.StatusPage}}
    ErrorDocument 503 /__ike_status.html
    Header always set Retry-After "3600" "expr=%{REQUEST_STATUS} == 503"
    RewriteCond %{REQUEST_URI} !^/\.well-known/acme-challenge/
    RewriteCond %{REQUEST_URI} !^/__ike_status\.html$
{{- range .AllowIPsRegex}}
    RewriteCond %{REMOTE_ADDR} !^{{.}}$
{{- end}}
    RewriteRule ^ - [R=503,L]
{{- end}}
{{- if and .AuthRules (eq .WebServer "apache")}}

    # Password protection: later blocks win, so the longest path comes last.
{{- range .AuthRules}}
    <LocationMatch "{{.Regex}}">
        AuthType Basic
        AuthName "{{.Realm}}"
        AuthUserFile {{.File}}
        <RequireAny>
            Require valid-user
{{- range .IPs}}
            Require ip {{.}}
{{- end}}
        </RequireAny>
    </LocationMatch>
{{- end}}
    <LocationMatch "^/(\.well-known/acme-challenge/|__ike_error/|__ike_status\.html$)">
        Require all granted
    </LocationMatch>
{{- end}}
{{- if and .ApacheRules (eq .WebServer "apache")}}

    # Redirects and rewrites
{{- range .ApacheRules}}
    {{.}}
{{- end}}
{{- end}}
{{- if .Balanced}}

    ProxyPreserveHost {{if eq .UpstreamHost "$host"}}On{{else}}Off{{end}}
    {{.ApacheBalancer}}
{{- else if .Proxied}}

    ProxyPreserveHost On
    ProxyPass /.well-known/acme-challenge/ !
    ProxyPass / http://127.0.0.1:{{.ActivePort}}/ upgrade=websocket
    ProxyPassReverse / http://127.0.0.1:{{.ActivePort}}/
    RequestHeader set X-Forwarded-Proto expr=%{REQUEST_SCHEME}
{{- else}}

    DocumentRoot {{.Root}}
    <Directory {{.Root}}>
        Options -Indexes +SymLinksIfOwnerMatch
        AllowOverride All
        Require all granted
        DirectoryIndex{{if eq .Runtime "php"}} index.php{{end}} index.html index.htm
{{- if .SPA}}
        # Single-page app: unknown paths get index.html.
        FallbackResource /index.html
{{- end}}
    </Directory>
{{- if eq .Runtime "php"}}

    <FilesMatch "\.php$">
        SetHandler "proxy:unix:{{.PHPSocket}}|fcgi://localhost"
    </FilesMatch>
{{- end}}
{{- end}}

    # Block dotfiles except ACME challenges
    <LocationMatch "/\.(?!well-known/)">
        Require all denied
    </LocationMatch>
{{- end}}
`))

var poolTmpl = template.Must(template.New("pool").Parse(`; Managed by Ike. Manual changes will be overwritten.
[{{.PoolName}}]
user = {{.User}}
group = {{.User}}

listen = {{.PHPSocket}}
listen.owner = {{.WebUser}}
listen.group = {{.WebGroup}}
listen.mode = 0660

pm = {{.PHP.PM}}
pm.max_children = {{.PHP.MaxChildren}}
{{- if eq .PHP.PM "dynamic"}}
pm.start_servers = {{.PHP.StartServers}}
pm.min_spare_servers = {{.PHP.MinSpareServers}}
pm.max_spare_servers = {{.PHP.MaxSpareServers}}
{{- end}}
{{- if eq .PHP.PM "ondemand"}}
pm.process_idle_timeout = 30s
{{- end}}
pm.max_requests = {{.PHP.MaxRequests}}

chdir = /
php_admin_value[open_basedir] = {{.SiteDir}}:{{.TmpDir}}
php_admin_value[upload_tmp_dir] = {{.TmpDir}}
php_admin_value[session.save_path] = {{.TmpDir}}
php_admin_value[sys_temp_dir] = {{.TmpDir}}
php_admin_value[error_log] = {{.SiteDir}}/logs/php-error.log
php_admin_flag[log_errors] = on
php_admin_flag[display_errors] = {{if .PHP.DisplayErrors}}on{{else}}off{{end}}
{{- if not .PHP.AllowShellFunctions}}
php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec
{{- end}}
php_admin_value[upload_max_filesize] = {{.PHP.UploadMaxMB}}M
php_admin_value[post_max_size] = {{.PHP.PostMaxMB}}M
php_admin_value[memory_limit] = {{.PHP.MemoryLimitMB}}M
php_admin_value[max_execution_time] = {{.PHP.MaxExecutionTime}}
php_admin_value[max_input_vars] = {{.PHP.MaxInputVars}}
{{- if .PHP.Timezone}}
php_admin_value[date.timezone] = {{.PHP.Timezone}}
{{- end}}
`))

// Root-owned launcher for apps. Runs as the account user.
var startScriptTmpl = template.Must(template.New("start").Parse(`#!/bin/bash
# Managed by Ike. Change the start command in the panel, not here.
set -a
if [ -f "{{.SiteDir}}/.env" ]; then . "{{.SiteDir}}/.env"; fi
set +a
export PORT={{.Port}}
export HOST=127.0.0.1
{{- range .AppEnvExtra}}
export {{.}}
{{- end}}
export PATH="{{.AppPath}}"
cd "{{.AppDir}}" || exit 1
exec {{.StartCommand}}
`))

var unitTmpl = template.Must(template.New("unit").Parse(`# Managed by Ike. Manual changes will be overwritten.
[Unit]
Description=Ike {{.Runtime}} app for {{.Domain}}
After=network.target
# Give up after 10 crashes in 5 minutes instead of looping forever; the health
# assistant reports it and a manual start resets the counter.
StartLimitIntervalSec=300
StartLimitBurst=10

[Service]
Type=simple
User={{.User}}
Group={{.User}}
WorkingDirectory={{.AppDir}}
ExecStart=/bin/bash {{.StartScript}}
Slice={{.Slice}}
Restart=always
RestartSec=3
# Isolation: the app sees only its own account home and cannot gain privileges.
NoNewPrivileges=yes
PrivateTmp=yes
PrivateDevices=yes
ProtectSystem=strict
ProtectHome=tmpfs
BindPaths={{.Home}}
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictSUIDSGID=yes
LockPersonality=yes
MemoryMax={{if eq .Runtime "java"}}2G{{else}}1G{{end}}
TasksMax={{if eq .Runtime "java"}}512{{else}}256{{end}}

[Install]
WantedBy=multi-user.target
`))

const defaultIndex = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site ready</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f3f4f9;color:#1e2142}main{text-align:center}</style>
</head>
<body><main><h1>Your site is ready</h1><p>Upload your files to <code>public_html</code>.</p></main></body>
</html>
`

const sampleNodeApp = `// Sample app created by Ike. Replace it with your own code, then restart the app.
const http = require("http");
const port = process.env.PORT || 3000;

http
  .createServer((req, res) => {
    res.writeHead(200, { "Content-Type": "text/html; charset=utf-8" });
    res.end("<h1>Your Node.js app is running</h1><p>Node " + process.version + "</p>");
  })
  .listen(port, "127.0.0.1", () => console.log("Listening on port " + port));
`

const samplePythonApp = `# Sample app created by Ike. Replace it with your own code (Flask, Django,
# FastAPI ...), install packages with: pip install -r requirements.txt
# then restart the app.
import os
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


class Hello(BaseHTTPRequestHandler):
    def do_GET(self):
        body = f"<h1>Your Python app is running</h1><p>Python {sys.version.split()[0]}</p>".encode()
        self.send_response(200)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


port = int(os.environ.get("PORT", "8000"))
print(f"Listening on port {port}", flush=True)
HTTPServer(("127.0.0.1", port), Hello).serve_forever()
`
