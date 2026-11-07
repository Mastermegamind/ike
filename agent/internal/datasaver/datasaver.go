// Package datasaver keeps the server's own heavy traffic (scheduled backups,
// automatic Ike and Ubuntu updates) inside a quiet time window and under a
// bandwidth cap, for servers on metered or slow links.
//
// Things a person starts by hand (a backup, an update) still run at once:
// the saver only holds back what Ike would otherwise do on its own.
package datasaver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

// Settings are the saver's choices.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Window is when held-back work may run, in server time ("00:00"-"06:00";
	// it may wrap past midnight). Empty Start means any time (caps only).
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	// Caps in kilobytes per second, 0 = none. Upload applies to backups;
	// download to backups' restores and checks, update downloads and apt.
	UploadKBps   int       `json:"upload_kbps,omitempty"`
	DownloadKBps int       `json:"download_kbps,omitempty"`
	UpdatedAt    time.Time `json:"updated_at,omitzero"`
}

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func minutes(s string) int {
	h, _ := strconv.Atoi(s[:2])
	m, _ := strconv.Atoi(s[3:])
	return h*60 + m
}

// InWindow reports whether t falls inside the window (always true without one).
func (s Settings) InWindow(t time.Time) bool {
	if !s.Enabled || s.Start == "" {
		return true
	}
	now, a, b := t.Hour()*60+t.Minute(), minutes(s.Start), minutes(s.End)
	if a <= b {
		return now >= a && now < b
	}
	return now >= a || now < b // wraps past midnight
}

// NextStart is when the window next opens (t itself when it is open).
func (s Settings) NextStart(t time.Time) time.Time {
	if s.InWindow(t) {
		return t
	}
	a := minutes(s.Start)
	next := time.Date(t.Year(), t.Month(), t.Day(), a/60, a%60, 0, 0, t.Location())
	if !next.After(t) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func (s Settings) validate() error {
	if (s.Start == "") != (s.End == "") {
		return fmt.Errorf("give the window a start and an end, or neither")
	}
	if s.Start != "" && (!hhmm.MatchString(s.Start) || !hhmm.MatchString(s.End) || s.Start == s.End) {
		return fmt.Errorf("the window is two different times like 00:00 and 06:00")
	}
	if s.UploadKBps < 0 || s.DownloadKBps < 0 || s.UploadKBps > 10_000_000 || s.DownloadKBps > 10_000_000 {
		return fmt.Errorf("caps are 0 (none) or kilobytes per second")
	}
	if s.Enabled && s.Start == "" && s.UploadKBps == 0 && s.DownloadKBps == 0 {
		return fmt.Errorf("set a time window or a cap")
	}
	return nil
}

// Manager stores the settings and applies them to apt.
type Manager struct {
	env   *kit.Env
	store kit.Store[Settings]
	Now   func() time.Time // replaced in tests
}

func New(env *kit.Env) *Manager {
	m := &Manager{env: env, Now: time.Now}
	m.store.Path = env.P("/etc/ike/datasaver.json")
	return m
}

// Get returns the settings (zero when never set).
func (m *Manager) Get() Settings {
	s, _ := m.store.Load()
	return s
}

// Allowed says whether held-back work may run now.
func (m *Manager) Allowed() bool { return m.Get().InWindow(m.Now()) }

// ResticFlags are restic's global bandwidth limits (KiB/s).
func (m *Manager) ResticFlags() []string {
	s := m.Get()
	if !s.Enabled {
		return nil
	}
	var out []string
	if s.UploadKBps > 0 {
		out = append(out, "--limit-upload", strconv.Itoa(s.UploadKBps))
	}
	if s.DownloadKBps > 0 {
		out = append(out, "--limit-download", strconv.Itoa(s.DownloadKBps))
	}
	return out
}

// Status is the settings with what they mean right now.
type Status struct {
	Settings
	Open     bool      `json:"open"`               // held-back work may run now
	NextOpen time.Time `json:"next_open,omitzero"` // when the window opens next
}

func (m *Manager) Status() Status {
	s := m.Get()
	now := m.Now()
	st := Status{Settings: s, Open: s.InWindow(now)}
	if !st.Open {
		st.NextOpen = s.NextStart(now)
	}
	return st
}

const (
	aptConf      = "/etc/apt/apt.conf.d/90ike-datasaver"
	timerDropIn  = "/etc/systemd/system/%s.timer.d/ike-datasaver.conf"
	aptDownload  = "apt-daily"
	aptUpgrade   = "apt-daily-upgrade"
	dropInHeader = "# Written by Ike's data saver. Change it in the panel under Updates.\n"
)

// Set saves the settings and applies them: an apt download cap, and Ubuntu's
// automatic update timers moved into the window.
func (m *Manager) Set(ctx context.Context, s Settings) (Status, error) {
	if err := s.validate(); err != nil {
		return Status{}, err
	}
	s.UpdatedAt = kit.Now()
	if _, err := m.store.Update(func(v *Settings) error { *v = s; return nil }); err != nil {
		return Status{}, err
	}
	if err := m.applyApt(ctx, s); err != nil {
		return Status{}, err
	}
	m.env.Publish("server.datasaver.changed", "data saver", map[string]any{"enabled": s.Enabled})
	return m.Status(), nil
}

func (m *Manager) applyApt(ctx context.Context, s Settings) error {
	conf := m.env.P(aptConf)
	if s.Enabled && s.DownloadKBps > 0 {
		body := dropInHeader + fmt.Sprintf("Acquire::http::Dl-Limit \"%d\";\nAcquire::https::Dl-Limit \"%d\";\n", s.DownloadKBps, s.DownloadKBps)
		if err := kit.WriteFile(conf, []byte(body), 0o644); err != nil {
			return err
		}
	} else {
		removed(conf)
	}
	changed := false
	for _, timer := range []string{aptDownload, aptUpgrade} {
		path := m.env.P(fmt.Sprintf(timerDropIn, timer))
		if s.Enabled && s.Start != "" {
			// Run within the window: start at its opening, spread over its length.
			length := (minutes(s.End) - minutes(s.Start) + 24*60) % (24 * 60)
			spread := length - 30
			if spread < 5 {
				spread = 5
			}
			start := s.Start
			if timer == aptUpgrade { // after the lists are fresh
				m := (minutes(s.Start) + 15) % (24 * 60)
				start = fmt.Sprintf("%02d:%02d", m/60, m%60)
			}
			body := dropInHeader + "[Timer]\nOnCalendar=\nOnCalendar=*-*-* " + start + ":00\n" +
				fmt.Sprintf("RandomizedDelaySec=%dm\nPersistent=false\n", spread)
			if err := kit.WriteFile(path, []byte(body), 0o644); err != nil {
				return err
			}
			changed = true
		} else if removed(path) {
			changed = true
		}
	}
	if changed && !m.env.Cfg.DryRun {
		_, _ = m.env.Run.Run(ctx, "systemctl", "daemon-reload")
		_, _ = m.env.Run.Run(ctx, "systemctl", "try-restart", aptDownload+".timer", aptUpgrade+".timer")
	}
	return nil
}

// removed deletes a file Ike wrote (and its drop-in folder when that is
// left empty); it reports whether there was one.
func removed(path string) bool {
	if err := os.Remove(path); err != nil {
		return false
	}
	if strings.HasSuffix(filepath.Dir(path), ".timer.d") {
		_ = os.Remove(filepath.Dir(path))
	}
	return true
}
