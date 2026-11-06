# Ike API reference

Everything the panel does goes through this API, so anything you can click you can also script. The `ike` CLI, the MCP server (`ike-mcp`), and the WHMCS and Blesta modules all use it.

This page is generated from the code by `scripts/gen_api_doc.py`. A test fails when a route is missing, so the list stays complete: 363 routes.

## Where to send requests

| From | Base URL | Signed in by |
|---|---|---|
| Anywhere | `https://your-server:2087/api/v1` (admin), `:2086` (resellers), `:2083` (users) | An API token or a browser session |
| The server itself, as root | `http://agent/v1` on the unix socket `/run/ike/agent.sock` | Being root (members of the `ike` group) |

Paths below are written without the prefix. `GET /sites` means `GET https://your-server:2087/api/v1/sites`, or `GET /v1/sites` on the socket.

The socket is the agent itself and has full access to everything; keep it for root and automation on the server. The panel has every route the agent has, plus the panel routes marked *(panel)*: people, tokens, packages, billing, branding, notifications, servers and clusters. The panel also checks who is asking before passing a request on.

```sh
# From your laptop or CI
curl -H "Authorization: Bearer $IKE_TOKEN" https://server.example.com:2087/api/v1/sites

# On the server, as root
curl --unix-socket /run/ike/agent.sock http://agent/v1/sites
```

## API tokens

Create tokens in the panel under **Settings → API tokens**, or with `ike tokens create`. A token starts with `ike_`, is shown once and is stored only as a hash. Send it as `Authorization: Bearer ike_…`. Token requests need no CSRF header.

Every token acts as the person who made it, and can never do more than that person. An admin's token can reach every account; a reseller's can reach only their customers; a user's can reach only their own accounts. On top of that, each token can be narrowed:

| Setting | Effect |
|---|---|
| **Full** or **limited** | Full: everything the person can do. Limited: only the areas listed in `scopes`. |
| `scopes` | For a limited token, each area is `read` (GET only), `write` (everything) or absent (no access). Areas: `sites`, `dns`, `mail`, `databases`, `files`, `backups`, `containers`, `security`, `billing`, `accounts`, `server`. Each section below names its area. |
| `accounts` | Only these hosting accounts. Every other account's routes are refused, and lists show only these accounts. |
| `allow_ips` | Only from these addresses or ranges (`203.0.113.7`, `10.0.0.0/8`, IPv6 too). |
| `read_only` | GET requests only, in every area. |
| `days` | The token expires after this many days. |

Limited tokens can never create, list or revoke tokens, or change your password or two-factor settings. Only someone signed in with a password can make a token, so a leaked token can't create others.

```sh
# A token for a deploy pipeline: websites only, one account, from the CI's addresses, 90 days
ike tokens create "GitHub deploy" --username andre \
    --scope sites=write --account kelechi --ip 140.82.112.0/20 --days 90
```

A refused request gets `403` with the reason in plain words, for example `{"error": "this API token can only read mail"}`. Use `GET /session` to see what the current token may do.

### The same access in every tool

| Tool | How access is chosen |
|---|---|
| **API** | The token's settings above, checked by the panel on every request. |
| **CLI** (`ike`) | `ike login` stores a token, or set `IKE_URL` and `IKE_TOKEN`. The CLI can do exactly what that token can. On the server as root without a token, it uses the socket (full access). |
| **MCP** (`ike-mcp`) | `ike-mcp --panel https://server:2087` with `IKE_TOKEN` uses the token's access, and tools the token can't use are hidden. `--tools dns,mail` narrows the tools further. Changes need `--allow-write`. Without `--panel` (run over `ssh root@server ike-mcp`) it uses the socket. See [MCP.md](MCP.md). |
| **SSH and SFTP** | Each SSH key has its own access: a full shell, SFTP only, or read-only SFTP. A key can also be limited to some addresses and given an expiry date. See `PUT /accounts/{name}/ssh/keys` and `ike keys`. |

## Requests and responses

- Bodies are JSON (`Content-Type: application/json`). Uploads (`…/upload`, imports) send the raw file as the body.
- From a browser session, any request that changes something needs the header `X-Requested-With: ike` (CSRF protection). Token requests don't.
- Errors come back as `{"error": "what went wrong and what to do"}` with `400` (bad input), `401` (not signed in), `403` (not allowed), `404` (not found), `409` (already exists, or the feature isn't installed) or `429` (too many failed sign-ins).
- Slow work (backups, restores, certificates, deployments, installs) returns a job, `{"id": "…"}`, straight away. Follow it with `GET /jobs/{id}` until `status` is `done` or `failed`; its `log` has the progress lines. `ike … --wait` does this for you.
- Passwords and keys a request creates are returned once in that response and never again.
- `GET /accounts/{a}/terminal`, `GET /terminal` and container terminals are WebSockets.

### Other servers

A panel that manages several servers sends each request to the main server unless you add the header `X-Ike-Server: SERVER_ID` (ids from `GET /servers`). With the CLI, use `ike --server NAME …`. `POST /fanout` runs one request on many servers at once.

## Routes

Paths use `{a}`, `{name}` and `{account}` for a hosting account, `{domain}` or `{d}` for a website or email domain, and `{z}` for a DNS zone.

## Always allowed

Any signed-in person or token, whatever its areas.

| Route | Description |
|---|---|
| `GET /apps` | One-click app catalog |
| `GET /branding` *(panel)* | Branding for this panel address (public: the sign-in page uses it) |
| `GET /events` | Recent server events (notification centre) |
| `GET /health` | Health checks with problems explained |
| `GET /health/events` | App health monitor events |
| `GET /jobs` | List jobs |
| `GET /jobs/{id}` | A background job: status, log lines, result |
| `GET /notifications` *(panel)* | Your notifications, newest first |
| `POST /notifications/read` *(panel)* | Mark read {"up_to": event id} |
| `GET /runtimes` | PHP / Node.js / Python versions and web server choices |
| `GET /session` *(panel)* | Who is signed in; with an API token, also the token's access |
| `POST /session` *(panel)* | Sign in {"username","password","code"} (browser session cookie) |
| `DELETE /session` *(panel)* | Sign out |

## Your sign-in and tokens

Never with a limited token (except `GET /session`); creating a token needs a password session.

| Route | Description |
|---|---|
| `POST /me/2fa/disable` *(panel)* | Turn off two-factor (needs a code) |
| `POST /me/2fa/enable` *(panel)* | Turn on two-factor with a code |
| `POST /me/2fa/setup` *(panel)* | Start two-factor setup → secret and QR code |
| `GET /me/branding` *(panel)* | Your branding (admin: the default; reseller: your own) |
| `PUT /me/branding` *(panel)* | Set product name, logo (PNG/JPEG data URL), colour, support links, host names |
| `POST /me/password` *(panel)* | Change your password {"current","new"} |
| `GET /tokens` *(panel)* | Your API tokens (never the secret) |
| `POST /tokens` *(panel)* | New token {"name","read_only","days","limited","scopes","accounts","allow_ips"} → secret once. Only from a signed-in browser or CLI session, never with another token |
| `DELETE /tokens/{id}` *(panel)* | Revoke a token |

## Accounts, users and provisioning

Token area: `accounts`.

| Route | Description |
|---|---|
| `GET /accounts` | Every hosting account (a reseller or user sees their own) |
| `POST /accounts` | Create an account: body `{"name"}` |
| `GET /accounts/{name}` | One account: limits, SSH mode, sites, suspension |
| `DELETE /accounts/{name}` | Delete an account, its sites and home folder |
| `GET /accounts/{name}/cron` | The account's cron jobs |
| `POST /accounts/{name}/cron` | Cron: body `{"schedule","command","comment","enabled"}` |
| `PUT /accounts/{name}/cron/{id}` | Change a cron job |
| `DELETE /accounts/{name}/cron/{id}` | Delete a cron job |
| `PUT /accounts/{name}/limits` | Set the limits: body `{"cpu_percent","memory_mb","max_processes","disk_mb","php_workers"}` |
| `PUT /accounts/{name}/ssh` | Set the ssh: body `{"mode":` "none"\|"sftp"\|"shell"} |
| `GET /accounts/{name}/ssh/keys` | SSH keys with each key's access (shell, sftp, sftp-readonly), allowed addresses and expiry |
| `POST /accounts/{name}/ssh/keys` | Create an SSH key: body `{"public_key","access","allow_from","expires"}` |
| `PUT /accounts/{name}/ssh/keys` | Set the SSH keys: body `{"access":"\|sftp\|sftp-readonly","allow_from":[],"expires":"YYYY-MM-DD"}` |
| `DELETE /accounts/{name}/ssh/keys` | Remove a key: ?fingerprint=SHA256:… |
| `POST /accounts/{name}/suspend` | Suspend: body `{"reason"}` |
| `POST /accounts/{name}/unsuspend` | Lift a suspension |
| `GET /people` *(panel)* | Panel users (admins see everyone; resellers their own users) |
| `POST /people` *(panel)* | New panel user {"username","email","password","role","accounts"} |
| `PATCH /people/{username}` *(panel)* | Change a user: role, accounts, quotas (max_sites, max_domains, max_disk_mb, max_bandwidth_gb), disabled |
| `DELETE /people/{username}` *(panel)* | Delete a panel user |
| `POST /provision/accounts` *(panel)* | Billing systems: create account, login and site {"account","domain","package","username","email","password"} |
| `DELETE /provision/accounts/{a}` *(panel)* | Billing systems: terminate (account, sites, mail, login) |
| `PUT /provision/accounts/{a}/package` *(panel)* | Billing systems: change package |
| `PUT /provision/accounts/{a}/password` *(panel)* | Billing systems: new password for the customer's login |
| `POST /provision/accounts/{a}/sso` *(panel)* | Billing systems: one-time sign-in link (60 seconds) |
| `POST /provision/accounts/{a}/suspend` *(panel)* | Billing systems: suspend {"reason"} |
| `POST /provision/accounts/{a}/unsuspend` *(panel)* | Billing systems: unsuspend |
| `GET /provision/packages` *(panel)* | Billing systems: packages to offer |
| `GET /provision/usage` *(panel)* | Billing systems: disk and bandwidth per account |

## Websites, deployments, certificates

Token area: `sites`.

| Route | Description |
|---|---|
| `GET /sites` | Every website (filter with ?account=) |
| `POST /sites` | Create a site: body `{"domain","account","runtime","version","web_server","start_command"}` |
| `GET /sites/{domain}` | One website: runtime, domains, HTTPS, status |
| `PATCH /sites/{domain}` | Change the site: body `{"version","web_server","start_command"}` |
| `DELETE /sites/{domain}` | Delete a website (?remove_files=true also deletes its files) |
| `GET /sites/{domain}/app` | App state (Node.js/Python) |
| `GET /sites/{domain}/app/logs` | Logs of the site's app process (Node.js, Python) |
| `POST /sites/{domain}/app/{action}` | Start \| stop \| restart |
| `POST /sites/{domain}/apps/wordpress` | Wordpress: body `{"title","admin_user","admin_email","locale"}` |
| `PUT /sites/{domain}/balancer` | Load balancer pool |
| `POST /sites/{domain}/composer` | Composer: body `{"action","package","dev"}` → job |
| `GET /sites/{domain}/deploy` | Settings, deploy key, webhook path |
| `PUT /sites/{domain}/deploy` | Connect a repository or image |
| `DELETE /sites/{domain}/deploy` | Disconnect (the live release stays) |
| `POST /sites/{domain}/deploy/key` | New deploy key |
| `POST /sites/{domain}/deploy/webhook-secret` | New secret for the Git push webhook |
| `GET /sites/{domain}/deployments` | Deployments, newest first |
| `POST /sites/{domain}/deployments` | Create a deployment: body `{"ref_type","ref","image"}` → queued |
| `GET /sites/{domain}/deployments/{id}` | Deployment + build log |
| `POST /sites/{domain}/deployments/{id}/cancel` | Stop a queued or running deployment |
| `POST /sites/{domain}/deployments/{id}/rollback` | Make an earlier release live again |
| `GET /sites/{domain}/diagnose` | Health assistant: plain-language findings |
| `PUT /sites/{domain}/domains` | Set the domains: body `{"aliases","www","redirect_aliases"}` |
| `GET /sites/{domain}/env` | Env .env variables |
| `PUT /sites/{domain}/env` | Set the env: body `[{"key","value"}]` |
| `PUT /sites/{domain}/healthcheck` | Set the healthcheck: body `{"path","interval_sec","fail_threshold","action"}` or null |
| `GET /sites/{domain}/logs/{kind}` | Access \| error \| php \| apache-access \| apache-error \| app |
| `POST /sites/{domain}/permissions` | Reset ownership and modes |
| `PUT /sites/{domain}/php` | Pool and php.ini settings |
| `POST /sites/{domain}/push` | Push a staging copy's files to live → job |
| `PUT /sites/{domain}/redirects` | Set the redirects: body `[{"kind","match","from","to","code","keep_query"}]` |
| `POST /sites/{domain}/ssl` | Ssl: body `{"email","include_aliases"}` Let's Encrypt → job |
| `PATCH /sites/{domain}/ssl` | Change the ssl: body `{"force_https","hsts","http2","http3"}` |
| `DELETE /sites/{domain}/ssl` | Remove the website's certificate |
| `PUT /sites/{domain}/ssl/custom` | Set the custom: body `{"certificate","private_key"}` |
| `POST /sites/{domain}/staging` | Clone to staging.<domain> → job |
| `PUT /sites/{domain}/status` | Set the status: body `{"status":` "active"\|"maintenance"\|"suspended", "allow_ips": []} |
| `GET /ssl` | Every certificate with days left |

## DNS

Token area: `dns`.

| Route | Description |
|---|---|
| `GET /accounts/{a}/dns/zones` | List DNS zones |
| `POST /accounts/{a}/dns/zones` | Create a DNS zone: body `{"name","backend","kind","primaries","template","ipv4","ipv6"}` |
| `GET /accounts/{a}/dns/zones/{z}` | One DNS zone |
| `PATCH /accounts/{a}/dns/zones/{z}` | Change the DNS zone: body `{"ttl","soa_email","nameservers","also_notify","allow_transfer","dnssec"}` |
| `DELETE /accounts/{a}/dns/zones/{z}` | Delete the DNS zone |
| `GET /accounts/{a}/dns/zones/{z}/check` | Propagation: what Cloudflare, Google, Quad9 and OpenDNS answer for each record, and whether the registrar points at the right name servers |
| `GET /accounts/{a}/dns/zones/{z}/export` | BIND zone file (text/plain) |
| `POST /accounts/{a}/dns/zones/{z}/import` | Import: body `{"zone_file","replace"}` |
| `POST /accounts/{a}/dns/zones/{z}/pull` | Import records from the provider |
| `POST /accounts/{a}/dns/zones/{z}/records` | Add a record {"name","type","content","ttl","priority","weight","port","proxied","comment"} |
| `PUT /accounts/{a}/dns/zones/{z}/records` | (replace all) |
| `PUT /accounts/{a}/dns/zones/{z}/records/{id}` | Replace one record {"name","type","content","ttl","priority",…} |
| `DELETE /accounts/{a}/dns/zones/{z}/records/{id}` | Delete one record |
| `POST /accounts/{a}/dns/zones/{z}/registration` | Refresh expiry from RDAP |
| `POST /accounts/{a}/dns/zones/{z}/template` | Template: body `{"template","ipv4","ipv6"}` |
| `GET /dns` | Settings (tokens masked), record types, templates |
| `PUT /dns` | DNS settings: name servers, SOA email, default TTL, secondaries, default provider and its token |
| `POST /dns/acme` | Add an ACME DNS challenge record {"domain","value"} (wildcard certificates) |
| `DELETE /dns/acme` | Remove an ACME DNS challenge record (wildcard certificates) |
| `GET /dns/registrar` | The connected registrar (API key hidden) and the contact used for new domains |
| `PUT /dns/registrar` | Namecheap account and contact |
| `POST /dns/registrar/check` | Check: body `{"domains":` [...]} |
| `GET /dns/registrar/domains` | Domains in the connected registrar account |
| `POST /dns/registrar/glue` | Glue: body `{"host","ip"}` |
| `PUT /dns/registrar/nameservers` | Set the nameservers: body `{"domain","nameservers"}` |
| `POST /dns/registrar/register` | Register: body `{"domain","years","account","use_own_nameservers"}` |
| `POST /dns/registrar/renew` | Renew: body `{"domain","years"}` |
| `GET /dns/reverse` | Reverse DNS of this server's addresses |
| `GET /dns/zones` | Every zone |

## Email

Token area: `mail`.

| Route | Description |
|---|---|
| `GET /accounts/{a}/mail/domains` | List email domains |
| `POST /accounts/{a}/mail/domains` | Create an email domain: body `{"domain"}` |
| `GET /accounts/{a}/mail/domains/{d}` | One email domain |
| `PATCH /accounts/{a}/mail/domains/{d}` | Change the email domain: body `{"enabled","catch_all"}` |
| `DELETE /accounts/{a}/mail/domains/{d}` | Delete the email domain ?delete_mail=true |
| `PUT /accounts/{a}/mail/domains/{d}/aliases` | Set the aliases: body `[{"from","to"}]` |
| `GET /accounts/{a}/mail/domains/{d}/checks` | Deliverability: MX, SPF, DKIM, DMARC, reverse DNS, port 25, blocklists and certificate, each with a fix |
| `POST /accounts/{a}/mail/domains/{d}/dkim/rotate` | New DKIM key; publish the new record before old mail ages out |
| `POST /accounts/{a}/mail/domains/{d}/dns` | Add them to the zone here |
| `POST /accounts/{a}/mail/domains/{d}/mailboxes` | Create a mailbox: body `{"local","name","password","quota_mb","send_only"}` |
| `PUT /accounts/{a}/mail/domains/{d}/mailboxes/{local}` | Replace the mailbox: body `{"name","password","quota_mb","enabled","forward","keep_copy","auto_reply"}` |
| `DELETE /accounts/{a}/mail/domains/{d}/mailboxes/{local}` | Delete a mailbox (?delete_mail=true also deletes its messages) |
| `GET /accounts/{a}/mail/domains/{d}/mailboxes/{local}/export` | Export .tar.gz |
| `POST /accounts/{a}/mail/domains/{d}/mailboxes/{local}/import` | Import .tar.gz body |
| `GET /accounts/{a}/mail/domains/{d}/records` | DNS records mail needs |
| `POST /accounts/{a}/mail/domains/{d}/tls` | Certificate for mail.<d> → job |
| `POST /accounts/{a}/mail/domains/{d}/usage` | Refresh mailbox usage |
| `GET /accounts/{a}/mail/log` | The account's delivery log (?search=) |
| `GET /accounts/{a}/mail/queue` | The account's queued mail, with why each message waits |
| `POST /accounts/{a}/mail/queue/{action}` | Same, for the account's own mail |
| `GET /mail` | Mail server settings (relay, limits, spam, antivirus) and relay presets |
| `PUT /mail` | Server mail settings (relay, limits, spam), relay presets |
| `GET /mail/domains` | Every email domain |
| `GET /mail/log` | Delivery log |
| `GET /mail/queue` | Every queued message, with why it waits |
| `POST /mail/queue/{action}` | flush \| retry \| hold \| release \| delete (?id= for one message) |

## Databases

Token area: `databases`.

| Route | Description |
|---|---|
| `GET /accounts/{a}/dbs` | Every engine's databases + Redis |
| `POST /accounts/{a}/dbs` | Create a database: body `{"engine":"postgres\|mongodb","name","note"}` → credentials once |
| `GET /accounts/{a}/dbs/processes` | List processes |
| `POST /accounts/{a}/dbs/processes/{id}/kill` | Stop a running query (?connection=true ends the connection) |
| `POST /accounts/{a}/dbs/sizes` | Measure sizes |
| `GET /accounts/{a}/dbs/slow-queries` | The account's slowest MariaDB queries (?sort=duration\|rows) |
| `DELETE /accounts/{a}/dbs/{engine}/{db}` | Delete a database of any engine |
| `POST /accounts/{a}/dbs/{engine}/{db}/diagnose` | Diagnose: body `{"user","password","php"}` |
| `GET /accounts/{a}/dbs/{engine}/{db}/export` | Export .sql.gz (MongoDB: archive.gz) |
| `POST /accounts/{a}/dbs/{engine}/{db}/import` | Raw dump, plain or gzipped |
| `POST /accounts/{a}/dbs/{engine}/{db}/password` | Password: body `{"role"}` → new password |
| `POST /accounts/{a}/dbs/{engine}/{db}/roles` | Create a role: body `{"name","access":"read\|write"}` |
| `DELETE /accounts/{a}/dbs/{engine}/{db}/roles/{user}` | Remove a database user |
| `GET /accounts/{a}/redis` | List redis |
| `PUT /accounts/{a}/redis` | Set the redis: body `{"memory_mb","policy","persist","new_password"}` |
| `DELETE /accounts/{a}/redis` | Delete redis |
| `POST /accounts/{a}/redis/flush` | Delete every key in the account's Redis |
| `GET /accounts/{name}/databases` | The account's MariaDB databases |
| `POST /accounts/{name}/databases` | Create a database: body `{"name","note"}` → password shown once |
| `DELETE /accounts/{name}/databases/{db}` | Delete a MariaDB database and its user |
| `POST /accounts/{name}/databases/{db}/password` | New random password |
| `GET /databases` | Engines on this server |
| `GET /databases/processes` | List processes |
| `POST /databases/processes/{id}/kill` | Stop any running query (admin) |
| `POST /databases/redis/primary` | Make this server's Redis a primary again |
| `POST /databases/redis/replicaof` | Replicaof: body `{"host","port","password"}` (empty host stops) |
| `GET /databases/replication` | MariaDB replication state |
| `POST /databases/replication/replica` | Replica: body `{"primary_host","user","password","server_id","copy_data"}` → job |
| `POST /databases/replication/source` | Source: body `{"replica_ip","server_id"}` → replication user |
| `POST /databases/replication/stop` | Stop: body `{"promote"}` |
| `GET /databases/slow-queries` | Slowest queries on the server (admin) |
| `GET /databases/slowlog` | Slow query log settings |
| `PUT /databases/slowlog` | Set the slowlog: body `{"enabled","long_query_secs","log_queries_not_using_indexes"}` |

## Files, terminal and sharing

Token area: `files`.

| Route | Description |
|---|---|
| `GET /accounts/{a}/files` | Folder listing |
| `GET /accounts/{a}/files/content` | Content: body `{"content"}` for the editor (≤ 2 MB text) |
| `PUT /accounts/{a}/files/content` | Set the content: body `{"content","overwrite"}` |
| `GET /accounts/{a}/files/download` | File, or folder as .tar.gz |
| `GET /accounts/{a}/files/search` | Find by ?name= or text inside files ?content=, under ?path= |
| `GET /accounts/{a}/files/sizes` | Folder sizes |
| `PUT /accounts/{a}/files/upload` | Raw body |
| `POST /accounts/{a}/files/{op}` | File operation; {op} is mkdir {path} · move {from,to,rename} · copy {from,to} · delete {paths} · chmod {paths,mode,recursive} · archive {dir,items,name,format} · extract {path,into} |
| `GET /accounts/{a}/shares` | List shares |
| `POST /accounts/{a}/shares` | Create a share: body `{"path","expires_hours","password","max_downloads"}` |
| `DELETE /accounts/{a}/shares/{token}` | Turn off a share link |
| `GET /accounts/{a}/snapshots` | List snapshots |
| `POST /accounts/{a}/snapshots` | Create a snapshot: body `{"keep"}` → job |
| `DELETE /accounts/{a}/snapshots/{id}` | Delete a file snapshot |
| `GET /accounts/{a}/snapshots/{id}/files` | List files |
| `POST /accounts/{a}/snapshots/{id}/restore` | Copy back {"path","overwrite"} from a snapshot |
| `GET /accounts/{a}/terminal` | WebSocket: the account's shell |
| `GET /shares/{token}` | One share |
| `GET /shares/{token}/download` | Download a shared file (?password=); public through the panel at /s/{token} |

## Backups and storage

Token area: `backups`.

| Route | Description |
|---|---|
| `GET /accounts/{a}/backups/plans` | The account's backup plans |
| `POST /accounts/{a}/backups/plans` | New plan for the account → recovery key once |
| `PUT /accounts/{a}/backups/plans/{id}` | Change one of the account's plans |
| `DELETE /accounts/{a}/backups/plans/{id}` | Delete one of the account's plans |
| `POST /accounts/{a}/backups/plans/{id}/check` | Integrity check → job |
| `GET /accounts/{a}/backups/plans/{id}/recovery-key` | Repository and password for the plan |
| `POST /accounts/{a}/backups/plans/{id}/restore` | Restore → job |
| `POST /accounts/{a}/backups/plans/{id}/run` | Back up now → job |
| `GET /accounts/{a}/backups/plans/{id}/snapshots` | Every backup the plan keeps |
| `GET /accounts/{a}/backups/plans/{id}/snapshots/{snap}/browse` | Files and dumps in one backup |
| `GET /accounts/{a}/storage` | The account's own storage targets |
| `POST /accounts/{a}/storage` | New storage target for the account |
| `PUT /accounts/{a}/storage/{id}` | Change one of the account's storage targets |
| `DELETE /accounts/{a}/storage/{id}` | Remove one of the account's storage targets |
| `GET /accounts/{a}/storage/{id}/browse` | List what's stored in one of the account's targets |
| `POST /accounts/{a}/storage/{id}/check` | Test one of the account's storage targets |
| `POST /accounts/{a}/storage/{id}/upload` | Upload: body `{"path","key"}` → job (file → bucket) |
| `GET /backups/plans` | The server's backup plans |
| `POST /backups/plans` | New plan {name,scope,target,schedule,retention,databases,excludes,check_days,recovery_password} → recovery key once |
| `PUT /backups/plans/{id}` | Change a plan |
| `DELETE /backups/plans/{id}` | Delete a plan (stored backups are kept) |
| `POST /backups/plans/{id}/check` | Read part of the stored data back to prove it restores → job |
| `GET /backups/plans/{id}/recovery-key` | Repository address and password, to restore on a new server |
| `POST /backups/plans/{id}/restore` | Restore {"snapshot","paths","databases","overwrite"} → job |
| `POST /backups/plans/{id}/run` | Back up now → job |
| `GET /backups/plans/{id}/snapshots` | Every backup the plan keeps |
| `GET /backups/plans/{id}/snapshots/{snap}/browse` | Files and database dumps in one backup (?path=) |
| `POST /backups/recover` | Recover: body `{"target","repository","password","snapshot"}` → job (new server) |
| `GET /storage` | The server's storage targets (S3, B2, SFTP, local disk); secret keys never returned |
| `POST /storage` | New storage target {"name","kind","endpoint","region","bucket","prefix","access_key","secret_key","host","port","user"} |
| `GET /storage/sftp-key` | Public key to authorise on SFTP storage |
| `PUT /storage/{id}` | Change a storage target (secret keys left empty stay as they are) |
| `DELETE /storage/{id}` | Remove a storage target |
| `GET /storage/{id}/browse` | List what's stored (?prefix=) |
| `POST /storage/{id}/check` | Test the connection and write access |

## Containers and Kubernetes

Token area: `containers`.

| Route | Description |
|---|---|
| `GET /accounts/{a}/containers/{c}/terminal` | WebSocket: docker exec |
| `GET /accounts/{name}/containers` | List containers |
| `POST /accounts/{name}/containers` | (spec) |
| `GET /accounts/{name}/containers/stats` | CPU, memory and network use of each container |
| `GET /accounts/{name}/containers/{c}/logs` | A container's logs (?lines=) |
| `POST /accounts/{name}/containers/{c}/{action}` | start \| stop \| restart \| remove a container, or exec {"command"} |
| `GET /accounts/{name}/networks` | List networks |
| `POST /accounts/{name}/networks` | Create a network: body `{"name"}` |
| `DELETE /accounts/{name}/networks/{n}` | Remove a Docker network |
| `GET /accounts/{name}/registries` | List registries |
| `POST /accounts/{name}/registries` | Create a registry: body `{"registry","username","password"}` |
| `DELETE /accounts/{name}/registries` | Delete registries ?registry= |
| `GET /accounts/{name}/stacks` | List stacks |
| `GET /accounts/{name}/stacks/{s}` | A Compose stack: compose file, services, state |
| `PUT /accounts/{name}/stacks/{s}` | Change a stack's compose file and environment |
| `DELETE /accounts/{name}/stacks/{s}` | Stop and remove a stack (?volumes=true also removes its volumes) |
| `GET /accounts/{name}/stacks/{s}/logs` | Logs of every service in a stack |
| `POST /accounts/{name}/stacks/{s}/{action}` | up \| down \| restart \| pull a Compose stack → job |
| `GET /accounts/{name}/volumes` | List volumes |
| `POST /accounts/{name}/volumes` | Create a volume: body `{"name"}` |
| `DELETE /accounts/{name}/volumes/{v}` | Remove a Docker volume and its data |
| `GET /docker` | Whether Docker is installed, its version and disk use |
| `GET /docker/containers` | List containers |
| `DELETE /docker/images` | Remove an image: ?ref= |
| `POST /docker/prune` | Remove unused images, networks and build cache |
| `GET /docker/updates` | Containers whose image has a newer version |
| `POST /docker/updates/check` | Check registries for newer images now → job |
| `GET /kubernetes` | List kubernetes |
| `POST /kubernetes/apply` | Apply a manifest (YAML body) |
| `GET /kubernetes/pods/{ns}/{pod}/logs` | A pod's logs |
| `GET /kubernetes/workloads` | Deployments, stateful sets and services of the k3s cluster |
| `DELETE /kubernetes/{kind}/{ns}/{n}` | Delete a Kubernetes object |
| `POST /kubernetes/{kind}/{ns}/{n}/{action}` | restart \| scale a workload: kind/namespace/name/action |

## Security

Token area: `security`.
Firewall, bans, malware, the web application firewall and stored secrets.

| Route | Description |
|---|---|
| `GET /accounts/{a}/malware/scans` | List malware scans |
| `POST /accounts/{a}/malware/scans` | Start a malware scan {"path"} (optional folder) → job |
| `GET /accounts/{a}/malware/scans/{id}` | One scan with its findings |
| `POST /accounts/{a}/malware/scans/{id}/quarantine` | Quarantine: body `{"findings":` [ids]} (empty = all) |
| `GET /accounts/{a}/quarantine` | Files the scanner moved out of the account's websites |
| `DELETE /accounts/{a}/quarantine/{id}` | Delete a quarantined file for good |
| `POST /accounts/{a}/quarantine/{id}/restore` | Put a quarantined file back where it was |
| `GET /firewall` | Open ports + blocked addresses |
| `GET /firewall/bans` | Fail2ban bans |
| `DELETE /firewall/bans` | Unban an address: ?ip=&jail= |
| `POST /firewall/blocks` | Block an address or range {"address","comment"} |
| `DELETE /firewall/blocks` | Unblock an address or range: ?address= |
| `POST /firewall/ports` | Open a port {"port","proto","comment"} |
| `DELETE /firewall/ports` | Close a port: ?port=&proto= |
| `GET /secrets` | List secrets |
| `POST /secrets/rotate` | New key for stored passwords and API keys; everything is re-encrypted |
| `GET /security` | Security baseline scan |
| `GET /security/malware` | Schedule, every report, quarantine (admin) |
| `PUT /security/malware/schedule` | Set the schedule: body `{"enabled","frequency","hour","weekday","auto_quarantine"}` |
| `GET /security/waf` | Installed?, app profiles |
| `PUT /sites/{domain}/waf` | Set the waf: body `{"mode":` "off\|detect\|block", "disabled_rules": [942100], "profile": "wordpress"} |
| `GET /sites/{domain}/waf/events` | Requests the web firewall flagged or blocked (?limit=) |

## Packages and billing

Token area: `billing`.
Most of these are panel routes, not on the agent socket.

| Route | Description |
|---|---|
| `GET /account-packages` *(panel)* | Account → package id |
| `PUT /account-packages/{name}` *(panel)* | Put an account on a package {"package"}; refused if it already uses more |
| `GET /bandwidth` *(panel)* | Traffic this month per account against its cap |
| `GET /billing/invoices` *(panel)* | Invoices with amount, status (open, paid, void) and due date |
| `POST /billing/invoices` *(panel)* | New invoice {"account","items","due"} |
| `POST /billing/invoices/{id}/checkout` *(panel)* | Payment link (Paystack, Flutterwave, Stripe) |
| `POST /billing/invoices/{id}/mark-paid` *(panel)* | Record a payment made another way |
| `POST /billing/invoices/{id}/void` *(panel)* | Cancel an invoice |
| `GET /billing/settings` *(panel)* | Currency, tax, payment providers (keys hidden) |
| `PUT /billing/settings` *(panel)* | Change billing settings |
| `GET /billing/subscriptions` *(panel)* | Recurring billing |
| `POST /billing/subscriptions` *(panel)* | Bill an account for its package {"account","package_id","first_due"} |
| `POST /billing/subscriptions/{id}/cancel` *(panel)* | Stop a subscription |
| `GET /packages` *(panel)* | Hosting packages with limits and how many accounts use each |
| `POST /packages` *(panel)* | New package {"name","price","currency","interval","limits"}; price in the smallest unit (kobo) |
| `PUT /packages/{id}` *(panel)* | Change a package; accounts on it get the new limits |
| `DELETE /packages/{id}` *(panel)* | Delete an unused package |

## Server

Token area: `server`.
Services, updates, usage, inventory, maintenance, webhooks, specs, migration and multi-server management.

| Route | Description |
|---|---|
| `GET /alerts/events` *(panel)* | Alerts sent recently |
| `GET /alerts/settings` *(panel)* | Where serious events are sent (email, Slack, Telegram, webhook) |
| `PUT /alerts/settings` *(panel)* | Change alert channels |
| `POST /alerts/test` *(panel)* | Send a test alert |
| `GET /audit` | Every change made through the agent |
| `GET /clusters` *(panel)* | Database clusters across servers, with health |
| `POST /clusters` *(panel)* | New cluster {"name","engine","primary","replicas"} |
| `DELETE /clusters/{id}` *(panel)* | Remove a cluster (replication stops) |
| `POST /clusters/{id}/promote` *(panel)* | Fail over: make {"server"} the primary |
| `GET /deploy/queue` | Deployments waiting or building, server-wide |
| `PUT /deploy/settings` | How many deployments build at once {"max_concurrent"} |
| `GET /drift` | Managed config files changed by hand |
| `POST /drift/reconcile` | Reconcile: body `{"paths":` [...]} restore them (all when empty) |
| `POST /fanout` *(panel)* | Run one request on many servers {"servers" or "group","method","path","body"} |
| `POST /git/webhook/{domain}` | Raw Git host webhook (forwarded by the panel) |
| `GET /ike/release` | Newest Ike release vs running version |
| `POST /ike/update` | Download, verify, install, restart → job |
| `GET /imports` | Backups waiting in the imports folder |
| `GET /imports/cpanel/{file}` | Inspect a cPanel backup in the imports folder |
| `DELETE /imports/cpanel/{file}` | Delete an uploaded cPanel backup |
| `POST /imports/cpanel/{file}/restore` | Restore: body `{"account","skip":[...],"point_to_this_server"}` → job |
| `PUT /imports/cpanel/{file}/upload` | The archive as the request body (streamed) |
| `GET /inventory` | Hardware, disks, network, asset details |
| `PUT /inventory/asset` | Set the asset: body `{"tag","owner","location","provider","purchase_date","renewal_date","monthly_cost","notes"}` |
| `GET /maintenance` | Maintenance windows |
| `POST /maintenance` | Maintenance: body `{"title","start","end","sites","updates","reboot"}` |
| `DELETE /maintenance/{id}` | Cancel a maintenance window |
| `GET /php/{version}/extensions` | Optional PHP extensions for a PHP version |
| `POST /php/{version}/extensions/{name}` | Install → job |
| `DELETE /php/{version}/extensions/{name}` | Remove → job |
| `GET /quotas` | Disk quota status and per-account use |
| `POST /server/reboot` | Reboot: body `{"in_minutes":` 5} |
| `DELETE /server/reboot` | Cancel a scheduled reboot |
| `GET /servers` *(panel)* | Servers this panel manages (send X-Ike-Server: ID to act on one) |
| `POST /servers/join-token` *(panel)* | One-time join command for a new server {"name","group"} |
| `PATCH /servers/{id}` *(panel)* | Rename or regroup a server |
| `DELETE /servers/{id}` *(panel)* | Stop managing a server |
| `GET /services` | System services and whether each runs |
| `POST /services/{name}/{action}` | Start \| stop \| restart \| reload \| enable \| disable |
| `POST /spec/apply` | Apply: body `{"spec",` "variables"} → job |
| `GET /spec/export` | This server as a spec |
| `POST /spec/plan` | Plan: body `{"spec",` "variables"} → changes |
| `GET /spec/templates` | List templates |
| `PUT /spec/templates/{name}` | Save a spec template {"description","spec"} |
| `DELETE /spec/templates/{name}` | Delete a spec template |
| `POST /spec/templates/{name}/{action}` | plan \| apply a template with {"variables"} |
| `GET /system` | Server facts + installed runtimes |
| `GET /terminal` | WebSocket: root shell (admin) |
| `GET /updates` | Pending Ubuntu updates (refreshes package lists) |
| `POST /updates/apply` | Apply: body `{"security_only":` bool} → background job |
| `GET /updates/auto` | Automatic security update settings |
| `PUT /updates/auto` | Set the auto: body `{"security_updates","auto_reboot","reboot_time"}` |
| `GET /usage` | Per-account CPU, memory, disk, bandwidth (+24 h history) |
| `GET /web/features` | Nginx version, HTTP/2 style, HTTP/3 |
| `GET /webhooks` | List webhooks |
| `POST /webhooks` | Create a webhook: body `{"url","events","enabled"}` → secret once |
| `GET /webhooks/deliveries` | Recent deliveries with the receiver's status code |
| `PUT /webhooks/{id}` | Change a webhook's URL, events or on/off |
| `DELETE /webhooks/{id}` | Delete a webhook |
| `POST /webhooks/{id}/test` | Send a test event to a webhook |

## Public addresses

Served by the panel without signing in. Each checks its own token or signature.

| Route | Description |
|---|---|
| `GET /s/{token}` | Public download page for a share link |
| `POST /s/{token}` | Download a password-protected share |
| `GET /sso/{token}` | One-time sign-in link from a billing system |
| `POST /webhooks/git/{domain}` | Git push webhook (GitHub, GitLab, Gitea, Bitbucket; signed with the site's secret) |
| `POST /webhooks/{provider}` | Payment provider webhook (Paystack, Flutterwave, Stripe; signature checked) |
| `POST /cluster/join` | A new server joining with its one-time token (mutual TLS afterwards) |
