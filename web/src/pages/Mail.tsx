// Email hosting: domains, mailboxes, forwarders, auto-replies, deliverability,
// the mail queue and delivery log, and the admin's mail server settings.
import { useState } from "react";
import {
  AtSign, CircleCheck, CircleX, ExternalLink, Inbox, LockKeyhole, Mail, Pencil, Plus, RefreshCw, RotateCw, Search, Send, Trash2, TriangleAlert, Wand2,
} from "lucide-react";
import { api } from "../api/client";
import type { Job, Role } from "../api/types";
import type { Delivery, MailAlias, MailboxV5, MailCheck, MailDomainV5, MailRecord, MailSettings, QueuedMessage } from "../api/types5";
import { Badge, Button, CopyField, Empty, Field, Meter, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { DownloadLink, enc, ErrorNote, FormModal, NumberField, OnceModal, SelectField, TextField, UploadButton, useAction, useGet } from "../components/kit5";
import { href, navigate } from "../router";
import { useAccountParam } from "./AccountTools";

const mailBase = (a: string) => `/accounts/${enc(a)}/mail`;

export function MailPage({ route, role }: { route: string; role: Role }) {
  const path = route.split("?")[0];
  if (path.startsWith("/email/")) return <MailDomainPage domain={decodeURIComponent(path.slice(7))} route={route} role={role} />;
  return <MailHome route={route} role={role} />;
}

function MailHome({ route, role }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const info = useGet<{ installed: boolean }>("/mail");
  const domains = useGet<MailDomainV5[]>(account ? `${mailBase(account)}/domains` : null);
  const sites = useAsync(() => api.sites(), []);
  const [tab, setTab] = useState<"domains" | "queue" | "log">("domains");
  const [adding, setAdding] = useState(new URLSearchParams(route.split("?")[1] || "").get("new") === "1");
  const [domain, setDomain] = useState("");
  const mine = (sites.data ?? []).filter((s) => s.account === account && !(domains.data ?? []).some((d) => d.domain === s.domain));
  return (
    <div className="stack">
      <PageHead title="Email" text="Mailboxes, forwarders and auto-replies for your domains, with spam filtering and webmail."
        actions={<>{picker}{role === "admin" ? <a className="btn" href={href("/mail-settings")}>Mail settings</a> : null}
          <Button variant="primary" disabled={!account} onClick={() => setAdding(true)}><Plus size={17} />Add email domain</Button></>} />
      {info.data && !info.data.installed ? <div className="callout warn"><TriangleAlert size={18} />Email hosting isn't installed on this server. {role === "admin" ? "Re-run the installer with --mail to add Postfix, Dovecot, spam filtering and webmail." : "Ask your hosting provider about email."}</div> : null}
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "domains", label: "Domains" }, { value: "queue", label: "Mail queue" }, { value: "log", label: "Delivery log" }]} />
      {tab === "domains" ? (
        <section className="panel">
          {!domains.data ? <div className="panel-body"><Skeleton /><Skeleton /></div> : domains.data.length === 0 ? (
            <Empty icon={<Mail size={22} />} title="No email domains yet" text="Turn on email for a domain to create mailboxes like info@yourdomain.com."
              action={<Button variant="primary" onClick={() => setAdding(true)}><Plus size={16} />Add email domain</Button>} />
          ) : (
            <div className="table-wrap"><table className="responsive">
              <thead><tr><th>Domain</th><th className="num">Mailboxes</th><th className="num" data-hide-sm>Forwarders</th><th>Security</th><th /></tr></thead>
              <tbody>{domains.data.map((d) => (
                <tr key={d.domain} style={{ cursor: "pointer" }} onClick={() => navigate(`/email/${enc(d.domain)}?account=${enc(account)}`)}>
                  <td className="lead"><a className="primary-cell" href={href(`/email/${enc(d.domain)}?account=${enc(account)}`)}>{d.domain}</a>{!d.enabled ? <div className="small muted">Paused</div> : null}</td>
                  <td className="num">{d.mailboxes.length}</td>
                  <td className="num" data-hide-sm>{d.aliases.length}</td>
                  <td><div className="row" style={{ gap: 6 }}>{d.dkim ? <Badge tone="ok">DKIM</Badge> : <Badge tone="warn">No DKIM</Badge>}{d.tls ? <Badge tone="ok">TLS</Badge> : null}</div></td>
                  <td style={{ textAlign: "right" }}><a className="btn sm ghost" href={d.webmail_url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}><ExternalLink size={14} />Webmail</a></td>
                </tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
      ) : tab === "queue" ? <QueuePanel base={mailBase(account)} /> : <DeliveryLog base={mailBase(account)} />}
      <FormModal open={adding} onClose={() => setAdding(false)} title="Turn on email for a domain" submit="Turn on email"
        description="Ike creates a DKIM key and, when the domain's DNS zone is here, adds the MX, SPF, DKIM and DMARC records."
        onSubmit={async () => {
          const d = await api.call<MailDomainV5>("POST", `${mailBase(account)}/domains`, { domain: domain.trim().toLowerCase() });
          navigate(`/email/${enc(d.domain)}?account=${enc(account)}`);
        }}>
        <TextField label="Domain" value={domain} onChange={setDomain} placeholder="example.com" mono autoFocus
          hint={mine.length ? <>Your websites: {mine.slice(0, 4).map((s) => <button key={s.domain} type="button" className="linklike" onClick={() => setDomain(s.domain)}>{s.domain}</button>)}</> : undefined} />
      </FormModal>
    </div>
  );
}

function MailDomainPage({ domain, route, role }: { domain: string; route: string; role: Role }) {
  const toast = useToast();
  const account = new URLSearchParams(route.split("?")[1] || "").get("account") || "";
  const base = `${mailBase(account)}/domains/${enc(domain)}`;
  const d = useGet<MailDomainV5>(base);
  const [tab, setTab] = useState<"mailboxes" | "forwarders" | "deliverability" | "settings">("mailboxes");
  const [add, setAdd] = useState(false);
  const [edit, setEdit] = useState<MailboxV5 | null>(null);
  const [created, setCreated] = useState<{ address: string; password?: string; imap_host: string; smtp_host: string } | null>(null);
  const [delBox, setDelBox] = useState<string | null>(null);
  const [delDomain, setDelDomain] = useState(false);
  const { job, start, clear } = useJob((j) => { toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Certificate issued" : j.error || "Failed"); d.reload(); });
  const { busy, run } = useAction();
  if (d.error) return <div className="stack"><PageHead title={domain} back={{ to: "/email", label: "Email" }} /><ErrorNote text={d.error} /></div>;
  const md = d.data;
  return (
    <div className="stack">
      <PageHead title={domain} back={{ to: `/email?account=${enc(account)}`, label: "Email" }}
        text={md ? <>Mail apps connect to <code>{md.mail_host}</code>: IMAP 993, POP3 995, sending 465 or 587.</> : null}
        actions={<>{md ? <a className="btn" href={md.webmail_url} target="_blank" rel="noreferrer"><ExternalLink size={16} />Webmail</a> : null}
          <Button variant="primary" onClick={() => setAdd(true)}><Plus size={16} />Add mailbox</Button></>} />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "mailboxes", label: "Mailboxes" }, { value: "forwarders", label: "Forwarders" }, { value: "deliverability", label: "Deliverability" }, { value: "settings", label: "Settings" }]} />
      {!md ? <Skeleton h={200} /> : tab === "mailboxes" ? (
        <section className="panel">
          <div className="panel-head"><h2>Mailboxes</h2>
            <Button size="sm" loading={busy === "usage"} onClick={() => run("usage", () => api.call<MailDomainV5>("POST", `${base}/usage`)).then((x) => x && d.setData(x))}><RefreshCw size={15} />Refresh usage</Button></div>
          {md.mailboxes.length === 0 ? <Empty icon={<Inbox size={22} />} title="No mailboxes yet" text={`Create addresses like info@${domain} or sales@${domain}.`}
            action={<Button variant="primary" onClick={() => setAdd(true)}><Plus size={16} />Add mailbox</Button>} /> : (
            <div className="table-wrap"><table className="responsive">
              <thead><tr><th>Address</th><th>Storage</th><th data-hide-sm>Extras</th><th /></tr></thead>
              <tbody>{md.mailboxes.map((m) => {
                const used = m.usage?.used_mb ?? 0;
                return (
                  <tr key={m.local}>
                    <td className="lead"><div className="primary-cell mono">{m.local}@{domain}</div>{m.name ? <div className="small muted">{m.name}</div> : null}
                      {!m.enabled ? <Badge tone="warn">Off</Badge> : null}</td>
                    <td style={{ minWidth: 140 }}>{m.quota_mb ? <><Meter value={(used / m.quota_mb) * 100} label={`Storage of ${m.local}`} /><div className="small muted">{used.toFixed(0)} of {m.quota_mb} MB</div></>
                      : <span className="small muted">{used.toFixed(0)} MB, no limit</span>}</td>
                    <td data-hide-sm><div className="row" style={{ gap: 6 }}>
                      {m.auto_reply?.enabled ? <Badge tone="accent">Auto-reply</Badge> : null}
                      {m.forward?.length ? <Badge>Forwards</Badge> : null}{m.send_only ? <Badge>Send only</Badge> : null}</div></td>
                    <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                      <Button variant="ghost" size="sm" icon aria-label={`Edit ${m.local}`} onClick={() => setEdit(m)}><Pencil size={15} /></Button>
                      <Button variant="ghost" size="sm" icon aria-label={`Delete ${m.local}`} onClick={() => setDelBox(m.local)}><Trash2 size={15} /></Button>
                    </td>
                  </tr>
                );
              })}</tbody>
            </table></div>
          )}
        </section>
      ) : tab === "forwarders" ? <Forwarders md={md} base={base} onSaved={(x) => d.setData(x)} /> :
        tab === "deliverability" ? <Deliverability base={base} domain={domain} md={md} onJob={start} /> :
        <DomainSettings md={md} base={base} onSaved={(x) => d.setData(x)} onDelete={() => setDelDomain(true)} />}

      <MailboxModal open={add} domain={domain} onClose={() => setAdd(false)} onSave={async (b) => {
        const r = await api.call<{ address: string; password?: string; imap_host: string; smtp_host: string }>("POST", `${base}/mailboxes`, b);
        setCreated(r); d.reload();
      }} />
      {edit ? <MailboxEdit m={edit} domain={domain} base={base} onClose={() => setEdit(null)} onSaved={(x) => { d.setData(x); toast("ok", "Mailbox saved"); }} /> : null}
      <OnceModal open={!!created} onClose={() => setCreated(null)} title={`${created?.address} is ready`}>
        {created?.password ? <Field label="Password">{() => <CopyField value={created.password!} label="Password" />}</Field> : <p className="small">Use the password you chose.</p>}
        <dl className="kv"><dt>Incoming (IMAP)</dt><dd className="mono">{created?.imap_host}, port 993, SSL</dd><dt>Outgoing (SMTP)</dt><dd className="mono">{created?.smtp_host}, port 465 SSL or 587 STARTTLS</dd><dt>Username</dt><dd className="mono">{created?.address}</dd></dl>
      </OnceModal>
      <ConfirmDelete open={!!delBox} onClose={() => setDelBox(null)} title={`Delete ${delBox}@${domain}?`} name={`${delBox}@${domain}`} confirmLabel="Delete mailbox"
        description="The address stops receiving mail and its messages are deleted." onConfirm={async () => { await api.call("DELETE", `${base}/mailboxes/${enc(delBox!)}?delete_mail=true`); d.reload(); }} />
      <ConfirmDelete open={delDomain} onClose={() => setDelDomain(false)} title={`Turn off email for ${domain}?`} name={domain} confirmLabel="Turn off and delete mail"
        description="Every mailbox and forwarder on this domain is removed, with its messages." onConfirm={async () => { await api.call("DELETE", `${base}?delete_mail=true`); navigate(`/email?account=${enc(account)}`); }} />
      <JobModal job={job} onClose={clear} />
      {role === "admin" ? null : null}
    </div>
  );
}

function MailboxModal({ open, domain, onClose, onSave }: { open: boolean; domain: string; onClose: () => void; onSave: (b: Record<string, unknown>) => Promise<void> }) {
  const [f, setF] = useState({ local: "", name: "", password: "", quota_mb: 1024, send_only: false });
  return (
    <FormModal open={open} onClose={onClose} title={`Add a mailbox on ${domain}`} submit="Create mailbox" onSubmit={() => onSave(f)}>
      <Field label="Address">{(id) => <div className="row" style={{ gap: 6, flexWrap: "nowrap" }}><input id={id} className="input mono" value={f.local} onChange={(e) => setF({ ...f, local: e.target.value.toLowerCase() })} placeholder="info" autoFocus /><span className="mono muted">@{domain}</span></div>}</Field>
      <TextField label="Name (optional)" value={f.name} onChange={(v) => setF({ ...f, name: v })} placeholder="Customer support" />
      <TextField label="Password" value={f.password} onChange={(v) => setF({ ...f, password: v })} type="password" hint="At least 10 characters. Leave empty to generate a strong one." />
      <NumberField label="Storage limit (MB)" value={f.quota_mb} onChange={(v) => setF({ ...f, quota_mb: v })} hint="0 means no limit." />
      <Toggle checked={f.send_only} onChange={(v) => setF({ ...f, send_only: v })} label="Send only (for apps that send mail, no inbox)" />
    </FormModal>
  );
}

function MailboxEdit({ m, domain, base, onClose, onSaved }: { m: MailboxV5; domain: string; base: string; onClose: () => void; onSaved: (d: MailDomainV5) => void }) {
  const ar = m.auto_reply ?? { enabled: false, subject: "Out of office", body: "", days: 1 };
  const [f, setF] = useState({ name: m.name ?? "", password: "", quota_mb: m.quota_mb, enabled: m.enabled, send_only: !!m.send_only,
    forward: (m.forward ?? []).join("\n"), keep_copy: m.keep_copy, auto: ar });
  const addr = `${m.local}@${domain}`;
  return (
    <FormModal open onClose={onClose} title={`Edit ${addr}`} submit="Save mailbox" onSubmit={async () => {
      onSaved(await api.call<MailDomainV5>("PUT", `${base}/mailboxes/${enc(m.local)}`, {
        name: f.name, password: f.password, quota_mb: f.quota_mb, enabled: f.enabled, send_only: f.send_only,
        forward: f.forward.split(/[\s,]+/).filter(Boolean), keep_copy: f.keep_copy, auto_reply: f.auto,
      }));
    }}>
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
        <TextField label="Name" value={f.name} onChange={(v) => setF({ ...f, name: v })} />
        <NumberField label="Storage limit (MB)" value={f.quota_mb} onChange={(v) => setF({ ...f, quota_mb: v })} />
      </div>
      <TextField label="New password" value={f.password} onChange={(v) => setF({ ...f, password: v })} type="password" hint="Leave empty to keep the current one." />
      <Toggle checked={f.enabled} onChange={(v) => setF({ ...f, enabled: v })} label="Mailbox is on" />
      <Field label="Also forward to" hint="Addresses, one per line.">{(id) => <textarea id={id} className="input mono" rows={2} value={f.forward} onChange={(e) => setF({ ...f, forward: e.target.value })} />}</Field>
      {f.forward.trim() ? <Toggle checked={f.keep_copy} onChange={(v) => setF({ ...f, keep_copy: v })} label="Keep a copy in this mailbox" /> : null}
      <fieldset className="form-grid" style={{ border: "1px solid var(--line)", borderRadius: "var(--radius)", padding: 14 }}>
        <legend className="small" style={{ padding: "0 6px" }}>Auto-reply</legend>
        <Toggle checked={f.auto.enabled} onChange={(v) => setF({ ...f, auto: { ...f.auto, enabled: v } })} label="Reply automatically" />
        {f.auto.enabled ? <>
          <TextField label="Subject" value={f.auto.subject} onChange={(v) => setF({ ...f, auto: { ...f.auto, subject: v } })} />
          <Field label="Message">{(id) => <textarea id={id} className="input" rows={4} value={f.auto.body} onChange={(e) => setF({ ...f, auto: { ...f.auto, body: e.target.value } })} />}</Field>
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(140px, 1fr))" }}>
            <TextField label="From (optional)" type="date" value={f.auto.from ?? ""} onChange={(v) => setF({ ...f, auto: { ...f.auto, from: v } })} />
            <TextField label="Until (optional)" type="date" value={f.auto.until ?? ""} onChange={(v) => setF({ ...f, auto: { ...f.auto, until: v } })} />
            <NumberField label="Reply once every (days)" value={f.auto.days} onChange={(v) => setF({ ...f, auto: { ...f.auto, days: v } })} min={1} max={30} />
          </div>
        </> : null}
      </fieldset>
      <div className="row">
        <DownloadLink small path={`${base}/mailboxes/${enc(m.local)}/export`} label="Export mail" />
        <UploadButton method="POST" path={() => `${base}/mailboxes/${enc(m.local)}/import`} label="Import mail (.tar.gz)" accept=".gz,.tgz" />
      </div>
    </FormModal>
  );
}

function Forwarders({ md, base, onSaved }: { md: MailDomainV5; base: string; onSaved: (d: MailDomainV5) => void }) {
  const toast = useToast();
  const [rows, setRows] = useState<{ from: string; to: string }[]>(md.aliases.map((a) => ({ from: a.from, to: a.to.join(", ") })));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Forwarders</h2><p className="small muted">Addresses that pass mail on to one or more other addresses, without a mailbox of their own.</p></div></div>
      <div className="panel-body form-grid">
        {rows.length === 0 ? <p className="muted small">No forwarders yet.</p> : rows.map((r, i) => (
          <div key={i} style={{ display: "grid", gridTemplateColumns: "minmax(110px, 0.6fr) minmax(0, 1.4fr) auto", gap: 8, alignItems: "center" }}>
            <div className="row" style={{ gap: 4, flexWrap: "nowrap" }}><input className="input mono" aria-label="From" value={r.from} onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, from: e.target.value } : x)))} /><span className="muted small mono" data-hide-sm>@{md.domain}</span></div>
            <input className="input mono" aria-label="Forward to" value={r.to} placeholder="someone@gmail.com, other@example.com" onChange={(e) => setRows(rows.map((x, j) => (j === i ? { ...x, to: e.target.value } : x)))} />
            <Button variant="ghost" size="sm" icon aria-label="Remove" onClick={() => setRows(rows.filter((_, j) => j !== i))}><Trash2 size={15} /></Button>
          </div>
        ))}
        <ErrorNote text={error} />
        <div className="row">
          <Button onClick={() => setRows([...rows, { from: "", to: "" }])}><Plus size={16} />Add forwarder</Button>
          <Button variant="primary" loading={busy} onClick={async () => {
            setBusy(true); setError(null);
            try {
              const aliases: MailAlias[] = rows.filter((r) => r.from.trim()).map((r) => ({ from: r.from.trim().toLowerCase(), to: r.to.split(/[\s,]+/).filter(Boolean) }));
              onSaved(await api.call<MailDomainV5>("PUT", `${base}/aliases`, aliases)); toast("ok", "Forwarders saved");
            } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}>Save</Button>
        </div>
      </div>
    </section>
  );
}

const statusIcon = { ok: <CircleCheck size={16} style={{ color: "var(--ok)" }} />, warn: <TriangleAlert size={16} style={{ color: "var(--warn)" }} />, missing: <CircleX size={16} style={{ color: "var(--bad)" }} />, wrong: <CircleX size={16} style={{ color: "var(--bad)" }} /> };

function Deliverability({ base, domain, md, onJob }: { base: string; domain: string; md: MailDomainV5; onJob: (j: Job) => void }) {
  const toast = useToast();
  const checks = useAsync(() => api.call<MailCheck[]>("GET", `${base}/checks`), [base]);
  const recs = useAsync(() => api.call<MailRecord[]>("GET", `${base}/records`), [base]);
  const { busy, run } = useAction();
  const bad = (checks.data ?? []).filter((c) => c.status !== "ok").length;
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head">
          <div><h2>Will your mail arrive?</h2><p className="small muted">Checks DNS, reverse DNS, outgoing port 25, blocklists and the certificate for mail.{domain}.</p></div>
          <div className="row">
            <Button loading={busy === "dns"} onClick={() => run("dns", () => api.call<{ added: boolean }>("POST", `${base}/dns`)).then((r) => {
              if (r) { toast(r.added ? "ok" : "error", r.added ? "Records added to the DNS zone" : "The domain's DNS zone isn't here; add the records at your DNS host"); checks.reload(); }
            })}><Wand2 size={16} />Add records to DNS</Button>
            <Button loading={busy === "tls"} onClick={() => run("tls", () => api.call<Job>("POST", `${base}/tls`)).then((j) => j && onJob(j))}><LockKeyhole size={16} />{md.tls ? "Renew" : "Get"} certificate</Button>
            <Button loading={checks.loading} onClick={checks.reload}><RefreshCw size={16} />Check again</Button>
          </div>
        </div>
        {!checks.data ? <div className="panel-body"><Skeleton /><Skeleton /></div> : (
          <>
            <div className="panel-body" style={{ paddingBottom: 0 }}>
              <p style={{ fontWeight: 600, color: bad ? "var(--warn)" : "var(--ok)" }}>{bad ? `${bad} ${bad === 1 ? "thing needs" : "things need"} attention` : "Everything looks good"}</p>
            </div>
            <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>{checks.data.map((c, i) => (
              <li key={i} style={{ display: "grid", gridTemplateColumns: "20px 1fr", gap: 12, padding: "14px 20px", borderTop: "1px solid var(--line)" }}>
                {statusIcon[c.status]}
                <div style={{ minWidth: 0, display: "grid", gap: 4 }}>
                  <strong>{c.record} <span className="muted small mono">{c.host}</span></strong>
                  <p className="small">{c.hint}</p>
                  {c.status !== "ok" && c.expected ? <div className="small"><span className="muted">Expected: </span><code style={{ wordBreak: "break-all" }}>{c.expected}</code></div> : null}
                  {c.found && c.status !== "ok" ? <div className="small"><span className="muted">Found: </span><code style={{ wordBreak: "break-all" }}>{c.found}</code></div> : null}
                </div>
              </li>
            ))}</ul>
          </>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>DNS records for email</h2><p className="small muted">If the domain's DNS is somewhere else, add these there.</p></div>
          <Button size="sm" loading={busy === "rot"} onClick={() => run("rot", () => api.call("POST", `${base}/dkim/rotate`), "New DKIM key made; publish the new record").then(() => recs.reload())}><RotateCw size={15} />New DKIM key</Button></div>
        {!recs.data ? <div className="panel-body"><Skeleton /></div> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Name</th><th>Type</th><th>Value</th></tr></thead>
            <tbody>{recs.data.map((r, i) => (
              <tr key={i}><td className="lead mono">{r.Name}</td><td><Badge>{r.Type}</Badge></td>
                <td><CopyField value={(r.Type === "MX" ? `${r.Priority} ` : "") + r.Content} label={`${r.Type} value`} /></td></tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
    </div>
  );
}

function DomainSettings({ md, base, onSaved, onDelete }: { md: MailDomainV5; base: string; onSaved: (d: MailDomainV5) => void; onDelete: () => void }) {
  const toast = useToast();
  const [enabled, setEnabled] = useState(md.enabled);
  const [catchAll, setCatchAll] = useState(md.catch_all ?? "");
  const { busy, run } = useAction();
  return (
    <section className="panel">
      <div className="panel-body form-grid">
        <Toggle checked={enabled} onChange={setEnabled} label="Accept mail for this domain" />
        <TextField label="Catch-all address" value={catchAll} onChange={setCatchAll} placeholder={`info@${md.domain}`}
          hint="Mail to addresses that don't exist goes here. Leave empty to bounce it (recommended: catch-alls attract spam)." />
        <div className="row">
          <Button variant="primary" loading={busy === "save"} onClick={() => run("save", () => api.call<MailDomainV5>("PATCH", base, { enabled, catch_all: catchAll.trim() })).then((x) => { if (x) { onSaved(x); toast("ok", "Saved"); } })}>Save</Button>
          <Button variant="danger" onClick={onDelete}><Trash2 size={16} />Turn off email</Button>
        </div>
      </div>
    </section>
  );
}

function QueuePanel({ base }: { base: string }) {
  const q = useAsync(() => api.call<QueuedMessage[]>("GET", `${base}/queue`), [base]);
  const { busy, run } = useAction();
  const act = (action: string, id = "") => run(action + id, () => api.call("POST", `${base}/queue/${action}${id ? `?id=${enc(id)}` : ""}`), "Done").then(() => q.reload());
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Mail waiting to go out</h2><p className="small muted">Messages the receiving server hasn't accepted yet. Postfix retries them for up to 5 days.</p></div>
        <div className="row"><Button loading={busy === "flush"} onClick={() => act("flush")}><Send size={16} />Retry all now</Button><Button onClick={q.reload} loading={q.loading}><RefreshCw size={16} /></Button></div></div>
      {!q.data ? <div className="panel-body"><Skeleton /></div> : q.data.length === 0 ? <Empty icon={<CircleCheck size={22} />} title="The queue is empty" text="Everything has been delivered." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>From → to</th><th>Why it's waiting</th><th data-hide-sm>Queued</th><th /></tr></thead>
          <tbody>{q.data.map((m) => (
            <tr key={m.id}>
              <td className="lead"><div className="mono small">{m.sender || "(bounce)"}</div><div className="mono small muted">→ {m.recipients.map((r) => r.address).join(", ")}</div></td>
              <td className="small">{m.recipients.find((r) => r.delay_reason)?.delay_reason ?? <Badge>{m.queue}</Badge>}</td>
              <td className="muted small" data-hide-sm>{timeAgo(m.arrival)}</td>
              <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                <Button size="sm" variant="ghost" loading={busy === "retry" + m.id} onClick={() => act("retry", m.id)}>Retry</Button>
                <Button size="sm" variant="ghost" onClick={() => act(m.queue === "hold" ? "release" : "hold", m.id)}>{m.queue === "hold" ? "Release" : "Hold"}</Button>
                <Button size="sm" variant="ghost" icon aria-label="Delete" onClick={() => act("delete", m.id)}><Trash2 size={15} /></Button>
              </td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
    </section>
  );
}

const deliveryTone = { sent: "ok", deferred: "warn", bounced: "bad", rejected: "bad" } as const;

function DeliveryLog({ base }: { base: string }) {
  const [search, setSearch] = useState("");
  const [q, setQ] = useState("");
  const log = useAsync(() => api.call<Delivery[]>("GET", `${base}/log?search=${enc(q)}&limit=200`), [base, q]);
  return (
    <section className="panel">
      <div className="panel-head">
        <form className="search" style={{ maxWidth: 360 }} onSubmit={(e) => { e.preventDefault(); setQ(search); }}>
          <Search size={16} aria-hidden /><input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Search by address or queue ID" aria-label="Search delivery log" />
        </form>
        <Button onClick={log.reload} loading={log.loading}><RefreshCw size={16} />Refresh</Button>
      </div>
      {!log.data ? <div className="panel-body"><Skeleton /></div> : log.data.length === 0 ? <Empty icon={<AtSign size={22} />} title="Nothing in the log" text="Deliveries show up here a few seconds after mail is sent or received." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>When</th><th>To</th><th>Result</th><th data-hide-sm>Detail</th></tr></thead>
          <tbody>{log.data.map((l, i) => (
            <tr key={i}>
              <td className="muted small" style={{ whiteSpace: "nowrap" }}>{l.time}</td>
              <td className="lead mono small">{l.to}{l.from ? <div className="muted">from {l.from}</div> : null}</td>
              <td><Badge tone={deliveryTone[l.status]}>{l.status}</Badge></td>
              <td className="small" data-hide-sm style={{ wordBreak: "break-word" }}>{l.detail}{l.relay ? <div className="muted mono">{l.relay}</div> : null}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
    </section>
  );
}

/* ---------------------------------------------------------- admin settings */

export function MailSettingsPage() {
  const info = useGet<{ installed: boolean; settings?: MailSettings; relay_presets?: Record<string, { Host: string; Port: number }> }>("/mail");
  const [f, setF] = useState<MailSettings | null>(null);
  const [relayPw, setRelayPw] = useState("");
  const [tab, setTab] = useState<"server" | "queue" | "log">("server");
  const { busy, run } = useAction();
  const s = f ?? info.data?.settings ?? null;
  const set = (patch: Partial<MailSettings>) => s && setF({ ...s, ...patch });
  return (
    <div className="stack">
      <PageHead title="Mail settings" text="Outgoing relay, spam filtering and limits for every email domain on this server." />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "server", label: "Server" }, { value: "queue", label: "Queue (all domains)" }, { value: "log", label: "Delivery log (all)" }]} />
      {tab === "queue" ? <QueuePanel base="/mail" /> : tab === "log" ? <DeliveryLog base="/mail" /> : !s ? <Skeleton h={200} /> : (
        <div className="grid-2">
          <section className="panel">
            <div className="panel-head"><div><h2>Outgoing mail</h2><p className="small muted">Many VPS providers block port 25. Send through a relay such as Brevo, Mailgun, Amazon SES or ZeptoMail instead.</p></div></div>
            <div className="panel-body form-grid">
              <SelectField label="Send mail" value={s.relay.provider || "direct"} onChange={(v) => {
                const p = info.data?.relay_presets?.[v];
                set({ relay: { ...s.relay, provider: v, host: p?.Host ?? (v === "direct" ? "" : s.relay.host), port: p?.Port ?? (v === "direct" ? 0 : s.relay.port || 587) } });
              }} options={[{ value: "direct", label: "Directly from this server" }, ...Object.keys(info.data?.relay_presets ?? {}).map((k) => ({ value: k, label: `Through ${k}` })), { value: "custom", label: "Through another SMTP server" }]} />
              {s.relay.provider && s.relay.provider !== "direct" ? <>
                <div className="grid-2" style={{ gridTemplateColumns: "1fr 110px" }}>
                  <TextField label="SMTP host" value={s.relay.host} onChange={(v) => set({ relay: { ...s.relay, host: v } })} mono />
                  <NumberField label="Port" value={s.relay.port} onChange={(v) => set({ relay: { ...s.relay, port: v } })} />
                </div>
                <TextField label="Username" value={s.relay.username} onChange={(v) => set({ relay: { ...s.relay, username: v } })} />
                <TextField label={`Password${s.has_relay_password ? " (saved)" : ""}`} value={relayPw} onChange={setRelayPw} type="password" />
              </> : null}
              <NumberField label="Most messages one mailbox may send per hour" value={s.hourly_send_limit} onChange={(v) => set({ hourly_send_limit: v })} hint="Stops a hijacked password from sending spam. 0 = no limit." />
              <NumberField label="Largest message (MB)" value={s.max_message_mb} onChange={(v) => set({ max_message_mb: v })} />
            </div>
          </section>
          <section className="panel">
            <div className="panel-head"><h2>Spam and viruses</h2></div>
            <div className="panel-body form-grid">
              {!s.spam_filter ? <p className="small muted">Rspamd isn't installed, so these don't apply.</p> : null}
              <NumberField label="Move to Junk at score" value={s.spam_threshold} onChange={(v) => set({ spam_threshold: v })} hint="Lower catches more spam but more good mail too. 6 is a good start." />
              <NumberField label="Reject at score" value={s.reject_threshold} onChange={(v) => set({ reject_threshold: v })} />
              <Toggle checked={s.greylisting} onChange={(v) => set({ greylisting: v })} label="Greylisting (delays mail from unknown senders a few minutes)" />
              <Toggle checked={s.antivirus} onChange={(v) => set({ antivirus: v })} label="Scan attachments with ClamAV" />
              <SelectField label="DMARC policy for new domains" value={s.dmarc_policy} onChange={(v) => set({ dmarc_policy: v })}
                options={[{ value: "none", label: "Monitor only (none)" }, { value: "quarantine", label: "Quarantine" }, { value: "reject", label: "Reject" }]} />
              <NumberField label="Default mailbox size (MB)" value={s.default_quota_mb} onChange={(v) => set({ default_quota_mb: v })} />
              <div><Button variant="primary" loading={busy === "save"} onClick={() => run("save", () => api.call<MailSettings>("PUT", "/mail", { ...s, relay_password: relayPw }), "Mail settings saved").then((x) => { if (x) { setF(x); setRelayPw(""); } })}>Save</Button></div>
            </div>
          </section>
        </div>
      )}
    </div>
  );
}
