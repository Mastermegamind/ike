// Package sites manages hosting accounts and their websites.
//
//	server
//	└── account  (one Linux user, isolated home, SSH/SFTP access, keys)
//	    └── website  (Nginx vhost, optional Apache vhost, and a runtime:
//	                  PHP-FPM pool, Node.js/Python app service, or static files)
package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"text/template"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

var (
	ErrExists   = kit.ErrExists
	ErrNotFound = kit.ErrNotFound
)

// AccountExists lets feature packages check an account without loading it.
func (m *Manager) AccountExists(name string) error {
	_, err := m.GetAccount(name)
	return err
}

// Config returns the agent configuration.
func (m *Manager) Config() config.Config { return m.cfg }

type Manager struct {
	cfg config.Config
	run runner.Runner
	mu  sync.Mutex // serializes every change to accounts and sites
	web WebFeatures

	histMu sync.Mutex // deployment history files
	dq     *deployQueue
	// healthy checks a freshly started app or container before traffic moves
	// to it. Tests replace it.
	healthy func(ctx context.Context, port int, host, path string) error
	// notify posts deployment events; tests replace it.
	notify func(ctx context.Context, url, secret string, body []byte) error

	// OnAccountDelete removes what other features keep for an account (mail,
	// DNS zones, PostgreSQL, Redis). Set by the API server.
	OnAccountDelete func(ctx context.Context, account string)
}

func NewManager(cfg config.Config, r runner.Runner) *Manager {
	m := &Manager{cfg: cfg, run: r, dq: newDeployQueue()}
	m.healthy, m.notify = waitHealthy, postSigned
	if cfg.DryRun {
		m.healthy = func(context.Context, int, string, string) error { return nil }
		m.notify = func(context.Context, string, string, []byte) error { return nil }
	}
	return m
}

// p maps an absolute system path into the configured root.
func (m *Manager) p(abs string) string { return filepath.Join(m.cfg.Root, abs) }

// chown sets owner to the account user and group to either the web server group
// or the user's own group. Skipped in dry-run mode. Uses Lchown so a symlink is
// never followed.
func (m *Manager) chown(path, usr string, webGroup bool) error {
	if m.cfg.DryRun {
		return nil
	}
	u, err := user.Lookup(usr)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if webGroup {
		g, err := user.LookupGroup(m.cfg.WebGroup)
		if err != nil {
			return fmt.Errorf("web server group %q: %w", m.cfg.WebGroup, err)
		}
		gid, _ = strconv.Atoi(g.Gid)
	}
	return os.Lchown(m.p(path), uid, gid)
}

// chownRootGroup makes path owned by root with the user's own group.
func (m *Manager) chownRootGroup(path, usr string) error {
	if m.cfg.DryRun {
		return nil
	}
	u, err := user.Lookup(usr)
	if err != nil {
		return err
	}
	gid, _ := strconv.Atoi(u.Gid)
	return os.Lchown(m.p(path), 0, gid)
}

// mkdirs creates directories with exact modes and ownership.
func (m *Manager) mkdirs(owner string, dirs []dirSpec) error {
	for _, d := range dirs {
		if err := os.MkdirAll(m.p(d.path), 0o750); err != nil {
			return err
		}
		if err := os.Chmod(m.p(d.path), d.mode); err != nil {
			return err
		}
		var err error
		if d.rootOwned {
			err = m.chownRootGroup(d.path, owner)
		} else {
			err = m.chown(d.path, owner, d.webGroup)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

type dirSpec struct {
	path      string
	mode      os.FileMode
	webGroup  bool
	rootOwned bool
}

func (m *Manager) render(t *template.Template, data any, dest string, mode os.FileMode) error {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	return writeFile(m.p(dest), buf.Bytes(), mode)
}

func (m *Manager) readJSON(path string, v any) error {
	b, err := os.ReadFile(m.p(path))
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func (m *Manager) writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return writeFile(m.p(path), b, 0o600)
}

// writeFile writes atomically: temp file in the same directory, then rename.
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

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// undoStack runs cleanup steps in reverse order when an operation fails halfway.
type undoStack []func()

func (u *undoStack) push(f func()) { *u = append(*u, f) }
func (u undoStack) run() {
	for i := len(u) - 1; i >= 0; i-- {
		u[i]()
	}
}
