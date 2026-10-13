package sites

import "text/template"

// Every value inserted into these templates has already passed package validate,
// so it cannot contain spaces, semicolons, braces or newlines.

var nginxTmpl = template.Must(template.New("nginx").Parse(`# Managed by Ike. Manual changes will be overwritten.
server {
    listen 80;
    listen [::]:80;
    server_name {{.Domain}} www.{{.Domain}};

    root {{.DocRoot}};
    index index.php index.html index.htm;

    # Logs live outside the user's home so a user cannot point them at system files.
    access_log {{.LogDir}}/access.log;
    error_log  {{.LogDir}}/error.log;

    client_max_body_size 64m;

    # Allow ACME challenges, block other dotfiles (.git, .env, .htaccess ...)
    location ^~ /.well-known/acme-challenge/ {
        allow all;
    }
    location ~ /\. {
        deny all;
    }

    location / {
        try_files $uri $uri/ /index.php?$args;
    }

    location ~ \.php$ {
        try_files $uri =404;
        fastcgi_pass unix:{{.PHPSocket}};
        fastcgi_index index.php;
        include fastcgi_params;
        fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
        fastcgi_param HTTPS $https if_not_empty;
        fastcgi_read_timeout 300;
    }

    location ~* \.(css|js|jpg|jpeg|png|gif|webp|avif|svg|ico|woff2?)$ {
        expires 30d;
        access_log off;
    }
}
`))

var poolTmpl = template.Must(template.New("pool").Parse(`; Managed by Ike. Manual changes will be overwritten.
[{{.User}}]
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

const defaultIndex = `<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Site ready</title>
<style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;background:#f5f5f4;color:#1c1917}main{text-align:center}</style>
</head>
<body><main><h1>Your site is ready</h1><p>Upload your files to <code>public_html</code>.</p></main></body>
</html>
`
