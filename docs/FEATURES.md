# Feature tracker

Ike's full scope, grouped by area. Pick anything marked *planned* and open a PR — see [CONTRIBUTING.md](../CONTRIBUTING.md).

**91 done · 9 partly done · 8 screen only · 69 planned** (of 177)

| Mark | Meaning |
|---|---|
| ✅ | Done: works end to end on a server |
| 🟡 | Partly done (the notes say which part) |
| 🖥️ | Screen is in the web panel; the server side is still planned |
| 🔜 | Planned — phase number from [ROADMAP.md](ROADMAP.md) |


## Server and operating system

| | Feature | Notes |
|---|---|---|
| ✅ | Linux server installation and initial configuration | installer |
| ✅ | CPU, RAM, disk, swap and network monitoring | CPU/RAM/disk live, 24 h graphs per account; swap & network planned |
| 🖥️ | Service management with systemd | status done; start/stop planned |
| ✅ | Process manager and resource limits | systemd unit per app; CPU, memory and process limits per account |
| ✅ | System package installation and updates | apt updates, security-only option, job log |
| ✅ | Linux user and group management | accounts = Linux users |
| ✅ | SSH keys, SSH terminal and SFTP | SSH/SFTP + keys; web terminal in UI |
| ✅ | Cron jobs and scheduled commands |  |
| ✅ | Firewall, ports and network interfaces | ports & IP blocks; interfaces planned |
| ✅ | Server reboot, shutdown and maintenance mode | scheduled restart; website maintenance mode |
| 🔜 2 | Multi-server administration |  |
| ✅ | OS security updates and vulnerability alerts | unattended security updates, security scan, alerts |

## Websites and web servers

| | Feature | Notes |
|---|---|---|
| ✅ | Apache, Nginx, OpenLiteSpeed and Caddy integration | Nginx, Apache, Nginx+Apache; OLS/Caddy planned |
| ✅ | Virtual hosts and website creation |  |
| ✅ | Domain, subdomain, wildcard and alias support | aliases, *.wildcards, www handling, redirect to main domain |
| ✅ | Reverse proxy configuration | Node/Python apps, Nginx→Apache |
| ✅ | HTTP/2 and HTTP/3 support where available | HTTP/3 with Nginx 1.25+ built with QUIC (nginx.org packages) |
| ✅ | Per-website access/error logs |  |
| ✅ | URL redirects and rewrite rules | exact, prefix and pattern rules; 301/302/307/308 |
| ✅ | Custom error pages and password protection | error pages; password protection planned |
| ✅ | PHP-FPM pools per website |  |
| ✅ | Website staging and cloning | staging copy with database, push to live |
| ✅ | One-click WordPress installation | WP-CLI |
| 🟡 | Website migration and import | cPanel backups are read and planned; restore next |

## PHP and runtime environments

| | Feature | Notes |
|---|---|---|
| ✅ | Multiple PHP versions installed side by side | 5.6 → 8.5 |
| ✅ | Per-site PHP version selection |  |
| ✅ | PHP-FPM pool configuration | process manager, workers, memory, uploads, time limits, timezone |
| ✅ | PHP extensions management | 40 extensions per PHP version |
| ✅ | Composer integration | install/update/require from the panel; run on Git deploys |
| ✅ | Node.js and npm/pnpm/yarn management | Node versions; package managers via shell |
| ✅ | Python virtual environments and pip | venv per app |
| ✅ | Java runtime and build tooling | OpenJDK; Maven, Gradle wrapper on deploy |
| ✅ | Go runtime support | go.dev releases, checksum-verified |
| ✅ | Dart runtime support | Dart SDK from Google's archive |
| ✅ | Environment variable and secrets management | editor per website; apps restart on save |
| ✅ | Process management for long-running applications | systemd unit per app |
| ✅ | Runtime-specific health checks | HTTP health check per app, restart or alert |

## Git, CI/CD and deployment

| | Feature | Notes |
|---|---|---|
| ✅ | GitHub, GitLab and Bitbucket integration | deploy keys, tokens, signed webhooks, setup steps; OAuth sign-in planned |
| ✅ | Private repository authentication | SSH deploy keys or access tokens |
| ✅ | Deploy from branch, tag or commit | tag patterns like v* |
| ✅ | Git push-triggered deployments | GitHub, GitLab, Bitbucket, generic |
| ✅ | Automatic application builds | detected per runtime, sandboxed as the account user |
| ✅ | Dockerfile and buildpack deployments | plus registry images |
| ✅ | Docker Compose project management | policy-checked stacks per account |
| ✅ | Build logs and deployment history |  |
| ✅ | Zero-downtime deployment strategies | atomic release switch; blue-green for apps and containers |
| ✅ | Rollback to previous release |  |
| ✅ | Preview deployments | per pull request; forks never built |
| ✅ | Deployment webhooks and API | signed outgoing events, deploy API, MCP |
| ✅ | Build queues and concurrency controls |  |

## Docker and orchestration

| | Feature | Notes |
|---|---|---|
| ✅ | Docker Engine integration |  |
| ✅ | Container creation, start, stop and restart |  |
| ✅ | Image registry management | registry logins, images, cleanup |
| ✅ | Docker Compose stack deployment |  |
| 🟡 | Container logs and shell | logs and one-off commands; interactive shell planned with the web terminal |
| ✅ | Volumes and persistent storage |  |
| ✅ | Container networks and port mapping | ports on 127.0.0.1, published through websites |
| ✅ | Resource quotas per container | plus the account's slice |
| ✅ | Container health checks |  |
| ✅ | Private registry credentials |  |
| ✅ | Image update notifications | daily check, alerts |
| ✅ | Optional Kubernetes integration | k3s single node for admins |

## DNS and domain management

| | Feature | Notes |
|---|---|---|
| 🔜 2 | DNS zone editor |  |
| 🔜 2 | A, AAAA, CNAME, MX, TXT, NS, SRV and CAA records |  |
| 🔜 2 | Domain registration integration |  |
| 🔜 2 | Custom nameserver management |  |
| 🔜 2 | Primary and secondary DNS support |  |
| 🔜 2 | DNSSEC support |  |
| 🔜 2 | Cloudflare and other DNS provider integrations |  |
| 🔜 2 | DNS propagation checks |  |
| 🔜 2 | Reverse DNS guidance or provider integration |  |
| 🔜 2 | Domain expiration reminders |  |
| 🔜 2 | DNS templates |  |
| 🔜 2 | DNS API automation |  |

## SSL and security

| | Feature | Notes |
|---|---|---|
| ✅ | Let's Encrypt certificate issuance and renewal |  |
| 🔜 2 | Wildcard certificates with DNS-01 validation |  |
| ✅ | Custom SSL certificate upload |  |
| ✅ | Certificate expiration monitoring | alerts when renewal fails |
| ✅ | UFW, nftables and firewalld integration | UFW |
| ✅ | Fail2ban or CrowdSec integration | fail2ban |
| ✅ | Two-factor authentication and passkeys | TOTP + recovery codes; passkeys planned |
| ✅ | Role-based access control | admin / reseller / account owner, each on its own port |
| ✅ | SSH hardening | key-only, jailed SFTP, root-owned keys |
| ✅ | Security audit logs | agent + MCP audit logs |
| 🔜 2 | Malware scanning and quarantine |  |
| 🔜 2 | WAF integration such as ModSecurity |  |
| ✅ | Rate limiting and brute-force protection | fail2ban jails |
| 🔜 2 | Secret encryption and rotation |  |
| ✅ | Security notifications | Telegram, WhatsApp and email |

## Email hosting

| | Feature | Notes |
|---|---|---|
| 🔜 3 | Email domain management |  |
| 🔜 3 | Postfix and Dovecot integration |  |
| 🖥️ | Mailboxes, aliases and forwarding |  |
| 🖥️ | SMTP submission, IMAP and POP3 | ports & TLS plan in docs/PORTS.md |
| 🖥️ | Webmail such as Roundcube or SnappyMail |  |
| 🖥️ | SPF, DKIM and DMARC configuration | deliverability checker |
| 🔜 3 | Spam filtering with Rspamd |  |
| 🔜 3 | Antivirus integration |  |
| 🔜 3 | Mail queue and delivery logs |  |
| 🖥️ | Mailbox quotas and usage |  |
| 🔜 3 | Email backups and restores |  |
| 🖥️ | SMTP relay configuration |  |
| 🔜 3 | TLS certificate management for mail |  |
| 🖥️ | Email deliverability diagnostics |  |

## Databases and caching

| | Feature | Notes |
|---|---|---|
| ✅ | MySQL and MariaDB | MariaDB |
| 🔜 2 | PostgreSQL |  |
| 🔜 2 | Redis and Valkey |  |
| 🔜 2 | MongoDB optional |  |
| ✅ | Database creation and deletion |  |
| ✅ | Database users and grants | one user per DB |
| ✅ | phpMyAdmin and Adminer integration | phpMyAdmin stable / 6.0 preview |
| 🔜 2 | Database connection diagnostics |  |
| 🔜 2 | Database imports and exports |  |
| 🔜 2 | Automated database backups |  |
| 🔜 2 | Slow-query and performance monitoring |  |
| 🔜 2 | Replication and high availability configuration |  |

## File and storage management

| | Feature | Notes |
|---|---|---|
| 🔜 2 | Browser-based file manager |  |
| 🔜 2 | Upload, download, copy, move and rename |  |
| 🔜 2 | ZIP and TAR extraction/compression |  |
| ✅ | File permission and ownership editor | fix-permissions; per-file editor planned |
| 🔜 2 | Code/text editor |  |
| ✅ | SFTP account management | per account; per-website SFTP users planned |
| 🟡 | Disk quota enforcement | set per account; needs a quota-enabled filesystem |
| 🔜 2 | S3-compatible object storage integration |  |
| 🟡 | Storage usage breakdown | disk per account; per-folder breakdown planned |
| 🔜 2 | File search |  |
| 🔜 2 | Snapshot and versioning integrations |  |
| 🔜 2 | Secure file sharing |  |

## Backups and disaster recovery

| | Feature | Notes |
|---|---|---|
| 🔜 2 | Full server backup orchestration |  |
| 🔜 2 | Per-website backups |  |
| 🔜 2 | Database and email backups |  |
| 🔜 2 | Incremental and differential backup support |  |
| 🔜 2 | Scheduled backups and retention policies |  |
| 🔜 2 | Local and remote backup targets |  |
| 🔜 2 | S3, Backblaze B2 and other compatible storage |  |
| 🔜 2 | Encrypted backups |  |
| 🔜 2 | One-click restoration |  |
| 🔜 2 | Backup integrity verification |  |
| 🔜 2 | Off-site disaster recovery |  |
| 🔜 2 | Recovery logs and notifications |  |

## Reseller and shared hosting

| | Feature | Notes |
|---|---|---|
| ✅ | Administrator, reseller, customer and developer roles | admin, reseller, account owner; developer role planned |
| 🟡 | Hosting packages and resource limits | limits per account; named packages next |
| ✅ | Customer account provisioning |  |
| 🔜 4 | Domain and website quotas |  |
| 🟡 | Bandwidth and disk quotas | disk quota; bandwidth measured, not capped yet |
| ✅ | Customer isolation | Linux users, chroot SFTP, open_basedir, systemd sandbox |
| 🟡 | Subscription and billing integration | invoices, overdue suspension; recurring billing next |
| ✅ | Invoice and payment gateway integration | invoices in naira or any currency, Bachs gateway |
| ✅ | Paystack and Flutterwave integration | signed webhooks, amount verified with the provider |
| 🔜 4 | WHMCS/Blesta or custom billing API |  |
| ✅ | Suspension and reactivation |  |
| 🔜 4 | White-label branding |  |
| ✅ | Usage reporting | CPU, memory, disk, bandwidth per account |

## Advanced infrastructure and automation

| | Feature | Notes |
|---|---|---|
| 🔜 6 | Multiple server nodes and server grouping |  |
| 🟡 | Remote agents with secure communication | local socket today; mTLS between servers planned |
| 🔜 6 | Infrastructure templates |  |
| ✅ | API-first administration | agent JSON API + MCP |
| 🔜 6 | CLI for automation |  |
| 🔜 6 | Webhooks and event subscriptions |  |
| 🔜 6 | Infrastructure as code integration |  |
| 🔜 6 | Load balancer configuration |  |
| 🔜 6 | Redis and database service clusters |  |
| ✅ | Monitoring and alert routing | uptime + app health to Telegram, WhatsApp, email |
| 🔜 6 | Scheduled maintenance workflows |  |
| 🔜 6 | Server inventory and asset tracking |  |
| ✅ | Audit trails and configuration history | audit trail, config drift detection and restore |

## UI/UX and administration

| | Feature | Notes |
|---|---|---|
| ✅ | Responsive desktop, tablet and mobile interface |  |
| ✅ | Light and dark themes | plus a data saver mode for slow connections |
| 🟡 | Custom branding and theme colors | 5 themes, 3 layouts; admin branding planned |
| 🔜 1 | Global search and command palette |  |
| ✅ | Role-aware navigation |  |
| 🔜 1 | Notification center |  |
| ✅ | Interactive setup wizards | guided installer wizard |
| 🔜 1 | Contextual documentation |  |
| 🔜 1 | Keyboard shortcuts |  |
| ✅ | Accessible forms and dashboards |  |
| ✅ | Real-time activity feed | agent + panel audit log |
| 🔜 1 | Multi-language readiness |  |

## Also built (beyond the checklist)

- ✅ One-line installer: `curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash` (see [INSTALLER.md](INSTALLER.md))
- ✅ Health assistant: diagnoses a website from its services and logs and says what to do
- ✅ Security baseline scanner with a score
- ✅ Configuration drift detection with diff and one-click restore
- ✅ Uptime alerts by WhatsApp, Telegram and email
- ✅ Naira-first billing with Bachs, Paystack and Flutterwave
- ✅ Data saver mode for slow or metered connections
- ✅ Update Ike itself from the panel, with checksums
- ✅ Proxy runtime: put a domain and HTTPS in front of any local port (containers, Kubernetes services)

## Also planned

- 🖥️ Browser-based terminal (runs as the account user, audited, can be disabled per plan)
- 🖥️ One-click app catalog beyond WordPress: Laravel, Joomla, Drupal, PrestaShop, Nextcloud, Ghost, Django
- ✅ Website status pages: maintenance (with allowed IPs) and suspended, admin-branded
- ✅ MCP server so AI agents can manage the server safely (read-only by default)
