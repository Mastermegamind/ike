# Roadmap

## Phase 1 — Foundation (current)
- [x] Installer for Ubuntu 22.04 / 24.04 / 26.04 with OS, KVM, RAM and fresh-server checks
- [x] nginx.org and ondrej/php repos with fallback to Ubuntu packages; Ubuntu's MariaDB
- [x] hp-agent daemon on a root-only unix socket
- [x] Site creation: Linux user, home layout, PHP-FPM pool per site, Nginx vhost, config test + reload
- [x] Site deletion, system info, service status
- [x] SSH per site: none / SFTP-only (chroot) / shell, panel-managed keys
- [x] MCP server (stdio, works over SSH): read-only default, `--allow-write`, delete confirmation, audit log
- [ ] Let's Encrypt SSL (issue, renew, force HTTPS)
- [ ] Database + DB user creation (MariaDB)
- [ ] Control-plane API (auth, roles: admin / reseller / user, audit log)
- [ ] Web UI: login, dashboard, sites list, create site

## Phase 2 — Hosting essentials
- DNS with PowerDNS, cron jobs, log viewer, bandwidth/disk stats
- Plans and quotas (disk, sites, DBs), cgroups v2 CPU/RAM/IO limits per user
- Backups with restic to S3 / Backblaze B2 / Cloudflare R2 / local
- File manager, browser-based SSH terminal, extra SFTP accounts per site
- Jailed SSH shells (namespaces) so `shell` mode can be offered to untrusted users
- WordPress installer and toolkit

## Phase 3 — Email
- Postfix, Dovecot, Rspamd, DKIM/SPF/DMARC, Roundcube
- SMTP relay option (Brevo, Mailgun, Amazon SES) for providers that block port 25

## Phase 4 — Reseller & billing
- Reseller accounts, white-label branding
- WHMCS module, Paystack, Flutterwave and Stripe billing

## Phase 5 — App platform (Coolify-style)
- Docker + Traefik/Caddy, Git push deploys, preview environments, one-click apps

## Phase 6 — Scale
- Multi-server via agent join tokens (gRPC + mTLS)
- MCP over Streamable HTTP with per-user API tokens and scopes
- Create VPSes through Hetzner / DigitalOcean / Vultr / Linode APIs
- Server-to-server migration (e.g. 22.04 → 24.04 before end of support)
- cPanel backup importer
