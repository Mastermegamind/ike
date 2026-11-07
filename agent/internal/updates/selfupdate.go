package updates

// Updating Ike itself, safely:
//
//  1. Watch checks GitHub every few hours. A new version is announced once
//     (event "ike.update.available": the panel's notification centre and the
//     admin's alert channels). In "auto" mode it is also installed.
//  2. UpdateIke (the admin pressed Update, or auto mode) downloads every
//     binary, checks its SHA-256 and that it runs and reports the expected
//     version, keeps a copy of the binaries now installed under
//     <state>/releases/<version>/, then swaps the new ones in and restarts.
//  3. Before restarting, it starts a watchdog from the *previous* version's
//     copy (`ike-agent upgrade-verify`). If the new agent or panel isn't
//     healthy within two minutes, the watchdog puts the previous binaries
//     back and restarts them: an update can't leave a server without its panel.
//  4. Rollback goes back to any kept version by hand, the same way.
//
// Ike's own data files only ever gain fields, and older versions ignore
// fields they don't know, so going back a version needs no data changes.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
)

// Binaries Ike installs on a server, in /usr/local/bin.
var Binaries = []string{"ike-agent", "ike-panel", "ike-mcp", "ike"}

// keepReleases is how many earlier versions stay available for rollback.
const keepReleases = 3

// verifyAfter is how long the watchdog waits before judging an update.
const verifyAfter = 90 * time.Second

var versionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// SelfSettings choose how Ike updates itself.
type SelfSettings struct {
	// Mode is "notify" (tell admins, install when one approves; the default),
	// "auto" (install new versions by itself) or "off" (don't check).
	Mode string `json:"mode"`
	// Skip is a version the admin chose not to install; newer ones are offered again.
	Skip string `json:"skip,omitempty"`
}

// Upgrade is one update or rollback of Ike.
type Upgrade struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"` // update | rollback
	From       string    `json:"from"`
	To         string    `json:"to"`
	By         string    `json:"by,omitempty"` // who approved it, or "auto"
	Status     string    `json:"status"`       // installing | checking | done | rolled_back | failed
	Reason     string    `json:"reason,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Announced  bool      `json:"announced,omitempty"` // its outcome event was published
}

// SelfStatus is everything the Updates screen shows about Ike.
type SelfStatus struct {
	Current   string       `json:"current"`
	Latest    *Release     `json:"latest,omitempty"`
	CheckedAt time.Time    `json:"checked_at,omitempty"`
	CheckErr  string       `json:"check_error,omitempty"`
	Settings  SelfSettings `json:"settings"`
	Kept      []string     `json:"kept_versions"` // versions available for rollback, newest first
	History   []Upgrade    `json:"history"`       // newest first
	Busy      bool         `json:"busy"`          // an update or its check is running now
}

type selfState struct {
	Settings  SelfSettings `json:"settings"`
	Latest    *Release     `json:"latest,omitempty"`
	CheckedAt time.Time    `json:"checked_at,omitempty"`
	Announced string       `json:"announced,omitempty"` // newest version already announced
	CheckErr  string       `json:"check_error,omitempty"`
	History   []Upgrade    `json:"history"`
	// Pending is the update the watchdog is judging.
	Pending *Upgrade `json:"pending,omitempty"`
}

var stateMu sync.Mutex

func (m *Manager) stateDir() string {
	if m.StateDir == "" {
		return "/var/lib/ike"
	}
	return m.StateDir
}

func (m *Manager) statePath() string       { return m.p(filepath.Join(m.stateDir(), "ike-updates.json")) }
func (m *Manager) releasesDir() string     { return m.p(filepath.Join(m.stateDir(), "releases")) }
func (m *Manager) binPath(b string) string { return m.p(filepath.Join(m.BinDir, b)) }

func (m *Manager) loadState() selfState {
	var s selfState
	if b, err := os.ReadFile(m.statePath()); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	if s.Settings.Mode == "" {
		s.Settings.Mode = "notify"
	}
	return s
}

func (m *Manager) saveState(s selfState) error {
	if len(s.History) > 50 {
		s.History = s.History[len(s.History)-50:]
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeFile(m.statePath(), b, 0o600)
}

// update changes the saved state under the lock.
func (m *Manager) update(fn func(*selfState)) selfState {
	stateMu.Lock()
	defer stateMu.Unlock()
	s := m.loadState()
	fn(&s)
	if err := m.saveState(s); err != nil {
		slog.Warn("could not save update state", "err", err)
	}
	return s
}

func (m *Manager) publish(kind, subject string, data map[string]any) {
	if m.Publish != nil {
		m.Publish(kind, subject, data)
	}
}

// keptVersions lists the versions saved for rollback, newest first.
func (m *Manager) keptVersions() []string {
	entries, _ := os.ReadDir(m.releasesDir())
	out := []string{}
	for _, e := range entries {
		if e.IsDir() && versionRe.MatchString(e.Name()) {
			if _, err := os.Stat(filepath.Join(m.releasesDir(), e.Name(), "ike-agent")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return newer(out[i], out[j]) })
	return out
}

// SelfStatus reports the running version, the newest release seen, the
// settings, kept versions and the update history.
func (m *Manager) SelfStatus() SelfStatus {
	stateMu.Lock()
	s := m.loadState()
	stateMu.Unlock()
	st := SelfStatus{Current: m.Version, Latest: s.Latest, CheckedAt: s.CheckedAt, CheckErr: s.CheckErr, Settings: s.Settings,
		Kept: m.keptVersions(), History: []Upgrade{}, Busy: s.Pending != nil}
	if st.Latest != nil {
		l := *st.Latest
		l.Current, l.Available = m.Version, newer(l.Latest, m.Version)
		st.Latest = &l
	}
	for i := len(s.History) - 1; i >= 0; i-- {
		st.History = append(st.History, s.History[i])
	}
	if m.Jobs != nil {
		for _, j := range m.Jobs.List() {
			if (j.Kind == "ike-update" || j.Kind == "ike-rollback") && j.Status == jobs.Running {
				st.Busy = true
			}
		}
	}
	return st
}

// SetSelfSettings changes the update mode or the skipped version.
func (m *Manager) SetSelfSettings(in SelfSettings) (SelfSettings, error) {
	switch in.Mode {
	case "notify", "auto", "off":
	default:
		return SelfSettings{}, fmt.Errorf("mode must be notify, auto or off")
	}
	in.Skip = strings.TrimPrefix(strings.TrimSpace(in.Skip), "v")
	if in.Skip != "" && !versionRe.MatchString(in.Skip) {
		return SelfSettings{}, fmt.Errorf("skip must be a version like 0.7.0")
	}
	s := m.update(func(s *selfState) { s.Settings = in })
	return s.Settings, nil
}

// CheckNow asks GitHub for the newest release and remembers the answer.
func (m *Manager) CheckNow(ctx context.Context) (*Release, error) {
	rel, err := m.CheckIke(ctx)
	if err != nil {
		m.update(func(s *selfState) { s.CheckErr, s.CheckedAt = err.Error(), time.Now().UTC() })
		return nil, err
	}
	m.update(func(s *selfState) { s.Latest, s.CheckedAt, s.CheckErr = rel, time.Now().UTC(), "" })
	return rel, nil
}

// Watch checks for new Ike versions every interval until ctx ends,
// announces each new one once, installs it in auto mode, and announces the
// outcome of updates the watchdog finished while the agent was restarting.
func (m *Manager) Watch(ctx context.Context, interval time.Duration) {
	m.announceOutcomes()
	t := time.NewTimer(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		m.watchOnce(ctx)
		t.Reset(interval)
	}
}

func (m *Manager) watchOnce(ctx context.Context) {
	m.announceOutcomes()
	if m.loadState().Settings.Mode == "off" {
		return
	}
	rel, err := m.CheckNow(ctx)
	if err != nil {
		slog.Debug("ike release check", "err", err)
		return
	}
	if !rel.Available {
		return
	}
	s := m.loadState()
	if s.Settings.Skip != "" && !newer(rel.Latest, s.Settings.Skip) {
		return
	}
	if s.Announced != rel.Latest {
		m.update(func(s *selfState) { s.Announced = rel.Latest })
		m.publish("ike.update.available", "Ike "+rel.Latest, map[string]any{"current": m.Version, "latest": rel.Latest, "url": rel.URL})
	}
	if s.Settings.Mode == "auto" && s.Pending == nil {
		if _, err := m.UpdateIke(ctx, "auto"); err != nil {
			slog.Warn("automatic Ike update didn't start", "err", err)
		}
	}
}

// announceOutcomes publishes the result of finished updates once. The
// watchdog runs outside the agent, so the agent reports for it.
func (m *Manager) announceOutcomes() {
	var todo []Upgrade
	m.update(func(s *selfState) {
		// A safety check that never reported (the server restarted, say):
		// this agent judges by its own version.
		if p := s.Pending; p != nil && time.Since(p.StartedAt) > 15*time.Minute {
			for i := range s.History {
				h := &s.History[i]
				if h.ID != p.ID {
					continue
				}
				h.FinishedAt = time.Now().UTC()
				if m.Version == p.To {
					h.Status = "done"
				} else {
					h.Status, h.Reason = "failed", "the safety check didn't finish; Ike "+m.Version+" is running"
				}
			}
			s.Pending = nil
		}
		for i := range s.History {
			u := &s.History[i]
			if !u.Announced && (u.Status == "done" || u.Status == "rolled_back" || u.Status == "failed") {
				u.Announced = true
				todo = append(todo, *u)
			}
		}
	})
	for _, u := range todo {
		data := map[string]any{"from": u.From, "to": u.To, "by": u.By, "reason": u.Reason}
		switch {
		case u.Status == "done" && u.Kind == "rollback":
			m.publish("ike.rolled_back", "Ike "+u.To, data)
		case u.Status == "done":
			m.publish("ike.updated", "Ike "+u.To, data)
		case u.Status == "rolled_back":
			data["error"] = u.Reason
			m.publish("ike.update.failed", "Ike "+u.To, data)
		default:
			data["error"] = u.Reason
			m.publish("ike.update.failed", "Ike "+u.To, data)
		}
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = io.ReadFull(rand.Reader, b)
	return hex.EncodeToString(b)
}

// UpdateIke installs the newest release (by is who approved it).
func (m *Manager) UpdateIke(ctx context.Context, by string) (*jobs.Job, error) {
	rel, err := m.CheckNow(ctx)
	if err != nil {
		return nil, err
	}
	if !rel.Available {
		return nil, fmt.Errorf("Ike %s is already the newest version", m.Version)
	}
	if !versionRe.MatchString(rel.Latest) {
		return nil, fmt.Errorf("the release is named %q, which isn't a version", rel.Latest)
	}
	if m.loadState().Pending != nil {
		return nil, fmt.Errorf("an update is still being checked; wait a few minutes")
	}
	u := Upgrade{ID: newID(), Kind: "update", From: m.Version, To: rel.Latest, By: by, Status: "installing", StartedAt: time.Now().UTC()}
	return m.Jobs.Start("ike-update", "Update Ike to "+rel.Latest, 30*time.Minute, func(ctx context.Context, log jobs.Logger) error {
		staged, err := m.download(ctx, rel.Latest, log)
		if err != nil {
			m.finish(u, "failed", err.Error())
			return err
		}
		defer os.RemoveAll(staged)
		return m.switchTo(ctx, u, staged, log)
	})
}

// download fetches and checks every binary of a release into a staging folder.
func (m *Manager) download(ctx context.Context, version string, log jobs.Logger) (string, error) {
	staged := filepath.Join(m.releasesDir(), ".staging-"+version)
	_ = os.RemoveAll(staged)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		return "", err
	}
	arch := runtime.GOARCH
	for _, bin := range Binaries {
		url := fmt.Sprintf("%s/%s/releases/download/v%s/%s-linux-%s", m.DownloadBase, m.Repo, version, bin, arch)
		log("Downloading %s %s", bin, version)
		data, err := m.fetch(ctx, url, 200<<20)
		if err != nil {
			if bin != "ike-agent" && bin != "ike-panel" {
				log("No %s in this release; keeping the installed one", bin)
				continue
			}
			return "", err
		}
		sumFile, err := m.fetch(ctx, url+".sha256", 4096)
		if err != nil {
			return "", fmt.Errorf("no checksum published for %s; refusing to install it", bin)
		}
		want := strings.Fields(string(sumFile))
		got := sha256.Sum256(data)
		if len(want) == 0 || !strings.EqualFold(want[0], hex.EncodeToString(got[:])) {
			return "", fmt.Errorf("checksum mismatch for %s; nothing was installed", bin)
		}
		if err := writeFile(filepath.Join(staged, bin), data, 0o755); err != nil {
			return "", err
		}
	}
	log("Checksums verified")
	// The new programs must start on this server and say they are the
	// version we asked for, before anything is replaced.
	for _, bin := range []string{"ike-agent", "ike-panel"} {
		out, err := m.Run.Run(ctx, filepath.Join(staged, bin), "-version")
		if err != nil {
			return "", fmt.Errorf("the new %s doesn't run on this server: %v", bin, err)
		}
		if !m.DryRun && !strings.Contains(string(out), version) {
			return "", fmt.Errorf("the new %s says it is %q, not %s", bin, strings.TrimSpace(string(out)), version)
		}
	}
	log("The new version starts on this server")
	return staged, nil
}

// keepCurrent copies the installed binaries to releases/<version>/.
func (m *Manager) keepCurrent(version string) error {
	dir := filepath.Join(m.releasesDir(), version)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, bin := range Binaries {
		data, err := os.ReadFile(m.binPath(bin))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, bin), data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// install copies the binaries in dir over the installed ones.
func (m *Manager) install(dir string) error {
	for _, bin := range Binaries {
		data, err := os.ReadFile(filepath.Join(dir, bin))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeFile(m.binPath(bin), data, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) prune(keep ...string) {
	kept := m.keptVersions()
	protect := map[string]bool{}
	for _, k := range keep {
		protect[k] = true
	}
	n := 0
	for _, v := range kept {
		if protect[v] {
			continue
		}
		n++
		if n > keepReleases {
			_ = os.RemoveAll(filepath.Join(m.releasesDir(), v))
		}
	}
}

// switchTo keeps the running version, installs dir, starts the watchdog from
// the kept copy and restarts Ike.
func (m *Manager) switchTo(ctx context.Context, u Upgrade, dir string, log jobs.Logger) error {
	if err := m.keepCurrent(u.From); err != nil {
		m.finish(u, "failed", "keep the current version: "+err.Error())
		return err
	}
	log("Kept Ike %s so it can come back", u.From)
	if err := m.install(dir); err != nil {
		// Put back whatever was partly replaced.
		_ = m.install(filepath.Join(m.releasesDir(), u.From))
		m.finish(u, "failed", "install: "+err.Error())
		return err
	}
	u.Status = "checking"
	m.update(func(s *selfState) {
		p := u
		s.Pending = &p
		s.History = append(s.History, u)
	})
	m.prune(u.From, u.To)
	// The watchdog is the version we're leaving: it knows how to come back.
	guard := filepath.Join(m.stateDir(), "releases", u.From, "ike-agent")
	if _, err := m.Run.Run(ctx, "systemd-run", fmt.Sprintf("--on-active=%d", int(verifyAfter.Seconds())), "--unit=ike-upgrade-verify-"+u.ID,
		"--collect", guard, "upgrade-verify", "--state", m.stateDir(), "--socket", m.socket(), "--bin", m.BinDir); err != nil {
		_ = m.install(filepath.Join(m.releasesDir(), u.From))
		m.finish(u, "failed", "couldn't start the safety check: "+err.Error())
		return err
	}
	log("Installed. Restarting Ike; the previous version checks the new one in %d seconds and comes back if it isn't healthy", int(verifyAfter.Seconds()))
	_, err := m.Run.Run(ctx, "systemd-run", "--on-active=3", "--unit=ike-self-restart-"+u.ID, "--collect",
		"systemctl", "restart", "ike-agent", "ike-panel")
	return err
}

func (m *Manager) socket() string {
	if m.Socket == "" {
		return "/run/ike/agent.sock"
	}
	return m.Socket
}

// finish records an update's outcome.
func (m *Manager) finish(u Upgrade, status, reason string) {
	m.update(func(s *selfState) {
		u.Status, u.Reason, u.FinishedAt = status, reason, time.Now().UTC()
		found := false
		for i := range s.History {
			if s.History[i].ID == u.ID {
				s.History[i] = u
				found = true
			}
		}
		if !found {
			s.History = append(s.History, u)
		}
		if s.Pending != nil && s.Pending.ID == u.ID {
			s.Pending = nil
		}
	})
}

// Rollback goes back to a kept version ("" = the newest kept one older than
// the running version, or else the newest kept).
func (m *Manager) Rollback(ctx context.Context, version, by string) (*jobs.Job, error) {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	kept := m.keptVersions()
	if version == "" {
		for _, v := range kept {
			if v != m.Version && newer(m.Version, v) {
				version = v
				break
			}
		}
		if version == "" {
			for _, v := range kept {
				if v != m.Version {
					version = v
					break
				}
			}
		}
	}
	if version == "" {
		return nil, fmt.Errorf("no earlier version of Ike is kept on this server yet")
	}
	if version == m.Version {
		return nil, fmt.Errorf("Ike %s is already running", version)
	}
	ok := false
	for _, v := range kept {
		ok = ok || v == version
	}
	if !ok {
		return nil, fmt.Errorf("Ike %s isn't kept on this server; kept: %s", version, strings.Join(kept, ", "))
	}
	if m.loadState().Pending != nil {
		return nil, fmt.Errorf("an update is still being checked; wait a few minutes")
	}
	u := Upgrade{ID: newID(), Kind: "rollback", From: m.Version, To: version, By: by, Status: "installing", StartedAt: time.Now().UTC()}
	return m.Jobs.Start("ike-rollback", "Go back to Ike "+version, 10*time.Minute, func(ctx context.Context, log jobs.Logger) error {
		src := filepath.Join(m.releasesDir(), version)
		// Work from a copy: switchTo may prune old versions.
		tmp := filepath.Join(m.releasesDir(), ".staging-"+version)
		_ = os.RemoveAll(tmp)
		if err := os.MkdirAll(tmp, 0o700); err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		for _, bin := range Binaries {
			if data, err := os.ReadFile(filepath.Join(src, bin)); err == nil {
				if err := writeFile(filepath.Join(tmp, bin), data, 0o755); err != nil {
					return err
				}
			}
		}
		log("Going back from %s to %s", m.Version, version)
		return m.switchTo(ctx, u, tmp, log)
	})
}

// ---------------------------------------------------------------- watchdog

// VerifyUpgrade runs as `ike-agent upgrade-verify` from the copy of the
// previous version, after an update restarted Ike. It waits for the new agent
// and panel; if they aren't healthy it reinstalls the previous version.
func (m *Manager) VerifyUpgrade(ctx context.Context) error {
	return m.VerifyUpgradeWithin(ctx, 2*time.Minute)
}

// VerifyUpgradeWithin is VerifyUpgrade with its own patience.
func (m *Manager) VerifyUpgradeWithin(ctx context.Context, patience time.Duration) error {
	s := m.loadState()
	u := s.Pending
	if u == nil {
		return nil
	}
	var problem string
	deadline := time.Now().Add(patience)
	for {
		problem = m.healthy(ctx, u.To)
		if problem == "" || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			// Out of time: judge with what we have.
			deadline = time.Time{}
		case <-time.After(5 * time.Second):
		}
	}
	if problem == "" {
		m.finish(*u, "done", "")
		return nil
	}
	slog.Error("new Ike version isn't healthy; going back", "to", u.To, "from", u.From, "problem", problem)
	if err := m.install(filepath.Join(m.releasesDir(), u.From)); err != nil {
		m.finish(*u, "failed", problem+"; going back also failed: "+err.Error())
		return err
	}
	_, _ = m.Run.Run(ctx, "systemctl", "restart", "ike-agent", "ike-panel")
	m.finish(*u, "rolled_back", problem)
	return nil
}

// healthy reports what's wrong with the running Ike, or "" when the agent
// answers with the expected version and the panel service is up.
func (m *Manager) healthy(ctx context.Context, want string) string {
	for _, svc := range []string{"ike-agent", "ike-panel"} {
		out, _ := m.Run.Run(ctx, "systemctl", "is-active", svc)
		if strings.TrimSpace(string(out)) != "active" {
			return svc + " isn't running"
		}
	}
	v, err := m.agentVersion(ctx)
	if err != nil {
		return "the agent doesn't answer: " + err.Error()
	}
	if v != want {
		return fmt.Sprintf("the agent runs %s, not %s", v, want)
	}
	return ""
}

func (m *Manager) agentVersion(ctx context.Context) (string, error) {
	if m.Health != nil {
		return m.Health(ctx)
	}
	c := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", m.p(m.socket()))
		}}}
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://agent/v1/health", nil)
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var h struct {
		Version string `json:"version"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err := json.Unmarshal(bytes.TrimSpace(b), &h); err != nil {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return h.Version, nil
}
