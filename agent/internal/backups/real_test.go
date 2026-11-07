package backups

import (
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/secrets"
	"github.com/Mastermegamind/ike/agent/internal/storage"
)

// TestRealRestic backs up, browses, restores and checks with a real restic
// and a local repository. IKE_REAL_RESTIC=1 go test ./internal/backups -run Real
func TestRealRestic(t *testing.T) {
	if os.Getenv("IKE_REAL_RESTIC") == "" {
		t.Skip("set IKE_REAL_RESTIC=1 (needs restic)")
	}
	dir := t.TempDir()
	site := filepath.Join(dir, "site")
	_ = os.MkdirAll(filepath.Join(site, "public_html"), 0o755)
	_ = os.WriteFile(filepath.Join(site, "public_html/index.php"), []byte("<?php echo 'v1';"), 0o644)
	cfg := config.Default()
	cfg.Root, cfg.Hostname, cfg.StorageFile = "/", "srv.test", filepath.Join(dir, "storage.json")
	keys, _ := secrets.Open(filepath.Join(dir, "k"))
	env := &kit.Env{Cfg: cfg, Run: runner.Exec{}, Events: kit.NewBus()}
	st := storage.New(env, keys)
	tg, err := st.Save(context.Background(), "", storage.Target{Name: "disk", Kind: "local", Prefix: strings.TrimPrefix(filepath.Join(dir, "repo"), "/")})
	if err != nil {
		t.Fatal(err)
	}
	var imported []string
	m := New(env, keys, st, Sources{
		SiteDir:   func(string) (string, string, error) { return site, "", nil },
		Databases: func(string) []string { return []string{"mariadb:shop_wp"} },
		Dump: func(_ context.Context, engine, name string, f *os.File) error {
			zw := gzip.NewWriter(f)
			_, _ = zw.Write([]byte("-- dump of " + name))
			return zw.Close()
		},
		ImportDump: func(_ context.Context, acct, engine, name string, f *os.File) error {
			zr, _ := gzip.NewReader(f)
			b, _ := io.ReadAll(zr)
			imported = append(imported, engine+":"+name+":"+string(b))
			return nil
		},
	})
	m.store.Path = filepath.Join(dir, "plans.json")
	ctx := context.Background()
	sp, err := m.Save(ctx, "", "marvellous", PlanRequest{Plan: Plan{Name: "Shop site", Scope: "site", Site: "shop.ng", Target: tg.ID, Enabled: true,
		Databases: []string{"mariadb:shop_wp"}, Retention: Retention{KeepLast: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(sp.RecoveryKey) != 32 {
		t.Fatal("no recovery key")
	}
	log := func(f string, a ...any) { t.Logf(f, a...) }
	r1, err := m.Run(ctx, "", sp.ID, log)
	if err != nil || r1.Snapshot == "" {
		t.Fatalf("%+v %v", r1, err)
	}
	_ = os.WriteFile(filepath.Join(site, "public_html/index.php"), []byte("<?php echo 'v2 broken';"), 0o644)
	if _, err := m.Run(ctx, "", sp.ID, log); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Run(ctx, "", sp.ID, log); err != nil {
		t.Fatal(err)
	}
	snaps, err := m.Snapshots(ctx, "", sp.ID)
	if err != nil || len(snaps) != 2 {
		t.Fatalf("retention keep-last 2: %d %v", len(snaps), err)
	}
	nodes, err := m.Browse(ctx, "", sp.ID, snaps[1].ID, filepath.Join(site, "public_html"))
	if err != nil || len(nodes) != 1 || nodes[0].Name != "index.php" {
		t.Fatalf("browse %+v %v", nodes, err)
	}
	// restore the oldest kept snapshot over the current file, plus the database
	if err := m.Restore(ctx, "", sp.ID, RestoreRequest{Snapshot: snaps[1].ID, Paths: []string{filepath.Join(site, "public_html")}, Overwrite: true, Databases: []string{"*"}}, log); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(site, "public_html/index.php"))
	if !strings.Contains(string(b), "v2") {
		// snaps[1] is the second-newest: both kept snapshots have v2
		t.Fatalf("restored %q", b)
	}
	if len(imported) != 1 || !strings.Contains(imported[0], "-- dump of shop_wp") {
		t.Fatalf("db import %v", imported)
	}
	if err := m.Check(ctx, "", sp.ID, log); err != nil {
		t.Fatal(err)
	}
	key, err := m.RecoveryKey("", sp.ID)
	if err != nil || key["password"] != sp.RecoveryKey || !strings.HasSuffix(key["repository"], "/ike/srv.test/"+sp.ID) {
		t.Fatalf("%v %v", key, err)
	}
	plans, _ := m.Plans("")
	if plans[0].Last.Status != "ok" || plans[0].LastCheck.Status != "ok" || plans[0].Password != "" {
		t.Fatalf("%+v", plans[0])
	}
}
