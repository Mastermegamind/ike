# Ike feature checklist

Marked against Ike v0.3.0.

- `[x]` done: works end to end on a server
- `[~]` partly done, or the screen is in the panel and the server side is still planned
- `[ ]` not yet


## Server and operating system

- [x] Linux server installation and initial configuration — installer
- [x] CPU, RAM, disk, swap and network monitoring — CPU/RAM/disk live, 24 h graphs per account; swap & network planned
- [~] Service management with systemd — status done; start/stop planned
- [x] Process manager and resource limits — systemd unit per app; CPU, memory and process limits per account
- [x] System package installation and updates — apt updates, security-only option, job log
- [x] Linux user and group management — accounts = Linux users
- [x] SSH keys, SSH terminal and SFTP — SSH/SFTP + keys; web terminal in UI
- [x] Cron jobs and scheduled commands
- [x] Firewall, ports and network interfaces — ports & IP blocks; interfaces planned
- [x] Server reboot, shutdown and maintenance mode — scheduled restart; website maintenance mode
- [ ] Multi-server administration
- [x] OS security updates and vulnerability alerts — unattended security updates, security scan, alerts

## Websites and web servers

- [x] Apache, Nginx, OpenLiteSpeed and Caddy integration — Nginx, Apache, Nginx+Apache; OLS/Caddy planned
- [x] Virtual hosts and website creation
- [ ] Domain, subdomain, wildcard and alias support
- [x] Reverse proxy configuration — Node/Python apps, Nginx→Apache
- [ ] HTTP/2 and HTTP/3 support where available
- [x] Per-website access/error logs
- [ ] URL redirects and rewrite rules
- [x] Custom error pages and password protection — error pages; password protection planned
- [x] PHP-FPM pools per website
- [x] Website staging and cloning — staging copy with database, push to live
- [x] One-click WordPress installation — WP-CLI
- [~] Website migration and import — cPanel backups are read and planned; restore next

## PHP and runtime environments

- [x] Multiple PHP versions installed side by side — 5.6 → 8.5
- [x] Per-site PHP version selection
- [~] PHP-FPM pool configuration — PHP workers and memory per website; raw pool editor planned
- [ ] PHP extensions management
- [ ] Composer integration
- [x] Node.js and npm/pnpm/yarn management — Node versions; package managers via shell
- [x] Python virtual environments and pip — venv per app
- [ ] Java runtime and build tooling
- [ ] Go runtime support
- [x] Environment variable and secrets management — editor per website; apps restart on save
- [x] Process management for long-running applications — systemd unit per app
- [x] Runtime-specific health checks — HTTP health check per app, restart or alert

## Git, CI/CD and deployment

- [ ] GitHub, GitLab and Bitbucket integration
- [ ] Private repository authentication
- [ ] Deploy from branch, tag or commit
- [ ] Git push-triggered deployments
- [ ] Automatic application builds
- [ ] Dockerfile and buildpack deployments
- [ ] Docker Compose project management
- [ ] Build logs and deployment history
- [ ] Zero-downtime deployment strategies
- [ ] Rollback to previous release
- [ ] Preview deployments
- [ ] Deployment webhooks and API
- [ ] Build queues and concurrency controls

## Docker and orchestration

- [ ] Docker Engine integration
- [ ] Container creation, start, stop and restart
- [ ] Image registry management
- [ ] Docker Compose stack deployment
- [ ] Container logs and shell
- [ ] Volumes and persistent storage
- [ ] Container networks and port mapping
- [ ] Resource quotas per container
- [ ] Container health checks
- [ ] Private registry credentials
- [ ] Image update notifications
- [ ] Optional Kubernetes integration

## DNS and domain management

- [ ] DNS zone editor
- [ ] A, AAAA, CNAME, MX, TXT, NS, SRV and CAA records
- [ ] Domain registration integration
- [ ] Custom nameserver management
- [ ] Primary and secondary DNS support
- [ ] DNSSEC support
- [ ] Cloudflare and other DNS provider integrations
- [ ] DNS propagation checks
- [ ] Reverse DNS guidance or provider integration
- [ ] Domain expiration reminders
- [ ] DNS templates
- [ ] DNS API automation

## SSL and security

- [ ] Let's Encrypt certificate issuance and renewal
- [ ] Wildcard certificates with DNS-01 validation
- [ ] Custom SSL certificate upload
- [ ] Certificate expiration monitoring
- [x] UFW, nftables and firewalld integration — UFW
- [x] Fail2ban or CrowdSec integration — fail2ban
- [x] Two-factor authentication and passkeys — TOTP + recovery codes; passkeys planned
- [x] Role-based access control — admin / reseller / account owner, each on its own port
- [x] SSH hardening — key-only, jailed SFTP, root-owned keys
- [x] Security audit logs — agent + MCP audit logs
- [ ] Malware scanning and quarantine
- [ ] WAF integration such as ModSecurity
- [x] Rate limiting and brute-force protection — fail2ban jails
- [ ] Secret encryption and rotation
- [x] Security notifications — Telegram, WhatsApp and email

## Email hosting

- [ ] Email domain management
- [ ] Postfix and Dovecot integration
- [~] Mailboxes, aliases and forwarding
- [~] SMTP submission, IMAP and POP3 — ports & TLS plan in docs/PORTS.md
- [~] Webmail such as Roundcube or SnappyMail
- [~] SPF, DKIM and DMARC configuration — deliverability checker
- [ ] Spam filtering with Rspamd
- [ ] Antivirus integration
- [ ] Mail queue and delivery logs
- [~] Mailbox quotas and usage
- [ ] Email backups and restores
- [~] SMTP relay configuration
- [ ] TLS certificate management for mail
- [~] Email deliverability diagnostics

## Databases and caching

- [x] MySQL and MariaDB — MariaDB
- [ ] PostgreSQL
- [ ] Redis and Valkey
- [ ] MongoDB optional
- [x] Database creation and deletion
- [x] Database users and grants — one user per DB
- [x] phpMyAdmin and Adminer integration — phpMyAdmin stable / 6.0 preview
- [ ] Database connection diagnostics
- [ ] Database imports and exports
- [ ] Automated database backups
- [ ] Slow-query and performance monitoring
- [ ] Replication and high availability configuration

## File and storage management

- [ ] Browser-based file manager
- [ ] Upload, download, copy, move and rename
- [ ] ZIP and TAR extraction/compression
- [x] File permission and ownership editor — fix-permissions; per-file editor planned
- [ ] Code/text editor
- [x] SFTP account management — per account; per-website SFTP users planned
- [~] Disk quota enforcement — set per account; needs a quota-enabled filesystem
- [ ] S3-compatible object storage integration
- [~] Storage usage breakdown — disk per account; per-folder breakdown planned
- [ ] File search
- [ ] Snapshot and versioning integrations
- [ ] Secure file sharing

## Backups and disaster recovery

- [ ] Full server backup orchestration
- [ ] Per-website backups
- [ ] Database and email backups
- [ ] Incremental and differential backup support
- [ ] Scheduled backups and retention policies
- [ ] Local and remote backup targets
- [ ] S3, Backblaze B2 and other compatible storage
- [ ] Encrypted backups
- [ ] One-click restoration
- [ ] Backup integrity verification
- [ ] Off-site disaster recovery
- [ ] Recovery logs and notifications

## Reseller and shared hosting

- [x] Administrator, reseller, customer and developer roles — admin, reseller, account owner; developer role planned
- [~] Hosting packages and resource limits — limits per account; named packages next
- [x] Customer account provisioning
- [ ] Domain and website quotas
- [~] Bandwidth and disk quotas — disk quota; bandwidth measured, not capped yet
- [x] Customer isolation — Linux users, chroot SFTP, open_basedir, systemd sandbox
- [~] Subscription and billing integration — invoices, overdue suspension; recurring billing next
- [x] Invoice and payment gateway integration — invoices in naira or any currency, Bachs gateway
- [x] Paystack and Flutterwave integration — signed webhooks, amount verified with the provider
- [ ] WHMCS/Blesta or custom billing API
- [x] Suspension and reactivation
- [ ] White-label branding
- [x] Usage reporting — CPU, memory, disk, bandwidth per account

## Advanced infrastructure and automation

- [ ] Multiple server nodes and server grouping
- [~] Remote agents with secure communication — local socket today; mTLS between servers planned
- [ ] Infrastructure templates
- [x] API-first administration — agent JSON API + MCP
- [ ] CLI for automation
- [ ] Webhooks and event subscriptions
- [ ] Infrastructure as code integration
- [ ] Load balancer configuration
- [ ] Redis and database service clusters
- [x] Monitoring and alert routing — uptime + app health to Telegram, WhatsApp, email
- [ ] Scheduled maintenance workflows
- [ ] Server inventory and asset tracking
- [x] Audit trails and configuration history — audit trail, config drift detection and restore

## UI/UX and administration

- [x] Responsive desktop, tablet and mobile interface
- [x] Light and dark themes — plus a data saver mode for slow connections
- [~] Custom branding and theme colors — 5 themes, 3 layouts; admin branding planned
- [ ] Global search and command palette
- [x] Role-aware navigation
- [ ] Notification center
- [x] Interactive setup wizards — guided installer wizard
- [ ] Contextual documentation
- [ ] Keyboard shortcuts
- [x] Accessible forms and dashboards
- [x] Real-time activity feed — agent + panel audit log
- [ ] Multi-language readiness

**Totals:** 55 done, 17 partly done or screen only, 104 not yet, of 176.
