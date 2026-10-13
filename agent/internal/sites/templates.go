package sites

import "text/template"

// Every value inserted into these templates has already passed validation, so
// domains, users and versions cannot contain spaces, quotes, braces or newlines.
// The only free-form value, StartCommand, is single-line and only ever runs as
// the account's own user.

// Nginx vhost. Three shapes:
//   - app:          proxy everything to the Node.js/Python process
//   - nginx+apache: serve static files, proxy the rest to Apache (.htaccess works)
//   - nginx:        serve files and PHP directly
var nginxTmpl = template.Must(template.New("nginx").Parse(`# Managed by Ike. Manual changes will be overwritten.
server {
    listen 80;
    listen [::]:80;
    server_name {{.Domain}} www.{{.Domain}};

    # Logs live outside the user's home so a user cannot point them at system files.
    access_log {{.LogDir}}/access.log;
    error_log  {{.LogDir}}/error.log;

    client_max_body_size 64m;

    location ^~ /.well-known/acme-challenge/ {
        root {{.DocRoot}};
        allow all;
    }
    # Block dotfiles (.git, .env, .htaccess ...)
    location ~ /\. {
        deny all;
    }

    # Error pages: the website's own errors/ folder first, then the server-wide set.
    error_page {{.ErrorCodes}} /__ike_error/$status.html;
    location ^~ /__ike_error/ {
        internal;
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
        root /;
        add_header Retry-After 3600 always;
        try_files {{.StatusPage}} =503;
    }
{{- end}}
{{- if .IsApp}}

    # {{.Runtime}} app on 127.0.0.1:{{.Port}}
    location / {
        proxy_pass http://127.0.0.1:{{.Port}};
{{template "proxyHeaders"}}
    }
{{- else if eq .WebServer "nginx+apache"}}

    root {{.DocRoot}};

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

    root {{.DocRoot}};
    index{{if eq .Runtime "php"}} index.php{{end}} index.html index.htm;

    location / {
        try_files $uri $uri/ {{if eq .Runtime "php"}}/index.php?$args{{else}}=404{{end}};
    }
{{- if eq .Runtime "php"}}

    location ~ \.php$ {
        try_files $uri =404;
        fastcgi_pass unix:{{.PHPSocket}};
        fastcgi_index index.php;
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_read_timeout 300;
    }
{{- end}}

    location ~* \.(css|js|mjs|jpg|jpeg|png|gif|webp|avif|svg|ico|woff2?)$ {
        expires 30d;
        access_log off;
    }
{{- end}}
}
{{define "proxyHeaders"}}        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection $connection_upgrade_ike;
        proxy_read_timeout 300;{{end}}`))

// Apache vhost, used for "apache" (Apache alone on port 80) and "nginx+apache"
// (Apache behind Nginx on 127.0.0.1:8080).
var apacheTmpl = template.Must(template.New("apache").Parse(`# Managed by Ike. Manual changes will be overwritten.
<VirtualHost {{.ApacheListen}}>
    ServerName {{.Domain}}
    ServerAlias www.{{.Domain}}

    ErrorLog {{.LogDir}}/apache-error.log
    CustomLog {{.LogDir}}/apache-access.log combined

    # Error pages from the website's own errors/ folder.
    Alias /__ike_error/ {{.ErrorsDir}}/
    <Directory {{.ErrorsDir}}>
        Require all granted
    </Directory>
{{- range .ErrorCodeList}}
    ErrorDocument {{.}} /__ike_error/{{.}}.html
{{- end}}
{{- if ne .Status "active"}}

    # Website is {{.Status}}.
    Alias /__ike_status.html {{.StatusPage}}
    ErrorDocument 503 /__ike_status.html
    Header always set Retry-After "3600" "expr=%{REQUEST_STATUS} == 503"
    RewriteEngine On
    RewriteCond %{REQUEST_URI} !^/\.well-known/acme-challenge/
    RewriteCond %{REQUEST_URI} !^/__ike_status\.html$
{{- range .AllowIPsRegex}}
    RewriteCond %{REMOTE_ADDR} !^{{.}}$
{{- end}}
    RewriteRule ^ - [R=503,L]
{{- end}}
{{- if .IsApp}}

    ProxyPreserveHost On
    ProxyPass /.well-known/acme-challenge/ !
    Alias /.well-known/acme-challenge/ {{.DocRoot}}/.well-known/acme-challenge/
    ProxyPass / http://127.0.0.1:{{.Port}}/ upgrade=websocket
    ProxyPassReverse / http://127.0.0.1:{{.Port}}/
    RequestHeader set X-Forwarded-Proto "http"
{{- else}}

    DocumentRoot {{.DocRoot}}
    <Directory {{.DocRoot}}>
        Options -Indexes +SymLinksIfOwnerMatch
        AllowOverride All
        Require all granted
        DirectoryIndex{{if eq .Runtime "php"}} index.php{{end}} index.html index.htm
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
</VirtualHost>
`))

var poolTmpl = template.Must(template.New("pool").Parse(`; Managed by Ike. Manual changes will be overwritten.
[{{.PoolName}}]
user = {{.User}}
group = {{.User}}

listen = {{.PHPSocket}}
listen.owner = {{.WebUser}}
listen.group = {{.WebGroup}}
listen.mode = 0660

pm = ondemand
pm.max_children = 5
pm.process_idle_timeout = 30s
pm.max_requests = 500

chdir = /
php_admin_value[open_basedir] = {{.SiteDir}}:{{.TmpDir}}
php_admin_value[upload_tmp_dir] = {{.TmpDir}}
php_admin_value[session.save_path] = {{.TmpDir}}
php_admin_value[sys_temp_dir] = {{.TmpDir}}
php_admin_value[error_log] = {{.SiteDir}}/logs/php-error.log
php_admin_flag[log_errors] = on
php_admin_value[disable_functions] = exec,passthru,shell_exec,system,proc_open,popen,pcntl_exec
php_admin_value[upload_max_filesize] = 64M
php_admin_value[post_max_size] = 64M
php_admin_value[memory_limit] = 256M
`))

// Root-owned launcher for Node.js/Python apps. Runs as the account user.
var startScriptTmpl = template.Must(template.New("start").Parse(`#!/bin/bash
# Managed by Ike. Change the start command in the panel, not here.
set -a
if [ -f "{{.SiteDir}}/.env" ]; then . "{{.SiteDir}}/.env"; fi
set +a
export PORT={{.Port}}
export HOST=127.0.0.1
{{- if eq .Runtime "node"}}
export NODE_ENV="${NODE_ENV:-production}"
export PATH="{{.AppDir}}/node_modules/.bin:{{.RuntimeBin}}:/usr/local/bin:/usr/bin:/bin"
{{- else}}
export VIRTUAL_ENV="{{.AppDir}}/.venv"
export PYTHONUNBUFFERED=1
export PATH="{{.AppDir}}/.venv/bin:{{.RuntimeBin}}:/usr/local/bin:/usr/bin:/bin"
{{- end}}
cd "{{.AppDir}}" || exit 1
exec {{.StartCommand}}
`))

var unitTmpl = template.Must(template.New("unit").Parse(`# Managed by Ike. Manual changes will be overwritten.
[Unit]
Description=Ike {{.Runtime}} app for {{.Domain}}
After=network.target

[Service]
Type=simple
User={{.User}}
Group={{.User}}
WorkingDirectory={{.AppDir}}
ExecStart=/bin/bash {{.StartScript}}
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
MemoryMax=1G
TasksMax=256

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
