package databases

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/kit"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
)

func TestRedisPerAccount(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun, cfg.Redis = root, true, "valkey"
	run := &runner.DryRun{Responses: map[string]string{"valkey-cli": "# Server\nvalkey_version:8.0.2\nuptime_in_seconds:7200\nused_memory:2097152\nconnected_clients:3\nkeyspace_hits:90\nkeyspace_misses:10\ndb0:keys=12,expires=0,avg_ttl=0\n"}}
	m := New(&kit.Env{Cfg: cfg, Run: run})
	ctx := context.Background()
	cred, inst, err := m.SetRedis(ctx, "shop", RedisRequest{MemoryMB: 64}, false)
	if err != nil {
		t.Fatal(err)
	}
	if cred == nil || len(cred.Password) != 40 || inst.Socket != "/home/shop/.redis/redis.sock" {
		t.Fatalf("%+v %+v", cred, inst)
	}
	conf, _ := os.ReadFile(filepath.Join(root, "etc/ike/redis/shop.conf"))
	for _, want := range []string{"port 0", "unixsocket /home/shop/.redis/redis.sock", "maxmemory 64mb", "maxmemory-policy allkeys-lru", "requirepass " + cred.Password, `rename-command CONFIG ""`, `save ""`} {
		if !strings.Contains(string(conf), want) {
			t.Errorf("conf missing %q", want)
		}
	}
	// changing memory keeps the password and restarts
	cred2, inst2, err := m.SetRedis(ctx, "shop", RedisRequest{MemoryMB: 256, Persist: true}, false)
	if err != nil || cred2 != nil || inst2.MemoryMB != 256 {
		t.Fatalf("%v %+v %v", cred2, inst2, err)
	}
	conf, _ = os.ReadFile(filepath.Join(root, "etc/ike/redis/shop.conf"))
	if !strings.Contains(string(conf), "requirepass "+cred.Password) || !strings.Contains(string(conf), "appendonly yes") {
		t.Fatal("password changed or persistence missing")
	}
	h := strings.Join(run.History(), "\n")
	if !strings.Contains(h, "systemctl enable --now ike-redis@shop.service") || !strings.Contains(h, "systemctl restart ike-redis@shop.service") {
		t.Fatal(h)
	}
	info, err := m.RedisStatus(ctx, "shop")
	if err != nil || info.Keys != 12 || info.HitRate != 90 || info.UsedMB != 2 || info.Version != "8.0.2" {
		t.Fatalf("%+v %v", info, err)
	}
	if strings.Contains(h, cred.Password) {
		t.Fatal("password on a command line")
	}
	if _, _, err := m.SetRedis(ctx, "shop", RedisRequest{Policy: "bogus"}, false); err == nil {
		t.Fatal("bad policy accepted")
	}
	if err := m.RemoveRedis(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
}

func TestSlowLogParse(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root = root
	m := New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}})
	_ = os.MkdirAll(filepath.Join(root, "var/log/mysql"), 0o755)
	_ = os.WriteFile(filepath.Join(root, slowLogFile), []byte(`# Time: 261014  3:10:01
# User@Host: shop_wp[shop_wp] @ localhost []
# Thread_id: 8  Schema: shop_wp  QC_hit: No
# Query_time: 3.500000  Lock_time: 0.000100  Rows_sent: 10  Rows_examined: 500000
SET timestamp=1760411401;
SELECT * FROM wp_posts
WHERE post_status = 'publish';
# User@Host: other_db[other_db] @ localhost []
# Query_time: 9.0  Lock_time: 0.0  Rows_sent: 1  Rows_examined: 1
SET timestamp=1760411402;
SELECT SLEEP(9);
`), 0o644)
	q, err := m.SlowQueries("shop", "duration", 0)
	if err != nil || len(q) != 1 || q[0].DB != "shop_wp" || q[0].RowsExamined != 500000 || !strings.Contains(q[0].Query, "post_status") {
		t.Fatalf("%+v %v", q, err)
	}
	all, _ := m.SlowQueries("", "duration", 0)
	if len(all) != 2 || all[0].QuerySecs != 9 {
		t.Fatalf("%+v", all)
	}
}
