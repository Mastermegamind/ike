# Changelog

## v0.8.0 (unreleased)

### New

- **Redis, Valkey and Dragonfly side by side.** `install.sh --redis --valkey --dragonfly` installs any of them; each account picks the engine for its private cache under Databases → Cache (`ike cache`). Switching engine keeps the socket and password and starts empty. Dragonfly runs on one thread with no TCP port, needs at least 256 MB, and is reached with `redis-cli`.
- **Video and calls.** `install.sh --turn --livekit`, then Server → Real-time media (`ike realtime`). TURN (coturn) with short-lived signed logins, no relaying into private networks and TLS from a certificate Ike manages; LiveKit behind a website of yours (`wss://`), with sealed API keys per app or account, a webhook, Ike's TURN server for its callers, and a join-token tester. Account owners get **Video & calls** when the admin allows it. [docs/REALTIME.md](docs/REALTIME.md)
- **Expiry dates.** Domains, SSL certificates and hosting in one list, soonest first (Hosting → Expiry dates, `ike expiry`), with a "Running out soon" card on the dashboard. Domains whose DNS is hosted elsewhere now get their expiry date from the registry too. Hosting comes from Billing subscriptions (an overdue invoice shows the suspension date) or an end date set for accounts billed elsewhere.
- **Expiry reminders on your alert channels.** Domains and hosting reach WhatsApp, Telegram and email at 30, 14, 7, 3, 1 and 0 days, once per mark.
- **Expiry emails to customers.** Owners hear about their hosting, and domains and uploaded certificates that don't renew themselves, at 30, 14, 7 and 1 days, in their reseller's brand, with a Pay now link. Each hosting expiry shows when the account would be suspended.
- **Domain auto-renew through Namecheap** (Ike renews N days before expiry, failures alert you), and renewal dates set by hand for domains registered elsewhere.
- **SMS alerts** through Termii or Africa's Talking, next to WhatsApp, Telegram and email.
- **Bank transfer invoices**: a temporary account number from Paystack or Flutterwave to pay into.
- **Data saver for the server**: scheduled backups and automatic updates wait for a night window and keep under speed caps (restic and apt limits).
- **Laravel per website**: queue workers, the scheduler and Reverb on ws.<domain>, restarted after deploys.
- **Meilisearch per account** (Laravel Scout) and **S3 storage per account** with Garage (buckets, quotas, one key per account).
- **Public status pages** per reseller with 90 days of uptime.
- **Jitsi Meet** in one step (Docker), using the TURN server.
- `install.sh --add dragonfly,turn,livekit` adds caches and media servers to a server that already runs Ike, keeping its settings.
- API: `/realtime/...`, `/accounts/{a}/realtime/...`, `/expiry`, `/expiry/hosting/{account}`, `/expiry/domains/{domain}/refresh`; the cache route takes `engine`. MCP: 22 new tools (208 in all).

### Fixed

- Domain expiry reminders never reached the alert channels (only failures were forwarded).
- A domain found already past several reminder days got one reminder per day; it now gets one, for the nearest day.
- During the week between a subscription's invoice and its due date, hosting expiry showed next month's date.
- Starting a second checkout for an invoice lost the first one's payment reference, so paying through the first didn't settle it.

### Not yet run on a live server

- The Dragonfly, coturn, LiveKit, Garage, Meilisearch and Jitsi setups, the Laravel services, and Namecheap renewals follow their documentation and are covered by tests, but haven't been run on a server yet. Please report anything they refuse.

## v0.7.0

### New

- **Ike updates itself, safely.** Every server checks for new releases and tells admins in the panel and on their alert channels. Updates install when an admin approves them, or by themselves if you choose. Before installing, Ike keeps the current version; after the restart, the previous version checks the new one and goes back by itself if it isn't healthy. Go back to any of the last three versions by hand. **Skip this version**. On a multi-server panel, see and update each server's version. [docs/UPDATES.md](docs/UPDATES.md)
- **Ubuntu packages with a history.** Waiting updates are announced (security fixes separately) and install when an admin approves. Each run records every package's version before and after, and can be rolled back.
- **New Ubuntu releases.** A newer LTS is announced; moving runs only after checks pass and you confirm a snapshot, and Ike turns its package sources back on afterwards.
- **JavaScript frameworks.** Git deploys recognise Next.js, Nuxt, SvelteKit, Astro, Remix, NestJS, Express and friends, Vite (React, Vue, Svelte, Solid, Preact), Create React App, Angular and Gatsby: static builds are served from their output folder, single-page apps send unknown paths to `index.html`, and Node.js apps start with the framework's server. **Single-page app** switch for static websites.
- **Package managers.** Deploys detect pnpm, Yarn (classic and Berry) and Bun at the version the project pins, uv, Poetry, Pipenv, Phive, Maven and Gradle wrappers and Go modules, next to npm, Composer, pip and pub. The installer adds uv, Poetry, Pipenv, pipx, PEAR/PECL and Phive (`--gradle` for Gradle). PECL extensions and PEAR packages install from the PHP extensions page.
- CLI: `ike update`, `ike os`, `ike tools`, `ike pecl`, `ike pear`. MCP: 9 new tools.

### Fixed

- Update checks were switched off by the repository rename in v0.6.0.

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
