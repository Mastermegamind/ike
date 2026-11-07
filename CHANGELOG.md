# Changelog

## v0.6.0

Every item in the feature tracker is now done.

### New

- **Server graphs:** CPU, memory, swap, disk and network (per interface) for the last 24 hours, every 5 minutes. `GET /v1/server/metrics`, `ike metrics`, MCP `get_server_metrics`.
- **Password-protected folders:** protect a whole website or some folders, each with its own users and realm, and let some addresses in without a password (Nginx, Apache and Caddy). Website tab "Passwords", `ike protect`.
- **Website SFTP users:** a login that only sees one website (or one folder of it), upload-and-edit or download only, with an optional expiry date. Files stay owned by the account. Website tab "SFTP", `ike sftp`.
- **Developers:** account owners (and resellers) invite a developer to some of their accounts and choose what they may look at or change: websites, files, databases, containers, DNS, email, backups or security. Developers never see billing or plans, can't add or delete websites, and their API tokens are limited the same way. Page "Developers", `ike developers`.
- **Firewalls:** `install.sh --firewall ufw|nftables|firewalld`. With nftables Ike keeps its own `inet ike` table, checked with `nft --check` before it loads, and `ike-firewall.service` loads it at boot. Ports can be limited to one address or range. `ike firewall`.
- **Passkeys:** sign in with a fingerprint, face, screen lock or security key, without a password or code. Passkeys are bound to the panel's address, so a fake sign-in page can't use them. Add them under Profile (it needs your password). The panel must be opened by its name over HTTPS.
- **Caddy and OpenLiteSpeed:** `install.sh --web caddy` or `--web openlitespeed`. PHP runs in Ike's PHP-FPM pools on both, and Ike still issues the certificates. OpenLiteSpeed reads `.htaccess` rewrites.

### Limits

- ModSecurity needs Nginx or Apache.
- OpenLiteSpeed can't let addresses into a password-protected folder without the password, and can't load-balance.
- The OpenLiteSpeed configuration was checked against its source code but not yet run on a server; please report anything it rejects.

### Changed

- `POST /v1/firewall/ports` takes `from`, and `DELETE /v1/firewall/ports` takes `&from=` for rules limited to one address.
- People in API responses include `passkeys` (names and dates, never keys).

### Upgrading

Run the installer again with the options you used the first time. Existing servers keep ufw and their web server.

## v0.5.0

### New

- **DNS:** zone editor for every record type, templates, PowerDNS with secondaries and DNSSEC, Cloudflare and DigitalOcean, domain registration through Namecheap, propagation and reverse DNS checks, expiry reminders, wildcard certificates.
- **Email:** Postfix, Dovecot, Rspamd, ClamAV and Roundcube; mailboxes, forwarders, auto-replies, quotas, SMTP relays, mail queue, delivery log and a deliverability checker.
- **Databases:** PostgreSQL, MongoDB and a private Redis or Valkey per account, next to MariaDB; connection diagnostics, slow queries, imports, exports and replication.
- **Files:** file manager with editor, archives, search, folder sizes, snapshots and expiring share links; a browser terminal for accounts and containers.
- **Backups:** encrypted, incremental restic backups to S3, Backblaze B2, SFTP or another disk, with schedules, retention, integrity checks, restores and disaster recovery onto a new server.
- **Security:** malware scanning and quarantine, a ModSecurity web firewall per site, encrypted secrets with rotation.
- **Reseller and billing:** hosting packages with enforced limits, reseller quotas, monthly bandwidth caps, recurring subscriptions, white-label branding, and WHMCS and Blesta modules with single sign-on.
- **Many servers:** join servers over mutual TLS, database clusters with failover, load balancing, server specs, webhooks, maintenance windows and an inventory.
- **cPanel restore:** websites, files, databases, mailboxes with their passwords, forwarders, cron jobs and DNS.
- **Disk quotas:** `install.sh --disk-quotas` sets up Linux user quotas.
- **Access:** API tokens can be limited to some areas (read or write), accounts and addresses, can be read only and can expire. Each SSH key can be limited to SFTP or read-only SFTP, some addresses and an expiry date. See [docs/ACCESS.md](docs/ACCESS.md).
- **The `ike` command** for Linux, macOS and Windows ([docs/CLI.md](docs/CLI.md)), installed on servers as `/usr/local/bin/ike`.
- **MCP:** 164 tools; `ike-mcp --panel URL` works through the panel with an API token and only shows what the token allows; `--tools` picks areas ([docs/MCP.md](docs/MCP.md)).
- **API reference** for all 363 routes ([docs/API.md](docs/API.md)).
- **Panel:** Ctrl K command palette, keyboard shortcuts (`?`), notification centre, help on every screen, server switcher, and English, French, Nigerian Pidgin, Yoruba, Igbo and Hausa.

### Changed

- The WHMCS and Blesta modules check the panel's certificate. Give the panel a real certificate ([docs/INTEGRATIONS.md](docs/INTEGRATIONS.md)), or set `IKE_ALLOW_SELF_SIGNED=1` while testing.
- SSH: read-only SFTP keys needed `ForceCommand` removed from the `ike-sftp` match block. The agent rewrites existing keys when it starts, and the installer writes the new sshd configuration.
- Empty lists in API responses are `[]`, not `null`.

### Upgrading

Run the installer again with the options you used the first time. It upgrades in place, and also installs the `ike` command.

The French, Pidgin, Yoruba, Igbo and Hausa translations should be checked by native speakers; corrections are welcome in `web/src/i18n.tsx`.
