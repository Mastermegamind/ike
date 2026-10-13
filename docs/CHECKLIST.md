# Ike feature checklist

Marked against Ike v0.4.0.

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
- [x] Domain, subdomain, wildcard and alias support — aliases, *.wildcards, www handling, redirect to main domain
- [x] Reverse proxy configuration — Node/Python apps, Nginx→Apache
- [x] HTTP/2 and HTTP/3 support where available — HTTP/3 with Nginx 1.25+ built with QUIC (nginx.org packages)
- [x] Per-website access/error logs
- [x] URL redirects and rewrite rules — exact, prefix and pattern rules; 301/302/307/308
- [x] Custom error pages and password protection — error pages; password protection planned
- [x] PHP-FPM pools per website
- [x] Website staging and cloning — staging copy with database, push to live
- [x] One-click WordPress installation — WP-CLI
- [~] Website migration and import — cPanel backups are read and planned; restore next

## PHP and runtime environments

- [x] Multiple PHP versions installed side by side — 5.6 → 8.5
- [x] Per-site PHP version selection
- [x] PHP-FPM pool configuration — process manager, workers, memory, uploads, time limits, timezone
- [x] PHP extensions management — 40 extensions per PHP version
- [x] Composer integration — install/update/require from the panel; run on Git deploys
- [x] Node.js and npm/pnpm/yarn management — Node versions; package managers via shell
- [x] Python virtual environments and pip — venv per app
- [x] Java runtime and build tooling — OpenJDK; Maven, Gradle wrapper on deploy
- [x] Go runtime support — go.dev releases, checksum-verified
- [x] Dart runtime support — Dart SDK from Google's archive
- [x] Environment variable and secrets management — editor per website; apps restart on save
- [x] Process management for long-running applications — systemd unit per app
- [x] Runtime-specific health checks — HTTP health check per app, restart or alert

## Git, CI/CD and deployment

- [x] GitHub, GitLab and Bitbucket integration — deploy keys, tokens, signed webhooks, setup steps; OAuth sign-in planned
- [x] Private repository authentication — SSH deploy keys or access tokens
- [x] Deploy from branch, tag or commit — tag patterns like v*
- [x] Git push-triggered deployments — GitHub, GitLab, Bitbucket, generic
- [x] Automatic application builds — detected per runtime, sandboxed as the account user
- [x] Dockerfile and buildpack deployments — plus registry images
- [x] Docker Compose project management — policy-checked stacks per account
- [x] Build logs and deployment history
- [x] Zero-downtime deployment strategies — atomic release switch; blue-green for apps and containers
- [x] Rollback to previous release
- [x] Preview deployments — per pull request; forks never built
- [x] Deployment webhooks and API — signed outgoing events, deploy API, MCP
- [x] Build queues and concurrency controls

## Docker and orchestration

- [x] Docker Engine integration
- [x] Container creation, start, stop and restart
- [x] Image registry management — registry logins, images, cleanup
- [x] Docker Compose stack deployment
- [~] Container logs and shell — logs and one-off commands; interactive shell planned with the web terminal
- [x] Volumes and persistent storage
- [x] Container networks and port mapping — ports on 127.0.0.1, published through websites
- [x] Resource quotas per container — plus the account's slice
- [x] Container health checks
- [x] Private registry credentials
- [x] Image update notifications — daily check, alerts
- [x] Optional Kubernetes integration — k3s single node for admins

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

- [x] Let's Encrypt certificate issuance and renewal
- [ ] Wildcard certificates with DNS-01 validation
- [x] Custom SSL certificate upload
- [x] Certificate expiration monitoring — alerts when renewal fails
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

**Totals:** 91 done, 17 partly done or screen only, 69 not yet, of 177.
