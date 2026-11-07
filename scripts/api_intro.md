# Ike API reference

Everything the panel does goes through this API, so anything you can click you can also script. The `ike` CLI, the MCP server (`ike-mcp`), and the WHMCS and Blesta modules all use it.

This page is generated from the code by `scripts/gen_api_doc.py`. A test fails when a route is missing, so the list stays complete: {total} routes.

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
ike tokens create "GitHub deploy" --username marvellous \
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
