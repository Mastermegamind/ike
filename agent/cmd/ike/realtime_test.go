package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mastermegamind/ike/agent/internal/api"
	"github.com/Mastermegamind/ike/agent/internal/config"
	"github.com/Mastermegamind/ike/agent/internal/jobs"
	"github.com/Mastermegamind/ike/agent/internal/runner"
	"github.com/Mastermegamind/ike/agent/internal/sites"
)

// The realtime and cache commands against a dry-run agent: their request
// bodies must be what the agent accepts.
func TestRealtimeAndCacheCommands(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "etc/php/8.3/fpm/pool.d"), 0o755)
	cfg := config.Default()
	cfg.Root, cfg.DryRun, cfg.Redis, cfg.CacheEngines, cfg.PublicIPv4 = root, true, "redis", []string{"dragonfly"}, "203.0.113.9"
	r := &runner.DryRun{}
	ag := &api.Server{Sites: sites.NewManager(cfg, r), Config: cfg, Runner: r, Jobs: jobs.NewManager()}
	srv := httptest.NewServer(ag.Handler())
	defer srv.Close()
	c := &client{base: srv.URL + "/v1", http: http.DefaultClient, asJSON: true}
	if err := c.do("POST", "/accounts", map[string]string{"name": "media"}, nil); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) error {
		pos, fs := parseFlags(args, "yes")
		switch pos[0] {
		case "realtime":
			return realtimeCmd(c, pos[1:], fs)
		case "cache":
			return cacheCmd(c, pos[1:], fs)
		}
		return expiryCmd(c, pos[1:], fs)
	}
	for _, args := range [][]string{
		{"realtime"},
		{"realtime", "turn", "on", "--domain", "turn.media.ng", "--accounts", "yes", "--max-kbps", "4000"},
		{"realtime", "turn", "login", "--user", "alice", "--hours", "2"},
		{"realtime", "livekit", "on", "--domain", "rtc.media.ng", "--account", "media", "--turn", "yes", "--webhook", "https://media.ng/hook"},
		{"realtime", "keys", "add", "Phone app", "--account", "media"},
		{"realtime", "token", "class-1", "ada", "--minutes", "30"},
		{"realtime", "turn", "set", "--tls", "yes"},
		{"realtime", "turn", "rotate-secret", "--yes"},
		{"cache", "media", "set", "--engine", "dragonfly", "--memory", "512"},
		{"cache", "media", "set", "--engine", "redis", "--memory", "128", "--yes"},
		{"cache", "media"},
	} {
		if err := run(args...); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	turn, _ := os.ReadFile(filepath.Join(root, "etc/turnserver.conf"))
	if !strings.Contains(string(turn), "realm=turn.media.ng") || !strings.Contains(string(turn), "max-bps=500000") {
		t.Fatalf("turnserver.conf:\n%s", turn)
	}
	lk, _ := os.ReadFile(filepath.Join(root, "etc/livekit/livekit.yaml"))
	if !strings.Contains(string(lk), "turn_servers:") || !strings.Contains(string(lk), "https://media.ng/hook") {
		t.Fatalf("livekit.yaml:\n%s", lk)
	}
	// "turn set" kept it on and the domain; turning LiveKit's settings didn't drop the webhook
	if err := run("realtime", "livekit", "set", "--accounts", "yes"); err != nil {
		t.Fatal(err)
	}
	lk, _ = os.ReadFile(filepath.Join(root, "etc/livekit/livekit.yaml"))
	if !strings.Contains(string(lk), "https://media.ng/hook") {
		t.Fatal("webhook lost by livekit set")
	}
	if err := run("realtime", "turn", "on", "--tls", "maybe"); err == nil {
		t.Fatal("--tls maybe accepted")
	}
	if !strings.Contains(strings.Join(r.History(), "\n"), "ike-redis@media.service") {
		t.Fatal("switch back to Redis didn't use its unit")
	}
}
