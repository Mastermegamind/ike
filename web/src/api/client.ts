import type {
  Account, AccountUsage, AlertEvent, AlertSettings, Api, AuditEntry, AutoUpdates, Ban, BillingSettings, CatalogApp,
  CreateSiteRequest, CronJob, Database, Diagnosis, DnsCheck, Drift, EnvVar, FirewallBlock, FirewallPort, Forwarder,
  HealthCheck, IkeRelease, ImportPlan, Invoice, Job, Limits, LogKind, MailDomain, Mailbox, NewDatabase, PaymentProvider,
  Person, Role, Runtimes, SSHKey, SSHMode, SecurityReport, ServiceStatus, Site, SiteStatus, SmtpSettings, SystemInfo,
  UpdateStatus, WebServer, WordPressResult, Redirect, PHPSettings, SSLSettings, CertInfo, PHPExtension, DeployInfo, DeploySetup,
  Deployment, QueueStatus, Container, ContainerSpec, StackInfo, DockerOverview, ImageUpdate, KubeStatus, KubeWorkloads, PersonCreate } from "./types";

import { DemoApi } from "./demo";
import { TwoFactorRequired } from "./errors";
export { TwoFactorRequired };

const e = encodeURIComponent;

/** Which server the panel is managing ("" = this one). Set by the server switcher. */
let currentServer = "";
export function setServer(id: string) { currentServer = id === "local" ? "" : id; }
export function getServer() { return currentServer || "local"; }

/** Talks to the Ike control-plane API, which authorizes the user and forwards to ike-agent. */
class HttpApi implements Api {
  constructor(private base = "/api/v1") {}

  private async req<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(this.base + path, {
      method,
      credentials: "same-origin",
      // The panel requires this header on every change (CSRF protection).
      headers: { "X-Requested-With": "ike", ...(currentServer ? { "X-Ike-Server": currentServer } : {}), ...(body !== undefined ? { "Content-Type": "application/json" } : {}) },
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
    const data = await res.json().catch(() => ({}));
    if ((data as { two_factor_required?: boolean }).two_factor_required) throw new TwoFactorRequired((data as { error: string }).error);
    if (res.status === 401 && path !== "/session") window.dispatchEvent(new Event("ike:signed-out"));
    if (!res.ok) throw new Error((data as { error?: string }).error || `Request failed (HTTP ${res.status})`);
    return data as T;
  }
  private get<T>(p: string) { return this.req<T>("GET", p); }

  call<T = unknown>(method: string, path: string, body?: unknown) { return this.req<T>(method, path, body); }
  upload<T = unknown>(method: "PUT" | "POST", path: string, file: Blob, onProgress?: (pct: number) => void): Promise<T> {
    // XMLHttpRequest reports upload progress; fetch doesn't.
    return new Promise((resolve, reject) => {
      const x = new XMLHttpRequest();
      x.open(method, this.base + path);
      x.setRequestHeader("X-Requested-With", "ike");
      if (currentServer) x.setRequestHeader("X-Ike-Server", currentServer);
      x.setRequestHeader("Content-Type", "application/octet-stream");
      x.upload.onprogress = (ev) => { if (ev.lengthComputable) onProgress?.(Math.round((ev.loaded / ev.total) * 100)); };
      x.onload = () => {
        let data: { error?: string } = {};
        try { data = JSON.parse(x.responseText || "{}"); } catch { /* not JSON */ }
        if (x.status === 401) window.dispatchEvent(new Event("ike:signed-out"));
        if (x.status >= 300) reject(new Error(data.error || `Upload failed (HTTP ${x.status})`));
        else resolve(data as T);
      };
      x.onerror = () => reject(new Error("The upload was interrupted. Check your connection and try again."));
      x.send(file);
    });
  }
  fileUrl(path: string) {
    const sep = path.includes("?") ? "&" : "?";
    return this.base + path + (currentServer ? `${sep}_server=${e(currentServer)}` : "");
  }
  socketUrl(path: string) {
    const u = new URL(this.fileUrl(path), window.location.href);
    u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
    return u.toString();
  }

  async login(username: string, password: string, code?: string) {
    const p = await this.req<{ username: string; role: Role }>("POST", "/session", { username, password, code });
    return { name: p.username, role: p.role };
  }
  async logout() { await this.req("DELETE", "/session"); }
  setup2fa() { return this.req<{ secret: string; otpauth_url: string }>("POST", "/me/2fa/setup"); }
  enable2fa(code: string) { return this.req<{ recovery_codes: string[] }>("POST", "/me/2fa/enable", { code }); }
  system() { return this.get<SystemInfo>("/system"); }
  services() { return this.get<ServiceStatus[]>("/services"); }
  runtimes() { return this.get<Runtimes>("/runtimes"); }

  accounts() { return this.get<Account[]>("/accounts"); }
  createAccount(name: string) { return this.req<Account>("POST", "/accounts", { name }); }
  async deleteAccount(n: string, withSites: boolean) { await this.req("DELETE", `/accounts/${e(n)}${withSites ? "?with_sites=true" : ""}`); }
  suspendAccount(n: string, reason: string) { return this.req<Account>("POST", `/accounts/${e(n)}/suspend`, { reason }); }
  unsuspendAccount(n: string) { return this.req<Account>("POST", `/accounts/${e(n)}/unsuspend`); }
  setSSH(n: string, mode: SSHMode) { return this.req<Account>("PUT", `/accounts/${e(n)}/ssh`, { mode }); }
  sshKeys(n: string) { return this.get<SSHKey[]>(`/accounts/${e(n)}/ssh/keys`); }
  addSSHKey(n: string, public_key: string) { return this.req<SSHKey>("POST", `/accounts/${e(n)}/ssh/keys`, { public_key }); }
  async removeSSHKey(n: string, f: string) { await this.req("DELETE", `/accounts/${e(n)}/ssh/keys?fingerprint=${e(f)}`); }
  cron(n: string) { return this.get<CronJob[]>(`/accounts/${e(n)}/cron`); }
  addCron(n: string, j: Omit<CronJob, "id">) { return this.req<CronJob>("POST", `/accounts/${e(n)}/cron`, j); }
  updateCron(n: string, id: string, j: Omit<CronJob, "id">) { return this.req<CronJob>("PUT", `/accounts/${e(n)}/cron/${e(id)}`, j); }
  async deleteCron(n: string, id: string) { await this.req("DELETE", `/accounts/${e(n)}/cron/${e(id)}`); }
  databases(n: string) { return this.get<Database[]>(`/accounts/${e(n)}/databases`); }
  createDatabase(n: string, name: string, note: string) { return this.req<NewDatabase>("POST", `/accounts/${e(n)}/databases`, { name, note }); }
  async deleteDatabase(n: string, db: string) { await this.req("DELETE", `/accounts/${e(n)}/databases/${e(db)}`); }

  sites() { return this.get<Site[]>("/sites"); }
  site(d: string) { return this.get<Site>(`/sites/${e(d)}`); }
  createSite(r: CreateSiteRequest) { return this.req<Site>("POST", "/sites", r); }
  updateSite(d: string, p: { version?: string; web_server?: WebServer; start_command?: string }) { return this.req<Site>("PATCH", `/sites/${e(d)}`, p); }
  async deleteSite(d: string, rm: boolean) { await this.req("DELETE", `/sites/${e(d)}${rm ? "?remove_files=true" : ""}`); }
  setStatus(d: string, status: SiteStatus, allow_ips: string[]) { return this.req<Site>("PUT", `/sites/${e(d)}/status`, { status, allow_ips }); }
  async appAction(d: string, a: "start" | "stop" | "restart") { await this.req("POST", `/sites/${e(d)}/app/${a}`); }
  async siteLog(d: string, k: LogKind) { return (await this.get<{ log: string }>(`/sites/${e(d)}/logs/${k}?lines=300`)).log; }
  fixPermissions(d: string) { return this.req<{ owners_fixed: number; skipped_links: number }>("POST", `/sites/${e(d)}/permissions`); }
  apps() { return this.get<CatalogApp[]>("/apps"); }
  installWordPress(d: string, r: { title: string; admin_user: string; admin_email: string }) {
    return this.req<WordPressResult>("POST", `/sites/${e(d)}/apps/wordpress`, r);
  }

  firewall() { return this.get<{ backend?: string; ports: FirewallPort[]; blocks: FirewallBlock[] }>("/firewall"); }
  async openPort(port: string, proto: "tcp" | "udp", comment: string, from = "") { await this.req("POST", "/firewall/ports", { port, proto, comment, ...(from ? { from } : {}) }); }
  async closePort(port: string, proto: "tcp" | "udp", from = "") { await this.req("DELETE", `/firewall/ports?port=${e(port)}&proto=${proto}${from ? `&from=${e(from)}` : ""}`); }
  async block(address: string, comment: string) { await this.req("POST", "/firewall/blocks", { address, comment }); }
  async unblock(address: string) { await this.req("DELETE", `/firewall/blocks?address=${e(address)}`); }
  bans() { return this.get<Ban[]>("/firewall/bans"); }
  async unban(jail: string, ip: string) { await this.req("DELETE", `/firewall/bans?jail=${e(jail)}&ip=${e(ip)}`); }

  audit() { return this.get<AuditEntry[]>("/audit"); }

  updates() { return this.get<UpdateStatus>("/updates"); }
  applyUpdates(security_only: boolean) { return this.req<Job>("POST", "/updates/apply", { security_only }); }
  setAutoUpdates(a: AutoUpdates) { return this.req<AutoUpdates>("PUT", "/updates/auto", a); }
  async scheduleReboot(in_minutes: number) { await this.req("POST", "/server/reboot", { in_minutes }); }
  ikeRelease() { return this.get<IkeRelease>("/ike/release"); }
  updateIke() { return this.req<Job>("POST", "/ike/update"); }
  job(id: string) { return this.get<Job>(`/jobs/${e(id)}`); }
  diagnose(d: string) { return this.get<Diagnosis>(`/sites/${e(d)}/diagnose`); }
  createStaging(d: string) { return this.req<Job>("POST", `/sites/${e(d)}/staging`); }
  pushStaging(d: string) { return this.req<Job>("POST", `/sites/${e(d)}/push`); }
  setHealthCheck(d: string, hc: HealthCheck | null) { return this.req<Site>("PUT", `/sites/${e(d)}/healthcheck`, hc ?? null); }
  env(d: string) { return this.get<EnvVar[]>(`/sites/${e(d)}/env`); }
  async setEnv(d: string, vars: EnvVar[]) { await this.req("PUT", `/sites/${e(d)}/env`, vars); }
  setLimits(n: string, l: Limits) { return this.req<Account>("PUT", `/accounts/${e(n)}/limits`, l); }
  usage() { return this.get<AccountUsage[]>("/usage"); }
  security() { return this.get<SecurityReport>("/security"); }
  drift() { return this.get<Drift[]>("/drift"); }
  reconcile(paths: string[]) { return this.req<Drift[]>("POST", "/drift/reconcile", { paths }); }
  inspectImport(f: string) { return this.get<ImportPlan>(`/imports/cpanel/${e(f)}`); }

  people() { return this.get<Person[]>("/people"); }
  createPerson(p: PersonCreate) { return this.req<Person>("POST", "/people", p); }
  updatePerson(u: string, patch: Partial<{ disabled: boolean; accounts: string[]; max_accounts: number; role: Role; areas: Record<string, string> }>) { return this.req<Person>("PATCH", `/people/${e(u)}`, patch); }
  billingSettings() { return this.get<{ settings: BillingSettings; webhook_urls: Record<PaymentProvider, string> }>("/billing/settings"); }
  saveBillingSettings(s: BillingSettings) { return this.req<{ settings: BillingSettings; webhook_urls: Record<PaymentProvider, string> }>("PUT", "/billing/settings", s); }
  invoices() { return this.get<Invoice[]>("/billing/invoices"); }
  createInvoice(r: { account: string; description: string; amount: string; currency?: string; due_in_days: number }) { return this.req<Invoice>("POST", "/billing/invoices", r); }
  payInvoice(id: string, provider: PaymentProvider) { return this.req<{ checkout_url: string }>("POST", `/billing/invoices/${e(id)}/checkout`, { provider }); }
  async voidInvoice(id: string) { await this.req("POST", `/billing/invoices/${e(id)}/void`); }
  async markPaid(id: string) { await this.req("POST", `/billing/invoices/${e(id)}/mark-paid`); }
  alertSettings() { return this.get<AlertSettings>("/alerts/settings"); }
  saveAlertSettings(a: AlertSettings) { return this.req<AlertSettings>("PUT", "/alerts/settings", a); }
  testAlert() { return this.req<Record<string, string>>("POST", "/alerts/test"); }
  alertEvents() { return this.get<AlertEvent[]>("/alerts/events"); }

  setDomains(d: string, r: { aliases: string[]; www: string; redirect_aliases: boolean }) { return this.req<Site>("PUT", `/sites/${e(d)}/domains`, r); }
  setRedirects(d: string, rules: Redirect[]) { return this.req<Site>("PUT", `/sites/${e(d)}/redirects`, rules); }
  setPHPSettings(d: string, p: PHPSettings) { return this.req<Site>("PUT", `/sites/${e(d)}/php`, p); }
  runComposer(d: string, r: { action: string; package?: string; dev?: boolean }) { return this.req<Job>("POST", `/sites/${e(d)}/composer`, r); }
  issueCertificate(d: string, r: { email?: string; include_aliases?: boolean; wildcard?: boolean }) { return this.req<Job>("POST", `/sites/${e(d)}/ssl`, r); }
  uploadCertificate(d: string, r: { certificate: string; private_key: string }) { return this.req<Site>("PUT", `/sites/${e(d)}/ssl/custom`, r); }
  updateSSL(d: string, o: Partial<Pick<SSLSettings, "force_https" | "hsts" | "http2" | "http3">>) { return this.req<Site>("PATCH", `/sites/${e(d)}/ssl`, o); }
  removeSSL(d: string) { return this.req<Site>("DELETE", `/sites/${e(d)}/ssl`); }
  certificates() { return this.get<CertInfo[]>("/ssl"); }
  phpExtensions(v: string) { return this.get<PHPExtension[]>(`/php/${e(v)}/extensions`); }
  changePHPExtension(v: string, name: string, install: boolean) { return this.req<Job>(install ? "POST" : "DELETE", `/php/${e(v)}/extensions/${e(name)}`); }

  deployInfo(d: string) { return this.get<DeployInfo>(`/sites/${e(d)}/deploy`); }
  configureDeploy(d: string, s: DeploySetup) { return this.req<DeployInfo>("PUT", `/sites/${e(d)}/deploy`, s); }
  async disconnectDeploy(d: string) { await this.req("DELETE", `/sites/${e(d)}/deploy`); }
  regenerateDeployKey(d: string) { return this.req<DeployInfo>("POST", `/sites/${e(d)}/deploy/key`); }
  regenerateWebhookSecret(d: string) { return this.req<DeployInfo>("POST", `/sites/${e(d)}/deploy/webhook-secret`); }
  deployments(d: string) { return this.get<Deployment[]>(`/sites/${e(d)}/deployments`); }
  deploy(d: string, r?: { ref_type?: string; ref?: string }) { return this.req<Deployment>("POST", `/sites/${e(d)}/deployments`, r ?? {}); }
  deployment(d: string, id: string) { return this.get<{ deployment: Deployment; log: string }>(`/sites/${e(d)}/deployments/${e(id)}`); }
  rollback(d: string, id: string) { return this.req<Deployment>("POST", `/sites/${e(d)}/deployments/${e(id)}/rollback`); }
  async cancelDeployment(d: string, id: string) { await this.req("POST", `/sites/${e(d)}/deployments/${e(id)}/cancel`); }
  deployQueue() { return this.get<QueueStatus>("/deploy/queue"); }
  async setDeploySettings(max: number) { await this.req("PUT", "/deploy/settings", { max_concurrent: max }); }

  containers(a: string) { return this.get<Container[]>(`/accounts/${e(a)}/containers`); }
  createContainer(a: string, spec: ContainerSpec) { return this.req<Container>("POST", `/accounts/${e(a)}/containers`, spec); }
  async containerAction(a: string, n: string, action: string) { await this.req("POST", `/accounts/${e(a)}/containers/${e(n)}/${action}`); }
  async containerLogs(a: string, n: string) { return (await this.get<{ log: string }>(`/accounts/${e(a)}/containers/${e(n)}/logs?lines=300`)).log; }
  async containerExec(a: string, n: string, command: string) { return (await this.req<{ output: string }>("POST", `/accounts/${e(a)}/containers/${e(n)}/exec`, { command })).output; }
  stacks(a: string) { return this.get<StackInfo[]>(`/accounts/${e(a)}/stacks`); }
  stack(a: string, n: string) { return this.get<StackInfo>(`/accounts/${e(a)}/stacks/${e(n)}`); }
  saveStack(a: string, n: string, compose: string) { return this.req<Job>("PUT", `/accounts/${e(a)}/stacks/${e(n)}`, { compose }); }
  stackAction(a: string, n: string, action: string) { return this.req<Job>("POST", `/accounts/${e(a)}/stacks/${e(n)}/${action}`); }
  async deleteStack(a: string, n: string, volumes: boolean) { await this.req("DELETE", `/accounts/${e(a)}/stacks/${e(n)}${volumes ? "?volumes=true" : ""}`); }
  volumes(a: string) { return this.get<{ name: string; short: string }[]>(`/accounts/${e(a)}/volumes`); }
  async createVolume(a: string, name: string) { await this.req("POST", `/accounts/${e(a)}/volumes`, { name }); }
  async removeVolume(a: string, name: string) { await this.req("DELETE", `/accounts/${e(a)}/volumes/${e(name)}`); }
  registries(a: string) { return this.get<string[]>(`/accounts/${e(a)}/registries`); }
  async addRegistry(a: string, r: { registry: string; username: string; password: string }) { await this.req("POST", `/accounts/${e(a)}/registries`, r); }
  async removeRegistry(a: string, reg: string) { await this.req("DELETE", `/accounts/${e(a)}/registries?registry=${e(reg)}`); }
  dockerOverview() { return this.get<DockerOverview>("/docker"); }
  allContainers() { return this.get<Container[]>("/docker/containers"); }
  async pruneDocker() { return (await this.req<{ output: string }>("POST", "/docker/prune")).output; }
  imageUpdates() { return this.get<ImageUpdate[]>("/docker/updates"); }
  checkImageUpdates() { return this.req<Job>("POST", "/docker/updates/check"); }
  kubeStatus() { return this.get<KubeStatus>("/kubernetes"); }
  kubeWorkloads() { return this.get<KubeWorkloads>("/kubernetes/workloads"); }
  async kubeApply(manifest: string) { return (await this.req<{ output: string }>("POST", "/kubernetes/apply", { manifest })).output; }
  async kubeAction(kind: string, ns: string, name: string, action: "restart" | "delete" | "scale", replicas?: number) {
    if (action === "delete") await this.req("DELETE", `/kubernetes/${e(kind)}/${e(ns)}/${e(name)}`);
    else await this.req("POST", `/kubernetes/${e(kind)}/${e(ns)}/${e(name)}/${action}`, action === "scale" ? { replicas } : undefined);
  }

  mailDomains() { return this.get<MailDomain[]>("/mail/domains"); }
  mailboxes(d: string) { return this.get<Mailbox[]>(`/mail/domains/${e(d)}/mailboxes`); }
  createMailbox(address: string, password: string, quota_mb: number) { return this.req<Mailbox>("POST", "/mail/mailboxes", { address, password, quota_mb }); }
  async deleteMailbox(a: string) { await this.req("DELETE", `/mail/mailboxes/${e(a)}`); }
  forwarders(d: string) { return this.get<Forwarder[]>(`/mail/domains/${e(d)}/forwarders`); }
  addForwarder(f: Forwarder) { return this.req<Forwarder>("POST", "/mail/forwarders", f); }
  async removeForwarder(f: Forwarder) { await this.req("DELETE", "/mail/forwarders", f); }
  dnsChecks(d: string) { return this.get<DnsCheck[]>(`/mail/domains/${e(d)}/dns`); }
  smtp() { return this.get<SmtpSettings>("/mail/smtp"); }
  saveSmtp(s: Omit<SmtpSettings, "has_password" | "port25_open"> & { password?: string }) { return this.req<SmtpSettings>("PUT", "/mail/smtp", s); }
}

// Demo mode runs entirely in the browser, for previews and until the
// control-plane API ships. Build with VITE_IKE_API=http to use a real server.
export const isDemo = import.meta.env.VITE_IKE_API !== "http";
export const demo = isDemo ? new DemoApi() : null;
export const api: Api = demo ?? new HttpApi();
