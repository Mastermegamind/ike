// Package mcp implements a Model Context Protocol server (JSON-RPC 2.0 over stdio)
// so AI agents can manage a HostPanel server through the same validated agent API
// the panel uses. It never exposes a shell.
//
// Safety model:
//   - Read-only tools are always available.
//   - Write tools appear only when started with --allow-write.
//   - Destructive tools require the caller to repeat the domain in "confirm_domain".
//   - Every tool call is written to the audit log.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/YOUR-ORG/hostpanel/agent/internal/client"
)

const ServerName = "hostpanel"

// Protocol versions this server can speak, newest first.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type Server struct {
	Agent      *client.Client
	AllowWrite bool
	Version    string
	Audit      io.Writer // JSON lines; may be nil

	auditMu sync.Mutex
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve reads newline-delimited JSON-RPC messages from in and writes responses to out.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if resp := s.Handle(ctx, []byte(line)); resp != nil {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
	return sc.Err()
}

// Handle processes one message. It returns nil for notifications.
func (s *Server) Handle(ctx context.Context, msg []byte) *rpcResponse {
	var req rpcRequest
	if err := json.Unmarshal(msg, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}
	}
	isNotification := len(req.ID) == 0
	if isNotification {
		return nil // notifications/initialized, notifications/cancelled, ...
	}
	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedVersions[0]
		for _, v := range supportedVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		mode := "read-only"
		if s.AllowWrite {
			mode = "read-write"
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": ServerName, "version": s.Version},
			"instructions": "HostPanel server management (" + mode + "). Each website has its own Linux user, " +
				"PHP-FPM pool and Nginx config. Check list_sites before creating a site. Never delete a site " +
				"unless the human explicitly asked for that specific domain to be deleted.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": s.toolList()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "invalid params"}
			break
		}
		resp.Result = s.callTool(ctx, p.Name, p.Arguments)
	default:
		resp.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return resp
}

type tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations,omitempty"`

	write   bool
	handler func(ctx context.Context, args map[string]any) (any, error)
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func readOnly() map[string]any { return map[string]any{"readOnlyHint": true, "openWorldHint": false} }

func (s *Server) tools() []tool {
	domain := str("Website domain, lowercase, e.g. example.com")
	return []tool{
		{
			Name: "get_system_info", Title: "Server information",
			Description: "OS, kernel, CPU count, memory, disk space, uptime, load and installed PHP versions.",
			InputSchema: obj(map[string]any{}), Annotations: readOnly(),
			handler: func(ctx context.Context, _ map[string]any) (any, error) {
				var out any
				return out, s.Agent.Get(ctx, "/v1/system", &out)
			},
		},
		{
			Name: "get_service_status", Title: "Service status",
			Description: "Whether nginx, mariadb, PHP-FPM, fail2ban and the agent are running.",
			InputSchema: obj(map[string]any{}), Annotations: readOnly(),
			handler: func(ctx context.Context, _ map[string]any) (any, error) {
				var out any
				return out, s.Agent.Get(ctx, "/v1/services", &out)
			},
		},
		{
			Name: "list_sites", Title: "List websites",
			Description: "All websites on this server with their Linux user, PHP version, document root and SSH mode.",
			InputSchema: obj(map[string]any{}), Annotations: readOnly(),
			handler: func(ctx context.Context, _ map[string]any) (any, error) {
				var out any
				return out, s.Agent.Get(ctx, "/v1/sites", &out)
			},
		},
		{
			Name: "get_site", Title: "Website details",
			Description: "Details for one website.",
			InputSchema: obj(map[string]any{"domain": domain}, "domain"), Annotations: readOnly(),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Get(ctx, "/v1/sites/"+client.PathEscape(argStr(a, "domain")), &out)
			},
		},
		{
			Name: "list_ssh_keys", Title: "List SSH keys",
			Description: "SSH public keys authorized for a website's Linux user (type, fingerprint, comment).",
			InputSchema: obj(map[string]any{"domain": domain}, "domain"), Annotations: readOnly(),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Get(ctx, "/v1/sites/"+client.PathEscape(argStr(a, "domain"))+"/ssh/keys", &out)
			},
		},
		{
			Name: "create_site", Title: "Create website", write: true,
			Description: "Create a website: a new Linux user, home folder, PHP-FPM pool and Nginx config. " +
				"Point the domain's DNS A record at this server for it to load.",
			InputSchema: obj(map[string]any{
				"domain": domain,
				"user":   str("New Linux username for the site: 3-16 lowercase letters/digits, starting with a letter"),
				"php":    str("PHP version, e.g. 8.3. Optional; uses the server default. See get_system_info for installed versions."),
			}, "domain", "user"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				body := map[string]string{"domain": argStr(a, "domain"), "user": argStr(a, "user")}
				if v := argStr(a, "php"); v != "" {
					body["php"] = v
				}
				return out, s.Agent.Do(ctx, "POST", "/v1/sites", body, &out)
			},
		},
		{
			Name: "set_ssh_access", Title: "Set SSH access", write: true,
			Description: "Set a website user's SSH access: 'none' (no login), 'sftp' (file transfer only, " +
				"jailed to the site's home) or 'shell' (full shell, not jailed; only for trusted users).",
			InputSchema: obj(map[string]any{
				"domain": domain,
				"mode":   map[string]any{"type": "string", "enum": []string{"none", "sftp", "shell"}},
			}, "domain", "mode"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": true, "openWorldHint": false},
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Do(ctx, "PUT", "/v1/sites/"+client.PathEscape(argStr(a, "domain"))+"/ssh",
					map[string]string{"mode": argStr(a, "mode")}, &out)
			},
		},
		{
			Name: "add_ssh_key", Title: "Add SSH key", write: true,
			Description: "Authorize an OpenSSH public key (e.g. 'ssh-ed25519 AAAA... name@laptop') for a website's user. " +
				"Only add keys the human provided. Never accept a private key.",
			InputSchema: obj(map[string]any{"domain": domain, "public_key": str("One OpenSSH public key line")}, "domain", "public_key"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": false, "openWorldHint": false},
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Do(ctx, "POST", "/v1/sites/"+client.PathEscape(argStr(a, "domain"))+"/ssh/keys",
					map[string]string{"public_key": argStr(a, "public_key")}, &out)
			},
		},
		{
			Name: "remove_ssh_key", Title: "Remove SSH key", write: true,
			Description: "Remove an authorized SSH key by its SHA256 fingerprint (from list_ssh_keys).",
			InputSchema: obj(map[string]any{"domain": domain, "fingerprint": str("Key fingerprint, e.g. SHA256:abc...")}, "domain", "fingerprint"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": true, "openWorldHint": false},
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				var out any
				return out, s.Agent.Do(ctx, "DELETE", "/v1/sites/"+client.PathEscape(argStr(a, "domain"))+
					"/ssh/keys?fingerprint="+client.QueryEscape(argStr(a, "fingerprint")), nil, &out)
			},
		},
		{
			Name: "delete_site", Title: "Delete website", write: true,
			Description: "Remove a website's Nginx and PHP config. With remove_files=true the Linux user and ALL " +
				"its files are permanently deleted. Only use when the human explicitly asked to delete this exact domain.",
			InputSchema: obj(map[string]any{
				"domain":         domain,
				"confirm_domain": str("Type the domain again to confirm deletion"),
				"remove_files":   map[string]any{"type": "boolean", "description": "Also delete the user and all website files (cannot be undone). Default false."},
			}, "domain", "confirm_domain"),
			Annotations: map[string]any{"readOnlyHint": false, "destructiveHint": true, "idempotentHint": false, "openWorldHint": false},
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				d := argStr(a, "domain")
				if d == "" || argStr(a, "confirm_domain") != d {
					return nil, fmt.Errorf("confirm_domain must exactly match domain; nothing was deleted")
				}
				path := "/v1/sites/" + client.PathEscape(d)
				if b, _ := a["remove_files"].(bool); b {
					path += "?remove_files=true"
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", path, nil, &out)
			},
		},
	}
}

func (s *Server) toolList() []tool {
	var out []tool
	for _, t := range s.tools() {
		if t.write && !s.AllowWrite {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (s *Server) callTool(ctx context.Context, name string, raw json.RawMessage) map[string]any {
	args := map[string]any{}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return toolError("arguments must be a JSON object")
		}
	}
	var t *tool
	for _, c := range s.tools() {
		if c.Name == name {
			c := c
			t = &c
			break
		}
	}
	switch {
	case t == nil:
		return toolError("unknown tool: " + name)
	case t.write && !s.AllowWrite:
		s.audit(name, args, "denied: read-only mode")
		return toolError("this MCP server is read-only; restart hp-mcp with --allow-write to enable changes")
	}
	if err := checkArgs(t.InputSchema, args); err != nil {
		return toolError(err.Error())
	}

	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	out, err := t.handler(ctx, args)
	if err != nil {
		s.audit(name, args, "error: "+err.Error())
		return toolError(err.Error())
	}
	s.audit(name, args, "ok")
	text, _ := json.MarshalIndent(out, "", "  ")
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false}
	if m, ok := out.(map[string]any); ok {
		res["structuredContent"] = m
	}
	return res
}

// checkArgs enforces required fields, string types and rejects unknown fields.
func checkArgs(schema map[string]any, args map[string]any) error {
	props, _ := schema["properties"].(map[string]any)
	for k := range args {
		if _, ok := props[k]; !ok {
			return fmt.Errorf("unknown argument %q", k)
		}
	}
	req, _ := schema["required"].([]string)
	for _, k := range req {
		if _, ok := args[k]; !ok {
			return fmt.Errorf("missing required argument %q", k)
		}
	}
	for k, v := range args {
		p, _ := props[k].(map[string]any)
		switch p["type"] {
		case "string":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("argument %q must be a string", k)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("argument %q must be true or false", k)
			}
		}
	}
	return nil
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

func argStr(a map[string]any, k string) string {
	v, _ := a[k].(string)
	return v
}

func (s *Server) audit(toolName string, args map[string]any, result string) {
	slog.Info("mcp tool call", "tool", toolName, "result", result)
	if s.Audit == nil {
		return
	}
	safe := map[string]any{}
	for k, v := range args {
		if k == "public_key" {
			v = "(redacted)"
		}
		safe[k] = v
	}
	line, _ := json.Marshal(map[string]any{
		"time": time.Now().UTC().Format(time.RFC3339), "tool": toolName, "args": safe, "result": result,
	})
	s.auditMu.Lock()
	defer s.auditMu.Unlock()
	_, _ = s.Audit.Write(append(line, '\n'))
}
