// Shapes mirror the ike-agent JSON API (agent/internal/api).

export type SSHMode = "none" | "sftp" | "shell";
export type Runtime = "php" | "node" | "python" | "java" | "go" | "dart" | "static" | "proxy" | "docker";
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
  aliases?: string[];
  www?: "both" | "www" | "root" | "none" | "";
  redirect_aliases?: boolean;
  redirects?: Redirect[];
  php?: PHPSettings;
  ssl?: SSLSettings;
  deploy?: DeployState;
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
  port?: number;
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
  java: string[];
  go: string[];
  dart: string[];
  composer?: boolean;
  docker?: boolean;
  docker_for_accounts?: boolean;
  buildpacks?: boolean;
  kubernetes?: boolean;
  web?: WebFeatures;
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

// ---- Domains, redirects, PHP, HTTPS

export interface Redirect {
  id?: string; kind: "redirect" | "rewrite"; match: "exact" | "prefix" | "regex"; from: string; to: string; code?: number; keep_query?: boolean;
}
export interface PHPSettings {
  pm?: "ondemand" | "dynamic" | "static"; max_children?: number; start_servers?: number; min_spare_servers?: number; max_spare_servers?: number;
  max_requests?: number; memory_limit_mb?: number; upload_max_mb?: number; post_max_mb?: number; max_execution_time?: number;
  max_input_vars?: number; display_errors?: boolean; allow_shell_functions?: boolean; timezone?: string;
}
export interface SSLSettings {
  provider: "letsencrypt" | "custom"; names: string[]; issuer?: string; issued_at: string; expires_at: string;
  force_https: boolean; hsts: boolean; http2: boolean; http3: boolean;
}
export interface WebFeatures { nginx_version: string; h2_directive: boolean; http3: boolean }
export interface CertInfo { domain: string; provider: string; names: string[]; issuer: string; expires_at: string; days_left: number; problem?: string }
export interface PHPExtension { name: string; package: string; description: string; installed: boolean }

// ---- Deployments

export interface GitSource {
  provider?: "github" | "gitlab" | "bitbucket" | "other"; url: string; ref_type: "branch" | "tag" | "commit"; ref: string;
  subdir?: string; auth: "none" | "deploy_key" | "token"; username?: string; auto_deploy: boolean;
}
export type DeployType = "native" | "dockerfile" | "buildpack" | "image";
export interface PreviewSettings { enabled: boolean; base_domain: string }
export interface DeployState {
  type: DeployType; git?: GitSource; image?: string; strategy: "restart" | "blue-green"; public_dir?: string; shared_paths?: string[];
  build_command?: string; release_command?: string; container_port?: number; health_path?: string; keep_releases?: number;
  notify_url?: string; previews?: PreviewSettings; preview_of?: string; preview_number?: number;
  current?: string; color?: string; alt_port?: number;
}
export interface DeploySetup extends Omit<DeployState, "current" | "color" | "alt_port" | "preview_of" | "preview_number" | "strategy"> {
  strategy?: "restart" | "blue-green"; token?: string;
}
export interface DeployInfo {
  deploy: DeployState | null; deploy_key?: string; has_token: boolean; webhook_path: string; webhook_secret?: string; instructions: string[];
}
export interface Deployment {
  id: string; domain: string; trigger: "manual" | "push" | "api" | "rollback" | "preview"; actor?: string; ref_type?: string; ref?: string;
  commit?: string; message?: string; author?: string; image?: string; release?: string;
  status: "queued" | "building" | "live" | "failed" | "cancelled" | "superseded" | "replaced"; error?: string;
  queued_at: string; started_at?: string; finished_at?: string; rollback_of?: string;
}
export interface QueueStatus { settings: { max_concurrent: number }; running: Deployment[]; queued: Deployment[] }

// ---- Containers

export interface Container {
  name: string; short: string; image: string; state: string; status: string; ports: string; created: string;
  site?: string; stack?: string; account: string;
}
export interface PortMap { container_port: number; host_port?: number; protocol?: "tcp" | "udp"; public?: boolean }
export interface VolumeMount { volume?: string; host_path?: string; target: string; read_only?: boolean }
export interface ContainerSpec {
  name: string; image: string; command?: string[]; env?: EnvVar[]; ports?: PortMap[]; volumes?: VolumeMount[];
  restart?: string; cpus?: number; memory_mb?: number; pids?: number; health?: { command: string; interval_sec?: number; retries?: number };
}
export interface StackService { service: string; name: string; image: string; state: string; status: string; ports: string }
export interface StackInfo { name: string; account: string; status: string; services?: StackService[]; compose?: string }
export interface DockerImage { repository: string; tag: string; id: string; size: string; created: string }
export interface DockerOverview { installed: boolean; version?: string; containers: number; running: number; images: DockerImage[]; for_accounts: boolean }
export interface ImageUpdate { image: string; containers: string[]; update_available: boolean; checked_at: string; error?: string }
export interface KubeStatus { installed: boolean; nodes: { name: string; ready: boolean; version: string; roles: string }[]; namespaces: string[]; error?: string }
export interface KubeWorkloads {
  workloads: { kind: string; namespace: string; name: string; ready: number; desired: number; images: string[] }[];
  pods: { namespace: string; name: string; phase: string; restarts: number; node: string }[];
  services: { namespace: string; name: string; type: string; ports: string[] }[];
}

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

  setDomains(domain: string, r: { aliases: string[]; www: string; redirect_aliases: boolean }): Promise<Site>;
  setRedirects(domain: string, rules: Redirect[]): Promise<Site>;
  setPHPSettings(domain: string, p: PHPSettings): Promise<Site>;
  runComposer(domain: string, r: { action: string; package?: string; dev?: boolean }): Promise<Job>;
  issueCertificate(domain: string, r: { email?: string; include_aliases?: boolean }): Promise<Job>;
  uploadCertificate(domain: string, r: { certificate: string; private_key: string }): Promise<Site>;
  updateSSL(domain: string, o: Partial<Pick<SSLSettings, "force_https" | "hsts" | "http2" | "http3">>): Promise<Site>;
  removeSSL(domain: string): Promise<Site>;
  certificates(): Promise<CertInfo[]>;
  phpExtensions(version: string): Promise<PHPExtension[]>;
  changePHPExtension(version: string, name: string, install: boolean): Promise<Job>;

  deployInfo(domain: string): Promise<DeployInfo>;
  configureDeploy(domain: string, s: DeploySetup): Promise<DeployInfo>;
  disconnectDeploy(domain: string): Promise<void>;
  regenerateDeployKey(domain: string): Promise<DeployInfo>;
  regenerateWebhookSecret(domain: string): Promise<DeployInfo>;
  deployments(domain: string): Promise<Deployment[]>;
  deploy(domain: string, r?: { ref_type?: string; ref?: string }): Promise<Deployment>;
  deployment(domain: string, id: string): Promise<{ deployment: Deployment; log: string }>;
  rollback(domain: string, id: string): Promise<Deployment>;
  cancelDeployment(domain: string, id: string): Promise<void>;
  deployQueue(): Promise<QueueStatus>;
  setDeploySettings(max: number): Promise<void>;

  containers(account: string): Promise<Container[]>;
  createContainer(account: string, spec: ContainerSpec): Promise<Container>;
  containerAction(account: string, name: string, action: "start" | "stop" | "restart" | "remove"): Promise<void>;
  containerLogs(account: string, name: string): Promise<string>;
  containerExec(account: string, name: string, command: string): Promise<string>;
  stacks(account: string): Promise<StackInfo[]>;
  stack(account: string, name: string): Promise<StackInfo>;
  saveStack(account: string, name: string, compose: string): Promise<Job>;
  stackAction(account: string, name: string, action: "up" | "down" | "restart" | "pull"): Promise<Job>;
  deleteStack(account: string, name: string, volumes: boolean): Promise<void>;
  volumes(account: string): Promise<{ name: string; short: string }[]>;
  createVolume(account: string, name: string): Promise<void>;
  removeVolume(account: string, name: string): Promise<void>;
  registries(account: string): Promise<string[]>;
  addRegistry(account: string, r: { registry: string; username: string; password: string }): Promise<void>;
  removeRegistry(account: string, registry: string): Promise<void>;
  dockerOverview(): Promise<DockerOverview>;
  allContainers(): Promise<Container[]>;
  pruneDocker(): Promise<string>;
  imageUpdates(): Promise<ImageUpdate[]>;
  checkImageUpdates(): Promise<Job>;
  kubeStatus(): Promise<KubeStatus>;
  kubeWorkloads(): Promise<KubeWorkloads>;
  kubeApply(manifest: string): Promise<string>;
  kubeAction(kind: string, ns: string, name: string, action: "restart" | "delete" | "scale", replicas?: number): Promise<void>;

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
