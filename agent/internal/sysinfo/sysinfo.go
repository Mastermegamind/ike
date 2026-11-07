// Package sysinfo reads server facts from /proc and /etc without running commands.
package sysinfo

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

type Info struct {
	Hostname    string   `json:"hostname"`
	OS          string   `json:"os"`
	OSVersion   string   `json:"os_version"`
	Kernel      string   `json:"kernel"`
	Arch        string   `json:"arch"`
	CPUs        int      `json:"cpus"`
	UptimeSec   int64    `json:"uptime_seconds"`
	Load        []string `json:"load_average"`
	MemTotalMB  int64    `json:"memory_total_mb"`
	MemAvailMB  int64    `json:"memory_available_mb"`
	DiskTotalGB float64  `json:"disk_total_gb"`
	DiskFreeGB  float64  `json:"disk_free_gb"`
	PHPVersions []string `json:"php_versions"`
	Node        []string `json:"node_versions"`
	Python      []string `json:"python_versions"`
}

// Collect gathers system information. root is "/" in production.
// runtimes maps "node"/"python" to installed versions.
func Collect(root string, runtimes map[string][]string) Info {
	in := Info{Arch: runtime.GOARCH, CPUs: runtime.NumCPU()}
	in.Hostname, _ = os.Hostname()

	if b, err := os.ReadFile(filepath.Join(root, "etc/os-release")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(v, `"`)
			switch k {
			case "NAME":
				in.OS = v
			case "VERSION_ID":
				in.OSVersion = v
			}
		}
	}
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		in.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			sec, _ := strconv.ParseFloat(f[0], 64)
			in.UptimeSec = int64(sec)
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) >= 3 {
			in.Load = f[:3]
		}
	}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 2 {
				continue
			}
			kb, _ := strconv.ParseInt(fields[1], 10, 64)
			switch fields[0] {
			case "MemTotal:":
				in.MemTotalMB = kb / 1024
			case "MemAvailable:":
				in.MemAvailMB = kb / 1024
			}
		}
		f.Close()
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err == nil {
		bs := float64(st.Bsize)
		in.DiskTotalGB = round1(float64(st.Blocks) * bs / 1e9)
		in.DiskFreeGB = round1(float64(st.Bavail) * bs / 1e9)
	}
	in.PHPVersions = PHPVersions(root)
	in.Node, in.Python = runtimes["node"], runtimes["python"]
	return in
}

// PHPVersions lists installed PHP-FPM versions, e.g. ["8.1","8.3"].
func PHPVersions(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, "etc/php/*/fpm"))
	out := []string{}
	for _, m := range matches {
		out = append(out, filepath.Base(filepath.Dir(m)))
	}
	sort.Slice(out, func(i, j int) bool {
		var a1, b1, a2, b2 int
		fmt.Sscanf(out[i], "%d.%d", &a1, &b1)
		fmt.Sscanf(out[j], "%d.%d", &a2, &b2)
		return a1 < a2 || (a1 == a2 && b1 < b2)
	})
	return out
}

type ServiceStatus struct {
	Name   string `json:"name"`
	Active string `json:"active"`
	// Controllable services can be started and stopped from the panel.
	Controllable bool `json:"controllable"`
}

// optionalServices appear when the admin installed them.
var optionalServices = []string{
	"postfix", "dovecot", "rspamd", "clamav-daemon", "clamav-freshclam", "redis-server", "valkey-server",
	"postgresql", "mongod", "pdns", "docker", "containerd", "k3s", "memcached", "opendkim",
}

// protectedServices are never stopped from the panel: losing them locks the
// admin out or takes the panel itself down.
var protectedServices = map[string]bool{"ssh": true, "sshd": true, "ike-agent": true, "ike-panel": true,
	"systemd-journald": true, "systemd-networkd": true, "systemd-resolved": true, "dbus": true}

func unitExists(root, name string) bool {
	for _, d := range []string{"/lib/systemd/system", "/usr/lib/systemd/system", "/etc/systemd/system"} {
		if _, err := os.Stat(filepath.Join(root, d, name+".service")); err == nil {
			return true
		}
	}
	return false
}

// ControlService starts, stops, restarts or reloads one of the services the
// panel lists. ssh and Ike itself can only be restarted.
func ControlService(ctx context.Context, r runner.Runner, root, webStack, name, action string) error {
	switch action {
	case "start", "stop", "restart", "reload", "enable", "disable":
	default:
		return fmt.Errorf("action must be start, stop, restart, reload, enable or disable")
	}
	known := false
	for _, s := range Services(ctx, r, root, webStack) {
		if s.Name == name {
			known = true
		}
	}
	if !known {
		return fmt.Errorf("%s is not a service Ike manages", name)
	}
	if protectedServices[name] && action != "restart" && action != "reload" {
		return fmt.Errorf("%s can only be restarted from the panel, so you are never locked out", name)
	}
	args := []string{action, name}
	if action == "enable" || action == "disable" {
		args = []string{action, "--now", name}
	}
	if (name == "ike-agent" || name == "ike-panel") && action == "restart" {
		// restart after this request has been answered
		args = []string{"--no-block", "restart", name}
	}
	_, err := r.Run(ctx, "systemctl", args...)
	return err
}

// Services reports systemd state of the services the panel manages.
func Services(ctx context.Context, r runner.Runner, root, webStack string) []ServiceStatus {
	names := []string{}
	switch webStack {
	case "caddy":
		names = append(names, "caddy")
	case "openlitespeed":
		names = append(names, "lsws")
	case "apache":
		names = append(names, "apache2")
	case "hybrid":
		names = append(names, "nginx", "apache2")
	default:
		names = append(names, "nginx")
	}
	names = append(names, "mariadb", "fail2ban", "cron", "ike-agent", "ike-panel")
	for _, v := range PHPVersions(root) {
		names = append(names, "php"+v+"-fpm")
	}
	for _, n := range optionalServices {
		if unitExists(root, n) {
			names = append(names, n)
		}
	}
	out := make([]ServiceStatus, 0, len(names))
	for _, n := range names {
		b, _ := r.Run(ctx, "systemctl", "is-active", n)
		state := strings.TrimSpace(string(b))
		if state == "" {
			state = "unknown"
		}
		out = append(out, ServiceStatus{Name: n, Active: state, Controllable: true})
	}
	return out
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
