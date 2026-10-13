// Website tabs and panels: the health assistant, environment variables, staging copies and app health checks.
import { useState } from "react";
import { CircleAlert, CircleCheck, CopyPlus, Eye, EyeOff, Info, Plus, Stethoscope, Trash2, TriangleAlert, Upload } from "lucide-react";
import { api } from "../api/client";
import type { EnvVar, Finding, HealthCheck, Site } from "../api/types";
import { Badge, Button, Field, Modal, Segmented, Skeleton, useAsync, useToast } from "../components/ui";
import { Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { href } from "../router";

const sevIcon = { ok: CircleCheck, info: Info, warn: TriangleAlert, bad: CircleAlert };
const sevColor = { ok: "var(--ok)", info: "var(--accent)", warn: "var(--warn)", bad: "var(--bad)" };

function FindingRow({ f }: { f: Finding }) {
  const Icon = sevIcon[f.severity];
  return (
    <li style={{ display: "grid", gridTemplateColumns: "20px 1fr", gap: 12, padding: "14px 20px", borderTop: "1px solid var(--line)" }}>
      <Icon size={18} style={{ color: sevColor[f.severity], marginTop: 2 }} aria-hidden />
      <div style={{ minWidth: 0, display: "grid", gap: 6 }}>
        <strong style={{ fontWeight: 600 }}>{f.title}</strong>
        {f.detail ? <p className="small muted">{f.detail}</p> : null}
        {f.fix ? <p className="small"><strong>What to do:</strong> {f.fix}</p> : null}
        {f.evidence ? <pre className="code small" style={{ margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{f.evidence}</pre> : null}
      </div>
    </li>
  );
}

function summaryOf(f: Finding[], fallback: string) {
  const bad = f.filter((x) => x.severity === "bad").length, warn = f.filter((x) => x.severity === "warn").length;
  if (!bad && !warn) return fallback;
  const parts = [bad ? `${bad} ${bad === 1 ? "problem" : "problems"}` : "", warn ? `${warn} ${warn === 1 ? "thing" : "things"} to look at` : ""].filter(Boolean);
  return `Found ${parts.join(" and ")}${bad ? ". Start with the first one." : "."}`;
}

export function HealthTab({ site }: { site: Site }) {
  const d = useAsync(() => api.diagnose(site.domain), [site.domain]);
  const worst = d.data?.findings.some((f) => f.severity === "bad") ? "bad" : d.data?.findings.some((f) => f.severity === "warn") ? "warn" : "ok";
  return (
    <section className="panel">
      <div className="panel-head">
        <div className="row" style={{ gap: 10 }}><Stethoscope size={20} aria-hidden />
          <div><h2>Health assistant</h2><p className="small muted">Checks the services this website needs, then reads its logs for known problems and explains them.</p></div></div>
        <Button loading={d.loading} onClick={d.reload}>Check again</Button>
      </div>
      {!d.data ? <div className="panel-body stack"><Skeleton /><Skeleton /></div> : (
        <>
          <div className="panel-body" style={{ paddingBottom: 14 }}>
            <p style={{ fontWeight: 600, color: sevColor[worst] }} role="status">{summaryOf(d.data.findings, d.data.summary)}</p>
          </div>
          <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>{d.data.findings.map((f, i) => <FindingRow key={i} f={f} />)}</ul>
        </>
      )}
    </section>
  );
}

export function EnvTab({ site }: { site: Site }) {
  const toast = useToast();
  const env = useAsync(() => api.env(site.domain), [site.domain]);
  const [rows, setRows] = useState<EnvVar[] | null>(null);
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const cur = rows ?? env.data ?? [];
  const isApp = site.runtime === "node" || site.runtime === "python";
  const edit = (i: number, patch: Partial<EnvVar>) => setRows(cur.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Environment variables</h2><p className="small muted">{isApp
          ? "Passed to the app when it starts. Saving restarts the app. PORT and HOST are set by Ike."
          : <>Written to <code>.env</code> next to the website's files, readable only by the account. Laravel and most PHP frameworks read it.</>}</p></div>
        <Button variant="ghost" size="sm" onClick={() => setShow(!show)}>{show ? <EyeOff size={15} /> : <Eye size={15} />}{show ? "Hide values" : "Show values"}</Button>
      </div>
      <div className="panel-body form-grid">
        {env.loading && !env.data ? <Skeleton /> : cur.length === 0 ? <p className="muted small">No variables yet.</p> : (
          <div className="stack" style={{ gap: 8 }}>
            {cur.map((r, i) => (
              <div key={i} style={{ display: "grid", gridTemplateColumns: "minmax(120px, 0.8fr) minmax(0, 1.4fr) auto", gap: 8 }}>
                <input className="input mono" aria-label="Name" value={r.key} onChange={(e) => edit(i, { key: e.target.value.toUpperCase().replace(/[^A-Z0-9_]/g, "_") })} spellCheck={false} />
                <input className="input mono" aria-label={`Value of ${r.key}`} type={show ? "text" : "password"} value={r.value} onChange={(e) => edit(i, { value: e.target.value })} spellCheck={false} autoComplete="off" />
                <Button variant="ghost" icon aria-label={`Remove ${r.key}`} onClick={() => setRows(cur.filter((_, j) => j !== i))}><Trash2 size={15} /></Button>
              </div>
            ))}
          </div>
        )}
        {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
        <div className="row">
          <Button onClick={() => setRows([...cur, { key: "", value: "" }])}><Plus size={16} />Add variable</Button>
          <Button variant="primary" disabled={!rows} loading={busy} onClick={async () => {
            setBusy(true); setError(null);
            try { await api.setEnv(site.domain, cur.filter((r) => r.key)); env.reload(); setRows(null); toast("ok", isApp ? "Saved, app restarted" : "Saved"); }
            catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}>Save</Button>
        </div>
      </div>
    </section>
  );
}

export function StagingPanel({ site }: { site: Site }) {
  const toast = useToast();
  const [confirm, setConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  const { job, start, clear } = useJob((j) => toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Finished" : j.error || "Failed"));
  const run = async (fn: () => Promise<Parameters<typeof start>[0]>) => {
    setBusy(true);
    try { start(await fn()); setConfirm(false); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
  };

  if (site.staging) {
    return (
      <section className="panel">
        <div className="panel-head">
          <div><h2>Staging copy of {site.staging_of}</h2><p className="small muted">Search engines are told not to index it. Push it live when your changes are ready.</p></div>
          <Badge tone="accent">Staging</Badge>
        </div>
        <div className="panel-body row">
          <Button variant="primary" onClick={() => setConfirm(true)}><Upload size={16} />Push to live</Button>
          <a className="btn ghost" href={href(`/sites/${encodeURIComponent(site.staging_of || "")}`)}>Open {site.staging_of}</a>
        </div>
        <Modal open={confirm} onClose={() => setConfirm(false)} title={`Replace ${site.staging_of} with this copy?`}
          description="Ike backs up the live files first. The live site keeps its own wp-config.php and .env, so it stays connected to its own database."
          footer={<><Button onClick={() => setConfirm(false)}>Cancel</Button><Button variant="primary" loading={busy} onClick={() => run(() => api.pushStaging(site.domain))}>Push to live</Button></>}>
          <p className="small muted">Database changes made on staging aren't copied. Make content changes on the live site.</p>
        </Modal>
        <JobModal job={job} onClose={clear} />
      </section>
    );
  }
  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Staging copy</h2><p className="small muted">A private copy at staging.{site.domain}, with its own files{site.runtime === "php" ? " and database" : ""}. Try updates, plugins or a new PHP version there first.</p></div>
        <Button loading={busy} onClick={() => run(() => api.createStaging(site.domain))}><CopyPlus size={16} />Create staging copy</Button>
      </div>
      <JobModal job={job} onClose={clear} />
    </section>
  );
}

export function HealthCheckPanel({ site, onChange }: { site: Site; onChange: (s: Site) => void }) {
  const toast = useToast();
  const [hc, setHc] = useState<HealthCheck>(site.health ?? { path: "/health", interval_sec: 30, fail_threshold: 3, action: "restart" });
  const [on, setOn] = useState(!!site.health);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Health check</h2><p className="small muted">Ike calls a URL on the app. If it fails several times in a row, Ike restarts the app or alerts you.</p></div>
        <Toggle checked={on} onChange={setOn} label={on ? "On" : "Off"} />
      </div>
      <div className="panel-body form-grid">
        {on ? (
          <>
            <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))" }}>
              <Field label="Path" error={error}>{(id) => <input id={id} className="input mono" value={hc.path} onChange={(e) => setHc({ ...hc, path: e.target.value })} />}</Field>
              <Field label="Every">{(id) => <select id={id} className="select" value={hc.interval_sec} onChange={(e) => setHc({ ...hc, interval_sec: Number(e.target.value) })}>
                <option value={15}>15 seconds</option><option value={30}>30 seconds</option><option value={60}>1 minute</option><option value={300}>5 minutes</option></select>}</Field>
              <Field label="After">{(id) => <select id={id} className="select" value={hc.fail_threshold} onChange={(e) => setHc({ ...hc, fail_threshold: Number(e.target.value) })}>
                {[2, 3, 5, 10].map((n) => <option key={n} value={n}>{n} failures</option>)}</select>}</Field>
            </div>
            <Segmented label="When it fails" value={hc.action} onChange={(a) => setHc({ ...hc, action: a })}
              options={[{ value: "restart", label: "Restart and alert me" }, { value: "notify", label: "Only alert me" }]} />
          </>
        ) : <p className="small muted">Without a health check, the app is still restarted if its process crashes.</p>}
        <div><Button variant="primary" loading={busy} onClick={async () => {
          setBusy(true); setError(null);
          try { onChange(await api.setHealthCheck(site.domain, on ? hc : null)); toast("ok", on ? "Health check saved" : "Health check turned off"); }
          catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>Save</Button></div>
      </div>
    </section>
  );
}
