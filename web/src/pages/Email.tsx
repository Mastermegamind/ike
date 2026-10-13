import { useEffect, useState } from "react";
import { ArrowRight, CircleAlert, CircleCheck, ExternalLink, Inbox, Lock, Mail, Plus, RefreshCw, Trash2 } from "lucide-react";
import { api } from "../api/client";
import type { DnsCheck, Forwarder, MailDomain, Mailbox, RelayProvider, SmtpSettings } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Meter, Modal, Segmented, Skeleton, Tabs, formatMB, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead } from "../components/common";

type Tab = "mailboxes" | "forwarders" | "dns" | "sending" | "apps";

export function Email() {
  const domains = useAsync(() => api.mailDomains());
  const [domain, setDomain] = useState("");
  const [tab, setTab] = useState<Tab>("mailboxes");
  useEffect(() => { if (!domain && domains.data?.length) setDomain(domains.data[0].domain); }, [domains.data, domain]);
  const md = domains.data?.find((d) => d.domain === domain);

  return (
    <div className="stack">
      <PageHead title="Email" text="Mailboxes and forwarders for each domain, plus everything that keeps mail out of spam."
        actions={<>
          <select className="select" style={{ width: "auto", minWidth: 220 }} value={domain} onChange={(e) => setDomain(e.target.value)} aria-label="Domain">
            {(domains.data ?? []).map((d) => <option key={d.domain} value={d.domain}>{d.domain}</option>)}
          </select>
          {md ? <a className="btn" href={md.webmail_url} target="_blank" rel="noreferrer"><ExternalLink size={16} />Open webmail</a> : null}
        </>} />
      <Tabs<Tab> value={tab} onChange={setTab} tabs={[
        { value: "mailboxes", label: "Mailboxes" }, { value: "forwarders", label: "Forwarders" },
        { value: "dns", label: "Deliverability" }, { value: "sending", label: "Outgoing mail" }, { value: "apps", label: "Mail app settings" },
      ]} />
      {!domain ? <Skeleton h={200} /> : (
        <>
          {tab === "mailboxes" && <Mailboxes domain={domain} />}
          {tab === "forwarders" && <Forwarders domain={domain} />}
          {tab === "dns" && <Deliverability domain={domain} />}
          {tab === "sending" && <Sending />}
          {tab === "apps" && md && <MailApps md={md} />}
        </>
      )}
    </div>
  );
}

function Mailboxes({ domain }: { domain: string }) {
  const toast = useToast();
  const list = useAsync(() => api.mailboxes(domain), [domain]);
  const [creating, setCreating] = useState(false);
  const [local, setLocal] = useState("");
  const [pass, setPass] = useState("");
  const [quota, setQuota] = useState(2048);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [del, setDel] = useState<string | null>(null);
  const genPass = () => setPass(Array.from(crypto.getRandomValues(new Uint8Array(12)), (b) => "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"[b % 56]).join(""));

  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Mailboxes</h2><p className="small muted">Each mailbox has its own login, quota and webmail access.</p></div>
        <Button variant="primary" size="sm" onClick={() => { setCreating(true); genPass(); }}><Plus size={16} />Create mailbox</Button>
      </div>
      {list.loading && !list.data ? <div className="panel-body"><Skeleton /></div> : (list.data ?? []).length === 0 ? (
        <Empty icon={<Inbox size={22} />} title={`No mailboxes on ${domain}`} text="Create one like hello@ or info@ to start receiving mail."
          action={<Button variant="primary" onClick={() => { setCreating(true); genPass(); }}><Plus size={16} />Create mailbox</Button>} />
      ) : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Address</th><th style={{ width: "34%" }}>Storage</th><th /></tr></thead>
          <tbody>{(list.data as Mailbox[]).map((b) => {
            const pct = (b.used_mb / b.quota_mb) * 100;
            return (
              <tr key={b.address}>
                <td className="lead"><span className="primary-cell">{b.address}</span></td>
                <td><div className="small muted" style={{ marginBottom: 6 }}>{formatMB(b.used_mb)} of {formatMB(b.quota_mb)}{pct >= 90 ? ", almost full" : ""}</div><Meter value={pct} label={`Storage for ${b.address}`} /></td>
                <td style={{ textAlign: "right" }}>
                  <Button variant="ghost" size="sm" icon aria-label={`Delete ${b.address}`} onClick={() => setDel(b.address)}><Trash2 size={15} /></Button>
                </td>
              </tr>
            );
          })}</tbody>
        </table></div>
      )}
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete ${del}?`} name={del || ""}
        description="The mailbox and every message in it are deleted. This can't be undone." confirmLabel="Delete mailbox"
        onConfirm={async () => { await api.deleteMailbox(del!); list.reload(); toast("ok", `Deleted ${del}`); }} />
      <Modal open={creating} onClose={() => setCreating(false)} title="Create mailbox"
        footer={<><Button onClick={() => setCreating(false)}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={!local || !pass} onClick={async () => {
            setBusy(true); setError(null);
            try { const b = await api.createMailbox(`${local}@${domain}`, pass, quota); list.reload(); setCreating(false); setLocal(""); toast("ok", `Created ${b.address}`); }
            catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}>Create mailbox</Button></>}>
        <div className="form-grid">
          <Field label="Address" error={error}>
            {(id) => <div className="input-group"><input id={id} className="input" value={local} onChange={(e) => setLocal(e.target.value.toLowerCase())} placeholder="hello" autoFocus /><span className="addon">@{domain}</span></div>}
          </Field>
          <Field label="Password" hint="Share it securely. The owner can change it in webmail.">
            {(id) => <div className="input-group"><input id={id} className="input mono" value={pass} onChange={(e) => setPass(e.target.value)} />
              <button className="addon" type="button" onClick={genPass} style={{ cursor: "pointer" }}>New</button></div>}
          </Field>
          <Field label="Storage limit">
            {(id) => <select id={id} className="select" value={quota} onChange={(e) => setQuota(+e.target.value)}>
              {[1024, 2048, 5120, 10240, 25600].map((q) => <option key={q} value={q}>{formatMB(q)}</option>)}</select>}
          </Field>
        </div>
      </Modal>
    </section>
  );
}

function Forwarders({ domain }: { domain: string }) {
  const toast = useToast();
  const list = useAsync(() => api.forwarders(domain), [domain]);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Forwarders</h2><p className="small muted">Send copies of incoming mail to another address. Nothing is stored here.</p></div></div>
      <div className="panel-body form-grid">
        <div className="row" style={{ alignItems: "flex-end" }}>
          <Field label="From">{(id) => <div className="input-group"><input id={id} className="input" value={from} onChange={(e) => setFrom(e.target.value.toLowerCase())} placeholder="sales" /><span className="addon">@{domain}</span></div>}</Field>
          <ArrowRight size={18} className="muted" style={{ marginBottom: 11 }} aria-hidden />
          <Field label="To">{(id) => <input id={id} className="input" value={to} onChange={(e) => setTo(e.target.value)} placeholder="someone@gmail.com" />}</Field>
          <Button loading={busy} disabled={!from || !to} onClick={async () => {
            setBusy(true);
            try { await api.addForwarder({ from: `${from}@${domain}`, to }); setFrom(""); setTo(""); list.reload(); toast("ok", "Forwarder added"); }
            catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
          }}>Add forwarder</Button>
        </div>
      </div>
      {(list.data ?? []).length ? (
        <div className="table-wrap"><table><tbody>
          {(list.data as Forwarder[]).map((f) => (
            <tr key={f.from + f.to}><td>{f.from}</td><td><ArrowRight size={15} className="muted" aria-label="forwards to" /></td><td>{f.to}</td>
              <td style={{ textAlign: "right" }}><Button variant="ghost" size="sm" icon aria-label="Remove forwarder" onClick={async () => { await api.removeForwarder(f); list.reload(); }}><Trash2 size={15} /></Button></td></tr>
          ))}
        </tbody></table></div>
      ) : null}
    </section>
  );
}

function Deliverability({ domain }: { domain: string }) {
  const checks = useAsync(() => api.dnsChecks(domain), [domain]);
  const bad = (checks.data ?? []).filter((c) => c.status !== "ok").length;
  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Deliverability</h2><p className="small muted">DNS records that decide whether your mail reaches the inbox.</p></div>
        <div className="row">{checks.data ? <Badge tone={bad ? "warn" : "ok"}>{bad ? `${bad} to fix` : "All good"}</Badge> : null}
          <Button size="sm" onClick={checks.reload} loading={checks.loading}><RefreshCw size={15} />Check again</Button></div>
      </div>
      {!checks.data ? <div className="panel-body"><Skeleton h={160} /></div> : (checks.data as DnsCheck[]).map((c) => (
        <div className="dns-row" key={c.record}>
          <div className="dns-tag">{c.record}</div>
          <div style={{ display: "grid", gap: 8, minWidth: 0 }}>
            <p className="small muted">{c.hint}</p>
            {c.status !== "ok" ? <>
              <div className="small">Add a <strong>{c.type}</strong> record for <code>{c.host}</code>:</div>
              <CopyField value={c.expected} label={`${c.record} value`} />
            </> : null}
          </div>
          {c.status === "ok" ? <Badge tone="ok"><CircleCheck size={13} />OK</Badge> : <Badge tone={c.status === "missing" ? "bad" : "warn"}><CircleAlert size={13} />{c.status === "missing" ? "Missing" : "Wrong"}</Badge>}
        </div>
      ))}
    </section>
  );
}

const relays: Record<RelayProvider, { label: string; host: string; port: number }> = {
  direct: { label: "Send directly", host: "", port: 25 },
  brevo: { label: "Brevo", host: "smtp-relay.brevo.com", port: 587 },
  mailgun: { label: "Mailgun", host: "smtp.mailgun.org", port: 587 },
  ses: { label: "Amazon SES", host: "email-smtp.eu-west-1.amazonaws.com", port: 587 },
  custom: { label: "Other", host: "", port: 587 },
};

function Sending() {
  const toast = useToast();
  const s = useAsync(() => api.smtp());
  const [form, setForm] = useState<SmtpSettings | null>(null);
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => { if (s.data) setForm(s.data); }, [s.data]);
  if (!form) return <Skeleton h={200} />;
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Outgoing mail</h2><p className="small muted">How this server sends mail to the rest of the internet.</p></div></div>
      <div className="panel-body form-grid" style={{ maxWidth: 640 }}>
        {!form.port25_open ? <div className="callout warn"><CircleAlert size={18} /><p className="small"><strong>Your VPS provider blocks outgoing port 25.</strong> Mail can't leave the server directly, so use a relay service. Many have a free tier.</p></div> : null}
        <Segmented<RelayProvider> label="Send through" value={form.provider}
          onChange={(p) => setForm({ ...form, provider: p, host: relays[p].host || form.host, port: relays[p].port })}
          options={(Object.keys(relays) as RelayProvider[]).map((k) => ({ value: k, label: relays[k].label }))} />
        {form.provider !== "direct" && <>
          <div className="form-2">
            <Field label="Relay host">{(id) => <input id={id} className="input mono" value={form.host} onChange={(e) => setForm({ ...form, host: e.target.value })} />}</Field>
            <Field label="Port" hint="587 uses STARTTLS; 465 is TLS from the start.">{(id) => <select id={id} className="select" value={form.port} onChange={(e) => setForm({ ...form, port: +e.target.value })}>
              <option value={587}>587 (STARTTLS)</option><option value={465}>465 (SSL/TLS)</option><option value={2525}>2525 (STARTTLS)</option></select>}</Field>
          </div>
          <div className="form-2">
            <Field label="Username">{(id) => <input id={id} className="input" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} />}</Field>
            <Field label="Password" hint={form.has_password ? "Saved. Leave empty to keep it." : undefined}>{(id) => <input id={id} className="input" type="password" value={password} onChange={(e) => setPassword(e.target.value)} />}</Field>
          </div>
        </>}
        <div><Button variant="primary" loading={busy} onClick={async () => {
          setBusy(true);
          try { const n = await api.saveSmtp({ provider: form.provider, host: form.host, port: form.port, username: form.username, password: password || undefined }); setForm(n); setPassword(""); toast("ok", "Outgoing mail saved"); }
          catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
        }}>Save</Button></div>
      </div>
    </section>
  );
}

function MailApps({ md }: { md: MailDomain }) {
  const [mode, setMode] = useState<"ssl" | "starttls">("ssl");
  const host = `mail.${md.domain}`;
  const rows = mode === "ssl"
    ? [["Incoming (IMAP)", "993", "SSL/TLS"], ["Incoming (POP3)", "995", "SSL/TLS"], ["Outgoing (SMTP)", "465", "SSL/TLS"]]
    : [["Incoming (IMAP)", "143", "STARTTLS"], ["Incoming (POP3)", "110", "STARTTLS"], ["Outgoing (SMTP)", "587", "STARTTLS"]];
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2>Mail app settings</h2><p className="small muted">For Outlook, Apple Mail, Thunderbird and phone mail apps.</p></div></div>
        <div className="panel-body stack">
          <Segmented label="Encryption" value={mode} onChange={setMode} options={[{ value: "ssl", label: "SSL/TLS (recommended)" }, { value: "starttls", label: "STARTTLS" }]} />
          <Field label="Server">{() => <CopyField value={host} label="Mail server" />}</Field>
          <div className="table-wrap"><table>
            <thead><tr><th>Connection</th><th>Port</th><th>Security</th></tr></thead>
            <tbody>{rows.map((r) => <tr key={r[0]}><td>{r[0]}</td><td className="mono">{r[1]}</td><td><Badge tone="ok"><Lock size={12} />{r[2]}</Badge></td></tr>)}</tbody>
          </table></div>
          <p className="small muted">Username is the full email address. Logins over unencrypted connections are refused.</p>
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><h2>Webmail</h2></div>
        <div className="panel-body stack">
          <p className="muted">Read and send mail in the browser, on any device.</p>
          <CopyField value={md.webmail_url} label="Webmail address" />
          <a className="btn primary" href={md.webmail_url} target="_blank" rel="noreferrer"><Mail size={16} />Open webmail</a>
        </div>
      </section>
    </div>
  );
}
