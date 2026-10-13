package sites

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Runtimes a site can use.
const (
	RuntimePHP    = "php"
	RuntimeNode   = "node"
	RuntimePython = "python"
	RuntimeStatic = "static"
)

var (
	nodeVersionRe   = regexp.MustCompile(`^[1-9][0-9]?$`)   // major: 20, 22, 24
	pythonVersionRe = regexp.MustCompile(`^3\.[0-9]{1,2}$`) // 3.12
	phpVersionRe    = regexp.MustCompile(`^[5-9]\.[0-9]$`)  // 5.6 ... 8.5
	appActions      = map[string]bool{"start": true, "stop": true, "restart": true}
)

// IsApp reports whether the runtime runs as a long-lived process behind a proxy.
func IsApp(runtime string) bool { return runtime == RuntimeNode || runtime == RuntimePython }

func defaultStartCommand(runtime string) string {
	switch runtime {
	case RuntimeNode:
		return "node server.js"
	case RuntimePython:
		return "python app.py"
	}
	return ""
}

// validateStartCommand: the command runs as the site's own user, so its content is
// the user's business. It must still be a single line so it can't break out of the
// generated start script.
func validateStartCommand(cmd string) error {
	if strings.TrimSpace(cmd) == "" {
		return fmt.Errorf("start command is required for this runtime")
	}
	if len(cmd) > 300 {
		return fmt.Errorf("start command must be under 300 characters")
	}
	for _, r := range cmd {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("start command must be a single line without control characters")
		}
	}
	return nil
}

// checkRuntime validates a runtime/version pair and confirms it is installed.
func (m *Manager) checkRuntime(runtime, version string) error {
	switch runtime {
	case RuntimePHP:
		if !phpVersionRe.MatchString(version) {
			return fmt.Errorf("invalid PHP version %q", version)
		}
		if _, err := os.Stat(m.p(filepath.Join("/etc/php", version, "fpm"))); err != nil {
			return fmt.Errorf("PHP %s is not installed on this server", version)
		}
	case RuntimeNode:
		if !nodeVersionRe.MatchString(version) {
			return fmt.Errorf("invalid Node.js version %q (use the major version, e.g. 22)", version)
		}
		if _, err := os.Stat(m.p(filepath.Join(m.runtimeBin(runtime, version), "node"))); err != nil {
			return fmt.Errorf("Node.js %s is not installed on this server", version)
		}
	case RuntimePython:
		if !pythonVersionRe.MatchString(version) {
			return fmt.Errorf("invalid Python version %q (e.g. 3.12)", version)
		}
		if _, err := os.Stat(m.p(filepath.Join(m.runtimeBin(runtime, version), "python3"))); err != nil {
			return fmt.Errorf("Python %s is not installed on this server", version)
		}
	case RuntimeStatic:
	default:
		return fmt.Errorf("runtime must be php, node, python or static")
	}
	return nil
}

func (m *Manager) runtimeBin(runtime, version string) string {
	return filepath.Join(m.cfg.RuntimesDir, runtime, version, "bin")
}

// defaultVersion picks the configured default or the newest installed version.
func (m *Manager) defaultVersion(runtime string) string {
	switch runtime {
	case RuntimePHP:
		return m.cfg.DefaultPHP
	case RuntimeNode, RuntimePython:
		v := InstalledRuntimes(m.cfg.Root, m.cfg.RuntimesDir)[runtime]
		if len(v) > 0 {
			return v[len(v)-1]
		}
	}
	return ""
}

// InstalledRuntimes lists versions found under the runtimes directory, oldest first.
func InstalledRuntimes(root, runtimesDir string) map[string][]string {
	out := map[string][]string{"node": {}, "python": {}}
	for _, rt := range []string{"node", "python"} {
		entries, _ := os.ReadDir(filepath.Join(root, runtimesDir, rt))
		for _, e := range entries {
			name := e.Name()
			if (rt == "node" && nodeVersionRe.MatchString(name)) || (rt == "python" && pythonVersionRe.MatchString(name)) {
				out[rt] = append(out[rt], name)
			}
		}
		sortVersions(out[rt])
	}
	return out
}

func sortVersions(v []string) {
	key := func(s string) (int, int) {
		var a, b int
		fmt.Sscanf(s, "%d.%d", &a, &b)
		return a, b
	}
	for i := 1; i < len(v); i++ {
		for j := i; j > 0; j-- {
			a1, b1 := key(v[j-1])
			a2, b2 := key(v[j])
			if a1 > a2 || (a1 == a2 && b1 > b2) {
				v[j-1], v[j] = v[j], v[j-1]
			}
		}
	}
}

func (m *Manager) nextPort() int {
	used := map[int]bool{}
	sites, _ := m.List()
	for _, s := range sites {
		used[s.Port] = true
	}
	for p := m.cfg.AppPortStart; p < m.cfg.AppPortStart+5000; p++ {
		if !used[p] {
			return p
		}
	}
	return 0
}

// AppAction starts, stops or restarts a Node.js/Python app.
func (m *Manager) AppAction(ctx context.Context, domain, action string) error {
	if !appActions[action] {
		return fmt.Errorf("action must be start, stop or restart")
	}
	site, err := m.Get(domain)
	if err != nil {
		return err
	}
	if !IsApp(site.Runtime) {
		return fmt.Errorf("%s is a %s site and has no app process", domain, site.Runtime)
	}
	if action != "stop" && statusOf(site.Status) == StatusSuspended {
		return fmt.Errorf("%s is suspended; reactivate it first", domain)
	}
	_, err = m.run.Run(ctx, "systemctl", action, m.layout(site).UnitName)
	return err
}

// AppStatus returns systemd's state for the app ("active", "failed", ...).
func (m *Manager) AppStatus(ctx context.Context, domain string) (string, error) {
	site, err := m.Get(domain)
	if err != nil {
		return "", err
	}
	if !IsApp(site.Runtime) {
		return "n/a", nil
	}
	out, _ := m.run.Run(ctx, "systemctl", "is-active", m.layout(site).UnitName)
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	return state, nil
}

// AppLogs returns the last lines of the app's output.
func (m *Manager) AppLogs(ctx context.Context, domain string, lines int) (string, error) {
	site, err := m.Get(domain)
	if err != nil {
		return "", err
	}
	if !IsApp(site.Runtime) {
		return "", fmt.Errorf("%s has no app process; check its web logs instead", domain)
	}
	if lines <= 0 || lines > 1000 {
		lines = 200
	}
	out, err := m.run.Run(ctx, "journalctl", "--unit", m.layout(site).UnitName+".service",
		"--lines", fmt.Sprint(lines), "--no-pager", "--output", "short-iso")
	return string(out), err
}
