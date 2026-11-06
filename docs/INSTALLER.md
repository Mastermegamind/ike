# The one-line installer

Anyone can set up Ike on a fresh Ubuntu 22.04, 24.04 or 26.04 server with:

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash
```

With no options, the guided wizard asks which web server, PHP versions, Node.js, Python and database to install. To skip it, pass options after `bash -s --`:

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- \
  --web hybrid --php all --default-php 8.3 --node "20 22" --python "3.12" --phpmyadmin 6
```

Every option is listed by `install.sh --help`.

## What it does

`installer.sh` (in `installer/`) is a short bootstrap script. It:

1. Checks it's running as root on a supported Ubuntu release and CPU (x86_64 or arm64).
2. Downloads `install.sh` and `install.sh.sha256` from the GitHub release, over HTTPS only.
3. Refuses to continue if the checksum doesn't match.
4. Runs `install.sh`, giving it the terminal so the wizard works even when piped from curl.

The whole script sits inside a function that runs on its last line, so a download cut off halfway does nothing.

| Variable | Default | Use |
|---|---|---|
| `IKE_REPO` | `YOUR-ORG/ike` | Install from a fork |
| `IKE_VERSION` | `latest` | Pin a release, e.g. `v0.3.0` |

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo IKE_VERSION=v0.3.0 bash
```

## Options

Run with no options in a terminal and a wizard asks for each choice. To skip it, pass options after `bash -s --`:

```bash
curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- --web hybrid --php all --mail --dns --postgres --redis --disk-quotas
```

This is `install.sh --help`:

```text
Usage: install.sh [options]

Web server
  --web nginx|apache|hybrid   nginx: Nginx only (default)
                              apache: Apache only
                              hybrid: Nginx in front; each website picks Nginx or Nginx+Apache (.htaccess)
  --default-web-server X      hybrid only: "nginx" or "nginx+apache" for new websites

Languages
  --php "7.4 8.3"             PHP versions, or "all" for 5.6 7.0 7.1 7.2 7.3 7.4 8.0 8.1 8.2 8.3 8.4 8.5
                              (default: "8.2 8.3 8.4 8.5")
  --default-php 8.3           PHP version for new websites (default: newest installed)
  --node "20 22 24"           Node.js major versions, or "none" (default: "22")
  --python "3.12 3.13"        Python versions, or "none" (default: "3.12")
  --java "17 21"              OpenJDK versions (default: none)
  --maven                     Also install Maven for Java builds
  --go "1.24 1.25"            Go versions (default: none)
  --dart "latest"             Dart SDK: latest or an exact version like 3.9.4 (default: none)

Containers
  --docker                    Install Docker Engine with Compose and Buildx
  --docker-for-accounts       Let account owners run containers and Compose stacks (not just admins)
  --buildpacks                Install the Cloud Native Buildpacks tool (pack) for Git deploys without a Dockerfile
  --k3s                       Install Kubernetes (k3s, single node) for server admins

DNS and email
  --dns                       Run a DNS server (PowerDNS) for your domains' zones
  --mail                      Email hosting: Postfix, Dovecot, Rspamd (spam, DKIM), ClamAV and Roundcube webmail
  --no-antivirus              With --mail: skip ClamAV (saves about 1 GB of memory)
  --no-webmail                With --mail: skip Roundcube
  --hostname mail.example.com This server's name (mail greeting, reverse DNS); default: hostname -f

Security
  --disk-quotas               Enforce per-account disk limits with Linux user quotas (ext4; XFS after a reboot).
  --modsecurity               Web application firewall (ModSecurity + OWASP Core Rule Set), switched on per website.
                              With Nginx this uses Ubuntu's Nginx package, which the ModSecurity module is built for.
  --clamav                    ClamAV for malware scans of websites (included with --mail)

Databases
  --no-mariadb                Do not install MariaDB
  --postgres [VERSION]        PostgreSQL: Ubuntu's version, or 14-18 from the PostgreSQL project's repository
  --redis | --valkey          Redis or Valkey: a private instance per account, on a unix socket
  --mongodb [VERSION]         MongoDB Community from mongodb.org (default 8.0)
  --phpmyadmin stable|6|none  phpMyAdmin channel (default: stable; "6" = 6.0 development snapshot)

Multiple servers
  --join URL                  Join this server to a panel on another server (https://panel.example.com:2087)
  --join-token TOKEN          The one-time token from "Servers → Add server" in that panel
  --join-fingerprint FP       That panel's certificate fingerprint (shown with the token)

Other
  --force                     Install even if the server is not fresh
  --agent-binary PATH         Use a locally built ike-agent
  --mcp-binary PATH           Use a locally built ike-mcp
  --cli-binary PATH           Use a locally built ike command-line tool
  --panel-binary PATH         Use a locally built ike-panel
  --admin-user NAME           First panel admin (default: admin)
  --admin-email EMAIL         First panel admin's email
  --wizard                    Ask for every choice step by step (default when run with no options in a terminal)
  -h, --help                  Show this help
```

Running the installer again on a server that already has Ike upgrades it in place; pass the same options as the first time.

## Publishing it on ike.megamindtechnologies.com

Each tagged release (`git tag v0.3.0 && git push --tags`) attaches `installer.sh`, `install.sh` and their `.sha256` files to the GitHub release. The domain then only has to point at them.

1. **DNS.** Add an `A` record for `ike` in the `megamindtechnologies.com` zone, pointing at the server that will answer for it. Any small server works, including one running Ike.
2. **Web server.** Copy `deploy/ike.megamindtechnologies.com.nginx.conf` to `/etc/nginx/conf.d/` and run `nginx -t && systemctl reload nginx`. It redirects `/installer.sh` to the latest release's copy, so you never have to re-upload after a release.
3. **HTTPS.** Run `certbot --nginx -d ike.megamindtechnologies.com`. The one-liner uses `https://`, and curl refuses an invalid certificate.
4. **Check it.**
   ```bash
   curl -fsSLI https://ike.megamindtechnologies.com/installer.sh   # 302, then 200 from GitHub
   curl -fsSL https://ike.megamindtechnologies.com/installer.sh | head -3
   ```

If you'd rather serve the file directly, use option B in the nginx file and upload `installer/installer.sh` to `/var/www/ike/installer.sh`. Upload it again after each release. Serve it as `text/plain` so browsers show it rather than download it.

On a server already running Ike, you can instead create a static website for `ike.megamindtechnologies.com` in the panel and upload `installer.sh` into its `public_html`.

## Before the first public release

- Replace `YOUR-ORG` with the GitHub owner in `installer/installer.sh`, `installer/install.sh`, `deploy/*.conf` and the Go module path (`agent/go.mod`).
- Push a tag so a release exists. Until then `releases/latest/download/...` returns 404 and the installer stops with a clear message.
