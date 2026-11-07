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
- **Web server of the admin's choice:** Nginx, Apache, Nginx + Apache per website (`.htaccess` support), Caddy or OpenLiteSpeed
- **Maintenance and suspension:** branded status pages, allowed IPs during maintenance, account suspension that stops apps, cron and SSH
- **Custom error pages** (400–504), admin-branded with per-website overrides
- **Cron jobs, MariaDB databases, phpMyAdmin (stable or 6.0 preview), one-click WordPress**
- **Firewall:** open ports, block IPs and ranges, see and lift fail2ban bans
- **Detailed logs:** access, error, PHP, Apache and app logs, plus an audit log of every change
- **Modern web panel:** responsive, light/dark, 5 themes, 3 layout templates, role-aware (admin, reseller, account owner), and a data saver mode for slow connections
- **Sign-in:** separate ports for account owners (2083), resellers (2086) and admins (2087), two-factor codes with recovery codes, and passkeys (fingerprint, face or security key)
- **Billing in naira first:** invoices in NGN or any currency, paid through **Bachs**, **Paystack** or **Flutterwave**, confirmed by signed webhooks; overdue accounts are suspended and reactivated on payment
- **Alerts:** uptime and app-health alerts by **WhatsApp**, **Telegram** and email
- **Server care:** OS updates (security-only or all), automatic security updates, scheduled restarts, and one-click Ike updates with checksums
- **Health assistant:** reads a website's services and logs and explains what's wrong and how to fix it
- **Security scan and drift detection:** a scored baseline check, plus every hand-edited config file Ike manages, with a diff and one-click restore
- **Staging sites:** copy a website (and its database) to staging, then push it live without touching the live config
- **Resource limits and graphs:** CPU, memory, processes, disk and PHP workers per account, with 24-hour graphs
- **Environment variables** per website, and HTTP health checks that restart a failing app
- **Deploy from Git:** GitHub, GitLab, Bitbucket or any Git host; branches, tags (v*) or commits; deploy keys or tokens; push-to-deploy with signed webhooks; builds detected per language; atomic releases, zero-downtime blue-green for apps, rollback, preview websites per pull request, a build queue, and signed deployment notifications
- **HTTPS everywhere:** free Let's Encrypt certificates renewed automatically, or upload your own; force HTTPS, HSTS, HTTP/2 and HTTP/3 (QUIC); expiry alerts
- **Domains and redirects:** aliases, subdomains, *.wildcards, www handling, and redirect/rewrite rules
- **More languages:** Java, Go and Dart apps next to PHP, Node.js and Python; Composer and 40 PHP extensions per version; per-website PHP-FPM settings
- **Containers:** Docker containers and Compose stacks per account (policy-checked: no privileged or host-network containers), volumes, networks, private registries, image update alerts, Dockerfile/buildpack/image deployments, and optional Kubernetes (k3s) for admins
- **DNS:** zone editor for every record type, templates (website, email, Google Workspace, Microsoft 365, Zoho), PowerDNS with secondaries and DNSSEC, Cloudflare and DigitalOcean, domain registration, propagation and reverse DNS checks, expiry reminders, and wildcard certificates
- **Email:** Postfix, Dovecot, Rspamd and ClamAV; mailboxes, forwarders, auto-replies, catch-all, quotas, Roundcube webmail, DKIM/SPF/DMARC, SMTP relays for providers that block port 25, a mail queue and delivery log, and a deliverability checker that says what to fix
- **Databases:** MariaDB, PostgreSQL, MongoDB and a private Redis (Valkey) per account, with connection diagnostics, slow queries, imports and exports, and replication
- **Files:** a file manager with editor, archives, search, folder sizes, snapshots and expiring share links; a browser terminal; S3, Backblaze B2 and SFTP storage
- **Backups:** encrypted, incremental restic backups of the server, accounts, databases or mail, on a schedule with retention, integrity checks, one-click restores and full disaster recovery onto a new server
- **Security:** malware scanning with quarantine, a ModSecurity web firewall per site, and encrypted secrets with key rotation
- **Reseller hosting:** packages with enforced limits, reseller quotas, monthly bandwidth caps, recurring subscriptions, white-label branding, and WHMCS and Blesta modules with single sign-on
- **Many servers:** join servers over mutual TLS, switch between them in the panel, database clusters with failover, load balancing, and server specs you can plan and apply
- **cPanel migration:** restores a cPanel full backup: websites, files, databases (WordPress rewired), mailboxes with their passwords, forwarders, cron jobs and DNS
- **Panel:** Ctrl K command palette, keyboard shortcuts, notifications, help on every screen, in English, French, Nigerian Pidgin, Yoruba, Igbo and Hausa

- **SSH per account**: none, SFTP-only (chrooted to the account's home) or full shell, and each key can be narrowed to SFTP or read-only SFTP, some addresses and an expiry date; website SFTP users see one website only
- **Developers:** account owners invite a developer to chosen accounts and areas (websites, files, databases ...), never billing
- **Password-protected folders** with their own users, and **server graphs** for CPU, memory, swap, disk and network
- **Updates with a way back:** Ike and Ubuntu packages update when you approve; a new Ike version is checked after it starts and rolled back by itself if it isn't healthy ([updates](docs/UPDATES.md))
- **Frameworks:** Next.js, Nuxt, SvelteKit, Astro, Remix, NestJS, React, Vue and Angular deploy from Git without settings, with npm, pnpm, Yarn, Bun, Composer, Poetry, Pipenv, uv, Maven or Gradle
- **API, CLI and AI agents**: every screen is in the [API](docs/API.md), the [`ike` command](docs/CLI.md) and a 186-tool [MCP server](docs/MCP.md). Each gives full or limited access: API tokens can be limited to some areas (read or write), accounts and addresses, and can expire ([who can do what](docs/ACCESS.md))
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
wget https://github.com/Mastermegamind/ike/releases/latest/download/install.sh
less install.sh
sudo bash install.sh
```

When it finishes, open `https://SERVER-IP:2087` and sign in as the admin it created.

Installer options:

| Flag | Meaning |
|---|---|
| `--web nginx\|apache\|hybrid\|caddy\|openlitespeed` | Web server stack (default `nginx`; `hybrid` lets each website choose Nginx or Nginx + Apache) |
| `--firewall ufw\|nftables\|firewalld` | The firewall Ike drives (default `ufw`) |
| `--php "7.4 8.3"` or `--php all` | PHP versions, 5.6 to 8.5 (default: `8.2 8.3 8.4 8.5`) |
| `--node "20 22 24"` | Node.js major versions (default `22`, `none` to skip) |
| `--python "3.12 3.13"` | Python versions via uv (default `3.12`, `none` to skip) |
| `--phpmyadmin stable\|6\|none` | phpMyAdmin channel; `6` is the 6.0 development snapshot |
| `--no-mariadb` | Skip MariaDB |
| `--force` | Install even if the server doesn't look fresh |
| `--agent-binary PATH` | Use a locally built agent instead of downloading a release |
| `--mcp-binary PATH` | Use a locally built MCP server instead of downloading a release |
| `--panel-binary PATH` | Use a locally built web panel instead of downloading a release |
| `--java "17 21"`, `--maven` | OpenJDK versions, and Maven for Java builds |
| `--go "1.24"` | Go versions from go.dev |
| `--dart latest` | Dart SDK |
| `--docker` | Docker Engine with Compose and Buildx (`--docker-for-accounts` lets account owners use it) |
| `--buildpacks` | Cloud Native Buildpacks for Git deploys without a Dockerfile |
| `--k3s` | Single-node Kubernetes for admins |
| `--admin-user NAME --admin-email EMAIL` | The first admin sign-in |
| `--wizard` | Ask for each choice interactively |

What it sets up, per your choices: Nginx, Apache, Caddy or OpenLiteSpeed, PHP-FPM, Node.js, Python, MariaDB, phpMyAdmin, WP-CLI, OpenSSH with SFTP jail rules, a firewall (ufw, nftables or firewalld: SSH, 80, 443 and the panel), fail2ban, and the `ike-agent` service. Run `install.sh --help` for every option.

## SSH and SFTP

Each account's Linux user has one of three modes:

| Mode | Access |
|---|---|
| `none` (default) | No login |
| `sftp` | File transfer only, locked to the account's home folder |
| `shell` | Full shell, **not** jailed; only for users you trust |

Each key can then be narrowed: SFTP only or read-only SFTP even on a shell account, only from some addresses, and until a date. Keys live in root-owned files under `/etc/ssh/ike/keys/`, so a hosting user can't add keys behind the panel's back. Password logins are disabled for these users.

## API, CLI and AI agents (MCP)

```sh
ike login https://server.example.com:2087 --username marvellous
ike sites
ike deploy shop.ng --wait
```

`ike-mcp` lets AI clients manage the server, over SSH or through the panel with an API token:

```json
{ "mcpServers": { "my-vps": { "command": "ssh", "args": ["root@SERVER-IP", "ike-mcp"] } } }
```

It is read-only unless you add `--allow-write`. With a limited API token (`ike-mcp --panel https://server:2087`) the AI only sees the tools that token allows. See [ACCESS.md](docs/ACCESS.md), [API.md](docs/API.md), [CLI.md](docs/CLI.md) and [MCP.md](docs/MCP.md).

## Architecture

```
┌────────────────────────────────────────────┐
│  ike-panel: web UI, sign-in + 2FA, roles,  │   runs as the unprivileged user "ike"
│  billing, alerts  (ports 2083/2086/2087)   │
└───────────────────┬────────────────────────┘
                    │  unix socket here; mutual TLS to other servers
              ┌─────▼──────┐
              │  ike-agent  │   small Go daemon, the only root component
              └────────────┘
                Nginx · PHP-FPM · MariaDB · Linux users
```

The panel checks who you are and what your role allows, then forwards the request to the agent. The agent exposes a **small, fixed set of validated actions** (create site, delete site, system info…). It never runs arbitrary shell commands sent by the panel. This is the core security rule of the project.

## Repository layout

```
agent/        Go source: ike-agent (root daemon), ike-panel (web panel server), ike (CLI), ike-mcp (MCP server)
web/          React web panel (embedded into ike-panel; runs on demo data in the preview)
installer/    installer.sh (one-liner bootstrap) and install.sh for Ubuntu 22.04 / 24.04 / 26.04
deploy/       nginx config for serving the one-line installer
integrations/ WHMCS and Blesta provisioning modules
scripts/      Generators for docs/API.md and the feature tracker
docs/         Guides, API reference, feature tracker and roadmap
.github/      CI (Go tests, ShellCheck)
```

## Roadmap

v0.7.0 covers all 177 features in the [tracker](docs/FEATURES.md). Next: a one-click app catalog, a certificate for the panel's own address from the settings, MCP over HTTP from the panel, and creating servers through cloud provider APIs. See [docs/ROADMAP.md](docs/ROADMAP.md) and the [ports plan](docs/PORTS.md).

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

## License

[GNU AGPL-3.0](LICENSE). You can use, modify and self-host it freely. If you run a modified version as a service for others, you must publish your changes.

## Security

Please report vulnerabilities privately — see [SECURITY.md](SECURITY.md).
