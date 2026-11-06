# Roadmap

## Done in v0.5.0

- **Foundation:** installer for Ubuntu 22.04, 24.04 and 26.04; root agent on a socket; web panel with roles on separate ports, two-factor sign-in, audit log; websites, PHP 5.6–8.5, Node.js, Python, Java, Go, Dart; HTTPS; billing; alerts
- **Hosting essentials:** DNS (PowerDNS, Cloudflare, DigitalOcean, DNSSEC, registrar, templates), wildcard certificates, cron, logs, disk quotas, file manager, browser terminal, backups with restic
- **Email:** Postfix, Dovecot, Rspamd, ClamAV, DKIM, SPF and DMARC, Roundcube, SMTP relays, deliverability checks
- **Databases:** MariaDB, PostgreSQL, MongoDB, a private Redis (Valkey) per account, diagnostics, slow queries, replication
- **Security:** malware scanning and quarantine, ModSecurity web firewall per site, encrypted secrets with rotation
- **Reseller and billing:** packages with enforced limits, reseller quotas, monthly bandwidth caps, recurring subscriptions, WHMCS and Blesta modules, white-label branding
- **App platform:** Git deploys, previews, rollback, blue-green, Docker, Compose, buildpacks, k3s
- **Scale:** many servers joined over mutual TLS, database clusters with failover, load balancing, server specs (infrastructure as code), webhooks, maintenance windows, inventory
- **Automation:** the `ike` CLI, a 164-tool MCP server, the full API, scoped API tokens and per-key SSH access ([ACCESS.md](ACCESS.md))
- **Migration:** cPanel full backups, including mail, cron and DNS
- **Panel:** command palette, keyboard shortcuts, notification centre, help on every screen, and six languages

## Next

- One-click app catalog: Laravel, Joomla, Drupal, Nextcloud, Ghost, n8n, Plausible, Metabase
- A Let's Encrypt certificate for the panel's own address from the settings screen
- MCP over Streamable HTTP served by the panel
- Password-protected folders and websites
- Passkeys; a developer role
- OpenLiteSpeed and Caddy; nftables and firewalld
- Create servers through Hetzner, DigitalOcean, Vultr and Linode
- Server-to-server migration (for example 22.04 to 24.04 before end of support)

The full status of every feature is in [FEATURES.md](FEATURES.md).
