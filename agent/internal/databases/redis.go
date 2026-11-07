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

// Each account gets its own Redis (or Valkey) server: a systemd instance
// running as the account's user, listening on a unix socket only, with a
// password and a memory cap. Websites in the account connect through the
// socket; nobody else can reach it.

func (m *Manager) redisBin() string {
	if m.env.Cfg.Redis == "valkey" {
		return "/usr/bin/valkey-server"
	}
	return "/usr/bin/redis-server"
}

func (m *Manager) redisConf(account string) string {
	return filepath.Join("/etc/ike/redis", account+".conf")
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

// SetRedis creates the account's instance, or changes its memory and
// persistence. The password is returned only when the instance is created
// or the password is reset.
func (m *Manager) SetRedis(ctx context.Context, account string, r RedisRequest, newPassword bool) (*Credentials, *RedisInstance, error) {
	if m.env.Cfg.Redis == "" {
		return nil, nil, fmt.Errorf("Redis %w", kit.ErrNotInstalled)
	}
	if r.MemoryMB == 0 {
		r.MemoryMB = 128
	}
	if r.MemoryMB < 16 || r.MemoryMB > 65536 {
		return nil, nil, fmt.Errorf("memory must be 16-65536 MB")
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
	confPath := m.env.P(m.redisConf(account))
	socket := m.RedisSocket(account)
	dir := filepath.Dir(socket)
	pass := ""
	if a.Redis != nil && !newPassword {
		pass = readRequirePass(confPath)
	}
	created := a.Redis == nil
	if pass == "" {
		pass = kit.RandomHex(20)
		newPassword = true
	}
	if _, err := m.env.Run.Run(ctx, "install", "-d", "-m", "0700", "-o", account, "-g", account, dir); err != nil {
		return nil, nil, err
	}
	if err := kit.WriteFile(confPath, []byte(renderRedisConf(socket, dir, pass, r)), 0o640); err != nil {
		return nil, nil, err
	}
	if !m.env.Cfg.DryRun {
		if _, gid, err := m.env.IDs(account); err == nil {
			_ = os.Chown(confPath, 0, gid)
		}
	}
	unit := "ike-redis@" + account + ".service"
	var cmd []string
	if created {
		cmd = []string{"enable", "--now", unit}
	} else {
		cmd = []string{"restart", unit}
	}
	if _, err := m.env.Run.Run(ctx, "systemctl", cmd...); err != nil {
		return nil, nil, fmt.Errorf("Redis did not start: %w", err)
	}
	inst := &RedisInstance{Socket: socket, MemoryMB: r.MemoryMB, Policy: r.Policy, Persist: r.Persist, CreatedAt: kit.Now()}
	if a.Redis != nil {
		inst.CreatedAt = a.Redis.CreatedAt
	}
	a.Redis = inst
	if err := m.save(a); err != nil {
		return nil, nil, err
	}
	if !newPassword {
		return nil, inst, nil
	}
	return &Credentials{Engine: Redis, Password: pass, Host: socket,
		URL: "unix://:" + pass + "@" + socket}, inst, nil
}

func readRequirePass(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "requirepass ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "requirepass "))
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
	_, _ = m.env.Run.Run(ctx, "systemctl", "disable", "--now", "ike-redis@"+account+".service")
	_ = os.Remove(m.env.P(m.redisConf(account)))
	_, _ = m.env.Run.Run(ctx, "rm", "-rf", "--one-file-system", filepath.Dir(m.RedisSocket(account)))
	a.Redis = nil
	return m.save(a)
}

// RedisInfo is live state from INFO.
type RedisInfo struct {
	Running     bool    `json:"running"`
	UsedMB      float64 `json:"used_mb"`
	Keys        int     `json:"keys"`
	Clients     int     `json:"clients"`
	HitRate     float64 `json:"hit_rate"` // percent
	UptimeHours float64 `json:"uptime_hours"`
	Version     string  `json:"version"`
}

func (m *Manager) RedisStatus(ctx context.Context, account string) (*RedisInfo, error) {
	a, err := m.load(account)
	if err != nil {
		return nil, err
	}
	if a.Redis == nil {
		return nil, fmt.Errorf("Redis for %s %w", account, kit.ErrNotFound)
	}
	pass := readRequirePass(m.env.P(m.redisConf(account)))
	cli := "redis-cli"
	if m.env.Cfg.Redis == "valkey" {
		cli = "valkey-cli"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// The password goes in the environment (REDISCLI_AUTH), not the arguments.
	out, err := m.env.Run.Exec(ctx, runnerCmd(cli, []string{"-s", a.Redis.Socket, "INFO"}, []string{"REDISCLI_AUTH=" + pass, "PATH=/usr/bin:/bin"}))
	info := &RedisInfo{}
	if err != nil {
		return info, nil
	}
	info.Running = true
	var hits, misses float64
	for _, l := range strings.Split(string(out), "\n") {
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
			info.Version = v
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
	if hits+misses > 0 {
		info.HitRate = float64(int(hits/(hits+misses)*1000)) / 10
	}
	return info, nil
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
	pass := readRequirePass(m.env.P(m.redisConf(account)))
	cli := "redis-cli"
	if m.env.Cfg.Redis == "valkey" {
		cli = "valkey-cli"
	}
	_, err = m.env.Run.Exec(ctx, runnerCmd(cli, []string{"-s", a.Redis.Socket, "FLUSHALL"}, []string{"REDISCLI_AUTH=" + pass, "PATH=/usr/bin:/bin"}))
	return err
}
