package sites

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/validate"
)

// Account is one hosting customer: a Linux user with an isolated home that can
// hold any number of websites.
type Account struct {
	Name           string            `json:"name"`
	SSH            string            `json:"ssh"` // none | sftp | shell
	Suspended      bool              `json:"suspended"`
	SuspendReason  string            `json:"suspend_reason,omitempty"`
	PreviousStatus map[string]string `json:"previous_status,omitempty"` // website statuses before suspension
	Cron           []CronJob         `json:"cron"`
	Databases      []Database        `json:"databases"`
	Limits         Limits            `json:"limits"`
	QuotaNote      string            `json:"quota_note,omitempty"`
	Sites          int               `json:"sites"`
	CreatedAt      time.Time         `json:"created_at"`
}

type accountLayout struct {
	Name, Home, WebDir, TmpDir, StateFile string
}

func (m *Manager) accountLayout(name string) accountLayout {
	home := filepath.Join(m.cfg.HomeBase, name)
	return accountLayout{
		Name:      name,
		Home:      home,
		WebDir:    filepath.Join(home, "web"),
		TmpDir:    filepath.Join(home, "tmp"),
		StateFile: filepath.Join(m.cfg.AccountsDir, name+".json"),
	}
}

// CreateAccount adds a Linux user with the standard home layout:
//
//	/home/a        0751 root:a  root-owned so it can be the SFTP chroot; others may only traverse
//	/home/a/web    0750 a:web   websites live here
//	/home/a/tmp    0700 a:a     uploads, sessions
func (m *Manager) CreateAccount(ctx context.Context, name string) (*Account, error) {
	if err := validate.Username(name); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.createAccount(ctx, name)
}

func (m *Manager) createAccount(ctx context.Context, name string) (*Account, error) {
	A := m.accountLayout(name)
	if _, err := os.Stat(m.p(A.StateFile)); err == nil {
		return nil, fmt.Errorf("account %s %w", name, ErrExists)
	}
	if !m.cfg.DryRun {
		if _, err := user.Lookup(name); err == nil {
			return nil, fmt.Errorf("a system user named %s %w", name, ErrExists)
		}
	}

	var undo undoStack
	fail := func(step string, err error) (*Account, error) {
		slog.Error("account create failed, rolling back", "account", name, "step", step, "err", err)
		undo.run()
		return nil, fmt.Errorf("%s: %w", step, err)
	}

	if _, err := m.run.Run(ctx, "useradd", "--create-home", "--home-dir", A.Home,
		"--shell", "/usr/sbin/nologin", "--user-group", name); err != nil {
		return fail("create user", err)
	}
	undo.push(func() { _, _ = m.run.Run(context.Background(), "userdel", "--remove", name) })
	if m.cfg.DryRun {
		undo.push(func() { _ = os.RemoveAll(m.p(A.Home)) })
	}
	if err := m.mkdirs(name, []dirSpec{
		{path: A.Home, mode: 0o751, rootOwned: true},
		{path: A.WebDir, mode: 0o750, webGroup: true},
		{path: A.TmpDir, mode: 0o700},
	}); err != nil {
		return fail("create home folders", err)
	}

	acct := &Account{Name: name, SSH: SSHNone, Cron: []CronJob{}, CreatedAt: time.Now().UTC()}
	if err := m.writeJSON(A.StateFile, acct); err != nil {
		return fail("save account", err)
	}
	slog.Info("account created", "account", name)
	return acct, nil
}

// DeleteAccount removes the Linux user and all files. With withSites, its
// websites are removed first; otherwise an account that still has websites is refused.
func (m *Manager) DeleteAccount(ctx context.Context, name string, withSites bool) error {
	if err := validate.Username(name); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.getAccount(name); err != nil {
		return err
	}
	sites, err := m.sitesOf(name)
	if err != nil {
		return err
	}
	if len(sites) > 0 && !withSites {
		domains := make([]string, len(sites))
		for i, s := range sites {
			domains[i] = s.Domain
		}
		return fmt.Errorf("account %s still has websites (%s); delete them first or pass with_sites", name, strings.Join(domains, ", "))
	}
	for _, s := range sites {
		if err := m.deleteSite(ctx, &s, true); err != nil {
			return fmt.Errorf("delete %s: %w", s.Domain, err)
		}
	}
	m.deleteSFTPUsersFor(ctx, name, "") // they share the account's UID; remove them before it
	A := m.accountLayout(name)
	_ = removeIfExists(m.keysFile(name))
	_ = m.disableCron(name)
	_ = removeIfExists(m.p(m.sliceFile(name)))
	if m.OnAccountDelete != nil {
		m.OnAccountDelete(ctx, name)
	}
	for _, d := range acctDBs(m, name) {
		_ = m.deleteDatabase(ctx, name, d.Name)
	}
	if _, err := m.run.Run(ctx, "userdel", "--remove", name); err != nil {
		return err
	}
	if m.cfg.DryRun {
		_ = os.RemoveAll(m.p(A.Home))
	}
	if err := removeIfExists(m.p(A.StateFile)); err != nil {
		return err
	}
	slog.Info("account deleted", "account", name, "sites_removed", len(sites))
	return nil
}

// Accounts lists all accounts with their website counts.
func (m *Manager) Accounts() ([]Account, error) {
	entries, err := os.ReadDir(m.p(m.cfg.AccountsDir))
	if os.IsNotExist(err) {
		return []Account{}, nil
	}
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	all, _ := m.List()
	for _, s := range all {
		counts[s.Account]++
	}
	out := []Account{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		a, err := m.getAccount(strings.TrimSuffix(e.Name(), ".json"))
		if err != nil {
			continue
		}
		a.Sites = counts[a.Name]
		out = append(out, *a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetAccount returns one account with its website count.
func (m *Manager) GetAccount(name string) (*Account, error) {
	if err := validate.Username(name); err != nil {
		return nil, err
	}
	a, err := m.getAccount(name)
	if err != nil {
		return nil, err
	}
	s, _ := m.sitesOf(name)
	a.Sites = len(s)
	return a, nil
}

func (m *Manager) getAccount(name string) (*Account, error) {
	var a Account
	if err := m.readJSON(m.accountLayout(name).StateFile, &a); err != nil {
		if err == ErrNotFound {
			return nil, fmt.Errorf("account %s %w", name, ErrNotFound)
		}
		return nil, err
	}
	return &a, nil
}

func (m *Manager) saveAccount(a *Account) error {
	a.Sites = 0 // derived, not stored
	return m.writeJSON(m.accountLayout(a.Name).StateFile, a)
}

func acctDBs(m *Manager, name string) []Database {
	a, err := m.getAccount(name)
	if err != nil {
		return nil
	}
	return append([]Database(nil), a.Databases...)
}

func (m *Manager) sitesOf(account string) ([]Site, error) {
	all, err := m.List()
	if err != nil {
		return nil, err
	}
	out := []Site{}
	for _, s := range all {
		if s.Account == account {
			out = append(out, s)
		}
	}
	return out, nil
}
