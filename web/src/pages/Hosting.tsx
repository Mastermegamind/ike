// Hosting packages, subscriptions, bandwidth and disk quotas; white-label
// branding; API tokens with scoped access (for scripts, the ike CLI, AI
// agents and billing systems); and the notification centre.
import { useState } from "react";
import { Bell, Boxes, CreditCard, Gauge, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { api, isDemo } from "../api/client";
import type { Role } from "../api/types";
import type { APIToken, BandwidthRow, Branding, HostingPackage, PackageLimits, QuotaUsage, ServerEvent, Subscription } from "../api/types5";
import { Badge, Button, CopyBlock, CopyField, Empty, Field, Meter, Segmented, Skeleton, Tabs, timeAgo, useAsync } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { dateTime, ErrorNote, FormModal, NumberField, OnceModal, SelectField, TextField, useAction, useGet } from "../components/kit5";
import { describeEvent, loadBranding } from "../components/shellx";

const money = (minor: number, cur: string) => new Intl.NumberFormat(undefined, { style: "currency", currency: cur || "NGN" }).format(minor / 100);
const unlimited = (n: number, unit = "") => (n ? `${n.toLocaleString()}${unit}` : "Unlimited");

export function PackagesPage({ role }: { role: Role }) {
  const [tab, setTab] = useState<"packages" | "subscriptions" | "bandwidth" | "quotas">("packages");
  return (
    <div className="stack">
      <PageHead title="Hosting packages" text="Bundles of limits you sell: websites, domains, email, disk and bandwidth. Accounts on a package can't go over it, and subscriptions bill for it automatically." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "packages", label: "Packages" }, { value: "subscriptions", label: "Subscriptions" }, { value: "bandwidth", label: "Bandwidth" },
        ...(role === "admin" ? [{ value: "quotas" as const, label: "Disk quotas" }] : [])]} />
      {tab === "packages" ? <Packages /> : tab === "subscriptions" ? <Subscriptions /> : tab === "bandwidth" ? <Bandwidth /> : <Quotas />}
    </div>
  );
}

const emptyLimits: PackageLimits = { sites: 1, domains: 3, databases: 2, email_domains: 1, mailboxes: 5, disk_mb: 5120, bandwidth_gb: 50,
  cpu_percent: 100, memory_mb: 1024, processes: 100, php_workers: 5, shell: false, containers: false, backups: true };

function Packages() {
  const list = useGet<HostingPackage[]>("/packages");
  const assigned = useGet<Record<string, string>>("/account-packages");
  const accounts = useAsync(() => api.accounts());
  const [edit, setEdit] = useState<HostingPackage | null>(null);
  const [del, setDel] = useState<HostingPackage | null>(null);
  const [assign, setAssign] = useState<{ account: string; pkg: string } | null>(null);
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><h2>Packages</h2><Button variant="primary" onClick={() => setEdit({ id: "", name: "", price: 0, currency: "NGN", interval: "month", limits: { ...emptyLimits }, created: "" })}><Plus size={16} />New package</Button></div>
        {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<Boxes size={22} />} title="No packages yet" text="Make Starter, Business and Pro packages, then give one to each account." /> : (
          <div className="pkg-grid">{list.data.map((p) => (
            <article key={p.id} className="pkg-card">
              <div className="row" style={{ justifyContent: "space-between" }}><h3>{p.name}</h3>{p.owner ? <Badge>{p.owner}</Badge> : null}</div>
              <div className="pkg-price">{p.price ? money(p.price, p.currency) : "Free"}<span className="small muted">{p.interval === "none" ? "" : ` / ${p.interval}`}</span></div>
              <dl className="kv small">
                <dt>Websites</dt><dd>{unlimited(p.limits.sites)}</dd><dt>Extra domains</dt><dd>{unlimited(p.limits.domains)}</dd>
                <dt>Disk</dt><dd>{p.limits.disk_mb ? `${(p.limits.disk_mb / 1024).toFixed(p.limits.disk_mb % 1024 ? 1 : 0)} GB` : "Unlimited"}</dd>
                <dt>Bandwidth</dt><dd>{unlimited(p.limits.bandwidth_gb, " GB/month")}</dd><dt>Mailboxes</dt><dd>{unlimited(p.limits.mailboxes)}</dd>
                <dt>Databases</dt><dd>{unlimited(p.limits.databases)}</dd>
              </dl>
              <div className="row small muted">{[p.limits.shell && "Shell", p.limits.containers && "Containers", p.limits.backups && "Own backups"].filter(Boolean).join(" · ") || "No extras"}</div>
              <div className="row" style={{ justifyContent: "space-between", marginTop: "auto" }}>
                <span className="small muted">{p.accounts ?? 0} accounts</span>
                <span className="row"><Button size="sm" variant="ghost" icon aria-label={`Edit ${p.name}`} onClick={() => setEdit(p)}><Pencil size={15} /></Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Delete ${p.name}`} onClick={() => setDel(p)}><Trash2 size={15} /></Button></span>
              </div>
            </article>
          ))}</div>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Accounts and their packages</h2><p className="small muted">Changing an account's package applies its CPU, memory, process and disk limits right away.</p></div></div>
        {!accounts.data ? <div className="panel-body"><Skeleton /></div> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Account</th><th>Package</th></tr></thead>
            <tbody>{accounts.data.map((a) => (
              <tr key={a.name}><td className="lead">{a.name}</td>
                <td><select className="select" style={{ width: "auto", minWidth: 180 }} aria-label={`Package for ${a.name}`} value={assigned.data?.[a.name] ?? ""}
                  onChange={(e) => setAssign({ account: a.name, pkg: e.target.value })}>
                  <option value="">No package (no limits)</option>{(list.data ?? []).map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}</select></td></tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      {edit ? <PackageModal pkg={edit} onClose={() => setEdit(null)} onSaved={() => list.reload()} /> : null}
      <FormModal open={!!assign} onClose={() => setAssign(null)} title={`Change ${assign?.account}'s package?`} submit="Change package"
        onSubmit={async () => { await api.call("PUT", `/account-packages/${encodeURIComponent(assign!.account)}`, { package: assign!.pkg }); assigned.reload(); list.reload(); }}>
        <p>New limits take effect now. If the account already uses more than the new package allows, nothing is deleted, but it can't add more.</p>
      </FormModal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete ${del?.name}?`} name={del?.name ?? ""} confirmLabel="Delete package"
        description="Only packages no account uses can be deleted." onConfirm={async () => { await api.call("DELETE", `/packages/${del!.id}`); list.reload(); }} />
    </div>
  );
}

function PackageModal({ pkg, onClose, onSaved }: { pkg: HostingPackage; onClose: () => void; onSaved: () => void }) {
  const [p, setP] = useState(pkg);
  const [price, setPrice] = useState(String(pkg.price / 100));
  const L = p.limits;
  const setL = (patch: Partial<PackageLimits>) => setP({ ...p, limits: { ...L, ...patch } });
  return (
    <FormModal open onClose={onClose} title={pkg.id ? `Edit ${pkg.name}` : "New package"} submit="Save package" description="0 means unlimited."
      onSubmit={async () => { await api.call(pkg.id ? "PUT" : "POST", pkg.id ? `/packages/${pkg.id}` : "/packages", { ...p, price: Math.round(Number(price) * 100) }); onSaved(); }}>
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(140px, 1fr))" }}>
        <TextField label="Name" value={p.name} onChange={(v) => setP({ ...p, name: v })} placeholder="Business" />
        <TextField label="Price" value={price} onChange={setPrice} placeholder="15000" />
        <SelectField label="Currency" value={p.currency} onChange={(v) => setP({ ...p, currency: v })} options={["NGN", "USD", "GHS", "KES", "ZAR", "EUR", "GBP"].map((c) => ({ value: c, label: c }))} />
        <SelectField label="Billed" value={p.interval} onChange={(v) => setP({ ...p, interval: v })} options={[{ value: "month", label: "Monthly" }, { value: "year", label: "Yearly" }, { value: "none", label: "Not billed" }]} />
      </div>
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(130px, 1fr))" }}>
        <NumberField label="Websites" value={L.sites} onChange={(v) => setL({ sites: v })} />
        <NumberField label="Extra domains" value={L.domains} onChange={(v) => setL({ domains: v })} />
        <NumberField label="Databases" value={L.databases} onChange={(v) => setL({ databases: v })} />
        <NumberField label="Email domains" value={L.email_domains} onChange={(v) => setL({ email_domains: v })} />
        <NumberField label="Mailboxes" value={L.mailboxes} onChange={(v) => setL({ mailboxes: v })} />
        <NumberField label="Disk (MB)" value={L.disk_mb} onChange={(v) => setL({ disk_mb: v })} />
        <NumberField label="Bandwidth (GB/month)" value={L.bandwidth_gb} onChange={(v) => setL({ bandwidth_gb: v })} />
        <NumberField label="CPU (% of a core)" value={L.cpu_percent} onChange={(v) => setL({ cpu_percent: v })} />
        <NumberField label="Memory (MB)" value={L.memory_mb} onChange={(v) => setL({ memory_mb: v })} />
        <NumberField label="Processes" value={L.processes} onChange={(v) => setL({ processes: v })} />
        <NumberField label="PHP workers" value={L.php_workers} onChange={(v) => setL({ php_workers: v })} />
      </div>
      <div className="row">
        <Toggle checked={L.shell} onChange={(v) => setL({ shell: v })} label="Shell and web terminal" />
        <Toggle checked={L.containers} onChange={(v) => setL({ containers: v })} label="Containers" />
        <Toggle checked={L.backups} onChange={(v) => setL({ backups: v })} label="Own backup plans" />
      </div>
    </FormModal>
  );
}

function Subscriptions() {
  const list = useGet<Subscription[]>("/billing/subscriptions");
  const pkgs = useGet<HostingPackage[]>("/packages");
  const accounts = useAsync(() => api.accounts());
  const [add, setAdd] = useState(false);
  const [f, setF] = useState({ account: "", package_id: "", first_due: new Date().toISOString().slice(0, 10) });
  const { run } = useAction();
  const pkgName = (id: string) => pkgs.data?.find((p) => p.id === id)?.name ?? id;
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Subscriptions</h2><p className="small muted">Invoices are made 7 days before each due date and sent to the customer. Unpaid invoices suspend the account after the grace days set under Billing.</p></div>
        <Button variant="primary" onClick={() => setAdd(true)}><CreditCard size={16} />New subscription</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<CreditCard size={22} />} title="No subscriptions" text="Subscribe an account to its package to bill it every month or year." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Account</th><th>Package</th><th>Price</th><th>Next invoice</th><th /></tr></thead>
          <tbody>{list.data.map((s) => (
            <tr key={s.id}><td className="lead">{s.account}</td><td>{pkgName(s.package_id)}</td><td>{money(s.price, s.currency)} / {s.interval}</td>
              <td>{s.status === "active" ? new Date(s.next_due).toLocaleDateString(undefined, { dateStyle: "medium" }) : <Badge>Cancelled</Badge>}</td>
              <td style={{ textAlign: "right" }}>{s.status === "active" ? <Button size="sm" variant="ghost" onClick={() => run("c", () => api.call("POST", `/billing/subscriptions/${s.id}/cancel`), "Subscription cancelled").then(() => list.reload())}>Cancel</Button> : null}</td></tr>
          ))}</tbody>
        </table></div>
      )}
      <FormModal open={add} onClose={() => setAdd(false)} title="New subscription" submit="Subscribe" onSubmit={async () => {
        await api.call("POST", "/billing/subscriptions", { account: f.account, package_id: f.package_id, first_due: new Date(f.first_due).toISOString() }); list.reload();
      }}>
        <SelectField label="Account" value={f.account} onChange={(v) => setF({ ...f, account: v })} options={[{ value: "", label: "Choose…" }, ...(accounts.data ?? []).map((a) => ({ value: a.name, label: a.name }))]} />
        <SelectField label="Package" value={f.package_id} onChange={(v) => setF({ ...f, package_id: v })} options={[{ value: "", label: "Choose…" }, ...(pkgs.data ?? []).filter((p) => p.interval !== "none").map((p) => ({ value: p.id, label: `${p.name} (${money(p.price, p.currency)} / ${p.interval})` }))]} />
        <TextField label="First due date" type="date" value={f.first_due} onChange={(v) => setF({ ...f, first_due: v })} />
      </FormModal>
    </section>
  );
}

function Bandwidth() {
  const bw = useGet<{ month: string; accounts: BandwidthRow[] }>("/bandwidth");
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Bandwidth this month</h2><p className="small muted">Accounts that use up their package's monthly bandwidth are suspended until the 1st, or until you give them a bigger package.</p></div></div>
      {!bw.data ? <div className="panel-body"><Skeleton /></div> : bw.data.accounts.length === 0 ? <Empty icon={<Gauge size={22} />} title="No traffic counted yet" text="Usage is added up every 15 minutes." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Account</th><th style={{ width: "40%" }}>Used</th><th>Status</th></tr></thead>
          <tbody>{bw.data.accounts.map((r) => (
            <tr key={r.account}><td className="lead">{r.account}</td>
              <td>{r.limit_gb ? <><Meter value={(r.used_mb / 1024 / r.limit_gb) * 100} label={`Bandwidth of ${r.account}`} /><div className="small muted">{(r.used_mb / 1024).toFixed(1)} of {r.limit_gb} GB</div></> : <span className="small">{(r.used_mb / 1024).toFixed(1)} GB, no limit</span>}</td>
              <td>{r.suspended_for_bandwidth ? <Badge tone="bad">Suspended</Badge> : <Badge tone="ok">OK</Badge>}</td></tr>
          ))}</tbody>
        </table></div>
      )}
    </section>
  );
}

function Quotas() {
  const q = useGet<{ enabled: boolean; note?: string; accounts: QuotaUsage[] }>("/quotas");
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Disk quotas</h2><p className="small muted">The kernel stops an account from writing more than its disk limit.</p></div></div>
      {!q.data ? <div className="panel-body"><Skeleton /></div> : (
        <>
          {q.data.note ? <div className="panel-body"><div className={`callout ${q.data.enabled ? "warn" : ""}`}>{q.data.note}</div></div> : null}
          {q.data.accounts.length ? <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Account</th><th style={{ width: "45%" }}>Used</th></tr></thead>
            <tbody>{q.data.accounts.map((a) => (
              <tr key={a.account}><td className="lead">{a.account}{a.over ? <> <Badge tone="bad">Full</Badge></> : null}</td>
                <td>{a.hard_mb ? <><Meter value={(a.used_mb / a.hard_mb) * 100} label={`Disk of ${a.account}`} /><div className="small muted">{a.used_mb.toFixed(0)} of {a.hard_mb.toFixed(0)} MB</div></> : <span className="small">{a.used_mb.toFixed(0)} MB, no limit</span>}</td></tr>
            ))}</tbody>
          </table></div> : null}
        </>
      )}
    </section>
  );
}

/* ---------------------------------------------------------------- branding */

export function BrandingPage({ role }: { role: Role }) {
  const cur = useGet<Branding>("/me/branding");
  const [b, setB] = useState<Branding | null>(null);
  const [hosts, setHosts] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { busy, run } = useAction();
  const v = b ?? cur.data ?? null;
  return (
    <div className="stack">
      <PageHead title="Branding" text={role === "reseller" ? "Your customers see your name, colour and logo instead of Ike's, on the panel addresses you give them." : "The name, colour and logo every person sees, unless a reseller sets their own for their customers."} />
      {!v ? <Skeleton h={240} /> : (
        <div className="grid-2">
          <section className="panel">
            <div className="panel-body form-grid">
              <TextField label="Product name" value={v.product_name} onChange={(x) => setB({ ...v, product_name: x })} placeholder="WebAfrica Hosting" />
              <Field label="Main colour">{(id) => <div className="row"><input id={id} type="color" value={v.primary_color || "#2357d6"} onChange={(e) => setB({ ...v, primary_color: e.target.value })} style={{ width: 48, height: 36, border: 0, background: "none" }} />
                <input className="input mono" style={{ width: 120 }} value={v.primary_color} onChange={(e) => setB({ ...v, primary_color: e.target.value })} placeholder="#2357d6" aria-label="Colour code" />
                {v.primary_color ? <Button size="sm" variant="ghost" onClick={() => setB({ ...v, primary_color: "" })}>Use the theme's</Button> : null}</div>}</Field>
              <Field label="Logo" hint="PNG, WebP or JPEG, square, under 200 KB.">{(id) => <div className="row">
                {v.logo ? <img src={v.logo} alt="" width={40} height={40} style={{ borderRadius: 8, objectFit: "contain", border: "1px solid var(--line)" }} /> : null}
                <input id={id} type="file" accept="image/png,image/webp,image/jpeg" onChange={(e) => {
                  const f = e.target.files?.[0];
                  if (!f) return;
                  if (f.size > 200_000) { setError("The logo is larger than 200 KB."); return; }
                  const rd = new FileReader();
                  rd.onload = () => setB({ ...v, logo: String(rd.result) });
                  rd.readAsDataURL(f);
                }} />{v.logo ? <Button size="sm" variant="ghost" onClick={() => setB({ ...v, logo: "" })}>Remove</Button> : null}</div>}</Field>
              <TextField label="Support email" value={v.support_email ?? ""} onChange={(x) => setB({ ...v, support_email: x })} />
              <TextField label="Support page" value={v.support_url ?? ""} onChange={(x) => setB({ ...v, support_url: x })} placeholder="https://help.example.com" />
              <Field label="Panel addresses" hint="Host names your customers open, one per line, pointed at this server. The panel shows this brand there, even before sign-in.">{(id) =>
                <textarea id={id} className="input mono" rows={2} value={hosts ?? (v.hostnames ?? []).join("\n")} onChange={(e) => setHosts(e.target.value)} placeholder="panel.webafrica.ng" />}</Field>
              <Toggle checked={!!v.hide_powered_by} onChange={(x) => setB({ ...v, hide_powered_by: x })} label={`Hide "Powered by Ike"`} />
              <ErrorNote text={error} />
              <div><Button variant="primary" loading={busy === "save"} onClick={() => { setError(null); run("save", () => api.call<Branding>("PUT", "/me/branding", { ...v, hostnames: (hosts ?? (v.hostnames ?? []).join("\n")).split(/\s+/).filter(Boolean) }), "Branding saved").then((x) => { if (x) { cur.setData(x); setB(null); loadBranding(); } }); }}>Save</Button></div>
            </div>
          </section>
          <section className="panel">
            <div className="panel-head"><h2>Preview</h2></div>
            <div className="panel-body">
              <div className="brand-preview" style={{ ["--p" as string]: v.primary_color || "var(--accent)" }}>
                <div className="row" style={{ gap: 10 }}>{v.logo ? <img src={v.logo} alt="" width={30} height={30} style={{ borderRadius: 7 }} /> : <span className="bp-mark" />}<strong>{v.product_name || "Your brand"}</strong></div>
                <div className="bp-bar" /><div className="bp-bar short" /><button type="button" className="bp-btn">Add website</button>
                {!v.hide_powered_by ? <div className="small muted" style={{ marginTop: 12 }}>Powered by Ike</div> : null}
              </div>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

/* ------------------------------------------------------------------ tokens */

const areas: [string, string][] = [["sites", "Websites"], ["dns", "DNS"], ["mail", "Email"], ["databases", "Databases"], ["files", "Files and terminal"], ["backups", "Backups and storage"],
  ["containers", "Containers"], ["security", "Security"], ["billing", "Billing and packages"], ["accounts", "Accounts and SSH"], ["server", "Server settings"]];

export function TokensPage({ role }: { role: Role }) {
  const list = useGet<APIToken[]>("/tokens");
  const accounts = useAsync(() => api.accounts());
  const [add, setAdd] = useState(false);
  const [f, setF] = useState({ name: "", days: 0, read_only: false, limited: false, scopes: {} as Record<string, string>, accounts: [] as string[], ips: "" });
  const [made, setMade] = useState<{ token: string; name: string } | null>(null);
  const [del, setDel] = useState<APIToken | null>(null);
  const panel = isDemo ? "https://panel.example.com:2087" : window.location.origin;
  return (
    <div className="stack">
      <PageHead title="API tokens" text="Tokens sign in scripts, the ike command-line tool, AI agents (MCP) and billing systems like WHMCS and Blesta. Give each one only the access it needs."
        actions={<Button variant="primary" onClick={() => setAdd(true)}><KeyRound size={16} />New token</Button>} />
      <section className="panel">
        {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<KeyRound size={22} />} title="No tokens yet" text="Make one for each tool, so you can remove one without breaking the others." /> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Token</th><th>Access</th><th data-hide-sm>Last used</th><th /></tr></thead>
            <tbody>{list.data.map((t) => (
              <tr key={t.id}>
                <td className="lead">{t.name}<div className="small muted">{t.expires && new Date(t.expires).getFullYear() > 2000 ? `Expires ${dateTime(t.expires)}` : "Never expires"}</div></td>
                <td className="small">{t.read_only ? <Badge>Read only</Badge> : null} {t.limited ? Object.entries(t.scopes ?? {}).map(([a, l]) => <Badge key={a} tone={l === "write" ? "accent" : undefined}>{a}: {l}</Badge>) : <Badge tone="warn">Full access</Badge>}
                  {t.accounts?.length ? <div className="muted">Accounts: {t.accounts.join(", ")}</div> : null}{t.allow_ips?.length ? <div className="muted mono">From {t.allow_ips.join(", ")}</div> : null}</td>
                <td className="small muted" data-hide-sm>{t.last_used && new Date(t.last_used).getFullYear() > 2000 ? timeAgo(t.last_used) : "Never"}</td>
                <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" icon aria-label={`Delete ${t.name}`} onClick={() => setDel(t)}><Trash2 size={15} /></Button></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><h2>Using a token</h2></div>
        <div className="panel-body stack">
          <Field label="API">{() => <CopyBlock value={`curl -H "Authorization: Bearer ike_…" ${panel}/api/v1/sites`} />}</Field>
          <Field label="ike command-line tool">{() => <CopyBlock value={`ike login ${panel} --token ike_…\nike sites\nike deploy shop.ng --wait`} />}</Field>
          <Field label="AI agents (Claude Desktop, Claude Code and other MCP clients)">{() => <CopyBlock value={JSON.stringify({ mcpServers: { ike: { command: "ike-mcp", args: ["--panel", panel, "--token", "ike_…"] } } }, null, 2)} />}</Field>
          <p className="small muted">An AI agent can only do what its token allows. Give it a limited, read-only token to start, and add write access for the areas you want it to change.</p>
        </div>
      </section>
      <FormModal open={add} onClose={() => setAdd(false)} title="New API token" submit="Create token" onSubmit={async () => {
        const r = await api.call<{ token: string; name: string }>("POST", "/tokens", { name: f.name, days: f.days, read_only: f.read_only, limited: f.limited,
          scopes: f.limited ? f.scopes : {}, accounts: f.accounts, allow_ips: f.ips.split(/[\s,]+/).filter(Boolean) });
        setMade(r); list.reload(); setF({ name: "", days: 0, read_only: false, limited: false, scopes: {}, accounts: [], ips: "" });
      }}>
        <TextField label="Name" value={f.name} onChange={(v) => setF({ ...f, name: v })} placeholder="WHMCS, deploy script, Claude…" autoFocus />
        <Segmented label="Access" value={f.limited ? "limited" : "full"} onChange={(v) => setF({ ...f, limited: v === "limited" })}
          options={[{ value: "full", label: "Everything I can do" }, { value: "limited", label: "Only what I choose" }]} />
        {f.limited ? (
          <div className="scope-grid">{areas.filter(([a]) => role === "admin" || a !== "server").map(([a, label]) => (
            <div key={a} className="scope-row"><span>{label}</span>
              <Segmented label={label} value={(f.scopes[a] ?? "none") as "none" | "read" | "write"} onChange={(v) => setF({ ...f, scopes: { ...f.scopes, [a]: v } })}
                options={[{ value: "none", label: "None" }, { value: "read", label: "Read" }, { value: "write", label: "Change" }]} /></div>))}</div>
        ) : <Toggle checked={f.read_only} onChange={(v) => setF({ ...f, read_only: v })} label="Read only (can look, can't change anything)" />}
        {role !== "user" ? <Field label="Only these accounts (optional)" hint="Leave all off for every account you manage.">{() => <div className="row">{(accounts.data ?? []).map((a) => (
          <Toggle key={a.name} checked={f.accounts.includes(a.name)} label={a.name} onChange={(v) => setF({ ...f, accounts: v ? [...f.accounts, a.name] : f.accounts.filter((x) => x !== a.name) })} />))}</div>}</Field> : null}
        <TextField label="Only from these IP addresses (optional)" value={f.ips} onChange={(v) => setF({ ...f, ips: v })} mono placeholder="203.0.113.7, 198.51.100.0/24" />
        <SelectField label="Expires" value={String(f.days)} onChange={(v) => setF({ ...f, days: Number(v) })} options={[{ value: "0", label: "Never" }, { value: "7", label: "In 7 days" }, { value: "30", label: "In 30 days" }, { value: "90", label: "In 90 days" }, { value: "365", label: "In a year" }]} />
      </FormModal>
      <OnceModal open={!!made} onClose={() => setMade(null)} title={`Token for ${made?.name}`}><CopyField value={made?.token ?? ""} label="API token" /></OnceModal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete ${del?.name}?`} name={del?.name ?? ""} confirmLabel="Delete token"
        description="Anything using this token stops working right away." onConfirm={async () => { await api.call("DELETE", `/tokens/${del!.id}`); list.reload(); }} />
    </div>
  );
}

/* ------------------------------------------------------------ notifications */

export function NotificationsPage() {
  const list = useGet<ServerEvent[]>("/notifications");
  const [level, setLevel] = useState<"all" | "warn" | "bad">("all");
  const { run } = useAction();
  const items = (list.data ?? []).filter((e) => level === "all" || (level === "bad" ? e.level === "bad" : e.level !== "info"));
  return (
    <div className="stack">
      <PageHead title="Notifications" text="What happened on the server: backups, certificates, domains about to expire, malware, mail and more. Serious ones are also sent to your alert channels."
        actions={<>
          <Segmented label="Show" value={level} onChange={setLevel} options={[{ value: "all", label: "All" }, { value: "warn", label: "Warnings" }, { value: "bad", label: "Problems" }]} />
          <Button onClick={() => run("r", () => api.call("POST", "/notifications/read", { up_to: Math.max(0, ...(list.data ?? []).map((e) => e.id)) })).then(() => list.reload())}>Mark all as read</Button></>} />
      <section className="panel">
        {!list.data ? <div className="panel-body"><Skeleton /></div> : items.length === 0 ? <Empty icon={<Bell size={22} />} title="Nothing here" text="Events show up as they happen." /> : (
          <ul className="notif-list" style={{ maxHeight: "none" }}>{items.map((e) => (
            <li key={e.id} data-unread={e.unread ? "1" : undefined}>
              <span className="dot" style={{ background: e.level === "bad" ? "var(--bad)" : e.level === "warn" ? "var(--warn)" : "var(--accent)" }} aria-hidden />
              <div style={{ minWidth: 0 }}><div style={{ fontWeight: 600 }}>{describeEvent(e)}</div>
                <div className="small muted">{e.subject} · {dateTime(e.time)}{e.data?.account ? ` · ${String(e.data.account)}` : ""}</div></div>
            </li>))}</ul>
        )}
      </section>
    </div>
  );
}
