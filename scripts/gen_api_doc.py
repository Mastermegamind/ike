#!/usr/bin/env python3
"""Writes docs/API.md from the code: every agent route (go test lists them),
the panel's own routes, the route comments at the top of each API file, and
the descriptions below for routes those comments don't cover.

    scripts/gen_api_doc.py        (run from the repo root)
"""
import glob, json, os, re, subprocess, sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
norm = lambda p: re.sub(r'\{[^}]+\}', '{}', p)

# ---- routes from the code
out = subprocess.run(["go", "test", "-count=1", "-run", "TestRoutesDocumented", "-v", "./internal/api/"],
                     cwd=ROOT + "/agent", env={**os.environ, "IKE_PRINT_ROUTES": "1"}, capture_output=True, text=True).stdout
agent = sorted(set(re.findall(r'((?:GET|POST|PUT|PATCH|DELETE|HEAD) /v1/\S+)', out)))
if not agent:
    sys.exit("no routes from go test")
panel = set()
for f in glob.glob(ROOT + "/agent/internal/panel/*.go"):
    panel |= set(re.findall(r'mux\.HandleFunc\("([A-Z]+ /[^"]+)"', open(f).read()))

# ---- descriptions from the route comments
QUALIFY = {"dns": "DNS", "mail": "email", "ssh": "SSH", "malware": "malware", "backups": "backup", "dbs": "database", "redis": "Redis"}

def singular(n):
    if n.endswith('ies'):
        return n[:-3] + 'y'
    if n.endswith(('xes', 'sses', 'ches')):
        return n[:-2]
    if n.endswith('s') and not n.endswith('ss'):
        return n[:-1]
    return n

def phrase(meth, path, text):
    """Turns a comment fragment into a sentence for one method."""
    text = text.strip(' ,')
    if text and not text.startswith(('{', '[', '?', '→', '.')):
        return text[0].upper() + text[1:]
    segs = [x for x in path.split('/') if x and x != 'v1']
    item = segs[-1].startswith('{')
    static = [x for x in segs if not x.startswith('{')]
    last = static[-1] if static else 'item'
    prev = static[-2] if len(static) > 1 else ''
    n = {"dbs": "databases"}.get(last, last.replace('-', ' '))
    if n in ("domains", "zones", "plans", "keys", "scans", "records", "settings") and prev in QUALIFY:
        n = QUALIFY[prev] + " " + n
    plural = n.endswith('s') and not n.endswith('ss')
    one = singular(n)
    if meth == "GET":
        lead = f"One {one}" if item else (f"List {n}" if plural else n[0].upper() + n[1:])
    elif meth == "POST":
        art = "an" if one[:1].lower() in "aeiou" or one.startswith(("SSH", "SQL", "S3")) else "a"
        lead = f"Create {art} {one}" if plural and not item else last.replace('-', ' ').capitalize()
    elif meth == "PUT":
        lead = f"Set the {n}" if not item else f"Replace the {one}"
    elif meth == "PATCH":
        lead = f"Change the {one}"
    elif meth == "DELETE":
        lead = f"Delete the {one}" if item else f"Delete {n}"
    else:
        lead = meth
    if not text:
        return lead
    if text.startswith(('{', '[')):
        body, _, after = text.partition(' ')
        return f"{lead}: body `{body}`" + (f" {after}" if after else "")
    return f"{lead} {text}"

docs = {}
for f in glob.glob(ROOT + "/agent/internal/api/*.go") + glob.glob(ROOT + "/agent/internal/panel/*.go"):
    for line in open(f):
        m = re.match(r'^//\s+((?:GET|POST|PUT|PATCH|DELETE)(?:\|[A-Z]+)*)\s+(/(?:api/)?v1/\S+)\s*(.*)$', line.rstrip())
        if not m:
            continue
        meths, path, rest = m.groups()
        base = path.rstrip(',').replace('/api/v1', '/v1').split('?')[0]
        # "GET x, POST {...}, DELETE ?q" → [(GET, ''), (POST, '{...}'), (DELETE, '?q')];
        # "GET x, GET /other" leaves the other path to its own line.
        if path.endswith(',') or re.match(r'(GET|POST|PUT|PATCH|DELETE)\b', rest):
            rest = ', ' + rest  # "GET /x, POST {...}": the comma stuck to the path
        parts = re.split(r',\s*(?=(?:GET|POST|PUT|PATCH|DELETE)\b)', rest)
        first, segs = parts[0], []
        for meth in meths.split('|'):
            segs.append((meth, first if not parts[0].startswith(('GET', 'POST', 'PUT', 'PATCH', 'DELETE')) else ''))
        for part in parts[1:]:
            mm = re.match(r'(GET|POST|PUT|PATCH|DELETE)\s*(.*)$', part)
            if mm and not mm.group(2).startswith('/') and not mm.group(2).startswith('.'):
                segs.append((mm.group(1), mm.group(2)))
            elif mm and mm.group(2).startswith('.../'):
                pass  # documented on its own route
        last = base.split('/')[-1]
        targets = ['/'.join(base.split('/')[:-1]) + '/' + a for a in last.split('|')] if '|' in last else [base]
        for t in targets:
            for meth, text in segs:
                d = phrase(meth, t, text)
                if text.strip() or (meth, norm(t)) not in docs:
                    docs[(meth, norm(t))] = d

MANUAL = {
 # accounts, sites, server basics
 "GET /v1/accounts": "Every hosting account (a reseller or user sees their own)",
 "GET /v1/accounts/{}": "One account: limits, SSH mode, sites, suspension",
 "DELETE /v1/accounts/{}": "Delete an account, its sites and home folder",
 "POST /v1/accounts/{}/unsuspend": "Lift a suspension",
 "GET /v1/accounts/{}/cron": "The account's cron jobs",
 "PUT /v1/accounts/{}/cron/{}": "Change a cron job",
 "DELETE /v1/accounts/{}/cron/{}": "Delete a cron job",
 "GET /v1/accounts/{}/databases": "The account's MariaDB databases",
 "DELETE /v1/accounts/{}/databases/{}": "Delete a MariaDB database and its user",
 "GET /v1/accounts/{}/ssh/keys": "SSH keys with each key's access (shell, sftp, sftp-readonly), allowed addresses and expiry",
 "DELETE /v1/accounts/{}/ssh/keys": "Remove a key: ?fingerprint=SHA256:…",
 "GET /v1/sites": "Every website (filter with ?account=)",
 "GET /v1/sites/{}": "One website: runtime, domains, HTTPS, status",
 "DELETE /v1/sites/{}": "Delete a website (?remove_files=true also deletes its files)",
 "DELETE /v1/sites/{}/ssl": "Remove the website's certificate",
 "GET /v1/sites/{}/app/logs": "Logs of the site's app process (Node.js, Python)",
 "GET /v1/sites/{}/deployments": "Deployments, newest first",
 "POST /v1/sites/{}/deployments/{}/cancel": "Stop a queued or running deployment",
 "POST /v1/sites/{}/deployments/{}/rollback": "Make an earlier release live again",
 "POST /v1/sites/{}/deploy/webhook-secret": "New secret for the Git push webhook",
 "PUT /v1/deploy/settings": "How many deployments build at once {\"max_concurrent\"}",
 "GET /v1/sites/{}/waf/events": "Requests the web firewall flagged or blocked (?limit=)",
 "GET /v1/services": "System services and whether each runs",
 "GET /v1/health": "Health checks with problems explained",
 "GET /v1/jobs/{}": "A background job: status, log lines, result",
 "GET /v1/php/{}/extensions": "Optional PHP extensions for a PHP version",
 "POST /v1/firewall/ports": "Open a port {\"port\",\"proto\",\"comment\"}",
 "POST /v1/firewall/blocks": "Block an address or range {\"address\",\"comment\"}",
 "POST /v1/accounts/{}/malware/scans": "Start a malware scan {\"path\"} (optional folder) → job",
 "POST /v1/accounts/{}/dns/zones/{}/records": "Add a record {\"name\",\"type\",\"content\",\"ttl\",\"priority\",\"weight\",\"port\",\"proxied\",\"comment\"}",
 "DELETE /v1/firewall/ports": "Close a port: ?port=&proto=",
 "DELETE /v1/firewall/bans": "Unban an address: ?ip=&jail=",
 "DELETE /v1/firewall/blocks": "Unblock an address or range: ?address=",
 # containers
 "GET /v1/accounts/{}/containers/{}/logs": "A container's logs (?lines=)",
 "POST /v1/accounts/{}/containers/{}/{}": "start | stop | restart | remove a container, or exec {\"command\"}",
 "GET /v1/accounts/{}/containers/stats": "CPU, memory and network use of each container",
 "GET /v1/accounts/{}/stacks/{}": "A Compose stack: compose file, services, state",
 "PUT /v1/accounts/{}/stacks/{}": "Change a stack's compose file and environment",
 "DELETE /v1/accounts/{}/stacks/{}": "Stop and remove a stack (?volumes=true also removes its volumes)",
 "GET /v1/accounts/{}/stacks/{}/logs": "Logs of every service in a stack",
 "DELETE /v1/accounts/{}/networks/{}": "Remove a Docker network",
 "DELETE /v1/accounts/{}/volumes/{}": "Remove a Docker volume and its data",
 "DELETE /v1/docker/images": "Remove an image: ?ref=",
 "POST /v1/docker/prune": "Remove unused images, networks and build cache",
 "GET /v1/docker/updates": "Containers whose image has a newer version",
 "POST /v1/docker/updates/check": "Check registries for newer images now → job",
 "GET /v1/kubernetes/workloads": "Deployments, stateful sets and services of the k3s cluster",
 "POST /v1/kubernetes/apply": "Apply a manifest (YAML body)",
 "POST /v1/kubernetes/{}/{}/{}/{}": "restart | scale a workload: kind/namespace/name/action",
 "DELETE /v1/kubernetes/{}/{}/{}": "Delete a Kubernetes object",
 "GET /v1/kubernetes/pods/{}/{}/logs": "A pod's logs",
 # dns
 "PUT /v1/accounts/{}/dns/zones/{}/records/{}": "Replace one record {\"name\",\"type\",\"content\",\"ttl\",\"priority\",…}",
 "DELETE /v1/accounts/{}/dns/zones/{}/records/{}": "Delete one record",
 "POST /v1/accounts/{}/stacks/{}/{}": "up | down | restart | pull a Compose stack → job",
 "GET /v1/accounts/{}/dns/zones/{}/check": "Propagation: what Cloudflare, Google, Quad9 and OpenDNS answer for each record, and whether the registrar points at the right name servers",
 "PUT /v1/dns": "DNS settings: name servers, SOA email, default TTL, secondaries, default provider and its token",
 "POST /v1/dns/acme": "Add an ACME DNS challenge record {\"domain\",\"value\"} (wildcard certificates)",
 "GET /v1/dns/registrar": "The connected registrar (API key hidden) and the contact used for new domains",
 "GET /v1/accounts/{}/mail/domains/{}/checks": "Deliverability: MX, SPF, DKIM, DMARC, reverse DNS, port 25, blocklists and certificate, each with a fix",
 "GET /v1/accounts/{}/mail/queue": "The account's queued mail, with why each message waits",
 "GET /v1/mail": "Mail server settings (relay, limits, spam, antivirus) and relay presets",
 "GET /v1/mail/queue": "Every queued message, with why it waits",
 "GET /v1/databases/slowlog": "Slow query log settings",
 "GET /v1/storage": "The server's storage targets (S3, B2, SFTP, local disk); secret keys never returned",
 "POST /v1/storage": "New storage target {\"name\",\"kind\",\"endpoint\",\"region\",\"bucket\",\"prefix\",\"access_key\",\"secret_key\",\"host\",\"port\",\"user\"}",
 "GET /v1/accounts/{}/storage": "The account's own storage targets",
 "POST /v1/accounts/{}/storage": "New storage target for the account",
 "GET /v1/docker": "Whether Docker is installed, its version and disk use",
 "POST /v1/accounts/{}/quarantine/{}/restore": "Put a quarantined file back where it was",
 "GET /v1/deploy/queue": "Deployments waiting or building, server-wide",
 "GET /v1/maintenance": "Maintenance windows",
 "POST /v1/webhooks/{}/test": "Send a test event to a webhook",
 "DELETE /v1/dns/acme": "Remove an ACME DNS challenge record (wildcard certificates)",
 "GET /v1/dns/registrar/domains": "Domains in the connected registrar account",
 # mail
 "POST /v1/mail/queue/{}": "flush | retry | hold | release | delete (?id= for one message)",
 "POST /v1/accounts/{}/mail/queue/{}": "Same, for the account's own mail",
 "GET /v1/accounts/{}/mail/log": "The account's delivery log (?search=)",
 "DELETE /v1/accounts/{}/mail/domains/{}/mailboxes/{}": "Delete a mailbox (?delete_mail=true also deletes its messages)",
 "POST /v1/accounts/{}/mail/domains/{}/dkim/rotate": "New DKIM key; publish the new record before old mail ages out",
 # databases
 "DELETE /v1/accounts/{}/dbs/{}/{}": "Delete a database of any engine",
 "DELETE /v1/accounts/{}/dbs/{}/{}/roles/{}": "Remove a database user",
 "GET /v1/accounts/{}/dbs/slow-queries": "The account's slowest MariaDB queries (?sort=duration|rows)",
 "POST /v1/accounts/{}/dbs/processes/{}/kill": "Stop a running query (?connection=true ends the connection)",
 "POST /v1/databases/processes/{}/kill": "Stop any running query (admin)",
 "GET /v1/databases/slow-queries": "Slowest queries on the server (admin)",
 "GET /v1/databases/replication": "MariaDB replication state",
 "POST /v1/databases/redis/primary": "Make this server's Redis a primary again",
 "POST /v1/accounts/{}/redis/flush": "Delete every key in the account's Redis",
 # files
 "POST /v1/accounts/{}/files/{}": "File operation; {op} is mkdir {path} · move {from,to,rename} · copy {from,to} · delete {paths} · chmod {paths,mode,recursive} · archive {dir,items,name,format} · extract {path,into}",
 "GET /v1/accounts/{}/files/search": "Find by ?name= or text inside files ?content=, under ?path=",
 "DELETE /v1/accounts/{}/snapshots/{}": "Delete a file snapshot",
 "POST /v1/accounts/{}/snapshots/{}/restore": "Copy back {\"path\",\"overwrite\"} from a snapshot",
 "DELETE /v1/accounts/{}/shares/{}": "Turn off a share link",
 "GET /v1/shares/{}/download": "Download a shared file (?password=); public through the panel at /s/{token}",
 # storage
 "PUT /v1/storage/{}": "Change a storage target (secret keys left empty stay as they are)",
 "DELETE /v1/storage/{}": "Remove a storage target",
 "POST /v1/storage/{}/check": "Test the connection and write access",
 "GET /v1/storage/{}/browse": "List what's stored (?prefix=)",
 "GET /v1/storage/sftp-key": "Public key to authorise on SFTP storage",
 "PUT /v1/accounts/{}/storage/{}": "Change one of the account's storage targets",
 "DELETE /v1/accounts/{}/storage/{}": "Remove one of the account's storage targets",
 "POST /v1/accounts/{}/storage/{}/check": "Test one of the account's storage targets",
 "GET /v1/accounts/{}/storage/{}/browse": "List what's stored in one of the account's targets",
 # backups (same routes under /v1/backups/plans for the server)
 "GET /v1/backups/plans": "The server's backup plans",
 "POST /v1/backups/plans": "New plan {name,scope,target,schedule,retention,databases,excludes,check_days,recovery_password} → recovery key once",
 "PUT /v1/backups/plans/{}": "Change a plan",
 "DELETE /v1/backups/plans/{}": "Delete a plan (stored backups are kept)",
 "POST /v1/backups/plans/{}/run": "Back up now → job",
 "POST /v1/backups/plans/{}/check": "Read part of the stored data back to prove it restores → job",
 "GET /v1/backups/plans/{}/snapshots": "Every backup the plan keeps",
 "GET /v1/backups/plans/{}/snapshots/{}/browse": "Files and database dumps in one backup (?path=)",
 "POST /v1/backups/plans/{}/restore": "Restore {\"snapshot\",\"paths\",\"databases\",\"overwrite\"} → job",
 "GET /v1/backups/plans/{}/recovery-key": "Repository address and password, to restore on a new server",
 "GET /v1/accounts/{}/backups/plans": "The account's backup plans",
 "POST /v1/accounts/{}/backups/plans": "New plan for the account → recovery key once",
 "PUT /v1/accounts/{}/backups/plans/{}": "Change one of the account's plans",
 "DELETE /v1/accounts/{}/backups/plans/{}": "Delete one of the account's plans",
 "POST /v1/accounts/{}/backups/plans/{}/run": "Back up now → job",
 "POST /v1/accounts/{}/backups/plans/{}/check": "Integrity check → job",
 "GET /v1/accounts/{}/backups/plans/{}/snapshots": "Every backup the plan keeps",
 "GET /v1/accounts/{}/backups/plans/{}/snapshots/{}/browse": "Files and dumps in one backup",
 "POST /v1/accounts/{}/backups/plans/{}/restore": "Restore → job",
 "GET /v1/accounts/{}/backups/plans/{}/recovery-key": "Repository and password for the plan",
 # security
 "GET /v1/accounts/{}/malware/scans/{}": "One scan with its findings",
 "GET /v1/accounts/{}/quarantine": "Files the scanner moved out of the account's websites",
 "DELETE /v1/accounts/{}/quarantine/{}": "Delete a quarantined file for good",
 "POST /v1/secrets/rotate": "New key for stored passwords and API keys; everything is re-encrypted",
 # infrastructure
 "DELETE /v1/maintenance/{}": "Cancel a maintenance window",
 "PUT /v1/webhooks/{}": "Change a webhook's URL, events or on/off",
 "DELETE /v1/webhooks/{}": "Delete a webhook",
 "GET /v1/webhooks/deliveries": "Recent deliveries with the receiver's status code",
 "PUT /v1/spec/templates/{}": "Save a spec template {\"description\",\"spec\"}",
 "DELETE /v1/spec/templates/{}": "Delete a spec template",
 "POST /v1/spec/templates/{}/{}": "plan | apply a template with {\"variables\"}",
 "DELETE /v1/imports/cpanel/{}": "Delete an uploaded cPanel backup",
 # panel
 "POST /v1/session": "Sign in {\"username\",\"password\",\"code\"} (browser session cookie)",
 "GET /v1/session": "Who is signed in; with an API token, also the token's access",
 "DELETE /v1/session": "Sign out",
 "POST /v1/me/password": "Change your password {\"current\",\"new\"}",
 "POST /v1/me/2fa/setup": "Start two-factor setup → secret and QR code",
 "POST /v1/me/2fa/enable": "Turn on two-factor with a code",
 "POST /v1/me/2fa/disable": "Turn off two-factor (needs a code)",
 "GET /v1/tokens": "Your API tokens (never the secret)",
 "POST /v1/tokens": "New token {\"name\",\"read_only\",\"days\",\"limited\",\"scopes\",\"accounts\",\"allow_ips\"} → secret once. Only from a signed-in browser or CLI session, never with another token",
 "DELETE /v1/tokens/{}": "Revoke a token",
 "GET /v1/people": "Panel users (admins see everyone; resellers their own users)",
 "POST /v1/people": "New panel user {\"username\",\"email\",\"password\",\"role\",\"accounts\"}",
 "PATCH /v1/people/{}": "Change a user: role, accounts, quotas (max_sites, max_domains, max_disk_mb, max_bandwidth_gb), disabled",
 "DELETE /v1/people/{}": "Delete a panel user",
 "GET /v1/servers": "Servers this panel manages (send X-Ike-Server: ID to act on one)",
 "POST /v1/servers/join-token": "One-time join command for a new server {\"name\",\"group\"}",
 "PATCH /v1/servers/{}": "Rename or regroup a server",
 "DELETE /v1/servers/{}": "Stop managing a server",
 "POST /v1/fanout": "Run one request on many servers {\"servers\" or \"group\",\"method\",\"path\",\"body\"}",
 "GET /v1/clusters": "Database clusters across servers, with health",
 "POST /v1/clusters": "New cluster {\"name\",\"engine\",\"primary\",\"replicas\"}",
 "POST /v1/clusters/{}/promote": "Fail over: make {\"server\"} the primary",
 "DELETE /v1/clusters/{}": "Remove a cluster (replication stops)",
 "GET /v1/packages": "Hosting packages with limits and how many accounts use each",
 "POST /v1/packages": "New package {\"name\",\"price\",\"currency\",\"interval\",\"limits\"}; price in the smallest unit (kobo)",
 "PUT /v1/packages/{}": "Change a package; accounts on it get the new limits",
 "DELETE /v1/packages/{}": "Delete an unused package",
 "GET /v1/account-packages": "Account → package id",
 "PUT /v1/account-packages/{}": "Put an account on a package {\"package\"}; refused if it already uses more",
 "GET /v1/bandwidth": "Traffic this month per account against its cap",
 "GET /v1/billing/settings": "Currency, tax, payment providers (keys hidden)",
 "PUT /v1/billing/settings": "Change billing settings",
 "GET /v1/billing/invoices": "Invoices with amount, status (open, paid, void) and due date",
 "POST /v1/billing/invoices": "New invoice {\"account\",\"items\",\"due\"}",
 "POST /v1/billing/invoices/{}/checkout": "Payment link (Bachs, Paystack or Flutterwave)",
 "POST /v1/billing/invoices/{}/mark-paid": "Record a payment made another way",
 "POST /v1/billing/invoices/{}/void": "Cancel an invoice",
 "GET /v1/billing/subscriptions": "Recurring billing",
 "POST /v1/billing/subscriptions": "Bill an account for its package {\"account\",\"package_id\",\"first_due\"}",
 "POST /v1/billing/subscriptions/{}/cancel": "Stop a subscription",
 "GET /v1/branding": "Branding for this panel address (public: the sign-in page uses it)",
 "GET /v1/me/branding": "Your branding (admin: the default; reseller: your own)",
 "PUT /v1/me/branding": "Set product name, logo (PNG/JPEG data URL), colour, support links, host names",
 "GET /v1/notifications": "Your notifications, newest first",
 "POST /v1/notifications/read": "Mark read {\"up_to\": event id}",
 "GET /v1/alerts/settings": "Where serious events are sent (Telegram, WhatsApp, email)",
 "PUT /v1/alerts/settings": "Change alert channels",
 "POST /v1/alerts/test": "Send a test alert",
 "GET /v1/alerts/events": "Alerts sent recently",
 "GET /v1/provision/packages": "Billing systems: packages to offer",
 "POST /v1/provision/accounts": "Billing systems: create account, login and site {\"account\",\"domain\",\"package\",\"username\",\"email\",\"password\"}",
 "POST /v1/provision/accounts/{}/suspend": "Billing systems: suspend {\"reason\"}",
 "POST /v1/provision/accounts/{}/unsuspend": "Billing systems: unsuspend",
 "DELETE /v1/provision/accounts/{}": "Billing systems: terminate (account, sites, mail, login)",
 "PUT /v1/provision/accounts/{}/package": "Billing systems: change package",
 "PUT /v1/provision/accounts/{}/password": "Billing systems: new password for the customer's login",
 "POST /v1/provision/accounts/{}/sso": "Billing systems: one-time sign-in link (60 seconds)",
 "GET /v1/provision/usage": "Billing systems: disk and bandwidth per account",
}
PUBLIC = {
 "GET /s/{token}": "Public download page for a share link",
 "POST /s/{token}": "Download a password-protected share",
 "GET /sso/{token}": "One-time sign-in link from a billing system",
 "POST /webhooks/git/{domain}": "Git push webhook (GitHub, GitLab, Gitea, Bitbucket; signed with the site's secret)",
 "POST /webhooks/{provider}": "Payment provider webhook: bachs, paystack or flutterwave (signature checked)",
 "POST /cluster/join": "A new server joining with its one-time token (mutual TLS afterwards)",
}

def area(path):
    seg = path.removeprefix('/v1/').split('/')
    s0 = seg[0]
    if s0 in ("session", "jobs", "branding", "notifications", "events", "health", "runtimes", "apps"): return "always"
    if s0 in ("me", "tokens"): return "tokens"
    if s0 == "ssl": return "sites"
    if s0 == "sites": return "security" if len(seg) >= 3 and seg[2] == "waf" else "sites"
    m = {"dns": "dns", "mail": "mail", "databases": "databases", "backups": "backups", "storage": "backups",
         "security": "security", "secrets": "security", "firewall": "security", "docker": "containers", "kubernetes": "containers",
         "packages": "billing", "billing": "billing", "account-packages": "billing", "bandwidth": "billing",
         "provision": "accounts", "people": "accounts", "shares": "files"}
    if s0 in m: return m[s0]
    if s0 == "accounts":
        if len(seg) < 3: return "accounts"
        m2 = {"dns": "dns", "mail": "mail", "dbs": "databases", "databases": "databases", "redis": "databases",
              "files": "files", "snapshots": "files", "shares": "files", "terminal": "files", "backups": "backups", "storage": "backups",
              "containers": "containers", "stacks": "containers", "volumes": "containers", "networks": "containers", "registries": "containers",
              "malware": "security", "quarantine": "security"}
        return m2.get(seg[2], "accounts")
    return "server"

rows = {}
missing = []
for r in agent + [p.replace('/api/v1', '/v1') for p in panel if '/api/v1' in p]:
    meth, path = r.split(' ', 1)
    key = meth + ' ' + norm(path)
    d = MANUAL.get(key) or docs.get((meth, norm(path)), "")
    if not d:
        missing.append(r)
    who = "panel" if r in {p.replace('/api/v1', '/v1') for p in panel} else ""
    rows.setdefault(area(path), []).append((path.removeprefix('/v1'), meth, d, who))
if missing:
    print("no description:\n  " + "\n  ".join(missing), file=sys.stderr)
    sys.exit(1)

ORDER = [("always", "Always allowed", "Any signed-in person or token, whatever its areas."),
         ("tokens", "Your sign-in and tokens", "Never with a limited token (except `GET /session`); creating a token needs a password session."),
         ("accounts", "Accounts, users and provisioning", ""), ("sites", "Websites, deployments, certificates", ""),
         ("dns", "DNS", ""), ("mail", "Email", ""), ("databases", "Databases", ""), ("files", "Files, terminal and sharing", ""),
         ("backups", "Backups and storage", ""), ("containers", "Containers and Kubernetes", ""),
         ("security", "Security", "Firewall, bans, malware, the web application firewall and stored secrets."),
         ("billing", "Packages and billing", "Most of these are panel routes, not on the agent socket."),
         ("server", "Server", "Services, updates, usage, inventory, maintenance, webhooks, specs, migration and multi-server management.")]

intro = open(ROOT + "/scripts/api_intro.md").read()
out = [intro.rstrip(), ""]
total = 0
for key, title, note in ORDER:
    rs = rows.get(key, [])
    if not rs:
        continue
    out += [f"## {title}", "", f"Token area: `{key}`." if key not in ("always", "tokens") else "", note, "",
            "| Route | Description |", "|---|---|"]
    for path, meth, d, who in sorted(rs, key=lambda x: (x[0], ["GET", "POST", "PUT", "PATCH", "DELETE"].index(x[1]) if x[1] in ["GET", "POST", "PUT", "PATCH", "DELETE"] else 9)):
        tag = " *(panel)*" if who else ""
        out.append(f"| `{meth} {path}`{tag} | {d.replace('|', '\\|')} |")
        total += 1
    out.append("")
out += ["## Public addresses", "", "Served by the panel without signing in. Each checks its own token or signature.", "",
        "| Route | Description |", "|---|---|"] + [f"| `{k}` | {v} |" for k, v in PUBLIC.items()] + [""]
out = [l for i, l in enumerate(out) if not (l == "" and i > 0 and out[i - 1] == "")]
open(ROOT + "/docs/API.md", "w").write("\n".join(out).replace("{total}", str(total)))
print(f"docs/API.md: {total} routes")
