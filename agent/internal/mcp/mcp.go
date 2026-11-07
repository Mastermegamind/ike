// Package mcp implements a Model Context Protocol server (JSON-RPC 2.0 over stdio)
// so AI agents can manage a Ike server through the same validated agent API
// the panel uses. It never exposes a shell.
//
// Safety model:
//   - Read-only tools are always available.
//   - Write tools appear only when started with --allow-write.
//   - Connected to a panel with an API token, the panel enforces the token's
//     areas, accounts and addresses; tools the token can't use are hidden.
//   - --tools limits the tools to some areas (dns, mail, ...).
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

	"github.com/YOUR-ORG/ike/agent/internal/client"
)

const ServerName = "ike"

// Protocol versions this server can speak, newest first.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type Server struct {
	Agent      *client.Client
	AllowWrite bool
	Version    string
	Audit      io.Writer // JSON lines; may be nil

	// Panel is true when Agent goes through a panel with an API token, which
	// also enables the panel's own tools (packages, billing, servers).
	Panel bool
	// Groups, when set, limits the tools to these areas.
	Groups map[string]bool
	// Scopes is a limited token's access per area (read or write); nil
	// means the token (or local socket) is not limited by area.
	Scopes map[string]string
	// ReadOnlyToken hides write tools: the panel would refuse them anyway.
	ReadOnlyToken bool

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
		if s.AllowWrite && !s.ReadOnlyToken {
			mode = "read-write"
		}
		if s.Scopes != nil {
			mode += ", limited to the areas its API token allows"
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": ServerName, "version": s.Version},
			"instructions": "Ike server management (" + mode + "). The server holds hosting accounts (isolated Linux users); " +
				"each account holds websites, each with a runtime (PHP, Node.js, Python or static) and a web server. " +
				"Check list_accounts, list_sites and list_runtimes before creating anything. Never delete, suspend, " +
				"or change the firewall unless the human explicitly asked for that specific change.",
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
	area    string // token area: sites, dns, mail, databases, files, backups, containers, security, billing, accounts, server
	panel   bool   // needs a panel connection (--panel)
	handler func(ctx context.Context, args map[string]any) (any, error)
}

// Areas are the groups --tools accepts; the same names as API token areas.
var Areas = []string{"sites", "dns", "mail", "databases", "files", "backups", "containers", "security", "billing", "accounts", "server"}

// areaFor files the original tools by name.
func areaFor(name string) string {
	// Match the panel's token areas (panel/scopes.go areaOf) where the
	// name alone would mislead.
	switch name {
	case "get_job", "list_runtimes", "list_apps":
		return ""
	case "set_limits":
		return "accounts"
	case "add_cron_job", "list_cron_jobs", "delete_cron_job":
		return "sites"
	case "install_php_extension", "list_php_extensions":
		return "server"
	}
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(name, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("dns"):
		return "dns"
	case has("mail"):
		return "mail"
	case has("database", "redis"):
		return "databases"
	case has("backup", "snapshot"):
		return "backups"
	case has("container", "stack", "kubernetes"):
		return "containers"
	case has("firewall", "port", "ban", "address", "malware", "security", "waf", "quarantine"):
		return "security"
	case has("account", "ssh"):
		return "accounts"
	case has("site", "deploy", "certificate", "redirect", "domains", "wordpress", "composer", "php", "staging", "app", "permissions", "cron"):
		return "sites"
	}
	return "server"
}

// hidden says why a tool isn't offered, or "" when it is.
func (s *Server) hidden(t tool) string {
	switch {
	case t.write && !s.AllowWrite:
		return "this MCP server is read-only; restart ike-mcp with --allow-write to enable changes"
	case t.write && s.ReadOnlyToken:
		return "this API token is read-only"
	case t.panel && !s.Panel:
		return "this tool needs a panel connection: start ike-mcp with --panel https://your-panel:2087 and an API token"
	case s.Groups != nil && !s.Groups[t.area]:
		return "the " + t.area + " tools are turned off (--tools)"
	}
	if s.Scopes != nil && t.area != "" {
		switch s.Scopes[t.area] {
		case "write":
		case "read":
			if t.write {
				return "this API token can only read " + t.area
			}
		default:
			return "this API token has no access to " + t.area
		}
	}
	return ""
}

func obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

func readOnly() map[string]any { return map[string]any{"readOnlyHint": true, "openWorldHint": false} }

// Shared argument schemas.
var (
	argDomain  = str("Website domain, lowercase, e.g. example.com")
	argAccount = str("Hosting account name (its Linux username), e.g. adaeze")
)

func esc(v string) string { return client.PathEscape(v) }

// get / call build handlers for simple endpoints.
func (s *Server) get(path func(a map[string]any) string) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, a map[string]any) (any, error) {
		var out any
		return out, s.Agent.Get(ctx, path(a), &out)
	}
}

func (s *Server) call(method string, path func(a map[string]any) string, body func(a map[string]any) any) func(context.Context, map[string]any) (any, error) {
	return func(ctx context.Context, a map[string]any) (any, error) {
		var b any
		if body != nil {
			b = body(a)
		}
		var out any
		return out, s.Agent.Do(ctx, method, path(a), b, &out)
	}
}

func fixed(p string) func(map[string]any) string { return func(map[string]any) string { return p } }

// pick copies the named non-empty string arguments into a request body.
func pick(keys ...string) func(map[string]any) any {
	return func(a map[string]any) any {
		out := map[string]any{}
		for _, k := range keys {
			if v, ok := a[k]; ok && v != "" {
				out[k] = v
			}
		}
		return out
	}
}

func write(destructive, idempotent bool) map[string]any {
	return map[string]any{"readOnlyHint": false, "destructiveHint": destructive, "idempotentHint": idempotent, "openWorldHint": false}
}

func (s *Server) tools() []tool {
	acct := func(a map[string]any) string { return "/v1/accounts/" + esc(argStr(a, "account")) }
	site := func(a map[string]any) string { return "/v1/sites/" + esc(argStr(a, "domain")) }
	none := obj(map[string]any{})

	return []tool{
		// ------------------------------------------------------------ read
		{Name: "get_system_info", Title: "Server information", Annotations: readOnly(), InputSchema: none,
			Description: "OS, kernel, CPU, memory, disk, uptime, load and installed PHP/Node.js/Python versions.",
			handler:     s.get(fixed("/v1/system"))},
		{Name: "get_service_status", Title: "Service status", Annotations: readOnly(), InputSchema: none,
			Description: "Whether the web server(s), MariaDB, PHP-FPM, cron, fail2ban and the agent are running.",
			handler:     s.get(fixed("/v1/services"))},
		{Name: "list_runtimes", Title: "Runtimes and web servers", Annotations: readOnly(), InputSchema: none,
			Description: "Installed PHP, Node.js and Python versions, and which web servers the admin enabled. Check before create_site.",
			handler:     s.get(fixed("/v1/runtimes"))},
		{Name: "list_accounts", Title: "List accounts", Annotations: readOnly(), InputSchema: none,
			Description: "Hosting accounts on this server with SSH mode, suspension state and website count.",
			handler:     s.get(fixed("/v1/accounts"))},
		{Name: "list_sites", Title: "List websites", Annotations: readOnly(),
			Description: "Websites with account, runtime, version, web server and status. Optionally only one account's.",
			InputSchema: obj(map[string]any{"account": argAccount}),
			handler: s.get(func(a map[string]any) string {
				if v := argStr(a, "account"); v != "" {
					return "/v1/sites?account=" + client.QueryEscape(v)
				}
				return "/v1/sites"
			})},
		{Name: "get_site", Title: "Website details", Annotations: readOnly(),
			Description: "Details for one website.", InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler: s.get(site)},
		{Name: "list_ssh_keys", Title: "List SSH keys", Annotations: readOnly(),
			Description: "SSH public keys authorized for an account (type, fingerprint, comment).",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/ssh/keys" })},
		{Name: "list_cron_jobs", Title: "List cron jobs", Annotations: readOnly(),
			Description: "Scheduled jobs for an account.", InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler: s.get(func(a map[string]any) string { return acct(a) + "/cron" })},
		{Name: "get_app_logs", Title: "App logs", Annotations: readOnly(),
			Description: "Recent output of a Node.js or Python website's app process (errors, startup messages).",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"lines": map[string]any{"type": "integer", "description": "How many lines (1-1000, default 200)"}}, "domain"),
			handler: s.get(func(a map[string]any) string {
				p := site(a) + "/app/logs"
				if n, ok := a["lines"].(float64); ok {
					p += fmt.Sprintf("?lines=%d", int(n))
				}
				return p
			})},
		{Name: "list_databases", Title: "List databases", Annotations: readOnly(),
			Description: "An account's MariaDB databases (passwords are never shown again after creation).",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/databases" })},
		{Name: "get_site_log", Title: "Website logs", Annotations: readOnly(),
			Description: "Last lines of a website log: access, error, php, apache-access, apache-error or app.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"kind":  map[string]any{"type": "string", "enum": []string{"access", "error", "php", "apache-access", "apache-error", "app"}},
				"lines": map[string]any{"type": "integer", "description": "1-2000, default 200"}}, "domain", "kind"),
			handler: s.get(func(a map[string]any) string {
				p := site(a) + "/logs/" + esc(argStr(a, "kind"))
				if n, ok := a["lines"].(float64); ok {
					p += fmt.Sprintf("?lines=%d", int(n))
				}
				return p
			})},
		{Name: "get_audit_log", Title: "Audit log", Annotations: readOnly(), InputSchema: none,
			Description: "Recent changes made on this server: who, what, when and whether it worked.",
			handler:     s.get(fixed("/v1/audit"))},
		{Name: "check_updates", Title: "Pending updates", Annotations: readOnly(), InputSchema: none,
			Description: "Pending Ubuntu package updates (security ones flagged) and whether a reboot is needed.",
			handler:     s.get(fixed("/v1/updates"))},
		{Name: "get_job", Title: "Background job", Annotations: readOnly(),
			Description: "Status and log of a background job such as an update.",
			InputSchema: obj(map[string]any{"id": str("Job id")}, "id"),
			handler:     s.get(func(a map[string]any) string { return "/v1/jobs/" + esc(argStr(a, "id")) })},
		{Name: "diagnose_site", Title: "Diagnose website", Annotations: readOnly(),
			Description: "Health assistant: checks services, status, PHP version, disk and logs, and explains in plain words " +
				"why a website might be broken and how to fix it. Start here when someone says a site is down.",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.get(func(a map[string]any) string { return site(a) + "/diagnose" })},
		{Name: "get_usage", Title: "Resource usage", Annotations: readOnly(), InputSchema: none,
			Description: "CPU, memory, disk and today's bandwidth per account, with limits and 24 h history.",
			handler:     s.get(fixed("/v1/usage"))},
		{Name: "security_scan", Title: "Security scan", Annotations: readOnly(), InputSchema: none,
			Description: "Security baseline: SSH, firewall, fail2ban, updates, exposed services, PHP versions, HTTPS, file permissions. Score 0-100.",
			handler:     s.get(fixed("/v1/security"))},
		{Name: "detect_drift", Title: "Config drift", Annotations: readOnly(), InputSchema: none,
			Description: "Managed config files (Nginx, Apache, PHP pools, app units, cron) that were changed by hand, with a diff.",
			handler:     s.get(fixed("/v1/drift"))},
		{Name: "list_apps", Title: "One-click apps", Annotations: readOnly(), InputSchema: none,
			Description: "Apps that can be installed into a website (WordPress, ...).", handler: s.get(fixed("/v1/apps"))},
		{Name: "get_firewall", Title: "Firewall rules", Annotations: readOnly(), InputSchema: none,
			Description: "Which firewall the server uses (ufw, nftables or firewalld), its open ports (with any source limits) and blocked IP addresses/ranges.", handler: s.get(fixed("/v1/firewall"))},
		{Name: "list_bans", Title: "Automatic bans", Annotations: readOnly(), InputSchema: none,
			Description: "IP addresses fail2ban banned for repeated failed logins.", handler: s.get(fixed("/v1/firewall/bans"))},

		// ----------------------------------------------------------- write
		{Name: "create_database", Title: "Create database", write: true, Annotations: write(false, false),
			Description: "Create a MariaDB database and user named <account>_<name>. The password is shown once; give it to the human.",
			InputSchema: obj(map[string]any{"account": argAccount, "name": str("1-16 lowercase letters/digits, e.g. store"),
				"note": str("Short note (optional)")}, "account", "name"),
			handler: s.call("POST", func(a map[string]any) string { return acct(a) + "/databases" }, pick("name", "note"))},
		{Name: "install_wordpress", Title: "Install WordPress", write: true, Annotations: write(false, false),
			Description: "Install the latest WordPress into an empty PHP website: creates the database and admin user. " +
				"Returns the admin login once; give it to the human.",
			InputSchema: obj(map[string]any{"domain": argDomain, "title": str("Site title"),
				"admin_user": str("Admin username (not 'admin')"), "admin_email": str("Admin email"),
				"locale": str("Language, e.g. en_US (optional)")}, "domain", "title", "admin_user", "admin_email"),
			handler: s.call("POST", func(a map[string]any) string { return site(a) + "/apps/wordpress" },
				pick("title", "admin_user", "admin_email", "locale"))},
		{Name: "fix_permissions", Title: "Fix file permissions", write: true, Annotations: write(false, true),
			Description: "Reset a website's file ownership and modes to safe defaults (e.g. after an upload or migration).",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return site(a) + "/permissions" }, nil)},
		{Name: "install_updates", Title: "Install updates", write: true, Annotations: write(false, true),
			Description: "Install Ubuntu updates in the background (security_only=true for just security fixes). " +
				"Returns a job id; follow it with get_job. Only when the human asked.",
			InputSchema: obj(map[string]any{"security_only": map[string]any{"type": "boolean"}}),
			handler:     s.call("POST", fixed("/v1/updates/apply"), pick("security_only"))},
		{Name: "create_staging", Title: "Create staging copy", write: true, Annotations: write(false, false),
			Description: "Copy a website to staging.<domain> (files, and the database for WordPress), hidden from search engines. Returns a job id.",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return site(a) + "/staging" }, nil)},
		{Name: "list_deployments", Title: "Deployment history", Annotations: readOnly(),
			Description: "A website's deployments from Git or an image, newest first: commit, author, status (queued, building, live, failed), release.",
			InputSchema: obj(map[string]any{"domain": argDomain}, "domain"),
			handler:     s.get(func(a map[string]any) string { return site(a) + "/deployments" })},
		{Name: "get_deployment", Title: "Deployment and build log", Annotations: readOnly(),
			Description: "One deployment with its full build log. Use it to explain why a deploy failed.",
			InputSchema: obj(map[string]any{"domain": argDomain, "id": str("Deployment id from list_deployments")}, "domain", "id"),
			handler:     s.get(func(a map[string]any) string { return site(a) + "/deployments/" + esc(argStr(a, "id")) })},
		{Name: "list_certificates", Title: "HTTPS certificates", Annotations: readOnly(),
			Description: "Every website certificate with days left and any renewal problem.", InputSchema: none,
			handler: s.get(fixed("/v1/ssl"))},
		{Name: "list_php_extensions", Title: "PHP extensions", Annotations: readOnly(),
			Description: "Optional PHP extensions for one PHP version and whether each is installed.",
			InputSchema: obj(map[string]any{"version": str("PHP version, e.g. 8.3")}, "version"),
			handler:     s.get(func(a map[string]any) string { return "/v1/php/" + esc(argStr(a, "version")) + "/extensions" })},
		{Name: "list_containers", Title: "List containers", Annotations: readOnly(),
			Description: "An account's containers (and Compose stack and website containers) with state and ports.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/containers" })},
		{Name: "list_stacks", Title: "List Compose stacks", Annotations: readOnly(),
			Description: "An account's Docker Compose stacks and whether they run.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.get(func(a map[string]any) string { return acct(a) + "/stacks" })},
		{Name: "kubernetes_status", Title: "Kubernetes status", Annotations: readOnly(),
			Description: "Nodes and namespaces of the optional k3s cluster.", InputSchema: none,
			handler: s.get(fixed("/v1/kubernetes"))},
		{Name: "deploy_site", Title: "Deploy website", write: true, Annotations: write(false, false),
			Description: "Queue a deployment of a website connected to Git (or an image). Optional ref_type (branch, tag, commit) and ref " +
				"deploy something other than the configured branch. Returns the deployment; follow it with get_deployment.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"ref_type": map[string]any{"type": "string", "enum": []string{"branch", "tag", "commit"}}, "ref": str("Branch, tag or commit (optional)")}, "domain"),
			handler: s.call("POST", func(a map[string]any) string { return site(a) + "/deployments" }, pick("ref_type", "ref"))},
		{Name: "rollback_deployment", Title: "Roll back", write: true, Annotations: write(false, true),
			Description: "Switch a website back to the release a past deployment produced (see list_deployments).",
			InputSchema: obj(map[string]any{"domain": argDomain, "id": str("Deployment id to go back to")}, "domain", "id"),
			handler: s.call("POST", func(a map[string]any) string {
				return site(a) + "/deployments/" + esc(argStr(a, "id")) + "/rollback"
			}, nil)},
		{Name: "issue_certificate", Title: "Get HTTPS certificate", write: true, Annotations: write(false, true),
			Description: "Get a free Let's Encrypt certificate for a website (its domain must already point at this server) and turn on HTTPS. Returns a job id.",
			InputSchema: obj(map[string]any{"domain": argDomain, "include_aliases": map[string]any{"type": "boolean"}}, "domain"),
			handler:     s.call("POST", func(a map[string]any) string { return site(a) + "/ssl" }, pick("include_aliases"))},
		{Name: "set_redirects", Title: "Set redirects", write: true, Annotations: write(false, true),
			Description: "Replace a website's redirect and rewrite rules. Each rule: kind (redirect|rewrite), match (exact|prefix|regex), from, to, code (301|302|307|308), keep_query.",
			InputSchema: obj(map[string]any{"domain": argDomain, "rules": map[string]any{"type": "array", "items": map[string]any{"type": "object"}}}, "domain", "rules"),
			handler:     s.call("PUT", func(a map[string]any) string { return site(a) + "/redirects" }, func(a map[string]any) any { return a["rules"] })},
		{Name: "set_domains", Title: "Set extra domains", write: true, Annotations: write(false, true),
			Description: "Set a website's aliases (other domains, subdomains, *.wildcards), www handling (both|www|root|none) and whether aliases redirect to the main domain.",
			InputSchema: obj(map[string]any{"domain": argDomain, "aliases": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"www": map[string]any{"type": "string", "enum": []string{"both", "www", "root", "none"}}, "redirect_aliases": map[string]any{"type": "boolean"}}, "domain", "aliases"),
			handler: s.call("PUT", func(a map[string]any) string { return site(a) + "/domains" }, pick("aliases", "www", "redirect_aliases"))},
		{Name: "run_composer", Title: "Run Composer", write: true, Annotations: write(false, false),
			Description: "Run composer install, update, require, remove or dump-autoload in a PHP website, as the account user. Returns a job id.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"action":  map[string]any{"type": "string", "enum": []string{"install", "update", "require", "remove", "dump-autoload"}},
				"package": str("vendor/name[:constraint] for require/remove"), "dev": map[string]any{"type": "boolean"}}, "domain", "action"),
			handler: s.call("POST", func(a map[string]any) string { return site(a) + "/composer" }, pick("action", "package", "dev"))},
		{Name: "install_php_extension", Title: "Install PHP extension", write: true, Annotations: write(false, true),
			Description: "Install an optional PHP extension (see list_php_extensions) for one PHP version, then reload PHP. Returns a job id.",
			InputSchema: obj(map[string]any{"version": str("PHP version, e.g. 8.3"), "name": str("Extension, e.g. redis, imagick, intl")}, "version", "name"),
			handler: s.call("POST", func(a map[string]any) string {
				return "/v1/php/" + esc(argStr(a, "version")) + "/extensions/" + esc(argStr(a, "name"))
			}, nil)},
		{Name: "container_action", Title: "Start, stop or restart container", write: true, Annotations: write(false, true),
			Description: "Start, stop, restart or remove one of an account's containers (by its short name from list_containers).",
			InputSchema: obj(map[string]any{"account": argAccount, "container": str("Container short name"),
				"action": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart", "remove"}}}, "account", "container", "action"),
			handler: s.call("POST", func(a map[string]any) string {
				return acct(a) + "/containers/" + esc(argStr(a, "container")) + "/" + esc(argStr(a, "action"))
			}, nil)},
		{Name: "stack_action", Title: "Compose stack up/down/restart/pull", write: true, Annotations: write(false, true),
			Description: "Run up, down, restart or pull (then up) on an account's Compose stack. Returns a job id.",
			InputSchema: obj(map[string]any{"account": argAccount, "stack": str("Stack name"),
				"action": map[string]any{"type": "string", "enum": []string{"up", "down", "restart", "pull"}}}, "account", "stack", "action"),
			handler: s.call("POST", func(a map[string]any) string {
				return acct(a) + "/stacks/" + esc(argStr(a, "stack")) + "/" + esc(argStr(a, "action"))
			}, nil)},
		{Name: "set_limits", Title: "Set account limits", write: true, Annotations: write(false, true),
			Description: "Cap an account's CPU (100 = one core), memory, processes, disk and PHP workers. 0 = no limit.",
			InputSchema: obj(map[string]any{"account": argAccount,
				"cpu_percent": map[string]any{"type": "integer"}, "memory_mb": map[string]any{"type": "integer"},
				"max_processes": map[string]any{"type": "integer"}, "disk_mb": map[string]any{"type": "integer"},
				"php_workers": map[string]any{"type": "integer"}}, "account"),
			handler: s.call("PUT", func(a map[string]any) string { return acct(a) + "/limits" },
				pick("cpu_percent", "memory_mb", "max_processes", "disk_mb", "php_workers"))},
		{Name: "reconcile_drift", Title: "Restore drifted configs", write: true, Annotations: write(false, true),
			Description: "Rewrite config files that drifted (see detect_drift) to what Ike manages, then reload services. Hand edits are lost.",
			InputSchema: obj(map[string]any{"paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "Only these files (optional; default all)"}}),
			handler: s.call("POST", fixed("/v1/drift/reconcile"), pick("paths"))},
		{Name: "create_account", Title: "Create account", write: true, Annotations: write(false, false),
			Description: "Create a hosting account: an isolated Linux user that can hold many websites.",
			InputSchema: obj(map[string]any{"name": str("Account name: 3-16 lowercase letters/digits, starting with a letter")}, "name"),
			handler:     s.call("POST", fixed("/v1/accounts"), pick("name"))},
		{Name: "create_site", Title: "Create website", write: true, Annotations: write(false, false),
			Description: "Add a website to an existing account. Runtime 'php' (default) gets a PHP-FPM pool; 'node' and " +
				"'python' get an app service started with start_command (it must listen on $PORT); 'static' serves files. " +
				"Point the domain's DNS A record at this server for it to load.",
			InputSchema: obj(map[string]any{
				"domain":        argDomain,
				"account":       argAccount,
				"runtime":       map[string]any{"type": "string", "enum": []string{"php", "node", "python", "java", "go", "dart", "static", "proxy", "docker"}, "description": "Default php. proxy forwards to a local port (give port)."},
				"port":          map[string]any{"type": "integer", "description": "proxy runtime only: the local port to forward to"},
				"version":       str("Runtime version, e.g. 8.3, 5.6 (PHP), 22 (Node.js), 3.12 (Python). Optional; see list_runtimes."),
				"web_server":    map[string]any{"type": "string", "enum": []string{"nginx", "apache", "nginx+apache", "caddy", "openlitespeed"}, "description": "Optional; only choices the admin enabled work"},
				"start_command": str("Node.js/Python only, e.g. 'npm start' or 'gunicorn app:app --bind 127.0.0.1:$PORT'"),
			}, "domain", "account"),
			handler: s.call("POST", fixed("/v1/sites"), pick("domain", "account", "runtime", "version", "web_server", "start_command", "port"))},
		{Name: "update_site", Title: "Change version, web server or start command", write: true, Annotations: write(false, true),
			Description: "Switch a website to another installed runtime version (e.g. PHP 7.4 to 8.3), another enabled web server, " +
				"and/or change a Node.js/Python start command.",
			InputSchema: obj(map[string]any{"domain": argDomain, "version": str("New version (optional)"),
				"web_server":    map[string]any{"type": "string", "enum": []string{"nginx", "apache", "nginx+apache", "caddy", "openlitespeed"}},
				"start_command": str("New start command (optional)")}, "domain"),
			handler: s.call("PATCH", site, pick("version", "web_server", "start_command"))},
		{Name: "set_site_status", Title: "Maintenance / suspend website", write: true, Annotations: write(false, true),
			Description: "Set a website to 'active', 'maintenance' (visitors see a back-soon page; allow_ips still see the site) " +
				"or 'suspended'.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"status":    map[string]any{"type": "string", "enum": []string{"active", "maintenance", "suspended"}},
				"allow_ips": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Maintenance only"},
			}, "domain", "status"),
			handler: s.call("PUT", func(a map[string]any) string { return site(a) + "/status" }, pick("status", "allow_ips"))},
		{Name: "app_action", Title: "Start, stop or restart app", write: true, Annotations: write(false, true),
			Description: "Start, stop or restart a Node.js or Python website's app process.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"action": map[string]any{"type": "string", "enum": []string{"start", "stop", "restart"}}}, "domain", "action"),
			handler: s.call("POST", func(a map[string]any) string { return site(a) + "/app/" + esc(argStr(a, "action")) }, nil)},
		{Name: "set_ssh_access", Title: "Set SSH access", write: true, Annotations: write(false, true),
			Description: "Set an account's SSH access: 'none', 'sftp' (file transfer only, jailed to the account's home) " +
				"or 'shell' (full shell, not jailed; trusted users only).",
			InputSchema: obj(map[string]any{"account": argAccount,
				"mode": map[string]any{"type": "string", "enum": []string{"none", "sftp", "shell"}}}, "account", "mode"),
			handler: s.call("PUT", func(a map[string]any) string { return acct(a) + "/ssh" }, pick("mode"))},
		{Name: "add_ssh_key", Title: "Add SSH key", write: true, Annotations: write(false, false),
			Description: "Authorize an OpenSSH public key (e.g. 'ssh-ed25519 AAAA... name@laptop') for an account. " +
				"Only add keys the human provided. Never accept a private key.",
			InputSchema: obj(map[string]any{"account": argAccount, "public_key": str("One OpenSSH public key line")}, "account", "public_key"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/ssh/keys" }, pick("public_key"))},
		{Name: "remove_ssh_key", Title: "Remove SSH key", write: true, Annotations: write(true, true),
			Description: "Remove an authorized SSH key by its SHA256 fingerprint (from list_ssh_keys).",
			InputSchema: obj(map[string]any{"account": argAccount, "fingerprint": str("e.g. SHA256:abc...")}, "account", "fingerprint"),
			handler: s.call("DELETE", func(a map[string]any) string {
				return acct(a) + "/ssh/keys?fingerprint=" + client.QueryEscape(argStr(a, "fingerprint"))
			}, nil)},
		{Name: "add_cron_job", Title: "Add cron job", write: true, Annotations: write(false, false),
			Description: "Schedule a command for an account. It runs as the account's user from its home folder.",
			InputSchema: obj(map[string]any{"account": argAccount,
				"schedule": str("Cron schedule, e.g. '*/15 * * * *' or '@daily'"),
				"command":  str("Single-line command, e.g. 'php web/example.com/public_html/artisan schedule:run'"),
				"comment":  str("Short note (optional)")}, "account", "schedule", "command"),
			handler: s.call("POST", func(a map[string]any) string { return acct(a) + "/cron" }, pick("schedule", "command", "comment"))},
		{Name: "delete_cron_job", Title: "Delete cron job", write: true, Annotations: write(true, true),
			Description: "Delete a cron job by id (from list_cron_jobs).",
			InputSchema: obj(map[string]any{"account": argAccount, "id": str("Job id")}, "account", "id"),
			handler:     s.call("DELETE", func(a map[string]any) string { return acct(a) + "/cron/" + esc(argStr(a, "id")) }, nil)},
		{Name: "suspend_account", Title: "Suspend account", write: true, Annotations: write(false, true),
			Description: "Suspend an account: all its websites show the suspended page, apps and cron jobs stop, SSH is blocked. " +
				"Only when the human asked for it.",
			InputSchema: obj(map[string]any{"account": argAccount, "reason": str("Why (optional, shown to admins)")}, "account"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/suspend" }, pick("reason"))},
		{Name: "unsuspend_account", Title: "Reactivate account", write: true, Annotations: write(false, true),
			Description: "Lift an account suspension and restore its websites, SSH access and cron jobs.",
			InputSchema: obj(map[string]any{"account": argAccount}, "account"),
			handler:     s.call("POST", func(a map[string]any) string { return acct(a) + "/unsuspend" }, nil)},
		{Name: "open_port", Title: "Open firewall port", write: true, Annotations: write(false, false),
			Description: "Allow incoming traffic on a port or range, from anyone or only from one IP address or range. Only open ports the human asked for. Works the same with ufw, nftables or firewalld.",
			InputSchema: obj(map[string]any{"port": str("Port or range, e.g. 8080 or 6000:6100"),
				"proto":   map[string]any{"type": "string", "enum": []string{"tcp", "udp"}},
				"from":    str("Only this IP address or range may connect (optional; empty means anyone)"),
				"comment": str("Short note (optional)")}, "port", "proto"),
			handler: s.call("POST", fixed("/v1/firewall/ports"), pick("port", "proto", "comment", "from"))},
		{Name: "close_port", Title: "Close firewall port", write: true, Annotations: write(true, true),
			Description: "Close a port opened earlier. SSH and web ports can't be closed.",
			InputSchema: obj(map[string]any{"port": str("Port or range"),
				"proto": map[string]any{"type": "string", "enum": []string{"tcp", "udp"}},
				"from":  str("The IP address or range the rule was limited to (empty for open-to-anyone rules)")}, "port", "proto"),
			handler: s.call("DELETE", func(a map[string]any) string {
				q := "/v1/firewall/ports?port=" + client.QueryEscape(argStr(a, "port")) + "&proto=" + client.QueryEscape(argStr(a, "proto"))
				if f := argStr(a, "from"); f != "" {
					q += "&from=" + client.QueryEscape(f)
				}
				return q
			}, nil)},
		{Name: "block_address", Title: "Block IP address", write: true, Annotations: write(false, false),
			Description: "Block all traffic from an IP address or range (e.g. 203.0.113.7 or 203.0.113.0/24).",
			InputSchema: obj(map[string]any{"address": str("IP or CIDR range"), "comment": str("Why (optional)")}, "address"),
			handler:     s.call("POST", fixed("/v1/firewall/blocks"), pick("address", "comment"))},
		{Name: "unblock_address", Title: "Unblock IP address", write: true, Annotations: write(false, true),
			Description: "Remove an address from the block list.",
			InputSchema: obj(map[string]any{"address": str("IP or CIDR range")}, "address"),
			handler: s.call("DELETE", func(a map[string]any) string {
				return "/v1/firewall/blocks?address=" + client.QueryEscape(argStr(a, "address"))
			}, nil)},
		{Name: "unban_ip", Title: "Lift automatic ban", write: true, Annotations: write(false, true),
			Description: "Release an IP banned by fail2ban (see list_bans).",
			InputSchema: obj(map[string]any{"jail": str("Jail name, e.g. sshd"), "ip": str("Banned IP")}, "jail", "ip"),
			handler: s.call("DELETE", func(a map[string]any) string {
				return "/v1/firewall/bans?jail=" + client.QueryEscape(argStr(a, "jail")) + "&ip=" + client.QueryEscape(argStr(a, "ip"))
			}, nil)},
		{Name: "delete_site", Title: "Delete website", write: true, Annotations: write(true, false),
			Description: "Remove a website's configuration. With remove_files=true its folder is permanently deleted. " +
				"The account and its other websites stay. Only when the human explicitly asked to delete this exact domain.",
			InputSchema: obj(map[string]any{"domain": argDomain,
				"confirm_domain": str("Type the domain again to confirm"),
				"remove_files":   map[string]any{"type": "boolean", "description": "Also delete the website's files (cannot be undone)"},
			}, "domain", "confirm_domain"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				d := argStr(a, "domain")
				if d == "" || argStr(a, "confirm_domain") != d {
					return nil, fmt.Errorf("confirm_domain must exactly match domain; nothing was deleted")
				}
				p := site(a)
				if b, _ := a["remove_files"].(bool); b {
					p += "?remove_files=true"
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", p, nil, &out)
			}},
		{Name: "delete_account", Title: "Delete account", write: true, Annotations: write(true, false),
			Description: "Permanently delete an account, its Linux user and ALL its files. With with_sites=true its websites " +
				"are deleted too; otherwise accounts with websites are refused. Only when the human explicitly asked.",
			InputSchema: obj(map[string]any{"account": argAccount,
				"confirm_account": str("Type the account name again to confirm"),
				"with_sites":      map[string]any{"type": "boolean", "description": "Also delete all of its websites"},
			}, "account", "confirm_account"),
			handler: func(ctx context.Context, a map[string]any) (any, error) {
				n := argStr(a, "account")
				if n == "" || argStr(a, "confirm_account") != n {
					return nil, fmt.Errorf("confirm_account must exactly match account; nothing was deleted")
				}
				p := acct(a)
				if b, _ := a["with_sites"].(bool); b {
					p += "?with_sites=true"
				}
				var out any
				return out, s.Agent.Do(ctx, "DELETE", p, nil, &out)
			}},
	}
}

func (s *Server) allTools() []tool {
	all := append(s.tools(), s.tools5()...)
	for i := range all {
		if all[i].area == "" {
			all[i].area = areaFor(all[i].Name)
		}
	}
	return all
}

func (s *Server) toolList() []tool {
	out := []tool{}
	for _, t := range s.allTools() {
		if s.hidden(t) == "" {
			out = append(out, t)
		}
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
	for _, c := range s.allTools() {
		if c.Name == name {
			c := c
			t = &c
			break
		}
	}
	switch {
	case t == nil:
		return toolError("unknown tool: " + name)
	case s.hidden(*t) != "":
		why := s.hidden(*t)
		s.audit(name, args, "denied: "+why)
		return toolError(why)
	}
	if err := checkArgs(t.InputSchema, args); err != nil {
		return toolError(err.Error())
	}

	ctx, cancel := context.WithTimeout(ctx, 6*time.Minute)
	defer cancel()
	out, err := t.handler(ctx, args)
	if text, ok := out.(rawText); ok && err == nil {
		s.audit(name, args, "ok")
		return map[string]any{"content": []map[string]any{{"type": "text", "text": string(text)}}, "isError": false}
	}
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
			sv, ok := v.(string)
			if !ok {
				return fmt.Errorf("argument %q must be a string", k)
			}
			if enum, ok := p["enum"].([]string); ok && !contains(enum, sv) {
				return fmt.Errorf("argument %q must be one of %s", k, strings.Join(enum, ", "))
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("argument %q must be true or false", k)
			}
		case "array":
			if _, ok := v.([]any); !ok {
				return fmt.Errorf("argument %q must be a list", k)
			}
		case "object":
			if _, ok := v.(map[string]any); !ok {
				return fmt.Errorf("argument %q must be an object", k)
			}
		case "number":
			if _, ok := v.(float64); !ok {
				return fmt.Errorf("argument %q must be a number", k)
			}
		case "integer":
			if f, ok := v.(float64); !ok || f != float64(int(f)) {
				return fmt.Errorf("argument %q must be a whole number", k)
			}
		}
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
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
		switch k {
		case "public_key", "password", "content", "zone_file", "recovery_password":
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
