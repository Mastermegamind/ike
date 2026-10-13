import type {
  Account, Api, AuditEntry, Ban, CatalogApp, CreateSiteRequest, CronJob, Database, DnsCheck, FirewallBlock,
  FirewallPort, Forwarder, LogKind, MailDomain, Mailbox, NewDatabase, Runtimes, SSHKey, SSHMode, ServiceStatus,
  Site, SiteStatus, SmtpSettings, SystemInfo, WebServer, WordPressResult,
} from "./types";
import { DemoApi } from "./demo";

const e = encodeURIComponent;

/** Talks to the Ike control-plane API, which authorizes the user and forwards to ike-agent. */
class HttpApi implements Api {
  constructor(private base = "/api/v1") {}

  private async req<T>(method: string, path: string, body?: unknown): Promise<T> {
    const res = await fetch(this.base + path, {
      method,
      credentials: "same-origin",
      headers: body ? { "Content-Type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error((data as { error?: string }).error || `Request failed (HTTP ${res.status})`);
    return data as T;
  }
  private get<T>(p: string) { return this.req<T>("GET", p); }

  login(username: string, password: string) { return this.req<{ name: string }>("POST", "/session", { username, password }); }
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

  firewall() { return this.get<{ ports: FirewallPort[]; blocks: FirewallBlock[] }>("/firewall"); }
  async openPort(port: string, proto: "tcp" | "udp", comment: string) { await this.req("POST", "/firewall/ports", { port, proto, comment }); }
  async closePort(port: string, proto: "tcp" | "udp") { await this.req("DELETE", `/firewall/ports?port=${e(port)}&proto=${proto}`); }
  async block(address: string, comment: string) { await this.req("POST", "/firewall/blocks", { address, comment }); }
  async unblock(address: string) { await this.req("DELETE", `/firewall/blocks?address=${e(address)}`); }
  bans() { return this.get<Ban[]>("/firewall/bans"); }
  async unban(jail: string, ip: string) { await this.req("DELETE", `/firewall/bans?jail=${e(jail)}&ip=${e(ip)}`); }

  audit() { return this.get<AuditEntry[]>("/audit"); }

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
