// Infrastructure: other servers joined over mutual TLS, database clusters,
// hardware inventory, maintenance windows, event webhooks and server specs
// (infrastructure as code: export, plan, apply, templates).
import { useState } from "react";
import { Boxes, CalendarClock, Cpu, FileCode2, Pencil, Plus, Rocket, Send, Server, Trash2, Webhook as WebhookIcon } from "lucide-react";
import { api } from "../api/client";
import type { Job } from "../api/types";
import type { DBCluster, Inventory, JoinTokenResult, MaintenanceWindow, RemoteServer, SpecChange, SpecTemplate, Webhook, WebhookDelivery } from "../api/types5";
import { Badge, Button, CopyBlock, CopyField, Empty, Field, Skeleton, Tabs, timeAgo, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { dateTime, enc, ErrorNote, FormModal, OnceModal, SelectField, TextField, useAction, useGet } from "../components/kit5";

export function InfraPage({ route }: { route: string }) {
  const initial = (new URLSearchParams(route.split("?")[1] || "").get("tab") as Tab) || "servers";
  const [tab, setTab] = useState<Tab>(initial);
  return (
    <div className="stack">
      <PageHead title="Infrastructure" text="Manage several Ike servers from this panel, keep databases replicated between them, and describe servers as code." />
      <Tabs value={tab} onChange={setTab} tabs={[
        { value: "servers", label: "Servers" }, { value: "clusters", label: "Clusters" }, { value: "inventory", label: "Inventory" },
        { value: "maintenance", label: "Maintenance" }, { value: "webhooks", label: "Webhooks" }, { value: "specs", label: "Server specs" },
      ]} />
      {tab === "servers" ? <Servers /> : tab === "clusters" ? <Clusters /> : tab === "inventory" ? <InventoryPanel /> : tab === "maintenance" ? <Maintenance /> : tab === "webhooks" ? <Webhooks /> : <Specs />}
    </div>
  );
}
type Tab = "servers" | "clusters" | "inventory" | "maintenance" | "webhooks" | "specs";

function Servers() {
  const list = useGet<RemoteServer[]>("/servers");
  const [add, setAdd] = useState(false);
  const [f, setF] = useState({ name: "", group: "", address: "" });
  const [tok, setTok] = useState<JoinTokenResult | null>(null);
  const [edit, setEdit] = useState<RemoteServer | null>(null);
  const [del, setDel] = useState<RemoteServer | null>(null);
  const groups = [...new Set((list.data ?? []).map((s) => s.group).filter(Boolean))] as string[];
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Servers</h2><p className="small muted">Each server keeps running its own agent. The panel talks to it over mutual TLS on port 2089, so no root passwords or SSH keys are shared.</p></div>
        <Button variant="primary" onClick={() => setAdd(true)}><Plus size={16} />Add a server</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? (
        <Empty icon={<Server size={22} />} title="Only this server so far" text="Add another server with a one-time join command. Afterwards pick it in the server switcher to manage it." />
      ) : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Server</th><th>Group</th><th>Status</th><th data-hide-sm>Version</th><th /></tr></thead>
          <tbody>{list.data.map((s) => (
            <tr key={s.id}>
              <td className="lead"><div className="primary-cell">{s.name}</div><div className="small muted mono">{s.hostname || s.address}</div></td>
              <td>{s.group ? <Badge>{s.group}</Badge> : <span className="muted">None</span>}</td>
              <td>{s.status === "online" ? <Badge tone="ok">Online</Badge> : <Badge tone="bad">Offline</Badge>}<div className="small muted">{s.last_seen ? `seen ${timeAgo(s.last_seen)}` : ""}</div></td>
              <td className="small mono" data-hide-sm>{s.version}</td>
              <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                <Button size="sm" variant="ghost" icon aria-label="Edit" onClick={() => setEdit(s)}><Pencil size={15} /></Button>
                <Button size="sm" variant="ghost" icon aria-label="Remove" onClick={() => setDel(s)}><Trash2 size={15} /></Button>
              </td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <FormModal open={add} onClose={() => setAdd(false)} title="Add a server" submit="Make join command"
        description="Ike makes a one-time command that works for 2 hours. Run it on the new server." onSubmit={async () => { setTok(await api.call<JoinTokenResult>("POST", "/servers/join-token", f)); }}>
        <TextField label="Name" value={f.name} onChange={(v) => setF({ ...f, name: v })} placeholder="vps-abuja-2" autoFocus />
        <TextField label="Group (optional)" value={f.group} onChange={(v) => setF({ ...f, group: v })} placeholder={groups[0] || "web"} hint="Groups let you run updates or commands on several servers at once." />
        <TextField label="Address (optional)" value={f.address} onChange={(v) => setF({ ...f, address: v })} mono hint="Only if the panel can't reach the new server at the address it connects from (NAT)." />
      </FormModal>
      <OnceModal open={!!tok} onClose={() => { setTok(null); list.reload(); }} title="Join command">
        <Field label="If Ike is already installed on the new server">{() => <CopyBlock value={tok?.command ?? ""} />}</Field>
        <Field label="Or install Ike and join in one step">{() => <CopyBlock value={tok?.install ?? ""} />}</Field>
        <p className="small muted">The fingerprint ({tok?.fingerprint.slice(0, 16)}…) lets the new server check it's really talking to this panel. Expires {dateTime(tok?.expires)}.</p>
      </OnceModal>
      {edit ? <FormModal open onClose={() => setEdit(null)} title={`Edit ${edit.name}`} submit="Save" onSubmit={async () => { await api.call("PATCH", `/servers/${enc(edit.id)}`, { name: edit.name, group: edit.group ?? "" }); list.reload(); }}>
        <TextField label="Name" value={edit.name} onChange={(v) => setEdit({ ...edit, name: v })} />
        <TextField label="Group" value={edit.group ?? ""} onChange={(v) => setEdit({ ...edit, group: v })} />
      </FormModal> : null}
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Remove ${del?.name}?`} name={del?.name ?? ""} confirmLabel="Remove server"
        description="The panel stops managing it and its certificate is revoked. The server and its websites keep running." onConfirm={async () => { await api.call("DELETE", `/servers/${enc(del!.id)}`); list.reload(); }} />
    </section>
  );
}

interface ClusterView extends DBCluster { members?: { server: string; role: string; status?: Record<string, unknown>; error?: string }[] }

function Clusters() {
  const toast = useToast();
  const list = useGet<ClusterView[]>("/clusters");
  const servers = useGet<RemoteServer[]>("/servers");
  const [add, setAdd] = useState(false);
  const [f, setF] = useState({ name: "", engine: "mariadb", primary: "local", replicas: [] as string[] });
  const [promote, setPromote] = useState<{ c: ClusterView; server: string } | null>(null);
  const opts = [{ value: "local", label: "This server" }, ...(servers.data ?? []).map((s) => ({ value: s.id, label: s.name }))];
  const nameOf = (id: string) => opts.find((o) => o.value === id)?.label ?? id;
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Database clusters</h2><p className="small muted">A primary MariaDB or Redis with live copies on other servers. If the primary fails, promote a copy.</p></div>
        <Button variant="primary" disabled={(servers.data ?? []).length === 0} onClick={() => setAdd(true)}><Plus size={16} />New cluster</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<Boxes size={22} />} title="No clusters" text="Add at least one other server first, then make a cluster." /> : (
        <div className="panel-body stack">{list.data.map((c) => (
          <div key={c.id} className="callout" style={{ display: "grid", gap: 8 }}>
            <div className="row" style={{ justifyContent: "space-between" }}><strong>{c.name}</strong><Badge>{c.engine === "mariadb" ? "MariaDB" : "Redis"}</Badge></div>
            <ul style={{ listStyle: "none", margin: 0, padding: 0 }} className="stack">{(c.members ?? [{ server: c.primary, role: "primary" }, ...c.replicas.map((r) => ({ server: r, role: "replica" }))]).map((m) => (
              <li key={m.server} className="row" style={{ justifyContent: "space-between" }}>
                <span>{nameOf(m.server)} <Badge tone={m.role === "primary" ? "accent" : undefined}>{m.role}</Badge>{m.error ? <span className="small" style={{ color: "var(--bad)" }}> {m.error}</span> : null}</span>
                {m.role !== "primary" ? <Button size="sm" onClick={() => setPromote({ c, server: m.server })}>Make primary</Button> : null}
              </li>))}</ul>
            <div><Button size="sm" variant="ghost" onClick={async () => { try { await api.call("DELETE", `/clusters/${enc(c.id)}`); list.reload(); } catch (e) { toast("error", (e as Error).message); } }}><Trash2 size={14} />Stop managing as a cluster</Button></div>
          </div>
        ))}</div>
      )}
      <FormModal open={add} onClose={() => setAdd(false)} title="New database cluster" submit="Create cluster" description="Ike sets up replication from the primary to each copy. The copies become read-only."
        onSubmit={async () => { await api.call("POST", "/clusters", f); list.reload(); }}>
        <TextField label="Name" value={f.name} onChange={(v) => setF({ ...f, name: v })} placeholder="main-db" />
        <SelectField label="Engine" value={f.engine} onChange={(v) => setF({ ...f, engine: v })} options={[{ value: "mariadb", label: "MariaDB" }, { value: "redis", label: "Redis / Valkey" }]} />
        <SelectField label="Primary" value={f.primary} onChange={(v) => setF({ ...f, primary: v })} options={opts} />
        <Field label="Copies on">{() => <div className="stack" style={{ gap: 6 }}>{opts.filter((o) => o.value !== f.primary).map((o) => (
          <Toggle key={o.value} checked={f.replicas.includes(o.value)} label={o.label}
            onChange={(v) => setF({ ...f, replicas: v ? [...f.replicas, o.value] : f.replicas.filter((x) => x !== o.value) })} />))}</div>}</Field>
      </FormModal>
      <FormModal open={!!promote} onClose={() => setPromote(null)} title={`Make ${promote ? nameOf(promote.server) : ""} the primary?`} submit="Promote" danger
        onSubmit={async () => { await api.call("POST", `/clusters/${enc(promote!.c.id)}/promote`, { server: promote!.server }); list.reload(); }}>
        <p>Writes move to this server. Point apps at it, or use the cluster's address if you set one up. The old primary becomes a copy when it's back.</p>
      </FormModal>
    </section>
  );
}

function InventoryPanel() {
  const inv = useGet<Inventory>("/inventory");
  const [asset, setAsset] = useState<Inventory["asset"] | null>(null);
  const { busy, run } = useAction();
  const i = inv.data;
  const a = asset ?? i?.asset ?? null;
  return !i ? <Skeleton h={240} /> : (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2><Cpu size={18} style={{ verticalAlign: -3 }} /> {i.hostname}</h2><p className="small muted">Collected {timeAgo(i.collected_at)}</p></div></div>
        <div className="panel-body">
          <dl className="kv">
            <dt>System</dt><dd>{i.os}, kernel {i.kernel}</dd><dt>Machine</dt><dd>{[i.vendor, i.product].filter(Boolean).join(" ") || i.virtualization}{i.serial ? ` · ${i.serial}` : ""}</dd>
            <dt>Processor</dt><dd>{i.cpus} × {i.cpu_model}</dd><dt>Memory</dt><dd>{(i.memory_mb / 1024).toFixed(1)} GB{i.swap_mb ? ` + ${(i.swap_mb / 1024).toFixed(1)} GB swap` : ""}</dd>
            <dt>Up since</dt><dd>{dateTime(i.booted_at)} ({Math.round(i.uptime_hours / 24)} days)</dd>
          </dl>
          <h3 style={{ fontSize: "0.95rem", margin: "16px 0 8px" }}>Disks</h3>
          <ul className="small" style={{ margin: 0, paddingLeft: 18 }}>{i.disks.map((d) => <li key={d.name}><span className="mono">{d.name}</span> {d.size_gb} GB {d.type}{d.mount ? ` on ${d.mount} (${d.filesystem})` : ""}{d.model ? ` · ${d.model}` : ""}{d.rotational ? " · HDD" : ""}</li>)}</ul>
          <h3 style={{ fontSize: "0.95rem", margin: "16px 0 8px" }}>Network</h3>
          <ul className="small" style={{ margin: 0, paddingLeft: 18 }}>{i.interfaces.map((n) => <li key={n.name}><span className="mono">{n.name}</span> {n.state} · {n.addresses.join(", ")}</li>)}</ul>
        </div>
      </section>
      {a ? <section className="panel">
        <div className="panel-head"><div><h2>Asset record</h2><p className="small muted">Your own notes about this machine: who pays for it and when it renews.</p></div></div>
        <div className="panel-body form-grid">
          {([["tag", "Asset tag"], ["owner", "Owner"], ["provider", "Provider"], ["location", "Location"], ["monthly_cost", "Monthly cost"]] as const).map(([k, l]) => (
            <TextField key={k} label={l} value={a[k]} onChange={(v) => setAsset({ ...a, [k]: v })} />))}
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))" }}>
            <TextField label="Bought" type="date" value={a.purchase_date} onChange={(v) => setAsset({ ...a, purchase_date: v })} />
            <TextField label="Renews" type="date" value={a.renewal_date} onChange={(v) => setAsset({ ...a, renewal_date: v })} />
          </div>
          <Field label="Notes">{(id) => <textarea id={id} className="input" rows={3} value={a.notes} onChange={(e) => setAsset({ ...a, notes: e.target.value })} />}</Field>
          <div><Button variant="primary" loading={busy === "a"} onClick={() => run("a", () => api.call("PUT", "/inventory/asset", a), "Asset record saved")}>Save</Button></div>
        </div>
      </section> : null}
    </div>
  );
}

function Maintenance() {
  const list = useGet<MaintenanceWindow[]>("/maintenance");
  const [add, setAdd] = useState(false);
  const soon = new Date(Date.now() + 86400000);
  soon.setHours(2, 0, 0, 0);
  const local = (d: Date) => new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16);
  const [f, setF] = useState({ title: "Monthly updates", start: local(soon), end: local(new Date(soon.getTime() + 3600000)), sites: "all", updates: true, reboot: false });
  const { run } = useAction();
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Maintenance windows</h2><p className="small muted">At the start, websites show your maintenance page and updates install; at the end everything goes back to normal.</p></div>
        <Button variant="primary" onClick={() => setAdd(true)}><CalendarClock size={16} />Schedule</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<CalendarClock size={22} />} title="Nothing scheduled" text="Plan updates and reboots for a quiet time." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Window</th><th>When</th><th>Does</th><th>State</th><th /></tr></thead>
          <tbody>{list.data.map((w) => (
            <tr key={w.id}><td className="lead">{w.title}</td><td className="small">{dateTime(w.start)} → {dateTime(w.end)}</td>
              <td className="small">{[w.sites[0] === "-" ? "" : w.sites.length ? `${w.sites.length} sites offline` : "All sites offline", w.updates ? "updates" : "", w.reboot ? "reboot" : ""].filter(Boolean).join(", ")}</td>
              <td><Badge tone={w.state === "active" ? "warn" : w.state === "done" ? "ok" : undefined}>{w.state}</Badge></td>
              <td style={{ textAlign: "right" }}>{w.state === "scheduled" || w.state === "active" ? <Button size="sm" variant="ghost" onClick={() => run("x", () => api.call("DELETE", `/maintenance/${enc(w.id)}`), "Cancelled").then(() => list.reload())}>Cancel</Button> : null}</td></tr>
          ))}</tbody>
        </table></div>
      )}
      <FormModal open={add} onClose={() => setAdd(false)} title="Schedule maintenance" submit="Schedule" onSubmit={async () => {
        await api.call("POST", "/maintenance", { title: f.title, start: new Date(f.start).toISOString(), end: new Date(f.end).toISOString(), sites: f.sites === "all" ? [] : ["-"], updates: f.updates, reboot: f.reboot });
        list.reload();
      }}>
        <TextField label="Title" value={f.title} onChange={(v) => setF({ ...f, title: v })} />
        <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
          <TextField label="Starts" type="datetime-local" value={f.start} onChange={(v) => setF({ ...f, start: v })} />
          <TextField label="Ends" type="datetime-local" value={f.end} onChange={(v) => setF({ ...f, end: v })} />
        </div>
        <SelectField label="Websites" value={f.sites} onChange={(v) => setF({ ...f, sites: v })} options={[{ value: "all", label: "Show the maintenance page on every website" }, { value: "none", label: "Leave websites online" }]} />
        <Toggle checked={f.updates} onChange={(v) => setF({ ...f, updates: v })} label="Install Ubuntu updates" />
        <Toggle checked={f.reboot} onChange={(v) => setF({ ...f, reboot: v })} label="Reboot if updates need it" />
      </FormModal>
    </section>
  );
}

function Webhooks() {
  const list = useGet<Webhook[]>("/webhooks");
  const deliveries = useGet<WebhookDelivery[]>("/webhooks/deliveries");
  const [edit, setEdit] = useState<Webhook | null>(null);
  const [secret, setSecret] = useState<string | null>(null);
  const { run } = useAction();
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><div><h2>Event webhooks</h2><p className="small muted">Ike POSTs a JSON event to your URL when something happens: a backup fails, a website is created, malware is found. Each request is signed with HMAC-SHA256 in the X-Ike-Signature header.</p></div>
          <Button variant="primary" onClick={() => setEdit({ id: "", url: "https://", events: ["*"], enabled: true, created_at: "" })}><Plus size={16} />Add webhook</Button></div>
        {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<WebhookIcon size={22} />} title="No webhooks" text="Connect Slack, Discord, n8n, Zapier or your own service." /> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>URL</th><th>Events</th><th /></tr></thead>
            <tbody>{list.data.map((w) => (
              <tr key={w.id}><td className="lead mono small" style={{ wordBreak: "break-all" }}>{w.url}{!w.enabled ? <> <Badge>Off</Badge></> : null}</td><td className="small mono">{w.events.join(", ")}</td>
                <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                  <Button size="sm" variant="ghost" onClick={() => run("t", () => api.call("POST", `/webhooks/${enc(w.id)}/test`), "Test event sent").then(() => deliveries.reload())}><Send size={14} />Test</Button>
                  <Button size="sm" variant="ghost" icon aria-label="Edit" onClick={() => setEdit(w)}><Pencil size={15} /></Button>
                  <Button size="sm" variant="ghost" icon aria-label="Delete" onClick={() => run("d", () => api.call("DELETE", `/webhooks/${enc(w.id)}`), "Webhook removed").then(() => list.reload())}><Trash2 size={15} /></Button></td></tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><h2>Recent deliveries</h2></div>
        {!deliveries.data?.length ? <div className="panel-body"><p className="muted small">Nothing sent yet.</p></div> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>When</th><th>Event</th><th>Result</th></tr></thead>
            <tbody>{deliveries.data.slice(0, 30).map((d, i) => (
              <tr key={i}><td className="small muted">{timeAgo(d.at)}</td><td className="mono small">{d.type}</td>
                <td>{d.status >= 200 && d.status < 300 ? <Badge tone="ok">{d.status}</Badge> : <Badge tone="bad">{d.status || "failed"}</Badge>} <span className="small muted">{d.error}{d.attempt > 1 ? ` (try ${d.attempt})` : ""}</span></td></tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      {edit ? <FormModal open onClose={() => setEdit(null)} title={edit.id ? "Edit webhook" : "Add webhook"} submit="Save" onSubmit={async () => {
        const w = await api.call<Webhook>(edit.id ? "PUT" : "POST", edit.id ? `/webhooks/${enc(edit.id)}` : "/webhooks", edit);
        if (w.secret) setSecret(w.secret);
        list.reload();
      }}>
        <TextField label="URL" value={edit.url} onChange={(v) => setEdit({ ...edit, url: v })} mono hint="HTTPS only, and not an address inside your network." />
        <TextField label="Events" value={edit.events.join(", ")} onChange={(v) => setEdit({ ...edit, events: v.split(/[\s,]+/).filter(Boolean) })} mono
          hint="* for everything, or patterns like backup.*, site.created, malware.*, dns.domain.expiring" />
        <Toggle checked={edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Send events" />
      </FormModal> : null}
      <OnceModal open={!!secret} onClose={() => setSecret(null)} title="Signing secret"><p className="small">Use it to check the X-Ike-Signature header (HMAC-SHA256 of the body).</p><CopyField value={secret ?? ""} label="Secret" /></OnceModal>
    </div>
  );
}

const sampleSpec = `{
  "accounts": [{ "name": "shop" }],
  "sites": [{ "domain": "shop.ng", "account": "shop", "runtime": "php", "https": true }],
  "databases": [{ "account": "shop", "name": "store" }],
  "dns": [{ "account": "shop", "name": "shop.ng", "template": "website-email" }],
  "mail": [{ "account": "shop", "domain": "shop.ng", "mailboxes": [{ "local": "info" }] }]
}`;

function Specs() {
  const toast = useToast();
  const [text, setText] = useState(sampleSpec);
  const [vars, setVars] = useState("");
  const [plan, setPlan] = useState<SpecChange[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const tmpls = useGet<SpecTemplate[]>("/spec/templates");
  const [save, setSave] = useState(false);
  const [tname, setTname] = useState("");
  const { job, start, clear } = useJob((j) => toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Spec applied" : j.error || "Failed"));
  const { busy, run } = useAction();
  const body = () => {
    let spec: unknown;
    try { spec = JSON.parse(text); } catch (e) { throw new Error("The spec isn't valid JSON: " + (e as Error).message); }
    const variables = Object.fromEntries(vars.split("\n").map((l) => l.split("=")).filter((p) => p.length === 2 && p[0].trim()).map(([k, v]) => [k.trim(), v.trim()]));
    return { spec, variables };
  };
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2>Server spec</h2><p className="small muted">Describe accounts, websites, databases, DNS, mail and cron as JSON. Plan shows what would change; Apply makes it so. Use the same file with <code>ike apply</code>.</p></div></div>
        <div className="panel-body form-grid">
          <textarea className="input mono editor" rows={16} value={text} onChange={(e) => setText(e.target.value)} aria-label="Spec JSON" spellCheck={false} />
          <Field label="Variables" hint="key=value per line, used as ${key} in the spec">{(id) => <textarea id={id} className="input mono" rows={2} value={vars} onChange={(e) => setVars(e.target.value)} />}</Field>
          <ErrorNote text={error} />
          <div className="row">
            <Button loading={busy === "plan"} onClick={() => { setError(null); run("plan", async () => api.call<SpecChange[]>("POST", "/spec/plan", body())).then((p) => p && setPlan(p)); }}><FileCode2 size={16} />Plan</Button>
            <Button variant="primary" loading={busy === "apply"} onClick={() => run("apply", async () => api.call<Job>("POST", "/spec/apply", body())).then((j) => j && start(j))}><Rocket size={16} />Apply</Button>
            <Button variant="ghost" onClick={() => run("exp", () => api.call<unknown>("GET", "/spec/export")).then((s) => s && setText(JSON.stringify(s, null, 2)))}>Load this server</Button>
            <Button variant="ghost" onClick={() => setSave(true)}>Save as template</Button>
          </div>
        </div>
      </section>
      <div className="stack">
        <section className="panel">
          <div className="panel-head"><h2>Plan</h2></div>
          <div className="panel-body">{!plan ? <p className="muted small">Run Plan to see the changes.</p> : plan.length === 0 ? <p className="small">Nothing to change: the server already matches.</p> : (
            <ul style={{ listStyle: "none", margin: 0, padding: 0 }} className="stack">{plan.map((c, i) => (
              <li key={i} className="row" style={{ gap: 8 }}><Badge tone={c.action === "create" ? "ok" : c.action === "update" ? "warn" : undefined}>{c.action}</Badge><span className="small">{c.kind}</span><span className="mono small">{c.name}</span>{c.detail ? <span className="small muted">{c.detail}</span> : null}</li>))}</ul>
          )}</div>
        </section>
        <section className="panel">
          <div className="panel-head"><h2>Templates</h2></div>
          <div className="panel-body">{!tmpls.data?.length ? <p className="muted small">Save a spec as a template to reuse it, for example "WordPress client" with a ${"{domain}"} variable.</p> : (
            <ul style={{ listStyle: "none", margin: 0, padding: 0 }} className="stack">{tmpls.data.map((t) => (
              <li key={t.name} className="row" style={{ justifyContent: "space-between" }}><span><strong>{t.name}</strong> <span className="small muted">{t.description}</span>{t.variables?.length ? <div className="small mono muted">{t.variables.join(", ")}</div> : null}</span>
                <span className="row"><Button size="sm" onClick={() => setText(JSON.stringify(t.spec, null, 2))}>Open</Button>
                  <Button size="sm" variant="ghost" icon aria-label="Delete template" onClick={() => run("dt", () => api.call("DELETE", `/spec/templates/${enc(t.name)}`), "Template deleted").then(() => tmpls.reload())}><Trash2 size={14} /></Button></span></li>))}</ul>
          )}</div>
        </section>
      </div>
      <FormModal open={save} onClose={() => setSave(false)} title="Save as template" submit="Save" onSubmit={async () => { await api.call("PUT", `/spec/templates/${enc(tname)}`, { description: "", spec: body().spec }); tmpls.reload(); }}>
        <TextField label="Name" value={tname} onChange={(v) => setTname(v.toLowerCase().replace(/[^a-z0-9-]/g, "-"))} mono placeholder="wordpress-client" />
      </FormModal>
      <JobModal job={job} onClose={clear} />
    </div>
  );
}
