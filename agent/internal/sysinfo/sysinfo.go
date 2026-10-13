// Package sysinfo reads server facts from /proc and /etc without running commands.
package sysinfo

import (
	"bufio"
	"context"
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
}

// Collect gathers system information. root is "/" in production.
func Collect(root string) Info {
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
	return in
}

// PHPVersions lists installed PHP-FPM versions, e.g. ["8.1","8.3"].
func PHPVersions(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, "etc/php/*/fpm"))
	out := []string{}
	for _, m := range matches {
		out = append(out, filepath.Base(filepath.Dir(m)))
	}
	sort.Strings(out)
	return out
}

type ServiceStatus struct {
	Name   string `json:"name"`
	Active string `json:"active"`
}

// Services reports systemd state of the services the panel manages.
func Services(ctx context.Context, r runner.Runner, root string) []ServiceStatus {
	names := []string{"nginx", "mariadb", "fail2ban", "ike-agent"}
	for _, v := range PHPVersions(root) {
		names = append(names, "php"+v+"-fpm")
	}
	out := make([]ServiceStatus, 0, len(names))
	for _, n := range names {
		b, _ := r.Run(ctx, "systemctl", "is-active", n)
		state := strings.TrimSpace(string(b))
		if state == "" {
			state = "unknown"
		}
		out = append(out, ServiceStatus{Name: n, Active: state})
	}
	return out
}

func round1(f float64) float64 { return float64(int64(f*10+0.5)) / 10 }
