# AI agents (MCP)

HostPanel ships `hp-mcp`, a [Model Context Protocol](https://modelcontextprotocol.io) server. It lets AI clients such as Claude, Cursor or VS Code manage a server through the **same validated agent API** the panel uses. There is no shell tool: an AI can only do what the listed tools allow.

## Connect over SSH (recommended)

Nothing extra to open on the firewall. The AI client runs `hp-mcp` on the server through SSH using your existing key. Add this to your MCP client config:

```json
{
  "mcpServers": {
    "my-vps": {
      "command": "ssh",
      "args": ["root@203.0.113.10", "hp-mcp"]
    }
  }
}
```

For write access, add `"--allow-write"` after `"hp-mcp"`. Tip: use a dedicated SSH key for the AI and restrict it in `/root/.ssh/authorized_keys`:

```
command="/usr/local/bin/hp-mcp --allow-write",restrict ssh-ed25519 AAAA... ai-agent
```

With `restrict` and a forced command, that key can only ever start the MCP server — never a shell.

## Tools

| Tool | Mode | What it does |
|---|---|---|
| `get_system_info` | read | OS, kernel, CPU, RAM, disk, uptime, PHP versions |
| `get_service_status` | read | nginx, mariadb, PHP-FPM, fail2ban, agent |
| `list_sites` | read | All sites |
| `get_site` | read | One site |
| `list_ssh_keys` | read | Keys authorized for a site user |
| `create_site` | write | New Linux user + PHP-FPM pool + Nginx vhost |
| `set_ssh_access` | write | `none`, `sftp` (jailed) or `shell` |
| `add_ssh_key` | write | Authorize a public key |
| `remove_ssh_key` | write | Remove a key by fingerprint (destructive) |
| `delete_site` | write | Remove a site; requires `confirm_domain` (destructive) |

## Safety

- **Read-only by default.** Write tools are hidden and refused unless `--allow-write` is passed.
- **Confirmation for deletes.** `delete_site` fails unless `confirm_domain` repeats the domain exactly.
- **Strict input checks.** Unknown arguments are rejected; the agent re-validates every value.
- **Audit log.** Every call (allowed, denied or failed) is appended to `/var/log/hostpanel/mcp-audit.log` as JSON lines. Public keys are redacted.

## Coming later

When the control-plane API lands, MCP will also be served over **Streamable HTTP** with per-user API tokens and scopes (e.g. a reseller's token can only see that reseller's sites).
