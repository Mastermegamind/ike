package mail

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

// ---- Webmail (Roundcube on its own port, one server block per mail domain) ----

func (m *Manager) webmailNames(all []Domain) (names, tlsHosts []string) {
	for _, d := range all {
		if !d.Enabled {
			continue
		}
		names = append(names, "mail."+d.Domain)
		if d.TLS {
			tlsHosts = append(tlsHosts, "mail."+d.Domain)
		}
	}
	return
}

func (m *Manager) writeWebmailVhost(ctx context.Context, all []Domain) error {
	if m.env.Cfg.Webmail == "" {
		return nil
	}
	set, _ := m.Settings()
	names, tlsHosts := m.webmailNames(all)
	le := m.env.Cfg.LetsEncryptDir
	root := m.env.Cfg.WebmailDir
	sock := "/run/php/ike-webmail.sock"
	if m.env.Cfg.WebStack == "apache" {
		var b strings.Builder
		b.WriteString("# Written by Ike: webmail and certificate checks for mail.<domain> names.\n")
		b.WriteString("Listen " + strconv.Itoa(set.WebmailPort) + "\n")
		if len(names) > 0 {
			fmt.Fprintf(&b, "<VirtualHost *:80>\n  ServerName %s\n  ServerAlias %s\n  Alias /.well-known/acme-challenge/ %s/.well-known/acme-challenge/\n  RedirectMatch 302 ^/(?!\\.well-known)(.*)$ https://%%{HTTP_HOST}:%d/$1\n</VirtualHost>\n",
				names[0], strings.Join(names, " "), m.env.Cfg.AcmeDir, set.WebmailPort)
		}
		for _, h := range tlsHosts {
			fmt.Fprintf(&b, "<VirtualHost *:%d>\n  ServerName %s\n  DocumentRoot %s\n  SSLEngine on\n  SSLCertificateFile %s/live/%s/fullchain.pem\n  SSLCertificateKeyFile %s/live/%s/privkey.pem\n  <FilesMatch \\.php$>\n    SetHandler \"proxy:unix:%s|fcgi://localhost\"\n  </FilesMatch>\n  <Directory %s>\n    Require all granted\n  </Directory>\n  <DirectoryMatch \"^%s/(config|temp|logs|SQL|bin)\">\n    Require all denied\n  </DirectoryMatch>\n</VirtualHost>\n",
				set.WebmailPort, h, root, le, h, le, h, sock, root, root)
		}
		path := m.env.P(filepath.Join(m.env.Cfg.ApacheSitesDir, "00-ike-mail.conf"))
		if err := writeFileIfChanged(path, b.String(), 0o644); err != nil {
			return err
		}
		if _, err := m.env.Run.Run(ctx, "apache2ctl", "configtest"); err != nil {
			return fmt.Errorf("Apache rejected the webmail configuration: %w", err)
		}
		_, _ = m.env.Run.Run(ctx, "systemctl", "reload", "apache2")
		return nil
	}
	var b strings.Builder
	b.WriteString("# Written by Ike: webmail and certificate checks for mail.<domain> names.\n")
	if len(names) > 0 {
		fmt.Fprintf(&b, "server {\n  listen 80;\n  listen [::]:80;\n  server_name %s;\n  location ^~ /.well-known/acme-challenge/ { root %s; }\n  location / { return 302 https://$host:%d$request_uri; }\n}\n",
			strings.Join(names, " "), m.env.Cfg.AcmeDir, set.WebmailPort)
	}
	for _, h := range tlsHosts {
		fmt.Fprintf(&b, `server {
  listen %d ssl;
  listen [::]:%d ssl;
  http2 on;
  server_name %s;
  ssl_certificate %s/live/%s/fullchain.pem;
  ssl_certificate_key %s/live/%s/privkey.pem;
  root %s;
  index index.php;
  client_max_body_size %dm;
  add_header X-Frame-Options SAMEORIGIN always;
  add_header X-Content-Type-Options nosniff always;
  location ~ ^/(config|temp|logs|SQL|bin|installer)/ { deny all; }
  location ~ /\. { deny all; }
  location ~ \.php$ {
    try_files $uri =404;
    include fastcgi_params;
    fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;
    fastcgi_param HTTPS on;
    fastcgi_pass unix:%s;
  }
}
`, set.WebmailPort, set.WebmailPort, h, le, h, le, h, root, set.MaxMessageMB+5, sock)
	}
	if !m.web(ctx) {
		// http2 on needs nginx 1.25.1+; older nginx takes it on the listen line
		s := strings.ReplaceAll(b.String(), "  http2 on;\n", "")
		s = strings.ReplaceAll(s, " ssl;\n", " ssl http2;\n")
		b.Reset()
		b.WriteString(s)
	}
	path := m.env.P(filepath.Join(m.env.Cfg.NginxSitesDir, "00-ike-mail.conf"))
	old, _ := os.ReadFile(path)
	if string(old) == b.String() {
		return nil
	}
	if err := writeFileIfChanged(path, b.String(), 0o644); err != nil {
		return err
	}
	if _, err := m.env.Run.Run(ctx, "nginx", "-t"); err != nil {
		_ = os.WriteFile(path, old, 0o644)
		return fmt.Errorf("Nginx rejected the webmail configuration: %w", err)
	}
	_, _ = m.env.Run.Run(ctx, "systemctl", "reload", "nginx")
	return nil
}

// NginxH2Directive reports whether nginx takes "http2 on;" (set by the API
// from the sites manager's feature detection).
var NginxH2Directive = func() bool { return true }

func (m *Manager) web(context.Context) bool { return NginxH2Directive() }

// IssueTLS gets a Let's Encrypt certificate for mail.<domain> so mail clients
// connect without warnings. Runs as a job.
func (m *Manager) IssueTLS(ctx context.Context, account, domain string, dnsValidation bool, log func(string, ...any)) (*PublicDomain, error) {
	d, err := m.get(account, domain)
	if err != nil {
		return nil, err
	}
	host := "mail." + d.Domain
	if !dnsValidation && !m.env.Cfg.DryRun {
		addrs, err := net.DefaultResolver.LookupHost(ctx, host)
		if err != nil || len(addrs) == 0 {
			return nil, fmt.Errorf("%s has no DNS record yet. Add an A record pointing at this server (the email DNS records do this), then try again", host)
		}
		log("%s points to %s", host, strings.Join(addrs, ", "))
	}
	if err := os.MkdirAll(m.env.P(filepath.Join(m.env.Cfg.AcmeDir, ".well-known", "acme-challenge")), 0o755); err != nil {
		return nil, err
	}
	reload := "systemctl reload postfix dovecot"
	if m.env.Cfg.Webmail != "" {
		if m.env.Cfg.WebStack == "apache" {
			reload += " apache2"
		} else {
			reload += " nginx"
		}
	}
	args := []string{"certonly", "--webroot", "-w", m.env.Cfg.AcmeDir}
	if dnsValidation {
		args = []string{"certonly", "--manual", "--preferred-challenges", "dns",
			"--manual-auth-hook", "/usr/local/bin/ike-agent dns-hook auth", "--manual-cleanup-hook", "/usr/local/bin/ike-agent dns-hook cleanup"}
	}
	args = append(args, "--cert-name", host, "-d", host, "--non-interactive", "--agree-tos", "--keep-until-expiring", "--deploy-hook", reload)
	if m.env.Cfg.AdminEmail != "" {
		args = append(args, "-m", m.env.Cfg.AdminEmail)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	log("Asking Let's Encrypt for a certificate for %s", host)
	if _, err := m.env.Run.Exec(ctx, runner.Command{Name: "certbot", Args: args, Output: lineWriter(log), Timeout: 5 * time.Minute}); err != nil {
		return nil, fmt.Errorf("Let's Encrypt didn't issue the certificate: %w", err)
	}
	p, err := m.change(ctx, account, domain, func(d *Domain) error { d.TLS = true; return nil })
	if err == nil {
		log("Mail clients can now connect to %s with encryption", host)
	}
	return p, err
}

type lw struct{ log func(string, ...any) }

func (l lw) Write(p []byte) (int, error) {
	for _, s := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		if s != "" {
			l.log("%s", s)
		}
	}
	return len(p), nil
}
func lineWriter(log func(string, ...any)) io.Writer { return lw{log} }

// ---- settings: relay, limits, spam ----

var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)

// RelayPresets fill host and port for common providers.
var RelayPresets = map[string]struct {
	Host string
	Port int
}{
	"brevo":     {"smtp-relay.brevo.com", 587},
	"mailgun":   {"smtp.mailgun.org", 587},
	"ses":       {"email-smtp.us-east-1.amazonaws.com", 587},
	"sendgrid":  {"smtp.sendgrid.net", 587},
	"postmark":  {"smtp.postmarkapp.com", 587},
	"resend":    {"smtp.resend.com", 587},
	"zeptomail": {"smtp.zeptomail.com", 587},
}

// SettingsUpdate changes server mail settings. Empty relay password keeps it.
type SettingsUpdate struct {
	Settings
	RelayPassword string `json:"relay_password"`
}

func (m *Manager) UpdateSettings(ctx context.Context, u SettingsUpdate) (PublicSettings, error) {
	if err := m.need(); err != nil {
		return PublicSettings{}, err
	}
	s := u.Settings
	r := &s.Relay
	switch r.Provider {
	case "", "direct":
		r.Provider, r.Host, r.Port, r.Username = "direct", "", 0, ""
	case "custom":
	default:
		p, ok := RelayPresets[r.Provider]
		if !ok {
			return PublicSettings{}, fmt.Errorf("unknown relay provider %q", r.Provider)
		}
		if r.Host == "" {
			r.Host = p.Host
		}
		if r.Port == 0 {
			r.Port = p.Port
		}
	}
	if r.Provider != "direct" {
		r.Host = strings.ToLower(strings.TrimSpace(r.Host))
		if !hostRe.MatchString(r.Host) || !strings.Contains(r.Host, ".") {
			return PublicSettings{}, fmt.Errorf("relay host %q is not a host name", r.Host)
		}
		if r.Port != 25 && r.Port != 465 && r.Port != 587 && r.Port != 2525 {
			return PublicSettings{}, fmt.Errorf("relay port is usually 587, 465, 2525 or 25")
		}
		if r.Username == "" || len(r.Username) > 200 || strings.ContainsAny(r.Username, " \t\r\n:") {
			return PublicSettings{}, fmt.Errorf("relay username is required and can't contain spaces or colons")
		}
	}
	if u.RelayPassword != "" && (len(u.RelayPassword) > 300 || strings.ContainsAny(u.RelayPassword, "\r\n")) {
		return PublicSettings{}, fmt.Errorf("relay password is too long or has line breaks")
	}
	switch {
	case s.MaxMessageMB < 1 || s.MaxMessageMB > 200:
		return PublicSettings{}, fmt.Errorf("largest message must be 1-200 MB")
	case s.HourlySendLimit < 10 || s.HourlySendLimit > 100000:
		return PublicSettings{}, fmt.Errorf("hourly send limit must be 10-100000")
	case s.SpamThreshold < 1 || s.SpamThreshold > 50 || s.RejectThreshold <= s.SpamThreshold || s.RejectThreshold > 100:
		return PublicSettings{}, fmt.Errorf("spam score must be lower than the reject score (1-100)")
	case s.WebmailPort < 1024 || s.WebmailPort > 65535:
		return PublicSettings{}, fmt.Errorf("webmail port must be 1024-65535")
	case s.DefaultQuotaMB < 0:
		return PublicSettings{}, fmt.Errorf("default quota can't be negative")
	}
	if s.DMARCPolicy != "none" && s.DMARCPolicy != "quarantine" && s.DMARCPolicy != "reject" {
		return PublicSettings{}, fmt.Errorf("DMARC policy is none, quarantine or reject")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	saved, err := m.set.Update(func(cur *Settings) error {
		pw := cur.Relay.Password
		if u.RelayPassword != "" {
			sealed, err := m.keys.Seal(u.RelayPassword)
			if err != nil {
				return err
			}
			pw = sealed
		}
		if s.Relay.Provider == "direct" {
			pw = ""
		}
		*cur = s
		cur.Relay.Password = pw
		return nil
	})
	if err != nil {
		return PublicSettings{}, err
	}
	if err := m.applySettings(ctx, saved); err != nil {
		return PublicSettings{}, err
	}
	return m.PublicSettings()
}

func (m *Manager) applySettings(ctx context.Context, s Settings) error {
	pc := []string{"-e", "message_size_limit=" + strconv.Itoa(s.MaxMessageMB*1024*1024)}
	if s.Relay.Provider == "direct" {
		pc = append(pc, "relayhost=", "smtp_sasl_auth_enable=no", "smtp_tls_security_level=may")
		if err := writeFileIfChanged(m.conf("relay_password"), "", 0o600); err != nil {
			return err
		}
	} else {
		pw, err := m.keys.Unseal(s.Relay.Password)
		if err != nil {
			return err
		}
		host := fmt.Sprintf("[%s]:%d", s.Relay.Host, s.Relay.Port)
		if err := writeFileIfChanged(m.conf("relay_password"), host+" "+s.Relay.Username+":"+pw+"\n", 0o600); err != nil {
			return err
		}
		level := "encrypt"
		wrapper := "no"
		if s.Relay.Port == 465 {
			wrapper = "yes"
		}
		pc = append(pc, "relayhost="+host, "smtp_sasl_auth_enable=yes",
			"smtp_sasl_password_maps=texthash:"+filepath.Join(m.env.Cfg.MailConfDir, "relay_password"),
			"smtp_sasl_security_options=noanonymous", "smtp_sasl_tls_security_options=noanonymous",
			"smtp_tls_security_level="+level, "smtp_tls_wrappermode="+wrapper)
	}
	if _, err := m.env.Run.Run(ctx, "postconf", pc...); err != nil {
		return err
	}
	if m.env.Cfg.SpamFilter == "rspamd" {
		files := map[string]string{
			"actions.conf": fmt.Sprintf("# Written by Ike\nreject = %d;\nadd_header = %d;\ngreylist = %d;\n",
				s.RejectThreshold, s.SpamThreshold, max(1, s.SpamThreshold-2)),
			"greylist.conf": fmt.Sprintf("# Written by Ike\nenabled = %v;\n", s.Greylisting),
			"ratelimit.conf": fmt.Sprintf("# Written by Ike: outgoing limit per signed-in mailbox\nrates {\n  ike_user {\n    selector = \"user.lower\";\n    bucket = { burst = %d; rate = \"%d / 1h\"; }\n  }\n}\n",
				s.HourlySendLimit, s.HourlySendLimit),
			"dkim_signing.conf": "# Written by Ike\nenabled = true;\nsign_authenticated = true;\nsign_local = true;\nuse_domain = \"header\";\nallow_username_mismatch = true;\n" +
				"selector_map = \"" + filepath.Join(m.env.Cfg.MailConfDir, "dkim_selectors") + "\";\n" +
				"path = \"" + m.env.Cfg.DKIMDir + "/$domain.$selector.key\";\n",
		}
		if m.env.Cfg.Antivirus == "clamav" {
			files["antivirus.conf"] = fmt.Sprintf("# Written by Ike\nclamav {\n  scan_mime_parts = true;\n  symbol = \"CLAM_VIRUS\";\n  type = \"clamav\";\n  action = \"reject\";\n  servers = \"/run/clamav/clamd.ctl\";\n  max_size = %d;\n}\n", s.MaxMessageMB*1024*1024)
			if !s.Antivirus {
				files["antivirus.conf"] = "# Written by Ike: antivirus turned off in the panel\n"
			}
		}
		for name, body := range files {
			if err := writeFileIfChanged(m.env.P(filepath.Join("/etc/rspamd/local.d", name)), body, 0o644); err != nil {
				return err
			}
		}
		if _, err := m.env.Run.Run(ctx, "rspamadm", "configtest"); err != nil {
			return fmt.Errorf("Rspamd rejected the settings: %w", err)
		}
		_, _ = m.env.Run.Run(ctx, "systemctl", "reload", "rspamd")
	}
	_, err := m.env.Run.Run(ctx, "postfix", "reload")
	return err
}

// ---- DNS records for a domain ----

// Records lists what a domain's DNS needs for mail through this server.
func (m *Manager) Records(domain string) ([]DNSRecord, error) {
	d, err := m.load(domain)
	if err != nil {
		return nil, err
	}
	set, _ := m.Settings()
	ip4, ip6 := m.env.Cfg.PublicIPv4, m.env.Cfg.PublicIPv6
	var recs []DNSRecord
	spf := "v=spf1 mx a"
	if ip4 != "" {
		recs = append(recs, DNSRecord{Name: "mail", Type: "A", Content: ip4})
		spf += " ip4:" + ip4
	}
	if ip6 != "" {
		recs = append(recs, DNSRecord{Name: "mail", Type: "AAAA", Content: ip6})
		spf += " ip6:" + ip6
	}
	if set.Relay.Provider != "direct" {
		if inc := relaySPF[set.Relay.Provider]; inc != "" {
			spf += " include:" + inc
		}
	}
	recs = append(recs,
		DNSRecord{Name: "@", Type: "MX", Content: "mail", Priority: 10},
		DNSRecord{Name: "@", Type: "TXT", Content: spf + " ~all"},
		DNSRecord{Name: "_dmarc", Type: "TXT", Content: "v=DMARC1; p=" + set.DMARCPolicy + "; rua=mailto:postmaster@" + d.Domain + "; adkim=r; aspf=r"},
		DNSRecord{Name: "autoconfig", Type: "CNAME", Content: "mail"},
		DNSRecord{Name: "autodiscover", Type: "CNAME", Content: "mail"},
		DNSRecord{Name: "_submissions._tcp", Type: "SRV", Content: "mail", Port: 465, Weight: 1},
		DNSRecord{Name: "_imaps._tcp", Type: "SRV", Content: "mail", Port: 993, Weight: 1},
		DNSRecord{Name: "_pop3s._tcp", Type: "SRV", Content: "mail", Port: 995, Weight: 1, Priority: 10},
	)
	if name, val, err := m.DKIMRecord(d.Domain); err == nil {
		recs = append(recs, DNSRecord{Name: name, Type: "TXT", Content: val})
	}
	return recs, nil
}

var relaySPF = map[string]string{
	"brevo": "spf.brevo.com", "mailgun": "mailgun.org", "ses": "amazonses.com", "sendgrid": "sendgrid.net",
	"postmark": "spf.mtasv.net", "resend": "amazonses.com", "zeptomail": "zeptomail.net",
}

// PublishDNS adds the mail records to the domain's zone when it is hosted
// here. It reports false when there is no zone (the user adds them by hand).
func (m *Manager) PublishDNS(ctx context.Context, account, domain string) (bool, error) {
	if _, err := m.get(account, domain); err != nil {
		return false, err
	}
	if m.Zone == nil {
		return false, nil
	}
	recs, err := m.Records(domain)
	if err != nil {
		return false, err
	}
	return m.Zone.SetManagedTXT(ctx, "mail", domain, recs)
}

// ---- queue ----

// QueuedMessage is one message waiting in Postfix's queue.
type QueuedMessage struct {
	ID         string    `json:"id"`
	Queue      string    `json:"queue"` // active | deferred | hold | incoming
	Arrival    time.Time `json:"arrival"`
	Size       int       `json:"size"`
	Sender     string    `json:"sender"`
	Recipients []struct {
		Address string `json:"address"`
		Reason  string `json:"delay_reason,omitempty"`
	} `json:"recipients"`
}

// Queue lists the mail queue; account limits it to that account's domains.
func (m *Manager) Queue(ctx context.Context, account string) ([]QueuedMessage, error) {
	if err := m.need(); err != nil {
		return nil, err
	}
	out, err := m.env.Run.Run(ctx, "postqueue", "-j")
	if err != nil {
		return nil, err
	}
	mine := m.domainSet(account)
	list := []QueuedMessage{}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var raw struct {
			Queue   string `json:"queue_name"`
			ID      string `json:"queue_id"`
			Arrival int64  `json:"arrival_time"`
			Size    int    `json:"message_size"`
			Sender  string `json:"sender"`
			Rcpt    []struct {
				Address string `json:"address"`
				Reason  string `json:"delay_reason"`
			} `json:"recipients"`
		}
		if json.Unmarshal(sc.Bytes(), &raw) != nil {
			continue
		}
		q := QueuedMessage{ID: raw.ID, Queue: raw.Queue, Arrival: time.Unix(raw.Arrival, 0).UTC(), Size: raw.Size, Sender: raw.Sender}
		for _, r := range raw.Rcpt {
			q.Recipients = append(q.Recipients, struct {
				Address string `json:"address"`
				Reason  string `json:"delay_reason,omitempty"`
			}{r.Address, r.Reason})
		}
		if mine != nil && !mine[domainOf(q.Sender)] {
			keep := false
			for _, r := range q.Recipients {
				if mine[domainOf(r.Address)] {
					keep = true
				}
			}
			if !keep {
				continue
			}
		}
		list = append(list, q)
	}
	return list, nil
}

var queueIDRe = regexp.MustCompile(`^[0-9A-Za-z]{6,20}$`)

// QueueAction acts on queued messages: flush (retry all), retry, hold,
// release or delete one message. Account owners may only touch messages
// from or to their domains.
func (m *Manager) QueueAction(ctx context.Context, account, action, id string) error {
	if err := m.need(); err != nil {
		return err
	}
	if action == "flush" {
		if account != "" {
			return fmt.Errorf("only the server admin can retry the whole queue")
		}
		_, err := m.env.Run.Run(ctx, "postqueue", "-f")
		return err
	}
	if !queueIDRe.MatchString(id) {
		return fmt.Errorf("invalid queue ID")
	}
	if account != "" {
		q, err := m.Queue(ctx, account)
		if err != nil {
			return err
		}
		found := false
		for _, x := range q {
			if x.ID == id {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("message %s %w", id, kit.ErrNotFound)
		}
	}
	var args []string
	switch action {
	case "retry":
		_, err := m.env.Run.Run(ctx, "postqueue", "-i", id)
		return err
	case "hold":
		args = []string{"-h", id}
	case "release":
		args = []string{"-H", id}
	case "delete":
		args = []string{"-d", id}
	default:
		return fmt.Errorf("action must be flush, retry, hold, release or delete")
	}
	_, err := m.env.Run.Run(ctx, "postsuper", args...)
	return err
}

func domainOf(addr string) string {
	_, d, _ := strings.Cut(strings.ToLower(addr), "@")
	return d
}

func (m *Manager) domainSet(account string) map[string]bool {
	if account == "" {
		return nil
	}
	set := map[string]bool{}
	all, _ := m.AllDomains()
	for _, d := range all {
		if d.Account == account {
			set[d.Domain] = true
		}
	}
	return set
}

// ---- delivery log ----

// Delivery is one delivery attempt from the mail log.
type Delivery struct {
	Time    string `json:"time"`
	QueueID string `json:"queue_id"`
	From    string `json:"from,omitempty"`
	To      string `json:"to"`
	Status  string `json:"status"` // sent | deferred | bounced | rejected
	Relay   string `json:"relay,omitempty"`
	Detail  string `json:"detail"`
}

var (
	deliveryRe = regexp.MustCompile(`^(\S+\s+\S+\s+\S+|\S+T\S+)\s+\S+\s+postfix/\S+\[\d+\]:\s+([0-9A-Za-z]+):\s+to=<([^>]*)>.*?status=(\w+)\s*(.*)$`)
	relayRe    = regexp.MustCompile(`relay=([^,]+),`)
	fromRe     = regexp.MustCompile(`postfix/qmgr\[\d+\]:\s+([0-9A-Za-z]+):\s+from=<([^>]*)>`)
	rejectRe   = regexp.MustCompile(`^(\S+\s+\S+\s+\S+|\S+T\S+)\s+\S+\s+postfix/\S+\[\d+\]:\s+NOQUEUE:\s+reject:.*?:\s+(.*?);\s+from=<([^>]*)>\s+to=<([^>]*)>`)
)

// Deliveries reads the newest delivery results from the mail log.
// search filters by address; account limits to that account's domains.
func (m *Manager) Deliveries(account, search string, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	search = strings.ToLower(search)
	lines, err := tailFile(m.env.P("/var/log/mail.log"), 4<<20)
	if err != nil {
		return nil, err
	}
	mine := m.domainSet(account)
	from := map[string]string{}
	var out []Delivery
	for _, l := range lines {
		if mm := fromRe.FindStringSubmatch(l); mm != nil {
			from[mm[1]] = mm[2]
			continue
		}
		var d Delivery
		if mm := deliveryRe.FindStringSubmatch(l); mm != nil {
			d = Delivery{Time: mm[1], QueueID: mm[2], To: mm[3], Status: mm[4], Detail: strings.Trim(mm[5], "() ")}
			if r := relayRe.FindStringSubmatch(l); r != nil {
				d.Relay = r[1]
			}
			d.From = from[d.QueueID]
		} else if mm := rejectRe.FindStringSubmatch(l); mm != nil {
			d = Delivery{Time: mm[1], QueueID: "NOQUEUE", Status: "rejected", Detail: mm[2], From: mm[3], To: mm[4]}
		} else {
			continue
		}
		if mine != nil && !mine[domainOf(d.To)] && !mine[domainOf(d.From)] {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(d.To+" "+d.From+" "+d.QueueID), search) {
			continue
		}
		out = append(out, d)
	}
	// newest first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if len(out) > limit {
		out = out[:limit]
	}
	if out == nil {
		out = []Delivery{}
	}
	return out, nil
}

func tailFile(path string, max int64) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, _ := f.Stat()
	if st.Size() > max {
		_, _ = f.Seek(st.Size()-max, io.SeekStart)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(b), "\n")
	if st.Size() > max && len(lines) > 0 {
		lines = lines[1:] // partial first line
	}
	return lines, nil
}

// ---- usage ----

// RefreshUsage asks Dovecot how full each mailbox is.
func (m *Manager) RefreshUsage(ctx context.Context, account, domain string) (*PublicDomain, error) {
	d, err := m.get(account, domain)
	if err != nil {
		return nil, err
	}
	usage := map[string]*MailboxUsage{}
	for _, mb := range d.Mailboxes {
		out, err := m.env.Run.Run(ctx, "doveadm", "-f", "tab", "quota", "get", "-u", mb.Local+"@"+d.Domain)
		if err != nil {
			continue
		}
		u := &MailboxUsage{At: kit.Now()}
		for _, l := range strings.Split(string(out), "\n")[1:] {
			f := strings.Split(l, "\t")
			if len(f) < 3 {
				continue
			}
			v, _ := strconv.ParseFloat(f[2], 64)
			switch strings.ToUpper(f[1]) {
			case "STORAGE":
				u.UsedMB = float64(int(v/1024*10)) / 10 // KiB → MB
			case "MESSAGE":
				u.Messages = int(v)
			}
		}
		usage[mb.Local] = u
	}
	return m.change(ctx, account, domain, func(d *Domain) error {
		for i := range d.Mailboxes {
			if u, ok := usage[d.Mailboxes[i].Local]; ok {
				d.Mailboxes[i].Usage = u
			}
		}
		return nil
	})
}

// ---- mailbox export and import ----

// ExportMailbox streams a mailbox's Maildir as a .tar.gz.
func (m *Manager) ExportMailbox(ctx context.Context, account, domain, local string, w io.Writer) error {
	d, err := m.get(account, domain)
	if err != nil {
		return err
	}
	if !d.hasMailbox(local) {
		return fmt.Errorf("mailbox %s@%s %w", local, d.Domain, kit.ErrNotFound)
	}
	_, err = m.env.Run.Exec(ctx, runner.Command{Name: "tar", Args: []string{"-C", filepath.Join(m.env.Cfg.MailDir, d.Domain), "-czf", "-", "--", local},
		Stdout: w, Timeout: 2 * time.Hour})
	return err
}

// ImportMailbox restores messages from a .tar.gz made by ExportMailbox (or
// any archive whose top folder is a Maildir). Existing messages are kept;
// the archive is unpacked as the vmail user so it can't write elsewhere.
func (m *Manager) ImportMailbox(ctx context.Context, account, domain, local string, r io.Reader) error {
	d, err := m.get(account, domain)
	if err != nil {
		return err
	}
	if !d.hasMailbox(local) {
		return fmt.Errorf("mailbox %s@%s %w", local, d.Domain, kit.ErrNotFound)
	}
	dir := m.MaildirOf(d.Domain, local)
	if _, err := m.env.Run.Run(ctx, "install", "-d", "-o", "vmail", "-g", "vmail", "-m", "0700", dir); err != nil {
		return err
	}
	_, err = m.env.Run.Exec(ctx, runner.Command{Name: "runuser", Args: []string{"-u", "vmail", "--", "tar", "-xzf", "-",
		"-C", dir, "--strip-components=1", "--no-same-owner", "--no-same-permissions", "--keep-old-files", "--warning=no-existing-file"},
		StdinReader: r, Timeout: 2 * time.Hour})
	if err != nil && !strings.Contains(err.Error(), "exit status 2") {
		return err
	}
	_, _ = m.env.Run.Run(ctx, "doveadm", "force-resync", "-u", local+"@"+d.Domain, "*")
	m.env.Publish("mail.mailbox.restored", local+"@"+d.Domain, map[string]any{"account": d.Account})
	return nil
}

func (d *Domain) hasMailbox(local string) bool {
	for _, mb := range d.Mailboxes {
		if mb.Local == local {
			return true
		}
	}
	return false
}
