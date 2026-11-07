package updates

import (
	"context"
	"os"
	"path/filepath"
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
	j, err := m.Apply(true, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(false, "test"); err == nil {
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
