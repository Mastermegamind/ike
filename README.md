# HostPanel

> **Working name.** Rename the project before the first public release (see "Renaming" below).

An open-source, all-in-one hosting control panel for Linux VPS servers. It combines:

- **Server administration** like Webmin (services, packages, firewall, cron, logs)
- **Multi-domain hosting** like Virtualmin (sites, DNS, mail, databases per domain)
- **Admin → Reseller → User** hosting like WHM/cPanel (plans, quotas, end-user panel)
- **Lightweight security-first design** like sPanel (low RAM, malware scanning, 2FA)
- **Modern app deployment** like Coolify (Git deploys, Docker apps, multi-server)

## Highlights

- **One Linux user per site** with its own PHP-FPM pool, `open_basedir` jail and Nginx config
- **Multiple PHP versions** side by side (8.1–8.5 via the ondrej/php PPA)
- **SSH per site**: none, SFTP-only (chrooted to the site's home) or full shell, with keys managed by the panel
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
curl -sSL https://raw.githubusercontent.com/YOUR-ORG/hostpanel/main/installer/install.sh | sudo bash
```

Or download it first and read it (recommended):

```bash
wget https://raw.githubusercontent.com/YOUR-ORG/hostpanel/main/installer/install.sh
less install.sh
sudo bash install.sh
```

Installer options:

| Flag | Meaning |
|---|---|
| `--php "8.3 8.4"` | PHP versions to install (default: `8.1 8.2 8.3 8.4`) |
| `--no-mariadb` | Skip MariaDB |
| `--force` | Install even if the server doesn't look fresh |
| `--agent-binary PATH` | Use a locally built agent instead of downloading a release |
| `--mcp-binary PATH` | Use a locally built MCP server instead of downloading a release |

What it sets up: Nginx (nginx.org repo, falling back to Ubuntu's), PHP-FPM, MariaDB (Ubuntu's LTS-maintained package), OpenSSH with SFTP jail rules, ufw firewall (SSH, 80, 443), fail2ban, and the `hp-agent` service.

## SSH and SFTP

Each site's Linux user has one of three modes, set through the agent API or MCP:

| Mode | Access |
|---|---|
| `none` (default) | No login |
| `sftp` | File transfer only, locked to the site's home folder |
| `shell` | Full shell, **not** jailed — only for users you trust |

Keys live in root-owned files under `/etc/ssh/hostpanel/keys/`, so a hosting user can't add keys behind the panel's back. Password logins are disabled for these users.

## AI agents (MCP)

`hp-mcp` lets AI clients manage the server over SSH, with no extra ports:

```json
{ "mcpServers": { "my-vps": { "command": "ssh", "args": ["root@SERVER-IP", "hp-mcp"] } } }
```

It is read-only unless you add `--allow-write`. See [docs/MCP.md](docs/MCP.md).

## Architecture

```
┌────────────────────────────────────────────┐
│  Control plane:  Web UI → API → Job queue  │   (coming next)
└───────────────────┬────────────────────────┘
                    │  unix socket today, gRPC + mTLS for remote nodes later
              ┌─────▼──────┐
              │  hp-agent  │   small Go daemon, the only root component
              └────────────┘
                Nginx · PHP-FPM · MariaDB · Linux users
```

The agent exposes a **small, fixed set of validated actions** (create site, delete site, system info…). It never runs arbitrary shell commands sent by the panel. This is the core security rule of the project.

## Repository layout

```
agent/        Go source: hp-agent (root daemon) and hp-mcp (MCP server)
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

See [docs/ROADMAP.md](docs/ROADMAP.md).

## Development

```bash
cd agent
go test ./...
go build -o hp-agent ./cmd/hp-agent
go build -o hp-mcp ./cmd/hp-mcp

# Run against a fake filesystem root without touching the system:
mkdir -p /tmp/hp-test/etc/php/8.3/fpm
./hp-agent --root /tmp/hp-test --dry-run --socket /tmp/hp.sock

# In another terminal:
curl --unix-socket /tmp/hp.sock -X POST http://agent/v1/sites -d '{"domain":"example.com","user":"example"}'
./hp-mcp --socket /tmp/hp.sock --audit-log ""   # then type JSON-RPC messages
```

Releases: push a tag like `v0.1.0` and GitHub Actions builds `hp-agent` and `hp-mcp` for amd64/arm64 with checksums, which the installer downloads.

## Renaming

The name `hostpanel` / `hp-agent` appears in: this README, `installer/install.sh` (the `PANEL_NAME` / `AGENT_NAME` variables at the top), `agent/go.mod` (module path), `.github/workflows`, and the Go import paths. Also replace `YOUR-ORG` with your GitHub user or organization. Search and replace `hostpanel` and `hp-agent` before publishing.

## License

[GNU AGPL-3.0](LICENSE). You can use, modify and self-host it freely. If you run a modified version as a service for others, you must publish your changes.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).
