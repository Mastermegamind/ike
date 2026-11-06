// Types for the 0.5 screens. They mirror the agent's and panel's JSON.

// ---- events and notifications
export interface ServerEvent { id: number; time: string; type: string; subject: string; level: "info" | "warn" | "bad"; data?: Record<string, unknown>; unread?: boolean }

// ---- DNS
export interface DnsRecord { id: string; name: string; type: string; ttl: number; content: string; priority?: number; weight?: number; port?: number; proxied?: boolean; comment?: string; managed?: string }
export interface DnsRegistration { registrar?: string; expires?: string; status?: string[]; nameservers?: string[]; checked_at: string; error?: string }
export interface DnsZone {
  name: string; account: string; kind: "primary" | "secondary"; backend: "local" | "cloudflare" | "digitalocean"; provider_id?: string;
  primaries?: string[]; also_notify?: string[]; allow_transfer?: string[]; nameservers: string[]; ttl: number; soa_email: string;
  serial: number; dnssec: boolean; ds?: string[]; records: DnsRecord[]; registration?: DnsRegistration; last_error?: string;
  created_at: string; updated_at: string;
}
export interface DnsTemplate { id: string; name: string; help: string; mail: boolean }
export interface DnsTypeInfo { type: string; help: string }
export interface Nameserver { host: string; ipv4?: string; ipv6?: string }
export interface DnsSettings {
  nameservers: Nameserver[]; soa_email: string; default_ttl: number; secondaries: string[]; default_backend: string;
  cloudflare_token: string; cloudflare_account?: string; digitalocean_token: string; expiry_reminders: number[];
  local_server: boolean; server_ipv4: string; server_ipv6: string;
}
export interface DnsInfo { types: DnsTypeInfo[]; templates: DnsTemplate[]; local: boolean; settings?: DnsSettings; nameservers?: Nameserver[]; default_backend?: string }
export interface DnsAnswer { resolver: string; values: string[] | null; match: boolean; error?: string }
export interface DnsRecordCheck { name: string; type: string; expected: string[]; answers: DnsAnswer[] | null; propagated: boolean; note?: string }
export interface DnsPropagation { zone: string; delegated: string[] | null; expected_nameservers: string[]; delegation_ok: boolean; records: DnsRecordCheck[]; checked_at: string; explanation: string }
export interface ReverseCheck { address: string; ptr: string[] | null; hostname: string; forward_confirmed: boolean; ok: boolean; advice: string }
export interface RegistrarContact { first_name: string; last_name: string; organization?: string; address1: string; city: string; state: string; postal_code: string; country: string; phone: string; email: string }
export interface RegistrarSettings { kind: string; api_user?: string; api_key?: string; username?: string; client_ip: string; sandbox?: boolean; contact?: RegistrarContact }
export interface Availability { domain: string; available: boolean; premium: boolean; price?: string }

// ---- mail
export interface AutoReply { enabled: boolean; subject: string; body: string; from?: string; until?: string; days: number }
export interface MailboxV5 {
  local: string; name?: string; quota_mb: number; enabled: boolean; send_only?: boolean; auto_reply?: AutoReply | null;
  forward?: string[]; keep_copy: boolean; created_at: string; usage?: { used_mb: number; messages: number; at: string };
}
export interface MailAlias { from: string; to: string[] }
export interface MailDomainV5 {
  domain: string; account: string; enabled: boolean; catch_all?: string; dkim_selector: string; dkim: boolean; tls: boolean;
  mailboxes: MailboxV5[]; aliases: MailAlias[]; created_at: string; webmail_url: string; mail_host: string;
}
export interface MailCheck { record: string; host: string; type: string; expected: string; found?: string; status: "ok" | "missing" | "wrong" | "warn"; hint: string }
export interface MailRecord { Name: string; Type: string; Content: string; Priority: number }
export interface QueuedMessage { id: string; queue: string; arrival: string; size: number; sender: string; recipients: { address: string; delay_reason?: string }[] }
export interface Delivery { time: string; queue_id: string; from?: string; to: string; status: "sent" | "deferred" | "bounced" | "rejected"; relay?: string; detail: string }
export interface MailSettings {
  relay: { provider: string; host: string; port: number; username: string }; max_message_mb: number; hourly_send_limit: number;
  spam_threshold: number; reject_threshold: number; antivirus: boolean; greylisting: boolean; webmail_port: number;
  default_quota_mb: number; dmarc_policy: string; has_relay_password: boolean; installed: boolean; webmail: boolean; spam_filter: boolean; hostname: string;
}

// ---- security
export interface MalwareFinding { id: string; path: string; engine: string; signature: string; severity: "high" | "medium"; size: number; quarantined?: string }
export interface MalwareScan { id: string; account: string; path: string; status: "running" | "done" | "failed"; error?: string; started_at: string; finished_at?: string; files: number; clamav: boolean; findings: MalwareFinding[]; scheduled?: boolean }
export interface QuarantineItem { id: string; account: string; original: string; signature: string; size: number; at: string }
export interface MalwareSchedule { enabled: boolean; frequency: "daily" | "weekly"; hour: number; weekday: number; auto_quarantine: boolean; last_run?: string }
export interface WAFSettings { mode: "off" | "detect" | "block"; disabled_rules?: number[]; profile?: string }
export interface WAFEvent { time: string; client: string; rule_id: string; message: string; uri: string; blocked: boolean }
export interface SecretsStatus { key_id: string; keys: number; stores: string[]; rotated_at: string }

// ---- databases
export interface EngineDB { engine: "mariadb" | "postgres" | "mongodb"; name: string; owner: string; note?: string; roles?: { name: string; access: string }[]; created_at: string; size_mb?: number }
export interface RedisInstance { socket: string; memory_mb: number; policy: string; persist: boolean; created_at: string; stats?: { running: boolean; used_mb: number; keys: number; clients: number; hit_rate: number; uptime_hours: number; version: string } }
export interface AccountDBs { account: string; databases: EngineDB[]; redis?: RedisInstance | null }
export interface Engines { mariadb: boolean; postgres: boolean; redis: string; mongodb: boolean }
export interface DBCredentials { engine: string; database: string; user: string; password: string; host: string; port?: number; url: string }
export interface DBProcess { id: number; user: string; host: string; db: string; command: string; seconds: number; state: string; query: string }
export interface SlowQuery { time: string; user: string; db: string; query_secs: number; lock_secs: number; rows_sent: number; rows_examined: number; query: string }
export interface Replication { configured: boolean; io_running: boolean; sql_running: boolean; seconds_behind: number; primary_host: string; gtid: string; last_error?: string; read_only: boolean; binary_logging: boolean }

// ---- files
export interface FileEntry { name: string; type: "file" | "dir" | "link" | "other"; size: number; modified: string; mode: string; target?: string }
export interface FileHit { path: string; type: string; size: number; line?: string }
export interface FolderSize { path: string; bytes: number }
export interface FileSnapshot { id: string; account: string; time: string }
export interface Share { token: string; account: string; path: string; folder: boolean; expires_at: string; max_downloads: number; downloads: number; created_at: string; has_password: boolean; url_path: string }
export interface StorageTarget { id: string; name: string; kind: string; endpoint?: string; region?: string; bucket?: string; prefix?: string; access_key?: string; secret_key?: string; host?: string; port?: number; user?: string; account?: string; created_at: string; last_check?: string }

// ---- backups
export interface BackupRun { status: "ok" | "failed" | "running"; started_at: string; finished_at?: string; snapshot?: string; added_bytes?: number; total_bytes?: number; files?: number; error?: string }
export interface BackupPlan {
  id: string; name: string; scope: "server" | "account" | "site" | "databases" | "mail"; account?: string; site?: string; target: string; enabled: boolean;
  schedule: { frequency: "hourly" | "daily" | "weekly" | "manual"; hour: number; minute: number; weekday: number };
  retention: { keep_last: number; keep_hourly: number; keep_daily: number; keep_weekly: number; keep_monthly: number };
  databases?: string[]; excludes?: string[]; check_days: number; created_at: string; last?: BackupRun; last_check?: BackupRun; recovery_key?: string;
}
export interface BackupSnapshot { id: string; time: string; paths: string[]; host: string; tags: string[]; size?: number }
export interface BackupNode { name: string; path: string; type: string; size: number; mtime: string }

// ---- infrastructure
export interface RemoteServer { id: string; name: string; address: string; group?: string; hostname?: string; added_at: string; last_seen?: string; version?: string; status?: "online" | "offline" }
export interface JoinTokenResult { token: string; fingerprint: string; expires: string; panel_url: string; command: string; install: string }
export interface DBCluster { id: string; name: string; engine: "mariadb" | "redis"; primary: string; replicas: string[]; created_at: string; health?: { server: string; role: string; ok: boolean; detail: string }[] }
export interface Inventory {
  hostname: string; os: string; kernel: string; virtualization: string; vendor?: string; product?: string; serial?: string; cpu_model: string; cpus: number;
  memory_mb: number; swap_mb: number; uptime_hours: number; booted_at: string; collected_at: string;
  disks: { name: string; size_gb: number; model?: string; type: string; mount?: string; filesystem?: string; rotational: boolean }[];
  interfaces: { name: string; mac?: string; state: string; mtu: number; addresses: string[] }[];
  asset: { tag: string; owner: string; location: string; provider: string; purchase_date: string; renewal_date: string; monthly_cost: string; notes: string };
}
export interface MaintenanceWindow { id: string; title: string; start: string; end: string; sites: string[]; updates: boolean; reboot: boolean; state: string; log?: string[] }
export interface Webhook { id: string; url: string; secret?: string; events: string[]; enabled: boolean; created_at: string }
export interface WebhookDelivery { webhook: string; event: number; type: string; status: number; error?: string; at: string; attempt: number }
export interface SpecChange { action: "create" | "update" | "delete" | "keep"; kind: string; name: string; detail?: string }
export interface SpecTemplate { name: string; description: string; spec: unknown; variables?: string[] }

// ---- business
export interface PackageLimits {
  sites: number; domains: number; databases: number; email_domains: number; mailboxes: number; disk_mb: number; bandwidth_gb: number;
  cpu_percent: number; memory_mb: number; processes: number; php_workers: number; shell: boolean; containers: boolean; backups: boolean;
}
export interface HostingPackage { id: string; name: string; owner?: string; price: number; currency: string; interval: "month" | "year" | "none"; limits: PackageLimits; created: string; accounts?: number }
export interface Subscription { id: string; account: string; package_id: string; price: number; currency: string; interval: string; next_due: string; status: "active" | "cancelled"; last_invoice?: string; created_by: string; created_at: string }
export interface BandwidthRow { account: string; used_mb: number; limit_gb: number; suspended_for_bandwidth: boolean; package_id?: string }
export interface Branding { product_name: string; logo?: string; primary_color: string; support_email?: string; support_url?: string; hide_powered_by?: boolean; hostnames?: string[] }
export interface APIToken { id: string; name: string; read_only: boolean; created: string; last_used?: string; expires?: string; limited?: boolean; scopes?: Record<string, string>; accounts?: string[]; allow_ips?: string[] }
export interface QuotaUsage { account: string; used_mb: number; soft_mb: number; hard_mb: number; over: boolean }
export interface ImportFile { file: string; size_mb: number; uploaded: string }

// ---- server metrics
export interface MetricPoint { t: string; cpu: number; mem: number; swap: number; net_in: number; net_out: number; disk_rd: number; disk_wr: number; load1: number }
export interface NetInterface { name: string; in_bps: number; out_bps: number; rx_bytes: number; tx_bytes: number; rx_errors: number; tx_errors: number }
export interface ServerMetrics { now: MetricPoint; mem_total_mb: number; swap_total_mb: number; interfaces: NetInterface[]; history: MetricPoint[] }
