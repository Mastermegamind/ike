// Website tabs: domains and HTTPS, redirects, and PHP settings with Composer.
import { useState } from "react";
import { ArrowRight, CircleAlert, FileKey, Lock, LockOpen, Plus, RefreshCw, Trash2, Upload } from "lucide-react";
import { api } from "../api/client";
import type { PHPSettings, Redirect, Site } from "../api/types";
import { Badge, Button, Field, Modal, Segmented, useAsync, useToast } from "../components/ui";
import { Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";

/* ======================================================= Domains & HTTPS */

type WWW = "both" | "www" | "root" | "none";

export function DomainsTab({ site, onChange }: { site: Site; onChange: (s: Site) => void }) {
  const toast = useToast();
  const rt = useAsync(() => api.runtimes());
  const [aliases, setAliases] = useState<string[]>(site.aliases ?? []);
  const [www, setWww] = useState<WWW>((site.www || "both") as WWW);
  const [redirect, setRedirect] = useState(!!site.redirect_aliases);
  const [adding, setAdding] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const dirty = JSON.stringify(aliases) !== JSON.stringify(site.aliases ?? []) || www !== ((site.www || "both") as WWW) || redirect !== !!site.redirect_aliases;
  const canonical = www === "www" ? `www.${site.domain}` : site.domain;

  const add = () => {
    const a = adding.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/\/.*$/, "");
    if (!a) return;
    if (aliases.includes(a)) { setError(`${a} is already listed.`); return; }
    setAliases([...aliases, a]); setAdding(""); setError(null);
  };

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><div><h2>Domains</h2><p className="small muted">Every name here needs a DNS record pointing to this server. Wildcards like <code>*.{site.domain}</code> cover every subdomain.</p></div></div>
        <div className="panel-body form-grid">
          <div className="field">
            <label>www</label>
            <Segmented<WWW> label="www" value={www} onChange={setWww} options={[
              { value: "both", label: "Serve both" }, { value: "root", label: `Send www to ${site.domain}` },
              { value: "www", label: `Send ${site.domain} to www` }, { value: "none", label: "No www" }]} />
          </div>
          <div className="field">
            <label htmlFor="alias-add">Other domains</label>
            {aliases.length ? (
              <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 6 }}>
                {aliases.map((a) => (
                  <li key={a} className="row" style={{ justifyContent: "space-between", padding: "8px 12px", border: "1px solid var(--line)", borderRadius: "var(--r-sm)" }}>
                    <span className="mono">{a}</span>
                    <span className="row" style={{ gap: 8 }}>
                      {redirect ? <span className="small muted row" style={{ gap: 4 }}><ArrowRight size={13} aria-hidden />{canonical}</span> : null}
                      <Button size="sm" variant="ghost" icon aria-label={`Remove ${a}`} onClick={() => setAliases(aliases.filter((x) => x !== a))}><Trash2 size={15} /></Button>
                    </span>
                  </li>
                ))}
              </ul>
            ) : <p className="small muted">None yet. Add another domain, a subdomain, or a wildcard.</p>}
            <form className="row" onSubmit={(e) => { e.preventDefault(); add(); }}>
              <input id="alias-add" className="input mono" style={{ flex: 1, minWidth: 200 }} value={adding} onChange={(e) => setAdding(e.target.value)} placeholder={`shop.${site.domain} or ${site.domain.split(".")[0]}.com`} spellCheck={false} />
              <Button type="submit" disabled={!adding.trim()}><Plus size={16} />Add</Button>
            </form>
          </div>
          {aliases.length ? <Toggle checked={redirect} onChange={setRedirect} label={`Send visitors on other domains to ${canonical}`} /> : null}
          {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
          <div><Button variant="primary" disabled={!dirty} loading={busy === "domains"} onClick={async () => {
            setBusy("domains"); setError(null);
            try { onChange(await api.setDomains(site.domain, { aliases, www, redirect_aliases: redirect })); toast("ok", "Domains saved"); }
            catch (e) { setError((e as Error).message); } finally { setBusy(null); }
          }}>Save domains</Button></div>
        </div>
      </section>
      <HTTPSPanel site={site} onChange={onChange} http3={!!rt.data?.web?.http3} />
    </div>
  );
}

function HTTPSPanel({ site, onChange, http3 }: { site: Site; onChange: (s: Site) => void; http3: boolean }) {
  const toast = useToast();
  const ssl = site.ssl;
  const [email, setEmail] = useState("");
  const [withAliases, setWithAliases] = useState(true);
  const [upload, setUpload] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const { job, start, clear } = useJob(async (j) => {
    if (j.status === "done") { onChange(await api.site(site.domain)); toast("ok", "HTTPS is on"); }
  });
  const days = ssl ? Math.floor((new Date(ssl.expires_at).getTime() - Date.now()) / 86400000) : 0;
  const toggle = async (key: "force_https" | "hsts" | "http2" | "http3", v: boolean) => {
    setBusy(key);
    try { onChange(await api.updateSSL(site.domain, { [key]: v })); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
  };
  const issue = async () => {
    setBusy("issue");
    try { start(await api.issueCertificate(site.domain, { email: email || undefined, include_aliases: withAliases })); }
    catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div className="row" style={{ gap: 10 }}>
          {ssl ? <Lock size={20} style={{ color: "var(--ok)" }} aria-hidden /> : <LockOpen size={20} style={{ color: "var(--warn)" }} aria-hidden />}
          <div><h2>HTTPS</h2><p className="small muted">{ssl ? `${ssl.provider === "letsencrypt" ? "Let's Encrypt" : "Uploaded"} certificate for ${ssl.names.join(", ")}` : "Visitors see “Not secure” until the website has a certificate."}</p></div>
        </div>
        {ssl ? <Badge tone={days < 0 ? "bad" : days < 21 ? "warn" : "ok"}>{days < 0 ? "Expired" : `${days} days left`}</Badge> : <Badge tone="warn">Off</Badge>}
      </div>
      {!ssl ? (
        <div className="panel-body grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(260px, 1fr))", alignItems: "start" }}>
          <div className="form-grid">
            <h3 style={{ fontSize: "1rem" }}>Free certificate</h3>
            <p className="small muted">From Let's Encrypt, renewed automatically every 60 days. The domain must already point to this server.</p>
            <Field label="Email for expiry notices (optional)">{(id) => <input id={id} className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="you@example.com" />}</Field>
            {(site.aliases ?? []).length ? <Toggle checked={withAliases} onChange={setWithAliases} label="Include the other domains" /> : null}
            {(site.aliases ?? []).some((a) => a.startsWith("*.")) ? <p className="small muted">Wildcard domains need DNS validation, which comes with the DNS manager.</p> : null}
            <div><Button variant="primary" loading={busy === "issue"} onClick={issue}><Lock size={16} />Get free certificate</Button></div>
          </div>
          <div className="form-grid">
            <h3 style={{ fontSize: "1rem" }}>Your own certificate</h3>
            <p className="small muted">Bought from a certificate authority, or an extended-validation certificate. You renew it yourself; Ike warns you three weeks before it expires.</p>
            <div><Button onClick={() => setUpload(true)}><Upload size={16} />Upload certificate</Button></div>
          </div>
        </div>
      ) : (
        <div className="panel-body form-grid">
          <dl className="kv">
            <dt>Issued by</dt><dd>{ssl.issuer || "Unknown"}</dd>
            <dt>Expires</dt><dd>{new Date(ssl.expires_at).toLocaleDateString("en-NG", { day: "numeric", month: "long", year: "numeric" })}{ssl.provider === "letsencrypt" ? " (renews automatically about 30 days before)" : ""}</dd>
          </dl>
          <div style={{ display: "grid", gap: 12 }}>
            <Toggle checked={ssl.force_https} onChange={(v) => toggle("force_https", v)} label="Send every visitor to HTTPS" />
            <Toggle checked={ssl.hsts} onChange={(v) => toggle("hsts", v)} label="HSTS: browsers never try plain HTTP again (only after HTTPS works everywhere)" />
            <Toggle checked={ssl.http2} onChange={(v) => toggle("http2", v)} label="HTTP/2" />
            {http3 ? <Toggle checked={ssl.http3} onChange={(v) => toggle("http3", v)} label="HTTP/3 (QUIC over UDP port 443, opened in the firewall for you)" />
              : <p className="small muted">HTTP/3 needs Nginx 1.25 or newer built with QUIC. This server's Nginx doesn't support it yet.</p>}
          </div>
          {busy && busy !== "issue" ? <p className="small muted" role="status">Saving…</p> : null}
          <div className="row">
            {ssl.provider === "letsencrypt" ? <Button loading={busy === "issue"} onClick={issue}><RefreshCw size={16} />Renew or add domains</Button>
              : <Button onClick={() => setUpload(true)}><Upload size={16} />Replace certificate</Button>}
            <Button variant="ghost" style={{ color: "var(--bad)" }} onClick={() => setRemoving(true)}>Turn off HTTPS</Button>
          </div>
        </div>
      )}
      <JobModal job={job} onClose={clear} />
      <UploadCert open={upload} onClose={() => setUpload(false)} domain={site.domain} onDone={(s) => { setUpload(false); onChange(s); toast("ok", "Certificate installed"); }} />
      <Modal open={removing} onClose={() => setRemoving(false)} title="Turn off HTTPS?"
        description={ssl?.hsts ? "HSTS is on: browsers that visited recently will refuse the plain HTTP version for up to a year." : "Visitors will see “Not secure”, and links to https:// stop working."}
        footer={<><Button onClick={() => setRemoving(false)}>Keep HTTPS</Button><Button variant="danger" loading={busy === "remove"} onClick={async () => {
          setBusy("remove");
          try { onChange(await api.removeSSL(site.domain)); setRemoving(false); toast("ok", "HTTPS turned off"); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
        }}>Turn off HTTPS</Button></>}>
        <p className="small">The certificate is deleted from this server.</p>
      </Modal>
    </section>
  );
}

function UploadCert({ open, onClose, domain, onDone }: { open: boolean; onClose: () => void; domain: string; onDone: (s: Site) => void }) {
  const [cert, setCert] = useState("");
  const [key, setKey] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <Modal open={open} onClose={onClose} title={`Upload a certificate for ${domain}`}
      description="Paste PEM text. Put the website's certificate first, then the intermediate certificates."
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={busy} disabled={!cert || !key} onClick={async () => {
        setBusy(true); setError(null);
        try { onDone(await api.uploadCertificate(domain, { certificate: cert, private_key: key })); setCert(""); setKey(""); }
        catch (e) { setError((e as Error).message); } finally { setBusy(false); }
      }}><FileKey size={16} />Install certificate</Button></>}>
      <div className="form-grid">
        <Field label="Certificate and chain">{(id) => <textarea id={id} className="input mono" rows={7} value={cert} onChange={(e) => setCert(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" spellCheck={false} />}</Field>
        <Field label="Private key" hint="Stored readable by root only.">{(id) => <textarea id={id} className="input mono" rows={5} value={key} onChange={(e) => setKey(e.target.value)} placeholder="-----BEGIN PRIVATE KEY-----" spellCheck={false} />}</Field>
        {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
      </div>
    </Modal>
  );
}

/* ============================================================ Redirects */

const blank = (): Redirect => ({ kind: "redirect", match: "exact", from: "", to: "", code: 301 });

export function RedirectsTab({ site, onChange }: { site: Site; onChange: (s: Site) => void }) {
  const toast = useToast();
  const [rules, setRules] = useState<Redirect[]>(site.redirects ?? []);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const set = (i: number, patch: Partial<Redirect>) => setRules(rules.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const dirty = JSON.stringify(rules) !== JSON.stringify(site.redirects ?? []);
  const explain = (r: Redirect) => {
    if (!r.from || !r.to) return "";
    const from = r.match === "prefix" ? `${r.from} and everything under it` : r.match === "regex" ? `paths matching ${r.from}` : r.from;
    return r.kind === "rewrite" ? `${from} is served from ${r.to}; the address bar doesn't change.` : `Visitors to ${from} go to ${r.to} (${r.code === 301 || r.code === 308 ? "permanent" : "temporary"}).`;
  };

  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Redirects and rewrites</h2><p className="small muted">Checked in order, before the website runs. A redirect sends the visitor to a new address; a rewrite serves another path without changing the address.</p></div>
        <Button onClick={() => setRules([...rules, blank()])}><Plus size={16} />Add rule</Button>
      </div>
      <div className="panel-body form-grid">
        {rules.length === 0 ? <p className="muted small">No rules. Add one to move a page, shorten a link, or keep old URLs working after a redesign.</p> : null}
        {rules.map((r, i) => (
          <div key={r.id || i} style={{ display: "grid", gap: 8, padding: 12, border: "1px solid var(--line)", borderRadius: "var(--r-sm)" }}>
            <div className="row" style={{ gap: 8 }}>
              <select className="select" style={{ width: "auto" }} aria-label="Kind" value={r.kind} onChange={(e) => set(i, { kind: e.target.value as Redirect["kind"], code: e.target.value === "redirect" ? r.code || 301 : undefined })}>
                <option value="redirect">Redirect</option><option value="rewrite">Rewrite</option></select>
              <select className="select" style={{ width: "auto" }} aria-label="Match" value={r.match} onChange={(e) => set(i, { match: e.target.value as Redirect["match"] })}>
                <option value="exact">Exact path</option><option value="prefix">Path and below</option><option value="regex">Pattern</option></select>
              {r.kind === "redirect" ? (
                <select className="select" style={{ width: "auto" }} aria-label="Code" value={r.code || 301} onChange={(e) => set(i, { code: Number(e.target.value) })}>
                  <option value={301}>301 permanent</option><option value={302}>302 temporary</option><option value={307}>307 temporary, keep method</option><option value={308}>308 permanent, keep method</option></select>
              ) : null}
              <label className="row small" style={{ gap: 6 }}><input type="checkbox" checked={!!r.keep_query} onChange={(e) => set(i, { keep_query: e.target.checked })} />Keep ?query</label>
              <Button size="sm" variant="ghost" icon aria-label="Remove rule" style={{ marginLeft: "auto" }} onClick={() => setRules(rules.filter((_, j) => j !== i))}><Trash2 size={15} /></Button>
            </div>
            <div style={{ display: "grid", gridTemplateColumns: "minmax(0,1fr) auto minmax(0,1fr)", gap: 8, alignItems: "center" }}>
              <input className="input mono" aria-label="From" value={r.from} placeholder={r.match === "regex" ? "^/product/([0-9]+)$" : "/old-page"} onChange={(e) => set(i, { from: e.target.value.trim() })} spellCheck={false} />
              <ArrowRight size={16} aria-hidden />
              <input className="input mono" aria-label="To" value={r.to} placeholder={r.kind === "rewrite" ? "/index.php?id=$1" : "/new-page or https://…"} onChange={(e) => set(i, { to: e.target.value.trim() })} spellCheck={false} />
            </div>
            {explain(r) ? <p className="small muted">{explain(r)}</p> : null}
          </div>
        ))}
        {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
        <div><Button variant="primary" disabled={!dirty} loading={busy} onClick={async () => {
          setBusy(true); setError(null);
          try { const s = await api.setRedirects(site.domain, rules); onChange(s); setRules(s.redirects ?? []); toast("ok", "Rules saved"); }
          catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>Save rules</Button></div>
      </div>
    </section>
  );
}

/* ================================================================== PHP */

const zones = ["", "Africa/Lagos", "Africa/Accra", "Africa/Nairobi", "Africa/Johannesburg", "Africa/Cairo", "Europe/London", "America/New_York", "UTC"];

export function PHPTab({ site, onChange }: { site: Site; onChange: (s: Site) => void }) {
  const toast = useToast();
  const [p, setP] = useState<PHPSettings>(site.php ?? {});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const num = (k: keyof PHPSettings, label: string, unit: string, hint: string, placeholder: string) => (
    <Field label={label} hint={hint}>
      {(id) => <div className="input-group"><input id={id} className="input mono" inputMode="numeric" value={(p[k] as number | undefined) ?? ""} placeholder={placeholder}
        onChange={(e) => setP({ ...p, [k]: e.target.value ? Number(e.target.value.replace(/\D/g, "")) : undefined })} /><span className="addon">{unit}</span></div>}
    </Field>
  );
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><div><h2>PHP {site.version} settings</h2><p className="small muted">For this website only. Empty fields use Ike's defaults, and the account's limits cap what you can set.</p></div></div>
        <div className="panel-body form-grid">
          <div className="field">
            <label>Process manager</label>
            <Segmented label="Process manager" value={p.pm || "ondemand"} onChange={(v) => setP({ ...p, pm: v })} options={[
              { value: "ondemand", label: "On demand" }, { value: "dynamic", label: "Dynamic" }, { value: "static", label: "Always on" }]} />
            <p className="hint">{p.pm === "dynamic" ? "Keeps a few workers warm; good for busy websites." : p.pm === "static" ? "All workers run all the time; fastest, uses the most memory." : "Starts workers when requests come in; uses the least memory."}</p>
          </div>
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(170px, 1fr))" }}>
            {num("max_children", "Workers", "max", "Requests handled at once.", "5")}
            {num("memory_limit_mb", "Memory per request", "MB", "memory_limit", "256")}
            {num("upload_max_mb", "Largest upload", "MB", "upload_max_filesize", "64")}
            {num("post_max_mb", "Largest form post", "MB", "post_max_size; at least the upload size", "64")}
            {num("max_execution_time", "Time limit", "sec", "max_execution_time", "60")}
            {num("max_input_vars", "Form fields", "max", "max_input_vars (big WooCommerce forms need more)", "3000")}
          </div>
          {p.pm === "dynamic" ? (
            <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(170px, 1fr))" }}>
              {num("start_servers", "Start with", "workers", "", "1")}
              {num("min_spare_servers", "Keep at least", "idle", "", "1")}
              {num("max_spare_servers", "Keep at most", "idle", "", "half")}
            </div>
          ) : null}
          <Field label="Time zone">
            {(id) => <select id={id} className="select" style={{ maxWidth: 280 }} value={p.timezone || ""} onChange={(e) => setP({ ...p, timezone: e.target.value || undefined })}>
              {zones.map((z) => <option key={z} value={z}>{z || "Server default"}</option>)}</select>}
          </Field>
          <Toggle checked={!!p.display_errors} onChange={(v) => setP({ ...p, display_errors: v })} label="Show PHP errors on the page (for debugging only)" />
          <Toggle checked={!!p.allow_shell_functions} onChange={(v) => setP({ ...p, allow_shell_functions: v })} label="Allow exec, shell_exec and proc_open (some apps need them; they run as the account user)" />
          {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
          <div><Button variant="primary" loading={busy} onClick={async () => {
            setBusy(true); setError(null);
            try { onChange(await api.setPHPSettings(site.domain, p)); toast("ok", "PHP settings saved and PHP reloaded"); }
            catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}>Save PHP settings</Button></div>
        </div>
      </section>
      <ComposerPanel site={site} />
    </div>
  );
}

function ComposerPanel({ site }: { site: Site }) {
  const toast = useToast();
  const [action, setAction] = useState("install");
  const [pkg, setPkg] = useState("");
  const [dev, setDev] = useState(false);
  const [busy, setBusy] = useState(false);
  const { job, start, clear } = useJob((j) => toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Composer finished" : j.error || "Composer failed"));
  const needsPkg = action === "require" || action === "remove";
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Composer</h2><p className="small muted">Runs in the folder with composer.json, as {site.account}, with PHP {site.version}.</p></div></div>
      <form className="panel-body row" style={{ alignItems: "flex-end" }} onSubmit={async (e) => {
        e.preventDefault(); setBusy(true);
        try { start(await api.runComposer(site.domain, { action, package: needsPkg ? pkg : undefined, dev })); } catch (err) { toast("error", (err as Error).message); } finally { setBusy(false); }
      }}>
        <Field label="Command">{(id) => <select id={id} className="select" style={{ width: "auto" }} value={action} onChange={(e) => setAction(e.target.value)}>
          <option value="install">install</option><option value="update">update</option><option value="require">require</option><option value="remove">remove</option><option value="dump-autoload">dump-autoload</option></select>}</Field>
        {needsPkg ? <Field label="Package">{(id) => <input id={id} className="input mono" style={{ minWidth: 240 }} value={pkg} onChange={(e) => setPkg(e.target.value.trim())} placeholder="guzzlehttp/guzzle:^7.9" spellCheck={false} />}</Field> : null}
        <label className="row small" style={{ gap: 6, paddingBottom: 10 }}><input type="checkbox" checked={dev} onChange={(e) => setDev(e.target.checked)} />{needsPkg ? "Development package" : "Include development packages"}</label>
        <div style={{ paddingBottom: 2 }}><Button type="submit" variant="primary" loading={busy} disabled={needsPkg && !pkg}>Run composer {action}</Button></div>
      </form>
      <JobModal job={job} onClose={clear} />
    </section>
  );
}


