# Feature tracker

Ike's full scope, grouped by area. Pick anything marked *planned* and open a PR — see [CONTRIBUTING.md](../CONTRIBUTING.md).

**33 done · 20 in the UI · 123 planned** (of 176)

| Mark | Meaning |
|---|---|
| ✅ | Done in the agent/installer |
| 🖥️ | In the web panel (demo data until the control-plane API ships) |
| 🔜 | Planned — phase number from [ROADMAP.md](ROADMAP.md) |


## Server and operating system

| | Feature | Notes |
|---|---|---|
| ✅ | Linux server installation and initial configuration | installer |
| 🖥️ | CPU, RAM, disk, swap and network monitoring | CPU/RAM/disk live; swap & network planned |
| 🖥️ | Service management with systemd | status done; start/stop planned |
| 🔜 2 | Process manager and resource limits |  |
| 🔜 2 | System package installation and updates |  |
| ✅ | Linux user and group management | accounts = Linux users |
| ✅ | SSH keys, SSH terminal and SFTP | SSH/SFTP + keys; web terminal in UI |
| ✅ | Cron jobs and scheduled commands |  |
| ✅ | Firewall, ports and network interfaces | ports & IP blocks; interfaces planned |
| 🖥️ | Server reboot, shutdown and maintenance mode | website maintenance mode done |
| 🔜 2 | Multi-server administration |  |
| 🔜 2 | OS security updates and vulnerability alerts |  |

## Websites and web servers

| | Feature | Notes |
|---|---|---|
| ✅ | Apache, Nginx, OpenLiteSpeed and Caddy integration | Nginx, Apache, Nginx+Apache; OLS/Caddy planned |
| ✅ | Virtual hosts and website creation |  |
| 🔜 2 | Domain, subdomain, wildcard and alias support |  |
| ✅ | Reverse proxy configuration | Node/Python apps, Nginx→Apache |
| 🔜 2 | HTTP/2 and HTTP/3 support where available |  |
| ✅ | Per-website access/error logs |  |
| 🔜 2 | URL redirects and rewrite rules |  |
| ✅ | Custom error pages and password protection | error pages; password protection planned |
| ✅ | PHP-FPM pools per website |  |
| 🔜 2 | Website staging and cloning |  |
| ✅ | One-click WordPress installation | WP-CLI |
| 🔜 2 | Website migration and import |  |

## PHP and runtime environments

| | Feature | Notes |
|---|---|---|
| ✅ | Multiple PHP versions installed side by side | 5.6 → 8.5 |
| ✅ | Per-site PHP version selection |  |
| 🔜 2 | PHP-FPM pool configuration |  |
| 🔜 2 | PHP extensions management |  |
| 🔜 2 | Composer integration |  |
| ✅ | Node.js and npm/pnpm/yarn management | Node versions; package managers via shell |
| ✅ | Python virtual environments and pip | venv per app |
| 🔜 2 | Java runtime and build tooling |  |
| 🔜 2 | Go runtime support |  |
| 🖥️ | Environment variable and secrets management | .env per site; UI editor planned |
| ✅ | Process management for long-running applications | systemd unit per app |
| 🔜 2 | Runtime-specific health checks |  |

## Git, CI/CD and deployment

| | Feature | Notes |
|---|---|---|
| 🔜 5 | GitHub, GitLab and Bitbucket integration |  |
| 🔜 5 | Private repository authentication |  |
| 🔜 5 | Deploy from branch, tag or commit |  |
| 🔜 5 | Git push-triggered deployments |  |
| 🔜 5 | Automatic application builds |  |
| 🔜 5 | Dockerfile and buildpack deployments |  |
| 🔜 5 | Docker Compose project management |  |
| 🔜 5 | Build logs and deployment history |  |
| 🔜 5 | Zero-downtime deployment strategies |  |
| 🔜 5 | Rollback to previous release |  |
| 🔜 5 | Preview deployments |  |
| 🔜 5 | Deployment webhooks and API |  |
| 🔜 5 | Build queues and concurrency controls |  |

## Docker and orchestration

| | Feature | Notes |
|---|---|---|
| 🔜 5 | Docker Engine integration |  |
| 🔜 5 | Container creation, start, stop and restart |  |
| 🔜 5 | Image registry management |  |
| 🔜 5 | Docker Compose stack deployment |  |
| 🔜 5 | Container logs and shell |  |
| 🔜 5 | Volumes and persistent storage |  |
| 🔜 5 | Container networks and port mapping |  |
| 🔜 5 | Resource quotas per container |  |
| 🔜 5 | Container health checks |  |
| 🔜 5 | Private registry credentials |  |
| 🔜 5 | Image update notifications |  |
| 🔜 5 | Optional Kubernetes integration |  |

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
| 🔜 1 | Let's Encrypt certificate issuance and renewal |  |
| 🔜 2 | Wildcard certificates with DNS-01 validation |  |
| 🔜 2 | Custom SSL certificate upload |  |
| 🔜 2 | Certificate expiration monitoring |  |
| ✅ | UFW, nftables and firewalld integration | UFW |
| ✅ | Fail2ban or CrowdSec integration | fail2ban |
| 🔜 2 | Two-factor authentication and passkeys |  |
| 🖥️ | Role-based access control | Admin / Reseller / Account roles in UI |
| ✅ | SSH hardening | key-only, jailed SFTP, root-owned keys |
| ✅ | Security audit logs | agent + MCP audit logs |
| 🔜 2 | Malware scanning and quarantine |  |
| 🔜 2 | WAF integration such as ModSecurity |  |
| ✅ | Rate limiting and brute-force protection | fail2ban jails |
| 🔜 2 | Secret encryption and rotation |  |
| 🔜 2 | Security notifications |  |

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
| 🔜 2 | Disk quota enforcement |  |
| 🔜 2 | S3-compatible object storage integration |  |
| 🔜 2 | Storage usage breakdown |  |
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
| 🖥️ | Administrator, reseller, customer and developer roles |  |
| 🔜 4 | Hosting packages and resource limits |  |
| ✅ | Customer account provisioning |  |
| 🔜 4 | Domain and website quotas |  |
| 🔜 4 | Bandwidth and disk quotas |  |
| ✅ | Customer isolation | Linux users, chroot SFTP, open_basedir, systemd sandbox |
| 🔜 4 | Subscription and billing integration |  |
| 🔜 4 | Invoice and payment gateway integration |  |
| 🔜 4 | Paystack and Flutterwave integration |  |
| 🔜 4 | WHMCS/Blesta or custom billing API |  |
| ✅ | Suspension and reactivation |  |
| 🔜 4 | White-label branding |  |
| 🔜 4 | Usage reporting |  |

## Advanced infrastructure and automation

| | Feature | Notes |
|---|---|---|
| 🔜 6 | Multiple server nodes and server grouping |  |
| 🖥️ | Remote agents with secure communication | local socket today; mTLS planned |
| 🔜 6 | Infrastructure templates |  |
| ✅ | API-first administration | agent JSON API + MCP |
| 🔜 6 | CLI for automation |  |
| 🔜 6 | Webhooks and event subscriptions |  |
| 🔜 6 | Infrastructure as code integration |  |
| 🔜 6 | Load balancer configuration |  |
| 🔜 6 | Redis and database service clusters |  |
| 🔜 6 | Monitoring and alert routing |  |
| 🔜 6 | Scheduled maintenance workflows |  |
| 🔜 6 | Server inventory and asset tracking |  |
| ✅ | Audit trails and configuration history | audit trail |

## UI/UX and administration

| | Feature | Notes |
|---|---|---|
| 🖥️ | Responsive desktop, tablet and mobile interface |  |
| 🖥️ | Light and dark themes |  |
| 🖥️ | Custom branding and theme colors | 5 themes; admin branding planned |
| 🔜 1 | Global search and command palette |  |
| 🖥️ | Role-aware navigation |  |
| 🔜 1 | Notification center |  |
| 🔜 1 | Interactive setup wizards |  |
| 🔜 1 | Contextual documentation |  |
| 🔜 1 | Keyboard shortcuts |  |
| 🖥️ | Accessible forms and dashboards |  |
| 🖥️ | Real-time activity feed |  |
| 🔜 1 | Multi-language readiness |  |

## Also planned

- 🖥️ Browser-based terminal (runs as the account user, audited, can be disabled per plan)
- 🖥️ One-click app catalog beyond WordPress: Laravel, Joomla, Drupal, PrestaShop, Nextcloud, Ghost, Django
- ✅ Website status pages: maintenance (with allowed IPs) and suspended, admin-branded
- ✅ MCP server so AI agents can manage the server safely (read-only by default)
