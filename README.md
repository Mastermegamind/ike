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
- **Modern web panel:** responsive, light/dark, 5 themes, 3 layout templates, role-aware (admin, reseller, account owner), and a data saver mode for slow connections
- **Sign-in:** separate ports for account owners (2083), resellers (2086) and admins (2087), two-factor codes with recovery codes
- **Billing in naira first:** invoices in NGN or any currency, paid through **Bachs**, **Paystack** or **Flutterwave**, confirmed by signed webhooks; overdue accounts are suspended and reactivated on payment
- **Alerts:** uptime and app-health alerts by **WhatsApp**, **Telegram** and email
- **Server care:** OS updates (security-only or all), automatic security updates, scheduled restarts, and one-click Ike updates with checksums
- **Health assistant:** reads a website's services and logs and explains what's wrong and how to fix it
- **Security scan and drift detection:** a scored baseline check, plus every hand-edited config file Ike manages, with a diff and one-click restore
- **Staging sites:** copy a website (and its database) to staging, then push it live without touching the live config
- **Resource limits and graphs:** CPU, memory, processes, disk and PHP workers per account, with 24-hour graphs
- **Environment variables** per website, and HTTP health checks that restart a failing app
- **cPanel migration:** reads a cPanel full backup and shows what will come across (restore is next)

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
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash
```

A guided wizard asks what to install. To skip it, pass options: `... | sudo bash -s -- --web hybrid --php all`. The one-liner downloads `install.sh` from the latest release and checks its SHA-256 before running it. See [docs/INSTALLER.md](docs/INSTALLER.md), including how the domain is set up.

Or download the installer and read it first:

```bash
wget https://github.com/YOUR-ORG/ike/releases/latest/download/install.sh
less install.sh
sudo bash install.sh
```

When it finishes, open `https://SERVER-IP:2087` and sign in as the admin it created.

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
| `--panel-binary PATH` | Use a locally built web panel instead of downloading a release |
| `--admin-user NAME --admin-email EMAIL` | The first admin sign-in |
| `--wizard` | Ask for each choice interactively |

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
│  ike-panel: web UI, sign-in + 2FA, roles,  │   runs as the unprivileged user "ike"
│  billing, alerts  (ports 2083/2086/2087)   │
└───────────────────┬────────────────────────┘
                    │  unix socket today, gRPC + mTLS for remote nodes later
              ┌─────▼──────┐
              │  ike-agent  │   small Go daemon, the only root component
              └────────────┘
                Nginx · PHP-FPM · MariaDB · Linux users
```

The panel checks who you are and what your role allows, then forwards the request to the agent. The agent exposes a **small, fixed set of validated actions** (create site, delete site, system info…). It never runs arbitrary shell commands sent by the panel. This is the core security rule of the project.

## Repository layout

```
agent/        Go source: ike-agent (root daemon), ike-mcp (MCP server), ike-panel (web panel server)
web/          React web panel (embedded into ike-panel; runs on demo data in the preview)
installer/    installer.sh (one-liner bootstrap) and install.sh for Ubuntu 22.04 / 24.04 / 26.04
deploy/       nginx config for serving the one-line installer
docs/         Design notes and roadmap
.github/      CI (Go tests, ShellCheck)
```

## Roadmap

1. **Foundation** — installer, agent, web panel, sites, SSH/SFTP, MCP, databases, billing, alerts — *done except SSL*
2. Hosting essentials — **SSL (Let's Encrypt) next**, DNS (PowerDNS), backups (restic), file manager, plans
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

The web panel: `cd web && npm install && npm run dev` runs it on demo data. `make panel` builds it in live mode and embeds it into `ike-panel`.

Releases: push a tag like `v0.3.0` and GitHub Actions builds `ike-agent`, `ike-mcp` and `ike-panel` for amd64/arm64, and attaches them with `install.sh`, `installer.sh` and SHA-256 checksums.

## Before publishing

Replace `YOUR-ORG` with your GitHub user or organization in: this README, `installer/install.sh` and `installer/installer.sh` (`IKE_REPO` default), `deploy/*.conf`, `agent/go.mod` and the Go import paths, `docs/`, the web panel links, and `SECURITY.md`:

```bash
grep -rl YOUR-ORG --exclude-dir=node_modules --exclude-dir=.git . | xargs sed -i 's/YOUR-ORG/your-github-name/g'
```

## License

[GNU AGPL-3.0](LICENSE). You can use, modify and self-host it freely. If you run a modified version as a service for others, you must publish your changes.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).
