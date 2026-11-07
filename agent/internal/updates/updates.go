// Package updates keeps the server and Ike itself up to date:
//
//   - Ubuntu packages: list pending updates, apply all or security-only,
//     configure automatic security updates, detect and schedule reboots.
//   - Ike: check GitHub releases and replace ike-agent / ike-mcp after
//     verifying their SHA-256 checksums.
//
// Moving to a new Ubuntu release is deliberately not done in place: Ike's
// server-to-server migration moves accounts to a freshly installed VPS instead.
package updates

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

type Manager struct {
	Run     runner.Runner
	Jobs    *jobs.Manager
	Root    string // "/" in production
	DryRun  bool
	Version string // running Ike version, e.g. "0.3.0"

	Repo         string // "owner/ike"
	APIBase      string // https://api.github.com (overridable for tests)
	DownloadBase string // https://github.com
	BinDir       string // /usr/local/bin
	HTTP         *http.Client
}

func (m *Manager) p(abs string) string { return filepath.Join(m.Root, abs) }

func (m *Manager) client() *http.Client {
	if m.HTTP != nil {
		return m.HTTP
	}
	return &http.Client{Timeout: 2 * time.Minute}
}

// ------------------------------------------------------------ OS packages --

// Package is one pending update.
type Package struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	To       string `json:"to"`
	Security bool   `json:"security"`
}

type Status struct {
	CheckedAt      time.Time    `json:"checked_at"`
	Packages       []Package    `json:"packages"`
	SecurityCount  int          `json:"security_count"`
	RebootRequired bool         `json:"reboot_required"`
	RebootPackages []string     `json:"reboot_packages,omitempty"`
	Auto           AutoSettings `json:"auto"`
}

// apt list --upgradable prints:  openssl/noble-updates,noble-security 3.0.13-0ubuntu3.4 amd64 [upgradable from: 3.0.13-0ubuntu3.1]
var aptLine = regexp.MustCompile(`^([a-z0-9][a-z0-9+.-]*)/(\S+)\s+(\S+)\s+\S+\s+\[upgradable from: ([^\]]+)\]`)

func parseUpgradable(out string) []Package {
	pkgs := []Package{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		mm := aptLine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
		if mm == nil {
			continue
		}
		pkgs = append(pkgs, Package{Name: mm[1], To: mm[3], From: mm[4], Security: strings.Contains(mm[2], "-security")})
	}
	return pkgs
}

// Check refreshes package lists and reports pending updates.
func (m *Manager) Check(ctx context.Context) (*Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if _, err := m.Run.Run(ctx, "apt-get", "update", "-q"); err != nil {
		return nil, fmt.Errorf("refresh package lists: %w", err)
	}
	out, err := m.Run.Run(ctx, "apt", "list", "--upgradable")
	if err != nil {
		return nil, err
	}
	st := &Status{CheckedAt: time.Now().UTC(), Packages: parseUpgradable(string(out))}
	for _, p := range st.Packages {
		if p.Security {
			st.SecurityCount++
		}
	}
	st.RebootRequired, st.RebootPackages = m.rebootRequired()
	st.Auto = m.AutoSettings()
	return st, nil
}

func (m *Manager) rebootRequired() (bool, []string) {
	if _, err := os.Stat(m.p("/var/run/reboot-required")); err != nil {
		return false, nil
	}
	b, _ := os.ReadFile(m.p("/var/run/reboot-required.pkgs"))
	var pkgs []string
	for _, l := range strings.Fields(string(b)) {
		pkgs = append(pkgs, l)
	}
	return true, pkgs
}

// Apply installs updates in the background. securityOnly uses Ubuntu's
// unattended-upgrade, which only takes packages from the security pocket.
func (m *Manager) Apply(securityOnly bool) (*jobs.Job, error) {
	title := "Install all updates"
	if securityOnly {
		title = "Install security updates"
	}
	return m.Jobs.Start("os-upgrade", title, 90*time.Minute, func(ctx context.Context, log jobs.Logger) error {
		log("Refreshing package lists")
		if out, err := m.Run.Run(ctx, "apt-get", "update", "-q"); err != nil {
			log("%s", out)
			return err
		}
		var out []byte
		var err error
		if securityOnly {
			log("Installing security updates")
			out, err = m.Run.Run(ctx, "unattended-upgrade", "-v")
		} else {
			log("Installing all updates (existing config files are kept)")
			out, err = m.Run.Run(ctx, "env", "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a",
				"apt-get", "-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade")
		}
		log("%s", tail(string(out), 120))
		if err != nil {
			return err
		}
		if out, err := m.Run.Run(ctx, "apt-get", "-y", "-q", "autoremove"); err != nil {
			log("%s", tail(string(out), 20))
		}
		if r, pkgs := m.rebootRequired(); r {
			log("A reboot is needed to finish (%s).", strings.Join(pkgs, ", "))
		} else {
			log("All done. No reboot needed.")
		}
		return nil
	})
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// ScheduleReboot reboots after a delay (1-1440 minutes) so visitors and users get warning.
func (m *Manager) ScheduleReboot(ctx context.Context, minutes int) error {
	if minutes < 1 || minutes > 1440 {
		return fmt.Errorf("delay must be 1-1440 minutes")
	}
	_, err := m.Run.Run(ctx, "shutdown", "-r", "+"+strconv.Itoa(minutes), "Ike: rebooting to finish updates")
	return err
}

// CancelReboot cancels a scheduled reboot.
func (m *Manager) CancelReboot(ctx context.Context) error {
	_, err := m.Run.Run(ctx, "shutdown", "-c")
	return err
}

// AutoSettings controls Ubuntu's unattended-upgrades.
type AutoSettings struct {
	SecurityUpdates bool   `json:"security_updates"` // install security updates daily
	AutoReboot      bool   `json:"auto_reboot"`      // reboot by itself when needed
	RebootTime      string `json:"reboot_time"`      // "03:30"
}

const autoFile = "/etc/apt/apt.conf.d/52ike-auto-upgrades"

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// AutoSettings reads the current settings (defaults: security on, no auto reboot).
func (m *Manager) AutoSettings() AutoSettings {
	s := AutoSettings{SecurityUpdates: true, RebootTime: "03:30"}
	b, err := os.ReadFile(m.p(autoFile))
	if err != nil {
		return s
	}
	text := string(b)
	s.SecurityUpdates = strings.Contains(text, `APT::Periodic::Unattended-Upgrade "1"`)
	s.AutoReboot = strings.Contains(text, `Unattended-Upgrade::Automatic-Reboot "true"`)
	if mm := regexp.MustCompile(`Automatic-Reboot-Time "([0-9:]+)"`).FindStringSubmatch(text); mm != nil {
		s.RebootTime = mm[1]
	}
	return s
}

// SetAutoSettings writes Ike's apt config file.
func (m *Manager) SetAutoSettings(s AutoSettings) error {
	if !hhmm.MatchString(s.RebootTime) {
		return fmt.Errorf("reboot time must look like 03:30")
	}
	on := map[bool]string{true: "1", false: "0"}
	content := fmt.Sprintf(`// Managed by Ike. Change these in the panel under Updates.
APT::Periodic::Update-Package-Lists "1";
APT::Periodic::Unattended-Upgrade "%s";
APT::Periodic::AutocleanInterval "7";
Unattended-Upgrade::Automatic-Reboot "%t";
Unattended-Upgrade::Automatic-Reboot-Time "%s";
Unattended-Upgrade::Remove-Unused-Kernel-Packages "true";
`, on[s.SecurityUpdates], s.AutoReboot, s.RebootTime)
	return writeFile(m.p(autoFile), []byte(content), 0o644)
}

// ---------------------------------------------------------------- Ike self --

// Release describes the newest Ike release.
type Release struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"update_available"`
	URL       string `json:"url"`
	Notes     string `json:"notes"`
	Published string `json:"published_at"`
}

// CheckIke asks GitHub for the latest release.
func (m *Manager) CheckIke(ctx context.Context) (*Release, error) {
	if m.Repo == "" || strings.Contains(m.Repo, "Mastermegamind") {
		return nil, fmt.Errorf("the Ike release repository isn't configured yet (set \"repo\" in agent.json)")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", m.APIBase+"/repos/"+m.Repo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("can't reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	var r struct {
		Tag       string `json:"tag_name"`
		URL       string `json:"html_url"`
		Body      string `json:"body"`
		Published string `json:"published_at"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&r); err != nil {
		return nil, err
	}
	notes := r.Body
	if len(notes) > 4000 {
		notes = notes[:4000] + "…"
	}
	return &Release{Current: m.Version, Latest: strings.TrimPrefix(r.Tag, "v"), URL: r.URL, Notes: notes,
		Published: r.Published, Available: newer(r.Tag, m.Version)}, nil
}

// newer reports whether version a is greater than b ("v0.3.1" > "0.3.0").
func newer(a, b string) bool {
	pa, pb := parseVer(a), parseVer(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVer(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "-")
	for i, part := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(part)
	}
	return out
}

// UpdateIke downloads, verifies and installs the latest ike-agent and ike-mcp,
// then restarts Ike's services a few seconds later (after this job reports back).
func (m *Manager) UpdateIke(ctx context.Context) (*jobs.Job, error) {
	rel, err := m.CheckIke(ctx)
	if err != nil {
		return nil, err
	}
	if !rel.Available {
		return nil, fmt.Errorf("Ike %s is already the newest version", m.Version)
	}
	return m.Jobs.Start("ike-update", "Update Ike to "+rel.Latest, 20*time.Minute, func(ctx context.Context, log jobs.Logger) error {
		arch := runtime.GOARCH
		for _, bin := range []string{"ike-agent", "ike-mcp", "ike-panel"} {
			url := fmt.Sprintf("%s/%s/releases/download/v%s/%s-linux-%s", m.DownloadBase, m.Repo, rel.Latest, bin, arch)
			log("Downloading %s %s", bin, rel.Latest)
			data, err := m.fetch(ctx, url, 200<<20)
			if err != nil {
				if bin == "ike-panel" {
					log("No ike-panel in this release; skipping")
					continue
				}
				return err
			}
			sumFile, err := m.fetch(ctx, url+".sha256", 4096)
			if err != nil {
				return fmt.Errorf("no checksum published for %s; refusing to install it", bin)
			}
			want := strings.Fields(string(sumFile))
			got := sha256.Sum256(data)
			if len(want) == 0 || !strings.EqualFold(want[0], hex.EncodeToString(got[:])) {
				return fmt.Errorf("checksum mismatch for %s; nothing was installed", bin)
			}
			log("Checksum verified")
			dest := m.p(filepath.Join(m.BinDir, bin))
			if err := writeFile(dest, data, 0o755); err != nil {
				return err
			}
		}
		log("Restarting Ike in 3 seconds")
		_, err := m.Run.Run(ctx, "systemd-run", "--on-active=3", "--unit=ike-self-restart", "--collect",
			"systemctl", "restart", "ike-agent", "ike-panel")
		return err
	})
}

func (m *Manager) fetch(ctx context.Context, url string, max int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := m.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errors.New("download is larger than expected")
	}
	return b, nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ike-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
