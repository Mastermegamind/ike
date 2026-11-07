# Ports and access

Every port is configurable by the server admin. The defaults below are what the installer opens. The panel never lets anyone close the SSH port or the panel port, so you can't lock yourself out.

## Control panel

One Ike service serves three panels. Each role gets its own port **and** its own subdomain on 443, so it works even on networks that block unusual ports.

| Who | Panel | Port | Subdomain on 443 |
|---|---|---|---|
| Server owner | **Ike Admin** (like WHM) — servers, plans, resellers, accounts, firewall, updates | 2087 | `admin.yourhost.com` |
| Resellers | **Ike Reseller** — their own accounts, plans and branding | 2086 | `reseller.yourhost.com` |
| Account owners | **Ike Panel** (like cPanel) — websites, email, databases, files, cron | 2083 | `panel.yourhost.com` or `yourdomain.com/panel` |
| Everyone with a mailbox | **Webmail** | 2096 | `webmail.yourdomain.com` |

All panels are HTTPS only, with Let's Encrypt certificates. 2FA (TOTP and passkeys) can be required per role. Repeated failed logins are banned by fail2ban.

## Email

| Service | Encrypted from the start (SSL/TLS) | Upgrades with STARTTLS | Notes |
|---|---|---|---|
| Sending from mail apps (SMTP submission) | **465** | **587** | Login required. Either works; 465 is preferred. |
| Server-to-server delivery (SMTP) | — | 25 | STARTTLS offered; many VPS providers block outgoing 25, so a relay can be set. |
| Reading mail (IMAP) | **993** | 143 | 143 only allows login after STARTTLS. |
| Reading mail (POP3) | **995** | 110 | 110 only allows login after STARTTLS. |
| Mail filters (ManageSieve) | — | 4190 | Optional. |

Plain-text passwords are refused on every unencrypted connection. TLS 1.2 is the minimum, and certificates come from Let's Encrypt for `mail.yourdomain.com`.

## Files and shell

| Service | Port | Notes |
|---|---|---|
| SSH / SFTP | 22 (or your SSH port) | SFTP users are jailed to their account or website. |
| FTP with TLS (FTPS) | 21 + passive 40000–40100 | Optional, off by default. Plain FTP is never offered. |

## Web and DNS

| Service | Port |
|---|---|
| HTTP (redirects to HTTPS) | 80 |
| HTTPS | 443 |
| DNS (PowerDNS) | 53 TCP/UDP, only if this server hosts DNS |

## Video and calls (only when switched on)

Opened by Ike when the admin switches the server on under Real-time media, and closed again when it is switched off. See [REALTIME.md](REALTIME.md).

| Service | Port |
|---|---|
| TURN and STUN (coturn) | 3478 UDP and TCP |
| TURN over TLS | 5349 TCP and UDP (when TLS is on) |
| TURN relay | 40000–49999 UDP |
| LiveKit media over TCP | 7881 TCP |
| LiveKit media | 50000–60000 UDP |
| LiveKit signalling | through its website on 443 (`wss://`); LiveKit itself listens on `127.0.0.1:7880` |
| Jitsi Meet media | 10000 UDP (its web pages go through a website on 443; the container listens on `127.0.0.1:8000`) |

## Internal only (never opened in the firewall)

| Service | Address |
|---|---|
| ike-agent API | `/run/ike/agent.sock` (unix socket, root and the `ike` group) |
| Apache behind Nginx | `127.0.0.1:8080` |
| phpMyAdmin | `127.0.0.1:8090` until the panel proxies it with single sign-on |
| Node.js / Python apps | `127.0.0.1:3001` and up |
| MariaDB | unix socket only |
| Account caches (Redis, Valkey, Dragonfly) | a unix socket in the account's home, no TCP port |
| Meilisearch per account | `127.0.0.1:7700` and up (with a master key) |
| Garage S3 / admin API | `127.0.0.1:3900` (served through a website on 443) / `127.0.0.1:3903` |
| Laravel Reverb | a local port per website, served through `ws.<domain>` on 443 |
