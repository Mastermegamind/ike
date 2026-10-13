# Ike

> *Ike* (pronounced **ee-keh**) is Igbo for **strength, power**: servers that stay strong.

An open-source, all-in-one hosting control panel for Linux VPS servers. It combines:

- **Server administration** like Webmin (services, packages, firewall, cron, logs)
- **Multi-domain hosting** like Virtualmin (sites, DNS, mail, databases per domain)
- **Admin → Reseller → User** hosting like WHM/cPanel (plans, quotas, end-user panel)
- **Lightweight security-first design** like sPanel (low RAM, malware scanning, 2FA)
- **Modern app deployment** like Coolify (Git deploys, Docker apps, multi-server)

## Highlights

- **Accounts → websites:** a server holds many isolated accounts; each account holds many websites
- **Runtimes per website:** PHP 5.6–8.5 side by side, Node.js, Python (own virtualenv) or static
- **Web server of the admin's choice:** Nginx, Apache, or Nginx + Apache per website (`.htaccess` support)
- **Maintenance and suspension:** branded status pages, allowed IPs during maintenance, account suspension that stops apps, cron and SSH
- **Custom error pages** (400–504), admin-branded with per-website overrides
- **Cron jobs, MariaDB databases, phpMyAdmin (stable or 6.0 preview), one-click WordPress**
- **Firewall:** open ports, block IPs and ranges, see and lift fail2ban bans
- **Detailed logs:** access, error, PHP, Apache and app logs, plus an audit log of every change
- **Modern web panel:** responsive, light/dark, 5 themes, 3 layout templates, role-aware (admin, reseller, account owner)

- **SSH per account**: none, SFTP-only (chrooted to the account's home) or full shell, with keys managed by the panel
- **AI-ready**: built-in [MCP server](docs/MCP.md) so AI agents (Claude, Cursor, VS Code…) can manage the server safely, read-only by default, with an audit log
- **Safe by design**: the root agent only accepts a fixed set of validated actions, never shell commands; failed changes roll back automatically

## Supported systems

| OS | Status |
|---|---|
| Ubuntu 22.04 LTS | Supported (standard support ends April 2027) |
| Ubuntu 24.04 LTS | Supported (recommended) |
| Ubuntu 26.04 LTS | Supported |

Requirements: a **fresh** KVM VPS (OpenVZ/LXC is not supported), root access, at least 1 GB RAM (2 GB+ if you enable email), x86_64 or arm64.

## Install

```bash
curl -sSL https://raw.githubusercontent.com/YOUR-ORG/ike/main/installer/install.sh | sudo bash
```

Or download it first and read it (recommended):

```bash
wget https://raw.githubusercontent.com/YOUR-ORG/ike/main/installer/install.sh
less install.sh
sudo bash install.sh
```

Installer options:

| Flag | Meaning |
|---|---|
| `--web nginx\|apache\|hybrid` | Web server stack (default `nginx`; `hybrid` lets each website choose Nginx or Nginx + Apache) |
| `--php "7.4 8.3"` or `--php all` | PHP versions, 5.6 to 8.5 (default: `8.2 8.3 8.4 8.5`) |
| `--node "20 22 24"` | Node.js major versions (default `22`, `none` to skip) |
| `--python "3.12 3.13"` | Python versions via uv (default `3.12`, `none` to skip) |
| `--phpmyadmin stable\|6\|none` | phpMyAdmin channel; `6` is the 6.0 development snapshot |
| `--no-mariadb` | Skip MariaDB |
| `--force` | Install even if the server doesn't look fresh |
| `--agent-binary PATH` | Use a locally built agent instead of downloading a release |
| `--mcp-binary PATH` | Use a locally built MCP server instead of downloading a release |

What it sets up, per your choices: Nginx and/or Apache, PHP-FPM, Node.js, Python, MariaDB, phpMyAdmin, WP-CLI, OpenSSH with SFTP jail rules, ufw (SSH, 80, 443), fail2ban, and the `ike-agent` service. Run `install.sh --help` for every option.

## SSH and SFTP

Each account's Linux user has one of three modes, set through the agent API or MCP:

| Mode | Access |
|---|---|
| `none` (default) | No login |
| `sftp` | File transfer only, locked to the account's home folder |
| `shell` | Full shell, **not** jailed — only for users you trust |

Keys live in root-owned files under `/etc/ssh/ike/keys/`, so a hosting user can't add keys behind the panel's back. Password logins are disabled for these users.

## AI agents (MCP)

`ike-mcp` lets AI clients manage the server over SSH, with no extra ports:

```json
{ "mcpServers": { "my-vps": { "command": "ssh", "args": ["root@SERVER-IP", "ike-mcp"] } } }
```

It is read-only unless you add `--allow-write`. See [docs/MCP.md](docs/MCP.md).

## Architecture

```
┌────────────────────────────────────────────┐
│  Control plane:  Web UI → API → Job queue  │   (coming next)
└───────────────────┬────────────────────────┘
                    │  unix socket today, gRPC + mTLS for remote nodes later
              ┌─────▼──────┐
              │  ike-agent  │   small Go daemon, the only root component
              └────────────┘
                Nginx · PHP-FPM · MariaDB · Linux users
```

The agent exposes a **small, fixed set of validated actions** (create site, delete site, system info…). It never runs arbitrary shell commands sent by the panel. This is the core security rule of the project.

## Repository layout

```
agent/        Go source: ike-agent (root daemon) and ike-mcp (MCP server)
web/          React web panel (themes, layout templates, demo data)
installer/    install.sh for Ubuntu 22.04 / 24.04 / 26.04
docs/         Design notes and roadmap
.github/      CI (Go tests, ShellCheck)
```

## Roadmap

1. **Foundation** — installer, agent, sites (Nginx + PHP-FPM per user), SSH/SFTP, MCP, SSL, databases ← *in progress*
2. Hosting essentials — DNS (PowerDNS), cron, logs, quotas/plans, backups (restic), WordPress toolkit
3. Email — Postfix, Dovecot, Rspamd, DKIM, SMTP relay option (many VPS providers block port 25)
4. Reseller layer — reseller accounts, branding, billing hooks (WHMCS, Paystack, Flutterwave, Stripe)
5. App platform — Git deploys, Docker apps, one-click app catalog
6. Scale — multi-server, server-to-server migration, cPanel backup importer

See [docs/ROADMAP.md](docs/ROADMAP.md), the full [feature tracker](docs/FEATURES.md) and the [ports plan](docs/PORTS.md) (panel, reseller, admin, webmail, and SSL/TLS + STARTTLS mail ports).

## Development

```bash
cd agent
go test ./...
go build -o ike-agent ./cmd/ike-agent
go build -o ike-mcp ./cmd/ike-mcp

# Run against a fake filesystem root without touching the system:
mkdir -p /tmp/ike-test/etc/php/8.3/fpm
./ike-agent --root /tmp/ike-test --dry-run --socket /tmp/ike.sock

# In another terminal:
curl --unix-socket /tmp/ike.sock -X POST http://agent/v1/accounts -d '{"name":"example"}'
curl --unix-socket /tmp/ike.sock -X POST http://agent/v1/sites -d '{"domain":"example.com","account":"example"}'
./ike-mcp --socket /tmp/ike.sock --audit-log ""   # then type JSON-RPC messages
```

Releases: push a tag like `v0.1.0` and GitHub Actions builds `ike-agent` and `ike-mcp` for amd64/arm64 with checksums, which the installer downloads.

## Before publishing

Replace `YOUR-ORG` with your GitHub user or organization in: this README, `installer/install.sh` (`REPO=`), `agent/go.mod` and the Go import paths, and `SECURITY.md`.

## License

[GNU AGPL-3.0](LICENSE). You can use, modify and self-host it freely. If you run a modified version as a service for others, you must publish your changes.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).
