package sites

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaravelWorkersSchedulerReverb(t *testing.T) {
	m, r, root := newTestManager(t)
	mustAccount(t, m, "shop")
	s, err := m.Create(ctx, CreateRequest{Domain: "shop.ng", Account: "shop", Runtime: "php", Version: "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	L := m.layout(s)
	// no artisan yet
	if _, _, err := m.SetLaravel(ctx, "shop.ng", &Laravel{Workers: 1}); err == nil || !strings.Contains(err.Error(), "artisan") {
		t.Fatalf("got %v", err)
	}
	_ = os.WriteFile(filepath.Join(root, L.SiteDir, "artisan"), []byte("#!/usr/bin/env php"), 0o644)
	for _, bad := range []*Laravel{{Workers: 9}, {Workers: 1, Queues: "high default"}, {Workers: 1, Path: "../other"}, {Workers: 1, Connection: "a b"}} {
		if _, _, err := m.SetLaravel(ctx, "shop.ng", bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	s, _, err = m.SetLaravel(ctx, "shop.ng", &Laravel{Workers: 2, Queues: "high,default", Tries: 5, Scheduler: true, Reverb: true})
	if err != nil {
		t.Fatal(err)
	}
	if s.Laravel.ReverbDomain != "ws.shop.ng" || s.Laravel.ReverbPort == 0 {
		t.Fatalf("%+v", s.Laravel)
	}
	prefix := filepath.Join(root, "etc/systemd/system", laravelUnitPrefix(L))
	w2 := read(t, root, strings.TrimPrefix(prefix, root)+"-queue-2.service")
	has(t, "worker", w2, "User=shop", "WorkingDirectory="+L.SiteDir, "ExecStart=/usr/bin/php8.3 artisan queue:work --queue=high,default --sleep=3 --tries=5 --timeout=60 --max-time=3600",
		"ProtectSystem=strict", "Slice=ike-acct-shop.slice", "TimeoutStopSec=70")
	has(t, "schedule", read(t, root, strings.TrimPrefix(prefix, root)+"-schedule.service"), "Type=oneshot", "artisan schedule:run")
	has(t, "timer", read(t, root, strings.TrimPrefix(prefix, root)+"-schedule.timer"), "OnCalendar=*-*-* *:*:00")
	has(t, "reverb", read(t, root, strings.TrimPrefix(prefix, root)+"-reverb.service"), "artisan reverb:start --host=127.0.0.1 --port=")
	// Reverb's website forwards to its port
	ws, err := m.Get("ws.shop.ng")
	if err != nil || ws.Runtime != RuntimeProxy || ws.Port != s.Laravel.ReverbPort || ws.Account != "shop" {
		t.Fatalf("%+v %v", ws, err)
	}
	h := strings.Join(r.History(), "\n")
	for _, u := range []string{"-queue-1.service", "-queue-2.service", "-schedule.timer", "-reverb.service"} {
		if !strings.Contains(h, "systemctl restart "+laravelUnitPrefix(L)+u) {
			t.Errorf("%s not started", u)
		}
	}
	// the next app doesn't get Reverb's port
	if p := m.nextPort(); p == s.Laravel.ReverbPort {
		t.Fatalf("next app port %d is Reverb's", p)
	}

	// fewer workers, no Reverb: the extra worker and Reverb's website go
	s, _, err = m.SetLaravel(ctx, "shop.ng", &Laravel{Workers: 1, Scheduler: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prefix + "-queue-2.service"); !os.IsNotExist(err) {
		t.Fatal("second worker left behind")
	}
	if _, err := m.Get("ws.shop.ng"); err == nil {
		t.Fatal("Reverb's website left behind")
	}
	// suspension stops them, reactivation starts them
	if _, err := m.SuspendAccount(ctx, "shop", "test"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "systemctl disable --now "+laravelUnitPrefix(L)+"-queue-1.service") {
		t.Fatal("worker kept running while suspended")
	}
	if _, err := m.UnsuspendAccount(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	// deleting the website removes everything
	if err := m.Delete(ctx, "shop.ng", false); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(prefix + "-*"); len(left) != 0 {
		t.Fatalf("left: %v", left)
	}
	// PHP only
	mustAccount(t, m, "box")
	if _, err := m.Create(ctx, CreateRequest{Domain: "box.ng", Account: "box", Runtime: "static"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.SetLaravel(ctx, "box.ng", &Laravel{Workers: 1}); err == nil {
		t.Fatal("static website accepted")
	}
}
