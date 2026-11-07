package files

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// Snapshots are quick local copies of an account's home made with rsync.
// Unchanged files are hard links to the previous snapshot (--link-dest), so
// each snapshot costs only what changed. They live in
// /var/backups/ike/snapshots/<account>/<time>/, readable by the account so
// restores can run as the account's own user.

var snapRe = regexp.MustCompile(`^\d{8}-\d{6}$`)

// Snapshot is one copy.
type Snapshot struct {
	ID      string    `json:"id"`
	Account string    `json:"account"`
	Time    time.Time `json:"time"`
}

func (m *Manager) snapDir(account string) string {
	return filepath.Join(m.env.Cfg.SnapshotDir, account)
}

// Snapshots lists an account's snapshots, newest first.
func (m *Manager) Snapshots(account string) ([]Snapshot, error) {
	if err := m.env.CheckAccount(account); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(m.env.P(m.snapDir(account)))
	if os.IsNotExist(err) {
		return []Snapshot{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Snapshot{}
	for _, e := range ents {
		if !e.IsDir() || !snapRe.MatchString(e.Name()) {
			continue
		}
		t, _ := time.Parse("20060102-150405", e.Name())
		out = append(out, Snapshot{ID: e.Name(), Account: account, Time: t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// TakeSnapshot copies the account's home and keeps the newest keep snapshots.
func (m *Manager) TakeSnapshot(ctx context.Context, account string, keep int, log func(string, ...any)) (*Snapshot, error) {
	if keep < 1 {
		keep = 7
	}
	prev, err := m.Snapshots(account)
	if err != nil {
		return nil, err
	}
	base := m.snapDir(account)
	id := time.Now().UTC().Format("20060102-150405")
	dest := path.Join(base, id)
	// The account's folder is root:<account> 0750 so the account can restore
	// from it; the parent is 0711 so nobody can list other accounts.
	if _, err := m.env.Run.Run(ctx, "install", "-d", "-m", "0711", "-o", "root", "-g", "root", m.env.Cfg.SnapshotDir); err != nil {
		return nil, err
	}
	if _, err := m.env.Run.Run(ctx, "install", "-d", "-m", "0750", "-o", "root", "-g", account, base); err != nil {
		return nil, err
	}
	args := []string{"-aHAX", "--numeric-ids", "--delete", "--one-file-system",
		"--exclude=/tmp/", "--exclude=/.cache/", "--exclude=*/node_modules/.cache/"}
	if len(prev) > 0 {
		args = append(args, "--link-dest="+path.Join(base, prev[0].ID))
	}
	args = append(args, m.env.Home(account)+"/", dest+"/")
	log("Copying %s (unchanged files are linked to the last snapshot)", m.env.Home(account))
	if _, err := m.env.Run.Exec(ctx, runner.Command{Name: "nice", Args: append([]string{"-n", "15", "ionice", "-c", "3", "rsync"}, args...), Timeout: 6 * time.Hour}); err != nil {
		// 24 = some files vanished while copying: the snapshot is still good
		if !strings.Contains(err.Error(), "exit status 24") {
			_, _ = m.env.Run.Run(context.Background(), "rm", "-rf", "--one-file-system", dest)
			return nil, err
		}
	}
	// keep only the newest
	all, _ := m.Snapshots(account)
	for i, s := range all {
		if i >= keep {
			log("Removing old snapshot %s", s.ID)
			_, _ = m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", path.Join(base, s.ID))
		}
	}
	m.env.Publish("files.snapshot.created", account, map[string]any{"id": id})
	t, _ := time.Parse("20060102-150405", id)
	return &Snapshot{ID: id, Account: account, Time: t}, nil
}

// SnapshotList lists a folder inside a snapshot (as the account user).
func (m *Manager) SnapshotList(ctx context.Context, account, id, rel string) ([]Entry, error) {
	dir, err := m.snapPath(account, id, rel)
	if err != nil {
		return nil, err
	}
	return m.listDir(ctx, account, dir, rel)
}

func (m *Manager) snapPath(account, id, rel string) (string, error) {
	if !snapRe.MatchString(id) {
		return "", fmt.Errorf("snapshot %s %w", id, kit.ErrNotFound)
	}
	abs, err := m.Clean(account, rel)
	if err != nil {
		return "", err
	}
	inner := strings.TrimPrefix(strings.TrimPrefix(abs, m.env.Home(account)), "/")
	return path.Join(m.snapDir(account), id, inner), nil
}

// RestoreFromSnapshot copies a file or folder from a snapshot back into the
// home, as the account user. Without overwrite, existing files are kept.
func (m *Manager) RestoreFromSnapshot(ctx context.Context, account, id, rel string, overwrite bool) error {
	if rel == "" {
		return fmt.Errorf("choose a file or folder to restore")
	}
	src, err := m.snapPath(account, id, rel)
	if err != nil {
		return err
	}
	dst, _ := m.Clean(account, rel)
	args := []string{"-a"}
	if !overwrite {
		args = append(args, "--ignore-existing")
	}
	if _, err := m.as(ctx, account, nil, nil, 0, "test", "-d", src); err == nil {
		src, dst = src+"/", dst+"/"
	}
	if _, err := m.as(ctx, account, nil, nil, 0, "mkdir", "-p", "--", path.Dir(strings.TrimSuffix(dst, "/"))); err != nil {
		return friendly(err, rel)
	}
	_, err = m.as(ctx, account, nil, nil, 6*time.Hour, "rsync", append(args, "--", src, dst)...)
	if err == nil {
		m.env.Publish("files.snapshot.restored", account, map[string]any{"id": id, "path": rel})
	}
	return friendlyIf(err, rel)
}

func (m *Manager) DeleteSnapshot(ctx context.Context, account, id string) error {
	if !snapRe.MatchString(id) {
		return fmt.Errorf("snapshot %s %w", id, kit.ErrNotFound)
	}
	if err := m.env.CheckAccount(account); err != nil {
		return err
	}
	_, err := m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", path.Join(m.snapDir(account), id))
	return err
}
