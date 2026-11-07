// Package infra holds server-level operations features: hardware and network
// inventory with asset details, scheduled maintenance windows, and event
// webhooks.
package infra

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

// Inventory describes the machine.
type Inventory struct {
	Hostname       string    `json:"hostname"`
	OS             string    `json:"os"`
	Kernel         string    `json:"kernel"`
	Virtualization string    `json:"virtualization"`
	Vendor         string    `json:"vendor,omitempty"`
	Product        string    `json:"product,omitempty"`
	Serial         string    `json:"serial,omitempty"`
	BIOS           string    `json:"bios,omitempty"`
	CPUModel       string    `json:"cpu_model"`
	CPUs           int       `json:"cpus"`
	MemoryMB       int       `json:"memory_mb"`
	SwapMB         int       `json:"swap_mb"`
	Disks          []Disk    `json:"disks"`
	Interfaces     []NIC     `json:"interfaces"`
	UptimeHours    float64   `json:"uptime_hours"`
	BootedAt       time.Time `json:"booted_at"`
	Asset          Asset     `json:"asset"`
	CollectedAt    time.Time `json:"collected_at"`
}

type Disk struct {
	Name       string `json:"name"`
	SizeGB     int64  `json:"size_gb"`
	Model      string `json:"model,omitempty"`
	Type       string `json:"type"` // disk | part | lvm | raid
	Mount      string `json:"mount,omitempty"`
	Filesystem string `json:"filesystem,omitempty"`
	Rotational bool   `json:"rotational"`
}

type NIC struct {
	Name      string   `json:"name"`
	MAC       string   `json:"mac,omitempty"`
	State     string   `json:"state"`
	MTU       int      `json:"mtu"`
	Addresses []string `json:"addresses"`
}

// Asset is what the admin records about the machine.
type Asset struct {
	Tag          string `json:"tag"`
	Owner        string `json:"owner"`
	Location     string `json:"location"`
	Provider     string `json:"provider"`
	PurchaseDate string `json:"purchase_date"` // YYYY-MM-DD
	RenewalDate  string `json:"renewal_date"`
	MonthlyCost  string `json:"monthly_cost"`
	Notes        string `json:"notes"`
}

type Manager struct {
	env   *kit.Env
	asset kit.Store[Asset]
	maint kit.Store[[]Window]
	hooks kit.Store[[]Webhook]
	// SetMaintenance puts websites in or out of maintenance mode; set by the API.
	SetMaintenance func(ctx context.Context, sites []string, on bool) (previous map[string]string, err error)
	// AllSites lists every website.
	AllSites func() []string
	// ApplyUpdates installs security updates (and reboots when asked).
	ApplyUpdates func(ctx context.Context, reboot bool) error
	deliveries   chan delivery
	deliveryLog  []Delivery
}

func New(env *kit.Env) *Manager {
	m := &Manager{env: env, deliveries: make(chan delivery, 500)}
	m.asset.Path = env.P("/var/lib/ike/infra/asset.json")
	m.maint.Path = env.P("/var/lib/ike/infra/maintenance.json")
	m.hooks.Path = env.P("/var/lib/ike/infra/webhooks.json")
	return m
}

func (m *Manager) read(p string) string {
	b, _ := os.ReadFile(m.env.P(p))
	return strings.TrimSpace(string(b))
}

// Collect gathers the inventory.
func (m *Manager) Collect(ctx context.Context) (*Inventory, error) {
	inv := &Inventory{CollectedAt: kit.Now(), Disks: []Disk{}, Interfaces: []NIC{}}
	inv.Hostname, _ = os.Hostname()
	if m.env.Cfg.Hostname != "" {
		inv.Hostname = m.env.Cfg.Hostname
	}
	for _, l := range strings.Split(m.read("/etc/os-release"), "\n") {
		if v, ok := strings.CutPrefix(l, "PRETTY_NAME="); ok {
			inv.OS = strings.Trim(v, `"`)
		}
	}
	inv.Kernel = m.read("/proc/sys/kernel/osrelease")
	inv.Vendor = m.read("/sys/class/dmi/id/sys_vendor")
	inv.Product = m.read("/sys/class/dmi/id/product_name")
	inv.Serial = m.read("/sys/class/dmi/id/product_serial")
	if v := m.read("/sys/class/dmi/id/bios_version"); v != "" {
		inv.BIOS = strings.TrimSpace(m.read("/sys/class/dmi/id/bios_vendor") + " " + v)
	}
	if out, err := m.env.Run.Run(ctx, "systemd-detect-virt"); err == nil {
		inv.Virtualization = strings.TrimSpace(string(out))
	} else {
		inv.Virtualization = "none"
	}
	if f, err := os.Open(m.env.P("/proc/cpuinfo")); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			k, v, ok := strings.Cut(sc.Text(), ":")
			if !ok {
				continue
			}
			switch strings.TrimSpace(k) {
			case "model name":
				inv.CPUModel = strings.TrimSpace(v)
			case "processor":
				inv.CPUs++
			}
		}
		f.Close()
	}
	for _, l := range strings.Split(m.read("/proc/meminfo"), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		kb, _ := strconv.Atoi(f[1])
		switch f[0] {
		case "MemTotal:":
			inv.MemoryMB = kb / 1024
		case "SwapTotal:":
			inv.SwapMB = kb / 1024
		}
	}
	if up := strings.Fields(m.read("/proc/uptime")); len(up) > 0 {
		s, _ := strconv.ParseFloat(up[0], 64)
		inv.UptimeHours = float64(int(s/360)) / 10
		inv.BootedAt = time.Now().Add(-time.Duration(s) * time.Second).UTC().Truncate(time.Minute)
	}
	if out, err := m.env.Run.Run(ctx, "lsblk", "-J", "-b", "-o", "NAME,SIZE,TYPE,MODEL,MOUNTPOINT,FSTYPE,ROTA"); err == nil {
		inv.Disks = parseLsblk(out)
	}
	if out, err := m.env.Run.Run(ctx, "ip", "-j", "addr"); err == nil {
		inv.Interfaces = parseIPAddr(out)
	}
	inv.Asset, _ = m.asset.Load()
	return inv, nil
}

func parseLsblk(b []byte) []Disk {
	var res struct {
		Devices []lsblkDev `json:"blockdevices"`
	}
	out := []Disk{}
	if json.Unmarshal(b, &res) != nil {
		return out
	}
	var walk func(ds []lsblkDev)
	walk = func(ds []lsblkDev) {
		for _, d := range ds {
			if d.Type == "loop" || d.Type == "rom" {
				continue
			}
			out = append(out, Disk{Name: d.Name, SizeGB: int64(d.Size) / 1e9, Model: strings.TrimSpace(d.Model), Type: d.Type,
				Mount: d.Mount, Filesystem: d.FS, Rotational: bool(d.Rota)})
			walk(d.Children)
		}
	}
	walk(res.Devices)
	return out
}

type lsblkDev struct {
	Name     string     `json:"name"`
	Size     flexNum    `json:"size"`
	Type     string     `json:"type"`
	Model    string     `json:"model"`
	Mount    string     `json:"mountpoint"`
	FS       string     `json:"fstype"`
	Rota     flexBool   `json:"rota"`
	Children []lsblkDev `json:"children"`
}

// lsblk prints numbers and booleans as strings on older versions.
type flexNum float64
type flexBool bool

func (n *flexNum) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "null" || s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	*n = flexNum(f)
	return err
}

func (v *flexBool) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	*v = flexBool(s == "true" || s == "1")
	return nil
}

func parseIPAddr(b []byte) []NIC {
	var raw []struct {
		Name  string   `json:"ifname"`
		MAC   string   `json:"address"`
		State string   `json:"operstate"`
		MTU   int      `json:"mtu"`
		Flags []string `json:"flags"`
		Addr  []struct {
			Local  string `json:"local"`
			Prefix int    `json:"prefixlen"`
			Scope  string `json:"scope"`
		} `json:"addr_info"`
	}
	out := []NIC{}
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	for _, r := range raw {
		if r.Name == "lo" || strings.HasPrefix(r.Name, "veth") || strings.HasPrefix(r.Name, "br-") || r.Name == "docker0" {
			continue
		}
		n := NIC{Name: r.Name, MAC: r.MAC, State: strings.ToLower(r.State), MTU: r.MTU, Addresses: []string{}}
		for _, a := range r.Addr {
			if a.Scope == "link" {
				continue
			}
			n.Addresses = append(n.Addresses, fmt.Sprintf("%s/%d", a.Local, a.Prefix))
		}
		out = append(out, n)
	}
	return out
}

var assetText = regexp.MustCompile(`^[\p{L}\p{N} .,:;'#&()/@+_-]{0,200}$`)
var dateOnly = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})?$`)

func (m *Manager) SetAsset(a Asset) (Asset, error) {
	for _, f := range []string{a.Tag, a.Owner, a.Location, a.Provider, a.MonthlyCost} {
		if !assetText.MatchString(f) {
			return a, fmt.Errorf("asset fields are up to 200 characters of plain text")
		}
	}
	if len(a.Notes) > 4000 {
		return a, fmt.Errorf("notes are up to 4000 characters")
	}
	if !dateOnly.MatchString(a.PurchaseDate) || !dateOnly.MatchString(a.RenewalDate) {
		return a, fmt.Errorf("dates look like 2026-01-31")
	}
	return m.asset.Update(func(cur *Asset) error { *cur = a; return nil })
}

var _ = filepath.Join
