package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SecurityCheck is one line of the security baseline.
type SecurityCheck struct {
	ID       string `json:"id"`
	Area     string `json:"area"` // ssh | firewall | updates | network | websites | files
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Fix      string `json:"fix,omitempty"`
}

// SecurityReport is the scanner's result with a 0-100 score.
type SecurityReport struct {
	Score  int             `json:"score"`
	Checks []SecurityCheck `json:"checks"`
}

// Ports that should never listen on a public address on a hosting server.
var riskyPorts = map[string]string{
	"3306": "MariaDB/MySQL", "5432": "PostgreSQL", "6379": "Redis", "11211": "Memcached",
	"27017": "MongoDB", "9000": "PHP-FPM", "2375": "Docker API",
}

// SecurityScan checks the server against Ike's security baseline.
func (m *Manager) SecurityScan(ctx context.Context) *SecurityReport {
	r := &SecurityReport{}
	add := func(id, area, sev, title, fix string) {
		r.Checks = append(r.Checks, SecurityCheck{ID: id, Area: area, Severity: sev, Title: title, Fix: fix})
	}

	// SSH.
	if out, err := m.run.Run(ctx, "sshd", "-T"); err == nil && len(out) > 0 {
		cfg := map[string]string{}
		for _, l := range strings.Split(string(out), "\n") {
			if k, v, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
				cfg[k] = v
			}
		}
		switch cfg["permitrootlogin"] {
		case "yes":
			add("ssh-root", "ssh", "bad", "Root can log in over SSH with a password", "Set PermitRootLogin prohibit-password and use SSH keys.")
		default:
			add("ssh-root", "ssh", "ok", "Root can only log in with an SSH key (or not at all)", "")
		}
		if cfg["passwordauthentication"] == "yes" {
			add("ssh-passwords", "ssh", "warn", "SSH accepts passwords", "Turn off PasswordAuthentication once everyone uses keys. fail2ban limits guessing meanwhile.")
		} else {
			add("ssh-passwords", "ssh", "ok", "SSH only accepts keys", "")
		}
	}

	// Firewall and brute-force protection.
	if out, _ := m.run.Run(ctx, "ufw", "status"); strings.Contains(string(out), "Status: active") {
		add("firewall", "firewall", "ok", "The firewall is on", "")
	} else if !m.cfg.DryRun {
		add("firewall", "firewall", "bad", "The firewall is off", "Run: ufw --force enable (SSH stays open).")
	}
	if m.serviceActive(ctx, "fail2ban") {
		add("fail2ban", "firewall", "ok", "Repeated failed logins are banned automatically", "")
	} else {
		add("fail2ban", "firewall", "bad", "fail2ban isn't running", "Start fail2ban from Server software.")
	}

	// Updates.
	if out, err := m.run.Run(ctx, "/usr/lib/update-notifier/apt-check"); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), ";")
		if len(parts) == 2 {
			if sec, _ := strconv.Atoi(parts[1]); sec > 0 {
				add("updates", "updates", "bad", fmt.Sprintf("%d security updates are waiting", sec), "Install security updates on the Updates page.")
			} else {
				add("updates", "updates", "ok", "No security updates are waiting", "")
			}
		}
	}
	if _, err := os.Stat(m.p("/var/run/reboot-required")); err == nil {
		add("reboot", "updates", "warn", "A reboot is needed to finish installing updates", "Schedule a reboot at a quiet time.")
	}

	// Services on public addresses.
	if out, err := m.run.Run(ctx, "ss", "-H", "-lntu"); err == nil {
		exposed := map[string]bool{}
		for _, l := range strings.Split(string(out), "\n") {
			f := strings.Fields(l)
			if len(f) < 5 {
				continue
			}
			local := f[4]
			i := strings.LastIndex(local, ":")
			if i < 0 {
				continue
			}
			host, port := local[:i], local[i+1:]
			if name, risky := riskyPorts[port]; risky && (host == "0.0.0.0" || host == "*" || host == "[::]") && !exposed[port] {
				exposed[port] = true
				add("exposed-"+port, "network", "bad", fmt.Sprintf("%s is listening on all addresses (port %s)", name, port),
					"Bind it to 127.0.0.1 so only this server can reach it.")
			}
		}
		if len(exposed) == 0 {
			add("exposed", "network", "ok", "Databases and caches only listen on this server", "")
		}
	}

	// Websites.
	sites, _ := m.List()
	eol, worldReadable := []string{}, []string{}
	for i := range sites {
		s := &sites[i]
		if s.Runtime == RuntimePHP && s.Version < "8.2" {
			eol = append(eol, s.Domain+" (PHP "+s.Version+")")
		}
		L := m.layout(s)
		for _, f := range []string{filepath.Join(L.DocRoot, "wp-config.php"), filepath.Join(L.SiteDir, ".env")} {
			if fi, err := os.Lstat(m.p(f)); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o004 != 0 {
				worldReadable = append(worldReadable, s.Domain)
				break
			}
		}
	}
	if len(eol) > 0 {
		add("php-eol", "websites", "warn", fmt.Sprintf("%d websites use PHP versions without security fixes", len(eol)),
			"Move them to PHP 8.3 or newer: "+strings.Join(eol, ", "))
	} else if len(sites) > 0 {
		add("php-eol", "websites", "ok", "Every PHP website uses a supported PHP version", "")
	}
	if len(sites) > 0 {
		add("https", "websites", "warn", fmt.Sprintf("%d websites don't have HTTPS yet", len(sites)),
			"Let's Encrypt certificates arrive in the next phase; until then traffic isn't encrypted.")
	}
	if len(worldReadable) > 0 {
		add("secrets-readable", "files", "bad", "Config files with passwords can be read by other users", "Run Fix permissions on: "+strings.Join(worldReadable, ", "))
	} else if len(sites) > 0 {
		add("secrets-readable", "files", "ok", "Config files with passwords are private", "")
	}

	// Score.
	r.Score = 100
	for _, c := range r.Checks {
		switch c.Severity {
		case "bad":
			r.Score -= 15
		case "warn":
			r.Score -= 5
		}
	}
	if r.Score < 0 {
		r.Score = 0
	}
	return r
}
