#!/usr/bin/env python3
"""The feature list, in one place. Writes docs/FEATURES.md and docs/CHECKLIST.md.

    scripts/features.py

Mark each item done (x), partly done (~) or not yet ( ), with a short note.
"""
import os

VERSION = "0.6.0"
ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

AREAS = [
("Server and operating system", [
 ("x", "Linux server installation and initial configuration", "one-line installer with a guided wizard"),
 ("x", "CPU, RAM, disk, swap and network monitoring", "CPU, RAM, disk, swap and network live and as 24 h server graphs (5-minute points, network per interface), plus per-account graphs and bandwidth"),
 ("x", "Service management with systemd", "status, start, stop and restart from the panel, CLI and MCP"),
 ("x", "Process manager and resource limits", "systemd unit per app; CPU, memory and process limits per account"),
 ("x", "System package installation and updates", "apt updates, security-only option, job log"),
 ("x", "Linux user and group management", "accounts are Linux users"),
 ("x", "SSH keys, SSH terminal and SFTP", "per-key access: shell, SFTP or read-only SFTP, allowed addresses, expiry; web terminal"),
 ("x", "Cron jobs and scheduled commands", ""),
 ("x", "Firewall, ports and network interfaces", "ports and IP blocks; interfaces in the inventory"),
 ("x", "Server reboot, shutdown and maintenance mode", "scheduled maintenance windows; website maintenance mode"),
 ("x", "Multi-server administration", "join servers with a one-time token (mutual TLS), switch server in the panel, CLI --server, fan-out"),
 ("x", "OS security updates and vulnerability alerts", "unattended security updates, security scan, alerts"),
]),
("Websites and web servers", [
 ("x", "Apache, Nginx, OpenLiteSpeed and Caddy integration", "Nginx, Apache, Nginx in front of Apache, Caddy or OpenLiteSpeed (PHP through Ike's PHP-FPM pools; ModSecurity needs Nginx or Apache)"),
 ("x", "Virtual hosts and website creation", ""),
 ("x", "Domain, subdomain, wildcard and alias support", "aliases, *.wildcards, www handling, redirect to the main domain"),
 ("x", "Reverse proxy configuration", "Node.js and Python apps, Nginx in front of Apache, any local port"),
 ("x", "HTTP/2 and HTTP/3 support where available", "HTTP/3 with Nginx 1.25+ built with QUIC"),
 ("x", "Per-website access/error logs", ""),
 ("x", "URL redirects and rewrite rules", "exact, prefix and pattern rules; 301, 302, 307, 308"),
 ("x", "Custom error pages and password protection", "error pages per website; password-protected folders with their own users, realms and addresses that skip the password (not on OpenLiteSpeed)"),
 ("x", "PHP-FPM pools per website", ""),
 ("x", "Website staging and cloning", "staging copy with its database, push to live"),
 ("x", "One-click WordPress installation", "WP-CLI"),
 ("x", "Website migration and import", "cPanel full backups: sites, files, databases, mailboxes with passwords, forwarders, cron, DNS"),
]),
("PHP and runtime environments", [
 ("x", "Multiple PHP versions installed side by side", "5.6 to 8.5"),
 ("x", "Per-site PHP version selection", ""),
 ("x", "PHP-FPM pool configuration", "process manager, workers, memory, uploads, time limits, timezone"),
 ("x", "PHP extensions management", "40 extensions per PHP version"),
 ("x", "Composer integration", "install, update, require; runs on Git deploys"),
 ("x", "Node.js and npm/pnpm/yarn management", "Node versions; package managers on deploy"),
 ("x", "Python virtual environments and pip", "venv per app"),
 ("x", "Java runtime and build tooling", "OpenJDK; Maven and Gradle on deploy"),
 ("x", "Go runtime support", "go.dev releases, checksum-verified"),
 ("x", "Dart runtime support", "Dart SDK from Google's archive"),
 ("x", "Environment variable and secrets management", "editor per website; apps restart on save"),
 ("x", "Process management for long-running applications", "systemd unit per app"),
 ("x", "Runtime-specific health checks", "HTTP health check per app, restart or alert"),
]),
("Git, CI/CD and deployment", [
 ("x", "GitHub, GitLab and Bitbucket integration", "deploy keys, tokens, signed webhooks; OAuth sign-in planned"),
 ("x", "Private repository authentication", "SSH deploy keys or access tokens"),
 ("x", "Deploy from branch, tag or commit", "tag patterns like v*"),
 ("x", "Git push-triggered deployments", "GitHub, GitLab, Bitbucket, Gitea, generic"),
 ("x", "Automatic application builds", "detected per runtime, sandboxed as the account user"),
 ("x", "Dockerfile and buildpack deployments", "plus registry images"),
 ("x", "Docker Compose project management", "policy-checked stacks per account"),
 ("x", "Build logs and deployment history", ""),
 ("x", "Zero-downtime deployment strategies", "atomic release switch; blue-green for apps and containers"),
 ("x", "Rollback to previous release", ""),
 ("x", "Preview deployments", "per pull request; forks never built"),
 ("x", "Deployment webhooks and API", "signed outgoing events, deploy API, CLI, MCP"),
 ("x", "Build queues and concurrency controls", ""),
]),
("Docker and orchestration", [
 ("x", "Docker Engine integration", ""),
 ("x", "Container creation, start, stop and restart", ""),
 ("x", "Image registry management", "registry logins, images, cleanup"),
 ("x", "Docker Compose stack deployment", ""),
 ("x", "Container logs and shell", "logs, commands and an interactive shell in the browser"),
 ("x", "Volumes and persistent storage", ""),
 ("x", "Container networks and port mapping", "ports on 127.0.0.1, published through websites"),
 ("x", "Resource quotas per container", "inside the account's own limits"),
 ("x", "Container health checks", ""),
 ("x", "Private registry credentials", ""),
 ("x", "Image update notifications", "daily check, alerts"),
 ("x", "Optional Kubernetes integration", "k3s single node for admins"),
]),
("DNS and domain management", [
 ("x", "DNS zone editor", "zones per account, import and export of BIND zone files"),
 ("x", "A, AAAA, CNAME, MX, TXT, NS, SRV and CAA records", "also PTR, TLSA, SSHFP, DS and NAPTR, checked before saving"),
 ("x", "Domain registration integration", "Namecheap, with availability search, registration, renewal and glue records"),
 ("x", "Custom nameserver management", "your own ns1/ns2 with glue at the registrar"),
 ("x", "Primary and secondary DNS support", "PowerDNS, zone transfers and NOTIFY"),
 ("x", "DNSSEC support", "signing on this server, DS records to give the registrar"),
 ("x", "Cloudflare and other DNS provider integrations", "Cloudflare and DigitalOcean"),
 ("x", "DNS propagation checks", "four public resolvers, plus the registrar's name servers, explained in plain words"),
 ("x", "Reverse DNS guidance or provider integration", "checks PTR and forward match, says what to ask the provider"),
 ("x", "Domain expiration reminders", "expiry from RDAP, reminders before it lapses"),
 ("x", "DNS templates", "website, website and email, Google Workspace, Microsoft 365, Zoho"),
 ("x", "DNS API automation", "API, CLI, MCP, and DNS-01 for wildcard certificates"),
]),
("SSL and security", [
 ("x", "Let's Encrypt certificate issuance and renewal", ""),
 ("x", "Wildcard certificates with DNS-01 validation", "for zones in Ike, here or at Cloudflare or DigitalOcean"),
 ("x", "Custom SSL certificate upload", ""),
 ("x", "Certificate expiration monitoring", "alerts when renewal fails"),
 ("x", "UFW, nftables and firewalld integration", "chosen at install; nftables keeps its own inet ike table; ports can be limited to one address; fail2ban bans through the same one"),
 ("x", "Fail2ban or CrowdSec integration", "fail2ban"),
 ("x", "Two-factor authentication and passkeys", "TOTP with recovery codes, and passkeys (WebAuthn: ES256, EdDSA, RS256) that sign in without a password or code"),
 ("x", "Role-based access control", "admin, reseller, account owner on their own ports; scoped API tokens; per-key SSH access"),
 ("x", "SSH hardening", "key-only, jailed SFTP, root-owned keys, per-key restrictions"),
 ("x", "Security audit logs", "agent, panel and MCP audit logs"),
 ("x", "Malware scanning and quarantine", "ClamAV and PHP backdoor checks, scheduled scans, quarantine and restore"),
 ("x", "WAF integration such as ModSecurity", "ModSecurity with the OWASP rules, per site: off, detect, block, app profiles"),
 ("x", "Rate limiting and brute-force protection", "fail2ban jails, panel sign-in limits"),
 ("x", "Secret encryption and rotation", "stored passwords and API keys encrypted (AES-GCM), key rotation"),
 ("x", "Security notifications", "Telegram, WhatsApp and email"),
]),
("Email hosting", [
 ("x", "Email domain management", ""),
 ("x", "Postfix and Dovecot integration", ""),
 ("x", "Mailboxes, aliases and forwarding", "plus catch-all and auto-replies"),
 ("x", "SMTP submission, IMAP and POP3", "TLS on 465, 587, 993 and 995"),
 ("x", "Webmail such as Roundcube or SnappyMail", "Roundcube"),
 ("x", "SPF, DKIM and DMARC configuration", "records added to the zone, DKIM rotation"),
 ("x", "Spam filtering with Rspamd", "thresholds and greylisting"),
 ("x", "Antivirus integration", "ClamAV"),
 ("x", "Mail queue and delivery logs", "retry, hold, release, delete; searchable delivery log"),
 ("x", "Mailbox quotas and usage", ""),
 ("x", "Email backups and restores", "mailbox export and import; email in backup plans"),
 ("x", "SMTP relay configuration", "Brevo, Mailgun, SES, SendGrid, Postmark, ZeptoMail or any host"),
 ("x", "TLS certificate management for mail", "Let's Encrypt for mail.<domain>"),
 ("x", "Email deliverability diagnostics", "MX, SPF, DKIM, DMARC, reverse DNS, port 25, blocklists, certificate"),
]),
("Databases and caching", [
 ("x", "MySQL and MariaDB", "MariaDB"),
 ("x", "PostgreSQL", ""),
 ("x", "Redis and Valkey", "a private instance per account on a unix socket"),
 ("x", "MongoDB optional", ""),
 ("x", "Database creation and deletion", ""),
 ("x", "Database users and grants", "owner plus read or write users"),
 ("x", "phpMyAdmin and Adminer integration", "phpMyAdmin"),
 ("x", "Database connection diagnostics", "checks each step and says how to fix it"),
 ("x", "Database imports and exports", ""),
 ("x", "Automated database backups", "hourly or daily backup plans"),
 ("x", "Slow-query and performance monitoring", "slow log, running queries, stop a query"),
 ("x", "Replication and high availability configuration", "MariaDB and Redis replicas, clusters across servers with failover"),
]),
("File and storage management", [
 ("x", "Browser-based file manager", ""),
 ("x", "Upload, download, copy, move and rename", ""),
 ("x", "ZIP and TAR extraction/compression", ""),
 ("x", "File permission and ownership editor", "chmod in the file manager, fix-permissions per site"),
 ("x", "Code/text editor", ""),
 ("x", "SFTP account management", "per account and per key, plus per-website SFTP users jailed to one website (read-only option, expiry date)"),
 ("x", "Disk quota enforcement", "Linux quotas set up by the installer (ext4 now, XFS after a reboot)"),
 ("x", "S3-compatible object storage integration", "S3, Backblaze B2, Wasabi, R2, MinIO, plus SFTP"),
 ("x", "Storage usage breakdown", "per account and per folder"),
 ("x", "File search", "by name and by text inside files"),
 ("x", "Snapshot and versioning integrations", "file snapshots on the server, restore a file or folder"),
 ("x", "Secure file sharing", "expiring links with password and download limit"),
]),
("Backups and disaster recovery", [
 ("x", "Full server backup orchestration", "restic"),
 ("x", "Per-website backups", ""),
 ("x", "Database and email backups", ""),
 ("x", "Incremental and differential backup support", "deduplicated; every run after the first stores only changes"),
 ("x", "Scheduled backups and retention policies", "hourly, daily, weekly; keep last, hourly, daily, weekly, monthly"),
 ("x", "Local and remote backup targets", ""),
 ("x", "S3, Backblaze B2 and other compatible storage", ""),
 ("x", "Encrypted backups", "with a recovery key shown once"),
 ("x", "One-click restoration", "files, folders or databases, beside or over the current ones"),
 ("x", "Backup integrity verification", "scheduled checks read data back"),
 ("x", "Off-site disaster recovery", "rebuild a new server from a backup and its recovery key"),
 ("x", "Recovery logs and notifications", ""),
]),
("Reseller and shared hosting", [
 ("x", "Administrator, reseller, customer and developer roles", "admin, reseller, account owner and developer (invited to chosen accounts and areas, never billing)"),
 ("x", "Hosting packages and resource limits", "named packages, enforced when accounts, sites, domains and mail are added"),
 ("x", "Customer account provisioning", "panel, API, WHMCS and Blesta"),
 ("x", "Domain and website quotas", "per package, and totals per reseller"),
 ("x", "Bandwidth and disk quotas", "monthly bandwidth caps suspend and lift themselves; disk quotas enforced"),
 ("x", "Customer isolation", "Linux users, chroot SFTP, open_basedir, systemd sandbox"),
 ("x", "Subscription and billing integration", "recurring subscriptions invoice 7 days ahead"),
 ("x", "Invoice and payment gateway integration", "invoices in naira or any currency"),
 ("x", "Paystack and Flutterwave integration", "signed webhooks, amount checked with the provider; Bachs too"),
 ("x", "WHMCS/Blesta or custom billing API", "WHMCS and Blesta modules with single sign-on; provisioning API"),
 ("x", "Suspension and reactivation", ""),
 ("x", "White-label branding", "product name, logo, colour and support links per host name or reseller"),
 ("x", "Usage reporting", "CPU, memory, disk, bandwidth per account"),
]),
("Advanced infrastructure and automation", [
 ("x", "Multiple server nodes and server grouping", ""),
 ("x", "Remote agents with secure communication", "mutual TLS with pinned certificates"),
 ("x", "Infrastructure templates", "spec templates with variables"),
 ("x", "API-first administration", "379 routes, scoped tokens, documented in API.md"),
 ("x", "CLI for automation", "the ike command for Linux, macOS and Windows"),
 ("x", "Webhooks and event subscriptions", "signed, with retries and a delivery log"),
 ("x", "Infrastructure as code integration", "declarative specs: plan, apply, export"),
 ("x", "Load balancer configuration", "a site can balance across servers"),
 ("x", "Redis and database service clusters", ""),
 ("x", "Monitoring and alert routing", "uptime and app health to Telegram, WhatsApp and email"),
 ("x", "Scheduled maintenance workflows", "maintenance page, updates and reboot in a window"),
 ("x", "Server inventory and asset tracking", "hardware, disks, network, owner, location, renewal, cost"),
 ("x", "Audit trails and configuration history", "audit trail, config drift detection and restore"),
]),
("UI/UX and administration", [
 ("x", "Responsive desktop, tablet and mobile interface", ""),
 ("x", "Light and dark themes", "plus a data saver mode for slow connections"),
 ("x", "Custom branding and theme colors", "5 themes, 3 layouts, white-label colour and logo"),
 ("x", "Global search and command palette", "Ctrl K"),
 ("x", "Role-aware navigation", ""),
 ("x", "Notification center", ""),
 ("x", "Interactive setup wizards", "installer wizard"),
 ("x", "Contextual documentation", "help drawer on every screen"),
 ("x", "Keyboard shortcuts", "? lists them"),
 ("x", "Accessible forms and dashboards", ""),
 ("x", "Real-time activity feed", "agent and panel audit log"),
 ("x", "Multi-language readiness", "English, French, Nigerian Pidgin, Yoruba, Igbo, Hausa"),
]),
]

EXTRA = [
 "One-line installer: `curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash` (see [INSTALLER.md](INSTALLER.md))",
 "Health assistant: diagnoses a website from its services and logs and says what to do",
 "Security baseline scanner with a score",
 "Configuration drift detection with diff and one-click restore",
 "Naira-first billing with Paystack and Flutterwave",
 "Data saver mode for slow or metered connections",
 "Update Ike itself from the panel, with checksums",
 "Proxy runtime: a domain and HTTPS in front of any local port",
 "MCP server for AI agents, read-only by default and scoped by API token (see [MCP.md](MCP.md))",
 "The same access model everywhere: scoped API tokens for the API, CLI and MCP; per-key SSH access (see [API.md](API.md#api-tokens))",
]
PLANNED = [
 "Password-protected folders and websites",
 "OpenLiteSpeed and Caddy",
 "Passkeys",
 "A developer role",
 "nftables and firewalld",
 "Swap and network graphs",
 "Per-website SFTP users",
 "One-click apps beyond WordPress: Laravel, Joomla, Drupal, Nextcloud, Ghost",
 "MCP over Streamable HTTP from the panel",
 "A Let's Encrypt certificate for the panel's own address from the settings screen (today: a renewal hook, see INTEGRATIONS.md)",
]

items = [i for _, l in AREAS for i in l]
done = sum(1 for i in items if i[0] == "x")
part = sum(1 for i in items if i[0] == "~")
todo = sum(1 for i in items if i[0] == " ")

# ---- CHECKLIST.md
out = [f"# Ike feature checklist", "", f"Marked against Ike v{VERSION}. Generated by `scripts/features.py`.", "",
       "- `[x]` done: works end to end on a server", "- `[~]` partly done (the note says which part)", "- `[ ]` not yet", ""]
for area, l in AREAS:
    out += [f"## {area}", ""]
    out += [f"- [{s}] {name}" + (f" — {note}" if note else "") for s, name, note in l]
    out.append("")
out.append(f"**Totals:** {done} done, {part} partly done, {todo} not yet, of {len(items)}.")
open(ROOT + "/docs/CHECKLIST.md", "w").write("\n".join(out) + "\n")

# ---- FEATURES.md
mark = {"x": "✅", "~": "🟡", " ": "🔜"}
out = ["# Feature tracker", "",
       "Ike's full scope, grouped by area. Pick anything not done and open a PR; see [CONTRIBUTING.md](../CONTRIBUTING.md).", "",
       f"**v{VERSION}: {done} done · {part} partly done · {todo} planned** (of {len(items)})", "",
       "| Mark | Meaning |", "|---|---|", "| ✅ | Done: works end to end on a server |", "| 🟡 | Partly done (the notes say which part) |", "| 🔜 | Planned |", ""]
for area, l in AREAS:
    out += [f"## {area}", "", "| | Feature | Notes |", "|---|---|---|"]
    out += [f"| {mark[s]} | {name} | {note} |" for s, name, note in l]
    out.append("")
out += ["## Also built (beyond the checklist)", ""] + [f"- ✅ {e}" for e in EXTRA] + ["", "## Planned next", ""] + [f"- 🔜 {p}" for p in PLANNED]
open(ROOT + "/docs/FEATURES.md", "w").write("\n".join(out) + "\n")
print(f"{done} done, {part} partly, {todo} not yet, of {len(items)}")
