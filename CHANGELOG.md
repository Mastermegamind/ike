# Changelog

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
