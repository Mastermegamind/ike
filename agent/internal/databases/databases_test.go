package databases

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/kit"
	"github.com/Mastermegamind/ike/agent/internal/runner"
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

func TestCacheEnginesSideBySide(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun, cfg.Redis = root, true, "redis"
	cfg.CacheEngines = []string{"valkey", "dragonfly"}
	run := &runner.DryRun{Responses: map[string]string{"redis-cli": "# Server\nredis_version:7.4.0\ndragonfly_version:df-v1.34.1\nused_memory:1048576\ndb0:keys=3,expires=0\n"}}
	m := New(&kit.Env{Cfg: cfg, Run: run})
	ctx := context.Background()
	if got := m.Engines().Caches; strings.Join(got, ",") != "redis,valkey,dragonfly" {
		t.Fatalf("caches %v", got)
	}

	// Dragonfly: a flag file, its own unit, raised to its 256 MB floor.
	cred, inst, err := m.SetRedis(ctx, "app", RedisRequest{Engine: "dragonfly"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Engine != "dragonfly" || inst.MemoryMB != 256 || cred == nil || cred.Engine != "dragonfly" {
		t.Fatalf("%+v %+v", inst, cred)
	}
	flags, _ := os.ReadFile(filepath.Join(root, "etc/ike/redis/app.dragonfly.flags"))
	for _, want := range []string{"--port=0\n", "--unixsocket=/home/app/.redis/redis.sock", "--requirepass=" + cred.Password,
		"--maxmemory=268435456", "--cache_mode=true", "--proactor_threads=1", "--admin_port=0", "--restricted_commands=CONFIG"} {
		if !strings.Contains(string(flags), want) {
			t.Errorf("flags missing %q:\n%s", want, flags)
		}
	}
	if _, _, err := m.SetRedis(ctx, "app", RedisRequest{Engine: "dragonfly", MemoryMB: 64}, false); err == nil {
		t.Fatal("Dragonfly accepted 64 MB")
	}
	// noeviction turns cache mode off
	if _, _, err := m.SetRedis(ctx, "app", RedisRequest{MemoryMB: 512, Policy: "noeviction"}, false); err != nil {
		t.Fatal(err)
	}
	flags, _ = os.ReadFile(filepath.Join(root, "etc/ike/redis/app.dragonfly.flags"))
	if !strings.Contains(string(flags), "--cache_mode=false") || !strings.Contains(string(flags), "--requirepass="+cred.Password) {
		t.Fatalf("policy or password lost:\n%s", flags)
	}
	info, err := m.RedisStatus(ctx, "app")
	if err != nil || info.Engine != "dragonfly" || info.Version != "v1.34.1" || info.Keys != 3 {
		t.Fatalf("%+v %v", info, err)
	}

	// Switching to Valkey stops Dragonfly first and keeps the password.
	cred2, inst2, err := m.SetRedis(ctx, "app", RedisRequest{Engine: "valkey", MemoryMB: 128}, false)
	if err != nil || cred2 != nil || inst2.Engine != "valkey" {
		t.Fatalf("%+v %+v %v", cred2, inst2, err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/ike/redis/app.dragonfly.flags")); !os.IsNotExist(err) {
		t.Fatal("old flag file left behind")
	}
	conf, _ := os.ReadFile(filepath.Join(root, "etc/ike/redis/app.conf"))
	if !strings.Contains(string(conf), "requirepass "+cred.Password) {
		t.Fatal("password changed on switch")
	}
	h := strings.Join(run.History(), "\n")
	for _, want := range []string{"systemctl enable --now ike-dragonfly@app.service", "systemctl restart ike-dragonfly@app.service",
		"systemctl disable --now ike-dragonfly@app.service", "systemctl enable --now ike-valkey@app.service"} {
		if !strings.Contains(h, want) {
			t.Errorf("missing %q in\n%s", want, h)
		}
	}
	if strings.Index(h, "disable --now ike-dragonfly@app") > strings.Index(h, "enable --now ike-valkey@app") {
		t.Fatal("Valkey started before Dragonfly stopped")
	}
	if strings.Contains(h, cred.Password) {
		t.Fatal("password on a command line")
	}
	// The server-wide engine keeps the old unit name.
	if _, _, err := m.SetRedis(ctx, "web", RedisRequest{}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "systemctl enable --now ike-redis@web.service") {
		t.Fatal("default engine didn't use ike-redis@")
	}
	if _, _, err := m.SetRedis(ctx, "web", RedisRequest{Engine: "keydb"}, false); err == nil {
		t.Fatal("unknown engine accepted")
	}
}

func TestCacheUnitNames(t *testing.T) {
	cfg := config.Default()
	cfg.Redis, cfg.CacheEngines = "valkey", []string{"redis", "dragonfly"}
	m := New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}})
	for engine, want := range map[string]string{"valkey": "ike-redis@a.service", "redis": "ike-redis-server@a.service", "dragonfly": "ike-dragonfly@a.service"} {
		if got := m.unitFor(engine, "a"); got != want {
			t.Errorf("%s: %s, want %s", engine, got, want)
		}
	}
	cfg.Redis = "redis"
	m = New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}})
	if got := m.unitFor("valkey", "a"); got != "ike-valkey@a.service" {
		t.Errorf("valkey next to Redis: %s", got)
	}
}

func TestCacheEngineNotInstalled(t *testing.T) {
	cfg := config.Default()
	cfg.Root, cfg.DryRun, cfg.Redis = t.TempDir(), true, "valkey"
	m := New(&kit.Env{Cfg: cfg, Run: &runner.DryRun{}})
	if _, _, err := m.SetRedis(context.Background(), "shop", RedisRequest{Engine: "dragonfly"}, false); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("got %v", err)
	}
}

func TestMeilisearchPerAccount(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	run := &runner.DryRun{}
	m := New(&kit.Env{Cfg: cfg, Run: run})
	ctx := context.Background()
	cred, inst, err := m.SetSearch(ctx, "shop", SearchRequest{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if inst.Port != 7700 || inst.MemoryMB != 256 || cred == nil || len(cred.Password) != 48 || cred.URL != "http://127.0.0.1:7700" {
		t.Fatalf("%+v %+v", inst, cred)
	}
	env, _ := os.ReadFile(filepath.Join(root, "etc/ike/meilisearch/shop.env"))
	for _, want := range []string{"MEILI_HTTP_ADDR=127.0.0.1:7700", "MEILI_MASTER_KEY=" + cred.Password, "MEILI_ENV=production",
		"MEILI_NO_ANALYTICS=true", "MEILI_DB_PATH=/home/shop/.meilisearch/data", "MEILI_MAX_INDEXING_MEMORY=256Mb"} {
		if !strings.Contains(string(env), want) {
			t.Errorf("env missing %q", want)
		}
	}
	if fi, _ := os.Stat(filepath.Join(root, "etc/ike/meilisearch/shop.env")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("env readable by others: %v", fi.Mode())
	}
	// a second account gets the next port; changing memory keeps key and port
	_, inst2, _ := m.SetSearch(ctx, "blog", SearchRequest{}, false)
	if inst2.Port != 7701 {
		t.Fatalf("port %d", inst2.Port)
	}
	c2, inst3, err := m.SetSearch(ctx, "shop", SearchRequest{MemoryMB: 512}, false)
	if err != nil || c2 != nil || inst3.Port != 7700 {
		t.Fatalf("%+v %+v %v", c2, inst3, err)
	}
	if !strings.Contains(strings.Join(run.History(), "\n"), "systemctl enable --now ike-meilisearch@shop.service") {
		t.Fatal("not started")
	}
	if strings.Contains(strings.Join(run.History(), "\n"), cred.Password) {
		t.Fatal("key on a command line")
	}
	if _, _, err := m.SetSearch(ctx, "shop", SearchRequest{MemoryMB: 10}, false); err == nil {
		t.Fatal("10 MB accepted")
	}
	if err := m.DeleteAccount(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "etc/ike/meilisearch/shop.env")); !os.IsNotExist(err) {
		t.Fatal("env left after account deletion")
	}
}
