package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YOUR-ORG/ike/agent/internal/api"
	"github.com/YOUR-ORG/ike/agent/internal/client"
	"github.com/YOUR-ORG/ike/agent/internal/config"
	"github.com/YOUR-ORG/ike/agent/internal/firewall"
	"github.com/YOUR-ORG/ike/agent/internal/jobs"
	"github.com/YOUR-ORG/ike/agent/internal/runner"
	"github.com/YOUR-ORG/ike/agent/internal/sites"
	"github.com/YOUR-ORG/ike/agent/internal/updates"
)

// newServer wires an MCP server to a real agent API running in dry-run mode.
func newServer(t *testing.T, allowWrite bool) (*Server, *bytes.Buffer) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/php/8.3/fpm/pool.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Root, cfg.DryRun = root, true
	r := &runner.DryRun{}
	fw := &firewall.Manager{Run: r, StateFile: filepath.Join(root, "fw.json"), Protected: cfg.ProtectedPorts}
	jm := jobs.NewManager()
	agent := &api.Server{Sites: sites.NewManager(cfg, r), Firewall: fw, Runner: r, Config: cfg, Jobs: jm,
		Updates: &updates.Manager{Run: r, Jobs: jm, Root: root}}
	ts := httptest.NewServer(agent.Handler())
	t.Cleanup(ts.Close)

	audit := &bytes.Buffer{}
	return &Server{
		Agent:      &client.Client{BaseURL: ts.URL, HTTP: http.DefaultClient},
		AllowWrite: allowWrite, Version: "test", Audit: audit,
	}, audit
}

func call(t *testing.T, s *Server, method string, params any) map[string]any {
	t.Helper()
	msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	resp := s.Handle(context.Background(), msg)
	b, _ := json.Marshal(resp)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func callTool(t *testing.T, s *Server, name string, args map[string]any) (text string, isError bool) {
	t.Helper()
	res := call(t, s, "tools/call", map[string]any{"name": name, "arguments": args})["result"].(map[string]any)
	content := res["content"].([]any)[0].(map[string]any)
	return content["text"].(string), res["isError"].(bool)
}

func toolNames(t *testing.T, s *Server) []string {
	res := call(t, s, "tools/list", nil)["result"].(map[string]any)
	var names []string
	for _, tl := range res["tools"].([]any) {
		names = append(names, tl.(map[string]any)["name"].(string))
	}
	return names
}

func TestInitializeNegotiatesVersion(t *testing.T) {
	s, _ := newServer(t, false)
	res := call(t, s, "initialize", map[string]any{"protocolVersion": "2025-03-26"})["result"].(map[string]any)
	if res["protocolVersion"] != "2025-03-26" {
		t.Errorf("got %v", res["protocolVersion"])
	}
	res = call(t, s, "initialize", map[string]any{"protocolVersion": "1999-01-01"})["result"].(map[string]any)
	if res["protocolVersion"] != supportedVersions[0] {
		t.Errorf("unknown version should fall back to latest, got %v", res["protocolVersion"])
	}
	if s.Handle(context.Background(), []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)) != nil {
		t.Error("notifications must not get a response")
	}
	if call(t, s, "nope", nil)["error"] == nil {
		t.Error("unknown method should error")
	}
}

func TestReadOnlyModeHidesAndBlocksWriteTools(t *testing.T) {
	s, audit := newServer(t, false)
	names := strings.Join(toolNames(t, s), ",")
	if strings.Contains(names, "create_site") || strings.Contains(names, "delete_site") {
		t.Errorf("write tools listed in read-only mode: %s", names)
	}
	if !strings.Contains(names, "list_sites") {
		t.Errorf("read tools missing: %s", names)
	}
	text, isErr := callTool(t, s, "create_site", map[string]any{"domain": "a.com", "user": "auser"})
	if !isErr || !strings.Contains(text, "read-only") {
		t.Errorf("create_site should be blocked: %q", text)
	}
	if !strings.Contains(audit.String(), "denied") {
		t.Error("denied call not audited")
	}
}

func TestSiteLifecycleOverMCP(t *testing.T) {
	s, audit := newServer(t, true)

	if text, isErr := callTool(t, s, "create_account", map[string]any{"name": "shop"}); isErr {
		t.Fatalf("create_account: %s", text)
	}
	text, isErr := callTool(t, s, "create_site", map[string]any{"domain": "shop.com.ng", "account": "shop"})
	if isErr {
		t.Fatalf("create_site: %s", text)
	}
	if text, _ = callTool(t, s, "list_sites", map[string]any{"account": "shop"}); !strings.Contains(text, "shop.com.ng") {
		t.Errorf("list_sites: %s", text)
	}

	// Validation errors from the agent come back as tool errors, not protocol errors.
	if _, isErr = callTool(t, s, "create_site", map[string]any{"domain": "BAD DOMAIN", "account": "shop"}); !isErr {
		t.Error("bad domain accepted")
	}
	if _, isErr = callTool(t, s, "create_site", map[string]any{"domain": "a.com", "account": "shop", "evil": "x"}); !isErr {
		t.Error("unknown argument accepted")
	}
	if _, isErr = callTool(t, s, "create_site", map[string]any{"domain": "a.com", "account": "shop", "runtime": "ruby"}); !isErr {
		t.Error("enum not enforced")
	}

	for name, args := range map[string]map[string]any{
		"set_ssh_access":  {"account": "shop", "mode": "sftp"},
		"set_site_status": {"domain": "shop.com.ng", "status": "maintenance", "allow_ips": []any{"203.0.113.9"}},
		"add_cron_job":    {"account": "shop", "schedule": "@hourly", "command": "php artisan schedule:run"},
		"block_address":   {"address": "198.51.100.23", "comment": "Spam"},
		"open_port":       {"port": "8080", "proto": "tcp"},
	} {
		if text, isErr := callTool(t, s, name, args); isErr {
			t.Errorf("%s: %s", name, text)
		}
	}
	if text, isErr := callTool(t, s, "close_port", map[string]any{"port": "22", "proto": "tcp"}); !isErr {
		t.Errorf("closing SSH must fail: %s", text)
	}

	text, isErr = callTool(t, s, "delete_site", map[string]any{"domain": "shop.com.ng", "confirm_domain": "shop.com"})
	if !isErr || !strings.Contains(text, "nothing was deleted") {
		t.Errorf("mismatched confirmation must fail: %q", text)
	}
	if text, isErr = callTool(t, s, "delete_account", map[string]any{"account": "shop", "confirm_account": "shop"}); !isErr {
		t.Errorf("account with websites deleted without with_sites: %s", text)
	}
	if text, isErr = callTool(t, s, "delete_account", map[string]any{"account": "shop", "confirm_account": "shop", "with_sites": true}); isErr {
		t.Errorf("delete_account: %s", text)
	}
	if text, _ = callTool(t, s, "list_sites", map[string]any{}); strings.Contains(text, "shop.com.ng") {
		t.Error("site still listed after account delete")
	}
	if strings.Count(audit.String(), "\n") < 10 {
		t.Errorf("expected audit lines, got:\n%s", audit.String())
	}
}

func TestServeStdio(t *testing.T) {
	s, _ := newServer(t, false)
	in := strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
			`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.Serve(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 responses, got %d:\n%s", len(lines), out.String())
	}
}
