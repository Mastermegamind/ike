# The `ike` command

`ike` manages a server from a terminal or a CI pipeline: websites, deployments, DNS, email, databases, files, backups, security, packages and more. It talks to the same API as the web panel, so it can do exactly what your API token allows (see [ACCESS.md](ACCESS.md)).

## Install

On the server it's already installed as `/usr/local/bin/ike`. As root there, it works straight away through the agent socket.

On your own computer, download `ike` for Linux, macOS or Windows from the release page, then sign in:

```sh
ike login https://server.example.com:2087 --username marvellous    # asks for your password (and 2FA code)
ike login https://server.example.com:2087 --token ike_…       # or use a token you made in the panel
ike whoami
```

`--insecure` accepts the panel's self-signed certificate until you give it a real one. Sign-ins are saved as profiles in `~/.config/ike/config.json` (readable only by you): `ike profiles`, `ike use NAME`, `--profile NAME` and `ike logout`.

In CI, skip `login` and set environment variables:

```sh
export IKE_URL=https://server.example.com:2087 IKE_TOKEN=ike_…
ike deploy api.kelechi.dev --wait
```

## Commands

Run `ike help` for the full list. Common ones:

```sh
# Websites and deployments
ike sites --account shop
ike sites create shop.ng --account shop --runtime php --version 8.3
ike deploy shop.ng --ref v1.4.0 --wait
ike rollback shop.ng DEPLOYMENT_ID
ike ssl shop.ng --wildcard --wait
ike logs shop.ng --kind error --lines 100

# Caches, video and calls
ike cache shop set --engine dragonfly --memory 512
ike realtime turn on --domain turn.shop.ng --tls yes
ike realtime livekit on --domain rtc.shop.ng --account shop --turn yes
ike realtime token class-1 ada --minutes 30

# Expiry dates
ike expiry --kind domain
ike expiry hosting oldclient 2027-03-31

# DNS
ike dns zones --account shop
ike dns add shop.ng --account shop --type TXT --name @ --content "google-site-verification=…"
ike dns check shop.ng --account shop

# Email
ike mail add sales@shop.ng --account shop --quota 2048
ike mail forward orders@shop.ng ada@gmail.com kitchen@shop.ng --account shop
ike mail check shop.ng --account shop          # why mail goes to spam
ike mail log --search customer@yahoo.com

# Files
ike files ls web/shop.ng/public_html --account shop
ike files put ./robots.txt web/shop.ng/public_html/robots.txt --account shop --overwrite
ike files share web/shop.ng/public_html/menu.pdf --account shop --hours 48

# Backups
ike backups
ike backups run PLAN_ID --wait
ike backups restore PLAN_ID SNAPSHOT_ID --path /home/shop/web --wait

# Security
ike malware scan --account shop --wait
ike waf shop.ng block --profile wordpress
ike protect shop.ng user ada                   # makes up a password and shows it once
ike protect shop.ng path /admin/ --realm "Staff only" --user ada
ike firewall open 6379/tcp --from 10.20.0.5 --note "Redis replica"
ike firewall block 203.0.113.7 --note "Brute force"

# Server
ike metrics

# Access
ike tokens create "CI deploy" --username marvellous --scope sites=write --account shop --days 90
ike keys add --account shop --key-file ~/.ssh/id_ed25519.pub --access sftp
ike sftp add designer --account shop --domain shop.ng --read-only
ike developers invite emeka.dev --email emeka@pixelforge.ng --account shop --area sites=write
ike passkeys

# Reseller and billing
ike packages
ike packages assign shop PACKAGE_ID

# Migration from cPanel
ike cpanel upload backup-6.1.2026_tunde.tar.gz
ike cpanel restore backup-6.1.2026_tunde.tar.gz --as tunde --point-dns --wait

# Anything else
ike api GET /sites/shop.ng
ike api POST /accounts '{"name":"newclient"}'
```

## Options for every command

| Option | Meaning |
|---|---|
| `--json` | Print JSON instead of a table, for scripts and `jq` |
| `--profile NAME` | Use another saved sign-in |
| `--server NAME` | Act on another server this panel manages (`ike servers` lists them) |
| `--wait` | For slow work (deploys, backups, restores, scans), follow the job's log until it ends |
| `--yes` | Don't ask before destructive commands |

Slow work returns a job id straight away; `ike jobs ID --wait` follows it later.

## Exit codes

`0` success, `1` the command failed (the reason is printed), `2` wrong usage (the help is printed). A `401` adds a reminder to sign in again.
