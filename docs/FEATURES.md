# Feature tracker

Ike's full scope, grouped by area. Pick anything not done and open a PR; see [CONTRIBUTING.md](../CONTRIBUTING.md).

**v0.6.0: 177 done · 0 partly done · 0 planned** (of 177)

| Mark | Meaning |
|---|---|
| ✅ | Done: works end to end on a server |
| 🟡 | Partly done (the notes say which part) |
| 🔜 | Planned |

## Server and operating system

| | Feature | Notes |
|---|---|---|
| ✅ | Linux server installation and initial configuration | one-line installer with a guided wizard |
| ✅ | CPU, RAM, disk, swap and network monitoring | CPU, RAM, disk, swap and network live and as 24 h server graphs (5-minute points, network per interface), plus per-account graphs and bandwidth |
| ✅ | Service management with systemd | status, start, stop and restart from the panel, CLI and MCP |
| ✅ | Process manager and resource limits | systemd unit per app; CPU, memory and process limits per account |
| ✅ | System package installation and updates | apt updates, security-only option, job log |
| ✅ | Linux user and group management | accounts are Linux users |
| ✅ | SSH keys, SSH terminal and SFTP | per-key access: shell, SFTP or read-only SFTP, allowed addresses, expiry; web terminal |
| ✅ | Cron jobs and scheduled commands |  |
| ✅ | Firewall, ports and network interfaces | ports and IP blocks; interfaces in the inventory |
| ✅ | Server reboot, shutdown and maintenance mode | scheduled maintenance windows; website maintenance mode |
| ✅ | Multi-server administration | join servers with a one-time token (mutual TLS), switch server in the panel, CLI --server, fan-out |
| ✅ | OS security updates and vulnerability alerts | unattended security updates, security scan, alerts |

## Websites and web servers

| | Feature | Notes |
|---|---|---|
| ✅ | Apache, Nginx, OpenLiteSpeed and Caddy integration | Nginx, Apache, Nginx in front of Apache, Caddy or OpenLiteSpeed (PHP through Ike's PHP-FPM pools; ModSecurity needs Nginx or Apache) |
| ✅ | Virtual hosts and website creation |  |
| ✅ | Domain, subdomain, wildcard and alias support | aliases, *.wildcards, www handling, redirect to the main domain |
| ✅ | Reverse proxy configuration | Node.js and Python apps, Nginx in front of Apache, any local port |
| ✅ | HTTP/2 and HTTP/3 support where available | HTTP/3 with Nginx 1.25+ built with QUIC |
| ✅ | Per-website access/error logs |  |
| ✅ | URL redirects and rewrite rules | exact, prefix and pattern rules; 301, 302, 307, 308 |
| ✅ | Custom error pages and password protection | error pages per website; password-protected folders with their own users, realms and addresses that skip the password (not on OpenLiteSpeed) |
| ✅ | PHP-FPM pools per website |  |
| ✅ | Website staging and cloning | staging copy with its database, push to live |
| ✅ | One-click WordPress installation | WP-CLI |
| ✅ | Website migration and import | cPanel full backups: sites, files, databases, mailboxes with passwords, forwarders, cron, DNS |

## PHP and runtime environments

| | Feature | Notes |
|---|---|---|
| ✅ | Multiple PHP versions installed side by side | 5.6 to 8.5 |
| ✅ | Per-site PHP version selection |  |
| ✅ | PHP-FPM pool configuration | process manager, workers, memory, uploads, time limits, timezone |
| ✅ | PHP extensions management | 40 extensions per PHP version |
| ✅ | Composer integration | install, update, require; runs on Git deploys |
| ✅ | Node.js and npm/pnpm/yarn management | Node versions; package managers on deploy |
| ✅ | Python virtual environments and pip | venv per app |
| ✅ | Java runtime and build tooling | OpenJDK; Maven and Gradle on deploy |
| ✅ | Go runtime support | go.dev releases, checksum-verified |
| ✅ | Dart runtime support | Dart SDK from Google's archive |
| ✅ | Environment variable and secrets management | editor per website; apps restart on save |
| ✅ | Process management for long-running applications | systemd unit per app |
| ✅ | Runtime-specific health checks | HTTP health check per app, restart or alert |

## Git, CI/CD and deployment

| | Feature | Notes |
|---|---|---|
| ✅ | GitHub, GitLab and Bitbucket integration | deploy keys, tokens, signed webhooks; OAuth sign-in planned |
| ✅ | Private repository authentication | SSH deploy keys or access tokens |
| ✅ | Deploy from branch, tag or commit | tag patterns like v* |
| ✅ | Git push-triggered deployments | GitHub, GitLab, Bitbucket, Gitea, generic |
| ✅ | Automatic application builds | detected per runtime, sandboxed as the account user |
| ✅ | Dockerfile and buildpack deployments | plus registry images |
| ✅ | Docker Compose project management | policy-checked stacks per account |
| ✅ | Build logs and deployment history |  |
| ✅ | Zero-downtime deployment strategies | atomic release switch; blue-green for apps and containers |
| ✅ | Rollback to previous release |  |
| ✅ | Preview deployments | per pull request; forks never built |
| ✅ | Deployment webhooks and API | signed outgoing events, deploy API, CLI, MCP |
| ✅ | Build queues and concurrency controls |  |

## Docker and orchestration

| | Feature | Notes |
|---|---|---|
| ✅ | Docker Engine integration |  |
| ✅ | Container creation, start, stop and restart |  |
| ✅ | Image registry management | registry logins, images, cleanup |
| ✅ | Docker Compose stack deployment |  |
| ✅ | Container logs and shell | logs, commands and an interactive shell in the browser |
| ✅ | Volumes and persistent storage |  |
| ✅ | Container networks and port mapping | ports on 127.0.0.1, published through websites |
| ✅ | Resource quotas per container | inside the account's own limits |
| ✅ | Container health checks |  |
| ✅ | Private registry credentials |  |
| ✅ | Image update notifications | daily check, alerts |
| ✅ | Optional Kubernetes integration | k3s single node for admins |

## DNS and domain management

| | Feature | Notes |
|---|---|---|
| ✅ | DNS zone editor | zones per account, import and export of BIND zone files |
| ✅ | A, AAAA, CNAME, MX, TXT, NS, SRV and CAA records | also PTR, TLSA, SSHFP, DS and NAPTR, checked before saving |
| ✅ | Domain registration integration | Namecheap, with availability search, registration, renewal and glue records |
| ✅ | Custom nameserver management | your own ns1/ns2 with glue at the registrar |
| ✅ | Primary and secondary DNS support | PowerDNS, zone transfers and NOTIFY |
| ✅ | DNSSEC support | signing on this server, DS records to give the registrar |
| ✅ | Cloudflare and other DNS provider integrations | Cloudflare and DigitalOcean |
| ✅ | DNS propagation checks | four public resolvers, plus the registrar's name servers, explained in plain words |
| ✅ | Reverse DNS guidance or provider integration | checks PTR and forward match, says what to ask the provider |
| ✅ | Domain expiration reminders | expiry from RDAP, reminders before it lapses |
| ✅ | DNS templates | website, website and email, Google Workspace, Microsoft 365, Zoho |
| ✅ | DNS API automation | API, CLI, MCP, and DNS-01 for wildcard certificates |

## SSL and security

| | Feature | Notes |
|---|---|---|
| ✅ | Let's Encrypt certificate issuance and renewal |  |
| ✅ | Wildcard certificates with DNS-01 validation | for zones in Ike, here or at Cloudflare or DigitalOcean |
| ✅ | Custom SSL certificate upload |  |
| ✅ | Certificate expiration monitoring | alerts when renewal fails |
| ✅ | UFW, nftables and firewalld integration | chosen at install; nftables keeps its own inet ike table; ports can be limited to one address; fail2ban bans through the same one |
| ✅ | Fail2ban or CrowdSec integration | fail2ban |
| ✅ | Two-factor authentication and passkeys | TOTP with recovery codes, and passkeys (WebAuthn: ES256, EdDSA, RS256) that sign in without a password or code |
| ✅ | Role-based access control | admin, reseller, account owner on their own ports; scoped API tokens; per-key SSH access |
| ✅ | SSH hardening | key-only, jailed SFTP, root-owned keys, per-key restrictions |
| ✅ | Security audit logs | agent, panel and MCP audit logs |
| ✅ | Malware scanning and quarantine | ClamAV and PHP backdoor checks, scheduled scans, quarantine and restore |
| ✅ | WAF integration such as ModSecurity | ModSecurity with the OWASP rules, per site: off, detect, block, app profiles |
| ✅ | Rate limiting and brute-force protection | fail2ban jails, panel sign-in limits |
| ✅ | Secret encryption and rotation | stored passwords and API keys encrypted (AES-GCM), key rotation |
| ✅ | Security notifications | Telegram, WhatsApp and email |

## Email hosting

| | Feature | Notes |
|---|---|---|
| ✅ | Email domain management |  |
| ✅ | Postfix and Dovecot integration |  |
| ✅ | Mailboxes, aliases and forwarding | plus catch-all and auto-replies |
| ✅ | SMTP submission, IMAP and POP3 | TLS on 465, 587, 993 and 995 |
| ✅ | Webmail such as Roundcube or SnappyMail | Roundcube |
| ✅ | SPF, DKIM and DMARC configuration | records added to the zone, DKIM rotation |
| ✅ | Spam filtering with Rspamd | thresholds and greylisting |
| ✅ | Antivirus integration | ClamAV |
| ✅ | Mail queue and delivery logs | retry, hold, release, delete; searchable delivery log |
| ✅ | Mailbox quotas and usage |  |
| ✅ | Email backups and restores | mailbox export and import; email in backup plans |
| ✅ | SMTP relay configuration | Brevo, Mailgun, SES, SendGrid, Postmark, ZeptoMail or any host |
| ✅ | TLS certificate management for mail | Let's Encrypt for mail.<domain> |
| ✅ | Email deliverability diagnostics | MX, SPF, DKIM, DMARC, reverse DNS, port 25, blocklists, certificate |

## Databases and caching

| | Feature | Notes |
|---|---|---|
| ✅ | MySQL and MariaDB | MariaDB |
| ✅ | PostgreSQL |  |
| ✅ | Redis and Valkey | a private instance per account on a unix socket |
| ✅ | MongoDB optional |  |
| ✅ | Database creation and deletion |  |
| ✅ | Database users and grants | owner plus read or write users |
| ✅ | phpMyAdmin and Adminer integration | phpMyAdmin |
| ✅ | Database connection diagnostics | checks each step and says how to fix it |
| ✅ | Database imports and exports |  |
| ✅ | Automated database backups | hourly or daily backup plans |
| ✅ | Slow-query and performance monitoring | slow log, running queries, stop a query |
| ✅ | Replication and high availability configuration | MariaDB and Redis replicas, clusters across servers with failover |

## File and storage management

| | Feature | Notes |
|---|---|---|
| ✅ | Browser-based file manager |  |
| ✅ | Upload, download, copy, move and rename |  |
| ✅ | ZIP and TAR extraction/compression |  |
| ✅ | File permission and ownership editor | chmod in the file manager, fix-permissions per site |
| ✅ | Code/text editor |  |
| ✅ | SFTP account management | per account and per key, plus per-website SFTP users jailed to one website (read-only option, expiry date) |
| ✅ | Disk quota enforcement | Linux quotas set up by the installer (ext4 now, XFS after a reboot) |
| ✅ | S3-compatible object storage integration | S3, Backblaze B2, Wasabi, R2, MinIO, plus SFTP |
| ✅ | Storage usage breakdown | per account and per folder |
| ✅ | File search | by name and by text inside files |
| ✅ | Snapshot and versioning integrations | file snapshots on the server, restore a file or folder |
| ✅ | Secure file sharing | expiring links with password and download limit |

## Backups and disaster recovery

| | Feature | Notes |
|---|---|---|
| ✅ | Full server backup orchestration | restic |
| ✅ | Per-website backups |  |
| ✅ | Database and email backups |  |
| ✅ | Incremental and differential backup support | deduplicated; every run after the first stores only changes |
| ✅ | Scheduled backups and retention policies | hourly, daily, weekly; keep last, hourly, daily, weekly, monthly |
| ✅ | Local and remote backup targets |  |
| ✅ | S3, Backblaze B2 and other compatible storage |  |
| ✅ | Encrypted backups | with a recovery key shown once |
| ✅ | One-click restoration | files, folders or databases, beside or over the current ones |
| ✅ | Backup integrity verification | scheduled checks read data back |
| ✅ | Off-site disaster recovery | rebuild a new server from a backup and its recovery key |
| ✅ | Recovery logs and notifications |  |

## Reseller and shared hosting

| | Feature | Notes |
|---|---|---|
| ✅ | Administrator, reseller, customer and developer roles | admin, reseller, account owner and developer (invited to chosen accounts and areas, never billing) |
| ✅ | Hosting packages and resource limits | named packages, enforced when accounts, sites, domains and mail are added |
| ✅ | Customer account provisioning | panel, API, WHMCS and Blesta |
| ✅ | Domain and website quotas | per package, and totals per reseller |
| ✅ | Bandwidth and disk quotas | monthly bandwidth caps suspend and lift themselves; disk quotas enforced |
| ✅ | Customer isolation | Linux users, chroot SFTP, open_basedir, systemd sandbox |
| ✅ | Subscription and billing integration | recurring subscriptions invoice 7 days ahead |
| ✅ | Invoice and payment gateway integration | invoices in naira or any currency |
| ✅ | Paystack and Flutterwave integration | signed webhooks, amount checked with the provider; Bachs too |
| ✅ | WHMCS/Blesta or custom billing API | WHMCS and Blesta modules with single sign-on; provisioning API |
| ✅ | Suspension and reactivation |  |
| ✅ | White-label branding | product name, logo, colour and support links per host name or reseller |
| ✅ | Usage reporting | CPU, memory, disk, bandwidth per account |

## Advanced infrastructure and automation

| | Feature | Notes |
|---|---|---|
| ✅ | Multiple server nodes and server grouping |  |
| ✅ | Remote agents with secure communication | mutual TLS with pinned certificates |
| ✅ | Infrastructure templates | spec templates with variables |
| ✅ | API-first administration | 379 routes, scoped tokens, documented in API.md |
| ✅ | CLI for automation | the ike command for Linux, macOS and Windows |
| ✅ | Webhooks and event subscriptions | signed, with retries and a delivery log |
| ✅ | Infrastructure as code integration | declarative specs: plan, apply, export |
| ✅ | Load balancer configuration | a site can balance across servers |
| ✅ | Redis and database service clusters |  |
| ✅ | Monitoring and alert routing | uptime and app health to Telegram, WhatsApp and email |
| ✅ | Scheduled maintenance workflows | maintenance page, updates and reboot in a window |
| ✅ | Server inventory and asset tracking | hardware, disks, network, owner, location, renewal, cost |
| ✅ | Audit trails and configuration history | audit trail, config drift detection and restore |

## UI/UX and administration

| | Feature | Notes |
|---|---|---|
| ✅ | Responsive desktop, tablet and mobile interface |  |
| ✅ | Light and dark themes | plus a data saver mode for slow connections |
| ✅ | Custom branding and theme colors | 5 themes, 3 layouts, white-label colour and logo |
| ✅ | Global search and command palette | Ctrl K |
| ✅ | Role-aware navigation |  |
| ✅ | Notification center |  |
| ✅ | Interactive setup wizards | installer wizard |
| ✅ | Contextual documentation | help drawer on every screen |
| ✅ | Keyboard shortcuts | ? lists them |
| ✅ | Accessible forms and dashboards |  |
| ✅ | Real-time activity feed | agent and panel audit log |
| ✅ | Multi-language readiness | English, French, Nigerian Pidgin, Yoruba, Igbo, Hausa |

## Also built (beyond the checklist)

- ✅ One-line installer: `curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash` (see [INSTALLER.md](INSTALLER.md))
- ✅ Health assistant: diagnoses a website from its services and logs and says what to do
- ✅ Security baseline scanner with a score
- ✅ Configuration drift detection with diff and one-click restore
- ✅ Naira-first billing with Paystack and Flutterwave
- ✅ Data saver mode for slow or metered connections
- ✅ Update Ike itself from the panel, with checksums
- ✅ Proxy runtime: a domain and HTTPS in front of any local port
- ✅ MCP server for AI agents, read-only by default and scoped by API token (see [MCP.md](MCP.md))
- ✅ The same access model everywhere: scoped API tokens for the API, CLI and MCP; per-key SSH access (see [API.md](API.md#api-tokens))

## Planned next

- 🔜 Password-protected folders and websites
- 🔜 OpenLiteSpeed and Caddy
- 🔜 Passkeys
- 🔜 A developer role
- 🔜 nftables and firewalld
- 🔜 Swap and network graphs
- 🔜 Per-website SFTP users
- 🔜 One-click apps beyond WordPress: Laravel, Joomla, Drupal, Nextcloud, Ghost
- 🔜 MCP over Streamable HTTP from the panel
- 🔜 A Let's Encrypt certificate for the panel's own address from the settings screen (today: a renewal hook, see INTEGRATIONS.md)
