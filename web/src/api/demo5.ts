// The preview's answers for the 0.5 screens (DNS, email, security, databases,
// files, backups, infrastructure, packages, branding, tokens). Everything
// lives in memory and resets when the page reloads.
import type { Job, SSHKey } from "./types";
import type {
  APIToken, BackupPlan, Branding, DBCluster, DnsRecord, DnsZone, FileEntry, HostingPackage, MailDomainV5, MaintenanceWindow,
  MalwareScan, QuarantineItem, RemoteServer, ServerEvent, Share, StorageTarget, Subscription, Webhook,
} from "./types5";

interface Host {
  accounts: () => string[];
  sites: () => { domain: string; account: string; runtime: string }[];
  job: (kind: string, title: string, steps: string[], onDone?: () => void) => Job;
  mariadb: (account: string) => string[];
  sshKeys: (account: string) => SSHKey[];
}

const now = () => new Date().toISOString();
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const day = 1440;
const hex = (n: number) => Array.from(crypto.getRandomValues(new Uint8Array(n)), (b) => b.toString(16).padStart(2, "0")).join("");
const pw = () => Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"[b % 56]).join("");
const IP = "102.89.40.17";

class NotFound extends Error {}
const nf = (what: string) => { throw new NotFound(`${what} not found.`); };
const bad = (msg: string): never => { throw new Error(msg); };

function rec(name: string, type: string, content: string, extra: Partial<DnsRecord> = {}): DnsRecord {
  return { id: hex(6), name, type, ttl: 3600, content, ...extra };
}
function zone(name: string, account: string, extra: Partial<DnsZone> = {}): DnsZone {
  return {
    name, account, kind: "primary", backend: "local", nameservers: ["ns1.megamind.ng", "ns2.megamind.ng"], ttl: 3600, soa_email: "hostmaster@megamind.ng",
    serial: 2026110601, dnssec: false, created_at: ago(30 * day), updated_at: ago(60), records: [
      rec("@", "A", IP), rec("www", "CNAME", "@"), rec("@", "MX", "mail", { priority: 10 }), rec("mail", "A", IP),
      rec("@", "TXT", `v=spf1 a mx ip4:${IP} ~all`), rec("_dmarc", "TXT", `v=DMARC1; p=quarantine; rua=mailto:postmaster@${name}`),
      rec("ike._domainkey", "TXT", "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu3fZ2b1…", { managed: "dkim" }),
      rec("@", "CAA", '0 issue "letsencrypt.org"'),
    ], ...extra,
  };
}

export class Demo5 {
  private zones: DnsZone[];
  private mail: MailDomainV5[];
  private scans: MalwareScan[];
  private quarantine: QuarantineItem[] = [];
  private files: Record<string, FileEntry[]> = {};
  private fileText: Record<string, string> = {};
  private shares: Share[] = [];
  private storage: StorageTarget[];
  private plans: BackupPlan[];
  private servers: RemoteServer[];
  private clusters: (DBCluster & { members?: { server: string; role: string }[] })[];
  private windows: MaintenanceWindow[] = [];
  private hooks: Webhook[] = [];
  private packages: HostingPackage[];
  private accountPkg: Record<string, string>;
  private subs: Subscription[];
  private brand: Branding = { product_name: "Ike", primary_color: "" };
  private tokens: APIToken[];
  private events: ServerEvent[];
  private readUpTo = 0;
  private dbs: Record<string, { engine: "postgres" | "mongodb"; name: string; owner: string; note?: string; roles?: { name: string; access: string }[]; created_at: string; size_mb?: number }[]> = {
    kelechi: [{ engine: "postgres", name: "kelechi_api", owner: "kelechi_api", note: "API (Prisma)", created_at: ago(5 * day), size_mb: 48.2, roles: [{ name: "kelechi_reports", access: "read" }] },
      { engine: "mongodb", name: "kelechi_events", owner: "kelechi_events", created_at: ago(3 * day), size_mb: 210.5 }],
  };
  private redis: Record<string, { socket: string; memory_mb: number; policy: string; persist: boolean; created_at: string }> = {
    kelechi: { socket: "/var/lib/ike/data/kelechi/redis/redis.sock", memory_mb: 256, policy: "allkeys-lru", persist: false, created_at: ago(5 * day) },
  };
  private mailSettings = {
    relay: { provider: "brevo", host: "smtp-relay.brevo.com", port: 587, username: "postmaster@megamind.ng" }, max_message_mb: 25, hourly_send_limit: 200, spam_threshold: 6,
    reject_threshold: 15, antivirus: true, greylisting: true, webmail_port: 2096, default_quota_mb: 2048, dmarc_policy: "quarantine", has_relay_password: true, installed: true,
    webmail: true, spam_filter: true, hostname: "vps-lagos-1.megamind.ng",
  };
  private dnsSettings = {
    nameservers: [{ host: "ns1.megamind.ng", ipv4: IP }, { host: "ns2.megamind.ng", ipv4: "102.89.40.18" }], soa_email: "hostmaster@megamind.ng", default_ttl: 3600,
    secondaries: [] as string[], default_backend: "local", cloudflare_token: "set", cloudflare_account: "", digitalocean_token: "", expiry_reminders: [30, 14, 7, 1],
    local_server: true, server_ipv4: IP, server_ipv6: "2c0f:f4c0:1::17",
  };
  private schedule = { enabled: true, frequency: "weekly" as const, hour: 3, weekday: 0, auto_quarantine: false, last_run: ago(3 * day) };
  private specTemplates: { name: string; description: string; spec: unknown; variables: string[] }[] = [
    { name: "wordpress-client", description: "New client with a WordPress site and email", variables: ["account", "domain"],
      spec: { accounts: [{ name: "${account}" }], sites: [{ domain: "${domain}", account: "${account}", https: true }], databases: [{ account: "${account}", engine: "mariadb", name: "wp" }], dns: [{ account: "${account}", name: "${domain}", template: "website-email" }] } },
  ];

  constructor(private h: Host) {
    this.zones = [
      zone("naijabites.ng", "naijabites", { dnssec: true, ds: ["47722 13 2 AE6E93DA4F5308DB5FA5CB95C72954A40405F8EEA542C876ED2BA498AC7E07E4"],
        registration: { registrar: "Whogohost Limited", expires: ago(-21 * day), nameservers: ["ns1.megamind.ng", "ns2.megamind.ng"], checked_at: ago(300) } }),
      zone("naijabites.com", "naijabites", { backend: "cloudflare", nameservers: ["ana.ns.cloudflare.com", "bob.ns.cloudflare.com"],
        registration: { registrar: "Namecheap, Inc.", expires: ago(-300 * day), checked_at: ago(300) } }),
      zone("adaeze.com", "adaeze", { records: [rec("@", "A", IP), rec("www", "CNAME", "@"), rec("shop", "A", IP), rec("@", "MX", "mail", { priority: 10 }), rec("mail", "A", IP), rec("@", "TXT", `v=spf1 a mx ip4:${IP} ~all`)],
        registration: { registrar: "Namecheap, Inc.", expires: ago(-200 * day), checked_at: ago(300) } }),
      zone("lagosrentals.com.ng", "lagosrentals", { registration: { registrar: "Web4Africa", expires: ago(-140 * day), checked_at: ago(600) } }),
      zone("kelechi.dev", "kelechi", { records: [rec("@", "A", IP), rec("api", "A", IP), rec("jobs", "A", IP), rec("status", "A", IP), rec("docs", "CNAME", "kelechi.github.io."),
        rec("*.preview", "A", IP), rec("@", "MX", "smtp.google.com.", { priority: 1 }), rec("@", "TXT", "v=spf1 include:_spf.google.com ~all")] }),
    ];
    const mb = (local: string, name: string, quota: number, used: number, extra: object = {}) => ({ local, name, quota_mb: quota, enabled: true, keep_copy: true, created_at: ago(30 * day),
      usage: { used_mb: used, messages: Math.round(used * 9), at: ago(30) }, ...extra });
    this.mail = [
      { domain: "naijabites.ng", account: "naijabites", enabled: true, dkim_selector: "ike", dkim: true, tls: true, created_at: ago(40 * day), webmail_url: "https://mail.naijabites.ng:2096",
        mail_host: "mail.naijabites.ng", aliases: [{ from: "orders", to: ["chidi@naijabites.ng", "kitchen@naijabites.ng"] }, { from: "hello", to: ["chidi@naijabites.ng"] }],
        mailboxes: [mb("chidi", "Chidi Okeke", 5120, 3120), mb("kitchen", "Kitchen", 2048, 410, { auto_reply: { enabled: true, subject: "We got your order", body: "Thanks! We'll call you to confirm.", days: 1 } }),
          mb("noreply", "Website", 512, 2, { send_only: true })] },
      { domain: "adaeze.com", account: "adaeze", enabled: true, dkim_selector: "ike", dkim: true, tls: true, created_at: ago(20 * day), webmail_url: "https://mail.adaeze.com:2096",
        mail_host: "mail.adaeze.com", aliases: [{ from: "orders", to: ["ada@adaeze.com"] }], mailboxes: [mb("ada", "Adaeze Nwosu", 2048, 640)] },
      { domain: "lagosrentals.com.ng", account: "lagosrentals", enabled: true, dkim_selector: "ike", dkim: true, tls: false, created_at: ago(90 * day), webmail_url: "https://mail.lagosrentals.com.ng:2096",
        mail_host: "mail.lagosrentals.com.ng", aliases: [], mailboxes: [mb("tunde", "Tunde Bakare", 10240, 8900), mb("listings", "Listings", 2048, 120)] },
    ];
    this.scans = [
      { id: hex(4), account: "lagosrentals", path: "web/lagosrentals.com.ng", status: "done", started_at: ago(3 * day), finished_at: ago(3 * day - 4), files: 18342, clamav: true, scheduled: true,
        findings: [
          { id: "f1", path: "web/lagosrentals.com.ng/public_html/wp-content/uploads/2024/03/cache.php", engine: "heuristic", signature: "PHP eval of base64 from a request (web shell)", severity: "high", size: 4210 },
          { id: "f2", path: "web/lagosrentals.com.ng/public_html/wp-includes/class-wp-hook.bak.php", engine: "clamav", signature: "Php.Webshell.Generic-1", severity: "high", size: 98112 },
        ] },
      { id: hex(4), account: "adaeze", path: "", status: "done", started_at: ago(3 * day), finished_at: ago(3 * day - 1), files: 2210, clamav: true, scheduled: true, findings: [] },
      { id: hex(4), account: "naijabites", path: "", status: "done", started_at: ago(3 * day), finished_at: ago(3 * day - 2), files: 9120, clamav: true, scheduled: true, findings: [] },
    ];
    this.storage = [
      { id: "b2main", name: "Backblaze B2 (eu-central)", kind: "b2", endpoint: "https://s3.eu-central-003.backblazeb2.com", bucket: "megamind-ike", prefix: "vps-lagos-1", access_key: "003a1b…", created_at: ago(60 * day), last_check: "ok " + ago(60) },
      { id: "disk2", name: "Second disk", kind: "local", prefix: "/mnt/backup/ike", created_at: ago(60 * day), last_check: "ok " + ago(60) },
    ];
    const plan = (id: string, name: string, scope: BackupPlan["scope"], target: string, freq: BackupPlan["schedule"]["frequency"], extra: Partial<BackupPlan> = {}): BackupPlan => ({
      id, name, scope, target, enabled: true, schedule: { frequency: freq, hour: 2, minute: 30, weekday: 0 }, retention: { keep_last: 3, keep_hourly: 24, keep_daily: 7, keep_weekly: 4, keep_monthly: 6 },
      check_days: 7, created_at: ago(60 * day), last: { status: "ok", started_at: ago(8 * 60), finished_at: ago(8 * 60 - 6), snapshot: hex(4), added_bytes: 412e6, total_bytes: 38.4e9, files: 412003 },
      last_check: { status: "ok", started_at: ago(2 * day) }, ...extra });
    this.plans = [
      plan("srv1", "Whole server, off-site", "server", "b2main", "daily"),
      plan("db1", "Databases every hour", "databases", "disk2", "hourly", { last: { status: "ok", started_at: ago(20), finished_at: ago(19), added_bytes: 3.1e6, total_bytes: 1.2e9 } }),
      plan("mail1", "Email, weekly", "mail", "b2main", "weekly", { last: { status: "failed", started_at: ago(6 * day), error: "B2 said: 403 the application key has no access to this bucket" } }),
    ];
    this.servers = [
      { id: "s2", name: "vps-abuja-2", address: "102.89.41.22:2089", group: "web", hostname: "vps-abuja-2.megamind.ng", added_at: ago(20 * day), last_seen: ago(1), version: "0.5.0", status: "online" },
      { id: "s3", name: "db-lagos-1", address: "10.20.0.5:2089", group: "databases", hostname: "db-lagos-1", added_at: ago(15 * day), last_seen: ago(1), version: "0.5.0", status: "online" },
      { id: "s4", name: "vps-ph-1", address: "102.89.43.8:2089", group: "web", hostname: "vps-ph-1.megamind.ng", added_at: ago(9 * day), last_seen: ago(220), version: "0.4.0", status: "offline" },
    ];
    this.clusters = [{ id: "c1", name: "main-db", engine: "mariadb", primary: "s3", replicas: ["local"], created_at: ago(14 * day),
      members: [{ server: "s3", role: "primary" }, { server: "local", role: "replica" }] }];
    const limits = (sites: number, disk: number, bw: number, mbx: number) => ({ sites, domains: sites * 3, databases: sites * 2, email_domains: sites, mailboxes: mbx, disk_mb: disk, bandwidth_gb: bw,
      cpu_percent: 100, memory_mb: 1024, processes: 100, php_workers: 5, shell: false, containers: false, backups: true });
    this.packages = [
      { id: "p1", name: "Starter", price: 250000, currency: "NGN", interval: "month", limits: limits(1, 5120, 50, 5), created: ago(90 * day) },
      { id: "p2", name: "Business", price: 1500000, currency: "NGN", interval: "month", limits: { ...limits(5, 30720, 300, 50), shell: true }, created: ago(90 * day) },
      { id: "p3", name: "Developer", price: 12000000, currency: "NGN", interval: "year", limits: { ...limits(20, 102400, 0, 0), shell: true, containers: true, memory_mb: 4096, cpu_percent: 200 }, created: ago(80 * day) },
      { id: "p4", name: "WebAfrica Lite", owner: "webafrica", price: 180000, currency: "NGN", interval: "month", limits: limits(1, 2048, 20, 3), created: ago(40 * day) },
    ];
    this.accountPkg = { naijabites: "p2", adaeze: "p4", lagosrentals: "p2", kelechi: "p3", oldclient: "p1" };
    this.subs = [
      { id: "sb1", account: "naijabites", package_id: "p2", price: 1500000, currency: "NGN", interval: "month", next_due: ago(-12 * day), status: "active", created_by: "andre", created_at: ago(80 * day) },
      { id: "sb2", account: "kelechi", package_id: "p3", price: 12000000, currency: "NGN", interval: "year", next_due: ago(-360 * day), status: "active", created_by: "andre", created_at: ago(5 * day) },
    ];
    this.tokens = [
      { id: "t1", name: "WHMCS", read_only: false, created: ago(30 * day), last_used: ago(45), limited: true, scopes: { accounts: "write", billing: "write", sites: "write" } },
      { id: "t2", name: "Claude (read only)", read_only: true, created: ago(10 * day), last_used: ago(3) },
      { id: "t3", name: "GitHub Actions deploy", read_only: false, created: ago(5 * day), last_used: ago(200), limited: true, scopes: { sites: "write" }, accounts: ["kelechi"], allow_ips: ["140.82.112.0/20"] },
    ];
    let id = 100;
    const ev = (min: number, type: string, subject: string, level: ServerEvent["level"], data: Record<string, unknown> = {}): ServerEvent => ({ id: id++, time: ago(min), type, subject, level, data });
    this.events = [
      ev(9 * day, "dns.zone.created", "kelechi.dev", "info", { account: "kelechi" }),
      ev(6 * day, "backup.failed", "Email, weekly", "bad", { error: "403 from Backblaze B2" }),
      ev(3 * day, "malware.infected", "lagosrentals", "bad", { account: "lagosrentals", findings: 2 }),
      ev(2 * day, "mail.mailbox.created", "kitchen@naijabites.ng", "info", { account: "naijabites" }),
      ev(300, "dns.domain.expiring", "naijabites.ng", "warn", { account: "naijabites", days_left: 21 }),
      ev(220, "cluster.failover", "vps-ph-1", "warn", {}),
      ev(25, "backup.finished", "Databases every hour", "info", {}),
      ev(4, "site.created", "jobs.kelechi.dev", "info", { account: "kelechi" }),
    ].reverse();
    this.readUpTo = 103;
    this.seedFiles();
  }

  private seedFiles() {
    const f = (name: string, size = 0, minAgo = 600): FileEntry => ({ name, type: name.endsWith("/") ? "dir" : "file", size, modified: ago(minAgo), mode: name.endsWith("/") ? "0755" : "0644" });
    const clean = (l: FileEntry[]) => l.map((e) => ({ ...e, name: e.name.replace(/\/$/, "") }));
    for (const a of this.h.accounts()) {
      const sites = this.h.sites().filter((s) => s.account === a);
      this.files[`${a}:`] = clean([f("web/"), f("tmp/"), f("logs/"), f(".bashrc", 3771, 9000)]);
      this.files[`${a}:web`] = clean(sites.map((s) => f(s.domain + "/")));
      for (const s of sites) {
        this.files[`${a}:web/${s.domain}`] = clean([f("public_html/"), f(".env", 412, 2000), f("errors/")]);
        this.files[`${a}:web/${s.domain}/public_html`] = clean(s.runtime === "php"
          ? [f("wp-admin/"), f("wp-content/"), f("wp-includes/"), f("index.php", 405, 4000), f("wp-config.php", 3301, 900), f(".htaccess", 523, 900), f("robots.txt", 68, 9000)]
          : [f("index.html", 1820, 300), f("assets/"), f("favicon.ico", 4286, 3000)]);
        this.fileText[`${a}:web/${s.domain}/public_html/robots.txt`] = "User-agent: *\nDisallow: /wp-admin/\nSitemap: https://" + s.domain + "/sitemap.xml\n";
        this.fileText[`${a}:web/${s.domain}/public_html/.htaccess`] = "# BEGIN WordPress\nRewriteEngine On\nRewriteRule ^index\\.php$ - [L]\nRewriteCond %{REQUEST_FILENAME} !-f\nRewriteRule . /index.php [L]\n# END WordPress\n";
        this.fileText[`${a}:web/${s.domain}/.env`] = "APP_ENV=production\nAPP_URL=https://" + s.domain + "\n";
      }
    }
  }

  private pushEvent(type: string, subject: string, data: Record<string, unknown> = {}, level: ServerEvent["level"] = "info") {
    const id = (this.events[0]?.id ?? 100) + 1;
    this.events.unshift({ id, time: now(), type, subject, level, data });
  }

  handle(method: string, rawPath: string, body: unknown): unknown {
    const [path, qs] = rawPath.split("?");
    const q = new URLSearchParams(qs || "");
    const b = (body ?? {}) as Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any
    const seg = path.split("/").filter(Boolean).map(decodeURIComponent);
    const M = method.toUpperCase();
    const is = (m: string, pattern: string) => {
      if (m !== M) return null;
      const ps = pattern.split("/").filter(Boolean);
      if (ps.length !== seg.length) return null;
      const out: Record<string, string> = {};
      for (let i = 0; i < ps.length; i++) {
        if (ps[i].startsWith(":")) out[ps[i].slice(1)] = seg[i];
        else if (ps[i] !== seg[i]) return null;
      }
      return out;
    };
    let p: Record<string, string> | null;

    // ---- server metrics
    if (is("GET", "/server/metrics")) {
      const N = 288;
      const history = Array.from({ length: N }, (_, i) => {
        const hr = ((Date.now() - (N - i) * 300_000) / 3.6e6 + 1) % 24; // Lagos local hour
        const busy = Math.max(0, Math.sin(((hr - 7) / 24) * Math.PI * 2)) ; // daytime traffic
        const wob = Math.sin(i * 1.7) * 0.5 + Math.sin(i * 0.37) * 0.5;
        return { t: new Date(Date.now() - (N - i) * 300_000).toISOString(), cpu: Math.round((12 + busy * 38 + wob * 6) * 10) / 10,
          mem: Math.round(4200 + busy * 1600 + wob * 120), swap: Math.round(180 + (i > 200 ? (i - 200) * 2.2 : 0)),
          net_in: Math.round(180_000 + busy * 1_400_000 + wob * 90_000), net_out: Math.round(420_000 + busy * 3_900_000 + wob * 200_000),
          disk_rd: Math.round(90_000 + busy * 600_000 + (hr > 2 && hr < 3 ? 9_000_000 : 0)), disk_wr: Math.round(140_000 + busy * 400_000 + (hr > 2 && hr < 3 ? 6_000_000 : 0)),
          load1: Math.round((0.4 + busy * 1.9) * 100) / 100 };
      });
      const now = history[N - 1];
      return { now, mem_total_mb: 8192, swap_total_mb: 2048, history, interfaces: [
        { name: "eth0", in_bps: now.net_in, out_bps: now.net_out, rx_bytes: 912_400_000_000, tx_bytes: 2_814_900_000_000, rx_errors: 0, tx_errors: 0 },
        { name: "eth1", in_bps: 42_000, out_bps: 38_000, rx_bytes: 18_200_000_000, tx_bytes: 16_900_000_000, rx_errors: 3, tx_errors: 0 }] };
    }

    // ---- shell and business
    if (is("GET", "/branding") || is("GET", "/me/branding")) return { ...this.brand };
    if (is("PUT", "/me/branding")) { this.brand = { ...b } as Branding; return this.brand; }
    if (is("GET", "/notifications")) return this.events.map((e) => ({ ...e, unread: e.id > this.readUpTo }));
    if (is("POST", "/notifications/read")) { this.readUpTo = Math.max(this.readUpTo, b.up_to ?? 0); return { read_up_to: this.readUpTo }; }
    if (is("GET", "/tokens")) return this.tokens;
    if (is("POST", "/tokens")) {
      if (!b.name) bad("Give the token a short name.");
      const t: APIToken = { id: hex(5), name: b.name, read_only: !!b.read_only, created: now(), limited: !!b.limited, scopes: b.limited ? Object.fromEntries(Object.entries(b.scopes ?? {}).filter(([, v]) => v !== "none")) as Record<string, string> : undefined,
        accounts: b.accounts?.length ? b.accounts : undefined, allow_ips: b.allow_ips?.length ? b.allow_ips : undefined, expires: b.days ? new Date(Date.now() + b.days * 864e5).toISOString() : undefined };
      this.tokens.push(t);
      return { ...t, token: "ike_" + hex(24) };
    }
    if ((p = is("DELETE", "/tokens/:id"))) { this.tokens = this.tokens.filter((t) => t.id !== p!.id); return {}; }
    if (is("GET", "/packages")) return this.packages.map((x) => ({ ...x, accounts: Object.values(this.accountPkg).filter((v) => v === x.id).length }));
    if (is("POST", "/packages")) { if (!b.name) bad("Give the package a short name."); const x = { ...b, id: hex(4), created: now() } as HostingPackage; this.packages.push(x); return x; }
    if ((p = is("PUT", "/packages/:id"))) { const i = this.packages.findIndex((x) => x.id === p!.id); this.packages[i] = { ...this.packages[i], ...b, id: p.id }; return this.packages[i]; }
    if ((p = is("DELETE", "/packages/:id"))) {
      if (Object.values(this.accountPkg).includes(p.id)) bad("Accounts still use this package; move them to another one first.");
      this.packages = this.packages.filter((x) => x.id !== p!.id); return {};
    }
    if (is("GET", "/account-packages")) return { ...this.accountPkg };
    if ((p = is("PUT", "/account-packages/:a"))) { if (b.package) this.accountPkg[p.a] = b.package; else delete this.accountPkg[p.a]; return { account: p.a }; }
    if (is("GET", "/billing/subscriptions")) return this.subs;
    if (is("POST", "/billing/subscriptions")) {
      if (this.subs.some((s) => s.account === b.account && s.status === "active")) bad("This account already has an active subscription; cancel it first.");
      const pk = this.packages.find((x) => x.id === b.package_id) ?? nf("Package");
      const s: Subscription = { id: hex(4), account: b.account, package_id: pk!.id, price: pk!.price, currency: pk!.currency, interval: pk!.interval, next_due: b.first_due || now(), status: "active", created_by: "andre", created_at: now() };
      this.subs.push(s); return s;
    }
    if ((p = is("POST", "/billing/subscriptions/:id/cancel"))) { const s = this.subs.find((x) => x.id === p!.id); if (s) s.status = "cancelled"; return {}; }
    if (is("GET", "/bandwidth")) {
      const used: Record<string, number> = { naijabites: 182_400, adaeze: 21_300, lagosrentals: 96_200, kelechi: 410_900, oldclient: 20_480 };
      return { month: now().slice(0, 7), accounts: Object.entries(used).map(([a, mb]) => ({ account: a, used_mb: mb, package_id: this.accountPkg[a],
        limit_gb: this.packages.find((x) => x.id === this.accountPkg[a])?.limits.bandwidth_gb ?? 0, suspended_for_bandwidth: a === "oldclient" })).sort((x, y) => y.used_mb - x.used_mb) };
    }
    if (is("GET", "/quotas")) return { enabled: true, accounts: [
      { account: "naijabites", used_mb: 18420, soft_mb: 30720, hard_mb: 30720, over: false }, { account: "adaeze", used_mb: 1980, soft_mb: 2048, hard_mb: 2048, over: false },
      { account: "lagosrentals", used_mb: 24100, soft_mb: 30720, hard_mb: 30720, over: false }, { account: "kelechi", used_mb: 40220, soft_mb: 102400, hard_mb: 102400, over: false },
      { account: "oldclient", used_mb: 5120, soft_mb: 5120, hard_mb: 5120, over: true }] };

    // ---- SSH keys (access options)
    if ((p = is("POST", "/accounts/:a/ssh/keys"))) {
      const k: SSHKey = { type: String(b.public_key ?? "").split(" ")[0] || "ssh-ed25519", fingerprint: "SHA256:" + btoa(hex(16)).slice(0, 43), comment: String(b.public_key ?? "").split(" ")[2] ?? "",
        access: b.access || "", allow_from: b.allow_from, expires: b.expires || undefined };
      if (!/^(ssh-|ecdsa-|sk-)/.test(String(b.public_key))) bad("public key must look like: ssh-ed25519 AAAA... comment");
      this.h.sshKeys(p.a).push(k); return k;
    }
    if ((p = is("PUT", "/accounts/:a/ssh/keys"))) {
      const k = this.h.sshKeys(p.a).find((x) => x.fingerprint === q.get("fingerprint")) ?? nf("Key");
      Object.assign(k!, { access: b.access || "", allow_from: b.allow_from, expires: b.expires || undefined }); return k;
    }

    // ---- DNS
    if (is("GET", "/dns")) return { types: dnsTypes, templates: dnsTemplates, local: true, settings: this.dnsSettings };
    if (is("PUT", "/dns")) {
      Object.assign(this.dnsSettings, { ...b, cloudflare_token: b.cloudflare_token === "-" ? "" : b.cloudflare_token ? "set" : this.dnsSettings.cloudflare_token,
        digitalocean_token: b.digitalocean_token === "-" ? "" : b.digitalocean_token ? "set" : this.dnsSettings.digitalocean_token });
      return this.dnsSettings;
    }
    if (is("GET", "/dns/zones")) return this.zones;
    if (is("GET", "/dns/reverse")) return [{ address: IP, ptr: ["vps-lagos-1.megamind.ng"], hostname: "vps-lagos-1.megamind.ng", forward_confirmed: true, ok: true, advice: "Reverse DNS is set up correctly." },
      { address: "2c0f:f4c0:1::17", ptr: [], hostname: "vps-lagos-1.megamind.ng", forward_confirmed: false, ok: false, advice: "This address has no reverse DNS. Mail servers often reject mail from it. In your VPS provider's control panel, set the reverse DNS (PTR) of 2c0f:f4c0:1::17 to vps-lagos-1.megamind.ng." }];
    if (is("GET", "/dns/registrar")) return { kind: "namecheap", api_user: "megamind", api_key: "set", username: "megamind", client_ip: IP, sandbox: false,
      contact: { first_name: "Andre", last_name: "Peterson", organization: "Megamind Technologies", address1: "12 Admiralty Way", city: "Lekki", state: "Lagos", postal_code: "106104", country: "NG", phone: "+234.8031234567", email: "domains@megamind.ng" } };
    if (is("PUT", "/dns/registrar")) return { ...b, api_key: "set" };
    if (is("POST", "/dns/registrar/check")) return (b.domains as string[]).map((d, i) => ({ domain: d, available: i % 2 === 0 || d.endsWith(".ng"), premium: d.length < 7 && d.endsWith(".com"), price: d.length < 7 ? "$2,400" : undefined }));
    if (is("POST", "/dns/registrar/register")) { this.pushEvent("dns.domain.registered", b.domain, { years: b.years }); if (b.account) this.zones.push(zone(b.domain, b.account)); return { domain: b.domain, registered: true, charged: "8.88" }; }
    if ((p = is("GET", "/accounts/:a/dns/zones"))) return this.zones.filter((z) => z.account === p!.a);
    if ((p = is("POST", "/accounts/:a/dns/zones"))) {
      const name = String(b.name || "").toLowerCase();
      if (!/^([a-z0-9-]+\.)+[a-z]{2,}$/.test(name)) bad(`"${name}" is not a valid domain`);
      if (this.zones.some((z) => z.name === name)) bad(`DNS zone ${name} already exists`);
      const z = zone(name, p.a, { backend: b.backend || "local", records: [], serial: 2026110601,
        nameservers: b.backend === "cloudflare" ? ["ana.ns.cloudflare.com", "bob.ns.cloudflare.com"] : ["ns1.megamind.ng", "ns2.megamind.ng"] });
      if (b.template && b.template !== "empty") z.records = tmplRecords(b.template, name);
      this.zones.push(z); this.pushEvent("dns.zone.created", name, { account: p.a }); return z;
    }
    if ((p = is("GET", "/accounts/:a/dns/zones/:z"))) return this.zone(p.a, p.z);
    if ((p = is("PATCH", "/accounts/:a/dns/zones/:z"))) {
      const z = this.zone(p.a, p.z);
      if (b.dnssec && z.backend !== "local") bad(z.backend === "cloudflare" ? "Turn on DNSSEC in the Cloudflare dashboard; Cloudflare signs the zone itself" : "DigitalOcean DNS does not support DNSSEC");
      Object.assign(z, { ttl: b.ttl || z.ttl, soa_email: b.soa_email || z.soa_email, dnssec: !!b.dnssec, ds: b.dnssec ? (z.ds ?? ["31415 13 2 " + hex(32).toUpperCase()]) : undefined,
        also_notify: b.also_notify ?? z.also_notify, allow_transfer: b.allow_transfer ?? z.allow_transfer, nameservers: b.nameservers?.length ? b.nameservers : z.nameservers });
      return this.bump(z);
    }
    if ((p = is("DELETE", "/accounts/:a/dns/zones/:z"))) { this.zones = this.zones.filter((z) => z.name !== p!.z); return {}; }
    if ((p = is("POST", "/accounts/:a/dns/zones/:z/records"))) {
      const z = this.zone(p.a, p.z);
      checkRecord(b as DnsRecord, z);
      z.records.push({ ...(b as DnsRecord), id: hex(6), name: b.name || "@", ttl: b.ttl || 3600 }); return this.bump(z);
    }
    if ((p = is("PUT", "/accounts/:a/dns/zones/:z/records/:id"))) {
      const z = this.zone(p.a, p.z); checkRecord(b as DnsRecord, z, p.id);
      z.records = z.records.map((r) => (r.id === p!.id ? { ...(b as DnsRecord), id: r.id } : r)); return this.bump(z);
    }
    if ((p = is("DELETE", "/accounts/:a/dns/zones/:z/records/:id"))) { const z = this.zone(p.a, p.z); z.records = z.records.filter((r) => r.id !== p!.id); return this.bump(z); }
    if ((p = is("POST", "/accounts/:a/dns/zones/:z/template"))) {
      const z = this.zone(p.a, p.z); const add = tmplRecords(b.template, z.name);
      const mail = ["website-email", "google", "microsoft", "zoho"].includes(b.template);
      z.records = z.records.filter((r) => !(mail && ((r.type === "MX" && r.name === "@") || (r.type === "TXT" && r.content.startsWith("v=spf1")) || r.name === "_dmarc")));
      for (const r of add) if (!z.records.some((o) => o.name === r.name && o.type === r.type && o.content === r.content)) z.records.push(r);
      return this.bump(z);
    }
    if ((p = is("POST", "/accounts/:a/dns/zones/:z/import"))) {
      const z = this.zone(p.a, p.z); const skipped: string[] = [];
      for (const line of String(b.zone_file).split("\n")) {
        const f = line.replace(/;.*/, "").trim().split(/\s+/);
        if (f.length < 4 || f[0].startsWith("$")) continue;
        const ti = f.findIndex((x) => /^(A|AAAA|CNAME|MX|TXT|NS|SRV|CAA)$/i.test(x));
        if (ti < 0) { skipped.push(line.trim()); continue; }
        const ttl = Number(f.slice(1, ti).find((x) => /^\d+$/.test(x))) || 3600;
        const type = f[ti].toUpperCase();
        const r: DnsRecord = { id: hex(6), name: f[0], type, ttl, content: f.slice(ti + 1).join(" ").replace(/^"|"$/g, "") };
        if (type === "MX") { r.priority = Number(f[ti + 1]); r.content = f[ti + 2]; }
        z.records.push(r);
      }
      return { zone: this.bump(z), skipped };
    }
    if ((p = is("GET", "/accounts/:a/dns/zones/:z/check"))) {
      const z = this.zone(p.a, p.z); const ok = z.name !== "lagosrentals.com.ng";
      const sets = new Map<string, DnsRecord[]>();
      for (const r of z.records) { const k = r.name + " " + r.type; sets.set(k, [...(sets.get(k) ?? []), r]); }
      return { zone: z.name, delegated: ok ? z.nameservers : ["ns1.web4africa.net", "ns2.web4africa.net"], expected_nameservers: z.nameservers, delegation_ok: ok, checked_at: now(),
        explanation: ok ? "Everything is visible worldwide." : "The registrar points this domain at different name servers (ns1.web4africa.net, ns2.web4africa.net). Records here won't be used until you change the name servers at your registrar.",
        records: [...sets.values()].slice(0, 12).map((rs, i) => {
          const r = rs[0];
          if (!["A", "AAAA", "CNAME", "MX", "TXT", "NS"].includes(r.type)) return { name: r.name, type: r.type, expected: rs.map((x) => x.content), answers: [], propagated: false, note: `${r.type} records are not compared; use dig to check them` };
          const match = ok && i !== 2;
          return { name: r.name, type: r.type, expected: rs.map((x) => (r.type === "MX" ? `${x.priority} ${x.content}` : x.content)), propagated: match,
            answers: ["Cloudflare", "Google", "Quad9", "OpenDNS"].map((res, j) => ({ resolver: res, values: [], match: match || (ok && j < 2) })) };
        }) };
    }
    if ((p = is("POST", "/accounts/:a/dns/zones/:z/registration"))) { const z = this.zone(p.a, p.z); z.registration = { ...(z.registration ?? {}), checked_at: now() }; return z; }
    if ((p = is("POST", "/accounts/:a/dns/zones/:z/pull"))) return this.zone(p.a, p.z);

    // ---- mail
    if (is("GET", "/mail")) return { installed: true, webmail: true, webmail_port: 2096, settings: this.mailSettings,
      relay_presets: { brevo: { Host: "smtp-relay.brevo.com", Port: 587 }, mailgun: { Host: "smtp.mailgun.org", Port: 587 }, ses: { Host: "email-smtp.us-east-1.amazonaws.com", Port: 587 },
        sendgrid: { Host: "smtp.sendgrid.net", Port: 587 }, postmark: { Host: "smtp.postmarkapp.com", Port: 587 }, zeptomail: { Host: "smtp.zeptomail.com", Port: 587 } } };
    if (is("PUT", "/mail")) { Object.assign(this.mailSettings, b); return this.mailSettings; }
    if ((p = is("GET", "/accounts/:a/mail/domains"))) return this.mail.filter((d) => d.account === p!.a);
    if ((p = is("POST", "/accounts/:a/mail/domains"))) {
      const d = String(b.domain || "").toLowerCase();
      if (this.mail.some((x) => x.domain === d)) bad(`${d} already has email`);
      const md: MailDomainV5 = { domain: d, account: p.a, enabled: true, dkim_selector: "ike", dkim: true, tls: false, mailboxes: [], aliases: [], created_at: now(), webmail_url: `https://mail.${d}:2096`, mail_host: `mail.${d}` };
      this.mail.push(md); this.pushEvent("mail.domain.created", d, { account: p.a }); return md;
    }
    if ((p = is("GET", "/accounts/:a/mail/domains/:d"))) return this.md(p.a, p.d);
    if ((p = is("PATCH", "/accounts/:a/mail/domains/:d"))) { const d = this.md(p.a, p.d); d.enabled = !!b.enabled; d.catch_all = b.catch_all || undefined; return d; }
    if ((p = is("DELETE", "/accounts/:a/mail/domains/:d"))) { this.mail = this.mail.filter((x) => x.domain !== p!.d); return {}; }
    if ((p = is("POST", "/accounts/:a/mail/domains/:d/mailboxes"))) {
      const d = this.md(p.a, p.d); const local = String(b.local || "").toLowerCase();
      if (!/^[a-z0-9][a-z0-9._-]{0,63}$/.test(local)) bad("The part before @ can use letters, digits, dots, dashes and underscores.");
      if (d.mailboxes.some((m) => m.local === local)) bad(`${local}@${d.domain} already exists`);
      if (b.password && String(b.password).length < 10) bad("password must be 10-200 characters");
      d.mailboxes.push({ local, name: b.name, quota_mb: b.quota_mb ?? 2048, enabled: true, send_only: !!b.send_only, keep_copy: true, created_at: now(), usage: { used_mb: 0, messages: 0, at: now() } });
      this.pushEvent("mail.mailbox.created", `${local}@${d.domain}`, { account: p.a });
      return { address: `${local}@${d.domain}`, password: b.password ? undefined : pw() + pw(), imap_host: d.mail_host, smtp_host: d.mail_host };
    }
    if ((p = is("PUT", "/accounts/:a/mail/domains/:d/mailboxes/:l"))) {
      const d = this.md(p.a, p.d); const m = d.mailboxes.find((x) => x.local === p!.l) ?? nf("Mailbox");
      Object.assign(m!, { name: b.name, quota_mb: b.quota_mb, enabled: b.enabled, send_only: b.send_only, forward: b.forward, keep_copy: b.keep_copy, auto_reply: b.auto_reply }); return d;
    }
    if ((p = is("DELETE", "/accounts/:a/mail/domains/:d/mailboxes/:l"))) { const d = this.md(p.a, p.d); d.mailboxes = d.mailboxes.filter((m) => m.local !== p!.l); return {}; }
    if ((p = is("POST", "/accounts/:a/mail/domains/:d/mailboxes/:l/import"))) return { imported: true };
    if ((p = is("PUT", "/accounts/:a/mail/domains/:d/aliases"))) { const d = this.md(p.a, p.d); d.aliases = body as MailDomainV5["aliases"]; return d; }
    if ((p = is("GET", "/accounts/:a/mail/domains/:d/records"))) {
      const d = p.d;
      return [{ Name: d, Type: "MX", Content: `mail.${d}`, Priority: 10 }, { Name: `mail.${d}`, Type: "A", Content: IP, Priority: 0 }, { Name: d, Type: "TXT", Content: `v=spf1 a mx ip4:${IP} ~all`, Priority: 0 },
        { Name: `ike._domainkey.${d}`, Type: "TXT", Content: "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu3fZ2b1qVv…IDAQAB", Priority: 0 },
        { Name: `_dmarc.${d}`, Type: "TXT", Content: `v=DMARC1; p=quarantine; rua=mailto:postmaster@${d}`, Priority: 0 }];
    }
    if ((p = is("POST", "/accounts/:a/mail/domains/:d/dns"))) return { added: this.zones.some((z) => z.name === p!.d) };
    if ((p = is("GET", "/accounts/:a/mail/domains/:d/checks"))) {
      const d = p.d; const here = this.zones.some((z) => z.name === d);
      return [
        { record: "MX", host: d, type: "MX", expected: `10 mail.${d}`, found: here ? `10 mail.${d}` : "10 mx.web4africa.net", status: here ? "ok" : "wrong", hint: here ? "Mail for the domain comes to this server." : "Mail for this domain goes to another server. Change the MX record to mail." + d + "." },
        { record: "SPF", host: d, type: "TXT", expected: `v=spf1 a mx ip4:${IP} ~all`, status: here ? "ok" : "missing", hint: here ? "This server may send mail for the domain." : "Without SPF, Gmail and Outlook treat your mail as suspicious." },
        { record: "DKIM", host: `ike._domainkey.${d}`, type: "TXT", expected: "v=DKIM1; k=rsa; p=MIIB…", status: "ok", hint: "Outgoing mail is signed and the key is published." },
        { record: "DMARC", host: `_dmarc.${d}`, type: "TXT", expected: "v=DMARC1; p=quarantine", status: here ? "ok" : "missing", hint: "Tells receivers what to do with mail that fails SPF and DKIM." },
        { record: "PTR", host: IP, type: "PTR", expected: "vps-lagos-1.megamind.ng", status: "ok", hint: "Reverse DNS matches the server's name." },
        { record: "PORT25", host: "outgoing", type: "", expected: "open", status: "warn", hint: "Your VPS provider blocks outgoing port 25. Mail goes out through the Brevo relay instead, which is fine." },
        { record: "BLOCKLIST", host: IP, type: "", expected: "not listed", status: "ok", hint: "This server isn't on Spamhaus, SpamCop or Barracuda." },
        { record: "TLS", host: `mail.${d}`, type: "", expected: "certificate", status: this.md(p.a, d).tls ? "ok" : "warn", hint: this.md(p.a, d).tls ? "mail." + d + " has its own certificate." : "Mail apps will warn about the certificate. Get one for mail." + d + "." },
      ];
    }
    if ((p = is("POST", "/accounts/:a/mail/domains/:d/tls"))) {
      const d = this.md(p.a, p.d);
      return this.h.job("mail-tls", `Certificate for mail.${d.domain}`, [`mail.${d.domain} points to ${IP}`, "Asking Let's Encrypt for a certificate", "Certificate issued", "Postfix and Dovecot reloaded"], () => { d.tls = true; });
    }
    if (is("POST", "/accounts/:a/mail/domains/:d/dkim/rotate") || is("POST", "/accounts/:a/mail/domains/:d/usage")) { const x = seg[4]; return this.md(seg[1], x); }
    if (is("GET", "/mail/queue") || (p = is("GET", "/accounts/:a/mail/queue"))) return [
      { id: "4BZp1q2Xyz", queue: "deferred", arrival: ago(95), size: 18210, sender: "orders@naijabites.ng", recipients: [{ address: "customer@yahoo.com", delay_reason: "host mta5.am0.yahoodns.net said: 421 4.7.0 [TSS04] Messages temporarily deferred due to unexpected volume" }] },
      { id: "5CAq2r3Abc", queue: "deferred", arrival: ago(30), size: 4096, sender: "tunde@lagosrentals.com.ng", recipients: [{ address: "agent@estatehub.ng", delay_reason: "connect to mail.estatehub.ng[41.58.12.3]:25: Connection timed out" }] },
    ];
    if (is("POST", "/mail/queue/:action") || is("POST", "/accounts/:a/mail/queue/:action")) return { action: seg[seg.length - 1] };
    if (is("GET", "/mail/log") || is("GET", "/accounts/:a/mail/log")) {
      const rows = [
        { time: "Nov  6 15:02:11", queue_id: "8FA21C0", from: "chidi@naijabites.ng", to: "ada@gmail.com", status: "sent", relay: "smtp-relay.brevo.com[1.179.112.10]:587", detail: "250 OK id=1t8Xyz-0003" },
        { time: "Nov  6 14:58:40", queue_id: "7E10B33", from: "noreply@naijabites.ng", to: "customer@yahoo.com", status: "deferred", relay: "mta5.am0.yahoodns.net", detail: "421 Messages temporarily deferred" },
        { time: "Nov  6 14:41:02", queue_id: "6D02A11", to: "kitchen@naijabites.ng", from: "uber@uber.com", status: "sent", relay: "dovecot", detail: "delivered to mailbox" },
        { time: "Nov  6 13:12:55", queue_id: "5C9F003", to: "old@lagosrentals.com.ng", from: "promo@spammy.biz", status: "rejected", detail: "554 5.7.1 Spam message rejected (score 18.2)" },
        { time: "Nov  6 12:00:03", queue_id: "4B88E20", from: "tunde@lagosrentals.com.ng", to: "typo@gmial.com", status: "bounced", detail: "Host or domain name not found" },
      ];
      const s = (q.get("search") ?? "").toLowerCase();
      return rows.filter((r) => !s || JSON.stringify(r).toLowerCase().includes(s));
    }

    // ---- security
    if ((p = is("GET", "/accounts/:a/malware/scans"))) return this.scans.filter((s) => s.account === p!.a);
    if ((p = is("POST", "/accounts/:a/malware/scans"))) {
      const a = p.a;
      return this.h.job("malware-scan", `Malware scan of ${a}`, ["Listing files", "ClamAV: checking 4,812 files", "Checking PHP files for backdoors", "Scan finished: nothing found"],
        () => { this.scans.unshift({ id: hex(4), account: a, path: b.path || "", status: "done", started_at: ago(1), finished_at: now(), files: 4812, clamav: true, findings: [] }); });
    }
    if ((p = is("POST", "/accounts/:a/malware/scans/:id/quarantine"))) {
      const s = this.scans.find((x) => x.id === p!.id) ?? nf("Scan");
      for (const f of s!.findings) if (!f.quarantined) { f.quarantined = hex(4); this.quarantine.push({ id: f.quarantined, account: p.a, original: f.path, signature: f.signature, size: f.size, at: now() }); }
      return s;
    }
    if ((p = is("GET", "/accounts/:a/quarantine"))) return this.quarantine.filter((x) => x.account === p!.a);
    if ((p = is("POST", "/accounts/:a/quarantine/:id/restore")) || (p = is("DELETE", "/accounts/:a/quarantine/:id"))) { this.quarantine = this.quarantine.filter((x) => x.id !== p!.id); return {}; }
    if (is("GET", "/security/malware")) return { schedule: this.schedule, reports: this.scans, quarantine: this.quarantine, clamav: true };
    if (is("PUT", "/security/malware/schedule")) { Object.assign(this.schedule, b); return this.schedule; }
    if (is("GET", "/secrets")) return { key_id: "9f3c2ab1", keys: 1, stores: ["dns", "mail", "storage", "backups", "cluster"], rotated_at: ago(40 * day) };
    if (is("POST", "/secrets/rotate")) return { key_id: hex(4), keys: 1, stores: ["dns", "mail", "storage", "backups", "cluster"], rotated_at: now() };
    if (is("GET", "/security/waf")) return { installed: true, profiles: ["wordpress", "drupal", "nextcloud", "dokuwiki", "cpanel", "xenforo", "phpbb", "phpmyadmin"] };
    if ((p = is("PUT", "/sites/:d/waf"))) { const s = this.h.sites().find((x) => x.domain === p!.d); return { ...s, waf: b }; }
    if ((p = is("GET", "/sites/:d/waf/events"))) return [
      { time: "06/Nov/2026:15:01:22", client: "185.220.101.44", rule_id: "942100", message: "SQL Injection Attack Detected via libinjection", uri: "/product.php?id=1' OR '1'='1", blocked: true },
      { time: "06/Nov/2026:14:44:09", client: "45.155.205.233", rule_id: "930120", message: "OS File Access Attempt", uri: "/index.php?file=../../../../etc/passwd", blocked: true },
      { time: "06/Nov/2026:11:20:51", client: "102.89.34.12", rule_id: "941160", message: "NoScript XSS InjectionChecker: HTML Injection", uri: "/wp-admin/post.php", blocked: false },
    ];

    // ---- databases
    if ((p = is("GET", "/accounts/:a/dbs"))) return { engines: { mariadb: true, postgres: true, redis: "valkey", mongodb: true }, mariadb: this.h.mariadb(p.a), databases: this.dbs[p.a] ?? [], redis: this.redis[p.a] ?? null };
    if ((p = is("POST", "/accounts/:a/dbs"))) {
      if (!/^[a-z0-9]{1,16}$/.test(b.name)) bad("name must be 1-16 lowercase letters or digits");
      const name = `${p.a}_${b.name}`; (this.dbs[p.a] ??= []).push({ engine: b.engine, name, owner: name, note: b.note, created_at: now() });
      const port = b.engine === "postgres" ? 5432 : 27017; const pass = pw() + pw();
      return { engine: b.engine, database: name, user: name, password: pass, host: "127.0.0.1", port, url: `${b.engine === "postgres" ? "postgresql" : "mongodb"}://${name}:${pass}@127.0.0.1:${port}/${name}` };
    }
    if (is("POST", "/accounts/:a/dbs/sizes")) return {};
    if ((p = is("DELETE", "/accounts/:a/dbs/:e/:db"))) { this.dbs[p.a] = (this.dbs[p.a] ?? []).filter((d) => d.name !== p!.db); return {}; }
    if ((p = is("POST", "/accounts/:a/dbs/:e/:db/password"))) { const pass = pw() + pw(); return { engine: p.e, database: p.db, user: p.db, password: pass, host: "127.0.0.1", url: `${p.e}://${p.db}:${pass}@127.0.0.1/${p.db}` }; }
    if ((p = is("POST", "/accounts/:a/dbs/:e/:db/roles"))) {
      const d = (this.dbs[p.a] ?? []).find((x) => x.name === p!.db); const user = `${p.a}_${b.name}`; d?.roles?.push({ name: user, access: b.access }) ?? (d && (d.roles = [{ name: user, access: b.access }]));
      const pass = pw() + pw(); return { engine: p.e, database: p.db, user, password: pass, host: "127.0.0.1", url: `${p.e}://${user}:${pass}@127.0.0.1/${p.db}` };
    }
    if ((p = is("POST", "/accounts/:a/dbs/:e/:db/import"))) return { imported: true };
    if ((p = is("POST", "/accounts/:a/dbs/:e/:db/diagnose"))) return [
      { title: "The database exists", severity: "ok", detail: `${p.db} is on this server.` },
      { title: "The user exists and can use it", severity: "ok", detail: `${b.user} has access to ${p.db}.` },
      b.password ? { title: "The password is wrong", severity: "bad", detail: "The server refused the password you entered.", fix: "Set a new password from the key button, then put it in your app's config (.env or wp-config.php)." }
        : { title: "Password not checked", severity: "info", detail: "Enter the password your app uses to test it." },
      { title: "Connect to localhost, not an IP", severity: "info", detail: "Apps on this server should use host localhost (MariaDB) or 127.0.0.1 (PostgreSQL, MongoDB)." },
    ];
    if (is("GET", "/accounts/:a/dbs/processes")) return [
      { id: 812, user: "naijabites_wp", host: "localhost", db: "naijabites_wp", command: "Query", seconds: 14, state: "Sending data", query: "SELECT * FROM wp_postmeta WHERE meta_key = '_price' ORDER BY meta_value+0 DESC" },
      { id: 820, user: "naijabites_wp", host: "localhost", db: "naijabites_wp", command: "Sleep", seconds: 2, state: "", query: "" },
    ];
    if (is("POST", "/accounts/:a/dbs/processes/:id/kill")) return {};
    if (is("GET", "/accounts/:a/dbs/slow-queries")) return [
      { time: "2026-11-06T14:02:11", user: "naijabites_wp", db: "naijabites_wp", query_secs: 6.41, lock_secs: 0, rows_sent: 20, rows_examined: 1_842_003, query: "SELECT wp_posts.* FROM wp_posts INNER JOIN wp_postmeta ON … WHERE wp_postmeta.meta_key = '_stock_status' …" },
      { time: "2026-11-06T09:44:50", user: "lagosrentals_app", db: "lagosrentals_app", query_secs: 2.03, lock_secs: 0.1, rows_sent: 1, rows_examined: 210_442, query: "SELECT COUNT(*) FROM listings WHERE LOWER(area) LIKE '%lekki%'" },
    ];
    if ((p = is("GET", "/accounts/:a/redis"))) { if (!this.redis[p.a]) nf("Redis"); return { running: true, used_mb: 41.8, keys: 18204, clients: 6, hit_rate: 93.4, uptime_hours: 118.2, version: "8.0.2" }; }
    if ((p = is("PUT", "/accounts/:a/redis"))) {
      const first = !this.redis[p.a];
      this.redis[p.a] = { socket: `/var/lib/ike/data/${p.a}/redis/redis.sock`, memory_mb: b.memory_mb, policy: b.policy, persist: b.persist, created_at: this.redis[p.a]?.created_at ?? now() };
      const pass = pw() + pw();
      return { redis: this.redis[p.a], credentials: first || b.new_password ? { engine: "redis", database: "0", user: "default", password: pass, host: this.redis[p.a].socket, url: `unix://:${pass}@${this.redis[p.a].socket}` } : undefined };
    }
    if ((p = is("DELETE", "/accounts/:a/redis"))) { delete this.redis[p.a]; return {}; }
    if (is("POST", "/accounts/:a/redis/flush")) return {};
    if (is("GET", "/databases/replication")) return { configured: true, io_running: true, sql_running: true, seconds_behind: 0, primary_host: "10.20.0.5", gtid: "0-3-981244", read_only: true, binary_logging: true };
    if (is("POST", "/databases/replication/source")) return { user: "ike_repl", password: pw() + pw(), server_id: b.server_id, restart_needed: false };
    if (is("POST", "/databases/replication/replica")) return this.h.job("replica", "Start replicating", ["Copying databases from " + b.primary_host, "Imported 4 databases (1.2 GB)", "Replication started at GTID 0-3-981244"]);
    if (is("POST", "/databases/replication/stop")) return {};
    if (is("GET", "/databases/slowlog")) return { enabled: true, long_query_secs: 2, log_queries_not_using_indexes: false };
    if (is("PUT", "/databases/slowlog")) return b;

    // ---- files
    if ((p = is("GET", "/accounts/:a/files"))) return this.files[`${p.a}:${q.get("path") ?? ""}`] ?? [];
    if ((p = is("GET", "/accounts/:a/files/content"))) {
      const k = `${p.a}:${q.get("path")}`;
      return { content: this.fileText[k] ?? (q.get("path")?.endsWith(".php") ? "<?php\n// " + q.get("path") + "\nrequire __DIR__ . '/wp-blog-header.php';\n" : "") };
    }
    if ((p = is("PUT", "/accounts/:a/files/content"))) {
      const path = q.get("path")!; this.fileText[`${p.a}:${path}`] = b.content;
      const dir = path.split("/").slice(0, -1).join("/"); const name = path.split("/").pop()!;
      const list = (this.files[`${p.a}:${dir}`] ??= []);
      if (!list.some((f) => f.name === name)) list.push({ name, type: "file", size: String(b.content).length, modified: now(), mode: "0644" });
      return {};
    }
    if ((p = is("PUT", "/accounts/:a/files/upload"))) {
      const path = q.get("path")!; const dir = path.split("/").slice(0, -1).join("/"); const name = path.split("/").pop()!;
      const list = (this.files[`${p.a}:${dir}`] ??= []);
      list.splice(list.findIndex((f) => f.name === name) >>> 0, list.some((f) => f.name === name) ? 1 : 0);
      list.push({ name, type: "file", size: b.size ?? 0, modified: now(), mode: "0644" }); return {};
    }
    if ((p = is("POST", "/accounts/:a/files/:op"))) {
      const a = p.a; const dirOf = (x: string) => x.split("/").slice(0, -1).join("/"); const base = (x: string) => x.split("/").pop()!;
      const listOf = (d: string) => (this.files[`${a}:${d}`] ??= []);
      switch (p.op) {
        case "mkdir": listOf(dirOf(b.path)).push({ name: base(b.path), type: "dir", size: 0, modified: now(), mode: "0755" }); this.files[`${a}:${b.path}`] = []; break;
        case "delete": for (const x of b.paths as string[]) { const l = listOf(dirOf(x)); this.files[`${a}:${dirOf(x)}`] = l.filter((f) => f.name !== base(x)); } break;
        case "move": if (b.rename) { const l = listOf(dirOf(b.from[0])); const f = l.find((x) => x.name === base(b.from[0])); if (f) f.name = base(b.to); } break;
        case "archive": listOf(b.dir).push({ name: b.name, type: "file", size: 1_204_331, modified: now(), mode: "0644" }); break;
      }
      return { done: p.op };
    }
    if ((p = is("GET", "/accounts/:a/files/search"))) {
      const n = (q.get("name") ?? "").toLowerCase(); const out: { path: string; type: string; size: number }[] = [];
      for (const [k, list] of Object.entries(this.files)) {
        const [acct, dir] = k.split(":"); if (acct !== p.a) continue;
        for (const f of list) if (f.name.toLowerCase().includes(n)) out.push({ path: dir ? `${dir}/${f.name}` : f.name, type: f.type, size: f.size });
      }
      return out.slice(0, 200);
    }
    if ((p = is("GET", "/accounts/:a/files/sizes"))) {
      const dir = q.get("path") ?? "";
      return (this.files[`${p.a}:${dir}`] ?? []).filter((f) => f.type === "dir").map((f, i) => ({ path: dir ? `${dir}/${f.name}` : f.name, bytes: [812e6, 2.4e9, 41e6, 160e6][i % 4] }));
    }
    if ((p = is("GET", "/accounts/:a/snapshots"))) return [{ id: "s3", account: p.a, time: ago(60) }, { id: "s2", account: p.a, time: ago(day + 60) }, { id: "s1", account: p.a, time: ago(7 * day) }];
    if ((p = is("POST", "/accounts/:a/snapshots"))) return this.h.job("snapshot", `Snapshot of ${p.a}`, ["Comparing with the last snapshot", "Copied 312 changed files (48 MB)", "Kept the 10 newest snapshots"]);
    if ((p = is("GET", "/accounts/:a/snapshots/:id/files"))) return this.files[`${p.a}:${q.get("path") ?? ""}`] ?? [];
    if (is("POST", "/accounts/:a/snapshots/:id/restore") || is("DELETE", "/accounts/:a/snapshots/:id")) return {};
    if ((p = is("GET", "/accounts/:a/shares"))) return this.shares.filter((s) => s.account === p!.a);
    if ((p = is("POST", "/accounts/:a/shares"))) {
      if (!b.path) bad("Choose a file or folder to share.");
      const t = hex(16); const s: Share = { token: t, account: p.a, path: b.path, folder: !/\.[a-z0-9]+$/i.test(b.path), expires_at: new Date(Date.now() + (b.expires_hours || 72) * 3.6e6).toISOString(),
        max_downloads: b.max_downloads || 0, downloads: 0, created_at: now(), has_password: !!b.password, url_path: `/s/${t}` };
      this.shares.push(s); return s;
    }
    if ((p = is("DELETE", "/accounts/:a/shares/:t"))) { this.shares = this.shares.filter((s) => s.token !== p!.t); return {}; }
    if (is("GET", "/storage") || is("GET", "/accounts/:a/storage")) return this.storage.filter((t) => (seg[0] === "storage" ? !t.account : t.account === seg[1]));
    if (is("POST", "/storage") || is("POST", "/accounts/:a/storage")) { if (!b.name) bad("Give the storage a name."); const t = { ...b, id: hex(4), created_at: now(), account: seg[0] === "accounts" ? seg[1] : undefined, secret_key: undefined } as StorageTarget; this.storage.push(t); return t; }
    if (is("PUT", "/storage/:id") || is("PUT", "/accounts/:a/storage/:id")) { const id = seg[seg.length - 1]; const i = this.storage.findIndex((t) => t.id === id); this.storage[i] = { ...this.storage[i], ...b, secret_key: undefined }; return this.storage[i]; }
    if (is("DELETE", "/storage/:id") || is("DELETE", "/accounts/:a/storage/:id")) { const id = seg[seg.length - 1]; this.storage = this.storage.filter((t) => t.id !== id); return {}; }
    if (is("POST", "/storage/:id/check") || is("POST", "/accounts/:a/storage/:id/check")) { const id = seg[seg.length - 2]; const t = this.storage.find((x) => x.id === id); if (t) t.last_check = "ok " + now(); return { ok: true }; }

    // ---- backups
    const planBase = seg[0] === "backups" && seg[1] === "plans" ? 2 : seg[0] === "accounts" && seg[2] === "backups" && seg[3] === "plans" ? 4 : -1;
    if (planBase > 0) {
      const acct = planBase === 4 ? seg[1] : "";
      const rest = seg.slice(planBase);
      const mine = () => this.plans.filter((x) => (acct ? x.account === acct && x.scope !== "server" : !x.account || x.scope === "server"));
      if (M === "GET" && rest.length === 0) return mine();
      if (M === "POST" && rest.length === 0) {
        if (!b.name) bad("Give the plan a name.");
        const x = { ...(b as BackupPlan), id: hex(4), account: acct || b.account, created_at: now() }; delete (x as { recovery_password?: string }).recovery_password;
        this.plans.push(x); return { ...x, recovery_key: `ike-recovery:v1:${btoa(`b2:megamind-ike/vps-lagos-1/${x.id}`)}:${pw()}${pw()}${pw()}` };
      }
      const plan = this.plans.find((x) => x.id === rest[0]) ?? nf("Backup plan");
      if (M === "PUT" && rest.length === 1) { Object.assign(plan!, b); return plan; }
      if (M === "DELETE" && rest.length === 1) { this.plans = this.plans.filter((x) => x !== plan); return {}; }
      if (M === "POST" && rest[1] === "run") return this.h.job("backup", `Back up ${plan!.name}`, ["Dumping databases", "Scanning 412,003 files", "Uploading 218 changed files (41.2 MB)", "Snapshot 7d1e2c9a saved", "Removed 2 old snapshots"],
        () => { plan!.last = { status: "ok", started_at: ago(1), finished_at: now(), added_bytes: 41.2e6, total_bytes: 38.5e9, snapshot: "7d1e2c9a" }; this.pushEvent("backup.finished", plan!.name); });
      if (M === "POST" && rest[1] === "check") return this.h.job("backup-check", `Check ${plan!.name}`, ["Checking the repository structure", "Reading 10% of the data back", "No errors found"], () => { plan!.last_check = { status: "ok", started_at: now() }; });
      if (M === "GET" && rest[1] === "snapshots" && rest.length === 2) return [0, 1, 2, 7, 14, 30].map((d, i) => ({ id: hex(4), time: ago(d * day + 8 * 60), paths: ["/home", "/var/lib/ike"], host: "vps-lagos-1", tags: [plan!.scope], size: 38.4e9 - i * 4e8 }));
      if (M === "GET" && rest[1] === "snapshots" && rest[3] === "browse") {
        const path = q.get("path") || "/";
        if (path === "/") return [{ name: "home", path: "/home", type: "dir", size: 0, mtime: ago(60) }, { name: "databases", path: "/databases", type: "dir", size: 0, mtime: ago(60) }];
        if (path === "/home") return this.h.accounts().map((a) => ({ name: a, path: `/home/${a}`, type: "dir", size: 0, mtime: ago(60) }));
        if (path === "/databases") return this.h.accounts().flatMap((a) => this.h.mariadb(a).map((d) => ({ name: `mariadb-${d}.sql.zst`, path: `/databases/mariadb-${d}.sql.zst`, type: "file", size: 81e6, mtime: ago(60) })));
        return [{ name: "web", path: `${path}/web`, type: "dir", size: 0, mtime: ago(60) }, { name: ".bashrc", path: `${path}/.bashrc`, type: "file", size: 3771, mtime: ago(9000) }];
      }
      if (M === "POST" && rest[1] === "restore") return this.h.job("restore", `Restore from ${plan!.name}`, ["Downloading the selected files", "Restored 1,204 files", "Imported 2 databases", "Fixed ownership and permissions"]);
      if (M === "GET" && rest[1] === "recovery-key") return { plan: plan!.name, repository: `s3:https://s3.eu-central-003.backblazeb2.com/megamind-ike/ike/vps-lagos-1/${plan!.id}`, password: pw() + pw() + pw() };
    }
    if (is("POST", "/backups/recover")) return this.h.job("recover", "Restore this server from a backup", ["Opening the repository", "Downloading snapshot latest (38.4 GB)", "Restoring accounts and websites", "Importing databases", "Rebuilding web server, mail and DNS configs", "Done. Point DNS at this server."]);

    // ---- infrastructure
    if (is("GET", "/servers")) return this.servers;
    if (is("POST", "/servers/join-token")) {
      if (!b.name) bad("Give the server a short name (and group).");
      const t = hex(24); const fpr = "sha256:" + hex(32);
      return { token: t, fingerprint: fpr, expires: new Date(Date.now() + 7.2e6).toISOString(), panel_url: "https://102.89.40.17:2087",
        command: `sudo ike-agent join https://102.89.40.17:2087 ${t} ${fpr}`,
        install: `curl -fsSL https://ike.megamindtechnologies.com/installer.sh | sudo bash -s -- --join https://102.89.40.17:2087 --join-token ${t} --join-fingerprint ${fpr}` };
    }
    if ((p = is("PATCH", "/servers/:id"))) { const s = this.servers.find((x) => x.id === p!.id); Object.assign(s!, b); return s; }
    if ((p = is("DELETE", "/servers/:id"))) { this.servers = this.servers.filter((x) => x.id !== p!.id); return {}; }
    if (is("GET", "/clusters")) return this.clusters;
    if (is("POST", "/clusters")) {
      if (!b.name || !b.replicas?.length) bad("Name the cluster and choose at least one copy.");
      const c = { id: hex(4), name: b.name, engine: b.engine, primary: b.primary, replicas: b.replicas, created_at: now(), members: [{ server: b.primary, role: "primary" }, ...b.replicas.map((r: string) => ({ server: r, role: "replica" }))] };
      this.clusters.push(c); return c;
    }
    if ((p = is("POST", "/clusters/:id/promote"))) {
      const c = this.clusters.find((x) => x.id === p!.id)!; const old = c.primary; c.primary = b.server; c.replicas = [...c.replicas.filter((r) => r !== b.server), old];
      c.members = [{ server: c.primary, role: "primary" }, ...c.replicas.map((r) => ({ server: r, role: "replica" }))]; this.pushEvent("cluster.failover", c.name, {}, "warn"); return c;
    }
    if ((p = is("DELETE", "/clusters/:id"))) { this.clusters = this.clusters.filter((x) => x.id !== p!.id); return {}; }
    if (is("GET", "/inventory")) return {
      hostname: "vps-lagos-1", os: "Ubuntu 24.04.3 LTS", kernel: "6.8.0-50-generic", virtualization: "kvm", vendor: "QEMU", product: "Standard PC (Q35 + ICH9, 2009)", cpu_model: "AMD EPYC 7543P 32-Core Processor",
      cpus: 4, memory_mb: 8192, swap_mb: 2048, uptime_hours: 23.4 * 24, booted_at: ago(23.4 * day), collected_at: ago(2),
      disks: [{ name: "vda", size_gb: 160, type: "disk", rotational: false, model: "QEMU HARDDISK" }, { name: "vda1", size_gb: 159, type: "part", mount: "/", filesystem: "ext4", rotational: false },
        { name: "vdb", size_gb: 500, type: "disk", mount: "/mnt/backup", filesystem: "xfs", rotational: true }],
      interfaces: [{ name: "eth0", mac: "52:54:00:12:34:56", state: "UP", mtu: 1500, addresses: [`${IP}/24`, "2c0f:f4c0:1::17/64"] }, { name: "eth1", state: "UP", mtu: 1450, addresses: ["10.20.0.2/24"] }],
      asset: { tag: "MMT-0042", owner: "Megamind Technologies", location: "Lagos (LOS1)", provider: "Hetzner / Rack Centre", purchase_date: "2025-03-01", renewal_date: "2026-12-01", monthly_cost: "₦48,000", notes: "Primary web server" },
    };
    if (is("PUT", "/inventory/asset")) return b;
    if (is("GET", "/maintenance")) return this.windows;
    if (is("POST", "/maintenance")) { const w = { ...(b as MaintenanceWindow), id: hex(4), state: "scheduled" }; this.windows.push(w); return w; }
    if ((p = is("DELETE", "/maintenance/:id"))) { const w = this.windows.find((x) => x.id === p!.id); if (w) w.state = "cancelled"; return {}; }
    if (is("GET", "/webhooks")) return this.hooks;
    if (is("GET", "/webhooks/deliveries")) return this.hooks.length ? [{ webhook: this.hooks[0].id, event: 107, type: "site.created", status: 200, at: ago(4), attempt: 1 }] : [];
    if (is("POST", "/webhooks")) { if (!String(b.url).startsWith("https://")) bad("Webhook URLs must use HTTPS."); const w = { ...(b as Webhook), id: hex(4), created_at: now() }; this.hooks.push(w); return { ...w, secret: hex(24) }; }
    if ((p = is("PUT", "/webhooks/:id"))) { const w = this.hooks.find((x) => x.id === p!.id); Object.assign(w!, b); return w; }
    if ((p = is("DELETE", "/webhooks/:id"))) { this.hooks = this.hooks.filter((x) => x.id !== p!.id); return {}; }
    if (is("POST", "/webhooks/:id/test")) return {};
    if (is("POST", "/spec/plan")) {
      const s = (b.spec ?? {}) as Record<string, { name?: string; domain?: string }[]>; const out: { action: string; kind: string; name: string; detail?: string }[] = [];
      const has = (k: string, n: string) => (k === "account" ? this.h.accounts().includes(n) : k === "site" ? this.h.sites().some((x) => x.domain === n) : k === "dns-zone" ? this.zones.some((z) => z.name === n) : false);
      for (const [key, kind] of [["accounts", "account"], ["sites", "site"], ["databases", "database"], ["dns", "dns-zone"], ["mail", "mail-domain"], ["cron", "cron"]] as const)
        for (const it of s[key] ?? []) { const n = it.name ?? it.domain ?? ""; out.push({ action: has(kind, n) ? "none" : "create", kind, name: n }); }
      return out.filter((c) => c.action !== "none");
    }
    if (is("POST", "/spec/apply")) return this.h.job("spec-apply", "Apply server spec", ["Planning changes", "Created account shop", "Created website shop.ng", "Created database shop_store", "Created DNS zone shop.ng", "Done: 4 changes"]);
    if (is("GET", "/spec/export")) return { accounts: this.h.accounts().map((name) => ({ name })), sites: this.h.sites().map((s) => ({ domain: s.domain, account: s.account, runtime: s.runtime })),
      dns: this.zones.map((z) => ({ account: z.account, name: z.name })) };
    if (is("GET", "/spec/templates")) return this.specTemplates;
    if ((p = is("PUT", "/spec/templates/:n"))) { this.specTemplates = [...this.specTemplates.filter((t) => t.name !== p!.n), { name: p.n, description: b.description ?? "", spec: b.spec, variables: [] }]; return {}; }
    if ((p = is("DELETE", "/spec/templates/:n"))) { this.specTemplates = this.specTemplates.filter((t) => t.name !== p!.n); return {}; }

    // ---- cPanel migration
    if ((p = is("PUT", "/imports/cpanel/:f/upload"))) return demoPlan(p.f);
    if ((p = is("POST", "/imports/cpanel/:f/restore"))) {
      const acct = b.account || "tundeprops";
      return this.h.job("cpanel-restore", `Restore ${p.f}`, [`Reading ${p.f} (cPanel user tundeprops)`, `Creating account ${acct}`, "Website tundeprops.com created", "Website listings.tundeprops.com created",
        "Copying website files", "Copied 1,204.6 MB of files", "Database tundeprops_wp → " + acct + "_wp", "Importing " + acct + "_wp (82.4 MB)", `/home/${acct}/web/tundeprops.com/public_html/wp-config.php now uses ${acct}_wp`,
        "Email for tundeprops.com: 3 mailboxes (passwords kept)", "Imported 18,402 email messages", "Added 2 cron jobs", "DNS zone tundeprops.com imported", "Fixing ownership and permissions",
        `Database ${acct}_wp (was tundeprops_wp): user ${acct}_wp, password ${pw()}${pw()}`,
        `Restored 2 websites, 1 databases, 3 mailboxes, 2 forwarders, 2 cron jobs and 1 DNS zones into ${acct}`]);
    }

    throw new Error(`The preview doesn't answer ${M} ${path} yet.`);
  }

  private zone(a: string, name: string) {
    const z = this.zones.find((x) => x.name === name && (x.account === a || !a));
    if (!z) nf(`DNS zone ${name}`);
    return z!;
  }
  private bump(z: DnsZone) { z.serial += 1; z.updated_at = now(); return { ...z, records: [...z.records] }; }
  private md(a: string, d: string) {
    const m = this.mail.find((x) => x.domain === d && x.account === a);
    if (!m) nf(`Email domain ${d}`);
    return m!;
  }
}

const dnsTypes = [
  ["A", "Points a name at an IPv4 address"], ["AAAA", "Points a name at an IPv6 address"], ["CNAME", "Makes a name an alias of another name"],
  ["MX", "Mail server for the domain, with a priority (lower is tried first)"], ["TXT", "Free text: SPF, DKIM, DMARC and site verification"],
  ["NS", "Delegates a subdomain to other name servers"], ["SRV", "Service location: priority, weight, port and target"], ["CAA", "Which certificate authorities may issue certificates"],
  ["PTR", "Reverse DNS (in reverse zones only)"], ["TLSA", "DANE certificate pin for a service"], ["SSHFP", "SSH host key fingerprint"], ["DS", "DNSSEC delegation for a child zone"],
  ["NAPTR", "Name authority pointer (telephony, SIP)"],
].map(([type, help]) => ({ type, help }));

const dnsTemplates = [
  { id: "website", name: "Website", help: "Points the domain and www at this server, and lets Let's Encrypt issue certificates.", mail: false },
  { id: "website-email", name: "Website and email on this server", help: "Website records plus MX, SPF, DMARC and mail client discovery for this server's mail service.", mail: true },
  { id: "google", name: "Google Workspace", help: "Mail goes to Gmail. Add the verification TXT record Google gives you as well.", mail: true },
  { id: "microsoft", name: "Microsoft 365", help: "Mail goes to Exchange Online, plus Teams and Outlook discovery records.", mail: true },
  { id: "zoho", name: "Zoho Mail", help: "Mail goes to Zoho. Add the verification and DKIM records from the Zoho console as well.", mail: true },
];

function tmplRecords(id: string, zone: string): DnsRecord[] {
  const web = [rec("@", "A", IP), rec("@", "AAAA", "2c0f:f4c0:1::17"), rec("www", "CNAME", "@"), rec("@", "CAA", '0 issue "letsencrypt.org"')];
  const dmarc = rec("_dmarc", "TXT", `v=DMARC1; p=quarantine; rua=mailto:postmaster@${zone}`);
  switch (id) {
    case "website": return web;
    case "website-email": return [...web, rec("mail", "A", IP), rec("@", "MX", "mail", { priority: 10 }), rec("@", "TXT", `v=spf1 a mx ip4:${IP} ~all`), dmarc, rec("autoconfig", "CNAME", "mail")];
    case "google": return [rec("@", "MX", "smtp.google.com.", { priority: 1 }), rec("@", "TXT", "v=spf1 include:_spf.google.com ~all"), dmarc];
    case "microsoft": return [rec("@", "MX", zone.replace(/\./g, "-") + ".mail.protection.outlook.com.", { priority: 0 }), rec("@", "TXT", "v=spf1 include:spf.protection.outlook.com -all"), rec("autodiscover", "CNAME", "autodiscover.outlook.com."), dmarc];
    case "zoho": return [rec("@", "MX", "mx.zoho.com.", { priority: 10 }), rec("@", "MX", "mx2.zoho.com.", { priority: 20 }), rec("@", "MX", "mx3.zoho.com.", { priority: 50 }), rec("@", "TXT", "v=spf1 include:zoho.com ~all"), dmarc];
  }
  return [];
}

function checkRecord(r: DnsRecord, z: DnsZone, id = "") {
  const t = (r.type || "").toUpperCase();
  if (t === "A" && !/^\d{1,3}(\.\d{1,3}){3}$/.test(r.content)) bad(`"${r.content}" is not an IPv4 address`);
  if (t === "AAAA" && !r.content.includes(":")) bad(`"${r.content}" is not an IPv6 address`);
  if (t === "CNAME" && (r.name === "@" || !r.name)) bad("the domain itself can't be a CNAME; use an A record (or a provider that flattens CNAMEs)");
  if (!r.content) bad("Enter a value.");
  const name = r.name || "@";
  if (t === "CNAME" && z.records.some((o) => o.name === name && o.id !== id)) bad(`${name} has a CNAME and other records; a CNAME must be the only record for its name`);
  if (t !== "CNAME" && z.records.some((o) => o.name === name && o.type === "CNAME" && o.id !== id)) bad(`${name} has a CNAME and other records; a CNAME must be the only record for its name`);
}

function demoPlan(file: string) {
  return { file, cpanel_user: "tundeprops", account: "tundeprops", main_domain: "tundeprops.com", databases: ["tundeprops_wp"], mailboxes: ["info@tundeprops.com", "tunde@tundeprops.com", "sales@tundeprops.com"],
    cron_jobs: 2, home_size_mb: 1204.6, warnings: [], websites: [{ domain: "tundeprops.com", kind: "main", php: "8.1", php_installed: true }, { domain: "listings.tundeprops.com", kind: "subdomain", php_installed: true }, { domain: "tundeprops.ng", kind: "alias", php_installed: true }] };
}
