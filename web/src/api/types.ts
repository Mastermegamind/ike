// Shapes mirror the ike-agent JSON API (agent/internal/api).

export type SSHMode = "none" | "sftp" | "shell";
export type Runtime = "php" | "node" | "python" | "static";
export type WebServer = "nginx" | "apache" | "nginx+apache";
export type SiteStatus = "active" | "maintenance" | "suspended";
export type Role = "admin" | "reseller" | "user";

export interface Account {
  name: string;
  ssh: SSHMode;
  suspended: boolean;
  suspend_reason?: string;
  sites: number;
  created_at: string;
  owner?: string; // reseller that owns this account (control plane)
  plan?: string;
}

export interface Site {
  domain: string;
  account: string;
  runtime: Runtime;
  version?: string;
  web_server: WebServer;
  doc_root: string;
  port?: number;
  start_command?: string;
  status: SiteStatus;
  allow_ips?: string[];
  created_at: string;
}

export interface CreateSiteRequest {
  domain: string;
  account: string;
  runtime?: Runtime;
  version?: string;
  web_server?: WebServer;
  start_command?: string;
}

export interface SSHKey { type: string; fingerprint: string; comment: string }

export interface SystemInfo {
  hostname: string;
  os: string;
  os_version: string;
  kernel: string;
  arch: string;
  cpus: number;
  uptime_seconds: number;
  load_average: string[];
  memory_total_mb: number;
  memory_available_mb: number;
  disk_total_gb: number;
  disk_free_gb: number;
  php_versions: string[];
  node_versions: string[];
  python_versions: string[];
}

export interface Runtimes {
  php: string[];
  node: string[];
  python: string[];
  default_php: string;
  web_stack: "nginx" | "apache" | "hybrid";
  web_servers: WebServer[];
}

export interface ServiceStatus { name: string; active: string }

export interface AuditEntry {
  time: string;
  actor: string; // "andre", "reseller:acme", "ai:mcp"
  method: string;
  path: string;
  status: number;
  result: "ok" | "error" | "denied";
}

export interface CronJob { id: string; schedule: string; command: string; comment?: string; enabled: boolean }

export interface Database { name: string; user: string; note?: string; created_at: string }
export interface NewDatabase extends Database { password: string; host: string }

export interface CatalogApp { id: string; name: string; description: string; runtime: Runtime; available: boolean }
export interface WordPressResult {
  url: string; admin_url: string; admin_user: string; admin_password: string; database: NewDatabase;
}

export interface FirewallPort { port: string; proto: "tcp" | "udp"; comment?: string; protected: boolean }
export interface FirewallBlock { address: string; comment?: string; added_at: string }
export interface Ban { jail: string; ip: string }

export type LogKind = "access" | "error" | "php" | "apache-access" | "apache-error" | "app";

// ---- Email (backend lands in roadmap phase 3; the UI runs on demo data until then)

export interface Mailbox { address: string; quota_mb: number; used_mb: number; created_at: string }
export interface Forwarder { from: string; to: string }
export interface DnsCheck {
  record: "MX" | "SPF" | "DKIM" | "DMARC" | "PTR";
  host: string; type: string; expected: string; status: "ok" | "missing" | "wrong"; hint: string;
}
export type RelayProvider = "direct" | "brevo" | "mailgun" | "ses" | "custom";
export interface SmtpSettings {
  provider: RelayProvider; host: string; port: number; username: string; has_password: boolean; port25_open: boolean;
}
export interface MailDomain { domain: string; webmail_url: string }

export interface Api {
  login(username: string, password: string): Promise<{ name: string }>;
  system(): Promise<SystemInfo>;
  services(): Promise<ServiceStatus[]>;
  runtimes(): Promise<Runtimes>;

  accounts(): Promise<Account[]>;
  createAccount(name: string): Promise<Account>;
  deleteAccount(name: string, withSites: boolean): Promise<void>;
  suspendAccount(name: string, reason: string): Promise<Account>;
  unsuspendAccount(name: string): Promise<Account>;
  setSSH(account: string, mode: SSHMode): Promise<Account>;
  sshKeys(account: string): Promise<SSHKey[]>;
  addSSHKey(account: string, publicKey: string): Promise<SSHKey>;
  removeSSHKey(account: string, fingerprint: string): Promise<void>;
  cron(account: string): Promise<CronJob[]>;
  addCron(account: string, job: Omit<CronJob, "id">): Promise<CronJob>;
  updateCron(account: string, id: string, job: Omit<CronJob, "id">): Promise<CronJob>;
  deleteCron(account: string, id: string): Promise<void>;
  databases(account: string): Promise<Database[]>;
  createDatabase(account: string, name: string, note: string): Promise<NewDatabase>;
  deleteDatabase(account: string, name: string): Promise<void>;

  sites(): Promise<Site[]>;
  site(domain: string): Promise<Site>;
  createSite(req: CreateSiteRequest): Promise<Site>;
  updateSite(domain: string, patch: { version?: string; web_server?: WebServer; start_command?: string }): Promise<Site>;
  deleteSite(domain: string, removeFiles: boolean): Promise<void>;
  setStatus(domain: string, status: SiteStatus, allowIps: string[]): Promise<Site>;
  appAction(domain: string, action: "start" | "stop" | "restart"): Promise<void>;
  siteLog(domain: string, kind: LogKind): Promise<string>;
  fixPermissions(domain: string): Promise<{ owners_fixed: number; skipped_links: number }>;
  apps(): Promise<CatalogApp[]>;
  installWordPress(domain: string, req: { title: string; admin_user: string; admin_email: string }): Promise<WordPressResult>;

  firewall(): Promise<{ ports: FirewallPort[]; blocks: FirewallBlock[] }>;
  openPort(port: string, proto: "tcp" | "udp", comment: string): Promise<void>;
  closePort(port: string, proto: "tcp" | "udp"): Promise<void>;
  block(address: string, comment: string): Promise<void>;
  unblock(address: string): Promise<void>;
  bans(): Promise<Ban[]>;
  unban(jail: string, ip: string): Promise<void>;

  audit(): Promise<AuditEntry[]>;

  mailDomains(): Promise<MailDomain[]>;
  mailboxes(domain: string): Promise<Mailbox[]>;
  createMailbox(address: string, password: string, quotaMb: number): Promise<Mailbox>;
  deleteMailbox(address: string): Promise<void>;
  forwarders(domain: string): Promise<Forwarder[]>;
  addForwarder(f: Forwarder): Promise<Forwarder>;
  removeForwarder(f: Forwarder): Promise<void>;
  dnsChecks(domain: string): Promise<DnsCheck[]>;
  smtp(): Promise<SmtpSettings>;
  saveSmtp(s: Omit<SmtpSettings, "has_password" | "port25_open"> & { password?: string }): Promise<SmtpSettings>;
}
