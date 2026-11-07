package sites

// Laravel helpers for PHP websites: queue workers, the scheduler and Reverb
// (Laravel's own Pusher-compatible WebSocket server), each run by systemd as
// the account's user like Ike's other apps. Reverb is served on its own
// website (ws.example.com by default), so broadcasting works without paying
// for Pusher and without touching the main website's web server config.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"

	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// Laravel is what Ike runs for a Laravel website.
type Laravel struct {
	// Path is the Laravel folder (the one with artisan), relative to the
	// website folder. Empty: the live release for Git deploys, otherwise
	// found automatically.
	Path string `json:"path,omitempty"`

	Workers    int    `json:"workers"`              // queue:work processes, 0 = none
	Connection string `json:"connection,omitempty"` // queue connection, "" = QUEUE_CONNECTION
	Queues     string `json:"queues,omitempty"`     // "high,default"
	Tries      int    `json:"tries,omitempty"`
	Timeout    int    `json:"timeout,omitempty"` // seconds per job
	Scheduler  bool   `json:"scheduler"`         // schedule:run every minute

	Reverb       bool   `json:"reverb"`
	ReverbDomain string `json:"reverb_domain,omitempty"` // default ws.<domain>
	ReverbPort   int    `json:"reverb_port,omitempty"`   // chosen by Ike
}

var (
	queuesRe  = regexp.MustCompile(`^[A-Za-z0-9_.:-]+(,[A-Za-z0-9_.:-]+)*$`)
	connRe    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	relPathRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
)

func (l *Laravel) check(domain string) error {
	if l.Workers < 0 || l.Workers > 8 {
		return fmt.Errorf("0-8 queue workers")
	}
	if l.Connection != "" && !connRe.MatchString(l.Connection) {
		return fmt.Errorf("queue connection: letters, digits, _ and -")
	}
	if l.Queues != "" && !queuesRe.MatchString(l.Queues) {
		return fmt.Errorf("queues are names separated by commas, like high,default")
	}
	if l.Tries < 0 || l.Tries > 100 || l.Timeout < 0 || l.Timeout > 86400 {
		return fmt.Errorf("tries are 0-100 and the timeout 0-86400 seconds")
	}
	if l.Path != "" && (!relPathRe.MatchString(l.Path) || strings.Contains(l.Path, "..") || strings.HasPrefix(l.Path, "/")) {
		return fmt.Errorf("the Laravel folder is a path inside the website folder, like app or public_html")
	}
	if l.Reverb {
		if l.ReverbDomain == "" {
			l.ReverbDomain = "ws." + domain
		}
		if err := validate.Domain(l.ReverbDomain); err != nil {
			return fmt.Errorf("Reverb's address: %w", err)
		}
	}
	return nil
}

// laravelRoot finds the folder with artisan (absolute system path).
func (m *Manager) laravelRoot(s *Site, L layout) (string, error) {
	if s.Laravel != nil && s.Laravel.Path != "" {
		return filepath.Join(L.SiteDir, s.Laravel.Path), nil
	}
	if s.Deploy != nil && s.Deploy.Current != "" {
		return m.currentLink(L), nil
	}
	for _, dir := range []string{L.SiteDir, filepath.Dir(L.DocRoot), L.DocRoot, filepath.Join(L.SiteDir, "app")} {
		if _, err := os.Stat(m.p(filepath.Join(dir, "artisan"))); err == nil {
			return dir, nil
		}
	}
	return "", fmt.Errorf("no Laravel app found (no artisan file); deploy it from Git or set its folder")
}

func laravelUnitPrefix(L layout) string { return "ike-laravel-" + L.PoolName }

var laravelUnitTmpl = template.Must(template.New("laravel").Parse(`# Managed by Ike. Change it in the panel (website → Laravel).
[Unit]
Description=Laravel {{.What}} for {{.Domain}}
After=network.target
StartLimitIntervalSec=300
StartLimitBurst=10

[Service]
Type={{if .Oneshot}}oneshot{{else}}simple{{end}}
User={{.User}}
Group={{.User}}
WorkingDirectory={{.Dir}}
ExecStart={{.Exec}}
Slice={{.Slice}}
{{- if not .Oneshot}}
Restart=always
RestartSec=5
KillSignal=SIGTERM
TimeoutStopSec={{.StopSecs}}
{{- end}}
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
MemoryMax=512M
{{- if not .Oneshot}}

[Install]
WantedBy=multi-user.target
{{- end}}
`))

const laravelTimer = `# Managed by Ike. Change it in the panel (website → Laravel).
[Unit]
Description=Laravel scheduler for %s

[Timer]
OnCalendar=*-*-* *:*:00
AccuracySec=1s

[Install]
WantedBy=timers.target
`

type laravelUnit struct {
	Name, What, Domain, User, Dir, Exec, Slice, Home string
	Oneshot                                          bool
	StopSecs                                         int
}

// laravelUnits are the systemd units a setting needs.
func (m *Manager) laravelUnits(s *Site, L layout, root string) []laravelUnit {
	l := s.Laravel
	php := "/usr/bin/php" + s.Version
	base := laravelUnit{Domain: s.Domain, User: s.Account, Dir: root, Slice: L.Slice, Home: L.Home}
	prefix := laravelUnitPrefix(L)
	var out []laravelUnit
	for i := 1; i <= l.Workers; i++ {
		args := []string{php, "artisan", "queue:work"}
		if l.Connection != "" {
			args = append(args, l.Connection)
		}
		if l.Queues != "" {
			args = append(args, "--queue="+l.Queues)
		}
		tries, timeout := l.Tries, l.Timeout
		if tries == 0 {
			tries = 3
		}
		if timeout == 0 {
			timeout = 60
		}
		args = append(args, "--sleep=3", "--tries="+strconv.Itoa(tries), "--timeout="+strconv.Itoa(timeout), "--max-time=3600")
		u := base
		u.Name, u.What, u.Exec, u.StopSecs = fmt.Sprintf("%s-queue-%d", prefix, i), "queue worker "+strconv.Itoa(i), strings.Join(args, " "), timeout+10
		out = append(out, u)
	}
	if l.Scheduler {
		u := base
		u.Name, u.What, u.Exec, u.Oneshot = prefix+"-schedule", "scheduler", php+" artisan schedule:run --no-interaction", true
		out = append(out, u)
	}
	if l.Reverb {
		u := base
		u.Name, u.What, u.StopSecs = prefix+"-reverb", "Reverb", 15
		u.Exec = fmt.Sprintf("%s artisan reverb:start --host=127.0.0.1 --port=%d", php, l.ReverbPort)
		out = append(out, u)
	}
	return out
}

// laravelUnitNames are every unit Ike may have made for a website.
func (m *Manager) existingLaravelUnits(L layout) []string {
	var out []string
	matches, _ := filepath.Glob(m.p(filepath.Join("/etc/systemd/system", laravelUnitPrefix(L)+"-*")))
	for _, f := range matches {
		out = append(out, filepath.Base(f))
	}
	return out
}

// SetLaravel turns workers, the scheduler and Reverb on or off for a PHP
// website (nil switches everything off). Reverb gets a website of its own
// that forwards WebSockets to it, made in the same account.
func (m *Manager) SetLaravel(ctx context.Context, domain string, l *Laravel) (*Site, string, error) {
	m.mu.Lock()
	s, err := m.Get(domain)
	if err != nil {
		m.mu.Unlock()
		return nil, "", err
	}
	if s.Runtime != RuntimePHP {
		m.mu.Unlock()
		return nil, "", fmt.Errorf("Laravel workers are for PHP websites")
	}
	if l != nil {
		if err := l.check(domain); err != nil {
			m.mu.Unlock()
			return nil, "", err
		}
		if l.Reverb {
			if s.Laravel != nil && s.Laravel.ReverbPort != 0 {
				l.ReverbPort = s.Laravel.ReverbPort
			} else if l.ReverbPort = m.nextPort(); l.ReverbPort == 0 {
				m.mu.Unlock()
				return nil, "", fmt.Errorf("no free local port for Reverb")
			}
		}
		if *l == (Laravel{Path: l.Path}) {
			l = nil // nothing to run
		}
	}
	L := m.layout(s)
	old := s.Laravel
	s.Laravel = l
	var units []laravelUnit
	if l != nil {
		root, err := m.laravelRoot(s, L)
		if err != nil {
			s.Laravel = old
			m.mu.Unlock()
			return nil, "", err
		}
		units = m.laravelUnits(s, L, root)
	}
	// Stop and remove what's no longer wanted, then write and start the rest.
	keep := map[string]bool{}
	for _, u := range units {
		keep[u.Name+".service"] = true
		if u.Oneshot {
			keep[u.Name+".timer"] = true
		}
	}
	for _, f := range m.existingLaravelUnits(L) {
		if !keep[f] {
			_, _ = m.run.Run(ctx, "systemctl", "disable", "--now", f)
			_ = os.Remove(m.p(filepath.Join("/etc/systemd/system", f)))
		}
	}
	for _, u := range units {
		if err := m.render(laravelUnitTmpl, u, filepath.Join("/etc/systemd/system", u.Name+".service"), 0o644); err != nil {
			m.mu.Unlock()
			return nil, "", err
		}
		if u.Oneshot {
			if err := writeFile(m.p(filepath.Join("/etc/systemd/system", u.Name+".timer")), []byte(fmt.Sprintf(laravelTimer, s.Domain)), 0o644); err != nil {
				m.mu.Unlock()
				return nil, "", err
			}
		}
	}
	_, _ = m.run.Run(ctx, "systemctl", "daemon-reload")
	suspended := statusOf(s.Status) == StatusSuspended
	for _, u := range units {
		unit := u.Name + ".service"
		if u.Oneshot {
			unit = u.Name + ".timer"
		}
		if suspended {
			continue // started again when the account is reactivated
		}
		if _, err := m.run.Run(ctx, "systemctl", "enable", unit); err != nil {
			m.mu.Unlock()
			return nil, "", err
		}
		if _, err := m.run.Run(ctx, "systemctl", "restart", unit); err != nil {
			m.mu.Unlock()
			return nil, "", fmt.Errorf("%s didn't start: %w", u.What, err)
		}
	}
	if err := m.writeJSON(L.StateFile, s); err != nil {
		m.mu.Unlock()
		return nil, "", err
	}
	m.mu.Unlock()

	note := ""
	if l != nil && l.Reverb {
		note = m.reverbSite(ctx, s, l)
	}
	if old != nil && old.Reverb && (l == nil || !l.Reverb || l.ReverbDomain != old.ReverbDomain) {
		if rs, err := m.Get(old.ReverbDomain); err == nil && rs.Runtime == RuntimeProxy && rs.Port == old.ReverbPort {
			_ = m.Delete(ctx, old.ReverbDomain, false)
		}
	}
	return s, note, nil
}

// reverbSite makes the website that forwards WebSockets to Reverb and tries
// to get it a certificate; it returns a note when something needs doing.
func (m *Manager) reverbSite(ctx context.Context, s *Site, l *Laravel) string {
	rs, err := m.Get(l.ReverbDomain)
	switch {
	case err != nil:
		if rs, err = m.Create(ctx, CreateRequest{Domain: l.ReverbDomain, Account: s.Account, Runtime: RuntimeProxy, Port: l.ReverbPort}); err != nil {
			return "Reverb runs, but its website " + l.ReverbDomain + " couldn't be made: " + err.Error()
		}
	case rs.Runtime != RuntimeProxy || rs.Account != s.Account:
		return l.ReverbDomain + " is already another website; pick another address for Reverb."
	case rs.Port != l.ReverbPort:
		return l.ReverbDomain + " forwards to another port; remove that website or pick another address."
	}
	if rs.SSL == nil {
		if _, err := m.IssueCertificate(ctx, l.ReverbDomain, IssueRequest{}, func(string, ...any) {}); err != nil {
			return "Reverb runs. HTTPS for " + l.ReverbDomain + " isn't ready yet (" + err.Error() + "); point the name at this server and get a certificate in its Domains & HTTPS tab."
		}
	}
	return ""
}

// laravelControl starts or stops a website's Laravel units (suspension,
// reactivation). Called with m.mu held.
func (m *Manager) laravelControl(ctx context.Context, s *Site, start bool) {
	if s.Laravel == nil {
		return
	}
	L := m.layout(s)
	for _, f := range m.existingLaravelUnits(L) {
		if strings.HasSuffix(f, "-schedule.service") {
			continue // the timer starts it
		}
		if start {
			_, _ = m.run.Run(ctx, "systemctl", "enable", "--now", f)
		} else {
			_, _ = m.run.Run(ctx, "systemctl", "disable", "--now", f)
		}
	}
}

// laravelRestart restarts workers and Reverb after a new release goes live,
// so they run the new code.
func (m *Manager) laravelRestart(ctx context.Context, s *Site) {
	if s.Laravel == nil || statusOf(s.Status) == StatusSuspended {
		return
	}
	for _, f := range m.existingLaravelUnits(m.layout(s)) {
		if strings.HasSuffix(f, ".service") && !strings.HasSuffix(f, "-schedule.service") {
			_, _ = m.run.Run(ctx, "systemctl", "restart", f)
		}
	}
}

// removeLaravel deletes a website's Laravel units (website deleted).
func (m *Manager) removeLaravel(ctx context.Context, L layout) {
	units := m.existingLaravelUnits(L)
	for _, f := range units {
		_, _ = m.run.Run(ctx, "systemctl", "disable", "--now", f)
		_ = os.Remove(m.p(filepath.Join("/etc/systemd/system", f)))
	}
	if len(units) > 0 {
		_, _ = m.run.Run(ctx, "systemctl", "daemon-reload")
	}
}
