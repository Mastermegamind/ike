package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

func TestParseUpgradable(t *testing.T) {
	out := `Listing... Done
openssl/noble-updates,noble-security 3.0.13-0ubuntu3.4 amd64 [upgradable from: 3.0.13-0ubuntu3.1]
nginx/stable 1.28.0-1~noble amd64 [upgradable from: 1.26.2-1~noble]
garbage line
`
	p := parseUpgradable(out)
	if len(p) != 2 || !p[0].Security || p[1].Security || p[1].From != "1.26.2-1~noble" || p[1].To != "1.28.0-1~noble" {
		t.Fatalf("%+v", p)
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"v0.3.0", "0.2.9", true}, {"0.2.0", "0.2.0", false}, {"v1.0.0-rc1", "0.9.9", true}, {"0.1.9", "0.2.0", false}, {"v0.10.0", "0.9.0", true}}
	for _, c := range cases {
		if newer(c.a, c.b) != c.want {
			t.Errorf("newer(%s, %s) != %v", c.a, c.b, c.want)
		}
	}
}

func TestApplyAndAutoSettings(t *testing.T) {
	root := t.TempDir()
	r := &runner.DryRun{}
	m := &Manager{Run: r, Jobs: jobs.NewManager(), Root: root}
	j, err := m.Apply(true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(false); err == nil {
		// may already be done in a fast run; only fail if still running
		if jj, _ := m.Jobs.Get(j.ID); jj.Status == jobs.Running {
			t.Error("second upgrade allowed while one runs")
		}
	}
	done := m.Jobs.Wait(j.ID, 2*time.Second)
	if done.Status != jobs.Done {
		t.Fatalf("job %+v", done)
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "unattended-upgrade -v") {
		t.Error("security-only must use unattended-upgrade")
	}

	if err := m.SetAutoSettings(AutoSettings{SecurityUpdates: true, AutoReboot: true, RebootTime: "04:15"}); err != nil {
		t.Fatal(err)
	}
	s := m.AutoSettings()
	if !s.SecurityUpdates || !s.AutoReboot || s.RebootTime != "04:15" {
		t.Errorf("%+v", s)
	}
	if err := m.SetAutoSettings(AutoSettings{RebootTime: "25:00"}); err == nil {
		t.Error("bad time accepted")
	}
	if err := os.MkdirAll(filepath.Join(root, "var/run"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "var/run/reboot-required"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(root, "var/run/reboot-required.pkgs"), []byte("linux-image-6.8.0-50-generic\n"), 0o644)
	if ok, pk := m.rebootRequired(); !ok || pk[0] != "linux-image-6.8.0-50-generic" {
		t.Error("reboot-required not detected")
	}
	if err := m.ScheduleReboot(context.Background(), 0); err == nil {
		t.Error("zero-minute reboot accepted")
	}
}

func TestSelfUpdate(t *testing.T) {
	bin := []byte("new-agent-binary")
	sum := sha256.Sum256(bin)
	bad := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			w.Write([]byte(`{"tag_name":"v0.9.0","html_url":"https://example/rel","body":"Fixes"}`))
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			s := hex.EncodeToString(sum[:])
			if bad {
				s = strings.Repeat("0", 64)
			}
			w.Write([]byte(s + "  file\n"))
		case strings.Contains(r.URL.Path, "ike-panel"):
			http.NotFound(w, r)
		case strings.HasSuffix(r.URL.Path, "-linux-"+runtime.GOARCH):
			w.Write(bin)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	root := t.TempDir()
	r := &runner.DryRun{}
	m := &Manager{Run: r, Jobs: jobs.NewManager(), Root: root, Version: "0.3.0", Repo: "acme/ike",
		APIBase: srv.URL, DownloadBase: srv.URL, BinDir: "/usr/local/bin"}

	rel, err := m.CheckIke(context.Background())
	if err != nil || !rel.Available || rel.Latest != "0.9.0" {
		t.Fatalf("%v %+v", err, rel)
	}
	j, err := m.UpdateIke(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Jobs.Wait(j.ID, 3*time.Second); d.Status != jobs.Done {
		t.Fatalf("%+v", d)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "usr/local/bin/ike-agent")); string(b) != string(bin) {
		t.Error("binary not installed")
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "systemd-run --on-active=3") {
		t.Error("restart not scheduled")
	}

	bad = true
	_ = os.Remove(filepath.Join(root, "usr/local/bin/ike-agent"))
	j, _ = m.UpdateIke(context.Background())
	if d := m.Jobs.Wait(j.ID, 3*time.Second); d.Status != jobs.Failed || !strings.Contains(d.Error, "checksum") {
		t.Fatalf("bad checksum must fail: %+v", d)
	}
	if _, err := os.Stat(filepath.Join(root, "usr/local/bin/ike-agent")); !os.IsNotExist(err) {
		t.Error("binary installed despite bad checksum")
	}

	m.Repo = "Mastermegamind/ike"
	if _, err := m.CheckIke(context.Background()); err == nil {
		t.Error("placeholder repo should be refused")
	}
}
