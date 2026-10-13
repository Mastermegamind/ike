// Package sites creates and removes hosted websites: one Linux user per site,
// a dedicated PHP-FPM pool and an Nginx server block.
package sites

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

var (
	ErrExists   = errors.New("already exists")
	ErrNotFound = errors.New("site not found")
)

// Site is the stored metadata for one website.
type Site struct {
	Domain    string    `json:"domain"`
	User      string    `json:"user"`
	PHP       string    `json:"php"`
	DocRoot   string    `json:"doc_root"`
	SSH       string    `json:"ssh"` // none | sftp | shell
	CreatedAt time.Time `json:"created_at"`
}

// CreateRequest is what the control plane sends to create a site.
type CreateRequest struct {
	Domain string `json:"domain"`
	User   string `json:"user"`
	PHP    string `json:"php,omitempty"`
}

type Manager struct {
	cfg config.Config
	run runner.Runner
	mu  sync.Mutex
}

func NewManager(cfg config.Config, r runner.Runner) *Manager {
	return &Manager{cfg: cfg, run: r}
}

// p maps an absolute system path into the configured root.
func (m *Manager) p(abs string) string { return filepath.Join(m.cfg.Root, abs) }

type layout struct {
	Domain, User, PHP         string
	Home, SiteDir, DocRoot    string
	TmpDir, LogDir, PHPSocket string
	WebUser, WebGroup         string
	VhostFile, PoolFile       string
	StateFile                 string
}

func (m *Manager) layout(domain, usr, php string) layout {
	home := filepath.Join(m.cfg.HomeBase, usr)
	site := filepath.Join(home, "web", domain)
	return layout{
		Domain: domain, User: usr, PHP: php,
		Home:      home,
		SiteDir:   site,
		DocRoot:   filepath.Join(site, "public_html"),
		TmpDir:    filepath.Join(home, "tmp"),
		LogDir:    filepath.Join("/var/log/ike", domain),
		PHPSocket: fmt.Sprintf("/run/php/php%s-fpm-%s.sock", php, usr),
		WebUser:   m.cfg.WebUser,
		WebGroup:  m.cfg.WebGroup,
		VhostFile: filepath.Join(m.cfg.NginxSitesDir, domain+".conf"),
		PoolFile:  filepath.Join("/etc/php", php, "fpm/pool.d", "ike-"+usr+".conf"),
		StateFile: filepath.Join(m.cfg.StateDir, domain+".json"),
	}
}

// Create provisions a new site. On any failure, everything done so far is undone.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (*Site, error) {
	if req.PHP == "" {
		req.PHP = m.cfg.DefaultPHP
	}
	if err := validate.Domain(req.Domain); err != nil {
		return nil, err
	}
	if err := validate.Username(req.User); err != nil {
		return nil, err
	}
	if err := validate.PHPVersion(req.PHP); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	L := m.layout(req.Domain, req.User, req.PHP)

	if _, err := os.Stat(m.p(L.StateFile)); err == nil {
		return nil, fmt.Errorf("domain %s %w", req.Domain, ErrExists)
	}
	if _, err := os.Stat(m.p(L.VhostFile)); err == nil {
		return nil, fmt.Errorf("nginx config for %s %w", req.Domain, ErrExists)
	}
	if m.userTaken(req.User) {
		return nil, fmt.Errorf("user %s %w", req.User, ErrExists)
	}
	if _, err := os.Stat(m.p(filepath.Join("/etc/php", req.PHP, "fpm"))); err != nil {
		return nil, fmt.Errorf("PHP %s is not installed on this server", req.PHP)
	}

	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}
	fail := func(step string, err error) (*Site, error) {
		slog.Error("site create failed, rolling back", "domain", req.Domain, "step", step, "err", err)
		rollback()
		return nil, fmt.Errorf("%s: %w", step, err)
	}

	// 1. Linux user (no login shell; SFTP access is added in a later phase).
	if _, err := m.run.Run(ctx, "useradd", "--create-home", "--home-dir", L.Home,
		"--shell", "/usr/sbin/nologin", "--user-group", req.User); err != nil {
		return fail("create user", err)
	}
	undo = append(undo, func() { _, _ = m.run.Run(context.Background(), "userdel", "--remove", req.User) })

	// 2. Directories.
	//    /home/u            0751 root:u  root-owned so it can be an SFTP chroot; others may only traverse
	//    /home/u/web/<d>    0750 u:web
	//    public_html        2750 u:web   setgid so new files keep the web group
	//    logs               0750 u:u     PHP error log (written by the site's own PHP user)
	//    tmp                0700 u:u
	dirs := []struct {
		path     string
		mode     os.FileMode
		webGroup bool
	}{
		{L.Home, 0o751, false},
		{filepath.Join(L.Home, "web"), 0o750, true},
		{L.SiteDir, 0o750, true},
		{L.DocRoot, 0o750 | os.ModeSetgid, true},
		{filepath.Join(L.SiteDir, "logs"), 0o750, false},
		{L.TmpDir, 0o700, false},
	}
	for _, d := range dirs {
		if err := os.MkdirAll(m.p(d.path), 0o750); err != nil {
			return fail("create directories", err)
		}
		if err := os.Chmod(m.p(d.path), d.mode); err != nil {
			return fail("set permissions", err)
		}
		if err := m.chown(d.path, req.User, d.webGroup); err != nil {
			return fail("set ownership", err)
		}
	}
	// sshd refuses a chroot that the user can write to, so the home itself belongs to root.
	if err := m.chownRootGroup(L.Home, req.User); err != nil {
		return fail("set home ownership", err)
	}
	idx := filepath.Join(L.DocRoot, "index.html")
	if err := writeFile(m.p(idx), []byte(defaultIndex), 0o640); err != nil {
		return fail("write index.html", err)
	}
	if err := m.chown(idx, req.User, true); err != nil {
		return fail("set ownership", err)
	}
	if m.cfg.DryRun {
		// useradd --remove does nothing in dry-run, so clean the fake home ourselves.
		undo = append(undo, func() { _ = os.RemoveAll(m.p(L.Home)) })
	}

	// 3. Log directory owned by root, outside the user's reach.
	if err := os.MkdirAll(m.p(L.LogDir), 0o750); err != nil {
		return fail("create log directory", err)
	}
	undo = append(undo, func() { _ = os.RemoveAll(m.p(L.LogDir)) })

	// 4. PHP-FPM pool.
	if err := m.render(poolTmpl, L, L.PoolFile); err != nil {
		return fail("write PHP-FPM pool", err)
	}
	undo = append(undo, func() {
		_ = os.Remove(m.p(L.PoolFile))
		_, _ = m.run.Run(context.Background(), "systemctl", "reload", "php"+req.PHP+"-fpm")
	})
	if _, err := m.run.Run(ctx, "php-fpm"+req.PHP, "--test"); err != nil {
		return fail("test PHP-FPM config", err)
	}
	if _, err := m.run.Run(ctx, "systemctl", "reload", "php"+req.PHP+"-fpm"); err != nil {
		return fail("reload PHP-FPM", err)
	}

	// 5. Nginx vhost.
	if err := m.render(nginxTmpl, L, L.VhostFile); err != nil {
		return fail("write nginx config", err)
	}
	undo = append(undo, func() {
		_ = os.Remove(m.p(L.VhostFile))
		_, _ = m.run.Run(context.Background(), "systemctl", "reload", "nginx")
	})
	if _, err := m.run.Run(ctx, "nginx", "-t"); err != nil {
		return fail("test nginx config", err)
	}
	if _, err := m.run.Run(ctx, "systemctl", "reload", "nginx"); err != nil {
		return fail("reload nginx", err)
	}

	// 6. Save metadata.
	site := &Site{Domain: req.Domain, User: req.User, PHP: req.PHP, DocRoot: L.DocRoot, SSH: SSHNone, CreatedAt: time.Now().UTC()}
	if err := m.saveSite(site); err != nil {
		return fail("save site metadata", err)
	}

	slog.Info("site created", "domain", site.Domain, "user", site.User, "php", site.PHP)
	return site, nil
}

// Delete removes a site's web server and PHP config. With removeFiles, the Linux
// user and all of their files are deleted too.
func (m *Manager) Delete(ctx context.Context, domain string, removeFiles bool) error {
	if err := validate.Domain(domain); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	site, err := m.get(domain)
	if err != nil {
		return err
	}
	L := m.layout(site.Domain, site.User, site.PHP)

	if err := removeIfExists(m.p(L.VhostFile)); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "reload", "nginx"); err != nil {
		return err
	}
	if err := removeIfExists(m.p(L.PoolFile)); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "systemctl", "reload", "php"+site.PHP+"-fpm"); err != nil {
		return err
	}
	if err := removeIfExists(m.keysFile(site.User)); err != nil {
		return err
	}
	if _, err := m.run.Run(ctx, "usermod", "--shell", "/usr/sbin/nologin", "--groups", "", site.User); err != nil && !removeFiles {
		return err
	}
	if removeFiles {
		if _, err := m.run.Run(ctx, "userdel", "--remove", site.User); err != nil {
			return err
		}
		if m.cfg.DryRun {
			_ = os.RemoveAll(m.p(L.Home))
		}
		_ = os.RemoveAll(m.p(L.LogDir))
	}
	if err := removeIfExists(m.p(L.StateFile)); err != nil {
		return err
	}
	slog.Info("site deleted", "domain", domain, "files_removed", removeFiles)
	return nil
}

// List returns all sites sorted by domain.
func (m *Manager) List() ([]Site, error) {
	entries, err := os.ReadDir(m.p(m.cfg.StateDir))
	if errors.Is(err, os.ErrNotExist) {
		return []Site{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Site{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := m.get(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			slog.Warn("skipping unreadable site file", "file", e.Name(), "err", err)
			continue
		}
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out, nil
}

// Get returns one site.
func (m *Manager) Get(domain string) (*Site, error) {
	if err := validate.Domain(domain); err != nil {
		return nil, err
	}
	return m.get(domain)
}

func (m *Manager) get(domain string) (*Site, error) {
	b, err := os.ReadFile(m.p(filepath.Join(m.cfg.StateDir, domain+".json")))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var s Site
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

func (m *Manager) userTaken(name string) bool {
	if !m.cfg.DryRun {
		if _, err := user.Lookup(name); err == nil {
			return true
		}
	}
	sites, _ := m.List()
	for _, s := range sites {
		if s.User == name {
			return true
		}
	}
	return false
}

// chown sets owner to the site user and group to either the web server group or
// the user's own group. Skipped in dry-run mode. Uses Lchown so a symlink is never followed.
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

// chownRootGroup makes path owned by root with the site user's own group.
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

func (m *Manager) render(t *template.Template, data layout, dest string) error {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return err
	}
	return writeFile(m.p(dest), buf.Bytes(), 0o644)
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
