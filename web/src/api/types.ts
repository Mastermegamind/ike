// Shapes mirror the ike-agent JSON API (agent/internal/api).

export type SSHMode = "none" | "sftp" | "shell";
export type Runtime = "php" | "node" | "python" | "static";
export type WebServer = "nginx" | "apache" | "nginx+apache";
export type SiteStatus = "active" | "maintenance" | "suspended";
export type Role = "admin" | "reseller" | "user";

export interface Account {
  limits?: Limits;
  quota_note?: string;
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
  staging?: boolean;
  staging_of?: string;
  health?: HealthCheck;
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

// ---- Updates, jobs, health, staging, limits, usage, security, drift, imports

export interface Job { id: string; kind: string; title: string; status: "running" | "done" | "failed"; error?: string; log: string[]; started_at: string; finished_at?: string }
export interface PendingPackage { name: string; from: string; to: string; security: boolean }
export interface AutoUpdates { security_updates: boolean; auto_reboot: boolean; reboot_time: string }
export interface UpdateStatus { checked_at: string; packages: PendingPackage[]; security_count: number; reboot_required: boolean; reboot_packages?: string[]; auto: AutoUpdates }
export interface IkeRelease { current: string; latest: string; update_available: boolean; url: string; notes: string }
export interface Finding { severity: "ok" | "info" | "warn" | "bad"; title: string; detail?: string; fix?: string; evidence?: string }
export interface Diagnosis { domain: string; summary: string; findings: Finding[] }
export interface HealthCheck { path: string; interval_sec: number; fail_threshold: number; action: "restart" | "notify" }
export interface EnvVar { key: string; value: string }
export interface Limits { cpu_percent: number; memory_mb: number; max_processes: number; disk_mb: number; php_workers: number }
export interface UsagePoint { t: string; cpu: number; mem: number }
export interface AccountUsage { account: string; cpu_percent: number; memory_mb: number; processes: number; disk_mb: number; bandwidth_today_mb: number; limits: Limits; history: UsagePoint[] }
export interface SecurityCheck { id: string; area: string; severity: "ok" | "warn" | "bad"; title: string; fix?: string }
export interface SecurityReport { score: number; checks: SecurityCheck[] }
export interface Drift { path: string; kind: string; owner: string; state: "changed" | "missing"; diff?: string }
export interface ImportPlan {
  file: string; cpanel_user: string; account: string; main_domain: string; databases: string[]; mailboxes: string[]; cron_jobs: number; home_size_mb: number; warnings: string[];
  websites: { domain: string; kind: "main" | "addon" | "subdomain" | "alias"; php?: string; php_installed: boolean }[];
}

// ---- Control plane: people, billing, alerts

export interface Person {
  username: string; email: string; name?: string; role: Role; totp_enabled: boolean; reseller?: string;
  accounts: string[]; max_accounts?: number; disabled: boolean; created_at: string; last_login_at?: string;
}
export type PaymentProvider = "bachs" | "paystack" | "flutterwave";
export interface Invoice {
  id: string; account: string; description: string; currency: string; amount: number; status: "unpaid" | "paid" | "void";
  due_at: string; created_at: string; paid_at?: string; provider?: PaymentProvider;
}
export interface ProviderSettings { enabled: boolean; secret_key?: string; webhook_secret?: string; sandbox?: boolean }
export interface BillingSettings { currency: string; grace_days: number; auto_suspend: boolean; providers: Partial<Record<PaymentProvider, ProviderSettings>> }
export interface AlertSettings {
  telegram: { enabled: boolean; bot_token?: string; chat_id: string };
  whatsapp: { enabled: boolean; token?: string; phone_number_id: string; to: string; template?: string };
  email: { enabled: boolean; host: string; port: number; username: string; password?: string; from: string; to: string };
  uptime_interval_min: number;
}
export interface AlertEvent { time: string; domain: string; kind: "down" | "up"; detail: string }

export interface Api {
  login(username: string, password: string, code?: string): Promise<{ name: string; role: Role }>;
  logout(): Promise<void>;
  setup2fa(): Promise<{ secret: string; otpauth_url: string }>;
  enable2fa(code: string): Promise<{ recovery_codes: string[] }>;
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

  updates(): Promise<UpdateStatus>;
  applyUpdates(securityOnly: boolean): Promise<Job>;
  setAutoUpdates(a: AutoUpdates): Promise<AutoUpdates>;
  scheduleReboot(minutes: number): Promise<void>;
  ikeRelease(): Promise<IkeRelease>;
  updateIke(): Promise<Job>;
  job(id: string): Promise<Job>;
  diagnose(domain: string): Promise<Diagnosis>;
  createStaging(domain: string): Promise<Job>;
  pushStaging(domain: string): Promise<Job>;
  setHealthCheck(domain: string, hc: HealthCheck | null): Promise<Site>;
  env(domain: string): Promise<EnvVar[]>;
  setEnv(domain: string, vars: EnvVar[]): Promise<void>;
  setLimits(account: string, l: Limits): Promise<Account>;
  usage(): Promise<AccountUsage[]>;
  security(): Promise<SecurityReport>;
  drift(): Promise<Drift[]>;
  reconcile(paths: string[]): Promise<Drift[]>;
  inspectImport(file: string): Promise<ImportPlan>;

  people(): Promise<Person[]>;
  createPerson(p: { username: string; email: string; name?: string; role: Role; password: string; accounts?: string[]; max_accounts?: number }): Promise<Person>;
  updatePerson(username: string, patch: Partial<{ disabled: boolean; accounts: string[]; max_accounts: number; role: Role }>): Promise<Person>;
  billingSettings(): Promise<{ settings: BillingSettings; webhook_urls: Record<PaymentProvider, string> }>;
  saveBillingSettings(s: BillingSettings): Promise<{ settings: BillingSettings; webhook_urls: Record<PaymentProvider, string> }>;
  invoices(): Promise<Invoice[]>;
  createInvoice(r: { account: string; description: string; amount: string; currency?: string; due_in_days: number }): Promise<Invoice>;
  payInvoice(id: string, provider: PaymentProvider): Promise<{ checkout_url: string }>;
  voidInvoice(id: string): Promise<void>;
  markPaid(id: string): Promise<void>;
  alertSettings(): Promise<AlertSettings>;
  saveAlertSettings(a: AlertSettings): Promise<AlertSettings>;
  testAlert(): Promise<Record<string, string>>;
  alertEvents(): Promise<AlertEvent[]>;

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
