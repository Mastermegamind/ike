# Who can do what

Ike has four ways in: the **web panel and API**, the **`ike` CLI**, the **MCP server** for AI agents, and **SSH/SFTP**. Each can give someone full access, or only the part they need. This page explains how access works across all four.

## The rule underneath

Every request is made *as someone*, and can never do more than that person could in the web panel:

| Role | Reaches |
|---|---|
| **Admin** | The whole server, every account, every server the panel manages |
| **Reseller** | Their own customers' accounts, within the quotas the admin gave them (websites, domains, disk, bandwidth) |
| **Account owner** | Their own hosting accounts, within their package's limits |
| **Developer** | Only the accounts they were invited to, and in those only the areas they were given |

Each role signs in on its own port: 2087 for admins, 2086 for resellers, 2083 for account owners and developers. Two-factor codes and passkeys are available for everyone.

## Developers

An account owner (or a reseller, for their customers' accounts) can invite a developer from the **Developers** page or with `ike developers invite`. They pick the accounts and, for each area, whether the developer may look or change:

| Area | Covers |
|---|---|
| `sites` | settings, deployments, logs, cron jobs and certificates of existing websites |
| `files` | file manager, browser terminal, snapshots and website SFTP users |
| `databases` | MariaDB, PostgreSQL, MongoDB and Redis |
| `containers` | Docker containers and Compose stacks |
| `dns`, `mail`, `backups`, `security` | zones, mailboxes, backup plans, malware scans and the web firewall |

Developers never see billing or plans, can't add or delete websites, accounts or people, and can't change an account's SSH mode or suspend it. A developer's API tokens can't do more than the developer. Pausing or removing a developer signs them out at once.

```sh
ike developers invite emeka.dev --email emeka@pixelforge.ng --account shop --area sites=write --area files=write --area databases=read
ike developers set emeka.dev --area sites=read
ike developers rm emeka.dev
```

## Passkeys

Under **Profile**, anyone can add passkeys: a phone, a laptop's fingerprint reader or screen lock, a password manager, or a security key. Signing in with one needs no password and no code, and it only works on this panel's address, so a look-alike page can't use it. Adding a passkey asks for your password. Passkeys can be listed with an API token (`ike passkeys`) but only added or removed in a browser.

The panel must be opened by its name (like `panel.example.com`) over HTTPS with a valid certificate; browsers don't offer passkeys for IP addresses.

## API tokens: the key to the API, CLI and MCP

An API token acts as the person who made it. When you make one (**Settings → API tokens**, or `ike tokens create`), choose:

| | Full token | Limited token |
|---|---|---|
| Areas | Everything you can do | Only the areas you pick, each **read** or **write** |
| Accounts | All of yours | Optionally only some of them |
| Addresses | Anywhere | Optionally only some IPs or ranges |
| Read only | Optional | Optional |
| Expiry | Optional | Optional |

The areas are `sites`, `dns`, `mail`, `databases`, `files`, `backups`, `containers`, `security`, `billing`, `accounts` and `server`. [API.md](API.md) lists which routes belong to each.

A limited token can never create or list tokens, or change your password or two-factor settings. Tokens can only be made by someone signed in with their password, so a leaked token can't make more tokens. Tokens are stored only as hashes and can be revoked at any time.

### Examples

| Who | Token |
|---|---|
| WHMCS or Blesta | Limited: `accounts` write, `billing` write, `sites` write; only from the billing server's IP |
| A GitHub Actions deploy | Limited: `sites` write; one account; GitHub's address ranges; 90 days |
| An AI assistant helping a customer with DNS and email | Limited: `dns` write, `mail` write; that customer's account; 30 days |
| A monitoring dashboard | Read only, full areas |
| You, on your laptop | Full (made by `ike login --username you`) |

```sh
ike tokens create "Claude for Adaeze" --username andre \
    --scope dns=write --scope mail=write --account adaeze --days 30
ike tokens                 # list, with each token's access
ike tokens revoke 5d7cc61b72
```

## The same access in each tool

**API.** Send the token as `Authorization: Bearer ike_…`. The panel checks the token's areas, accounts and addresses on every request and refuses the rest with a plain reason, such as `this API token can only read mail`. `GET /session` says what the current token may do.

**CLI.** `ike login https://server:2087` saves a token, or set `IKE_URL` and `IKE_TOKEN` (for CI). The CLI can do exactly what that token can; `ike whoami` shows it. On the server itself, as root, `ike` uses the agent socket directly and has full access. See [CLI.md](CLI.md).

**MCP.** Two modes:

- `ike-mcp --panel https://server:2087`, with `IKE_TOKEN` set, uses the token's access. Tools the token can't use are hidden from the AI, and the panel refuses them anyway.
- `ssh root@server ike-mcp` uses the agent socket: every account. Use a dedicated SSH key pinned to `ike-mcp` (see below).

Either way, `--tools dns,mail` shows only some areas, and nothing changes without `--allow-write`. See [MCP.md](MCP.md).

**SSH and SFTP.** Each account has an SSH mode (none, SFTP or shell). Each key added to the account can be narrowed further:

| Key access | What the key can do |
|---|---|
| Account default | Whatever the account's mode allows |
| SFTP only | File transfer in the account's home, never a shell, even when the account has shell access |
| Read-only SFTP | Download only |

Each key can also be limited to some addresses (`--from 203.0.113.0/24`) and given an expiry date. Ike enforces these through OpenSSH's own key options (`restrict`, `from=`, `expiry-time=`, forced `internal-sftp`), and the key files are owned by root so a hosting user can't change them.

```sh
ike keys add --account shop --key-file designer.pub --access sftp --from 102.89.0.0/16 --expires 2026-12-31
ike keys set SHA256:abc… --account shop --access sftp-readonly
```

**Website SFTP users** go one step narrower: a separate login (`shop_designer`) that only sees one website, or one folder of it such as `public_html/wp-content`. It can be upload-and-edit or download only, and can stop working on a date. Files it uploads belong to the account, so the website can still change them. Suspending the account stops these logins too.

```sh
ike sftp add designer --account shop --domain shop.ng --folder public_html --expires 2026-12-31
ike sftp passwd shop_designer --account shop
```

For an AI over SSH, pin the key to the MCP server in root's `authorized_keys`, so it can never open a shell:

```
command="/usr/local/bin/ike-mcp --allow-write --tools sites,dns",restrict,from="203.0.113.7" ssh-ed25519 AAAA… ai-agent
```

## Where it's recorded

Every change is in the activity log with who made it, including the token name. MCP calls are also written to `/var/log/ike/mcp-audit.log`.
