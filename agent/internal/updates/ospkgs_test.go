package updates

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// seqRunner answers dpkg-query from a list of package states, one per call,
// and everything else like DryRun.
type seqRunner struct {
	runner.DryRun
	mu     sync.Mutex
	states []string
	n      int
}

func (s *seqRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	if name == "dpkg-query" {
		s.mu.Lock()
		defer s.mu.Unlock()
		st := s.states[len(s.states)-1]
		if s.n < len(s.states) {
			st = s.states[s.n]
		}
		s.n++
		_, _ = s.DryRun.Run(ctx, name, args...)
		return []byte(st), nil
	}
	return s.DryRun.Run(ctx, name, args...)
}

func TestPackageRunsAndRollback(t *testing.T) {
	before := "ii  openssl 3.0.13-0ubuntu3.1\nii  nginx 1.24.0-2ubuntu7\nii  oldlib 1.0\nrc  gone 0.1\n"
	after := "ii  openssl 3.0.13-0ubuntu3.4\nii  nginx 1.24.0-2ubuntu7.1\nii  newdep 2.0\n"
	back := "ii  openssl 3.0.13-0ubuntu3.1\nii  nginx 1.24.0-2ubuntu7.1\nii  newdep 2.0\n"
	r := &seqRunner{states: []string{before, after, after, back}}
	r.Responses = map[string]string{
		"apt-cache madison openssl": " openssl | 3.0.13-0ubuntu3.4 | http://archive.ubuntu.com noble-updates/main amd64 Packages\n openssl | 3.0.13-0ubuntu3.1 | http://archive.ubuntu.com noble/main amd64 Packages\n",
		"apt-cache madison nginx":   " nginx | 1.24.0-2ubuntu7.1 | http://archive.ubuntu.com noble-updates/main amd64 Packages\n",
	}
	var events []string
	m := &Manager{Run: r, Jobs: jobs.NewManager(), Root: t.TempDir(), StateDir: "/var/lib/ike",
		Publish: func(kind, _ string, _ map[string]any) { events = append(events, kind) }}
	j, err := m.Apply(false, "marvellous")
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Jobs.Wait(j.ID, 3*time.Second); d.Status != jobs.Done {
		t.Fatalf("%+v", d)
	}
	runs := m.PackageRuns()
	if len(runs) != 1 || runs[0].By != "marvellous" || runs[0].Status != "done" {
		t.Fatalf("%+v", runs)
	}
	got := map[string]Change{}
	for _, c := range runs[0].Changes {
		got[c.Name] = c
	}
	if got["openssl"].From != "3.0.13-0ubuntu3.1" || got["openssl"].To != "3.0.13-0ubuntu3.4" || got["newdep"].From != "" || got["oldlib"].To != "" || len(got) != 4 {
		t.Fatalf("changes %+v", runs[0].Changes)
	}

	j, err = m.RollbackPackages(context.Background(), runs[0].ID, "marvellous")
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Jobs.Wait(j.ID, 3*time.Second); d.Status != jobs.Done {
		t.Fatalf("%+v", d)
	}
	cmds := strings.Join(r.History(), "\n")
	if !strings.Contains(cmds, "apt-get install -y -q --allow-downgrades") || !strings.Contains(cmds, "openssl=3.0.13-0ubuntu3.1") || strings.Contains(cmds, "nginx=1.24.0-2ubuntu7 ") {
		t.Errorf("rollback command:\n%s", cmds)
	}
	runs = m.PackageRuns()
	if runs[0].Kind != "rollback" || len(runs[0].Skipped) != 1 || runs[0].Skipped[0] != "nginx" {
		t.Errorf("rollback run %+v", runs[0])
	}
	if runs[1].RolledBackBy != runs[0].ID {
		t.Errorf("original run not marked: %+v", runs[1])
	}
	if _, err := m.RollbackPackages(context.Background(), runs[1].ID, "x"); err == nil {
		t.Error("rolled back twice")
	}
	if !strings.Contains(strings.Join(events, ","), "server.updates.installed") {
		t.Errorf("events %v", events)
	}
}

func TestCheckAnnouncesOnce(t *testing.T) {
	r := &runner.DryRun{Responses: map[string]string{"apt list --upgradable": "openssl/noble-updates,noble-security 3.0.13-0ubuntu3.4 amd64 [upgradable from: 3.0.13-0ubuntu3.1]\n"}}
	var events []string
	m := &Manager{Run: r, Root: t.TempDir(), Publish: func(kind, _ string, _ map[string]any) { events = append(events, kind) }}
	for i := 0; i < 2; i++ {
		if _, err := m.Check(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || events[0] != "server.updates.security" {
		t.Fatalf("events %v", events)
	}
	if st := m.CachedStatus(); st == nil || st.SecurityCount != 1 {
		t.Fatalf("cached %+v", st)
	}
}

func TestReleaseUpgradeNeedsSnapshotAndChecks(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	_ = os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("VERSION_ID=\"24.04\"\nVERSION_CODENAME=noble\n"), 0o644)
	r := &runner.DryRun{Responses: map[string]string{
		"do-release-upgrade -c": "Checking for a new Ubuntu release\nNew release '26.04 LTS' available.\nRun 'do-release-upgrade' to upgrade to it.\n",
		"apt list --upgradable": "",
	}}
	var events []string
	m := &Manager{Run: r, Jobs: jobs.NewManager(), Root: root, DryRun: true, Publish: func(kind, _ string, _ map[string]any) { events = append(events, kind) }}
	rel, err := m.CheckRelease(context.Background())
	if err != nil || rel.Available != "26.04" || !rel.Supported || rel.Current != "24.04" {
		t.Fatalf("%v %+v", err, rel)
	}
	if _, err := m.UpgradeRelease(context.Background(), "x", false); err == nil || !strings.Contains(err.Error(), "snapshot") {
		t.Errorf("without a snapshot: %v", err)
	}
	// Package updates haven't been checked yet: not ready.
	if _, err := m.UpgradeRelease(context.Background(), "x", true); err == nil || !strings.Contains(err.Error(), "package updates") {
		t.Errorf("before checking packages: %v", err)
	}
	if _, err := m.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A third-party source the upgrade switched off.
	src := filepath.Join(root, "etc/apt/sources.list.d/ondrej-php.list")
	_ = os.MkdirAll(filepath.Dir(src), 0o755)
	_ = os.WriteFile(src, []byte("# deb [signed-by=/usr/share/keyrings/php.gpg] https://ppa.launchpadcontent.net/ondrej/php/ubuntu noble main # disabled on upgrade to resolute\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("VERSION_ID=\"24.04\"\nVERSION_CODENAME=resolute\n"), 0o644)
	j, err := m.UpgradeRelease(context.Background(), "marvellous", true)
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Jobs.Wait(j.ID, 3*time.Second); d.Status != jobs.Done {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "do-release-upgrade -f DistUpgradeViewNonInteractive") {
		t.Error("release upgrade didn't run")
	}
	b, _ := os.ReadFile(src)
	if string(b) != "deb [signed-by=/usr/share/keyrings/php.gpg] https://ppa.launchpadcontent.net/ondrej/php/ubuntu resolute main\n" {
		t.Errorf("source not re-enabled: %q", b)
	}
	if _, err := m.RollbackPackages(context.Background(), m.PackageRuns()[0].ID, "x"); err == nil {
		t.Error("a release upgrade can't be rolled back package by package")
	}
	if strings.Count(strings.Join(events, ","), "server.release.available") != 1 {
		t.Errorf("events %v", events)
	}
}
