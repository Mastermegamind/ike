# AI agents (MCP)

Ike ships `ike-mcp`, a [Model Context Protocol](https://modelcontextprotocol.io) server. It lets AI clients such as Claude, Cursor or VS Code manage a server through the **same validated agent API** the panel uses. There is no shell tool: an AI can only do what the listed tools allow.

## Connect over SSH (recommended)

Nothing extra to open on the firewall. The AI client runs `ike-mcp` on the server through SSH using your existing key. Add this to your MCP client config:

```json
{
  "mcpServers": {
    "my-vps": {
      "command": "ssh",
      "args": ["root@203.0.113.10", "ike-mcp"]
    }
  }
}
```

For write access, add `"--allow-write"` after `"ike-mcp"`. Tip: use a dedicated SSH key for the AI and restrict it in `/root/.ssh/authorized_keys`:

```
command="/usr/local/bin/ike-mcp --allow-write",restrict ssh-ed25519 AAAA... ai-agent
```

With `restrict` and a forced command, that key can only ever start the MCP server — never a shell.

## Tools

63 tools. Read-only tools are always available; the rest need `--allow-write`.

| Area | Read | Write |
|---|---|---|
| Server | `get_system_info`, `get_service_status`, `list_runtimes`, `get_audit_log`, `check_updates`, `get_job`, `get_usage`, `security_scan`, `detect_drift` | `install_updates`, `reconcile_drift` |
| Accounts | `list_accounts`, `list_ssh_keys`, `list_cron_jobs`, `list_databases` | `create_account`, `set_limits`, `set_ssh_access`, `add_ssh_key`, `remove_ssh_key`, `add_cron_job`, `delete_cron_job`, `create_database`, `suspend_account`, `unsuspend_account`, `delete_account` |
| Websites | `list_sites`, `get_site`, `get_site_log`, `get_app_logs`, `list_apps`, `diagnose_site` | `create_site`, `update_site`, `set_site_status`, `app_action`, `install_wordpress`, `fix_permissions`, `create_staging`, `delete_site` |
| Deployments | `list_deployments`, `get_deployment` | `deploy_site`, `rollback_deployment` |
| HTTPS, domains, PHP | `list_certificates`, `list_php_extensions` | `issue_certificate`, `set_domains`, `set_redirects`, `run_composer`, `install_php_extension` |
| Containers | `list_containers`, `list_stacks`, `kubernetes_status` | `container_action`, `stack_action` |
| Firewall | `get_firewall`, `list_bans` | `open_port`, `close_port`, `block_address`, `unblock_address`, `unban_ip` |

## Safety

- **Read-only by default.** Write tools are hidden and refused unless `--allow-write` is passed.
- **Confirmation for deletes.** `delete_site` and `delete_account` fail unless the name is repeated exactly.
- **No lock-out.** SSH and web ports can't be closed, and loopback or overly broad ranges can't be blocked.
- **Secrets stay out of logs.** Database and WordPress passwords are passed on stdin and returned only once.
- **Strict input checks.** Unknown arguments are rejected; the agent re-validates every value.
- **Audit log.** Every call (allowed, denied or failed) is appended to `/var/log/ike/mcp-audit.log` as JSON lines. Public keys are redacted.

## Coming later

MCP will also be served over **Streamable HTTP** with per-user API tokens and scopes (e.g. a reseller's token can only see that reseller's sites).
