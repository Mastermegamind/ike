package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

// fakeRelease serves a GitHub release of version with the given binaries.
type fakeRelease struct {
	mu      sync.Mutex
	version string
	bins    map[string][]byte
	badSum  bool
}

func (f *fakeRelease) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/releases/latest") {
			_, _ = w.Write([]byte(`{"tag_name":"v` + f.version + `","html_url":"https://example/rel","body":"Fixes"}`))
			return
		}
		for name, data := range f.bins {
			file := "/releases/download/v" + f.version + "/" + name + "-linux-" + runtime.GOARCH
			sum := sha256.Sum256(data)
			switch {
			case strings.HasSuffix(r.URL.Path, file+".sha256"):
				s := hex.EncodeToString(sum[:])
				if f.badSum {
					s = strings.Repeat("0", 64)
				}
				_, _ = w.Write([]byte(s + "  " + name + "\n"))
				return
			case strings.HasSuffix(r.URL.Path, file):
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
	})
}

func newSelf(t *testing.T, version string, rel *fakeRelease) (*Manager, *runner.DryRun, string, *[]string) {
	t.Helper()
	srv := httptest.NewServer(rel.handler())
	t.Cleanup(srv.Close)
	root := t.TempDir()
	r := &runner.DryRun{Responses: map[string]string{"systemctl is-active": "active\n"}}
	var events []string
	var mu sync.Mutex
	m := &Manager{Run: r, Jobs: jobs.NewManager(), Root: root, DryRun: true, Version: version, Repo: "acme/ike",
		APIBase: srv.URL, DownloadBase: srv.URL, BinDir: "/usr/local/bin", StateDir: "/var/lib/ike",
		Publish: func(kind, subject string, _ map[string]any) { mu.Lock(); events = append(events, kind); mu.Unlock() }}
	for _, b := range Binaries {
		if err := writeFile(m.binPath(b), []byte(b+" "+version), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return m, r, root, &events
}

func bins(version string) map[string][]byte {
	out := map[string][]byte{}
	for _, b := range Binaries {
		out[b] = []byte(b + " " + version)
	}
	return out
}

func installedVersion(t *testing.T, m *Manager) string {
	t.Helper()
	b, err := os.ReadFile(m.binPath("ike-agent"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimPrefix(string(b), "ike-agent ")
}

func TestUpdateVerifiedThenRollback(t *testing.T) {
	rel := &fakeRelease{version: "0.7.0", bins: bins("0.7.0")}
	m, r, _, events := newSelf(t, "0.6.0", rel)

	// Watch announces a new version once (notify mode installs nothing).
	m.watchOnce(context.Background())
	m.watchOnce(context.Background())
	if n := strings.Count(strings.Join(*events, ","), "ike.update.available"); n != 1 {
		t.Fatalf("announced %d times: %v", n, *events)
	}
	if installedVersion(t, m) != "0.6.0" {
		t.Fatal("notify mode installed the update")
	}

	j, err := m.UpdateIke(context.Background(), "marvellous")
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Jobs.Wait(j.ID, 5*time.Second); d.Status != jobs.Done {
		t.Fatalf("%+v", d)
	}
	if installedVersion(t, m) != "0.7.0" {
		t.Fatal("new version not installed")
	}
	if got := m.keptVersions(); len(got) != 1 || got[0] != "0.6.0" {
		t.Fatalf("kept %v", got)
	}
	cmds := strings.Join(r.History(), "\n")
	for _, want := range []string{"-version", "systemd-run --on-active=90 --unit=ike-upgrade-verify-", "/var/lib/ike/releases/0.6.0/ike-agent upgrade-verify",
		"systemctl restart ike-agent ike-panel"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("missing %q in\n%s", want, cmds)
		}
	}
	if _, err := m.UpdateIke(context.Background(), "x"); err == nil {
		t.Error("a second update started while the first is checked")
	}

	// The watchdog (the old version) finds the new one healthy.
	m.Health = func(context.Context) (string, error) { return "0.7.0", nil }
	if err := m.VerifyUpgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	st := m.SelfStatus()
	if st.Busy || st.History[0].Status != "done" || st.History[0].By != "marvellous" {
		t.Fatalf("%+v", st.History)
	}
	// The new agent announces the outcome after it starts.
	m2 := *m
	m2.Version = "0.7.0"
	m2.announceOutcomes()
	if !strings.Contains(strings.Join(*events, ","), "ike.updated") {
		t.Errorf("events %v", *events)
	}

	// Rolling back by hand goes to the kept 0.6.0 and keeps 0.7.0.
	j, err = m2.Rollback(context.Background(), "", "marvellous")
	if err != nil {
		t.Fatal(err)
	}
	if d := m2.Jobs.Wait(j.ID, 5*time.Second); d.Status != jobs.Done {
		t.Fatalf("%+v", d)
	}
	if installedVersion(t, m) != "0.6.0" {
		t.Fatal("rollback didn't put 0.6.0 back")
	}
	if got := m.keptVersions(); len(got) != 2 {
		t.Errorf("kept %v", got)
	}
	m2.Health = func(context.Context) (string, error) { return "0.6.0", nil }
	if err := m2.VerifyUpgrade(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h := m2.SelfStatus().History[0]; h.Kind != "rollback" || h.Status != "done" {
		t.Errorf("%+v", h)
	}
	if _, err := m2.Rollback(context.Background(), "0.1.0", "x"); err == nil {
		t.Error("rollback to a version that isn't kept")
	}
}

func TestUnhealthyUpdateRollsBack(t *testing.T) {
	rel := &fakeRelease{version: "0.7.0", bins: bins("0.7.0")}
	m, _, _, events := newSelf(t, "0.6.0", rel)
	j, err := m.UpdateIke(context.Background(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	m.Jobs.Wait(j.ID, 5*time.Second)
	// The new agent never comes up.
	m.Health = func(context.Context) (string, error) { return "", errors.New("connection refused") }
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = m.VerifyUpgradeWithin(ctx, 0)
	if installedVersion(t, m) != "0.6.0" {
		t.Fatal("unhealthy update was not rolled back")
	}
	h := m.SelfStatus().History[0]
	if h.Status != "rolled_back" || !strings.Contains(h.Reason, "doesn't answer") {
		t.Fatalf("%+v", h)
	}
	m.announceOutcomes()
	if !strings.Contains(strings.Join(*events, ","), "ike.update.failed") {
		t.Errorf("events %v", *events)
	}
}

func TestBadChecksumInstallsNothing(t *testing.T) {
	rel := &fakeRelease{version: "0.7.0", bins: bins("0.7.0"), badSum: true}
	m, _, _, _ := newSelf(t, "0.6.0", rel)
	j, err := m.UpdateIke(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Jobs.Wait(j.ID, 5*time.Second); d.Status != jobs.Failed || !strings.Contains(d.Error, "checksum") {
		t.Fatalf("%+v", d)
	}
	if installedVersion(t, m) != "0.6.0" || len(m.keptVersions()) != 0 {
		t.Error("something was installed despite a bad checksum")
	}
	if h := m.SelfStatus().History[0]; h.Status != "failed" {
		t.Errorf("%+v", h)
	}
}

func TestAutoModeAndSkip(t *testing.T) {
	rel := &fakeRelease{version: "0.7.0", bins: bins("0.7.0")}
	m, _, _, _ := newSelf(t, "0.6.0", rel)
	if _, err := m.SetSelfSettings(SelfSettings{Mode: "auto", Skip: "0.7.0"}); err != nil {
		t.Fatal(err)
	}
	m.watchOnce(context.Background())
	if installedVersion(t, m) != "0.6.0" {
		t.Fatal("a skipped version was installed")
	}
	if _, err := m.SetSelfSettings(SelfSettings{Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	m.watchOnce(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	for installedVersion(t, m) != "0.7.0" && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if installedVersion(t, m) != "0.7.0" {
		t.Fatal("auto mode didn't install")
	}
	if _, err := m.SetSelfSettings(SelfSettings{Mode: "sometimes"}); err == nil {
		t.Error("bad mode accepted")
	}
}

func TestPlaceholderRepoRefused(t *testing.T) {
	m := &Manager{Repo: "YOUR-" + "ORG/ike"}
	if _, err := m.CheckIke(context.Background()); err == nil {
		t.Error("placeholder repo should be refused")
	}
}

func TestKeepsThreeOlderVersions(t *testing.T) {
	m, _, _, _ := newSelf(t, "0.6.0", &fakeRelease{})
	for _, v := range []string{"0.1.0", "0.2.0", "0.3.0", "0.4.0", "0.5.0"} {
		_ = writeFile(filepath.Join(m.releasesDir(), v, "ike-agent"), []byte(v), 0o755)
	}
	m.prune("0.6.0")
	if got := strings.Join(m.keptVersions(), ","); got != "0.5.0,0.4.0,0.3.0" {
		t.Errorf("kept %s", got)
	}
}
