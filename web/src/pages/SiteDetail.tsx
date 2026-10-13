import { useEffect, useState } from "react";
import { CircleAlert, ExternalLink, FileWarning, Play, RefreshCw, RotateCw, ShieldCheck, Square, Trash2, Wrench } from "lucide-react";
import { api, demo } from "../api/client";
import { phpEol, runtimeName } from "../api/demo";
import type { LogKind, Runtimes, Site, SiteStatus, WebServer, WordPressResult } from "../api/types";
import { Badge, Button, CopyField, Field, Modal, Segmented, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, RuntimeBadge, StatusBadge, webServerLabel } from "../components/common";
import { navigate } from "../router";

type Tab = "overview" | "runtime" | "status" | "logs" | "tools";

export function SiteDetail({ domain }: { domain: string }) {
  const toast = useToast();
  const s = useAsync(() => api.site(domain), [domain]);
  const rt = useAsync(() => api.runtimes());
  const [tab, setTab] = useState<Tab>("overview");
  const [deleting, setDeleting] = useState(false);
  const [removeFiles, setRemoveFiles] = useState(false);

  if (s.error) return <div className="callout bad"><CircleAlert size={18} />{s.error}</div>;
  if (!s.data) return <div className="stack"><Skeleton h={34} w={260} /><Skeleton h={200} /></div>;
  const site = s.data;
  const isApp = site.runtime === "node" || site.runtime === "python";
  const update = (next: Site) => s.setData(next);

  return (
    <div className="stack">
      <PageHead
        title={site.domain}
        back={{ to: "/sites", label: "Websites" }}
        text={<span className="row" style={{ gap: 8 }}><RuntimeBadge runtime={site.runtime} version={site.version} /><StatusBadge status={site.status} />
          <span className="small">in account <strong>{site.account}</strong></span></span>}
        actions={<a className="btn" href={`http://${site.domain}`} target="_blank" rel="noreferrer"><ExternalLink size={16} />Visit</a>}
      />
      <Tabs<Tab> value={tab} onChange={setTab} tabs={[
        { value: "overview", label: "Overview" }, { value: "runtime", label: isApp ? "App" : "Runtime" },
        { value: "status", label: "Status" }, { value: "logs", label: "Logs" }, { value: "tools", label: "Tools" },
      ]} />

      {tab === "overview" && (
        <div className="grid-2">
          <section className="panel">
            <div className="panel-head"><h2>Details</h2></div>
            <div className="panel-body">
              <dl className="kv">
                <dt>Files</dt><dd><code>{site.doc_root}</code></dd>
                <dt>Error pages</dt><dd><code>{site.doc_root.replace(/\/(public_html|app)$/, "/errors")}</code></dd>
                <dt>Runtime</dt><dd>{runtimeName(site.runtime)} {site.version}</dd>
                <dt>Web server</dt><dd>{webServerLabel[site.web_server]}{site.web_server === "nginx+apache" ? " (.htaccess on)" : ""}</dd>
                {isApp ? <><dt>Local port</dt><dd><code>127.0.0.1:{site.port}</code></dd></> : null}
                <dt>Created</dt><dd>{timeAgo(site.created_at)}</dd>
              </dl>
            </div>
          </section>
          <section className="panel">
            <div className="panel-head"><h2>Connect</h2></div>
            <div className="panel-body stack">
              <Field label="SFTP host">{() => <CopyField value={`sftp://${site.account}@vps-lagos-1.ike.host`} label="SFTP address" />}</Field>
              <Field label="DNS A record">{() => <CopyField value={`${site.domain}  A  203.0.113.24`} label="DNS record" />}</Field>
              <p className="small muted">SSH and SFTP access are set on the account, so every website in {site.account} shares them.</p>
            </div>
          </section>
        </div>
      )}

      {tab === "runtime" && rt.data && <RuntimeTab site={site} rt={rt.data} onChange={update} />}
      {tab === "status" && <StatusTab site={site} onChange={update} />}
      {tab === "logs" && <LogsTab site={site} />}
      {tab === "tools" && (
        <div className="stack">
          <ToolsTab site={site} />
          <section className="panel">
            <div className="panel-head"><div><h2>Delete website</h2><p className="small muted">The account and its other websites stay.</p></div>
              <Button variant="ghost" style={{ color: "var(--bad)" }} onClick={() => setDeleting(true)}><Trash2 size={16} />Delete</Button></div>
          </section>
        </div>
      )}

      <ConfirmDelete open={deleting} onClose={() => setDeleting(false)} title={`Delete ${site.domain}?`} name={site.domain}
        description="Visitors will get an error until you host the domain again."
        extra={<label className="row" style={{ gap: 8 }}><input type="checkbox" checked={removeFiles} onChange={(e) => setRemoveFiles(e.target.checked)} />
          Also delete the website's files (can't be undone)</label>}
        confirmLabel="Delete website"
        onConfirm={async () => { await api.deleteSite(site.domain, removeFiles); toast("ok", `Deleted ${site.domain}`); navigate("/sites"); }} />
    </div>
  );
}

function RuntimeTab({ site, rt, onChange }: { site: Site; rt: Runtimes; onChange: (s: Site) => void }) {
  const toast = useToast();
  const isApp = site.runtime === "node" || site.runtime === "python";
  const [version, setVersion] = useState(site.version || "");
  const [web, setWeb] = useState<WebServer>(site.web_server);
  const [start, setStart] = useState(site.start_command || "");
  const [busy, setBusy] = useState<string | null>(null);
  const [state, setState] = useState(demo?.appStateOf(site.domain) ?? "active");
  const versions = site.runtime === "static" ? [] : rt[site.runtime];
  const dirty = version !== (site.version || "") || web !== site.web_server || start !== (site.start_command || "");

  const act = async (a: "start" | "stop" | "restart") => {
    setBusy(a);
    try { await api.appAction(site.domain, a); setState(a === "stop" ? "inactive" : "active"); toast("ok", a === "stop" ? "App stopped" : a === "start" ? "App started" : "App restarted"); }
    catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
  };

  return (
    <div className="stack">
      {isApp && (
        <section className="panel">
          <div className="panel-head">
            <div><h2>App process</h2><p className="small muted">Runs as {site.account}, restarts automatically if it crashes.</p></div>
            <Badge tone={state === "active" ? "ok" : "bad"}>{state === "active" ? "Running" : "Stopped"}</Badge>
          </div>
          <div className="panel-body row">
            <Button loading={busy === "restart"} onClick={() => act("restart")}><RotateCw size={16} />Restart</Button>
            {state === "active"
              ? <Button loading={busy === "stop"} onClick={() => act("stop")}><Square size={15} />Stop</Button>
              : <Button variant="primary" loading={busy === "start"} onClick={() => act("start")}><Play size={15} />Start</Button>}
          </div>
        </section>
      )}
      <section className="panel">
        <div className="panel-head"><h2>{isApp ? "Version and start command" : "Version and web server"}</h2></div>
        <div className="panel-body form-grid" style={{ maxWidth: 560 }}>
          {site.runtime !== "static" ? (
            <Field label={`${runtimeName(site.runtime)} version`}
              hint={site.runtime === "php" && phpEol(version) ? "This version no longer gets security fixes. Plan an upgrade." : "Switching takes a few seconds and keeps your files."}>
              {(id) => (
                <select id={id} className="select" value={version} onChange={(e) => setVersion(e.target.value)}>
                  {versions.slice().reverse().map((v) => <option key={v} value={v}>{runtimeName(site.runtime)} {v}{site.runtime === "php" && phpEol(v) ? " (end of life)" : ""}</option>)}
                </select>
              )}
            </Field>
          ) : <p className="muted">Static websites are served straight from disk and have no runtime.</p>}
          {!isApp && rt.web_servers.length > 1 ? (
            <Field label="Web server" hint="Nginx + Apache reads .htaccess files, which many WordPress plugins need.">
              {(id) => <select id={id} className="select" value={web} onChange={(e) => setWeb(e.target.value as WebServer)}>
                {rt.web_servers.map((w) => <option key={w} value={w}>{webServerLabel[w]}</option>)}</select>}
            </Field>
          ) : null}
          {isApp ? (
            <Field label="Start command" hint="Runs in the app folder. Your app must listen on $PORT.">
              {(id) => <input id={id} className="input mono" value={start} onChange={(e) => setStart(e.target.value)} spellCheck={false} />}
            </Field>
          ) : null}
          <div><Button variant="primary" disabled={!dirty} loading={busy === "save"} onClick={async () => {
            setBusy("save");
            try {
              const n = await api.updateSite(site.domain, { version: version !== site.version ? version : undefined,
                web_server: web !== site.web_server ? web : undefined, start_command: start !== site.start_command ? start : undefined });
              onChange(n); toast("ok", "Saved changes");
            } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
          }}>Save changes</Button></div>
        </div>
      </section>
    </div>
  );
}

function StatusTab({ site, onChange }: { site: Site; onChange: (s: Site) => void }) {
  const toast = useToast();
  const [status, setStatus] = useState<SiteStatus>(site.status);
  const [ips, setIps] = useState((site.allow_ips || []).join("\n"));
  const [busy, setBusy] = useState(false);
  const list = ips.split(/[\s,]+/).filter(Boolean);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Website status</h2><p className="small muted">Visitors see your branded page while the site isn't active.</p></div><StatusBadge status={site.status} /></div>
      <div className="panel-body form-grid" style={{ maxWidth: 620 }}>
        <Segmented<SiteStatus> label="Status" value={status} onChange={setStatus}
          options={[{ value: "active", label: "Active" }, { value: "maintenance", label: "Maintenance" }, { value: "suspended", label: "Suspended" }]} />
        {status === "maintenance" && (
          <Field label="Who can still see the real site" hint="One IP address per line. Your current address is a good start.">
            {(id) => <textarea id={id} className="textarea" rows={3} value={ips} onChange={(e) => setIps(e.target.value)} placeholder="102.89.34.12" />}
          </Field>
        )}
        <div className="callout info"><FileWarning size={18} /><p className="small">
          {status === "active" ? "The website is open to everyone." :
            status === "maintenance" ? "Everyone else gets a “Back soon” page with HTTP 503, so search engines know it's temporary." :
              "Visitors see “Site unavailable”. Node.js and Python apps are stopped."}
          {" "}Edit the pages in the website's errors folder or, for every site, in /etc/ike/pages.</p></div>
        <div><Button variant="primary" loading={busy} disabled={status === site.status && list.join() === (site.allow_ips || []).join()} onClick={async () => {
          setBusy(true);
          try { const n = await api.setStatus(site.domain, status, status === "maintenance" ? list : []); onChange(n); toast("ok", `Status set to ${status}`); }
          catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
        }}>Apply status</Button></div>
      </div>
    </section>
  );
}

function LogsTab({ site }: { site: Site }) {
  const isApp = site.runtime === "node" || site.runtime === "python";
  const kinds: { value: LogKind; label: string }[] = [
    { value: "access", label: "Access" }, { value: "error", label: "Errors" },
    ...(site.runtime === "php" ? [{ value: "php" as LogKind, label: "PHP" }] : []),
    ...(site.web_server !== "nginx" ? [{ value: "apache-error" as LogKind, label: "Apache" }] : []),
    ...(isApp ? [{ value: "app" as LogKind, label: "App output" }] : []),
  ];
  const [kind, setKind] = useState<LogKind>(isApp ? "app" : "access");
  const [text, setText] = useState<string | null>(null);
  const [filter, setFilter] = useState("");
  const load = () => { setText(null); api.siteLog(site.domain, kind).then(setText).catch((e) => setText(`Couldn't read the log: ${e.message}`)); };
  useEffect(load, [kind, site.domain]); // eslint-disable-line react-hooks/exhaustive-deps
  const shown = (text ?? "").split("\n").filter((l) => !filter || l.toLowerCase().includes(filter.toLowerCase())).join("\n");
  return (
    <section className="panel">
      <div className="panel-head" style={{ flexWrap: "wrap" }}>
        <Segmented<LogKind> label="Log" value={kind} onChange={setKind} options={kinds} />
        <div className="row">
          <input className="input" style={{ width: 200, height: 34 }} placeholder="Filter lines" value={filter} onChange={(e) => setFilter(e.target.value)} aria-label="Filter log lines" />
          <Button size="sm" onClick={load}><RefreshCw size={15} />Refresh</Button>
        </div>
      </div>
      <div className="panel-body">
        {text === null ? <Skeleton h={180} /> : shown ? <pre className="code" style={{ maxHeight: 460 }}>{shown}</pre> :
          <p className="muted">{filter ? "No lines match the filter." : "This log is empty."}</p>}
      </div>
    </section>
  );
}

function ToolsTab({ site }: { site: Site }) {
  const toast = useToast();
  const [busy, setBusy] = useState(false);
  const [wpOpen, setWpOpen] = useState(false);
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2>Install WordPress</h2><p className="small muted">Creates the database and admin user for you.</p></div></div>
        <div className="panel-body">
          {site.runtime === "php"
            ? <Button variant="primary" onClick={() => setWpOpen(true)}>Install WordPress</Button>
            : <p className="muted">WordPress needs a PHP website.</p>}
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Fix file permissions</h2><p className="small muted">Resets ownership and modes after uploads or a migration. Config files like wp-config.php become private.</p></div></div>
        <div className="panel-body">
          <Button loading={busy} onClick={async () => {
            setBusy(true);
            try { const r = await api.fixPermissions(site.domain); toast("ok", `Fixed ${r.owners_fixed.toLocaleString()} files${r.skipped_links ? `, skipped ${r.skipped_links} links` : ""}`); }
            catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
          }}><Wrench size={16} />Fix permissions</Button>
        </div>
      </section>
      <WordPressDialog open={wpOpen} onClose={() => setWpOpen(false)} site={site} />
    </div>
  );
}

export function WordPressDialog({ open, onClose, site }: { open: boolean; onClose: () => void; site: Site }) {
  const [title, setTitle] = useState("");
  const [user, setUser] = useState("");
  const [email, setEmail] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<WordPressResult | null>(null);
  const close = () => { setDone(null); setError(null); onClose(); };
  return (
    <Modal open={open} onClose={close} title={done ? "WordPress is ready" : `Install WordPress on ${site.domain}`}
      description={done ? "Save the password now. It isn't shown again." : "Downloads the latest WordPress and verifies it before installing."}
      footer={done ? <Button variant="primary" onClick={close}>Done</Button> : <>
        <Button onClick={close}>Cancel</Button>
        <Button variant="primary" loading={busy} disabled={!title || !user || !email} onClick={async () => {
          setBusy(true); setError(null);
          try { setDone(await api.installWordPress(site.domain, { title, admin_user: user, admin_email: email })); }
          catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>Install WordPress</Button></>}>
      {done ? (
        <div className="form-grid">
          <Field label="Admin login">{() => <CopyField value={done.admin_url} label="Admin URL" />}</Field>
          <div className="form-2">
            <Field label="Username">{() => <CopyField value={done.admin_user} label="Username" />}</Field>
            <Field label="Password">{() => <CopyField value={done.admin_password} label="Password" />}</Field>
          </div>
          <p className="small muted"><ShieldCheck size={14} style={{ display: "inline", verticalAlign: -2 }} /> Database <code>{done.database.name}</code> was created for this site.</p>
        </div>
      ) : (
        <div className="form-grid">
          <Field label="Site title">{(id) => <input id={id} className="input" value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Naija Bites" autoFocus />}</Field>
          <div className="form-2">
            <Field label="Admin username" hint="Anything but “admin”.">{(id) => <input id={id} className="input" value={user} onChange={(e) => setUser(e.target.value)} autoComplete="off" />}</Field>
            <Field label="Admin email">{(id) => <input id={id} className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} />}</Field>
          </div>
          {error ? <div className="callout bad" role="alert">{error}</div> : null}
        </div>
      )}
    </Modal>
  );
}
