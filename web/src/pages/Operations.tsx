// Server care: updates, the security scan with drift detection, resource usage and limits, and cPanel migration.
import { useState } from "react";
import {
  ArrowUpCircle, CircleAlert, CircleCheck, FileDiff, FolderInput, PackageCheck, Power, RefreshCw, RotateCcw, Search,
  ShieldAlert, ShieldCheck, SlidersHorizontal, TriangleAlert,
} from "lucide-react";
import { api } from "../api/client";
import type { AccountUsage, AutoUpdates, ImportPlan, Job, Limits, SecurityCheck } from "../api/types";
import { UploadButton } from "../components/kit5";
import { Badge, Button, Empty, Field, Meter, Modal, Segmented, Skeleton, formatMB, timeAgo, useAsync, useToast } from "../components/ui";
import { PageHead, Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { UsageChart } from "../components/chart";

/* =============================================================== Updates */

export function Updates() {
  const toast = useToast();
  const st = useAsync(() => api.updates());
  const rel = useAsync(() => api.ikeRelease());
  const { job, start, clear } = useJob((j) => { st.reload(); toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Updates installed" : "Updates failed"); });
  const [auto, setAuto] = useState<AutoUpdates | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [rebootIn, setRebootIn] = useState("1");
  const [rebootOpen, setRebootOpen] = useState(false);
  const a = auto ?? st.data?.auto ?? null;
  const pkgs = st.data?.packages ?? [];
  const sec = st.data?.security_count ?? 0;

  const run = async (key: string, fn: () => Promise<unknown>) => {
    setBusy(key);
    try { await fn(); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
  };

  return (
    <div className="stack">
      <PageHead title="Updates" text="Operating system packages and Ike itself. Updates run in the background and are written to the activity log."
        actions={<Button onClick={() => { st.reload(); rel.reload(); }} loading={st.loading}><RefreshCw size={16} />Check again</Button>} />

      {st.data?.reboot_required ? (
        <div className="callout warn" role="status">
          <Power size={18} />
          <div style={{ flex: 1 }}>
            <strong>A restart is needed to finish updating</strong>
            <p className="small muted">Updated: {(st.data.reboot_packages ?? []).join(", ") || "system libraries"}. Websites are offline for about a minute while the server restarts.</p>
          </div>
          <Button size="sm" onClick={() => setRebootOpen(true)}>Schedule restart</Button>
        </div>
      ) : null}

      <div className="grid-2">
        <section className="panel">
          <div className="panel-head">
            <div><h2>System packages</h2><p className="small muted">{st.data ? `Checked ${timeAgo(st.data.checked_at)}` : "Checking…"}</p></div>
            {st.data ? (pkgs.length ? <Badge tone={sec ? "bad" : "warn"}>{pkgs.length} waiting</Badge> : <Badge tone="ok">Up to date</Badge>) : null}
          </div>
          {!st.data ? <div className="panel-body stack"><Skeleton /><Skeleton /><Skeleton /></div> : pkgs.length === 0 ? (
            <Empty icon={<PackageCheck size={22} />} title="Everything is up to date" text="Ike checks again every day and can install security fixes on its own." />
          ) : (
            <>
              <div className="table-wrap"><table className="responsive">
                <thead><tr><th>Package</th><th>Version</th><th /></tr></thead>
                <tbody>{pkgs.map((p) => (
                  <tr key={p.name}>
                    <td className="lead mono">{p.name}</td>
                    <td className="mono small"><span className="muted">{p.from}</span> → {p.to}</td>
                    <td style={{ textAlign: "right" }}>{p.security ? <Badge tone="bad">Security</Badge> : null}</td>
                  </tr>
                ))}</tbody>
              </table></div>
              <div className="panel-body row" style={{ borderTop: "1px solid var(--line)" }}>
                {sec ? <Button variant="primary" loading={busy === "sec"} onClick={() => run("sec", async () => start(await api.applyUpdates(true)))}><ShieldCheck size={16} />Install {sec} security {sec === 1 ? "update" : "updates"}</Button> : null}
                <Button variant={sec ? "secondary" : "primary"} loading={busy === "all"} onClick={() => run("all", async () => start(await api.applyUpdates(false)))}>Install all {pkgs.length}</Button>
              </div>
            </>
          )}
        </section>

        <div className="stack">
          <section className="panel">
            <div className="panel-head">
              <div><h2>Ike</h2><p className="small muted">{rel.data ? `Version ${rel.data.current}` : "Checking GitHub…"}</p></div>
              {rel.data ? (rel.data.update_available ? <Badge tone="accent">{rel.data.latest} available</Badge> : <Badge tone="ok">Latest</Badge>) : null}
            </div>
            <div className="panel-body stack" style={{ gap: 12 }}>
              {rel.error ? <p className="small muted">{rel.error}</p> : null}
              {rel.data?.update_available ? (
                <>
                  <p className="small">{rel.data.notes}</p>
                  <p className="small muted">Downloads are checked against their SHA-256 checksums. Websites stay online; the panel reconnects in a few seconds.</p>
                  <div className="row">
                    <Button variant="primary" loading={busy === "ike"} onClick={() => run("ike", async () => start(await api.updateIke()))}><ArrowUpCircle size={16} />Update to {rel.data.latest}</Button>
                    <a className="btn ghost" href={rel.data.url} target="_blank" rel="noreferrer">Release notes</a>
                  </div>
                </>
              ) : rel.data ? <p className="small muted">You're on the latest release.</p> : <Skeleton />}
            </div>
          </section>

          <section className="panel">
            <div className="panel-head"><div><h2>Automatic updates</h2><p className="small muted">Security fixes only. Feature updates always wait for you.</p></div></div>
            {a ? (
              <div className="panel-body form-grid">
                <Toggle checked={a.security_updates} onChange={(v) => setAuto({ ...a, security_updates: v })} label="Install security updates every day" />
                <Toggle checked={a.auto_reboot} onChange={(v) => setAuto({ ...a, auto_reboot: v })} label="Restart when an update needs it" />
                {a.auto_reboot ? (
                  <Field label="Restart time (server time)" hint="Pick a quiet hour. Websites are offline for about a minute.">
                    {(id) => <input id={id} className="input mono" style={{ maxWidth: 120 }} value={a.reboot_time} onChange={(e) => setAuto({ ...a, reboot_time: e.target.value })} placeholder="03:30" />}
                  </Field>
                ) : null}
                <div><Button variant="primary" disabled={!auto} loading={busy === "auto"} onClick={() => run("auto", async () => { const r = await api.setAutoUpdates(a); setAuto(null); st.setData({ ...st.data!, auto: r }); toast("ok", "Automatic updates saved"); })}>Save</Button></div>
              </div>
            ) : <div className="panel-body"><Skeleton /></div>}
          </section>
        </div>
      </div>

      <JobModal job={job} onClose={clear} />
      <Modal open={rebootOpen} onClose={() => setRebootOpen(false)} title="Restart the server?"
        description="Everyone signed in over SSH gets a warning, and websites are offline for about a minute."
        footer={<><Button onClick={() => setRebootOpen(false)}>Cancel</Button>
          <Button variant="danger" loading={busy === "reboot"} onClick={() => run("reboot", async () => { await api.scheduleReboot(Number(rebootIn)); setRebootOpen(false); toast("ok", `Restart scheduled in ${rebootIn} min`); })}><Power size={16} />Schedule restart</Button></>}>
        <Field label="Restart in">
          {(id) => <select id={id} className="select" value={rebootIn} onChange={(e) => setRebootIn(e.target.value)}>
            <option value="1">1 minute</option><option value="15">15 minutes</option><option value="60">1 hour</option><option value="240">4 hours</option>
          </select>}
        </Field>
      </Modal>
    </div>
  );
}

/* ====================================================== Security + drift */

const areaName: Record<string, string> = { ssh: "SSH", firewall: "Firewall", updates: "Updates", network: "Network", websites: "Websites", files: "Files" };

function CheckRow({ c }: { c: SecurityCheck }) {
  const Icon = c.severity === "ok" ? CircleCheck : c.severity === "warn" ? TriangleAlert : ShieldAlert;
  const color = c.severity === "ok" ? "var(--ok)" : c.severity === "warn" ? "var(--warn)" : "var(--bad)";
  return (
    <li style={{ display: "grid", gridTemplateColumns: "20px 1fr auto", gap: 12, padding: "12px 20px", borderTop: "1px solid var(--line)" }}>
      <Icon size={18} style={{ color, marginTop: 2 }} aria-label={c.severity === "ok" ? "Passed" : c.severity === "warn" ? "Warning" : "Problem"} />
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 500 }}>{c.title}</div>
        {c.fix && c.severity !== "ok" ? <div className="small muted" style={{ marginTop: 2 }}>{c.fix}</div> : null}
      </div>
      <span className="small muted">{areaName[c.area] ?? c.area}</span>
    </li>
  );
}

export function Security() {
  const toast = useToast();
  const scan = useAsync(() => api.security());
  const drift = useAsync(() => api.drift());
  const [busy, setBusy] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const r = scan.data;
  const sorted = r ? [...r.checks].sort((x, y) => rank(y.severity) - rank(x.severity)) : [];
  const tone = !r ? "" : r.score >= 85 ? "var(--ok)" : r.score >= 65 ? "var(--warn)" : "var(--bad)";

  const restore = async (paths: string[]) => {
    setBusy(paths.join() || "all");
    try {
      const fixed = await api.reconcile(paths);
      drift.reload();
      toast("ok", `Restored ${fixed.length} ${fixed.length === 1 ? "file" : "files"}`);
    } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
  };

  return (
    <div className="stack">
      <PageHead title="Security" text="A baseline scan of this server, and the config files Ike manages that were changed by hand."
        actions={<Button loading={scan.loading} onClick={() => { scan.reload(); drift.reload(); }}><Search size={16} />Scan again</Button>} />

      <section className="panel">
        <div className="panel-head" style={{ alignItems: "center" }}>
          <div className="row" style={{ gap: 18 }}>
            {r ? (
              <div style={{ fontFamily: "var(--font-display)", fontSize: "2.6rem", fontWeight: 700, lineHeight: 1, color: tone, fontVariantNumeric: "tabular-nums" }}
                aria-label={`Score ${r.score} out of 100`}>{r.score}<span className="small muted" style={{ fontSize: "0.9rem", fontWeight: 500 }}> / 100</span></div>
            ) : <Skeleton h={42} w={90} />}
            <div>
              <h2>Baseline scan</h2>
              <p className="small muted">{r ? `${r.checks.filter((c) => c.severity === "bad").length} problems, ${r.checks.filter((c) => c.severity === "warn").length} warnings, ${r.checks.filter((c) => c.severity === "ok").length} passed` : "Scanning…"}</p>
            </div>
          </div>
        </div>
        {r ? <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>{sorted.map((c) => <CheckRow key={c.id} c={c} />)}</ul>
          : <div className="panel-body stack"><Skeleton /><Skeleton /><Skeleton /></div>}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div><h2>Configuration drift</h2><p className="small muted">Ike compares every file it writes against what it last wrote. Changes made by hand can be lost on the next update, so restore them or move them into Ike.</p></div>
          {(drift.data ?? []).length > 1 ? <Button loading={busy === "all"} onClick={() => restore([])}><RotateCcw size={16} />Restore all</Button> : null}
        </div>
        {!drift.data ? <div className="panel-body"><Skeleton /></div> : drift.data.length === 0 ? (
          <Empty icon={<FileDiff size={22} />} title="No drift" text="Every managed config file matches what Ike wrote." />
        ) : (
          <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
            {drift.data.map((d) => (
              <li key={d.path} style={{ borderTop: "1px solid var(--line)", padding: "14px 20px", display: "grid", gap: 10 }}>
                <div className="row" style={{ justifyContent: "space-between" }}>
                  <div style={{ minWidth: 0 }}>
                    <code style={{ wordBreak: "break-all" }}>{d.path}</code>
                    <div className="small muted" style={{ marginTop: 4 }}>{d.kind} for {d.owner} · {d.state === "missing" ? "deleted" : "edited by hand"}</div>
                  </div>
                  <div className="row">
                    {d.diff ? <Button size="sm" variant="ghost" aria-expanded={open === d.path} onClick={() => setOpen(open === d.path ? null : d.path)}>{open === d.path ? "Hide changes" : "Show changes"}</Button> : null}
                    <Button size="sm" loading={busy === d.path} onClick={() => restore([d.path])}><RotateCcw size={15} />Restore</Button>
                  </div>
                </div>
                {open === d.path && d.diff ? (
                  <pre className="code" style={{ margin: 0 }}>{d.diff.split("\n").filter(Boolean).map((l, i) => (
                    <div key={i} style={{ color: l.startsWith("+") ? "var(--ok)" : l.startsWith("-") ? "var(--bad)" : undefined }}>{l}</div>
                  ))}</pre>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

const rank = (s: string) => (s === "bad" ? 2 : s === "warn" ? 1 : 0);

/* ======================================================= Usage + limits */

export function Usage() {
  const usage = useAsync(() => api.usage());
  const [pick, setPick] = useState<string | null>(null);
  const [metric, setMetric] = useState<"cpu" | "mem">("cpu");
  const [editing, setEditing] = useState<AccountUsage | null>(null);
  const list = usage.data ?? [];
  const cur = list.find((u) => u.account === pick) ?? list[0];

  return (
    <div className="stack">
      <PageHead title="Resource usage" text="CPU, memory, disk and bandwidth per account, sampled every 15 minutes. Limits are enforced by the kernel, so one busy account can't slow the rest."
        actions={<Button loading={usage.loading} onClick={usage.reload}><RefreshCw size={16} />Refresh</Button>} />

      {cur ? (
        <section className="panel">
          <div className="panel-head">
            <div className="row">
              <select className="select" style={{ width: "auto", minWidth: 180 }} value={cur.account} onChange={(e) => setPick(e.target.value)} aria-label="Account">
                {list.map((u) => <option key={u.account} value={u.account}>{u.account}</option>)}
              </select>
              <Segmented label="Metric" value={metric} onChange={setMetric} options={[{ value: "cpu", label: "CPU" }, { value: "mem", label: "Memory" }]} />
            </div>
            <Button onClick={() => setEditing(cur)}><SlidersHorizontal size={16} />Limits</Button>
          </div>
          <div className="panel-body">
            <UsageChart
              label={`${metric === "cpu" ? "CPU" : "Memory"} for ${cur.account} over 24 hours`}
              points={cur.history.map((h) => ({ t: h.t, v: metric === "cpu" ? h.cpu : h.mem }))}
              unit={metric === "cpu" ? "%" : ""}
              format={metric === "cpu" ? undefined : (v) => formatMB(Math.round(v))}
              limit={metric === "cpu" ? cur.limits.cpu_percent : cur.limits.memory_mb} />
            <p className="small muted" style={{ marginTop: 8 }}>
              {metric === "cpu" ? "100% is one full CPU core. " : ""}
              {(metric === "cpu" ? cur.limits.cpu_percent : cur.limits.memory_mb) ? "The dashed line is this account's limit." : "This account has no limit set."}
            </p>
          </div>
        </section>
      ) : null}

      <section className="panel">
        {!usage.data ? <div className="panel-body stack"><Skeleton /><Skeleton /></div> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Account</th><th>CPU now</th><th>Memory</th><th>Disk</th><th className="num">Bandwidth today</th><th /></tr></thead>
            <tbody>{list.map((u) => (
              <tr key={u.account}>
                <td className="lead"><button className="link" onClick={() => setPick(u.account)} style={{ fontWeight: 600 }}>{u.account}</button><div className="small muted">{u.processes} processes</div></td>
                <td style={{ minWidth: 120 }}><UsageCell value={u.cpu_percent} limit={u.limits.cpu_percent} text={`${u.cpu_percent}%`} /></td>
                <td style={{ minWidth: 140 }}><UsageCell value={u.memory_mb} limit={u.limits.memory_mb} text={formatMB(u.memory_mb)} limitText={u.limits.memory_mb ? formatMB(u.limits.memory_mb) : ""} /></td>
                <td style={{ minWidth: 140 }} data-hide-sm><UsageCell value={u.disk_mb} limit={u.limits.disk_mb} text={formatMB(u.disk_mb)} limitText={u.limits.disk_mb ? formatMB(u.limits.disk_mb) : ""} /></td>
                <td className="num" data-hide-sm>{formatMB(u.bandwidth_today_mb)}</td>
                <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" onClick={() => setEditing(u)}>Limits</Button></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <LimitsModal usage={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); usage.reload(); }} />
    </div>
  );
}

function UsageCell({ value, limit, text, limitText }: { value: number; limit: number; text: string; limitText?: string }) {
  return (
    <div style={{ display: "grid", gap: 6 }}>
      <span className="small" style={{ fontVariantNumeric: "tabular-nums" }}>{text}{limit ? <span className="muted"> of {limitText || `${limit}%`}</span> : <span className="muted"> · no limit</span>}</span>
      {limit ? <Meter value={(value / limit) * 100} label={`${text} of ${limitText || limit}`} /> : null}
    </div>
  );
}

function LimitsModal({ usage, onClose, onSaved }: { usage: AccountUsage | null; onClose: () => void; onSaved: () => void }) {
  const toast = useToast();
  const [l, setL] = useState<Limits | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const cur = l ?? usage?.limits ?? null;
  const close = () => { setL(null); setError(null); onClose(); };
  const num = (k: keyof Limits, label: string, hint: string, unit: string) => cur ? (
    <Field label={label} hint={hint}>
      {(id) => <div className="input-group"><input id={id} className="input mono" inputMode="numeric" value={cur[k] || ""} placeholder="No limit"
        onChange={(e) => setL({ ...cur, [k]: Number(e.target.value.replace(/\D/g, "")) || 0 })} /><span className="addon">{unit}</span></div>}
    </Field>
  ) : null;
  return (
    <Modal open={!!usage} onClose={close} title={`Limits for ${usage?.account ?? ""}`}
      description="Leave a field empty for no limit. Changes apply right away, without restarting websites."
      footer={<><Button onClick={close}>Cancel</Button><Button variant="primary" loading={busy} disabled={!l} onClick={async () => {
        setBusy(true); setError(null);
        try { await api.setLimits(usage!.account, cur!); toast("ok", "Limits saved"); setL(null); onSaved(); }
        catch (e) { setError((e as Error).message); } finally { setBusy(false); }
      }}>Save limits</Button></>}>
      {cur ? (
        <div className="form-grid">
          <div className="grid-2" style={{ gridTemplateColumns: "1fr 1fr" }}>
            {num("cpu_percent", "CPU", "100% is one core.", "%")}
            {num("memory_mb", "Memory", "At least 64 MB.", "MB")}
            {num("disk_mb", "Disk", "Quota on the home folder.", "MB")}
            {num("max_processes", "Processes", "Stops runaway forks.", "max")}
          </div>
          {num("php_workers", "PHP workers per website", "How many requests PHP handles at once. More uses more memory.", "workers")}
          {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
        </div>
      ) : null}
    </Modal>
  );
}

/* ============================================================ Migration */

function RestoreControls({ plan }: { plan: ImportPlan }) {
  const toast = useToast();
  const [account, setAccount] = useState(plan.account);
  const [skip, setSkip] = useState<string[]>([]);
  const [point, setPoint] = useState(true);
  const { job, start, clear } = useJob((j) => toast(j.status === "done" ? "ok" : "error", j.status === "done" ? `${account} restored` : j.error || "Restore failed"));
  const [busy, setBusy] = useState(false);
  const parts: [string, string][] = [["files", "Files"], ["databases", "Databases"], ["mail", "Email and passwords"], ["cron", "Cron jobs"], ["dns", "DNS zones"]];
  return (
    <div className="panel-body form-grid" style={{ borderTop: "1px solid var(--line)" }}>
      <h3 style={{ fontSize: "1rem" }}>3. Restore</h3>
      <Field label="Ike account name">{(id) => <input id={id} className="input mono" value={account} onChange={(e) => setAccount(e.target.value.toLowerCase())} />}</Field>
      <div className="row">{parts.map(([k, l]) => <Toggle key={k} checked={!skip.includes(k)} label={l} onChange={(v) => setSkip(v ? skip.filter((x) => x !== k) : [...skip, k])} />)}</div>
      <Toggle checked={point} onChange={setPoint} label="Point DNS records that used the old server's IP at this server" />
      <p className="small muted">Everything is written as the new account's own user, never as root, so nothing in the backup can reach the rest of the server. Mailboxes keep their passwords; database passwords are new and shown in the job log.</p>
      <div><Button variant="primary" loading={busy} onClick={async () => {
        setBusy(true);
        try { start(await api.call<Job>("POST", `/imports/cpanel/${encodeURIComponent(plan.file)}/restore`, { account, skip, point_to_this_server: point })); }
        catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
      }}><FolderInput size={16} />Restore account</Button></div>
      <JobModal job={job} onClose={clear} />
    </div>
  );
}

export function Migration() {
  const [file, setFile] = useState("");
  const [plan, setPlan] = useState<ImportPlan | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  return (
    <div className="stack">
      <PageHead title="Migrate from cPanel" text="Move an account from a cPanel server using its full backup. Ike reads the backup first and shows exactly what will come across." />
      <div className="grid-2">
        <section className="panel">
          <div className="panel-head"><h2>1. Read the backup</h2></div>
          <form className="panel-body form-grid" onSubmit={async (e) => {
            e.preventDefault(); setBusy(true); setError(null); setPlan(null);
            try { setPlan(await api.inspectImport(file.trim())); } catch (err) { setError((err as Error).message); } finally { setBusy(false); }
          }}>
            <Field label="Backup file" error={error} hint={<>Upload it here, or copy it to <code>/var/lib/ike/imports</code> over SFTP. In this preview, try <code>cpmove-tundeprops.tar.gz</code>.</>}>
              {(id) => <input id={id} className="input mono" value={file} onChange={(e) => setFile(e.target.value)} placeholder="cpmove-username.tar.gz" spellCheck={false} />}
            </Field>
            <div className="row"><Button variant="primary" type="submit" loading={busy} disabled={!file.trim()}><Search size={16} />Read backup</Button>
              <UploadButton path={(f) => `/imports/cpanel/${encodeURIComponent(f.name)}/upload`} label="Upload backup" accept=".gz,.tar"
                onDone={(r) => { const p = r as ImportPlan; if (p?.cpanel_user) { setPlan(p); setFile(p.file); } }} /></div>
          </form>
        </section>
        <section className="panel">
          <div className="panel-head"><h2>Where to get the backup</h2></div>
          <div className="panel-body stack" style={{ gap: 10 }}>
            <p className="small">In cPanel, open <strong>Backup</strong> and choose <strong>Download a Full Account Backup</strong>. In WHM, use <strong>Transfer or Restore a cPanel Account</strong> or run:</p>
            <pre className="code" style={{ margin: 0 }}>/scripts/pkgacct username</pre>
            <p className="small muted">Ike never runs anything from inside the backup. It reads the list of domains, databases, mailboxes and cron jobs, and copies files without their owners or permissions.</p>
          </div>
        </section>
      </div>

      {plan ? (
        <section className="panel">
          <div className="panel-head">
            <div><h2>2. Review what comes across</h2><p className="small muted">cPanel user <strong>{plan.cpanel_user}</strong> becomes the Ike account <strong>{plan.account}</strong>. {formatMB(plan.home_size_mb)} of files.</p></div>
          </div>
          {plan.warnings.length ? (
            <div className="panel-body stack" style={{ gap: 8, paddingBottom: 0 }}>
              {plan.warnings.map((w) => <div key={w} className="callout warn"><TriangleAlert size={18} />{w}</div>)}
            </div>
          ) : null}
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Website</th><th>Type</th><th>PHP</th></tr></thead>
            <tbody>{plan.websites.map((w) => (
              <tr key={w.domain}>
                <td className="lead">{w.domain}</td>
                <td>{{ main: "Main domain", addon: "Addon domain", subdomain: "Subdomain", alias: "Alias (parked)" }[w.kind]}</td>
                <td>{w.php ? (w.php_installed ? <Badge>PHP {w.php}</Badge> : <Badge tone="warn">PHP {w.php}, not installed</Badge>) : <span className="muted small">Default</span>}</td>
              </tr>
            ))}</tbody>
          </table></div>
          <div className="panel-body">
            <dl className="kv">
              <dt>Databases</dt><dd>{plan.databases.map((d) => <code key={d} style={{ marginRight: 8 }}>{d}</code>)}</dd>
              <dt>Mailboxes</dt><dd>{plan.mailboxes.join(", ") || "None"}</dd>
              <dt>Cron jobs</dt><dd>{plan.cron_jobs}</dd>
            </dl>
          </div>
          <RestoreControls plan={plan} />
        </section>
      ) : null}
    </div>
  );
}
