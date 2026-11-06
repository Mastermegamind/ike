package sites

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
)

// Limits caps what one account can use, so a busy or broken customer can't
// slow down everyone else on the server. Zero means "no limit".
type Limits struct {
	CPUPercent   int `json:"cpu_percent"`   // 100 = one full core
	MemoryMB     int `json:"memory_mb"`     // apps; PHP is capped through its workers
	MaxProcesses int `json:"max_processes"` // tasks per account
	DiskMB       int `json:"disk_mb"`       // filesystem quota, where quotas are enabled
	PHPWorkers   int `json:"php_workers"`   // pm.max_children for each of its websites
}

// How limits are enforced:
//   - Node.js/Python apps run inside a systemd slice per account
//     (ike-acct-<name>.slice) with CPUQuota, MemoryMax and TasksMax.
//   - PHP runs in shared PHP-FPM services, so each website's pool gets
//     pm.max_children and a memory_limit derived from the account's memory.
//   - Disk uses Linux user quotas (setquota); if the filesystem has no quota
//     support the agent says so instead of pretending.
var sliceTmpl = template.Must(template.New("slice").Parse(`# Managed by Ike: resource limits for account {{.Name}}
[Unit]
Description=Ike account {{.Name}}

[Slice]
{{- if .L.CPUPercent}}
CPUQuota={{.L.CPUPercent}}%
{{- end}}
{{- if .L.MemoryMB}}
MemoryMax={{.L.MemoryMB}}M
MemoryHigh={{.High}}M
{{- end}}
{{- if .L.MaxProcesses}}
TasksMax={{.L.MaxProcesses}}
{{- end}}
`))

func (l Limits) check() error {
	switch {
	case l.CPUPercent < 0 || l.CPUPercent > 6400:
		return fmt.Errorf("CPU limit must be 0-6400%% (100%% is one core)")
	case l.MemoryMB < 0 || (l.MemoryMB > 0 && l.MemoryMB < 64):
		return fmt.Errorf("memory limit must be at least 64 MB, or 0 for none")
	case l.MaxProcesses < 0 || (l.MaxProcesses > 0 && l.MaxProcesses < 16):
		return fmt.Errorf("process limit must be at least 16, or 0 for none")
	case l.DiskMB < 0:
		return fmt.Errorf("disk limit can't be negative")
	case l.PHPWorkers < 0 || l.PHPWorkers > 200:
		return fmt.Errorf("PHP workers must be 0-200")
	}
	return nil
}

// phpWorkers is the pm.max_children a site's pool gets.
func (l Limits) phpWorkers() int {
	if l.PHPWorkers > 0 {
		return l.PHPWorkers
	}
	return 5
}

// phpMemoryMB is the per-request memory_limit for the account's PHP.
func (l Limits) phpMemoryMB() int {
	if l.MemoryMB == 0 {
		return 256
	}
	per := l.MemoryMB / l.phpWorkers()
	if per < 64 {
		per = 64
	}
	if per > 512 {
		per = 512
	}
	return per
}

func (m *Manager) sliceFile(account string) string {
	return filepath.Join("/etc/systemd/system", "ike-acct-"+account+".slice")
}

// SetLimits applies resource limits to an account and all of its websites.
func (m *Manager) SetLimits(ctx context.Context, account string, l Limits) (*Account, error) {
	if err := l.check(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(account)
	if err != nil {
		return nil, err
	}
	a.Limits = l
	if err := m.writeSlice(ctx, a); err != nil {
		return nil, err
	}
	// Rewrite PHP pools so pm.max_children and memory_limit follow the new limits.
	sites, _ := m.sitesOf(account)
	reload := map[string]bool{}
	for i := range sites {
		if sites[i].Runtime != RuntimePHP {
			continue
		}
		L := m.layoutWith(&sites[i], a)
		if err := m.render(poolTmpl, L, L.PoolFile, 0o644); err != nil {
			return nil, err
		}
		reload[sites[i].Version] = true
	}
	for v := range reload {
		if _, err := m.run.Run(ctx, "systemctl", "reload", "php"+v+"-fpm"); err != nil {
			return nil, err
		}
	}
	if l.DiskMB > 0 {
		kb := strconv.Itoa(l.DiskMB * 1024)
		soft := strconv.Itoa(l.DiskMB * 1024 * 95 / 100)
		if _, err := m.run.Run(ctx, "setquota", "-u", account, soft, kb, "0", "0", "-a"); err != nil {
			slog.Warn("disk quota not applied", "account", account, "err", err)
			a.QuotaNote = "Disk quotas aren't enabled on this server's filesystem, so the disk limit is shown but not enforced."
		} else {
			a.QuotaNote = ""
		}
	} else {
		_, _ = m.run.Run(ctx, "setquota", "-u", account, "0", "0", "0", "0", "-a")
		a.QuotaNote = ""
	}
	if err := m.saveAccount(a); err != nil {
		return nil, err
	}
	slog.Info("limits set", "account", account, "cpu", l.CPUPercent, "mem", l.MemoryMB, "disk", l.DiskMB)
	return a, nil
}

func (m *Manager) writeSlice(ctx context.Context, a *Account) error {
	high := a.Limits.MemoryMB * 9 / 10
	if err := m.render(sliceTmpl, map[string]any{"Name": a.Name, "L": a.Limits, "High": high}, m.sliceFile(a.Name), 0o644); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	return nil
}

// layoutWith is layout() plus the account's limits (for pool rendering).
func (m *Manager) layoutWith(s *Site, a *Account) layout {
	L := m.layout(s)
	if a != nil { // the account may not be saved yet
		L.PHPWorkers = a.Limits.phpWorkers()
		L.PHPMemoryMB = a.Limits.phpMemoryMB()
		L.applyPHP(s.PHP, a.Limits)
	}
	return L
}

// QuotaUsage is one account's disk use as the kernel counts it.
type QuotaUsage struct {
	Account string  `json:"account"`
	UsedMB  float64 `json:"used_mb"`
	SoftMB  float64 `json:"soft_mb"`
	HardMB  float64 `json:"hard_mb"`
	Over    bool    `json:"over"`
}

// QuotaReport says whether disk quotas are on and, if so, every account's
// usage from repquota (fast: no walking of files).
func (m *Manager) QuotaReport(ctx context.Context) (map[string]any, error) {
	out := map[string]any{"enabled": m.cfg.DiskQuotas, "accounts": []QuotaUsage{}}
	if !m.cfg.DiskQuotas {
		out["note"] = "Disk quotas are off. Re-run the installer with --disk-quotas to enforce per-account disk limits."
		return out, nil
	}
	b, err := m.run.Run(ctx, "repquota", "-u", "-O", "csv", "-a")
	if err != nil {
		out["note"] = "Quotas are configured but not active yet (reboot the server if the installer asked you to)."
		return out, nil
	}
	accts, _ := m.Accounts()
	mine := map[string]bool{}
	for _, a := range accts {
		mine[a.Name] = true
	}
	out["accounts"] = parseRepquotaCSV(string(b), mine)
	return out, nil
}

func parseRepquotaCSV(s string, keep map[string]bool) []QuotaUsage {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	col := map[string]int{"name": 0, "used": 3, "soft": 4, "hard": 5}
	if len(lines) > 0 && strings.Contains(strings.ToLower(lines[0]), "blockused") {
		for i, h := range strings.Split(lines[0], ",") {
			switch strings.ToLower(strings.TrimSpace(h)) {
			case "blockused":
				col["used"] = i
			case "blocksoftlimit":
				col["soft"] = i
			case "blockhardlimit":
				col["hard"] = i
			}
		}
		lines = lines[1:]
	}
	out := []QuotaUsage{}
	for _, l := range lines {
		f := strings.Split(l, ",")
		if len(f) <= col["hard"] {
			continue
		}
		name := strings.Trim(f[col["name"]], `" `)
		if !keep[name] {
			continue
		}
		kb := func(i int) float64 {
			v, _ := strconv.ParseFloat(strings.Trim(f[i], `" `), 64)
			return float64(int(v/1024*10)) / 10
		}
		q := QuotaUsage{Account: name, UsedMB: kb(col["used"]), SoftMB: kb(col["soft"]), HardMB: kb(col["hard"])}
		q.Over = q.HardMB > 0 && q.UsedMB >= q.HardMB
		out = append(out, q)
	}
	return out
}
