package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// Finding is one plain-language result of a health check.
type Finding struct {
	Severity string `json:"severity"` // ok | info | warn | bad
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Fix      string `json:"fix,omitempty"`
	Evidence string `json:"evidence,omitempty"` // the log line that led here
}

// Diagnosis is what the health assistant says about a website.
type Diagnosis struct {
	Domain   string    `json:"domain"`
	Summary  string    `json:"summary"`
	Findings []Finding `json:"findings"`
}

// logRule turns a recognizable log line into an explanation.
type logRule struct {
	re       *regexp.Regexp
	severity string
	title    func(m []string) string
	fix      string
}

var pluginRe = regexp.MustCompile(`wp-content/(plugins|themes)/([A-Za-z0-9._-]+)/`)

var logRules = []logRule{
	{regexp.MustCompile(`PHP Fatal error:\s+(.{0,160})`), "bad", func(m []string) string {
		if p := pluginRe.FindStringSubmatch(m[0]); p != nil {
			kind := "plugin"
			if p[1] == "themes" {
				kind = "theme"
			}
			return fmt.Sprintf("PHP crashed in the WordPress %s “%s”", kind, p[2])
		}
		return "PHP crashed while building a page"
	}, "Update or disable the plugin/theme named above (rename its folder over SFTP), or switch to the PHP version it supports."},
	{regexp.MustCompile(`Allowed memory size of (\d+) bytes exhausted`), "bad", func(m []string) string {
		return "PHP ran out of memory"
	}, "A plugin or script needs more memory than the limit. Find the heavy plugin, or ask the admin to raise memory_limit for this site."},
	{regexp.MustCompile(`(?i)connect\(\) to unix:\S+php\S*fpm\S*\.sock failed|Primary script unknown`), "bad", func(m []string) string {
		if strings.Contains(m[0], "Primary script") {
			return "The web server can't find the PHP file it was asked for"
		}
		return "The web server couldn't reach PHP for this site"
	}, "Check that index.php exists in public_html. If PHP itself is down, switching the PHP version (or restarting it) usually brings it back."},
	{regexp.MustCompile(`(?i)connect\(\) failed \(111: Connection refused\) while connecting to upstream.*127\.0\.0\.1:(\d+)`), "bad", func(m []string) string {
		return "The app isn't answering on its port"
	}, "Start or restart the app, then check its logs for the reason it stopped."},
	{regexp.MustCompile(`(?i)Permission denied`), "warn", func(m []string) string {
		return "Some files can't be read because of their permissions"
	}, "Run “Fix permissions” in the website's Tools."},
	{regexp.MustCompile(`(?i)No space left on device`), "bad", func(m []string) string {
		return "The server's disk is full"
	}, "Delete old backups or logs, or add disk space at your VPS provider."},
	{regexp.MustCompile(`(?i)Too many connections|Error establishing a database connection`), "bad", func(m []string) string {
		return "The website can't connect to its database"
	}, "Check the database name, user and password in the site's config file (wp-config.php or .env), and that MariaDB is running."},
	{regexp.MustCompile(`EADDRINUSE`), "bad", func(m []string) string {
		return "The app tried to use a port that's already taken"
	}, "Make the app listen on the PORT environment variable instead of a fixed port."},
	{regexp.MustCompile(`Cannot find module '([^']+)'`), "bad", func(m []string) string {
		return fmt.Sprintf("Node.js can't find the module “%s”", m[1])
	}, "Run npm install (or pnpm/yarn install) in the app folder, then restart the app."},
	{regexp.MustCompile(`ModuleNotFoundError: No module named '([^']+)'`), "bad", func(m []string) string {
		return fmt.Sprintf("Python can't find the package “%s”", m[1])
	}, "Run .venv/bin/pip install -r requirements.txt in the app folder, then restart the app."},
	{regexp.MustCompile(`PHP Warning:\s+`), "info", func(m []string) string {
		return "PHP is logging warnings"
	}, "Warnings don't break the site, but they often appear after a PHP upgrade. Update the plugin or code named in the log."},
}

func lastLines(s string, n int) []string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

// scanLogs matches the newest occurrence of each rule.
func scanLogs(text string) []Finding {
	var out []Finding
	seen := map[int]bool{}
	lines := lastLines(text, 400)
	for i := len(lines) - 1; i >= 0; i-- {
		for ri, r := range logRules {
			if seen[ri] {
				continue
			}
			if m := r.re.FindStringSubmatch(lines[i]); m != nil {
				seen[ri] = true
				ev := strings.TrimSpace(lines[i])
				if len(ev) > 300 {
					ev = ev[:300] + "…"
				}
				out = append(out, Finding{Severity: r.severity, Title: r.title(m), Fix: r.fix, Evidence: ev})
			}
		}
	}
	return out
}

func (m *Manager) serviceActive(ctx context.Context, unit string) bool {
	if m.cfg.DryRun {
		return true
	}
	out, _ := m.run.Run(ctx, "systemctl", "is-active", unit)
	return strings.TrimSpace(string(out)) == "active"
}

// Diagnose checks a website the way a sysadmin would and explains the result.
func (m *Manager) Diagnose(ctx context.Context, domain string) (*Diagnosis, error) {
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	L := m.layout(site)
	d := &Diagnosis{Domain: domain}
	add := func(f Finding) { d.Findings = append(d.Findings, f) }

	if a, err := m.getAccount(site.Account); err == nil && a.Suspended {
		add(Finding{Severity: "bad", Title: "The account is suspended", Detail: a.SuspendReason, Fix: "Reactivate the account to bring its websites back."})
	}
	switch statusOf(site.Status) {
	case StatusMaintenance:
		add(Finding{Severity: "warn", Title: "The website is in maintenance mode", Detail: "Visitors see the “Back soon” page.", Fix: "Set its status back to Active when you're done."})
	case StatusSuspended:
		add(Finding{Severity: "bad", Title: "The website is suspended", Fix: "Set its status to Active."})
	}

	webNames := map[string]string{"nginx": "Nginx", "apache2": "Apache", "caddy": "Caddy", "lsws": "OpenLiteSpeed"}
	for _, svc := range L.webServices() {
		if !m.serviceActive(ctx, svc) {
			add(Finding{Severity: "bad", Title: webNames[svc] + " isn't running", Fix: "Restart " + webNames[svc] + " from Server software, or check the server's error log."})
		}
	}
	switch {
	case site.Runtime == RuntimePHP:
		if !m.serviceActive(ctx, "php"+site.Version+"-fpm") {
			add(Finding{Severity: "bad", Title: fmt.Sprintf("PHP %s isn't running", site.Version), Fix: "Restart PHP or switch the website to another installed version."})
		}
		if v := site.Version; v < "8.2" {
			add(Finding{Severity: "warn", Title: fmt.Sprintf("PHP %s no longer gets security fixes", v), Fix: "Test the site on PHP 8.3 or newer (a staging copy is the safe way)."})
		}
	case L.IsApp:
		if statusOf(site.Status) == StatusActive && !m.serviceActive(ctx, L.UnitName) {
			add(Finding{Severity: "bad", Title: "The app process has stopped", Fix: "Start the app. If it stops again, its logs say why."})
		}
	}

	// Empty document root.
	if !L.IsApp {
		entries, _ := os.ReadDir(m.p(L.DocRoot))
		hasIndex := false
		for _, e := range entries {
			n := strings.ToLower(e.Name())
			if strings.HasPrefix(n, "index.") {
				hasIndex = true
			}
		}
		if !hasIndex {
			add(Finding{Severity: "warn", Title: "There's no index file in public_html", Detail: "Visitors get a 403 or 404 on the home page.", Fix: "Upload your website, or install an app."})
		}
	}

	// Disk space.
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.p("/"), &st); err == nil && st.Blocks > 0 {
		freePct := float64(st.Bavail) / float64(st.Blocks) * 100
		if freePct < 5 {
			add(Finding{Severity: "bad", Title: fmt.Sprintf("The disk is almost full (%.0f%% free)", freePct), Fix: "Free up space before uploads and databases start failing."})
		} else if freePct < 15 {
			add(Finding{Severity: "warn", Title: fmt.Sprintf("Disk space is getting low (%.0f%% free)", freePct)})
		}
	}

	// Logs.
	var text strings.Builder
	for _, k := range []string{"error", "php", "apache-error"} {
		if t, err := m.SiteLog(domain, k, 400); err == nil {
			text.WriteString(t + "\n")
		}
	}
	if L.IsApp {
		if t, err := m.appLogsLocked(ctx, site, 400); err == nil {
			text.WriteString(t)
		}
	}
	d.Findings = append(d.Findings, scanLogs(text.String())...)

	// Summary in one sentence.
	worst := ""
	for _, f := range d.Findings {
		if f.Severity == "bad" {
			worst = f.Title
			break
		}
	}
	switch {
	case worst != "":
		d.Summary = worst + "."
	case len(d.Findings) > 0:
		d.Summary = "The website is up, with a few things to look at."
	default:
		d.Summary = "Everything looks healthy."
		d.Findings = []Finding{{Severity: "ok", Title: "No problems found", Detail: "Services are running and the logs show no errors."}}
	}
	return d, nil
}

func (m *Manager) appLogsLocked(ctx context.Context, site *Site, lines int) (string, error) {
	out, err := m.run.Run(ctx, "journalctl", "--unit", m.layout(site).UnitName+".service",
		"--lines", fmt.Sprint(lines), "--no-pager", "--output", "short-iso")
	return string(out), err
}

var _ = filepath.Join
