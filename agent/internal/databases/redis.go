package databases

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Mastermegamind/ike/agent/internal/kit"
)

// Each account gets its own cache server: a systemd instance running as the
// account's user, listening on a unix socket only, with a password and a
// memory cap. Websites in the account connect through the socket; nobody else
// can reach it. The engine is Redis, Valkey or Dragonfly, whichever the admin
// installed; they all speak the Redis protocol, so apps don't change.

// Cache engines for account instances.
const (
	EngineRedis     = "redis"
	EngineValkey    = "valkey"
	EngineDragonfly = "dragonfly"
)

// dragonflyMinMB is Dragonfly's floor: it needs 256 MB per thread, and
// account instances run on one thread.
const dragonflyMinMB = 256

var engineNames = map[string]string{EngineRedis: "Redis", EngineValkey: "Valkey", EngineDragonfly: "Dragonfly"}

// engineOf is the engine an instance runs (older instances didn't record it).
func (m *Manager) engineOf(inst *RedisInstance) string {
	if inst != nil && inst.Engine != "" {
		return inst.Engine
	}
	if m.env.Cfg.Redis != "" {
		return m.env.Cfg.Redis
	}
	return EngineRedis
}

// defaultEngine is used when a request doesn't name one.
func (m *Manager) defaultEngine() string {
	if c := m.env.Cfg.Caches(); len(c) > 0 {
		return c[0]
	}
	return ""
}

// unitFor is the systemd template for an engine. The server-wide engine keeps
// the original ike-redis@ name so instances made before 0.8 keep running.
func (m *Manager) unitFor(engine, account string) string {
	if engine == m.env.Cfg.Redis {
		return "ike-redis@" + account + ".service"
	}
	return "ike-" + engine + "@" + account + ".service"
}

func (m *Manager) redisBin() string {
	if m.env.Cfg.Redis == "valkey" {
		return "/usr/bin/valkey-server"
	}
	return "/usr/bin/redis-server"
}

// redisConf is the instance's config (Redis/Valkey syntax).
func (m *Manager) redisConf(account string) string {
	return filepath.Join("/etc/ike/redis", account+".conf")
}

// confFor is where an engine's settings for an account live.
func (m *Manager) confFor(account, engine string) string {
	if engine == EngineDragonfly {
		return filepath.Join("/etc/ike/redis", account+".dragonfly.flags")
	}
	return m.redisConf(account)
}

// cliFor is the command-line client used to talk to an engine. Dragonfly
// ships none, so it borrows whichever Redis-protocol client is installed.
func (m *Manager) cliFor(engine string) string {
	switch engine {
	case EngineValkey:
		return "valkey-cli"
	case EngineDragonfly:
		if !m.env.Cfg.HasCache(EngineRedis) && m.env.Cfg.HasCache(EngineValkey) {
			return "valkey-cli"
		}
	}
	return "redis-cli"
}

// RedisSocket is where an account's instance listens.
func (m *Manager) RedisSocket(account string) string {
	return filepath.Join(m.env.Home(account), ".redis", "redis.sock")
}

var policies = map[string]bool{"allkeys-lru": true, "volatile-lru": true, "allkeys-lfu": true, "volatile-lfu": true,
	"allkeys-random": true, "volatile-ttl": true, "noeviction": true}

// RedisRequest creates or changes an account's instance.
type RedisRequest struct {
	MemoryMB int    `json:"memory_mb"`
	Policy   string `json:"policy"`
	Persist  bool   `json:"persist"` // save to disk (otherwise a pure cache)
	// Engine is redis, valkey or dragonfly (default: the instance's current
	// engine, or the server's first). Changing it starts a new, empty instance.
	Engine string `json:"engine,omitempty"`
}

func renderRedisConf(socket, dir, pass string, r RedisRequest) string {
	var b strings.Builder
	b.WriteString("# Written by Ike. Change it in the panel under Databases → Redis.\n")
	b.WriteString("port 0\nbind 127.0.0.1 -::1\nprotected-mode yes\n")
	fmt.Fprintf(&b, "unixsocket %s\nunixsocketperm 660\n", socket)
	fmt.Fprintf(&b, "dir %s\n", dir)
	fmt.Fprintf(&b, "requirepass %s\n", pass)
	fmt.Fprintf(&b, "maxmemory %dmb\nmaxmemory-policy %s\n", r.MemoryMB, r.Policy)
	if r.Persist {
		b.WriteString("save 900 1\nsave 300 10\nappendonly yes\nappendfsync everysec\n")
	} else {
		b.WriteString("save \"\"\nappendonly no\n")
	}
	b.WriteString("databases 16\nloglevel notice\nlogfile \"\"\n")
	b.WriteString("rename-command CONFIG \"\"\nrename-command DEBUG \"\"\nrename-command SHUTDOWN \"\"\nrename-command MODULE \"\"\n")
	return b.String()
}

// renderDragonflyFlags writes the same promises for Dragonfly: no TCP port,
// a unix socket, a password, one thread and a memory cap. Dragonfly has one
// eviction behaviour (cache mode), so every policy but noeviction turns it on.
func renderDragonflyFlags(socket, dir, pass string, r RedisRequest) string {
	var b strings.Builder
	b.WriteString("# Written by Ike. Change it in the panel under Databases → Redis.\n")
	b.WriteString("--port=0\n--bind=127.0.0.1\n--admin_port=0\n--memcached_port=0\n--primary_port_http_enabled=false\n")
	fmt.Fprintf(&b, "--unixsocket=%s\n--unixsocketperm=660\n", socket)
	fmt.Fprintf(&b, "--dir=%s\n", dir)
	fmt.Fprintf(&b, "--requirepass=%s\n", pass)
	fmt.Fprintf(&b, "--maxmemory=%d\n", int64(r.MemoryMB)<<20)
	fmt.Fprintf(&b, "--cache_mode=%t\n", r.Policy != "noeviction")
	b.WriteString("--proactor_threads=1\n--dbnum=16\n--logtostderr\n")
	// Snapshots in RDB format, so the data is readable by Redis tools too.
	b.WriteString("--dbfilename=dump\n--df_snapshot_format=false\n")
	if r.Persist {
		b.WriteString("--snapshot_cron=*/5 * * * *\n")
	} else {
		b.WriteString("--snapshot_cron=\n")
	}
	// Only reachable on the admin port, which is off.
	b.WriteString("--restricted_commands=CONFIG,DEBUG,SHUTDOWN,MODULE,REPLICAOF,SLAVEOF\n")
	return b.String()
}

// SetRedis creates the account's instance, or changes its memory, persistence
// or engine. The password is returned only when the instance is created or
// the password is reset; it carries over when the engine changes.
func (m *Manager) SetRedis(ctx context.Context, account string, r RedisRequest, newPassword bool) (*Credentials, *RedisInstance, error) {
	if len(m.env.Cfg.Caches()) == 0 {
		return nil, nil, fmt.Errorf("Redis %w", kit.ErrNotInstalled)
	}
	if r.MemoryMB == 0 {
		r.MemoryMB = 128
	}
	if r.Policy == "" {
		r.Policy = "allkeys-lru"
	}
	if !policies[r.Policy] {
		return nil, nil, fmt.Errorf("unknown eviction policy %q", r.Policy)
	}
	unlock := m.lock(account)
	defer unlock()
	a, err := m.load(account)
	if err != nil {
		return nil, nil, err
	}
	engine := r.Engine
	if engine == "" && a.Redis != nil {
		engine = m.engineOf(a.Redis)
	}
	if engine == "" {
		engine = m.defaultEngine()
	}
	if engineNames[engine] == "" {
		return nil, nil, fmt.Errorf("engine must be redis, valkey or dragonfly")
	}
	if !m.env.Cfg.HasCache(engine) {
		return nil, nil, fmt.Errorf("%s %w", engineNames[engine], kit.ErrNotInstalled)
	}
	if engine == EngineDragonfly && r.MemoryMB < dragonflyMinMB {
		if r.MemoryMB == 128 && (a.Redis == nil || a.Redis.MemoryMB < dragonflyMinMB) {
			r.MemoryMB = dragonflyMinMB // the default is too small for Dragonfly
		} else {
			return nil, nil, fmt.Errorf("Dragonfly needs at least %d MB", dragonflyMinMB)
		}
	}
	if r.MemoryMB < 16 || r.MemoryMB > 65536 {
		return nil, nil, fmt.Errorf("memory must be 16-65536 MB")
	}
	socket := m.RedisSocket(account)
	dir := filepath.Dir(socket)
	pass := ""
	switching := a.Redis != nil && m.engineOf(a.Redis) != engine
	if a.Redis != nil && !newPassword {
		pass = readRequirePass(m.env.P(m.confFor(account, m.engineOf(a.Redis))))
	}
	if switching {
		// The old engine stops first: both would use the same socket.
		old := m.engineOf(a.Redis)
		if _, err := m.env.Run.Run(ctx, "systemctl", "disable", "--now", m.unitFor(old, account)); err != nil {
			return nil, nil, fmt.Errorf("could not stop %s: %w", engineNames[old], err)
		}
		_ = os.Remove(m.env.P(m.confFor(account, old)))
	}
	created := a.Redis == nil || switching
	if pass == "" {
		pass = kit.RandomHex(20)
		newPassword = true
	}
	if _, err := m.env.Run.Run(ctx, "install", "-d", "-m", "0700", "-o", account, "-g", account, dir); err != nil {
		return nil, nil, err
	}
	confPath := m.env.P(m.confFor(account, engine))
	conf := renderRedisConf(socket, dir, pass, r)
	if engine == EngineDragonfly {
		conf = renderDragonflyFlags(socket, dir, pass, r)
	}
	if err := kit.WriteFile(confPath, []byte(conf), 0o640); err != nil {
		return nil, nil, err
	}
	if !m.env.Cfg.DryRun {
		if _, gid, err := m.env.IDs(account); err == nil {
			_ = os.Chown(confPath, 0, gid)
		}
	}
	unit := m.unitFor(engine, account)
	var cmd []string
	if created {
		cmd = []string{"enable", "--now", unit}
	} else {
		cmd = []string{"restart", unit}
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", cmd...); err != nil {
		return nil, nil, fmt.Errorf("%s did not start: %w", engineNames[engine], err)
	}
	inst := &RedisInstance{Engine: engine, Socket: socket, MemoryMB: r.MemoryMB, Policy: r.Policy, Persist: r.Persist, CreatedAt: kit.Now()}
	if a.Redis != nil && !switching {
		inst.CreatedAt = a.Redis.CreatedAt
	}
	a.Redis = inst
	if err := m.save(a); err != nil {
		return nil, nil, err
	}
	if switching {
		m.env.Publish("database.cache.switched", account, map[string]any{"engine": engine})
	}
	if !newPassword {
		return nil, inst, nil
	}
	return &Credentials{Engine: engine, Password: pass, Host: socket,
		URL: "unix://:" + pass + "@" + socket}, inst, nil
}

// readRequirePass finds the password in a Redis config or a Dragonfly flag file.
func readRequirePass(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "requirepass "); ok {
			return strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(l, "--requirepass="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (m *Manager) RemoveRedis(ctx context.Context, account string) error {
	a, err := m.load(account)
	if err != nil {
		return err
	}
	if a.Redis == nil {
		return fmt.Errorf("Redis for %s %w", account, kit.ErrNotFound)
	}
	engine := m.engineOf(a.Redis)
	_, _ = m.env.Run.Run(ctx, "systemctl", "disable", "--now", m.unitFor(engine, account))
	_ = os.Remove(m.env.P(m.confFor(account, engine)))
	_, _ = m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", filepath.Dir(m.RedisSocket(account)))
	a.Redis = nil
	return m.save(a)
}

// RedisInfo is live state from INFO.
type RedisInfo struct {
	Engine      string  `json:"engine"`
	Running     bool    `json:"running"`
	UsedMB      float64 `json:"used_mb"`
	Keys        int     `json:"keys"`
	Clients     int     `json:"clients"`
	HitRate     float64 `json:"hit_rate"` // percent
	UptimeHours float64 `json:"uptime_hours"`
	Version     string  `json:"version"`
}

// cacheCLI runs a client command against an account's instance. The password
// goes in the environment (REDISCLI_AUTH), not the arguments.
func (m *Manager) cacheCLI(ctx context.Context, account string, inst *RedisInstance, args ...string) ([]byte, error) {
	engine := m.engineOf(inst)
	pass := readRequirePass(m.env.P(m.confFor(account, engine)))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return m.env.Run.Exec(ctx, runnerCmd(m.cliFor(engine), append([]string{"-s", inst.Socket}, args...),
		[]string{"REDISCLI_AUTH=" + pass, "PATH=/usr/bin:/bin"}))
}

func (m *Manager) RedisStatus(ctx context.Context, account string) (*RedisInfo, error) {
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	if a.Redis == nil {
		return nil, fmt.Errorf("Redis for %s %w", account, kit.ErrNotFound)
	}
	info := &RedisInfo{Engine: m.engineOf(a.Redis)}
	out, err := m.cacheCLI(ctx, account, a.Redis, "INFO")
	if err != nil {
		return info, nil
	}
	parseInfo(string(out), info)
	return info, nil
}

// parseInfo reads the fields Ike shows from an INFO reply.
func parseInfo(out string, info *RedisInfo) {
	info.Running = true
	var hits, misses float64
	version, df := "", ""
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		switch {
		case k == "used_memory":
			f, _ := strconv.ParseFloat(v, 64)
			info.UsedMB = float64(int(f/1048576*10)) / 10
		case k == "connected_clients":
			info.Clients, _ = strconv.Atoi(v)
		case k == "keyspace_hits":
			hits, _ = strconv.ParseFloat(v, 64)
		case k == "keyspace_misses":
			misses, _ = strconv.ParseFloat(v, 64)
		case k == "uptime_in_seconds":
			s, _ := strconv.ParseFloat(v, 64)
			info.UptimeHours = float64(int(s/360)) / 10
		case k == "redis_version" || k == "valkey_version":
			if version == "" || k == "valkey_version" {
				version = v
			}
		case k == "dragonfly_version":
			df = strings.TrimPrefix(v, "df-")
		case strings.HasPrefix(k, "db"):
			// keys=12,expires=0,avg_ttl=0
			for _, kv := range strings.Split(v, ",") {
				if n, ok := strings.CutPrefix(kv, "keys="); ok {
					c, _ := strconv.Atoi(n)
					info.Keys += c
				}
			}
		}
	}
	// Dragonfly also reports a Redis version it is compatible with.
	info.Version = version
	if df != "" {
		info.Version = df
	}
	if hits+misses > 0 {
		info.HitRate = float64(int(hits/(hits+misses)*1000)) / 10
	}
}

// FlushRedis empties an account's instance.
func (m *Manager) FlushRedis(ctx context.Context, account string) error {
	a, err := m.load(account)
	if err != nil {
		return err
	}
	if a.Redis == nil {
		return fmt.Errorf("Redis for %s %w", account, kit.ErrNotFound)
	}
	_, err = m.cacheCLI(ctx, account, a.Redis, "FLUSHALL")
	return err
}
