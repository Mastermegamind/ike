package datasaver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

func at(h, m int) time.Time { return time.Date(2026, 11, 7, h, m, 0, 0, time.Local) }

func TestWindow(t *testing.T) {
	night := Settings{Enabled: true, Start: "23:00", End: "05:30"}
	for _, c := range []struct {
		t    time.Time
		open bool
	}{{at(23, 0), true}, {at(2, 0), true}, {at(5, 29), true}, {at(5, 30), false}, {at(12, 0), false}, {at(22, 59), false}} {
		if got := night.InWindow(c.t); got != c.open {
			t.Errorf("%v: open=%v", c.t.Format("15:04"), got)
		}
	}
	if n := night.NextStart(at(12, 0)); n.Hour() != 23 || n.Day() != 7 {
		t.Fatalf("next %v", n)
	}
	day := Settings{Enabled: true, Start: "01:00", End: "06:00"}
	if n := day.NextStart(at(7, 0)); n.Day() != 8 || n.Hour() != 1 {
		t.Fatalf("next %v", n)
	}
	if !(Settings{Enabled: false, Start: "01:00", End: "02:00"}).InWindow(at(12, 0)) {
		t.Fatal("off saver held work")
	}
}

func TestSetAndApply(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	m := New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}})
	m.Now = func() time.Time { return at(14, 0) }
	ctx := context.Background()
	for _, bad := range []Settings{{Enabled: true}, {Enabled: true, Start: "01:00"}, {Enabled: true, Start: "25:00", End: "02:00"},
		{Enabled: true, Start: "02:00", End: "02:00"}, {Enabled: true, UploadKBps: -1, Start: "01:00", End: "02:00"}} {
		if _, err := m.Set(ctx, bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	st, err := m.Set(ctx, Settings{Enabled: true, Start: "00:00", End: "06:00", UploadKBps: 512, DownloadKBps: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if st.Open || st.NextOpen.Hour() != 0 || m.Allowed() {
		t.Fatalf("%+v", st)
	}
	if f := strings.Join(m.ResticFlags(), " "); f != "--limit-upload 512 --limit-download 1024" {
		t.Fatalf("restic %q", f)
	}
	apt, _ := os.ReadFile(filepath.Join(root, "etc/apt/apt.conf.d/90ike-datasaver"))
	if !strings.Contains(string(apt), `Acquire::http::Dl-Limit "1024";`) {
		t.Fatalf("apt: %s", apt)
	}
	up, _ := os.ReadFile(filepath.Join(root, "etc/systemd/system/apt-daily-upgrade.timer.d/ike-datasaver.conf"))
	if !strings.Contains(string(up), "OnCalendar=\nOnCalendar=*-*-* 00:15:00") || !strings.Contains(string(up), "RandomizedDelaySec=330m") {
		t.Fatalf("timer: %s", up)
	}
	// off: files go, nothing held
	if _, err := m.Set(ctx, Settings{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/apt/apt.conf.d/90ike-datasaver")); !os.IsNotExist(err) {
		t.Fatal("apt cap left behind")
	}
	if _, err := os.Stat(filepath.Join(root, "etc/systemd/system/apt-daily.timer.d")); !os.IsNotExist(err) {
		t.Fatal("timer drop-in folder left behind")
	}
	if !m.Allowed() || m.ResticFlags() != nil {
		t.Fatal("still holding work")
	}
}
