import { TwoFactorRequired } from "./errors";
import type {
  Account, AccountUsage, AlertEvent, AlertSettings, Api, AuditEntry, AutoUpdates, Ban, BillingSettings, CatalogApp,
  CreateSiteRequest, CronJob, Database, Diagnosis, DnsCheck, Drift, EnvVar, FirewallBlock, FirewallPort, Forwarder,
  HealthCheck, IkeRelease, ImportPlan, Invoice, Job, Limits, LogKind, MailDomain, Mailbox, NewDatabase, PaymentProvider,
  Person, Role, Runtimes, SSHKey, SSHMode, SecurityReport, ServiceStatus, Site, SiteStatus, SmtpSettings, SystemInfo,
  UpdateStatus, WebServer, WordPressResult, Redirect, PHPSettings, SSLSettings, CertInfo, PHPExtension, DeployInfo, DeploySetup,
  Deployment, QueueStatus, Container, ContainerSpec, StackInfo, DockerOverview, ImageUpdate, KubeStatus, KubeWorkloads, PersonCreate } from "./types";
import { isApp as isAppRt, isProxied } from "./runtime";
import { Demo5 } from "./demo5";

// Same rules as agent/internal/validate, so demo errors match the real server.
const domainLabel = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const tld = /^([a-z]{2,63}|xn--[a-z0-9-]{1,59})$/;
const usernameRe = /^[a-z][a-z0-9]{2,15}$/;
const reserved = new Set(["root", "admin", "nginx", "mysql", "www", "ike", "ubuntu", "mail", "ftp", "daemon", "nobody"]);

export function domainError(d: string): string | null {
  if (d.length < 4 || d.length > 253) return "Domain must be 4–253 characters.";
  if (d !== d.toLowerCase()) return "Use lowercase letters only.";
  const labels = d.split(".");
  if (labels.length < 2) return "Include the extension, e.g. example.com.";
  if (!labels.every((l) => domainLabel.test(l))) return "Use letters, numbers, dots and hyphens only.";
  if (!tld.test(labels[labels.length - 1])) return "The extension doesn't look right.";
  return null;
}

export function usernameError(u: string): string | null {
  if (!usernameRe.test(u)) return "3–16 lowercase letters or numbers, starting with a letter.";
  if (reserved.has(u)) return `“${u}” is reserved by the system.`;
  return null;
}

/** Suggest an account name from a domain: shop.adaeze.com → adaeze */
export function suggestUser(domain: string): string {
  const parts = domain.toLowerCase().split(".").filter(Boolean);
  parts.pop();
  if (parts.length > 1 && ["com", "co", "org", "net", "edu", "gov", "ac", "sch"].includes(parts[parts.length - 1])) parts.pop();
  let u = (parts[parts.length - 1] || "").replace(/[^a-z0-9]/g, "");
  if (!u) return "";
  if (!/^[a-z]/.test(u)) u = "s" + u;
  return u.slice(0, 16);
}

const wait = (ms = 300) => new Promise((r) => setTimeout(r, ms + Math.random() * 200));
const now = () => new Date().toISOString();
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();
const day = 60 * 24;
const hex = (n: number) => Array.from(crypto.getRandomValues(new Uint8Array(n)), (b) => b.toString(16).padStart(2, "0")).join("");
const fp = () => "SHA256:" + btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32)))).replace(/=+$/, "");

function docRoot(s: { account: string; domain: string; runtime: string }) {
  const base = `/home/${s.account}/web/${s.domain}`;
  if (s.runtime === "proxy" || s.runtime === "docker") return base;
  return isAppRt(s.runtime as never) ? `${base}/app` : `${base}/public_html`;
}

export class DemoApi implements Api {
  private runtimes_: Runtimes = {
    php: ["5.6", "7.4", "8.1", "8.2", "8.3", "8.4", "8.5"],
    node: ["20", "22", "24"],
    python: ["3.11", "3.12", "3.13"],
    java: ["17", "21"],
    go: ["1.24", "1.25"],
    dart: ["3.9"],
    default_php: "8.4",
    web_stack: "hybrid",
    web_servers: ["nginx+apache", "nginx"],
    composer: true, docker: true, docker_for_accounts: true, buildpacks: true, kubernetes: true,
    web: { nginx_version: "1.28.0", h2_directive: true, http3: true },
  };

  private accounts_: Account[] = [
    { name: "naijabites", ssh: "sftp", suspended: false, sites: 0, created_at: ago(41 * day), plan: "Business", owner: "admin" },
    { name: "adaeze", ssh: "none", suspended: false, sites: 0, created_at: ago(12 * day), plan: "Starter", owner: "reseller:webafrica" },
    { name: "lagosrentals", ssh: "shell", suspended: false, sites: 0, created_at: ago(90 * day), plan: "Business", owner: "admin" },
    { name: "kelechi", ssh: "shell", suspended: false, sites: 0, created_at: ago(5 * day), plan: "Developer", owner: "admin" },
    { name: "oldclient", ssh: "none", suspended: true, suspend_reason: "Invoice unpaid since August", sites: 0, created_at: ago(400 * day), plan: "Starter", owner: "reseller:webafrica" },
  ];

  private sites_: Site[] = ([
    { domain: "naijabites.ng", account: "naijabites", runtime: "php", version: "8.3", web_server: "nginx+apache", status: "active", created_at: ago(41 * day),
      aliases: ["naijabites.com"], www: "root", redirect_aliases: true,
      ssl: { provider: "letsencrypt", names: ["naijabites.ng", "www.naijabites.ng", "naijabites.com"], issuer: "R11", issued_at: ago(40 * day), expires_at: ago(-50 * day), force_https: true, hsts: true, http2: true, http3: true },
      php: { pm: "dynamic", max_children: 12, memory_limit_mb: 512, upload_max_mb: 128, post_max_mb: 130, timezone: "Africa/Lagos" } },
    { domain: "shop.adaeze.com", account: "adaeze", runtime: "php", version: "8.4", web_server: "nginx", status: "active", created_at: ago(12 * day) },
    { domain: "legacy.adaeze.com", account: "adaeze", runtime: "php", version: "5.6", web_server: "nginx+apache", status: "maintenance", allow_ips: ["102.89.34.12"], created_at: ago(11 * day) },
    { domain: "lagosrentals.com.ng", account: "lagosrentals", runtime: "php", version: "7.4", web_server: "nginx+apache", status: "active", created_at: ago(90 * day),
      redirects: [
        { id: "a1", kind: "redirect", match: "prefix", from: "/old-listings", to: "/listings", code: 301, keep_query: true },
        { id: "a2", kind: "redirect", match: "exact", from: "/whatsapp", to: "https://wa.me/2348031234567", code: 302 },
        { id: "a3", kind: "rewrite", match: "regex", from: "^/flat/([0-9]+)$", to: "/listing.php?id=$1" },
      ],
      ssl: { provider: "custom", names: ["lagosrentals.com.ng", "www.lagosrentals.com.ng"], issuer: "Sectigo RSA Domain Validation", issued_at: ago(350 * day), expires_at: ago(-12 * day), force_https: true, hsts: false, http2: true, http3: false } },
    { domain: "api.kelechi.dev", account: "kelechi", runtime: "node", version: "22", web_server: "nginx", port: 3001, start_command: "npm start", status: "active", created_at: ago(5 * day),
      deploy: { type: "native", git: { provider: "github", url: "git@github.com:kelechi/api.git", ref_type: "branch", ref: "main", auth: "deploy_key", auto_deploy: true },
        strategy: "blue-green", health_path: "/health", shared_paths: [".env"], keep_releases: 5, current: "20261012-181502-9f3c2ab", color: "b", alt_port: 3010,
        previews: { enabled: true, base_domain: "preview.kelechi.dev" } },
      ssl: { provider: "letsencrypt", names: ["api.kelechi.dev"], issuer: "R10", issued_at: ago(5 * day), expires_at: ago(-85 * day), force_https: true, hsts: false, http2: true, http3: false } },
    { domain: "jobs.kelechi.dev", account: "kelechi", runtime: "go", version: "1.25", web_server: "nginx", port: 3004, start_command: "./app", status: "active", created_at: ago(2 * day) },
    { domain: "status.kelechi.dev", account: "kelechi", runtime: "docker", web_server: "nginx", port: 3005, status: "active", created_at: ago(1 * day),
      deploy: { type: "image", image: "louislam/uptime-kuma:1", strategy: "blue-green", container_port: 3001, health_path: "/", keep_releases: 3, current: "20261012-090000", color: "a", alt_port: 3006 } },
    { domain: "ml.kelechi.dev", account: "kelechi", runtime: "python", version: "3.12", web_server: "nginx", port: 3002, start_command: "gunicorn app:app --bind 127.0.0.1:$PORT", status: "active", created_at: ago(4 * day) },
    { domain: "docs.kelechi.dev", account: "kelechi", runtime: "static", web_server: "nginx", status: "active", created_at: ago(3 * day),
      deploy: { type: "native", git: { provider: "gitlab", url: "https://gitlab.com/kelechi/docs.git", ref_type: "tag", ref: "v*", auth: "token", auto_deploy: true },
        strategy: "restart", public_dir: "dist", keep_releases: 5, current: "20261010-120000-71aa0de" } },
    { domain: "oldclient.com", account: "oldclient", runtime: "php", version: "8.2", web_server: "nginx", status: "suspended", created_at: ago(400 * day) },
  ] as Omit<Site, "doc_root">[]).map((s) => ({ ...s, doc_root: docRoot(s) }));

  private appState: Record<string, string> = { "api.kelechi.dev": "active", "ml.kelechi.dev": "active", "jobs.kelechi.dev": "active", "status.kelechi.dev": "active" };

  private keys: Record<string, SSHKey[]> = {
    naijabites: [{ type: "ssh-ed25519", fingerprint: fp(), comment: "chidi@macbook" }],
    lagosrentals: [
      { type: "ssh-ed25519", fingerprint: fp(), comment: "deploy@github-actions" },
      { type: "ssh-rsa", fingerprint: fp(), comment: "tunde@thinkpad" },
    ],
    kelechi: [{ type: "ssh-ed25519", fingerprint: fp(), comment: "kelechi@framework" }],
  };

  private cron_: Record<string, CronJob[]> = {
    naijabites: [
      { id: hex(4), schedule: "*/5 * * * *", command: "php web/naijabites.ng/public_html/wp-cron.php", comment: "WordPress cron", enabled: true },
      { id: hex(4), schedule: "30 2 * * *", command: "mysqldump naijabites_wp > backups/db.sql", comment: "Nightly DB dump", enabled: true },
    ],
    lagosrentals: [{ id: hex(4), schedule: "0 * * * *", command: "php web/lagosrentals.com.ng/public_html/artisan schedule:run", enabled: true }],
    kelechi: [{ id: hex(4), schedule: "@weekly", command: "cd web/ml.kelechi.dev/app && .venv/bin/python retrain.py", comment: "Retrain model", enabled: false }],
  };

  private dbs: Record<string, Database[]> = {
    naijabites: [{ name: "naijabites_wp", user: "naijabites_wp", note: "WordPress naijabites.ng", created_at: ago(41 * day) }],
    adaeze: [{ name: "adaeze_store", user: "adaeze_store", note: "Shop", created_at: ago(12 * day) }],
    lagosrentals: [
      { name: "lagosrentals_app", user: "lagosrentals_app", created_at: ago(90 * day) },
      { name: "lagosrentals_old", user: "lagosrentals_old", note: "Legacy listings", created_at: ago(300 * day) },
    ],
  };

  private ports: FirewallPort[] = [
    { port: "22", proto: "tcp", comment: "Required", protected: true },
    { port: "80", proto: "tcp", comment: "Required", protected: true },
    { port: "443", proto: "tcp", comment: "Required", protected: true },
    { port: "2083", proto: "tcp", comment: "Ike Panel", protected: true },
    { port: "2087", proto: "tcp", comment: "Ike Admin", protected: true },
    { port: "465", proto: "tcp", comment: "SMTP (SSL/TLS)", protected: false },
    { port: "587", proto: "tcp", comment: "SMTP (STARTTLS)", protected: false },
    { port: "993", proto: "tcp", comment: "IMAP (SSL/TLS)", protected: false },
  ];
  private blocks: FirewallBlock[] = [
    { address: "45.95.147.0/24", comment: "Scanner network", added_at: ago(3 * day) },
    { address: "185.220.101.44", comment: "WordPress login abuse", added_at: ago(20 * 60) },
  ];
  private bans_: Ban[] = [
    { jail: "sshd", ip: "218.92.0.112" },
    { jail: "sshd", ip: "61.177.172.140" },
    { jail: "nginx-botsearch", ip: "194.26.29.18" },
  ];

  private log: AuditEntry[] = [
    { time: ago(3), actor: "ai:mcp", method: "GET", path: "/v1/sites", status: 200, result: "ok" },
    { time: ago(6), actor: "andre", method: "POST", path: "/v1/sites", status: 201, result: "ok" },
    { time: ago(14), actor: "andre", method: "PUT", path: "/v1/sites/legacy.adaeze.com/status", status: 200, result: "ok" },
    { time: ago(38), actor: "ai:mcp", method: "DELETE", path: "/v1/sites/old.naijabites.ng", status: 403, result: "denied" },
    { time: ago(60), actor: "reseller:webafrica", method: "POST", path: "/v1/accounts/oldclient/suspend", status: 200, result: "ok" },
    { time: ago(130), actor: "andre", method: "POST", path: "/v1/firewall/blocks", status: 201, result: "ok" },
    { time: ago(26 * 60), actor: "andre", method: "POST", path: "/v1/accounts/lagosrentals/ssh/keys", status: 201, result: "ok" },
  ];
  private started = Date.now() - 1000 * 60 * 60 * 24 * 23.4;

  private record(method: string, path: string, status = 200) {
    this.log.unshift({ time: now(), actor: "andre", method, path, status, result: status < 400 ? "ok" : "error" });
  }
  private acct(name: string) {
    const a = this.accounts_.find((x) => x.name === name);
    if (!a) throw new Error(`Account ${name} not found.`);
    return a;
  }
  private find(domain: string) {
    const s = this.sites_.find((x) => x.domain === domain);
    if (!s) throw new Error(`Website ${domain} not found.`);
    return s;
  }

  async login(username: string, password: string, code?: string) {
    await wait(500);
    if (!username || !password) throw new Error("Enter your username and password.");
    // The preview account has 2FA on, so the second step can be tried out.
    if (this.people_.find((p) => p.username === username)?.totp_enabled) {
      if (!code) throw new TwoFactorRequired("Enter the 6-digit code from your authenticator app.");
      if (!/^\d{6}$|^[0-9a-f]{4}-[0-9a-f]{4}$/i.test(code.trim())) throw new Error("That code isn't right. Codes are 6 digits, or a recovery code like 3f9a-0c21.");
    }
    return { name: username, role: "admin" as Role };
  }
  async logout() { await wait(100); }
  async setup2fa() {
    await wait(300);
    const secret = Array.from(crypto.getRandomValues(new Uint8Array(20)), (b) => "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"[b % 32]).join("");
    return { secret, otpauth_url: `otpauth://totp/Ike:andre?secret=${secret}&issuer=Ike` };
  }
  async enable2fa(code: string) {
    await wait(400);
    if (!/^\d{6}$/.test(code)) throw new Error("Enter the 6-digit code from your authenticator app.");
    return { recovery_codes: Array.from({ length: 8 }, () => hex(2) + "-" + hex(2)) };
  }

  async system(): Promise<SystemInfo> {
    await wait(150);
    const t = Date.now() / 1000;
    const load = (b: number) => (b + Math.sin(t / 7) * 0.15 + Math.random() * 0.08).toFixed(2);
    return {
      hostname: "vps-lagos-1", os: "Ubuntu", os_version: "24.04", kernel: "6.8.0-48-generic", arch: "amd64", cpus: 4,
      uptime_seconds: Math.floor((Date.now() - this.started) / 1000),
      load_average: [load(0.62), load(0.48), load(0.41)],
      memory_total_mb: 7840, memory_available_mb: 4120 + Math.round(Math.sin(t / 9) * 120),
      disk_total_gb: 157.3, disk_free_gb: 96.8,
      php_versions: this.runtimes_.php, node_versions: this.runtimes_.node, python_versions: this.runtimes_.python,
    };
  }

  async services(): Promise<ServiceStatus[]> {
    await wait(150);
    return [
      "nginx", "apache2", "mariadb", ...this.runtimes_.php.map((v) => `php${v}-fpm`), "cron", "fail2ban", "ike-agent",
    ].map((name) => ({ name, active: "active" }));
  }

  async runtimes() { await wait(100); return { ...this.runtimes_ }; }

  // --------------------------------------------------------------- accounts
  async accounts() {
    await wait();
    return this.accounts_.map((a) => ({ ...a, sites: this.sites_.filter((s) => s.account === a.name).length }))
      .sort((a, b) => a.name.localeCompare(b.name));
  }
  async createAccount(name: string) {
    await wait(600);
    const e = usernameError(name);
    if (e) throw new Error(e);
    if (this.accounts_.some((a) => a.name === name)) throw new Error(`Account ${name} already exists.`);
    const a: Account = { name, ssh: "none", suspended: false, sites: 0, created_at: now(), plan: "Starter", owner: "admin" };
    this.accounts_.push(a);
    this.record("POST", "/v1/accounts", 201);
    return a;
  }
  async deleteAccount(name: string, withSites: boolean) {
    await wait(700);
    const own = this.sites_.filter((s) => s.account === name);
    if (own.length && !withSites) throw new Error(`Account ${name} still has websites (${own.map((s) => s.domain).join(", ")}).`);
    this.sites_ = this.sites_.filter((s) => s.account !== name);
    this.accounts_ = this.accounts_.filter((a) => a.name !== name);
    this.record("DELETE", `/v1/accounts/${name}`);
  }
  async suspendAccount(name: string, reason: string) {
    await wait(600);
    const a = this.acct(name);
    a.suspended = true; a.suspend_reason = reason;
    this.sites_.filter((s) => s.account === name).forEach((s) => (s.status = "suspended"));
    this.record("POST", `/v1/accounts/${name}/suspend`);
    return { ...a };
  }
  async unsuspendAccount(name: string) {
    await wait(600);
    const a = this.acct(name);
    a.suspended = false; a.suspend_reason = undefined;
    this.sites_.filter((s) => s.account === name).forEach((s) => (s.status = "active"));
    this.record("POST", `/v1/accounts/${name}/unsuspend`);
    return { ...a };
  }
  async setSSH(account: string, mode: SSHMode) {
    await wait(400);
    const a = this.acct(account);
    if (a.suspended && mode !== "none") throw new Error(`${account} is suspended. Reactivate it first.`);
    a.ssh = mode;
    this.record("PUT", `/v1/accounts/${account}/ssh`);
    return { ...a };
  }
  async sshKeys(account: string) { await wait(150); this.acct(account); return [...(this.keys[account] || [])]; }
  async addSSHKey(account: string, publicKey: string) {
    await wait(400);
    const parts = publicKey.trim().split(/\s+/);
    const types = ["ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521",
      "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com"];
    if (/[\r\n]/.test(publicKey.trim())) throw new Error("Paste one key at a time.");
    if (parts.length < 2 || !types.includes(parts[0])) throw new Error("That doesn't look like a public key. It should start with ssh-ed25519 or ssh-rsa.");
    if (!/^[A-Za-z0-9+/]+=*$/.test(parts[1]) || parts[1].length < 60) throw new Error("The key is incomplete. Copy the whole line from your .pub file.");
    const k: SSHKey = { type: parts[0], fingerprint: fp(), comment: parts.slice(2).join(" ") };
    (this.keys[account] ||= []).push(k);
    this.record("POST", `/v1/accounts/${account}/ssh/keys`, 201);
    return k;
  }
  async removeSSHKey(account: string, fingerprint: string) {
    await wait(300);
    this.keys[account] = (this.keys[account] || []).filter((k) => k.fingerprint !== fingerprint);
    this.record("DELETE", `/v1/accounts/${account}/ssh/keys`);
  }

  async cron(account: string) { await wait(150); return [...(this.cron_[account] || [])]; }
  async addCron(account: string, job: Omit<CronJob, "id">) {
    await wait(400);
    const e = cronError(job.schedule) || (!job.command.trim() ? "Enter the command to run." : null);
    if (e) throw new Error(e);
    const j = { ...job, id: hex(4) };
    (this.cron_[account] ||= []).push(j);
    this.record("POST", `/v1/accounts/${account}/cron`, 201);
    return j;
  }
  async updateCron(account: string, id: string, job: Omit<CronJob, "id">) {
    await wait(300);
    const e = cronError(job.schedule);
    if (e) throw new Error(e);
    const list = this.cron_[account] || [];
    const i = list.findIndex((j) => j.id === id);
    if (i < 0) throw new Error("Cron job not found.");
    list[i] = { ...job, id };
    this.record("PUT", `/v1/accounts/${account}/cron/${id}`);
    return list[i];
  }
  async deleteCron(account: string, id: string) {
    await wait(300);
    this.cron_[account] = (this.cron_[account] || []).filter((j) => j.id !== id);
    this.record("DELETE", `/v1/accounts/${account}/cron/${id}`);
  }

  async databases(account: string) { await wait(150); return [...(this.dbs[account] || [])]; }
  async createDatabase(account: string, name: string, note: string): Promise<NewDatabase> {
    await wait(600);
    if (!/^[a-z0-9]{1,16}$/.test(name)) throw new Error("Use 1–16 lowercase letters or numbers.");
    const full = `${account}_${name}`;
    if ((this.dbs[account] || []).some((d) => d.name === full)) throw new Error(`${full} already exists.`);
    const d: Database = { name: full, user: full, note, created_at: now() };
    (this.dbs[account] ||= []).push(d);
    this.record("POST", `/v1/accounts/${account}/databases`, 201);
    return { ...d, password: hex(16), host: "localhost" };
  }
  async deleteDatabase(account: string, name: string) {
    await wait(500);
    this.dbs[account] = (this.dbs[account] || []).filter((d) => d.name !== name);
    this.record("DELETE", `/v1/accounts/${account}/databases/${name}`);
  }

  // --------------------------------------------------------------- websites
  async sites() { await wait(); return [...this.sites_].sort((a, b) => a.domain.localeCompare(b.domain)); }
  async site(domain: string) { await wait(150); return { ...this.find(domain) }; }
  async createSite(req: CreateSiteRequest) {
    await wait(900);
    const e = domainError(req.domain);
    if (e) throw new Error(e);
    const a = this.acct(req.account);
    if (a.suspended) throw new Error(`${a.name} is suspended.`);
    if (this.sites_.some((s) => s.domain === req.domain)) throw new Error(`${req.domain} is already hosted on this server.`);
    const runtime = req.runtime || "php";
    const hasVersion = runtime !== "static" && runtime !== "proxy" && runtime !== "docker";
    const versions: string[] = hasVersion ? (this.runtimes_ as unknown as Record<string, string[]>)[runtime] : [];
    const version = !hasVersion ? undefined : req.version || (runtime === "php" ? this.runtimes_.default_php : versions.at(-1));
    if (version && !versions.includes(version)) throw new Error(`${runtimeName(runtime)} ${version} isn't installed on this server.`);
    if (runtime === "proxy" && !(req.port && req.port > 0 && req.port < 65536)) throw new Error("Give the local port to forward to (1-65535).");
    const isApp = isAppRt(runtime);
    let web: WebServer = req.web_server || this.runtimes_.web_servers[0];
    if (isProxied(runtime)) web = "nginx";
    const port = runtime === "proxy" ? req.port : isApp || runtime === "docker" ? 3011 + this.sites_.filter((s) => s.port).length : undefined;
    const starts: Record<string, string> = { node: "node server.js", python: "python app.py", java: "java Main.java", go: "./app", dart: "dart run bin/server.dart" };
    const site: Site = {
      domain: req.domain, account: req.account, runtime, version, web_server: web, port, status: "active", created_at: now(),
      start_command: isApp ? req.start_command || starts[runtime] : undefined,
      doc_root: docRoot({ ...req, runtime }),
    };
    this.sites_.push(site);
    if (isApp) this.appState[site.domain] = "active";
    this.record("POST", "/v1/sites", 201);
    return site;
  }
  async updateSite(domain: string, patch: { version?: string; web_server?: WebServer; start_command?: string }) {
    await wait(700);
    const s = this.find(domain);
    Object.assign(s, Object.fromEntries(Object.entries(patch).filter(([, v]) => v)));
    this.record("PATCH", `/v1/sites/${domain}`);
    return { ...s };
  }
  async deleteSite(domain: string, removeFiles: boolean) {
    await wait(700);
    this.find(domain);
    this.sites_ = this.sites_.filter((s) => s.domain !== domain);
    this.record("DELETE", `/v1/sites/${domain}${removeFiles ? "?remove_files=true" : ""}`);
  }
  async setStatus(domain: string, status: SiteStatus, allowIps: string[]) {
    await wait(500);
    const s = this.find(domain);
    for (const ip of allowIps) {
      if (!/^(\d{1,3}\.){3}\d{1,3}$|^[0-9a-f:]+$/i.test(ip)) throw new Error(`“${ip}” is not an IP address.`);
    }
    s.status = status;
    s.allow_ips = status === "maintenance" ? allowIps : [];
    this.record("PUT", `/v1/sites/${domain}/status`);
    return { ...s };
  }
  async appAction(domain: string, action: "start" | "stop" | "restart") {
    await wait(600);
    const s = this.find(domain);
    if (s.status === "suspended" && action !== "stop") throw new Error(`${domain} is suspended.`);
    this.appState[domain] = action === "stop" ? "inactive" : "active";
    this.record("POST", `/v1/sites/${domain}/app/${action}`);
  }
  appStateOf(domain: string) { return this.appState[domain] || "inactive"; }
  async siteLog(domain: string, kind: LogKind) {
    await wait(300);
    const s = this.find(domain);
    const t = (m: number) => new Date(Date.now() - m * 60_000).toISOString().replace("T", " ").slice(0, 19);
    const ips = ["102.89.34.12", "105.112.40.7", "41.184.12.90", "197.210.76.3"];
    switch (kind) {
      case "access":
        return Array.from({ length: 24 }, (_, i) => {
          const paths = ["/", "/shop", "/wp-login.php", "/products/ankara-dress", "/cart", "/favicon.ico", "/api/orders"];
          const p = paths[i % paths.length];
          const code = p === "/wp-login.php" && i % 3 === 0 ? 403 : p === "/favicon.ico" ? 404 : 200;
          return `${ips[i % ips.length]} - - [${t(24 - i)}] "GET ${p} HTTP/2.0" ${code} ${1200 + i * 37} "-" "Mozilla/5.0"`;
        }).join("\n");
      case "error":
        return `${t(42)} [error] 1204#1204: *88 open() "${s.doc_root}/favicon.ico" failed (2: No such file or directory)\n` +
          `${t(9)} [warn] 1204#1204: *311 an upstream response is buffered to a temporary file`;
      case "php":
        return s.runtime !== "php" ? "" :
          `[${t(30)}] PHP Warning:  Undefined array key "currency" in ${s.doc_root}/wp-content/plugins/naira-pay/checkout.php on line 88\n` +
          `[${t(2)}] PHP Notice:  Function wp_enqueue_script was called incorrectly in ${s.doc_root}/wp-includes/functions.php on line 6031`;
      case "apache-access":
      case "apache-error":
        return s.web_server === "nginx" ? "" : `[${t(15)}] [proxy_fcgi:error] AH01071: Got error 'Primary script unknown'`;
      case "app":
        return s.runtime === "node"
          ? `${t(55)} ike-app[2210]: > api@1.4.0 start\n${t(55)} ike-app[2210]: Listening on port ${s.port}\n${t(3)} ike-app[2210]: POST /orders 201 48ms`
          : `${t(40)} ike-app[3302]: [INFO] Starting gunicorn 22.0.0\n${t(40)} ike-app[3302]: [INFO] Listening at: http://127.0.0.1:${s.port}`;
    }
  }
  async fixPermissions(domain: string) { await wait(900); this.find(domain); this.record("POST", `/v1/sites/${domain}/permissions`); return { owners_fixed: 1284, skipped_links: 2 }; }
  async apps(): Promise<CatalogApp[]> {
    await wait(100);
    return [
      { id: "wordpress", name: "WordPress", runtime: "php", available: true, description: "Websites, blogs and online shops (WooCommerce)." },
      { id: "laravel", name: "Laravel", runtime: "php", available: false, description: "PHP framework for web apps and APIs." },
      { id: "joomla", name: "Joomla", runtime: "php", available: false, description: "Content management for larger sites." },
      { id: "drupal", name: "Drupal", runtime: "php", available: false, description: "Structured content and multilingual sites." },
      { id: "prestashop", name: "PrestaShop", runtime: "php", available: false, description: "Online store." },
      { id: "nextcloud", name: "Nextcloud", runtime: "php", available: false, description: "File sync and sharing for teams." },
      { id: "ghost", name: "Ghost", runtime: "node", available: false, description: "Publishing and newsletters." },
      { id: "django", name: "Django", runtime: "python", available: false, description: "Python web framework starter." },
    ];
  }
  async installWordPress(domain: string, req: { title: string; admin_user: string; admin_email: string }): Promise<WordPressResult> {
    await wait(2200);
    const s = this.find(domain);
    if (s.runtime !== "php") throw new Error("WordPress needs a PHP website.");
    if (req.admin_user.toLowerCase() === "admin") throw new Error("Choose an admin username other than “admin”.");
    if (!/^[^@\s]+@[^@\s]+\.[a-z]{2,}$/i.test(req.admin_email)) throw new Error("Enter a valid admin email address.");
    const db = await this.createDatabase(s.account, "wp" + hex(2), "WordPress " + domain);
    return { url: `http://${domain}`, admin_url: `http://${domain}/wp-admin/`, admin_user: req.admin_user, admin_password: hex(10), database: db };
  }

  // --------------------------------------------------------------- firewall
  async firewall() { await wait(200); return { ports: [...this.ports], blocks: [...this.blocks] }; }
  async openPort(port: string, proto: "tcp" | "udp", comment: string) {
    await wait(400);
    if (!/^\d{1,5}(:\d{1,5})?$/.test(port) || +port.split(":")[0] < 1 || +port.split(":")[0] > 65535) throw new Error("Use a port from 1 to 65535, or a range like 6000:6100.");
    if (this.ports.some((p) => p.port === port && p.proto === proto)) throw new Error(`${port}/${proto} is already open.`);
    this.ports.push({ port, proto, comment, protected: false });
    this.record("POST", "/v1/firewall/ports", 201);
  }
  async closePort(port: string, proto: "tcp" | "udp") {
    await wait(400);
    const p = this.ports.find((x) => x.port === port && x.proto === proto);
    if (p?.protected) throw new Error(`${port}/${proto} keeps you connected to this server and can't be closed here.`);
    this.ports = this.ports.filter((x) => x !== p);
    this.record("DELETE", "/v1/firewall/ports");
  }
  async block(address: string, comment: string) {
    await wait(400);
    if (!/^(\d{1,3}\.){3}\d{1,3}(\/\d{1,2})?$|^[0-9a-f:]+(\/\d{1,3})?$/i.test(address)) throw new Error(`“${address}” is not an IP address or range.`);
    const bits = +(address.split("/")[1] ?? 32);
    if (address.includes(".") && bits < 8) throw new Error("That range is too broad to block.");
    if (address.startsWith("127.")) throw new Error("This server's own address can't be blocked.");
    if (this.blocks.some((b) => b.address === address)) throw new Error(`${address} is already blocked.`);
    this.blocks.unshift({ address, comment, added_at: now() });
    this.record("POST", "/v1/firewall/blocks", 201);
  }
  async unblock(address: string) { await wait(300); this.blocks = this.blocks.filter((b) => b.address !== address); this.record("DELETE", "/v1/firewall/blocks"); }
  async bans() { await wait(200); return [...this.bans_]; }
  async unban(jail: string, ip: string) { await wait(300); this.bans_ = this.bans_.filter((b) => !(b.jail === jail && b.ip === ip)); this.record("DELETE", "/v1/firewall/bans"); }

  async audit() { await wait(150); return [...this.log]; }

  // ------------------------------------------------- updates and health
  private jobs_: Record<string, Job> = {};
  private auto: AutoUpdates = { security_updates: true, auto_reboot: false, reboot_time: "03:30" };
  private pending = [
    { name: "openssl", from: "3.0.13-0ubuntu3.4", to: "3.0.13-0ubuntu3.5", security: true },
    { name: "linux-image-generic", from: "6.8.0-48.48", to: "6.8.0-50.51", security: true },
    { name: "libxml2", from: "2.9.14+dfsg-1.3ubuntu3", to: "2.9.14+dfsg-1.3ubuntu3.1", security: true },
    { name: "nginx", from: "1.26.2-1~noble", to: "1.28.0-1~noble", security: false },
    { name: "php8.4-fpm", from: "8.4.12-1+ubuntu24.04.1", to: "8.4.13-1+ubuntu24.04.1", security: false },
    { name: "mariadb-server", from: "1:10.11.13-0ubuntu0.24.04.1", to: "1:10.11.14-0ubuntu0.24.04.1", security: false },
  ];
  private reboot = false;

  private startJob(kind: string, title: string, steps: string[], onDone?: () => void): Job {
    const j: Job = { id: hex(6), kind, title, status: "running", log: [], started_at: now() };
    this.jobs_[j.id] = j;
    steps.forEach((line, i) => setTimeout(() => {
      j.log.push(line);
      if (i === steps.length - 1) { j.status = "done"; j.finished_at = now(); onDone?.(); }
    }, 700 * (i + 1)));
    this.record("POST", `/v1/jobs/${kind}`, 202);
    return { ...j };
  }
  // Newer screens talk to the API through call(); the preview answers them
  // from an in-memory server in demo5.ts.
  private d5: Demo5 | null = null;
  private v5() {
    if (!this.d5) this.d5 = new Demo5({
      accounts: () => this.accounts_.map((a) => a.name),
      sites: () => this.sites_.map((s) => ({ domain: s.domain, account: s.account, runtime: s.runtime })),
      job: (kind: string, title: string, steps: string[], onDone?: () => void) => this.startJob(kind, title, steps, onDone),
      mariadb: (a: string) => (this.dbs[a] ?? []).map((d) => d.name),
      sshKeys: (a: string) => (this.keys[a] ??= []),
    });
    return this.d5;
  }
  async call<T = unknown>(method: string, path: string, body?: unknown): Promise<T> {
    await wait(method === "GET" ? 220 : 380);
    const out = this.v5().handle(method, path, body);
    if (method !== "GET") this.record(method, "/v1" + path.split("?")[0]);
    return out as T;
  }
  async upload<T = unknown>(method: "PUT" | "POST", path: string, file: Blob, onProgress?: (pct: number) => void): Promise<T> {
    for (let p = 10; p <= 100; p += 30) { onProgress?.(Math.min(p, 100)); await wait(150); }
    return this.v5().handle(method, path, { size: file.size, name: (file as File).name }) as T;
  }
  fileUrl() { return ""; }
  socketUrl() { return null; }

  async job(id: string) { await wait(100); const j = this.jobs_[id]; if (!j) throw new Error("Job not found."); return { ...j, log: [...j.log] }; }

  async updates(): Promise<UpdateStatus> {
    await wait(900);
    return { checked_at: now(), packages: [...this.pending], security_count: this.pending.filter((p) => p.security).length,
      reboot_required: this.reboot, reboot_packages: this.reboot ? ["linux-image-6.8.0-50-generic"] : [], auto: { ...this.auto } };
  }
  async applyUpdates(securityOnly: boolean) {
    await wait(300);
    const take = this.pending.filter((p) => !securityOnly || p.security);
    return this.startJob("os-upgrade", securityOnly ? "Install security updates" : "Install all updates",
      ["Refreshing package lists", ...take.map((p) => `Unpacking ${p.name} (${p.to})`), "Setting up packages", "A reboot is needed to finish (linux-image-6.8.0-50-generic)."],
      () => { this.pending = this.pending.filter((p) => !take.includes(p)); this.reboot = take.some((p) => p.name.startsWith("linux-image")); });
  }
  async setAutoUpdates(a: AutoUpdates) {
    await wait(300);
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(a.reboot_time)) throw new Error("Reboot time must look like 03:30.");
    this.auto = { ...a };
    this.record("PUT", "/v1/updates/auto");
    return { ...this.auto };
  }
  async scheduleReboot(minutes: number) { await wait(300); if (minutes < 1) throw new Error("Pick a delay of at least a minute."); this.record("POST", "/v1/server/reboot"); }
  async ikeRelease(): Promise<IkeRelease> {
    await wait(500);
    return { current: "0.3.0", latest: "0.3.1", update_available: true, url: "https://github.com/YOUR-ORG/ike/releases/tag/v0.3.1",
      notes: "Fixes: PHP 8.5 pools on Ubuntu 26.04, faster usage sampling. New: Paystack split payments." };
  }
  async updateIke() { await wait(300); return this.startJob("ike-update", "Update Ike to 0.3.1", ["Downloading ike-agent 0.3.1", "Checksum verified", "Downloading ike-mcp 0.3.1", "Checksum verified", "Restarting Ike in 3 seconds"]); }

  async diagnose(domain: string): Promise<Diagnosis> {
    await wait(1100);
    const s = this.find(domain);
    const f: Diagnosis["findings"] = [];
    if (s.status === "maintenance") f.push({ severity: "warn", title: "The website is in maintenance mode", detail: "Visitors see the “Back soon” page.", fix: "Set its status back to Active when you're done." });
    if (s.status === "suspended") f.push({ severity: "bad", title: "The website is suspended", fix: "Set its status to Active, or reactivate the account." });
    if (s.runtime === "php" && s.version && phpEol(s.version)) f.push({ severity: "warn", title: `PHP ${s.version} no longer gets security fixes`, fix: "Test the site on PHP 8.3 or newer. A staging copy is the safe way." });
    if (domain === "naijabites.ng") f.unshift({ severity: "bad", title: "PHP crashed in the WordPress plugin “naira-pay”",
      fix: "Update or disable the plugin (rename its folder over SFTP), or switch to the PHP version it supports.",
      evidence: "PHP Fatal error:  Uncaught TypeError: number_format(): Argument #1 must be of type float, string given in /home/naijabites/web/naijabites.ng/public_html/wp-content/plugins/naira-pay/checkout.php:88" });
    if (domain === "api.kelechi.dev") f.push({ severity: "bad", title: "Node.js can't find the module “dotenv”", fix: "Run npm install in the app folder, then restart the app.", evidence: "Error: Cannot find module 'dotenv'" });
    const bad = f.find((x) => x.severity === "bad");
    return { domain, summary: bad ? bad.title + "." : f.length ? "The website is up, with a few things to look at." : "Everything looks healthy.",
      findings: f.length ? f : [{ severity: "ok", title: "No problems found", detail: "Services are running and the logs show no errors." }] };
  }
  async createStaging(domain: string) {
    await wait(300);
    const live = this.find(domain);
    if (live.staging) throw new Error(`${domain} is already a staging copy.`);
    const stage = "staging." + domain;
    if (this.sites_.some((x) => x.domain === stage)) throw new Error(`${stage} already exists.`);
    return this.startJob("staging", "Create a staging copy of " + domain,
      [`Creating ${stage}`, "Copying files", ...(live.runtime === "php" ? ["Exporting the live database", "Importing the database copy", `Replacing ${domain} with ${stage} in the copy`] : []), `Staging copy ready at ${stage}`],
      () => this.sites_.push({ ...live, domain: stage, staging: true, staging_of: domain, status: "active", created_at: now(), doc_root: live.doc_root.replace(domain, stage) }));
  }
  async pushStaging(domain: string) {
    await wait(300);
    const s = this.find(domain);
    if (!s.staging) throw new Error(`${domain} isn't a staging copy.`);
    return this.startJob("push", `Push ${domain} to live`, [`Backing up the live files of ${s.staging_of}`, `Copying staging files to ${s.staging_of} (keeping its wp-config.php and .env)`, "Done."]);
  }
  async setHealthCheck(domain: string, hc: HealthCheck | null) {
    await wait(300);
    const s = this.find(domain);
    if (hc && !/^\/[A-Za-z0-9._~/-]*$/.test(hc.path)) throw new Error("The path must start with / and contain only letters, numbers and ._~/-");
    s.health = hc ?? undefined;
    this.record("PUT", `/v1/sites/${domain}/healthcheck`);
    return { ...s };
  }
  private envs: Record<string, EnvVar[]> = {
    "api.kelechi.dev": [{ key: "NODE_ENV", value: "production" }, { key: "DATABASE_URL", value: "postgres://kelechi:••••@localhost/api" }, { key: "PAYSTACK_SECRET", value: "sk_live_••••••••" }],
    "lagosrentals.com.ng": [{ key: "APP_ENV", value: "production" }, { key: "APP_KEY", value: "base64:7d0a••••" }, { key: "DB_DATABASE", value: "lagosrentals_app" }],
  };
  async env(domain: string) { await wait(200); this.find(domain); return [...(this.envs[domain] || [])]; }
  async setEnv(domain: string, vars: EnvVar[]) {
    await wait(400);
    const seen = new Set<string>();
    for (const v of vars) {
      if (!/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(v.key)) throw new Error(`“${v.key}” isn't a valid name. Use letters, numbers and _, not starting with a number.`);
      if (["PORT", "HOST", "PATH"].includes(v.key)) throw new Error(`${v.key} is set by Ike and can't be changed here.`);
      if (seen.has(v.key)) throw new Error(`${v.key} appears twice.`);
      seen.add(v.key);
    }
    this.envs[domain] = vars;
    this.record("PUT", `/v1/sites/${domain}/env`);
  }
  async setLimits(account: string, l: Limits) {
    await wait(400);
    if (l.memory_mb && l.memory_mb < 64) throw new Error("Memory limit must be at least 64 MB, or 0 for none.");
    const a = this.acct(account);
    a.limits = { ...l };
    this.record("PUT", `/v1/accounts/${account}/limits`);
    return { ...a };
  }
  async usage(): Promise<AccountUsage[]> {
    await wait(300);
    const base: Record<string, [number, number, number, number]> = {
      naijabites: [38, 612, 4210, 820], adaeze: [6, 180, 940, 96], lagosrentals: [72, 1430, 11800, 1410], kelechi: [21, 890, 2300, 310], oldclient: [0, 0, 1800, 0],
    };
    const t0 = Date.now();
    return this.accounts_.map((a) => {
      const [cpu, mem, disk, bw] = base[a.name] || [3, 90, 300, 12];
      const history = Array.from({ length: 96 }, (_, i) => {
        const wave = Math.sin(i / 7 + a.name.length) * 0.35 + Math.sin(i / 2.3) * 0.12;
        const spike = a.name === "lagosrentals" && i > 70 && i < 76 ? 1.6 : 1;
        return { t: new Date(t0 - (95 - i) * 15 * 60_000).toISOString(), cpu: Math.max(0, +(cpu * (1 + wave) * spike).toFixed(1)), mem: Math.max(0, +(mem * (1 + wave / 3)).toFixed(0)) };
      });
      return { account: a.name, cpu_percent: cpu, memory_mb: mem, processes: Math.round(cpu / 3) + 2, disk_mb: disk, bandwidth_today_mb: bw,
        limits: a.limits || { cpu_percent: a.name === "lagosrentals" ? 100 : 0, memory_mb: a.name === "lagosrentals" ? 2048 : 0, max_processes: 0, disk_mb: a.plan === "Starter" ? 5120 : 20480, php_workers: 5 }, history };
    });
  }
  async security(): Promise<SecurityReport> {
    await wait(1000);
    const checks: SecurityReport["checks"] = [
      { id: "ssh-root", area: "ssh", severity: "ok", title: "Root can only log in with an SSH key" },
      { id: "ssh-passwords", area: "ssh", severity: "warn", title: "SSH accepts passwords", fix: "Turn off PasswordAuthentication once everyone uses keys. fail2ban limits guessing meanwhile." },
      { id: "firewall", area: "firewall", severity: "ok", title: "The firewall is on" },
      { id: "fail2ban", area: "firewall", severity: "ok", title: "Repeated failed logins are banned automatically" },
      { id: "updates", area: "updates", severity: this.pending.some((p) => p.security) ? "bad" : "ok",
        title: this.pending.some((p) => p.security) ? `${this.pending.filter((p) => p.security).length} security updates are waiting` : "No security updates are waiting", fix: "Install security updates on the Updates page." },
      { id: "exposed", area: "network", severity: "ok", title: "Databases and caches only listen on this server" },
      { id: "php-eol", area: "websites", severity: "warn", title: "2 websites use PHP versions without security fixes", fix: "Move them to PHP 8.3 or newer: legacy.adaeze.com (PHP 5.6), lagosrentals.com.ng (PHP 7.4)" },
      { id: "https", area: "websites", severity: "warn", title: `${this.sites_.length} websites don't have HTTPS yet`, fix: "Let's Encrypt certificates arrive in the next phase." },
      { id: "secrets-readable", area: "files", severity: "bad", title: "Config files with passwords can be read by other users", fix: "Run Fix permissions on: lagosrentals.com.ng" },
    ];
    let score = 100;
    checks.forEach((c) => { score -= c.severity === "bad" ? 15 : c.severity === "warn" ? 5 : 0; });
    return { score, checks };
  }
  private drifts: Drift[] = [
    { path: "/etc/nginx/ike/sites/lagosrentals.com.ng.conf", kind: "nginx", owner: "lagosrentals.com.ng", state: "changed",
      diff: "-     client_max_body_size 64m;\n+     client_max_body_size 2g;\n+     location /old-listings { return 301 /listings; }\n" },
    { path: "/etc/php/8.3/fpm/pool.d/ike-naijabites-1f3a9c20.conf", kind: "php-pool", owner: "naijabites.ng", state: "changed",
      diff: "- pm.max_children = 5\n+ pm.max_children = 40\n" },
  ];
  async drift() { await wait(600); return [...this.drifts]; }
  async reconcile(paths: string[]) {
    await wait(800);
    const fixed = this.drifts.filter((d) => !paths.length || paths.includes(d.path));
    this.drifts = this.drifts.filter((d) => !fixed.includes(d));
    this.record("POST", "/v1/drift/reconcile");
    return fixed;
  }
  async inspectImport(file: string): Promise<ImportPlan> {
    await wait(1200);
    if (!/^[A-Za-z0-9._-]+\.tar(\.gz)?$/.test(file)) throw new Error("Give the backup's file name, e.g. cpmove-adaeze.tar.gz");
    if (file !== "cpmove-tundeprops.tar.gz") throw new Error(`${file} isn't in /var/lib/ike/imports. Upload it there first (SFTP as root), then try again.`);
    return { file, cpanel_user: "tundeprops", account: "tundeprops", main_domain: "tundeproperties.ng", cron_jobs: 3, home_size_mb: 4382,
      databases: ["tundeprops_wp", "tundeprops_crm"], mailboxes: ["info@tundeproperties.ng", "sales@tundeproperties.ng", "tunde@tundeproperties.ng"],
      websites: [
        { domain: "tundeproperties.ng", kind: "main", php: "8.1", php_installed: true },
        { domain: "lekkihomes.com.ng", kind: "addon", php: "7.3", php_installed: false },
        { domain: "blog.tundeproperties.ng", kind: "subdomain", php_installed: true },
        { domain: "tundeproperties.com", kind: "alias", php_installed: true },
      ],
      warnings: ["lekkihomes.com.ng uses PHP 7.3, which isn't installed here", "Mailboxes are listed; importing mail arrives with the email phase"] };
  }

  // -------------------------------------------------- people and billing
  private people_: Person[] = [
    { username: "andre", email: "andre@megamind.ng", name: "Andre", role: "admin", totp_enabled: true, accounts: [], disabled: false, created_at: ago(90 * day), last_login_at: ago(2) },
    { username: "webafrica", email: "ops@webafrica.ng", name: "WebAfrica Hosting", role: "reseller", totp_enabled: true, accounts: [], max_accounts: 25, disabled: false, created_at: ago(60 * day), last_login_at: ago(300) },
    { username: "ada", email: "ada@shop.adaeze.com", name: "Adaeze Okafor", role: "user", totp_enabled: false, reseller: "webafrica", accounts: ["adaeze"], disabled: false, created_at: ago(12 * day), last_login_at: ago(1500) },
    { username: "kelechi", email: "kelechi@kelechi.dev", role: "user", totp_enabled: true, accounts: ["kelechi"], disabled: false, created_at: ago(5 * day) },
    { username: "emeka.dev", email: "emeka@pixelforge.ng", name: "Emeka (Pixelforge)", role: "developer", totp_enabled: true, accounts: ["naijabites"], disabled: false, created_at: ago(8 * day),
      last_login_at: ago(90), areas: { sites: "write", files: "write", databases: "read" }, invited_by: "chidi" },
  ];
  async people() { await wait(200); return [...this.people_]; }
  async createPerson(p: PersonCreate) {
    await wait(500);
    if (!/^[a-z][a-z0-9._-]{2,31}$/.test(p.username)) throw new Error("Username must be 3–32 lowercase letters, numbers, dots, dashes or underscores.");
    if (!/^[^@\s]+@[^@\s]+\.[a-z]{2,}$/i.test(p.email)) throw new Error("Enter a valid email address.");
    if (p.password.length < 10) throw new Error("Use a password of at least 10 characters.");
    if (this.people_.some((x) => x.username === p.username)) throw new Error(`${p.username} already exists.`);
    if (p.role === "developer") {
      if (!p.accounts?.length) throw new Error("choose the accounts the developer works on");
      if (!p.areas || !Object.keys(p.areas).length) throw new Error("give the developer at least one area, such as sites");
    }
    const n: Person = { username: p.username, email: p.email, name: p.name, role: p.role, totp_enabled: false, accounts: p.accounts || [], max_accounts: p.max_accounts, disabled: false, created_at: now(),
      areas: p.areas as Person["areas"], invited_by: p.role === "developer" ? "andre" : undefined };
    this.people_.push(n);
    return n;
  }
  async updatePerson(username: string, patch: Partial<{ disabled: boolean; accounts: string[]; max_accounts: number; role: Role; areas: Record<string, string> }>) {
    await wait(300);
    const p = this.people_.find((x) => x.username === username);
    if (!p) throw new Error("Person not found.");
    if (patch.disabled && p.role === "admin" && this.people_.filter((x) => x.role === "admin" && !x.disabled).length <= 1) throw new Error("This is the last server admin and can't be disabled.");
    Object.assign(p, patch);
    return { ...p };
  }
  private billing: BillingSettings = { currency: "NGN", grace_days: 7, auto_suspend: true, providers: {
    bachs: { enabled: true, secret_key: "••••••••", webhook_secret: "••••••••", sandbox: false },
    paystack: { enabled: true, secret_key: "••••••••" },
    flutterwave: { enabled: false },
  } };
  private webhookUrls = { bachs: "https://vps-lagos-1.ike.host:2083/webhooks/bachs", paystack: "https://vps-lagos-1.ike.host:2083/webhooks/paystack", flutterwave: "https://vps-lagos-1.ike.host:2083/webhooks/flutterwave" };
  async billingSettings() { await wait(200); return { settings: JSON.parse(JSON.stringify(this.billing)), webhook_urls: this.webhookUrls }; }
  async saveBillingSettings(set: BillingSettings) {
    await wait(500);
    if (!/^[A-Z]{3}$/.test(set.currency)) throw new Error("Currency must be a 3-letter code like NGN, GHS, KES or USD.");
    for (const [k, p] of Object.entries(set.providers)) {
      if (p?.enabled && !p.secret_key) throw new Error(`Enter the secret key for ${k}.`);
      if (p?.enabled && k !== "paystack" && !p.webhook_secret) throw new Error(`Enter the webhook secret for ${k}, so payments can be verified.`);
      if (p?.secret_key && p.secret_key !== "••••••••") p.secret_key = "••••••••";
      if (p?.webhook_secret && p.webhook_secret !== "••••••••") p.webhook_secret = "••••••••";
    }
    this.billing = set;
    this.record("PUT", "/v1/billing/settings");
    return this.billingSettings();
  }
  private invoices_: Invoice[] = [
    { id: "INV-000014", account: "lagosrentals", description: "Business plan, October", currency: "NGN", amount: 2500000, status: "unpaid", due_at: ago(-3 * day), created_at: ago(4 * day) },
    { id: "INV-000013", account: "naijabites", description: "Business plan, October", currency: "NGN", amount: 2500000, status: "paid", provider: "paystack", due_at: ago(5 * day), created_at: ago(12 * day), paid_at: ago(6 * day) },
    { id: "INV-000012", account: "kelechi", description: "Developer plan, October", currency: "USD", amount: 1500, status: "paid", provider: "bachs", due_at: ago(8 * day), created_at: ago(13 * day), paid_at: ago(9 * day) },
    { id: "INV-000011", account: "oldclient", description: "Starter plan, August", currency: "NGN", amount: 800000, status: "unpaid", due_at: ago(48 * day), created_at: ago(55 * day) },
    { id: "INV-000010", account: "adaeze", description: "Starter plan, October", currency: "NGN", amount: 800000, status: "unpaid", due_at: ago(-6 * day), created_at: ago(1 * day) },
  ];
  private seq = 15;
  async invoices() { await wait(250); return [...this.invoices_]; }
  async createInvoice(r: { account: string; description: string; amount: string; currency?: string; due_in_days: number }) {
    await wait(400);
    const cur = (r.currency || this.billing.currency).toUpperCase();
    const n = Math.round(parseFloat(r.amount) * 100);
    if (!n || n <= 0) throw new Error("Enter an amount greater than zero, like 15000.00.");
    if (!r.description.trim()) throw new Error("Add a short description.");
    const inv: Invoice = { id: `INV-${String(this.seq++).padStart(6, "0")}`, account: r.account, description: r.description.trim(), currency: cur, amount: n,
      status: "unpaid", due_at: new Date(Date.now() + r.due_in_days * 86400_000).toISOString(), created_at: now() };
    this.invoices_.unshift(inv);
    return inv;
  }
  async payInvoice(id: string, provider: PaymentProvider) {
    await wait(700);
    if (!this.billing.providers[provider]?.enabled) throw new Error("That payment method isn't set up on this server.");
    const host = { bachs: "pay.bachs.io/c/chk_" + hex(6), paystack: "checkout.paystack.com/" + hex(6), flutterwave: "checkout.flutterwave.com/v3/hosted/pay/" + hex(6) }[provider];
    return { checkout_url: "https://" + host + "#" + id };
  }
  async voidInvoice(id: string) { await wait(300); const i = this.invoices_.find((x) => x.id === id); if (i) i.status = "void"; }
  async markPaid(id: string) {
    await wait(400);
    const i = this.invoices_.find((x) => x.id === id);
    if (!i) throw new Error("Invoice not found.");
    i.status = "paid"; i.paid_at = now();
    const a = this.accounts_.find((x) => x.name === i.account);
    if (a?.suspended) { a.suspended = false; this.sites_.filter((s) => s.account === a.name).forEach((s) => (s.status = "active")); }
  }
  private alerts: AlertSettings = {
    telegram: { enabled: true, bot_token: "••••••••", chat_id: "-1002233445566" },
    whatsapp: { enabled: false, phone_number_id: "", to: "" },
    email: { enabled: true, host: "smtp-relay.brevo.com", port: 587, username: "alerts@megamind.ng", password: "••••••••", from: "alerts@megamind.ng", to: "andre@megamind.ng" },
    uptime_interval_min: 5,
  };
  async alertSettings() { await wait(200); return JSON.parse(JSON.stringify(this.alerts)); }
  async saveAlertSettings(a: AlertSettings) { await wait(400); this.alerts = a; return this.alertSettings(); }
  async testAlert() {
    await wait(900);
    const r: Record<string, string> = {};
    if (this.alerts.telegram.enabled) r.telegram = "sent";
    if (this.alerts.whatsapp.enabled) r.whatsapp = this.alerts.whatsapp.to ? "sent" : "token, phone number id and recipient are required";
    if (this.alerts.email.enabled) r.email = "sent";
    if (!Object.keys(r).length) throw new Error("Turn on at least one channel first.");
    return r;
  }
  async alertEvents(): Promise<AlertEvent[]> {
    await wait(200);
    return [
      { time: ago(95), domain: "api.kelechi.dev", kind: "up", detail: "Back up after 6m" },
      { time: ago(101), domain: "api.kelechi.dev", kind: "down", detail: "HTTP 502" },
      { time: ago(60 * 26), domain: "lagosrentals.com.ng", kind: "up", detail: "Back up after 2m" },
      { time: ago(60 * 26 + 2), domain: "lagosrentals.com.ng", kind: "down", detail: "context deadline exceeded" },
    ];
  }

  // ------------------------------------------------------------------ email
  private boxes: Mailbox[] = [
    { address: "hello@naijabites.ng", quota_mb: 2048, used_mb: 412, created_at: ago(40 * day) },
    { address: "orders@naijabites.ng", quota_mb: 5120, used_mb: 3870, created_at: ago(40 * day) },
    { address: "chidi@naijabites.ng", quota_mb: 2048, used_mb: 230, created_at: ago(38 * day) },
    { address: "ada@shop.adaeze.com", quota_mb: 2048, used_mb: 96, created_at: ago(11 * day) },
    { address: "info@lagosrentals.com.ng", quota_mb: 10240, used_mb: 9650, created_at: ago(88 * day) },
  ];
  private fwds: Forwarder[] = [
    { from: "support@naijabites.ng", to: "hello@naijabites.ng" },
    { from: "sales@lagosrentals.com.ng", to: "tunde.okafor@gmail.com" },
  ];
  private smtp_: SmtpSettings = {
    provider: "brevo", host: "smtp-relay.brevo.com", port: 587, username: "postmaster@naijabites.ng", has_password: true, port25_open: false,
  };

  async mailDomains(): Promise<MailDomain[]> {
    await wait(120);
    return this.sites_.filter((s) => s.status !== "suspended").map((s) => ({ domain: s.domain, webmail_url: `https://webmail.${s.domain}` }));
  }
  async mailboxes(domain: string) { await wait(200); return this.boxes.filter((b) => b.address.endsWith("@" + domain)); }
  async createMailbox(address: string, password: string, quotaMb: number) {
    await wait(600);
    const [local, domain] = address.split("@");
    if (!local || !domain || !/^[a-z0-9]([a-z0-9._-]{0,62}[a-z0-9])?$/.test(local)) throw new Error("Use letters, numbers, dots, hyphens or underscores before the @.");
    if (!this.sites_.some((s) => s.domain === domain)) throw new Error(`${domain} isn't hosted on this server.`);
    if (this.boxes.some((b) => b.address === address)) throw new Error(`${address} already exists.`);
    if (password.length < 10) throw new Error("Use a password of at least 10 characters.");
    const box: Mailbox = { address, quota_mb: quotaMb, used_mb: 0, created_at: now() };
    this.boxes.push(box);
    this.record("POST", "/v1/mail/mailboxes", 201);
    return box;
  }
  async deleteMailbox(address: string) { await wait(400); this.boxes = this.boxes.filter((b) => b.address !== address); this.record("DELETE", `/v1/mail/mailboxes/${address}`); }
  async forwarders(domain: string) { await wait(150); return this.fwds.filter((f) => f.from.endsWith("@" + domain)); }
  async addForwarder(f: Forwarder) {
    await wait(400);
    if (!/^[^@\s]+@[^@\s]+\.[a-z]{2,}$/i.test(f.to)) throw new Error("Enter a full email address to forward to.");
    if (this.fwds.some((x) => x.from === f.from && x.to === f.to)) throw new Error("That forwarder already exists.");
    this.fwds.push(f);
    return f;
  }
  async removeForwarder(f: Forwarder) { await wait(300); this.fwds = this.fwds.filter((x) => !(x.from === f.from && x.to === f.to)); }
  async dnsChecks(domain: string): Promise<DnsCheck[]> {
    await wait(700);
    const ip = "203.0.113.24";
    const good = domain === "naijabites.ng";
    const relay = this.smtp_.provider === "brevo" ? " include:spf.brevo.com" : "";
    return [
      { record: "MX", host: domain, type: "MX", expected: `10 mail.${domain}`, status: "ok", hint: "Tells other servers where to deliver mail for this domain." },
      { record: "SPF", host: domain, type: "TXT", expected: `v=spf1 a mx ip4:${ip}${relay} ~all`, status: good ? "ok" : "wrong", hint: "Lists the servers allowed to send as this domain. Without it, mail lands in spam." },
      { record: "DKIM", host: `ike._domainkey.${domain}`, type: "TXT", expected: "v=DKIM1; k=rsa; p=MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAu3k…", status: good ? "ok" : "missing", hint: "A signature that proves messages weren't changed on the way." },
      { record: "DMARC", host: `_dmarc.${domain}`, type: "TXT", expected: `v=DMARC1; p=quarantine; rua=mailto:postmaster@${domain}`, status: good ? "ok" : "missing", hint: "Tells receivers what to do with mail that fails SPF or DKIM." },
      { record: "PTR", host: ip, type: "PTR", expected: `mail.${domain}`, status: "wrong", hint: "Reverse DNS is set at your VPS provider, not your domain registrar." },
    ];
  }
  async smtp() { await wait(150); return { ...this.smtp_ }; }
  async saveSmtp(s: Omit<SmtpSettings, "has_password" | "port25_open"> & { password?: string }) {
    await wait(700);
    if (s.provider !== "direct" && (!s.host || !s.port)) throw new Error("Enter the relay host and port.");
    if (s.provider !== "direct" && !s.username) throw new Error("Enter the relay username.");
    this.smtp_ = { ...this.smtp_, provider: s.provider, host: s.host, port: s.port, username: s.username, has_password: this.smtp_.has_password || !!s.password };
    this.record("PUT", "/v1/mail/smtp");
    return { ...this.smtp_ };
  }
  // ------------------------------------------------ domains, redirects, PHP, HTTPS
  private update(domain: string, patch: Partial<Site>, path: string) {
    const s = this.find(domain);
    Object.assign(s, patch);
    this.record("PUT", `/v1/sites/${domain}/${path}`);
    return { ...s };
  }
  async setDomains(domain: string, r: { aliases: string[]; www: string; redirect_aliases: boolean }) {
    await wait(500);
    for (const a of r.aliases) {
      const plain = a.replace(/^\*\./, "");
      const e = domainError(plain);
      if (e) throw new Error(`${a}: ${e}`);
      if (a.startsWith("*.") && plain.split(".").length < 2) throw new Error(`${a}: a wildcard needs a domain under it.`);
      const owner = this.sites_.find((s) => s.domain !== domain && (s.domain === a || (s.aliases || []).includes(a)));
      if (owner) throw new Error(`${a} is already served by ${owner.domain}.`);
    }
    if (new Set(r.aliases).size !== r.aliases.length) throw new Error("A domain is listed twice.");
    return this.update(domain, { aliases: r.aliases, www: r.www as Site["www"], redirect_aliases: r.redirect_aliases && r.aliases.length > 0 }, "domains");
  }
  async setRedirects(domain: string, rules: Redirect[]) {
    await wait(500);
    rules.forEach((r, i) => {
      const n = `Rule ${i + 1}: `;
      if (r.match !== "regex" && !/^\/[A-Za-z0-9._~%/:@,+=-]*$/.test(r.from)) throw new Error(n + `“${r.from}” must be a path starting with /, without spaces.`);
      if (r.match === "regex") {
        if (!r.from.startsWith("^")) throw new Error(n + "a pattern must start with ^.");
        try { new RegExp(r.from); } catch { throw new Error(n + "the pattern isn't valid."); }
      }
      if (!/^(https?:\/\/[A-Za-z0-9.-]+(:[0-9]{1,5})?)?(\/[A-Za-z0-9._~%/:@,+=&?$-]*)?$/.test(r.to) || !r.to) throw new Error(n + "the target must be a path like /new or a URL like https://example.com/new.");
      if (r.kind === "rewrite" && !r.to.startsWith("/")) throw new Error(n + "a rewrite serves another path on this website, so its target must start with /.");
      if (/\$(?![1-9])/.test(r.to)) throw new Error(n + "the target can only use $1 to $9.");
      r.id ||= hex(4);
      if (r.kind === "redirect") r.code ||= 301;
    });
    return this.update(domain, { redirects: rules }, "redirects");
  }
  async setPHPSettings(domain: string, p: PHPSettings) {
    await wait(500);
    if ((p.post_max_mb || 64) < (p.upload_max_mb || 64)) throw new Error("Post max size must be at least the upload size.");
    if (p.max_children && p.max_children > 50) throw new Error("This account allows at most 50 PHP workers per website.");
    return this.update(domain, { php: p }, "php");
  }
  async runComposer(domain: string, r: { action: string; package?: string; dev?: boolean }) {
    await wait(300);
    const s = this.find(domain);
    if (s.runtime !== "php") throw new Error(`${domain} doesn't use PHP.`);
    if ((r.action === "require" || r.action === "remove") && !/^[a-z0-9]([_.-]?[a-z0-9]+)*\/[a-z0-9]([_.-]?[a-z0-9]+)*(:.+)?$/.test(r.package || "")) throw new Error("Package must look like vendor/name.");
    return this.startJob("composer", `composer ${r.action} on ${domain}`, [`composer ${r.action} in /home/${s.account}/web/${domain} (PHP ${s.version})`,
      "Loading composer repositories with package information", r.action === "require" ? `Using version ^3.2 for ${r.package}` : "Installing dependencies from lock file",
      "Package operations: 3 installs, 12 updates, 0 removals", "Generating optimized autoload files", "No security vulnerability advisories found."]);
  }
  async issueCertificate(domain: string, r: { email?: string; include_aliases?: boolean; wildcard?: boolean }) {
    await wait(300);
    const s = this.find(domain);
    const names = [domain, ...(s.www === "none" ? [] : [`www.${domain}`]), ...(r.wildcard ? [`*.${domain}`] : []), ...(r.include_aliases ? (s.aliases || []).filter((a) => !a.startsWith("*.") || r.wildcard) : [])];
    return this.startJob("ssl", `Get an HTTPS certificate for ${domain}`, [...names.map((n) => `${n} points to 203.0.113.24`),
      `Asking Let's Encrypt for a certificate for ${names.join(", ")}`, "Requesting a certificate for " + names[0], "Successfully received certificate.",
      `HTTPS is on for ${domain}`], () => {
      s.ssl = { provider: "letsencrypt", names, issuer: "R11", issued_at: now(), expires_at: ago(-90 * day), force_https: true, hsts: false, http2: true, http3: false };
    });
  }
  async uploadCertificate(domain: string, r: { certificate: string; private_key: string }) {
    await wait(600);
    if (!r.certificate.includes("BEGIN CERTIFICATE")) throw new Error("Paste the certificate in PEM format (-----BEGIN CERTIFICATE-----).");
    if (!/BEGIN (EC |RSA )?PRIVATE KEY/.test(r.private_key)) throw new Error("Paste the private key in PEM format.");
    return this.update(domain, { ssl: { provider: "custom", names: [domain, `www.${domain}`], issuer: "Uploaded", issued_at: now(), expires_at: ago(-365 * day), force_https: true, hsts: false, http2: true, http3: false } }, "ssl/custom");
  }
  async updateSSL(domain: string, o: Partial<Pick<SSLSettings, "force_https" | "hsts" | "http2" | "http3">>) {
    await wait(400);
    const s = this.find(domain);
    if (!s.ssl) throw new Error(`${domain} has no certificate yet.`);
    const next = { ...s.ssl, ...o };
    if (next.hsts && !next.force_https) throw new Error("HSTS needs Force HTTPS: browsers would otherwise refuse the plain HTTP version.");
    return this.update(domain, { ssl: next }, "ssl");
  }
  async removeSSL(domain: string) { await wait(500); return this.update(domain, { ssl: undefined }, "ssl"); }
  async certificates(): Promise<CertInfo[]> {
    await wait(200);
    return this.sites_.filter((s) => s.ssl).map((s) => {
      const days = Math.floor((new Date(s.ssl!.expires_at).getTime() - Date.now()) / 86400000);
      const problem = days < 0 ? "Expired. Visitors see a security warning." : s.ssl!.provider === "custom" && days < 21 ? "Expires soon. Upload a renewed certificate." :
        s.ssl!.provider === "letsencrypt" && days < 20 ? "Automatic renewal seems to be failing." : undefined;
      return { domain: s.domain, provider: s.ssl!.provider, names: s.ssl!.names, issuer: s.ssl!.issuer || "", expires_at: s.ssl!.expires_at, days_left: days, problem };
    });
  }
  private exts: Record<string, string[]> = { "8.3": ["curl", "gd", "intl", "mbstring", "mysql", "opcache", "xml", "zip", "redis", "imagick", "bcmath"], "8.4": ["curl", "gd", "mbstring", "mysql", "opcache", "xml", "zip"] };
  async phpExtensions(version: string): Promise<PHPExtension[]> {
    await wait(300);
    const all: Record<string, string> = { apcu: "In-memory user cache", bcmath: "Arbitrary precision maths", curl: "HTTP requests", gd: "Image processing (GD)",
      gmp: "Big integer maths", igbinary: "Compact serializer", imagick: "Image processing (ImageMagick)", imap: "IMAP mail", intl: "Internationalization, number and date formatting",
      ldap: "LDAP directory", mbstring: "Multibyte strings", memcached: "Memcached client", mongodb: "MongoDB driver", mysql: "MySQL and MariaDB (mysqli, PDO)",
      opcache: "Bytecode cache (faster PHP)", pgsql: "PostgreSQL (pgsql, PDO)", redis: "Redis and Valkey client", soap: "SOAP web services", sqlite3: "SQLite",
      ssh2: "SSH and SFTP client", swoole: "Async server (Swoole)", xdebug: "Debugger and profiler (not for production)", xml: "XML, DOM and XSL", yaml: "YAML parsing", zip: "ZIP archives" };
    const on = this.exts[version] || ["curl", "mbstring", "mysql", "opcache", "xml"];
    return Object.entries(all).map(([name, description]) => ({ name, description, package: `php${version}-${name}`, installed: on.includes(name) }));
  }
  async changePHPExtension(version: string, name: string, install: boolean) {
    await wait(300);
    const pkg = `php${version}-${name}`;
    return this.startJob("php-ext", `${install ? "Install" : "Remove"} PHP ${version} ${name}`,
      [install ? `Installing ${pkg}` : `Removing ${pkg}`, "Reading package lists...", install ? `Setting up ${pkg} ...` : `Removing ${pkg} ...`, `PHP ${version} reloaded`], () => {
        const l = (this.exts[version] ||= []);
        if (install && !l.includes(name)) l.push(name);
        if (!install) this.exts[version] = l.filter((x) => x !== name);
      });
  }

  // ------------------------------------------------------------ deployments
  private deps: Record<string, Deployment[]> = {
    "api.kelechi.dev": [
      { id: "d5", domain: "api.kelechi.dev", trigger: "push", actor: "git", ref_type: "branch", ref: "main", commit: "9f3c2ab41d7e0c55e1a0b9a6d1e3f20c88a2b7d4", message: "Add Paystack webhook retries", author: "Kelechi Nwosu", release: "20261012-181502-9f3c2ab", status: "live", queued_at: ago(18 * 60), started_at: ago(18 * 60), finished_at: ago(18 * 60 - 2) },
      { id: "d4", domain: "api.kelechi.dev", trigger: "push", actor: "git", ref_type: "branch", ref: "main", commit: "0c1d9be7712f4e44a1bd3b0f6c2d9e8a7b6c5d4e", message: "Bump express to 5.1", author: "Kelechi Nwosu", release: "20261012-110322-0c1d9be", status: "failed", error: "the new release didn't answer on /health, so traffic stayed on the old one: HTTP 502", queued_at: ago(25 * 60), started_at: ago(25 * 60), finished_at: ago(25 * 60 - 3) },
      { id: "d3", domain: "api.kelechi.dev", trigger: "manual", actor: "kelechi", ref_type: "branch", ref: "main", commit: "5e8a7c31f0b2d4e6a8c0e2f4b6d8a0c2e4f6a8b0", message: "Cache exchange rates for 10 minutes", author: "Ada Obi", release: "20261011-160055-5e8a7c3", status: "replaced", queued_at: ago(26 * 60 + 30), started_at: ago(26 * 60 + 30), finished_at: ago(26 * 60 + 28) },
      { id: "d2", domain: "api.kelechi.dev", trigger: "preview", actor: "git", ref_type: "commit", ref: "c4d5e6f", commit: "c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f7", message: "WIP: naira wallet", author: "Ada Obi", release: "20261011-091200-c4d5e6f", status: "replaced", queued_at: ago(33 * 60), started_at: ago(33 * 60), finished_at: ago(33 * 60 - 2) },
    ],
    "docs.kelechi.dev": [
      { id: "s1", domain: "docs.kelechi.dev", trigger: "push", actor: "git", ref_type: "tag", ref: "v*", commit: "71aa0de5c3b2a1908f7e6d5c4b3a29180f7e6d5c", message: "Release v1.4.0", author: "Kelechi Nwosu", release: "20261010-120000-71aa0de", status: "live", queued_at: ago(3 * day), started_at: ago(3 * day), finished_at: ago(3 * day - 1) },
    ],
    "status.kelechi.dev": [
      { id: "c1", domain: "status.kelechi.dev", trigger: "manual", actor: "kelechi", image: "louislam/uptime-kuma:1", release: "20261012-090000", status: "live", queued_at: ago(day), started_at: ago(day), finished_at: ago(day - 1) },
    ],
  };
  private depLogs: Record<string, string> = {
    d5: "18:15:02  Fetching git@github.com:kelechi/api.git\n18:15:04  Commit 9f3c2ab by Kelechi Nwosu: Add Paystack webhook retries\n18:15:04  Files exported to releases/20261012-181502-9f3c2ab\n18:15:04  Shared: .env\n18:15:04  $ npm ci\n18:15:31  added 214 packages in 26s\n18:15:31  $ npm run build\n18:15:44  Starting the new release on port 3010\n18:15:47  Traffic moved to the new release; stopping the old instance\n18:15:47  Live in 45s",
    d4: "11:03:22  Fetching git@github.com:kelechi/api.git\n11:03:24  Commit 0c1d9be by Kelechi Nwosu: Bump express to 5.1\n11:03:24  $ npm ci\n11:03:51  $ npm run build\n11:04:02  Starting the new release on port 3001\n11:05:32  Failed: the new release didn't answer on /health, so traffic stayed on the old one: HTTP 502",
  };
  private deployKeys: Record<string, string> = { "api.kelechi.dev": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHk3mY0XbqfE0m9oVwR5yPpx2s1Lr8tQd7c4Ez6nB2aF ike-deploy@api.kelechi.dev" };
  private info(s: Site, secret?: string): DeployInfo {
    const g = s.deploy?.git;
    const how: Record<string, string[]> = {
      github: ["In the repository on GitHub: Settings → Deploy keys → Add deploy key. Paste the key; leave write access off.",
        "Settings → Webhooks → Add webhook. Payload URL: the webhook URL, content type application/json, secret: the webhook secret. Choose 'Just the push event' (add Pull requests for previews)."],
      gitlab: ["In the project on GitLab: Settings → Webhooks → Add new webhook. URL: the webhook URL, Secret token: the webhook secret. Tick Push events and Tag push events."],
      bitbucket: ["Repository settings → Webhooks → Add webhook. URL: the webhook URL, Secret: the webhook secret. Trigger: Repository push."],
      other: ["Have your Git host POST to the webhook URL on each push, signed with the webhook secret (X-Ike-Signature: sha256=<HMAC>)."],
    };
    return { deploy: s.deploy ?? null, deploy_key: g?.auth === "deploy_key" ? this.deployKeys[s.domain] ||= "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI" + hex(16) + " ike-deploy@" + s.domain : undefined,
      has_token: g?.auth === "token", webhook_path: `/webhooks/git/${s.domain}`, webhook_secret: secret, instructions: g ? how[g.provider || "other"] : [] };
  }
  async deployInfo(domain: string) { await wait(200); return this.info(this.find(domain)); }
  async configureDeploy(domain: string, set: DeploySetup) {
    await wait(600);
    const s = this.find(domain);
    if (set.type === "image") {
      if (!/^[a-z0-9]+([._-][a-z0-9]+)*(\/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?$/.test(set.image || "")) throw new Error("Image must look like nginx:1.27 or ghcr.io/owner/app:latest.");
    } else {
      const u = set.git?.url || "";
      if (!/^https:\/\/[A-Za-z0-9.-]+\/[A-Za-z0-9._~/-]+$/.test(u) && !/^[A-Za-z0-9._-]+@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+$/.test(u)) throw new Error("Repository must be an https:// URL or an SSH address like git@github.com:owner/repo.git.");
      if (set.git!.auth === "deploy_key" && u.startsWith("https://")) throw new Error("Deploy keys work with SSH addresses; use a token for https:// URLs.");
      if (set.git!.auth === "token" && !u.startsWith("https://")) throw new Error("Tokens work with https:// URLs; use a deploy key for SSH addresses.");
      if (set.git!.auth === "token" && !set.token && s.deploy?.git?.auth !== "token") throw new Error("Paste the access token for this repository.");
      set.git!.provider = u.includes("github.com") ? "github" : u.includes("gitlab") ? "gitlab" : u.includes("bitbucket.org") ? "bitbucket" : "other";
    }
    if (set.strategy === "blue-green" && !(isAppRt(s.runtime) || s.runtime === "docker")) throw new Error("PHP and static websites already switch releases instantly; blue-green is for apps.");
    const first = !s.deploy;
    const { token: _t, ...rest } = set;
    void _t;
    s.deploy = { ...s.deploy, ...rest, strategy: set.strategy || (s.runtime === "docker" ? "blue-green" : "restart"), keep_releases: set.keep_releases || 5, health_path: set.health_path || "/" } as Site["deploy"];
    this.record("PUT", `/v1/sites/${domain}/deploy`);
    return this.info(s, first ? hex(24) : undefined);
  }
  async disconnectDeploy(domain: string) { await wait(400); const s = this.find(domain); s.deploy = s.deploy?.current ? { ...s.deploy, git: undefined, previews: undefined } : undefined; }
  async regenerateDeployKey(domain: string) { await wait(400); this.deployKeys[domain] = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI" + hex(16) + " ike-deploy@" + domain; return this.info(this.find(domain)); }
  async regenerateWebhookSecret(domain: string) { await wait(400); return this.info(this.find(domain), hex(24)); }
  async deployments(domain: string) { await wait(250); return [...(this.deps[domain] || [])]; }
  async deploy(domain: string, r?: { ref_type?: string; ref?: string }) {
    await wait(300);
    const s = this.find(domain);
    if (!s.deploy?.git && s.deploy?.type !== "image") throw new Error(`${domain} isn't connected to a repository or image yet.`);
    const sha = hex(20);
    const rel = new Date().toISOString().replace(/[-:]/g, "").slice(0, 15).replace("T", "-") + (s.deploy.git ? "-" + sha.slice(0, 7) : "");
    const d: Deployment = { id: hex(6), domain, trigger: "manual", actor: "andre", ref_type: r?.ref_type || s.deploy.git?.ref_type, ref: r?.ref || s.deploy.git?.ref,
      image: s.deploy.image, status: "queued", queued_at: now() };
    (this.deps[domain] ||= []).unshift(d);
    const lines = s.deploy.type === "image" ? [`Pulling ${s.deploy.image}`, "Status: Image is up to date", `Starting container on port ${s.deploy.color === "b" ? s.port : s.deploy.alt_port}`, "Traffic moved; removing the old container"]
      : [`Fetching ${s.deploy.git!.url}`, `Commit ${sha.slice(0, 7)} by Ada Obi: Update copy on the pricing page`, `Files exported to releases/${rel}`,
        ...(s.deploy.shared_paths || []).map((p) => `Shared: ${p}`), ...(s.runtime === "php" ? ["$ composer install --no-dev --no-interaction --prefer-dist --optimize-autoloader", "Generating optimized autoload files"] :
          s.runtime === "go" ? ["$ go build -o app ."] : ["$ npm ci", "added 214 packages in 21s", "$ npm run build"]),
        s.deploy.strategy === "blue-green" ? "Traffic moved to the new release; stopping the old instance" : `Switched to release ${rel}`];
    let t = 600;
    setTimeout(() => { d.status = "building"; d.started_at = now(); d.commit = s.deploy?.git ? sha : undefined; d.message = s.deploy?.git ? "Update copy on the pricing page" : undefined; d.author = s.deploy?.git ? "Ada Obi" : undefined; }, t);
    lines.forEach((l, i) => setTimeout(() => { this.depLogs[d.id] = (this.depLogs[d.id] ? this.depLogs[d.id] + "\n" : "") + new Date().toTimeString().slice(0, 8) + "  " + l; }, (t += 700) + i));
    setTimeout(() => {
      d.status = "live"; d.release = rel; d.finished_at = now();
      (this.deps[domain] || []).forEach((x) => { if (x !== d && x.status === "live") x.status = "replaced"; });
      if (s.deploy) { s.deploy.current = rel; if (s.deploy.strategy === "blue-green") s.deploy.color = s.deploy.color === "b" ? "a" : "b"; }
    }, t + 800);
    this.record("POST", `/v1/sites/${domain}/deployments`, 202);
    return { ...d };
  }
  async deployment(domain: string, id: string) {
    await wait(150);
    const d = (this.deps[domain] || []).find((x) => x.id === id);
    if (!d) throw new Error("Deployment not found.");
    return { deployment: { ...d }, log: this.depLogs[id] || (d.status === "queued" ? "" : "(log rotated)") };
  }
  async rollback(domain: string, id: string) {
    await wait(300);
    const s = this.find(domain);
    const old = (this.deps[domain] || []).find((x) => x.id === id);
    if (!old?.release) throw new Error("That deployment didn't produce a release to go back to.");
    if (s.deploy?.current === old.release) throw new Error("That release is already live.");
    const d: Deployment = { ...old, id: hex(6), trigger: "rollback", actor: "andre", status: "live", rollback_of: old.release, queued_at: now(), started_at: now(), finished_at: now() };
    (this.deps[domain] || []).forEach((x) => { if (x.status === "live") x.status = "replaced"; });
    this.deps[domain].unshift(d);
    this.depLogs[d.id] = `${new Date().toTimeString().slice(0, 8)}  Rolling back to release ${old.release}\n${new Date().toTimeString().slice(0, 8)}  Switched to release ${old.release}`;
    if (s.deploy) s.deploy.current = old.release;
    return { ...d };
  }
  async cancelDeployment(domain: string, id: string) { await wait(200); const d = (this.deps[domain] || []).find((x) => x.id === id); if (d && d.status === "queued") d.status = "cancelled"; }
  private maxBuilds = 1;
  async deployQueue(): Promise<QueueStatus> {
    await wait(200);
    const all = Object.values(this.deps).flat();
    return { settings: { max_concurrent: this.maxBuilds }, running: all.filter((d) => d.status === "building"), queued: all.filter((d) => d.status === "queued") };
  }
  async setDeploySettings(max: number) { await wait(300); if (max < 1 || max > 8) throw new Error("Run 1-8 builds at once."); this.maxBuilds = max; }

  // ------------------------------------------------------------- containers
  private ctrs: Container[] = [
    { name: "ike-kelechi-c-redis", short: "redis", image: "redis:7.4-alpine", state: "running", status: "Up 3 days (healthy)", ports: "127.0.0.1:16379->6379/tcp", created: ago(3 * day), account: "kelechi" },
    { name: "ike-kelechi-c-meilisearch", short: "meilisearch", image: "getmeili/meilisearch:v1.15", state: "exited", status: "Exited (0) 2 hours ago", ports: "", created: ago(2 * day), account: "kelechi" },
    { name: "ike-kelechi-automation-n8n-1", short: "ike-kelechi-automation-n8n-1", image: "n8nio/n8n:1.110", state: "running", status: "Up 26 hours", ports: "127.0.0.1:5678->5678/tcp", created: ago(day), account: "kelechi", stack: "ike-kelechi-automation" },
    { name: "ike-kelechi-automation-postgres-1", short: "ike-kelechi-automation-postgres-1", image: "postgres:17", state: "running", status: "Up 26 hours (healthy)", ports: "", created: ago(day), account: "kelechi", stack: "ike-kelechi-automation" },
    { name: "ike-kelechi-1f0c2e9a-a", short: "ike-kelechi-1f0c2e9a-a", image: "ike/kelechi-1f0c2e9a:20261012-090000", state: "running", status: "Up 22 hours", ports: "127.0.0.1:3005->3001/tcp", created: ago(day), account: "kelechi", site: "status.kelechi.dev" },
  ];
  private stacks_: Record<string, StackInfo[]> = { kelechi: [{ name: "automation", account: "kelechi", status: "running(2)" }] };
  private composeSrc: Record<string, string> = {
    "kelechi/automation": "services:\n  n8n:\n    image: n8nio/n8n:1.110\n    ports: [\"5678:5678\"]\n    environment:\n      DB_TYPE: postgresdb\n      DB_POSTGRESDB_HOST: postgres\n    volumes: [\"n8n:/home/node/.n8n\"]\n  postgres:\n    image: postgres:17\n    environment:\n      POSTGRES_PASSWORD: ${PG_PASSWORD}\n    volumes: [\"pg:/var/lib/postgresql/data\"]\nvolumes:\n  n8n: {}\n  pg: {}\n",
  };
  private vols: Record<string, string[]> = { kelechi: ["redis-data", "meili-data"] };
  private regs: Record<string, string[]> = { kelechi: ["ghcr.io"] };
  private needDocker(account: string) { this.acct(account); }
  async containers(account: string) { await wait(250); this.needDocker(account); return this.ctrs.filter((c) => c.account === account); }
  async createContainer(account: string, spec: ContainerSpec) {
    await wait(900);
    this.needDocker(account);
    if (!/^[a-z0-9][a-z0-9_.-]{0,39}$/.test(spec.name)) throw new Error("Container name must be lowercase letters, numbers, dots, dashes or underscores.");
    if (!/^[a-z0-9]+([._-][a-z0-9]+)*(\/[a-z0-9]+([._-][a-z0-9]+)*)*(:[A-Za-z0-9_][A-Za-z0-9_.-]{0,127})?$/.test(spec.image)) throw new Error("Image must look like nginx:1.27 or ghcr.io/owner/app:1.0.");
    if (this.ctrs.some((c) => c.name === `ike-${account}-c-${spec.name}`)) throw new Error(`Container ${spec.name} already exists.`);
    for (const p of spec.ports || []) if (p.host_port && p.host_port < 1024) throw new Error("Host ports below 1024 are reserved for the server.");
    const c: Container = { name: `ike-${account}-c-${spec.name}`, short: spec.name, image: spec.image, state: "running", status: "Up 1 second", created: now(), account,
      ports: (spec.ports || []).map((p) => `127.0.0.1:${p.host_port || 49153}->${p.container_port}/${p.protocol || "tcp"}`).join(", ") };
    this.ctrs.unshift(c);
    this.record("POST", `/v1/accounts/${account}/containers`, 201);
    return c;
  }
  async containerAction(account: string, name: string, action: string) {
    await wait(500);
    const c = this.ctrs.find((x) => x.account === account && x.short === name);
    if (!c) throw new Error("Container not found.");
    if (action === "remove") this.ctrs = this.ctrs.filter((x) => x !== c);
    else { c.state = action === "stop" ? "exited" : "running"; c.status = action === "stop" ? "Exited (0) just now" : "Up 1 second"; }
    this.record("POST", `/v1/accounts/${account}/containers/${name}/${action}`);
  }
  async containerLogs(_account: string, name: string) {
    await wait(300);
    return name === "redis" ? "2026-10-13T09:12:01Z 1:M * Ready to accept connections tcp\n2026-10-13T10:12:02Z 1:M * 1 changes in 3600 seconds. Saving...\n2026-10-13T10:12:02Z 1:M * Background saving terminated with success"
      : "2026-10-13T08:00:01Z Starting...\n2026-10-13T08:00:03Z Listening on 0.0.0.0:7700";
  }
  async containerExec(_account: string, name: string, command: string) {
    await wait(500);
    if (command.trim() === "") throw new Error("Type a command.");
    return name === "redis" && command.includes("ping") ? "PONG" : `$ ${command}\n(preview) the command would run inside ${name}`;
  }
  async stacks(account: string) { await wait(200); return [...(this.stacks_[account] || [])]; }
  async stack(account: string, name: string) {
    await wait(250);
    const s = (this.stacks_[account] || []).find((x) => x.name === name);
    if (!s) throw new Error("Stack not found.");
    return { ...s, compose: this.composeSrc[`${account}/${name}`] || "", services: this.ctrs.filter((c) => c.stack === `ike-${account}-${name}`)
      .map((c) => ({ service: c.name.split("-").slice(-2, -1)[0], name: c.name, image: c.image, state: c.state, status: c.status, ports: c.ports })) };
  }
  async saveStack(account: string, name: string, compose: string) {
    await wait(300);
    if (!/^[a-z0-9][a-z0-9_.-]{0,39}$/.test(name)) throw new Error("Stack names are lowercase letters, numbers, dots, dashes or underscores.");
    if (!/^services:/m.test(compose)) throw new Error("compose.yaml has a problem: services must be a mapping.");
    if (/privileged:\s*true/.test(compose)) throw new Error("service: privileged containers aren't allowed.");
    if (/network_mode:\s*["']?host/.test(compose)) throw new Error("service: network_mode: host isn't allowed.");
    if (/docker\.sock/.test(compose)) throw new Error("service: only folders in /home/" + account + "/ can be mounted.");
    this.composeSrc[`${account}/${name}`] = compose;
    return this.startJob("stack", `Start stack ${name}`, ["Checking compose.yaml", "Ports kept on 127.0.0.1; add a website with the proxy runtime to publish one.", "Starting the stack", " Network ike-" + account + "-" + name + "_default  Created", " Container started", `Stack ${name} is up`], () => {
      const l = (this.stacks_[account] ||= []);
      if (!l.some((x) => x.name === name)) l.push({ name, account, status: "running(1)" });
    });
  }
  async stackAction(account: string, name: string, action: string) {
    await wait(300);
    const s = (this.stacks_[account] || []).find((x) => x.name === name);
    if (!s) throw new Error("Stack not found.");
    return this.startJob("stack", `Stack ${name}: ${action}`, action === "pull" ? ["Pulling images", "n8n Pulled", "postgres Pulled", "Recreating changed containers", "Done"] : [`docker compose ${action}`, "Done"],
      () => { s.status = action === "down" ? "exited" : "running(2)"; });
  }
  async deleteStack(account: string, name: string) { await wait(500); this.stacks_[account] = (this.stacks_[account] || []).filter((x) => x.name !== name); }
  async volumes(account: string) { await wait(200); return (this.vols[account] || []).map((v) => ({ name: `ike-${account}-${v}`, short: v })); }
  async createVolume(account: string, name: string) {
    await wait(300);
    if (!/^[a-z0-9][a-z0-9_.-]{0,39}$/.test(name)) throw new Error("Volume names are lowercase letters, numbers, dots, dashes or underscores.");
    (this.vols[account] ||= []).push(name);
  }
  async removeVolume(account: string, name: string) { await wait(300); this.vols[account] = (this.vols[account] || []).filter((v) => v !== name); }
  async registries(account: string) { await wait(150); return [...(this.regs[account] || [])]; }
  async addRegistry(account: string, r: { registry: string; username: string; password: string }) {
    await wait(700);
    if (!/^[a-z0-9.-]+(:[0-9]{1,5})?$/.test(r.registry) || !r.username || !r.password) throw new Error("Give the registry host, a username and a password or token.");
    (this.regs[account] ||= []).push(r.registry);
  }
  async removeRegistry(account: string, reg: string) { await wait(300); this.regs[account] = (this.regs[account] || []).filter((x) => x !== reg); }
  async dockerOverview(): Promise<DockerOverview> {
    await wait(300);
    return { installed: true, version: "28.4.0", containers: this.ctrs.length, running: this.ctrs.filter((c) => c.state === "running").length, for_accounts: true,
      images: [
        { repository: "n8nio/n8n", tag: "1.110", id: "3f2a1b0c9d8e", size: "1.13GB", created: "9 days ago" },
        { repository: "postgres", tag: "17", id: "a1b2c3d4e5f6", size: "438MB", created: "3 weeks ago" },
        { repository: "louislam/uptime-kuma", tag: "1", id: "0f9e8d7c6b5a", size: "462MB", created: "2 months ago" },
        { repository: "redis", tag: "7.4-alpine", id: "5e4d3c2b1a09", size: "41.4MB", created: "5 weeks ago" },
        { repository: "getmeili/meilisearch", tag: "v1.15", id: "9a8b7c6d5e4f", size: "176MB", created: "6 weeks ago" },
      ] };
  }
  async allContainers() { await wait(250); return [...this.ctrs]; }
  async pruneDocker() { await wait(1200); return "Deleted build cache objects: 14\nTotal reclaimed space: 1.82GB"; }
  async imageUpdates(): Promise<ImageUpdate[]> {
    await wait(200);
    return [
      { image: "n8nio/n8n:1.110", containers: ["ike-kelechi-automation-n8n-1"], update_available: true, checked_at: ago(300) },
      { image: "postgres:17", containers: ["ike-kelechi-automation-postgres-1"], update_available: false, checked_at: ago(300) },
      { image: "redis:7.4-alpine", containers: ["ike-kelechi-c-redis"], update_available: true, checked_at: ago(300) },
    ];
  }
  async checkImageUpdates() { await wait(300); return this.startJob("image-updates", "Check container images for updates", ["Update available: n8nio/n8n:1.110", "Update available: redis:7.4-alpine", "Checked 3 images"]); }
  async kubeStatus(): Promise<KubeStatus> {
    await wait(400);
    return { installed: true, nodes: [{ name: "vps-lagos-1", ready: true, version: "v1.33.4+k3s1", roles: "control-plane,master" }], namespaces: ["default", "kube-system", "shop"] };
  }
  async kubeWorkloads(): Promise<KubeWorkloads> {
    await wait(500);
    return {
      workloads: [
        { kind: "Deployment", namespace: "shop", name: "storefront", ready: 3, desired: 3, images: ["ghcr.io/webafrica/storefront:2.8.1"] },
        { kind: "Deployment", namespace: "shop", name: "worker", ready: 1, desired: 2, images: ["ghcr.io/webafrica/worker:2.8.1"] },
        { kind: "StatefulSet", namespace: "shop", name: "valkey", ready: 1, desired: 1, images: ["valkey/valkey:8"] },
        { kind: "Deployment", namespace: "kube-system", name: "coredns", ready: 1, desired: 1, images: ["rancher/mirrored-coredns-coredns:1.12.0"] },
      ],
      pods: [
        { namespace: "shop", name: "storefront-7c9d6b5f4-2xk8p", phase: "Running", restarts: 0, node: "vps-lagos-1" },
        { namespace: "shop", name: "worker-5b8c7d6e9-q4wzn", phase: "Running", restarts: 4, node: "vps-lagos-1" },
        { namespace: "shop", name: "worker-5b8c7d6e9-7hjks", phase: "Pending", restarts: 0, node: "" },
      ],
      services: [{ namespace: "shop", name: "storefront", type: "NodePort", ports: ["80/TCP → node 30080"] }],
    };
  }
  async kubeApply(manifest: string) {
    await wait(700);
    const kinds = [...manifest.matchAll(/^kind:\s*(\w+)/gm)].map((m) => m[1]);
    const names = [...manifest.matchAll(/^\s{2}name:\s*([\w.-]+)/gm)].map((m) => m[1]);
    if (!kinds.length) throw new Error("error: no objects passed to apply");
    return kinds.map((k, i) => `${k.toLowerCase()}/${names[i] || "unnamed"} configured`).join("\n");
  }
  async kubeAction(_kind: string, _ns: string, _name: string, action: string, replicas?: number) {
    await wait(500);
    if (action === "scale" && (replicas === undefined || replicas < 0 || replicas > 100)) throw new Error("Replicas must be 0-100.");
  }

}

export function runtimeName(r: string) {
  return ({ php: "PHP", node: "Node.js", python: "Python", java: "Java", go: "Go", dart: "Dart", static: "Static", proxy: "Proxy", docker: "Container" } as Record<string, string>)[r] || r;
}

// --------------------------------------------------------------- cron text

const cronField = /^(\*|\d{1,2}(-\d{1,2})?)(\/\d{1,2})?(,(\*|\d{1,2}(-\d{1,2})?)(\/\d{1,2})?)*$/;
export function cronError(s: string): string | null {
  const t = s.trim();
  if (/^@(hourly|daily|weekly|monthly|yearly|annually|reboot)$/.test(t)) return null;
  const f = t.split(/\s+/);
  if (f.length !== 5) return "Use 5 fields: minute hour day month weekday, e.g. 30 2 * * *";
  const max = [59, 23, 31, 12, 7];
  for (let i = 0; i < 5; i++) {
    if (!cronField.test(f[i]) && !(i >= 3 && /^[a-z]{3}(-[a-z]{3})?$/i.test(f[i]))) return `Field ${i + 1} (“${f[i]}”) isn't valid.`;
    const nums = (f[i].match(/\d+/g) || []).map(Number);
    if (!f[i].includes("/") && nums.some((n) => n > max[i])) return `Field ${i + 1} is out of range.`;
  }
  return null;
}

const DOW = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];
const pad = (n: string) => n.padStart(2, "0");

/** Plain-words description of common schedules; falls back to the raw expression. */
export function describeCron(s: string): string {
  const t = s.trim();
  const macros: Record<string, string> = {
    "@hourly": "Every hour", "@daily": "Every day at midnight", "@weekly": "Every Sunday at midnight",
    "@monthly": "On the 1st of every month", "@yearly": "Every 1 January", "@annually": "Every 1 January", "@reboot": "When the server starts",
  };
  if (macros[t]) return macros[t];
  const [mi, h, dom, mon, dow] = t.split(/\s+/);
  if ([mi, h, dom, mon, dow].some((x) => x === undefined)) return t;
  const at = /^\d+$/.test(h) && /^\d+$/.test(mi) ? `at ${pad(h)}:${pad(mi)}` : "";
  if (mi === "*" && h === "*" && dom === "*" && mon === "*" && dow === "*") return "Every minute";
  if (/^\*\/\d+$/.test(mi) && h === "*" && dom === "*" && mon === "*" && dow === "*") return `Every ${mi.slice(2)} minutes`;
  if (/^\d+$/.test(mi) && h === "*" && dom === "*" && mon === "*" && dow === "*") return mi === "0" ? "Every hour, on the hour" : `Every hour at minute ${mi}`;
  if (/^\d+$/.test(mi) && /^\*\/\d+$/.test(h) && dom === "*" && mon === "*" && dow === "*") return `Every ${h.slice(2)} hours`;
  if (at && dom === "*" && mon === "*" && dow === "*") return `Every day ${at}`;
  if (at && dom === "*" && mon === "*" && dow === "1-5") return `Weekdays ${at}`;
  if (at && dom === "*" && mon === "*" && /^\d$/.test(dow)) return `Every ${DOW[+dow]} ${at}`;
  if (at && /^\d+$/.test(dom) && mon === "*" && dow === "*") return `Monthly on day ${dom} ${at}`;
  return t;
}

/** PHP releases that no longer get security fixes. */
export function phpEol(v: string): boolean {
  const [a, b] = v.split(".").map(Number);
  return a < 8 || (a === 8 && b <= 1);
}
