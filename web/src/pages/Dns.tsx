// DNS zones: records, templates, DNSSEC, transfers, propagation checks,
// registration, and the admin's DNS settings (name servers, providers,
// registrar).
import { useEffect, useState } from "react";
import {
  CircleCheck, CircleX, Download, FileUp, Globe2, KeyRound, Network, Pencil, Plus, RefreshCw, Search, ShieldCheck, Trash2, Wand2,
} from "lucide-react";
import { api } from "../api/client";
import type { Role } from "../api/types";
import type {
  Availability, DnsInfo, DnsPropagation, DnsRecord, DnsSettings, DnsZone, Nameserver, RegistrarSettings, ReverseCheck,
} from "../api/types5";
import { Badge, Button, CopyField, Empty, Field, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { DownloadLink, enc, ErrorNote, FormModal, NumberField, SelectField, TextField, useAction, useGet } from "../components/kit5";
import { href, navigate } from "../router";
import { useAccountParam } from "./AccountTools";

const backendLabel: Record<string, string> = { local: "This server", cloudflare: "Cloudflare", digitalocean: "DigitalOcean" };

function zonesPath(account: string) { return `/accounts/${enc(account)}/dns/zones`; }

export function DnsPage({ route, role }: { route: string; role: Role }) {
  const path = route.split("?")[0];
  if (path.startsWith("/dns/")) return <ZoneDetail name={decodeURIComponent(path.slice(5))} route={route} role={role} />;
  return <ZoneList route={route} role={role} />;
}

function ZoneList({ route, role }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const info = useGet<DnsInfo>("/dns");
  const zones = useGet<DnsZone[]>(account ? zonesPath(account) : null);
  const [adding, setAdding] = useState(new URLSearchParams(route.split("?")[1] || "").get("new") === "1");
  const [form, setForm] = useState({ name: "", template: "website", backend: "" });
  useEffect(() => { if (info.data && !form.backend) setForm((f) => ({ ...f, backend: info.data?.settings?.default_backend || info.data?.default_backend || "local" })); }, [info.data, form.backend]);
  const noDNS = info.data && !info.data.local && !(info.data.settings?.cloudflare_token || info.data.settings?.digitalocean_token) && (info.data.default_backend ?? info.data.settings?.default_backend) === "";
  return (
    <div className="stack">
      <PageHead title="DNS zones" text="The records that point each domain at its website and mail. Zones are served by this server or kept in sync with Cloudflare or DigitalOcean."
        actions={<>{picker}{role === "admin" ? <a className="btn" href={href("/dns-settings")}>DNS settings</a> : null}
          <Button variant="primary" onClick={() => setAdding(true)} disabled={!account}><Plus size={17} />Add zone</Button></>} />
      {noDNS ? <div className="callout warn"><Network size={18} />This server has no DNS server, and no DNS provider is connected. {role === "admin" ? <a href={href("/dns-settings")}>Connect Cloudflare or DigitalOcean</a> : "Ask your hosting provider to set one up."}</div> : null}
      <section className="panel">
        {!zones.data ? <div className="panel-body stack"><Skeleton /><Skeleton /></div> : zones.data.length === 0 ? (
          <Empty icon={<Network size={22} />} title="No DNS zones yet" text="Add a zone for a domain to manage its records here. Choose a template to start with the records a website or email needs."
            action={<Button variant="primary" onClick={() => setAdding(true)}><Plus size={16} />Add zone</Button>} />
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Domain</th><th>Served by</th><th className="num">Records</th><th>DNSSEC</th><th data-hide-sm>Expires</th></tr></thead>
            <tbody>{zones.data.map((z) => (
              <tr key={z.name} onClick={() => navigate(`/dns/${enc(z.name)}?account=${enc(z.account)}`)} style={{ cursor: "pointer" }}>
                <td className="lead"><a href={href(`/dns/${enc(z.name)}?account=${enc(z.account)}`)} className="primary-cell">{z.name}</a>
                  {z.kind === "secondary" ? <div className="small muted">Secondary of {z.primaries?.join(", ")}</div> : null}
                  {z.last_error ? <div className="small" style={{ color: "var(--bad)" }}>{z.last_error}</div> : null}</td>
                <td>{backendLabel[z.backend]}</td>
                <td className="num">{z.records.length}</td>
                <td>{z.dnssec ? <Badge tone="ok">Signed</Badge> : <span className="muted">Off</span>}</td>
                <td data-hide-sm className="muted">{z.registration?.expires ? new Date(z.registration.expires).toLocaleDateString() : ""}</td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <FormModal open={adding} onClose={() => setAdding(false)} title="Add a DNS zone" submit="Add zone"
        description="After adding it, set the name servers shown on the zone at your domain registrar."
        onSubmit={async () => {
          const z = await api.call<DnsZone>("POST", zonesPath(account), { name: form.name.trim().toLowerCase(), template: form.template, backend: form.backend });
          navigate(`/dns/${enc(z.name)}?account=${enc(account)}`);
        }}>
        <TextField label="Domain" value={form.name} onChange={(v) => setForm({ ...form, name: v })} placeholder="example.com" mono autoFocus />
        <SelectField label="Start with" value={form.template} onChange={(v) => setForm({ ...form, template: v })}
          options={[{ value: "empty", label: "No records" }, ...(info.data?.templates ?? []).map((t) => ({ value: t.id, label: t.name }))]}
          hint={info.data?.templates.find((t) => t.id === form.template)?.help} />
        {role === "admin" ? <SelectField label="Served by" value={form.backend} onChange={(v) => setForm({ ...form, backend: v })}
          options={[...(info.data?.local ? [{ value: "local", label: "This server (PowerDNS)" }] : []),
            ...(info.data?.settings?.cloudflare_token ? [{ value: "cloudflare", label: "Cloudflare" }] : []),
            ...(info.data?.settings?.digitalocean_token ? [{ value: "digitalocean", label: "DigitalOcean" }] : [])]} /> : null}
      </FormModal>
    </div>
  );
}

const typeHelp: Record<string, string> = {};

function recordValue(r: DnsRecord) {
  if (r.type === "MX") return `${r.priority} ${r.content}`;
  if (r.type === "SRV") return `${r.priority} ${r.weight} ${r.port} ${r.content}`;
  return r.content;
}

function blankRecord(): DnsRecord { return { id: "", name: "", type: "A", ttl: 3600, content: "", priority: 10, weight: 5, port: 443 }; }

function ZoneDetail({ name, route, role }: { name: string; route: string; role: Role }) {
  const toast = useToast();
  const account = new URLSearchParams(route.split("?")[1] || "").get("account") || "";
  const base = `${zonesPath(account)}/${enc(name)}`;
  const zone = useGet<DnsZone>(base);
  const info = useGet<DnsInfo>("/dns");
  const [tab, setTab] = useState<"records" | "check" | "settings" | "import">("records");
  const [edit, setEdit] = useState<DnsRecord | null>(null);
  const [filter, setFilter] = useState("");
  const [del, setDel] = useState(false);
  const { busy, run } = useAction();
  (info.data?.types ?? []).forEach((t) => { typeHelp[t.type] = t.help; });
  if (zone.error) return <div className="stack"><PageHead title={name} back={{ to: "/dns", label: "DNS zones" }} /><ErrorNote text={zone.error} /></div>;
  const z = zone.data;
  const records = (z?.records ?? []).filter((r) => !filter || (r.name + " " + r.type + " " + r.content).toLowerCase().includes(filter.toLowerCase()));
  const set = (nz: DnsZone | undefined) => { if (nz) zone.setData(nz); };
  return (
    <div className="stack">
      <PageHead title={name} back={{ to: `/dns?account=${enc(account)}`, label: "DNS zones" }}
        text={z ? <>{backendLabel[z.backend]} · serial {z.serial}{z.kind === "secondary" ? " · secondary zone" : ""}</> : null}
        actions={<>
          {z?.kind === "primary" ? <Button variant="primary" onClick={() => setEdit(blankRecord())}><Plus size={16} />Add record</Button> : null}
          <Button variant="ghost" onClick={() => setDel(true)}><Trash2 size={16} />Delete zone</Button></>} />
      {z ? (
        <section className="panel">
          <div className="panel-body">
            <div className="small muted" style={{ marginBottom: 8 }}>Set these name servers at the registrar where you bought {name}:</div>
            <div className="row" style={{ gap: 8 }}>{z.nameservers.map((n) => <CopyField key={n} value={n} label="Name server" />)}</div>
            {z.dnssec && z.ds?.length ? <div style={{ marginTop: 14 }}>
              <div className="small muted" style={{ marginBottom: 6 }}>DNSSEC is on. Give your registrar this DS record:</div>
              {z.ds.map((d) => <CopyField key={d} value={d} label="DS record" />)}</div> : null}
          </div>
        </section>
      ) : null}
      <Tabs value={tab} onChange={setTab} tabs={[
        { value: "records", label: "Records" }, { value: "check", label: "Check" }, { value: "settings", label: "Settings" }, { value: "import", label: "Import and export" },
      ]} />
      {!z ? <Skeleton h={200} /> : tab === "records" ? (
        <section className="panel">
          <div className="panel-head">
            <div className="search" style={{ maxWidth: 320 }}><Search size={16} aria-hidden /><input placeholder="Filter records" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter records" /></div>
            {z.kind === "primary" ? <TemplateMenu info={info.data} onApply={(id) => run("tmpl", () => api.call<DnsZone>("POST", `${base}/template`, { template: id }), "Template applied").then(set)} busy={busy === "tmpl"} /> : null}
          </div>
          {records.length === 0 ? <Empty icon={<Network size={22} />} title={filter ? "No matching records" : "No records yet"} text="Add records one by one, or apply a template." /> : (
            <div className="table-wrap"><table className="responsive">
              <thead><tr><th>Name</th><th>Type</th><th>Value</th><th className="num" data-hide-sm>TTL</th><th /></tr></thead>
              <tbody>{records.map((r) => (
                <tr key={r.id}>
                  <td className="lead mono">{r.name}{r.managed ? <div className="small muted" style={{ fontFamily: "var(--font-ui)" }}>Managed by Ike ({r.managed})</div> : null}</td>
                  <td><Badge>{r.type}</Badge>{r.proxied ? <> <Badge tone="accent">Proxied</Badge></> : null}</td>
                  <td className="mono" style={{ wordBreak: "break-all", maxWidth: 420 }}>{recordValue(r)}</td>
                  <td className="num muted" data-hide-sm>{r.ttl}</td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    {z.kind === "primary" ? <>
                      <Button variant="ghost" size="sm" icon aria-label={`Edit ${r.name} ${r.type}`} onClick={() => setEdit({ ...r })}><Pencil size={15} /></Button>
                      <Button variant="ghost" size="sm" icon aria-label={`Delete ${r.name} ${r.type}`} loading={busy === r.id}
                        onClick={() => run(r.id, () => api.call<DnsZone>("DELETE", `${base}/records/${enc(r.id)}`), "Record deleted").then(set)}><Trash2 size={15} /></Button>
                    </> : null}
                  </td>
                </tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
      ) : tab === "check" ? <Propagation base={base} /> : tab === "settings" ? (
        <ZoneSettings z={z} base={base} role={role} onSaved={set} />
      ) : <ImportExport base={base} name={name} onImported={() => zone.reload()} />}
      {edit ? <RecordModal rec={edit} zone={name} backend={z?.backend ?? "local"} types={info.data?.types.map((t) => t.type) ?? ["A"]} onClose={() => setEdit(null)}
        onSave={async (r) => {
          const nz = r.id ? await api.call<DnsZone>("PUT", `${base}/records/${enc(r.id)}`, strip(r)) : await api.call<DnsZone>("POST", `${base}/records`, strip(r));
          zone.setData(nz);
          toast("ok", r.id ? "Record saved" : "Record added");
        }} /> : null}
      <ConfirmDelete open={del} onClose={() => setDel(false)} title={`Delete the ${name} zone?`} name={name} confirmLabel="Delete zone"
        description="Every record in it stops being served. Websites and email on this domain stop working until DNS points somewhere else."
        onConfirm={async () => { await api.call("DELETE", base); navigate(`/dns?account=${enc(account)}`); }} />
    </div>
  );
}

function strip(r: DnsRecord) {
  const out: Record<string, unknown> = { name: r.name, type: r.type, ttl: r.ttl, content: r.content };
  if (r.type === "MX" || r.type === "SRV") out.priority = r.priority;
  if (r.type === "SRV") { out.weight = r.weight; out.port = r.port; }
  if (r.proxied) out.proxied = true;
  if (r.comment) out.comment = r.comment;
  return out;
}

function TemplateMenu({ info, onApply, busy }: { info: DnsInfo | null; onApply: (id: string) => void; busy: boolean }) {
  const [id, setId] = useState("");
  return (
    <div className="row" style={{ flexWrap: "wrap", minWidth: 0, maxWidth: "100%" }}>
      <select className="select" style={{ width: "auto", maxWidth: "100%", minWidth: 0 }} value={id} onChange={(e) => setId(e.target.value)} aria-label="Template">
        <option value="">Apply a template…</option>
        {(info?.templates ?? []).map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select>
      <Button disabled={!id} loading={busy} onClick={() => onApply(id)}><Wand2 size={16} />Apply</Button>
    </div>
  );
}

const placeholders: Record<string, string> = {
  A: "203.0.113.10", AAAA: "2001:db8::10", CNAME: "target.example.com.", MX: "mail.example.com.", TXT: "v=spf1 a mx ~all", NS: "ns1.example.com.",
  SRV: "sip.example.com.", CAA: '0 issue "letsencrypt.org"', PTR: "host.example.com.", TLSA: "3 1 1 <hex>", SSHFP: "4 2 <hex>", DS: "12345 13 2 <hex>",
  NAPTR: '100 10 "S" "SIP+D2U" "" _sip._udp.example.com.',
};

function RecordModal({ rec, zone, backend, types, onClose, onSave }: { rec: DnsRecord; zone: string; backend: string; types: string[]; onClose: () => void; onSave: (r: DnsRecord) => Promise<void> }) {
  const [r, setR] = useState(rec);
  const full = r.name === "@" || r.name === "" ? zone : `${r.name}.${zone}`;
  return (
    <FormModal open onClose={onClose} title={rec.id ? "Edit record" : "Add a record"} submit={rec.id ? "Save record" : "Add record"}
      onSubmit={() => onSave({ ...r, name: r.name || "@" })}>
      <div className="grid-2" style={{ gridTemplateColumns: "1fr 140px" }}>
        <TextField label="Name" value={r.name} onChange={(v) => setR({ ...r, name: v })} placeholder="@ or www" mono hint={<>Answers for <code>{full}</code></>} />
        <SelectField label="Type" value={r.type} onChange={(v) => setR({ ...r, type: v })} options={types.map((t) => ({ value: t, label: t }))} />
      </div>
      {typeHelp[r.type] ? <p className="small muted" style={{ marginTop: -8 }}>{typeHelp[r.type]}</p> : null}
      {r.type === "MX" || r.type === "SRV" ? (
        <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(110px, 1fr))" }}>
          <NumberField label="Priority" value={r.priority ?? 10} onChange={(v) => setR({ ...r, priority: v })} max={65535} />
          {r.type === "SRV" ? <><NumberField label="Weight" value={r.weight ?? 5} onChange={(v) => setR({ ...r, weight: v })} max={65535} />
            <NumberField label="Port" value={r.port ?? 443} onChange={(v) => setR({ ...r, port: v })} max={65535} /></> : null}
        </div>
      ) : null}
      <Field label={r.type === "MX" || r.type === "SRV" || r.type === "CNAME" ? "Target host" : "Value"}
        hint={r.type === "TXT" ? "Long values are split into 255-character pieces automatically." : r.type === "CNAME" || r.type === "MX" ? "A name with a dot is a full name; a single word is inside this zone." : undefined}>
        {(id) => r.type === "TXT" ? <textarea id={id} className="input mono" rows={3} value={r.content} onChange={(e) => setR({ ...r, content: e.target.value })} placeholder={placeholders.TXT} spellCheck={false} />
          : <input id={id} className="input mono" value={r.content} onChange={(e) => setR({ ...r, content: e.target.value })} placeholder={placeholders[r.type]} spellCheck={false} />}
      </Field>
      <SelectField label="TTL" value={String(r.ttl)} onChange={(v) => setR({ ...r, ttl: Number(v) })}
        options={[["60", "1 minute"], ["300", "5 minutes"], ["1800", "30 minutes"], ["3600", "1 hour"], ["14400", "4 hours"], ["86400", "1 day"]].map(([value, label]) => ({ value, label }))}
        hint="How long resolvers keep the answer. Lower it before a move so changes show up sooner." />
      {backend === "cloudflare" && ["A", "AAAA", "CNAME"].includes(r.type) ? <Toggle checked={!!r.proxied} onChange={(v) => setR({ ...r, proxied: v })} label="Proxy through Cloudflare (orange cloud)" /> : null}
    </FormModal>
  );
}

function Propagation({ base }: { base: string }) {
  const p = useAsync(() => api.call<DnsPropagation>("GET", `${base}/check`), [base]);
  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Can the world see these records?</h2><p className="small muted">Asks Cloudflare, Google, Quad9 and OpenDNS, and checks which name servers the registrar lists.</p></div>
        <Button loading={p.loading} onClick={p.reload}><RefreshCw size={16} />Check again</Button>
      </div>
      {!p.data ? <div className="panel-body"><Skeleton /><Skeleton /></div> : (
        <>
          <div className="panel-body">
            <div className={`callout ${p.data.delegation_ok ? "info" : "warn"}`}>{p.data.delegation_ok ? <CircleCheck size={18} /> : <Globe2 size={18} />}{p.data.explanation}</div>
            {!p.data.delegation_ok && p.data.delegated?.length ? <p className="small muted" style={{ marginTop: 8 }}>The registrar lists: <span className="mono">{p.data.delegated.join(", ")}</span></p> : null}
          </div>
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Record</th><th>Expected</th><th>Resolvers</th></tr></thead>
            <tbody>{p.data.records.map((r) => (
              <tr key={r.name + r.type}>
                <td className="lead mono">{r.name} <Badge>{r.type}</Badge></td>
                <td className="mono small" style={{ wordBreak: "break-all" }}>{r.expected.join(", ")}</td>
                <td>{r.note ? <span className="small muted">{r.note}</span> : <div className="row" style={{ gap: 6 }}>{(r.answers ?? []).map((a) => (
                  <span key={a.resolver} className="small" title={a.error || (a.values ?? []).join(", ")} style={{ display: "inline-flex", gap: 4, alignItems: "center", color: a.match ? "var(--ok)" : "var(--bad)" }}>
                    {a.match ? <CircleCheck size={14} /> : <CircleX size={14} />}{a.resolver}</span>))}</div>}</td>
              </tr>
            ))}</tbody>
          </table></div>
        </>
      )}
    </section>
  );
}

function ZoneSettings({ z, base, role, onSaved }: { z: DnsZone; base: string; role: Role; onSaved: (z: DnsZone) => void }) {
  const toast = useToast();
  const [s, setS] = useState({ ttl: z.ttl, soa_email: z.soa_email, dnssec: z.dnssec, nameservers: z.nameservers.join("\n"),
    also_notify: (z.also_notify ?? []).join("\n"), allow_transfer: (z.allow_transfer ?? []).join("\n") });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [regBusy, setRegBusy] = useState(false);
  const lines = (v: string) => v.split(/[\s,]+/).map((x) => x.trim()).filter(Boolean);
  const admin = role === "admin";
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><h2>Zone settings</h2></div>
        <div className="panel-body form-grid">
          <NumberField label="Default TTL (seconds)" value={s.ttl} onChange={(v) => setS({ ...s, ttl: v })} min={60} max={86400} />
          <TextField label="Contact email (SOA)" value={s.soa_email} onChange={(v) => setS({ ...s, soa_email: v })} />
          {z.backend === "local" ? <Toggle checked={s.dnssec} onChange={(v) => setS({ ...s, dnssec: v })} label="Sign the zone with DNSSEC" /> :
            <p className="small muted">{z.backend === "cloudflare" ? "Cloudflare signs zones itself: turn DNSSEC on in the Cloudflare dashboard." : "DigitalOcean DNS doesn't support DNSSEC."}</p>}
          {admin && z.backend === "local" ? <>
            <Field label="Name servers" hint="One per line. Leave as is to use the server's own.">{(id) => <textarea id={id} className="input mono" rows={3} value={s.nameservers} onChange={(e) => setS({ ...s, nameservers: e.target.value })} />}</Field>
            <Field label="Notify these secondaries" hint="IP addresses, one per line.">{(id) => <textarea id={id} className="input mono" rows={2} value={s.also_notify} onChange={(e) => setS({ ...s, also_notify: e.target.value })} />}</Field>
            <Field label="Allow zone transfers to" hint="IP addresses or ranges.">{(id) => <textarea id={id} className="input mono" rows={2} value={s.allow_transfer} onChange={(e) => setS({ ...s, allow_transfer: e.target.value })} />}</Field>
          </> : null}
          <ErrorNote text={error} />
          <div><Button variant="primary" loading={busy} onClick={async () => {
            setBusy(true); setError(null);
            try {
              const body: Record<string, unknown> = { ttl: s.ttl, soa_email: s.soa_email, dnssec: s.dnssec };
              if (admin && z.backend === "local") Object.assign(body, { nameservers: lines(s.nameservers), also_notify: lines(s.also_notify), allow_transfer: lines(s.allow_transfer) });
              onSaved(await api.call<DnsZone>("PATCH", base, body));
              toast("ok", "Zone settings saved");
            } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}>Save</Button></div>
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Registration</h2><p className="small muted">From the domain registry (RDAP). Ike reminds you before it expires.</p></div>
          <Button size="sm" loading={regBusy} onClick={async () => {
            setRegBusy(true);
            try { onSaved(await api.call<DnsZone>("POST", `${base}/registration`)); } catch (e) { toast("error", (e as Error).message); } finally { setRegBusy(false); }
          }}><RefreshCw size={15} />Refresh</Button></div>
        <div className="panel-body">
          {!z.registration ? <p className="muted small">Not checked yet.</p> : z.registration.error ? <ErrorNote text={z.registration.error} /> : (
            <dl className="kv">
              <dt>Registrar</dt><dd>{z.registration.registrar || "Unknown"}</dd>
              <dt>Expires</dt><dd>{z.registration.expires ? <>{new Date(z.registration.expires).toLocaleDateString(undefined, { dateStyle: "long" })} <span className="muted">({Math.round((new Date(z.registration.expires).getTime() - Date.now()) / 86400000)} days)</span></> : "Unknown"}</dd>
              <dt>Name servers</dt><dd className="mono small">{(z.registration.nameservers ?? []).join(", ")}</dd>
              <dt>Checked</dt><dd className="muted">{timeAgo(z.registration.checked_at)}</dd>
            </dl>
          )}
          {z.backend !== "local" ? <div style={{ marginTop: 14 }}><Button size="sm" onClick={async () => {
            try { onSaved(await api.call<DnsZone>("POST", `${base}/pull`)); toast("ok", "Records imported from the provider"); } catch (e) { toast("error", (e as Error).message); }
          }}><Download size={15} />Import records from {backendLabel[z.backend]}</Button></div> : null}
        </div>
      </section>
    </div>
  );
}

function ImportExport({ base, name, onImported }: { base: string; name: string; onImported: () => void }) {
  const [text, setText] = useState("");
  const [replace, setReplace] = useState(false);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<string[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2>Import a zone file</h2><p className="small muted">Paste a BIND zone file, from cPanel or another DNS host.</p></div></div>
        <div className="panel-body form-grid">
          <textarea className="input mono" rows={10} value={text} onChange={(e) => setText(e.target.value)} placeholder={`$TTL 3600\n@ IN A 203.0.113.10\nwww IN CNAME @`} aria-label="Zone file" spellCheck={false} />
          <Toggle checked={replace} onChange={setReplace} label="Replace the current records" />
          <ErrorNote text={error} />
          {result ? <div className="callout info"><CircleCheck size={18} /><div>Imported.{result.length ? <> Skipped {result.length}: <ul className="small">{result.slice(0, 8).map((x) => <li key={x} className="mono">{x}</li>)}</ul></> : null}</div></div> : null}
          <div><Button variant="primary" loading={busy} disabled={!text.trim()} onClick={async () => {
            setBusy(true); setError(null); setResult(null);
            try { const r = await api.call<{ skipped: string[] | null }>("POST", `${base}/import`, { zone_file: text, replace }); setResult(r.skipped ?? []); onImported(); }
            catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}><FileUp size={16} />Import</Button></div>
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Export</h2><p className="small muted">Download {name} as a standard zone file, to keep or move elsewhere.</p></div></div>
        <div className="panel-body"><DownloadLink path={`${base}/export`} label="Download zone file" /></div>
      </section>
    </div>
  );
}

/* ------------------------------------------------------------- settings */

export function DnsSettingsPage() {
  const info = useGet<DnsInfo>("/dns");
  const [tab, setTab] = useState<"servers" | "providers" | "registrar" | "reverse">("servers");
  return (
    <div className="stack">
      <PageHead title="DNS settings" text="Your name servers, DNS providers, the registrar account for buying domains, and this server's reverse DNS." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "servers", label: "Name servers" }, { value: "providers", label: "Providers" }, { value: "registrar", label: "Registrar" }, { value: "reverse", label: "Reverse DNS" }]} />
      {!info.data?.settings ? <Skeleton h={200} /> : tab === "servers" || tab === "providers" ? <DnsServerSettings s={info.data.settings} tab={tab} onSaved={(s) => info.setData({ ...info.data!, settings: s })} />
        : tab === "registrar" ? <RegistrarPanel /> : <ReversePanel />}
    </div>
  );
}

function DnsServerSettings({ s, tab, onSaved }: { s: DnsSettings; tab: "servers" | "providers"; onSaved: (s: DnsSettings) => void }) {
  const toast = useToast();
  const [ns, setNs] = useState<Nameserver[]>(s.nameservers.length ? s.nameservers : [{ host: "", ipv4: s.server_ipv4 }, { host: "", ipv4: "" }]);
  const [f, setF] = useState({ soa_email: s.soa_email, default_ttl: s.default_ttl, secondaries: s.secondaries.join("\n"), default_backend: s.default_backend,
    cloudflare_token: "", cloudflare_account: s.cloudflare_account ?? "", digitalocean_token: "", expiry_reminders: s.expiry_reminders.join(", ") });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const save = async () => {
    setBusy(true); setError(null);
    try {
      const out = await api.call<DnsSettings>("PUT", "/dns", {
        nameservers: ns.filter((n) => n.host.trim()), soa_email: f.soa_email, default_ttl: f.default_ttl, default_backend: f.default_backend,
        secondaries: f.secondaries.split(/[\s,]+/).filter(Boolean), cloudflare_token: f.cloudflare_token, cloudflare_account: f.cloudflare_account,
        digitalocean_token: f.digitalocean_token, expiry_reminders: f.expiry_reminders.split(/[\s,]+/).filter(Boolean).map(Number),
      });
      onSaved(out); setF({ ...f, cloudflare_token: "", digitalocean_token: "" });
      toast("ok", "DNS settings saved");
    } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };
  return (
    <section className="panel">
      <div className="panel-body form-grid">
        {tab === "servers" ? <>
          {!s.local_server ? <div className="callout warn"><Network size={18} />PowerDNS isn't installed. Re-run the installer with --dns to serve zones from this server, or connect a provider.</div> : null}
          <div className="small muted">Your own name servers (for example ns1.yourcompany.com and ns2.yourcompany.com). If they're inside a domain hosted here, Ike adds their A records; register them as "glue" at your registrar too.</div>
          {ns.map((n, i) => (
            <div key={i} className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))" }}>
              <TextField label={`Name server ${i + 1}`} value={n.host} onChange={(v) => setNs(ns.map((x, j) => (j === i ? { ...x, host: v } : x)))} placeholder={`ns${i + 1}.example.com`} mono />
              <TextField label="IPv4" value={n.ipv4 ?? ""} onChange={(v) => setNs(ns.map((x, j) => (j === i ? { ...x, ipv4: v } : x)))} mono />
              <TextField label="IPv6" value={n.ipv6 ?? ""} onChange={(v) => setNs(ns.map((x, j) => (j === i ? { ...x, ipv6: v } : x)))} mono />
            </div>
          ))}
          <div><Button size="sm" onClick={() => setNs([...ns, { host: "" }])} disabled={ns.length >= 6}><Plus size={15} />Add name server</Button></div>
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
            <TextField label="Contact email (SOA)" value={f.soa_email} onChange={(v) => setF({ ...f, soa_email: v })} />
            <NumberField label="Default TTL (seconds)" value={f.default_ttl} onChange={(v) => setF({ ...f, default_ttl: v })} min={60} />
            <TextField label="Remind before expiry (days)" value={f.expiry_reminders} onChange={(v) => setF({ ...f, expiry_reminders: v })} hint="Comma separated, e.g. 30, 14, 7, 1" />
          </div>
          <Field label="Secondary DNS servers" hint="IP addresses that get every zone by transfer (NOTIFY + AXFR).">{(id) => <textarea id={id} className="input mono" rows={2} value={f.secondaries} onChange={(e) => setF({ ...f, secondaries: e.target.value })} />}</Field>
        </> : <>
          <SelectField label="New zones are served by" value={f.default_backend || "local"} onChange={(v) => setF({ ...f, default_backend: v })}
            options={[{ value: "local", label: "This server (PowerDNS)" }, { value: "cloudflare", label: "Cloudflare" }, { value: "digitalocean", label: "DigitalOcean" }]} />
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(240px, 1fr))" }}>
            <TextField label={`Cloudflare API token${s.cloudflare_token ? " (saved)" : ""}`} value={f.cloudflare_token} onChange={(v) => setF({ ...f, cloudflare_token: v })} type="password"
              hint={<>Needs Zone:Edit and DNS:Edit. {s.cloudflare_token ? "Type - to remove it." : ""}</>} />
            <TextField label="Cloudflare account ID" value={f.cloudflare_account} onChange={(v) => setF({ ...f, cloudflare_account: v })} mono hint="Only needed to create new zones." />
            <TextField label={`DigitalOcean API token${s.digitalocean_token ? " (saved)" : ""}`} value={f.digitalocean_token} onChange={(v) => setF({ ...f, digitalocean_token: v })} type="password" hint="Read and write scope." />
          </div>
          <p className="small muted"><KeyRound size={14} style={{ verticalAlign: -2 }} /> Tokens are encrypted with this server's master key and never shown again.</p>
        </>}
        <ErrorNote text={error} />
        <div><Button variant="primary" loading={busy} onClick={save}>Save</Button></div>
      </div>
    </section>
  );
}

function RegistrarPanel() {
  const toast = useToast();
  const reg = useGet<RegistrarSettings>("/dns/registrar");
  const [f, setF] = useState<RegistrarSettings | null>(null);
  const [key, setKey] = useState("");
  const [q, setQ] = useState("");
  const [avail, setAvail] = useState<Availability[] | null>(null);
  const [buy, setBuy] = useState<string | null>(null);
  const [years, setYears] = useState(1);
  const [acct, setAcct] = useState("");
  const accounts = useAsync(() => api.accounts());
  const { busy, run } = useAction();
  useEffect(() => { if (reg.data && !f) setF({ ...reg.data, kind: "namecheap", contact: reg.data.contact ?? { first_name: "", last_name: "", address1: "", city: "", state: "", postal_code: "", country: "NG", phone: "+234.", email: "" } }); }, [reg.data, f]);
  if (!f) return <Skeleton h={200} />;
  const c = f.contact!;
  const setC = (k: string, v: string) => setF({ ...f, contact: { ...c, [k]: v } });
  return (
    <div className="stack">
      {reg.data?.kind ? (
        <section className="panel">
          <div className="panel-head"><div><h2>Find and register a domain</h2><p className="small muted">Through your Namecheap account. Registering charges the card or balance on that account.</p></div></div>
          <div className="panel-body form-grid">
            <div className="row">
              <input className="input mono" style={{ maxWidth: 360 }} value={q} onChange={(e) => setQ(e.target.value)} placeholder="mybusiness.com mybusiness.ng" aria-label="Domains to check" />
              <Button loading={busy === "check"} onClick={() => run("check", () => api.call<Availability[]>("POST", "/dns/registrar/check", { domains: q.split(/[\s,]+/).filter(Boolean) })).then((r) => r && setAvail(r))}><Search size={16} />Check</Button>
            </div>
            {avail ? <ul className="stack" style={{ gap: 6, listStyle: "none", padding: 0, margin: 0 }}>{avail.map((a) => (
              <li key={a.domain} className="row" style={{ justifyContent: "space-between" }}>
                <span className="mono">{a.domain}</span>
                {a.available ? <span className="row"><Badge tone="ok">Available{a.premium ? ` (premium ${a.price})` : ""}</Badge><Button size="sm" variant="primary" onClick={() => setBuy(a.domain)}>Register</Button></span> : <Badge>Taken</Badge>}
              </li>))}</ul> : null}
          </div>
        </section>
      ) : null}
      <section className="panel">
        <div className="panel-head"><div><h2>Namecheap account</h2><p className="small muted">In Namecheap, go to Profile → Tools → API Access, turn the API on and allow-list this server's IP address: <code>{f.client_ip}</code>.</p></div></div>
        <div className="panel-body form-grid">
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
            <TextField label="Namecheap username" value={f.username ?? ""} onChange={(v) => setF({ ...f, username: v, api_user: v })} />
            <TextField label={`API key${reg.data?.api_key ? " (saved)" : ""}`} value={key} onChange={setKey} type="password" />
            <TextField label="Allow-listed IP" value={f.client_ip} onChange={(v) => setF({ ...f, client_ip: v })} mono />
          </div>
          <Toggle checked={!!f.sandbox} onChange={(v) => setF({ ...f, sandbox: v })} label="Use the Namecheap sandbox (for testing)" />
          <h3 style={{ fontSize: "1rem" }}>Registrant contact</h3>
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
            {([["first_name", "First name"], ["last_name", "Last name"], ["organization", "Organization (optional)"], ["email", "Email"], ["phone", "Phone (+234.8031234567)"],
              ["address1", "Address"], ["city", "City"], ["state", "State"], ["postal_code", "Postal code"], ["country", "Country (2 letters)"]] as const).map(([k, l]) => (
              <TextField key={k} label={l} value={(c as unknown as Record<string, string>)[k] ?? ""} onChange={(v) => setC(k, v)} />))}
          </div>
          <div><Button variant="primary" loading={busy === "save"} onClick={() => run("save", () => api.call<RegistrarSettings>("PUT", "/dns/registrar", { ...f, kind: "namecheap", api_key: key, api_user: f.username }), "Registrar saved").then((r) => { if (r) { reg.setData(r); setKey(""); } })}>Save</Button></div>
        </div>
      </section>
      <FormModal open={!!buy} onClose={() => setBuy(null)} title={`Register ${buy}`} submit="Register and pay"
        description="Namecheap charges your account. Ike then creates the DNS zone and points the domain at it."
        onSubmit={async () => { await api.call("POST", "/dns/registrar/register", { domain: buy, years, account: acct, use_own_nameservers: true }); toast("ok", `${buy} registered`); }}>
        <NumberField label="Years" value={years} onChange={setYears} min={1} max={10} />
        <SelectField label="Create its DNS zone in" value={acct} onChange={setAcct} options={[{ value: "", label: "Don't create a zone" }, ...(accounts.data ?? []).map((a) => ({ value: a.name, label: a.name }))]} />
      </FormModal>
    </div>
  );
}

function ReversePanel() {
  const r = useAsync(() => api.call<ReverseCheck[]>("GET", "/dns/reverse"));
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Reverse DNS</h2><p className="small muted">Mail servers check that this server's address points back to its name. Reverse DNS is set at your VPS provider, not here.</p></div>
        <Button loading={r.loading} onClick={r.reload}><RefreshCw size={16} />Check again</Button></div>
      <div className="panel-body stack">
        {!r.data ? <Skeleton /> : r.data.length === 0 ? <p className="muted">This server's public address isn't known.</p> : r.data.map((c) => (
          <div key={c.address} className={`callout ${c.ok ? "info" : "warn"}`}>
            {c.ok ? <ShieldCheck size={18} /> : <Globe2 size={18} />}
            <div><strong className="mono">{c.address}</strong> → <span className="mono">{(c.ptr ?? []).join(", ") || "no PTR record"}</span>
              <p className="small" style={{ marginTop: 4 }}>{c.advice}</p></div>
          </div>
        ))}
      </div>
    </section>
  );
}

