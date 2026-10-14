package sites

import (
	"context"
	"os/user"
)

// EnsureAccountUser recreates the Linux user and home folders for an account
// whose state came back from a backup (moving to a new server). Existing
// users are left as they are.
func (m *Manager) EnsureAccountUser(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	A := m.accountLayout(name)
	if !m.cfg.DryRun {
		if _, err := user.Lookup(name); err != nil {
			if _, err := m.run.Run(ctx, "useradd", "--create-home", "--home-dir", A.Home,
				"--shell", "/usr/sbin/nologin", "--user-group", name); err != nil {
				return err
			}
		}
	} else {
		_, _ = m.run.Run(ctx, "useradd", "--create-home", "--home-dir", A.Home, "--shell", "/usr/sbin/nologin", "--user-group", name)
	}
	return m.mkdirs(name, []dirSpec{
		{path: A.Home, mode: 0o751, rootOwned: true},
		{path: A.WebDir, mode: 0o750, webGroup: true},
		{path: A.TmpDir, mode: 0o700},
	})
}

// RebuildAll rewrites every file Ike manages from its saved state and reloads
// the services (after restoring a server from backup).
func (m *Manager) RebuildAll(ctx context.Context) ([]Drift, error) {
	return m.Reconcile(ctx, nil)
}
