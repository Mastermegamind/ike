package mail

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// Webmail on the Caddy and OpenLiteSpeed stacks (Nginx and Apache are in ops.go).

func (m *Manager) webGroupOwn(path string) {
	if m.env.Cfg.DryRun {
		return
	}
	if g, err := user.LookupGroup(m.env.Cfg.WebGroup); err == nil {
		gid, _ := strconv.Atoi(g.Gid)
		_ = os.Chown(path, 0, gid)
	}
}

// writeCaddyWebmail serves webmail on its own HTTPS port. Caddy reads a copy
// of each mail.<domain> certificate (the renewal hook keeps it fresh).
func (m *Manager) writeCaddyWebmail(ctx context.Context, names, tlsHosts []string, port, bodyMB int, root, sock string) error {
	cfg := m.env.Cfg
	var b strings.Builder
	b.WriteString("# Written by Ike: webmail and certificate checks for mail.<domain> names.\n")
	if len(names) > 0 {
		addrs := make([]string, len(names))
		for i, n := range names {
			addrs[i] = "http://" + n
		}
		fmt.Fprintf(&b, "\n%s {\n\thandle /.well-known/acme-challenge/* {\n\t\troot * %s\n\t\tfile_server\n\t}\n\thandle {\n\t\tredir https://{host}:%d{uri} 302\n\t}\n}\n",
			strings.Join(addrs, ", "), cfg.AcmeDir, port)
	}
	for _, h := range tlsHosts {
		dir := filepath.Join(cfg.CaddySitesDir, "certs", h)
		if err := os.MkdirAll(m.env.P(dir), 0o750); err != nil {
			return err
		}
		m.webGroupOwn(m.env.P(dir))
		for _, f := range []string{"fullchain.pem", "privkey.pem"} {
			data, err := os.ReadFile(m.env.P(filepath.Join(cfg.LetsEncryptDir, "live", h, f)))
			if err != nil {
				return fmt.Errorf("read the certificate for %s: %w", h, err)
			}
			if err := writeFileIfChanged(m.env.P(filepath.Join(dir, f)), string(data), 0o640); err != nil {
				return err
			}
			m.webGroupOwn(m.env.P(filepath.Join(dir, f)))
		}
		fmt.Fprintf(&b, `
https://%s:%d {
	tls %s/fullchain.pem %s/privkey.pem
	request_body {
		max_size %dMB
	}
	header X-Frame-Options SAMEORIGIN
	header X-Content-Type-Options nosniff
	@ike_hidden path_regexp ike_hidden `+"`^/(config|temp|logs|SQL|bin|installer)/|/\\.`"+`
	respond @ike_hidden 403
	root * %s
	php_fastcgi unix/%s
	file_server
}
`, h, port, dir, dir, bodyMB, root, sock)
	}
	path := m.env.P(filepath.Join(cfg.CaddySitesDir, "00-ike-mail.caddy"))
	old, oldErr := os.ReadFile(path)
	if oldErr == nil && string(old) == b.String() {
		return nil
	}
	if err := writeFileIfChanged(path, b.String(), 0o640); err != nil {
		return err
	}
	m.webGroupOwn(path)
	if _, err := m.env.Run.Run(ctx, "caddy", "validate", "--adapter", "caddyfile", "--config", cfg.CaddyConfig); err != nil {
		if oldErr == nil {
			_ = os.WriteFile(path, old, 0o640)
		} else {
			_ = os.Remove(path)
		}
		return fmt.Errorf("Caddy rejected the webmail configuration: %w", err)
	}
	_, _ = m.env.Run.Run(ctx, "systemctl", "reload", "caddy")
	return nil
}

// writeOLSWebmail serves webmail on its own HTTPS listener, one virtual
// host per mail.<domain> so each uses its own certificate. Certificate
// checks on port 80 go to the catch-all host the sites manager maps.
func (m *Manager) writeOLSWebmail(ctx context.Context, tlsHosts []string, port int, root, sock string) error {
	cfg := m.env.Cfg
	dir := cfg.OLSSitesDir
	var idx strings.Builder
	idx.WriteString("# Written by Ike: webmail for mail.<domain> names.\n")
	var maps []string
	for _, h := range tlsHosts {
		name := "ike-webmail-" + h
		conf := filepath.Join(dir, "webmail-"+h+".conf")
		body := fmt.Sprintf(`# Written by Ike.
docRoot                   %s/
index  {
  useServer               0
  indexFiles              index.php
}
extprocessor ike_webmail {
  type                    fcgi
  address                 uds:/%s
  maxConns                8
  initTimeout             60
  retryTimeout            0
  persistConn             1
  autoStart               0
}
scripthandler  {
  add                     fcgi:ike_webmail php
}
vhssl  {
  keyFile                 %s/live/%s/privkey.pem
  certFile                %s/live/%s/fullchain.pem
  certChain               1
}
context / {
  location                %s/
  allowBrowse             1
  extraHeaders            <<<END_extraHeaders
set X-Frame-Options SAMEORIGIN
set X-Content-Type-Options nosniff
  END_extraHeaders
}
rewrite  {
  enable                  1
  rules                   <<<END_rules
RewriteRule ^/?(config|temp|logs|SQL|bin|installer)/ - [F,L]
RewriteRule (^|/)\. - [F,L]
  END_rules
}
`, root, sock, cfg.LetsEncryptDir, h, cfg.LetsEncryptDir, h, root)
		if err := writeFileIfChanged(m.env.P(conf), body, 0o640); err != nil {
			return err
		}
		m.webGroupOwn(m.env.P(conf))
		fmt.Fprintf(&idx, "\nvirtualhost %s {\n  vhRoot                  %s/\n  configFile              %s\n  allowSymbolLink         1\n  enableScript            1\n  restrained              1\n}\n", name, root, conf)
		maps = append(maps, fmt.Sprintf("  map                     %s %s", name, h))
	}
	if len(tlsHosts) > 0 {
		fmt.Fprintf(&idx, "\nlistener ike-webmail {\n  address                 *:%d\n  secure                  1\n  keyFile                 %s/default.key\n  certFile                %s/default.crt\n%s\n}\n",
			port, dir, dir, strings.Join(maps, "\n"))
	}
	path := m.env.P(filepath.Join(dir, "webmail.conf"))
	old, oldErr := os.ReadFile(path)
	if oldErr == nil && string(old) == idx.String() {
		return nil
	}
	if err := writeFileIfChanged(path, idx.String(), 0o640); err != nil {
		return err
	}
	m.webGroupOwn(path)
	bin := filepath.Join(cfg.OLSDir, "bin")
	if _, err := m.env.Run.Run(ctx, filepath.Join(bin, "openlitespeed"), "-t"); err != nil {
		if oldErr == nil {
			_ = os.WriteFile(path, old, 0o640)
		}
		return fmt.Errorf("OpenLiteSpeed rejected the webmail configuration: %w", err)
	}
	_, _ = m.env.Run.Run(ctx, filepath.Join(bin, "lswsctrl"), "restart")
	return nil
}
