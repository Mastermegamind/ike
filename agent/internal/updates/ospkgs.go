package updates

// Ubuntu packages, with approval and a way back:
//
//   - WatchPackages refreshes the package lists twice a day and announces
//     waiting updates once per change ("server.updates.available", or
//     "server.updates.security" when security fixes wait).
//   - Updates are installed only when an admin approves them (or by Ubuntu's
//     own unattended security updates, if the admin turned those on).
//   - Every run records each package's version before and after, so a run
//     can be rolled back: Ike reinstalls the earlier versions that apt can
//     still get (from the archive or its download cache) and says which it
//     couldn't.
//   - A new Ubuntu LTS release is announced too; moving to it runs only when
//     an admin approves it, after checks, and can't be rolled back except
//     from a snapshot of the server, which the admin must confirm they have.

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
)

// Change is one package a run changed.
type Change struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"` // empty: newly installed
	To   string `json:"to,omitempty"`   // empty: removed
}

// PackageRun is one approved package update, rollback or release upgrade.
type PackageRun struct {
	ID           string    `json:"id"`
	Kind         string    `json:"kind"` // security | all | rollback | release
	By           string    `json:"by,omitempty"`
	Status       string    `json:"status"` // running | done | failed
	Error        string    `json:"error,omitempty"`
	Changes      []Change  `json:"changes"`
	Skipped      []string  `json:"skipped,omitempty"` // rollback: packages whose earlier version apt can't get
	RollbackOf   string    `json:"rollback_of,omitempty"`
	RolledBackBy string    `json:"rolled_back_by,omitempty"`
	StartedAt    time.Time `json:"started_at"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
}

type pkgState struct {
	Runs         []PackageRun    `json:"runs"`
	LastCheck    *Status         `json:"last_check,omitempty"`
	AnnouncedKey string          `json:"announced_key,omitempty"`
	Release      *ReleaseUpgrade `json:"release,omitempty"`
	AnnouncedRel string          `json:"announced_release,omitempty"`
}

func (m *Manager) pkgStatePath() string {
	return m.p(filepath.Join(m.stateDir(), "package-updates.json"))
}

func (m *Manager) loadPkgState() pkgState {
	var s pkgState
	if b, err := os.ReadFile(m.pkgStatePath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (m *Manager) updatePkgState(fn func(*pkgState)) pkgState {
	stateMu.Lock()
	defer stateMu.Unlock()
	s := m.loadPkgState()
	fn(&s)
	if len(s.Runs) > 50 {
		s.Runs = s.Runs[len(s.Runs)-50:]
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := writeFile(m.pkgStatePath(), b, 0o600); err != nil {
		slog.Warn("could not save package update history", "err", err)
	}
	return s
}

// PackageRuns lists recent runs, newest first.
func (m *Manager) PackageRuns() []PackageRun {
	s := m.loadPkgState()
	out := []PackageRun{}
	for i := len(s.Runs) - 1; i >= 0; i-- {
		out = append(out, s.Runs[i])
	}
	return out
}

// installed returns every installed package's version.
func (m *Manager) installed(ctx context.Context) (map[string]string, error) {
	out, err := m.Run.Run(ctx, "dpkg-query", "-W", "-f=${db:Status-Abbrev} ${Package} ${Version}\n")
	if err != nil {
		return nil, err
	}
	return parseInstalled(string(out)), nil
}

func parseInstalled(out string) map[string]string {
	pkgs := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 3 && strings.HasPrefix(f[0], "ii") {
			pkgs[f[1]] = f[2]
		}
	}
	return pkgs
}

func diffInstalled(before, after map[string]string) []Change {
	var out []Change
	for name, v := range after {
		if before[name] != v {
			out = append(out, Change{Name: name, From: before[name], To: v})
		}
	}
	for name, v := range before {
		if _, ok := after[name]; !ok {
			out = append(out, Change{Name: name, From: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Apply installs updates in the background (by is who approved it).
// securityOnly uses Ubuntu's unattended-upgrade, which only takes packages
// from the security pocket.
func (m *Manager) Apply(securityOnly bool, by string) (*jobs.Job, error) {
	title, kind := "Install all updates", "all"
	if securityOnly {
		title, kind = "Install security updates", "security"
	}
	run := PackageRun{ID: newID(), Kind: kind, By: by, Status: "running", StartedAt: time.Now().UTC(), Changes: []Change{}}
	return m.Jobs.Start("os-upgrade", title, 90*time.Minute, func(ctx context.Context, log jobs.Logger) error {
		before, err := m.installed(ctx)
		if err != nil {
			return fmt.Errorf("read installed packages: %w", err)
		}
		m.updatePkgState(func(s *pkgState) { s.Runs = append(s.Runs, run) })
		err = m.applyPackages(ctx, securityOnly, log)
		m.finishRun(ctx, run.ID, before, err)
		return err
	})
}

func (m *Manager) applyPackages(ctx context.Context, securityOnly bool, log jobs.Logger) error {
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
		cmd := append(aptEnv(), "apt-get", "-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold", "upgrade")
		out, err = m.Run.Run(ctx, cmd[0], cmd[1:]...)
	}
	log("%s", tail(string(out), 120))
	if err != nil {
		return err
	}
	if r, pkgs := m.rebootRequired(); r {
		log("A reboot is needed to finish (%s).", strings.Join(pkgs, ", "))
	} else {
		log("All done. No reboot needed.")
	}
	return nil
}

func aptEnv() []string {
	return []string{"env", "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=a"}
}

// finishRun records what a run changed.
func (m *Manager) finishRun(ctx context.Context, id string, before map[string]string, runErr error) {
	after, err := m.installed(ctx)
	var changes []Change
	if err == nil {
		changes = diffInstalled(before, after)
	}
	m.updatePkgState(func(s *pkgState) {
		for i := range s.Runs {
			r := &s.Runs[i]
			if r.ID != id {
				continue
			}
			r.Changes, r.FinishedAt, r.Status = changes, time.Now().UTC(), "done"
			if r.Changes == nil {
				r.Changes = []Change{}
			}
			if runErr != nil {
				r.Status, r.Error = "failed", runErr.Error()
			}
		}
		s.LastCheck = nil // the next check reads the new state
	})
	kind := "server.updates.installed"
	if runErr != nil {
		kind = "server.updates.failed"
	}
	data := map[string]any{"changed": len(changes), "run": id}
	if runErr != nil {
		data["error"] = runErr.Error()
	}
	m.publish(kind, "Ubuntu packages", data)
}

// madison lists the versions apt can install for a package.
var madisonRe = regexp.MustCompile(`^\s*\S+\s*\|\s*(\S+)\s*\|`)

func (m *Manager) available(ctx context.Context, name string) map[string]bool {
	out, _ := m.Run.Run(ctx, "apt-cache", "madison", name)
	vs := map[string]bool{}
	for _, l := range strings.Split(string(out), "\n") {
		if mm := madisonRe.FindStringSubmatch(l); mm != nil {
			vs[mm[1]] = true
		}
	}
	// apt also installs from its download cache.
	if matches, _ := filepath.Glob(m.p("/var/cache/apt/archives/" + name + "_*.deb")); len(matches) > 0 {
		for _, f := range matches {
			parts := strings.SplitN(strings.TrimSuffix(filepath.Base(f), ".deb"), "_", 3)
			if len(parts) == 3 {
				v := strings.ReplaceAll(parts[1], "%3a", ":")
				vs[v] = true
			}
		}
	}
	return vs
}

var debName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]*(:[a-z0-9]+)?$`)
var debVersion = regexp.MustCompile(`^[0-9A-Za-z.+:~-]+$`)

// RollbackPackages puts a run's packages back to the versions they had
// before it, where apt can still get them.
func (m *Manager) RollbackPackages(ctx context.Context, runID, by string) (*jobs.Job, error) {
	s := m.loadPkgState()
	var target *PackageRun
	for i := range s.Runs {
		if s.Runs[i].ID == runID {
			target = &s.Runs[i]
		}
	}
	if target == nil {
		return nil, fmt.Errorf("package update %s %w", runID, ErrNotFound)
	}
	if target.Kind == "release" {
		return nil, fmt.Errorf("a move to a new Ubuntu release can't be undone package by package; restore the snapshot you took before it")
	}
	if target.Status == "running" {
		return nil, fmt.Errorf("that update is still running")
	}
	if target.RolledBackBy != "" {
		return nil, fmt.Errorf("that update was already rolled back")
	}
	var back []Change
	for _, c := range target.Changes {
		if c.From != "" && c.To != "" && debName.MatchString(c.Name) && debVersion.MatchString(c.From) {
			back = append(back, c)
		}
	}
	if len(back) == 0 {
		return nil, fmt.Errorf("that update didn't change any package versions")
	}
	run := PackageRun{ID: newID(), Kind: "rollback", By: by, Status: "running", RollbackOf: runID, StartedAt: time.Now().UTC(), Changes: []Change{}}
	return m.Jobs.Start("os-rollback", "Roll back package update "+runID, 60*time.Minute, func(ctx context.Context, log jobs.Logger) error {
		before, err := m.installed(ctx)
		if err != nil {
			return err
		}
		var args, skipped []string
		for _, c := range back {
			if before[c.Name] != c.To {
				log("%s has changed again since (%s); leaving it", c.Name, before[c.Name])
				skipped = append(skipped, c.Name)
				continue
			}
			if !m.available(ctx, c.Name)[c.From] && !m.DryRun {
				skipped = append(skipped, c.Name)
				continue
			}
			args = append(args, c.Name+"="+c.From)
		}
		run.Skipped = skipped
		m.updatePkgState(func(s *pkgState) { s.Runs = append(s.Runs, run) })
		if len(skipped) > 0 {
			log("Can't get the earlier version of: %s", strings.Join(skipped, ", "))
		}
		var runErr error
		if len(args) == 0 {
			runErr = fmt.Errorf("apt can't get the earlier version of any of these packages any more")
		} else {
			log("Reinstalling %d earlier versions", len(args))
			cmd := append(aptEnv(), "apt-get", "install", "-y", "-q", "--allow-downgrades", "-o", "Dpkg::Options::=--force-confdef",
				"-o", "Dpkg::Options::=--force-confold")
			out, err := m.Run.Run(ctx, cmd[0], append(cmd[1:], args...)...)
			log("%s", tail(string(out), 80))
			runErr = err
		}
		m.updatePkgState(func(s *pkgState) {
			for i := range s.Runs {
				if s.Runs[i].ID == run.ID {
					s.Runs[i].Skipped = skipped
				}
				if s.Runs[i].ID == runID && runErr == nil {
					s.Runs[i].RolledBackBy = run.ID
				}
			}
		})
		m.finishRun(ctx, run.ID, before, runErr)
		return runErr
	})
}

// ---------------------------------------------------------------- watching

// CachedStatus is the last package check without running apt (nil if none yet).
func (m *Manager) CachedStatus() *Status {
	s := m.loadPkgState()
	if s.LastCheck != nil {
		s.LastCheck.Auto = m.AutoSettings()
		s.LastCheck.RebootRequired, s.LastCheck.RebootPackages = m.rebootRequired()
	}
	return s.LastCheck
}

// remember stores a check and announces it if the waiting set changed.
func (m *Manager) remember(st *Status) {
	names := make([]string, 0, len(st.Packages))
	for _, p := range st.Packages {
		names = append(names, p.Name+"="+p.To)
	}
	sort.Strings(names)
	h := sha256.Sum256([]byte(strings.Join(names, ",")))
	key := hex.EncodeToString(h[:8])
	announce := false
	m.updatePkgState(func(s *pkgState) {
		s.LastCheck = st
		if len(st.Packages) > 0 && s.AnnouncedKey != key {
			s.AnnouncedKey, announce = key, true
		}
		if len(st.Packages) == 0 {
			s.AnnouncedKey = ""
		}
	})
	if announce {
		kind := "server.updates.available"
		if st.SecurityCount > 0 {
			kind = "server.updates.security"
		}
		m.publish(kind, "Ubuntu packages", map[string]any{"packages": len(st.Packages), "security": st.SecurityCount})
	}
}

// WatchPackages checks for package updates and new Ubuntu releases every
// interval until ctx ends.
func (m *Manager) WatchPackages(ctx context.Context, interval time.Duration) {
	t := time.NewTimer(5 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := m.Check(ctx); err != nil {
			slog.Debug("package check", "err", err)
		}
		if _, err := m.CheckRelease(ctx); err != nil {
			slog.Debug("ubuntu release check", "err", err)
		}
		t.Reset(interval)
	}
}

// ---------------------------------------------------------------- Ubuntu releases

// ReleaseUpgrade is a newer Ubuntu LTS this server could move to.
type ReleaseUpgrade struct {
	Current   string    `json:"current"`   // "24.04"
	Available string    `json:"available"` // "26.04", empty when none
	Supported bool      `json:"supported"` // Ike supports the new release
	CheckedAt time.Time `json:"checked_at"`
	Checks    []Check   `json:"checks,omitempty"`
}

// Check is one thing that must be true before moving to a new release.
type Check struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}

// SupportedReleases are the Ubuntu releases Ike installs on.
var SupportedReleases = []string{"22.04", "24.04", "26.04"}

var newReleaseRe = regexp.MustCompile(`New release '([0-9]{2}\.[0-9]{2})`)

func (m *Manager) osVersion() string {
	b, _ := os.ReadFile(m.p("/etc/os-release"))
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VERSION_ID="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// CheckRelease asks Ubuntu whether a newer LTS is available and checks
// whether this server is ready to move.
func (m *Manager) CheckRelease(ctx context.Context) (*ReleaseUpgrade, error) {
	r := &ReleaseUpgrade{Current: m.osVersion(), CheckedAt: time.Now().UTC()}
	out, _ := m.Run.Run(ctx, "do-release-upgrade", "-c")
	if mm := newReleaseRe.FindStringSubmatch(string(out)); mm != nil {
		r.Available = mm[1]
		for _, v := range SupportedReleases {
			r.Supported = r.Supported || v == r.Available
		}
		r.Checks = m.releaseChecks(ctx, r)
	}
	announce := false
	m.updatePkgState(func(s *pkgState) {
		s.Release = r
		if r.Available != "" && s.AnnouncedRel != r.Available {
			s.AnnouncedRel, announce = r.Available, true
		}
	})
	if announce {
		m.publish("server.release.available", "Ubuntu "+r.Available, map[string]any{"current": r.Current, "available": r.Available, "supported": r.Supported})
	}
	return r, nil
}

// CachedRelease is the last release check (nil if none yet).
func (m *Manager) CachedRelease() *ReleaseUpgrade { return m.loadPkgState().Release }

func (m *Manager) releaseChecks(ctx context.Context, r *ReleaseUpgrade) []Check {
	var cs []Check
	cs = append(cs, Check{Name: "Ike supports Ubuntu " + r.Available, OK: r.Supported,
		Note: "Ike runs on " + strings.Join(SupportedReleases, ", ") + "; wait for an Ike release that supports it"})
	var free uint64
	var st syscall.Statfs_t
	if err := syscall.Statfs(m.p("/"), &st); err == nil {
		free = st.Bavail * uint64(st.Bsize)
	}
	cs = append(cs, Check{Name: "At least 5 GB free on /", OK: m.DryRun || free >= 5<<30, Note: fmt.Sprintf("%.1f GB free", float64(free)/(1<<30))})
	reboot, _ := m.rebootRequired()
	cs = append(cs, Check{Name: "No restart waiting from earlier updates", OK: !reboot, Note: "restart the server first"})
	pending := -1
	if c := m.loadPkgState().LastCheck; c != nil {
		pending = len(c.Packages)
	}
	cs = append(cs, Check{Name: "All package updates installed", OK: pending == 0, Note: "install waiting updates first (" + strconv.Itoa(pending) + " waiting)"})
	return cs
}

// UpgradeRelease moves the server to the new Ubuntu release. It needs the
// admin to confirm they have a snapshot (there's no other way back) and
// every check to pass.
func (m *Manager) UpgradeRelease(ctx context.Context, by string, haveSnapshot bool) (*jobs.Job, error) {
	if !haveSnapshot {
		return nil, fmt.Errorf("take a snapshot of the server at your provider first and confirm it: moving to a new Ubuntu release can't be undone any other way")
	}
	r, err := m.CheckRelease(ctx)
	if err != nil {
		return nil, err
	}
	if r.Available == "" {
		return nil, fmt.Errorf("Ubuntu %s is the newest release this server can move to", r.Current)
	}
	for _, c := range r.Checks {
		if !c.OK {
			return nil, fmt.Errorf("not ready: %s (%s)", c.Name, c.Note)
		}
	}
	run := PackageRun{ID: newID(), Kind: "release", By: by, Status: "running", StartedAt: time.Now().UTC(), Changes: []Change{}}
	return m.Jobs.Start("os-release-upgrade", "Move to Ubuntu "+r.Available, 4*time.Hour, func(ctx context.Context, log jobs.Logger) error {
		before, err := m.installed(ctx)
		if err != nil {
			return err
		}
		m.updatePkgState(func(s *pkgState) { s.Runs = append(s.Runs, run) })
		log("Moving from Ubuntu %s to %s. This takes a while; websites keep running until the restart at the end.", r.Current, r.Available)
		cmd := append(aptEnv(), "do-release-upgrade", "-f", "DistUpgradeViewNonInteractive")
		out, runErr := m.Run.Run(ctx, cmd[0], cmd[1:]...)
		log("%s", tail(string(out), 150))
		if runErr == nil {
			log("Turning Ike's package sources back on for the new release")
			if err := m.reenableSources(ctx, log); err != nil {
				log("Some package sources need a look: %v", err)
			}
			log("Done. Restart the server to finish (Updates, Schedule restart).")
		}
		m.finishRun(ctx, run.ID, before, runErr)
		return runErr
	})
}

// reenableSources turns back on the third-party package sources Ike's
// installer added (PHP, Nginx, Node.js, MariaDB ...) that do-release-upgrade
// switched off, pointing them at the new release.
func (m *Manager) reenableSources(ctx context.Context, log jobs.Logger) error {
	codename := ""
	b, _ := os.ReadFile(m.p("/etc/os-release"))
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "VERSION_CODENAME="); ok {
			codename = strings.Trim(v, `"`)
		}
	}
	if codename == "" {
		return fmt.Errorf("can't tell the new release's codename")
	}
	disabled := regexp.MustCompile(`^#\s*(deb .*?)\s*# disabled on upgrade to \S+`)
	files, _ := filepath.Glob(m.p("/etc/apt/sources.list.d/*.list"))
	sources, _ := filepath.Glob(m.p("/etc/apt/sources.list.d/*.sources"))
	var problems []string
	for _, f := range append(files, sources...) {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		text, changed := string(data), false
		if strings.HasSuffix(f, ".list") {
			var lines []string
			for _, l := range strings.Split(text, "\n") {
				if mm := disabled.FindStringSubmatch(l); mm != nil {
					l = withCodename(mm[1], codename)
					changed = true
				}
				lines = append(lines, l)
			}
			text = strings.Join(lines, "\n")
		} else if strings.Contains(text, "Enabled: no") {
			text = strings.ReplaceAll(text, "Enabled: no", "Enabled: yes")
			text = regexp.MustCompile(`(?m)^Suites: \S+`).ReplaceAllString(text, "Suites: "+codename)
			changed = true
		}
		if changed {
			if err := writeFile(f, []byte(text), 0o644); err != nil {
				problems = append(problems, filepath.Base(f))
				continue
			}
			log("Turned %s back on", filepath.Base(f))
		}
	}
	if out, err := m.Run.Run(ctx, "apt-get", "update", "-q"); err != nil {
		problems = append(problems, "apt-get update: "+tail(string(out), 3))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

// withCodename replaces the suite in a one-line "deb URL SUITE parts" entry.
func withCodename(line, codename string) string {
	f := strings.Fields(line)
	for i := 1; i < len(f); i++ {
		if strings.HasPrefix(f[i], "[") {
			continue
		}
		if i+1 < len(f) && (strings.HasPrefix(f[i], "http") || strings.HasPrefix(f[i], "https")) {
			f[i+1] = codename
			break
		}
	}
	return strings.Join(f, " ")
}

// ErrNotFound is returned for an unknown update run.
var ErrNotFound = errors.New("not found")
