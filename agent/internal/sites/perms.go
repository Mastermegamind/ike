package sites

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
)

// FixPermissions resets a website's files to the safe defaults after uploads or
// a migration left them wrong:
//
//	public_html, errors   folders 2750, files 0640, owner account:web
//	wp-config.php, .env   0600 (only the site's own PHP can read them)
//	app, logs             owner account:account (file modes left alone, so
//	                      executables in node_modules keep working)
//
// Ownership is changed by root with Lchown, which never follows symlinks.
// Modes are changed by the account user itself, so a planted symlink can at
// worst point at a file the user already controls.
func (m *Manager) FixPermissions(ctx context.Context, domain string) (map[string]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	site, err := m.Get(domain)
	if err != nil {
		return nil, err
	}
	L := m.layout(site)
	stats := map[string]int{"owners_fixed": 0, "skipped_links": 0}

	uid, ugid, wgid := 0, 0, 0
	if !m.cfg.DryRun {
		u, err := user.Lookup(site.Account)
		if err != nil {
			return nil, err
		}
		uid, _ = strconv.Atoi(u.Uid)
		ugid, _ = strconv.Atoi(u.Gid)
		g, err := user.LookupGroup(m.cfg.WebGroup)
		if err != nil {
			return nil, err
		}
		wgid, _ = strconv.Atoi(g.Gid)
	}

	webTrees := []string{L.DocRoot, L.ErrorsDir}
	inWebTree := func(p string) bool {
		for _, t := range webTrees {
			rt := m.p(t)
			if p == rt || strings.HasPrefix(p, rt+"/") {
				return true
			}
		}
		return false
	}

	root := m.p(L.SiteDir)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry: skip, keep going
		}
		if d.Type()&fs.ModeSymlink != 0 {
			stats["skipped_links"]++
			return nil
		}
		gid := ugid
		if path == root || inWebTree(path) {
			gid = wgid
		}
		if !m.cfg.DryRun {
			if err := os.Lchown(path, uid, gid); err == nil {
				stats["owners_fixed"]++
			}
		} else {
			stats["owners_fixed"]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	asUser := func(args ...string) error {
		_, err := m.run.Run(ctx, "runuser", append([]string{"-u", site.Account, "--"}, args...)...)
		return err
	}
	for _, t := range webTrees {
		// find never follows symlinks unless told to (-L), so links are left alone.
		if err := asUser("find", t, "-type", "d", "-exec", "chmod", "2750", "{}", "+"); err != nil {
			return nil, fmt.Errorf("set folder modes: %w", err)
		}
		if err := asUser("find", t, "-type", "f", "-exec", "chmod", "0640", "{}", "+"); err != nil {
			return nil, fmt.Errorf("set file modes: %w", err)
		}
	}
	if err := asUser("find", L.SiteDir, "-type", "f", "(", "-name", "wp-config.php", "-o", "-name", ".env", ")",
		"-exec", "chmod", "0600", "{}", "+"); err != nil {
		return nil, fmt.Errorf("protect config files: %w", err)
	}
	slog.Info("permissions fixed", "domain", domain, "owners", stats["owners_fixed"], "links_skipped", stats["skipped_links"])
	return stats, nil
}
