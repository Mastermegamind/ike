package sites

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/YOUR-ORG/ike/agent/internal/validate"
)

// SuspendAccount takes an account offline: every website shows the suspended
// page, apps stop, cron jobs stop and SSH/SFTP logins are blocked. Each
// website's previous status is remembered and restored by UnsuspendAccount.
func (m *Manager) SuspendAccount(ctx context.Context, name, reason string) (*Account, error) {
	if err := validate.Username(name); err != nil {
		return nil, err
	}
	if len(reason) > 200 {
		return nil, fmt.Errorf("reason must be under 200 characters")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(name)
	if err != nil {
		return nil, err
	}
	if a.Suspended {
		return a, nil
	}
	sites, err := m.sitesOf(name)
	if err != nil {
		return nil, err
	}
	a.PreviousStatus = map[string]string{}
	for i := range sites {
		s := &sites[i]
		a.PreviousStatus[s.Domain] = statusOf(s.Status)
		if statusOf(s.Status) == StatusSuspended {
			continue
		}
		if _, err := m.setStatus(ctx, s, StatusSuspended, nil); err != nil {
			return nil, fmt.Errorf("suspend %s: %w", s.Domain, err)
		}
	}
	// Block logins but remember the SSH mode for later.
	if _, err := m.run.Run(ctx, "usermod", "--shell", "/usr/sbin/nologin", "--groups", "", name); err != nil {
		return nil, err
	}
	if err := m.disableCron(name); err != nil {
		return nil, err
	}
	a.Suspended, a.SuspendReason = true, reason
	if err := m.saveAccount(a); err != nil {
		return nil, err
	}
	slog.Info("account suspended", "account", name, "sites", len(sites))
	return a, nil
}

// UnsuspendAccount restores websites, SSH access and cron jobs.
func (m *Manager) UnsuspendAccount(ctx context.Context, name string) (*Account, error) {
	if err := validate.Username(name); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, err := m.getAccount(name)
	if err != nil {
		return nil, err
	}
	if !a.Suspended {
		return a, nil
	}
	sites, err := m.sitesOf(name)
	if err != nil {
		return nil, err
	}
	for i := range sites {
		s := &sites[i]
		prev := a.PreviousStatus[s.Domain]
		if prev == "" {
			prev = StatusActive
		}
		if prev == statusOf(s.Status) {
			continue
		}
		if _, err := m.setStatus(ctx, s, prev, s.AllowIPs); err != nil {
			return nil, fmt.Errorf("reactivate %s: %w", s.Domain, err)
		}
	}
	shell, groups := "/usr/sbin/nologin", ""
	switch a.SSH {
	case SSHSFTP:
		groups = groupSFTP
	case SSHShell:
		shell, groups = "/bin/bash", groupSSH
	}
	if _, err := m.run.Run(ctx, "usermod", "--shell", shell, "--groups", groups, name); err != nil {
		return nil, err
	}
	a.Suspended, a.SuspendReason, a.PreviousStatus = false, "", nil
	if err := m.writeCron(name); err != nil {
		return nil, err
	}
	if err := m.saveAccount(a); err != nil {
		return nil, err
	}
	slog.Info("account reactivated", "account", name)
	return a, nil
}
