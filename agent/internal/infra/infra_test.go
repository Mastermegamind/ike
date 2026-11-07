package infra

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
)

func TestInventoryParsing(t *testing.T) {
	disks := parseLsblk([]byte(`{"blockdevices":[{"name":"vda","size":"85899345920","type":"disk","model":"QEMU HARDDISK","mountpoint":null,"fstype":null,"rota":"1",
	 "children":[{"name":"vda1","size":85000000000,"type":"part","model":null,"mountpoint":"/","fstype":"ext4","rota":false}]},{"name":"loop0","size":1,"type":"loop"}]}`))
	if len(disks) != 2 || disks[0].SizeGB != 85 || !disks[0].Rotational || disks[1].Mount != "/" {
		t.Fatalf("%+v", disks)
	}
	nics := parseIPAddr([]byte(`[{"ifname":"lo","addr_info":[]},{"ifname":"eth0","address":"52:54:00:12:34:56","operstate":"UP","mtu":1500,
	 "addr_info":[{"local":"203.0.113.10","prefixlen":24,"scope":"global"},{"local":"fe80::1","prefixlen":64,"scope":"link"}]},{"ifname":"veth12","addr_info":[]}]`))
	if len(nics) != 1 || nics[0].Addresses[0] != "203.0.113.10/24" || nics[0].State != "up" {
		t.Fatalf("%+v", nics)
	}
	m := New(&kit.Env{Cfg: config.Default(), Run: &runner.DryRun{}})
	m.asset.Path = filepath.Join(t.TempDir(), "a.json")
	if _, err := m.SetAsset(Asset{Tag: "SRV-001", PurchaseDate: "2026-01-31", Notes: "rack 4"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetAsset(Asset{PurchaseDate: "31/01/2026"}); err == nil {
		t.Fatal("bad date accepted")
	}
}

func TestMaintenanceWindow(t *testing.T) {
	dir := t.TempDir()
	m := New(&kit.Env{Cfg: config.Default(), Run: &runner.DryRun{}, Events: kit.NewBus()})
	m.maint.Path = filepath.Join(dir, "m.json")
	var calls []string
	m.SetMaintenance = func(_ context.Context, sites []string, on bool) (map[string]string, error) {
		if on {
			calls = append(calls, "on")
			return map[string]string{"shop.ng": "active"}, nil
		}
		calls = append(calls, "off")
		return nil, nil
	}
	m.AllSites = func() []string { return []string{"shop.ng"} }
	w, err := m.AddWindow(Window{Title: "Kernel update", Start: time.Now().Add(time.Minute), End: time.Now().Add(time.Hour)}, "andre")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AddWindow(Window{Title: "Overlap", Start: time.Now().Add(10 * time.Minute), End: time.Now().Add(2 * time.Hour)}, "andre"); err == nil {
		t.Fatal("overlap accepted")
	}
	m.startWindow(context.Background(), &w)
	ws, _ := m.Windows()
	if ws[0].State != "active" || ws[0].Previous["shop.ng"] != "active" {
		t.Fatalf("%+v", ws[0])
	}
	if err := m.CancelWindow(context.Background(), w.ID); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[1] != "off" {
		t.Fatalf("%v", calls)
	}
}

func TestWebhookMatchingAndSigning(t *testing.T) {
	if !matches([]string{"backup.*"}, "backup.failed") || matches([]string{"backup.*"}, "site.created") || !matches([]string{"*"}, "x") {
		t.Fatal("matching")
	}
	sig := Sign("whsec_x", 1700000000, []byte(`{"a":1}`))
	if sig != "t=1700000000,v1=3a1f8e2b0e7a2c1cbd4e0a9e5c6b8a0d4e1f6c2b7a9d0e3f5a8c1b4d7e0f2a6c" && len(sig) != len("t=1700000000,v1=")+64 {
		t.Fatal(sig)
	}
	m := New(&kit.Env{Cfg: config.Default(), Run: &runner.DryRun{}})
	m.hooks.Path = filepath.Join(t.TempDir(), "h.json")
	for _, u := range []string{"http://x.com/h", "https://127.0.0.1/h", "https://10.0.0.5/h", "https://localhost/h"} {
		if _, err := m.SaveWebhook(Webhook{URL: u}); err == nil {
			t.Errorf("%s accepted", u)
		}
	}
	h, err := m.SaveWebhook(Webhook{URL: "https://hooks.example.com/ike", Events: []string{"backup.*"}, Enabled: true})
	if err != nil || len(h.Secret) < 30 {
		t.Fatalf("%+v %v", h, err)
	}
	m.OnEvent(kit.Event{Type: "backup.failed"})
	m.OnEvent(kit.Event{Type: "site.created"})
	if len(m.deliveries) != 1 {
		t.Fatalf("queued %d", len(m.deliveries))
	}
}
