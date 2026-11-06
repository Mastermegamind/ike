// Backup plans (full server, accounts, websites, databases, mail): schedules,
// retention, encrypted incremental snapshots on S3/B2/SFTP/local storage,
// browsing and one-click restore, integrity checks and off-site recovery.
import { useState } from "react";
import { CircleCheck, CircleX, Folder, File, HardDriveDownload, KeyRound, LifeBuoy, Play, RotateCcw, ShieldCheck, Trash2 } from "lucide-react";
import { api } from "../api/client";
import type { Job, Role } from "../api/types";
import type { BackupNode, BackupPlan, BackupSnapshot, StorageTarget } from "../api/types5";
import { Badge, Button, CopyField, Empty, Field, Modal, Segmented, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { bytes, dateTime, enc, FormModal, NumberField, OnceModal, SelectField, TextField, useAction, useGet } from "../components/kit5";
import { useAccountParam } from "./AccountTools";
import { Storage } from "./Files";

const scopeLabel: Record<string, string> = { server: "Whole server", account: "Whole account", site: "One website", databases: "Databases", mail: "Email" };
const dayNames = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function when(p: BackupPlan) {
  const s = p.schedule;
  const hm = `${String(s.hour).padStart(2, "0")}:${String(s.minute).padStart(2, "0")}`;
  return s.frequency === "manual" ? "Only when you run it" : s.frequency === "hourly" ? `Every hour at :${String(s.minute).padStart(2, "0")}` : s.frequency === "daily" ? `Every day at ${hm}` : `${dayNames[s.weekday]}s at ${hm}`;
}

export function BackupsPage({ route, role }: { route: string; role: Role }) {
  const toast = useToast();
  const { account, picker } = useAccountParam(route);
  const [scope, setScope] = useState<"server" | "account">(role === "admin" ? "server" : "account");
  const [tab, setTab] = useState<"plans" | "storage" | "recover">("plans");
  const base = scope === "server" ? "/backups/plans" : `/accounts/${enc(account)}/backups/plans`;
  const storageBase = scope === "server" ? "/storage" : `/accounts/${enc(account)}/storage`;
  const plans = useGet<BackupPlan[]>(scope === "account" && !account ? null : base);
  const targets = useGet<StorageTarget[]>(scope === "account" && !account ? null : storageBase);
  const sites = useAsync(() => api.sites(), []);
  const [edit, setEdit] = useState<BackupPlan | null>(new URLSearchParams(route.split("?")[1] || "").get("new") === "1" ? null : null);
  const [key, setKey] = useState<string | null>(null);
  const [open, setOpen] = useState<BackupPlan | null>(null);
  const [del, setDel] = useState<BackupPlan | null>(null);
  const { job, start, clear } = useJob((j) => { toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Finished" : j.error || "Failed"); plans.reload(); });
  const { busy, run } = useAction();
  const blank = (): BackupPlan => ({ id: "", name: "", scope: scope === "server" ? "server" : "account", account: scope === "account" ? account : "", target: targets.data?.[0]?.id ?? "", enabled: true,
    schedule: { frequency: "daily", hour: 2, minute: 30, weekday: 0 }, retention: { keep_last: 3, keep_hourly: 0, keep_daily: 7, keep_weekly: 4, keep_monthly: 6 },
    excludes: [], check_days: 7, created_at: "" });
  return (
    <div className="stack">
      <PageHead title="Backups" text="Encrypted, incremental backups to cloud storage or another server, on a schedule, with one-click restore."
        actions={<>{role === "admin" ? <Segmented label="Backups of" value={scope} onChange={setScope} options={[{ value: "server", label: "Server" }, { value: "account", label: "Accounts" }]} /> : null}
          {scope === "account" ? picker : null}
          <Button variant="primary" disabled={!targets.data?.length} onClick={() => setEdit(blank())}><HardDriveDownload size={17} />New backup plan</Button></>} />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "plans", label: "Plans" }, { value: "storage", label: "Storage" }, ...(role === "admin" && scope === "server" ? [{ value: "recover" as const, label: "Recover a server" }] : [])]} />
      {tab === "storage" ? <Storage base={storageBase} /> : tab === "recover" ? <Recover targets={targets.data ?? []} onJob={start} /> : (
        <>
          {targets.data && targets.data.length === 0 ? <div className="callout warn"><HardDriveDownload size={18} />Add a storage target first: a Backblaze B2 or S3 bucket, an SFTP server, or a second disk. <button className="linklike" onClick={() => setTab("storage")}>Add storage</button></div> : null}
          <section className="panel">
            {!plans.data ? <div className="panel-body"><Skeleton /><Skeleton /></div> : plans.data.length === 0 ? (
              <Empty icon={<HardDriveDownload size={22} />} title="No backup plans" text="A plan says what to back up, where, when, and how long to keep it." />
            ) : (
              <div className="table-wrap"><table className="responsive">
                <thead><tr><th>Plan</th><th>When</th><th>Last backup</th><th data-hide-sm>Last check</th><th /></tr></thead>
                <tbody>{plans.data.map((p) => (
                  <tr key={p.id}>
                    <td className="lead"><button className="linklike primary-cell" onClick={() => setOpen(p)}>{p.name}</button>
                      <div className="small muted">{scopeLabel[p.scope]}{p.site ? `: ${p.site}` : p.account && p.scope !== "account" ? `: ${p.account}` : ""}{!p.enabled ? " · paused" : ""}</div></td>
                    <td className="small">{when(p)}</td>
                    <td className="small">{!p.last ? <span className="muted">Never</span> : p.last.status === "ok" ? <span><CircleCheck size={14} style={{ color: "var(--ok)", verticalAlign: -2 }} /> {timeAgo(p.last.finished_at || p.last.started_at)}
                      <div className="muted">{bytes(p.last.added_bytes)} new of {bytes(p.last.total_bytes)}</div></span>
                      : p.last.status === "running" ? <Badge>Running</Badge> : <span style={{ color: "var(--bad)" }}><CircleX size={14} style={{ verticalAlign: -2 }} /> {p.last.error || "Failed"}</span>}</td>
                    <td className="small" data-hide-sm>{!p.last_check ? <span className="muted">Not yet</span> : p.last_check.status === "ok" ? <Badge tone="ok">Intact</Badge> : <Badge tone="bad">Problem</Badge>}</td>
                    <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                      <Button size="sm" loading={busy === "run" + p.id} onClick={() => run("run" + p.id, () => api.call<Job>("POST", `${base}/${enc(p.id)}/run`)).then((j) => j && start(j))}><Play size={14} />Back up now</Button>
                      <Button size="sm" variant="ghost" onClick={() => setOpen(p)}><RotateCcw size={14} />Restore</Button>
                      <Button size="sm" variant="ghost" icon aria-label="Check integrity" onClick={() => run("chk" + p.id, () => api.call<Job>("POST", `${base}/${enc(p.id)}/check`)).then((j) => j && start(j))}><ShieldCheck size={15} /></Button>
                      <Button size="sm" variant="ghost" onClick={() => setEdit(p)}>Edit</Button>
                      <Button size="sm" variant="ghost" icon aria-label="Delete plan" onClick={() => setDel(p)}><Trash2 size={15} /></Button>
                    </td>
                  </tr>
                ))}</tbody>
              </table></div>
            )}
          </section>
        </>
      )}
      {edit ? <PlanModal plan={edit} base={base} targets={targets.data ?? []} sites={(sites.data ?? []).filter((s) => scope === "server" || s.account === account).map((s) => s.domain)}
        server={scope === "server"} onClose={() => setEdit(null)} onSaved={(p) => { plans.reload(); if (p.recovery_key) setKey(p.recovery_key); }} /> : null}
      {open ? <RestoreDrawer plan={open} base={`${base}/${enc(open.id)}`} admin={scope === "server"} onClose={() => setOpen(null)} onJob={start} /> : null}
      <OnceModal open={!!key} onClose={() => setKey(null)} title="Recovery key">
        <p className="small">With this key you can restore these backups onto a new server, even if this one is lost. Store it in a password manager, not on this server.</p>
        <CopyField value={key ?? ""} label="Recovery key" />
      </OnceModal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete the ${del?.name} plan?`} name={del?.name ?? ""} confirmLabel="Delete plan"
        description="The schedule stops. Backups already in storage stay there until you delete them from the bucket." onConfirm={async () => { await api.call("DELETE", `${base}/${enc(del!.id)}`); plans.reload(); }} />
      <JobModal job={job} onClose={clear} />
    </div>
  );
}

function PlanModal({ plan, base, targets, sites, server, onClose, onSaved }: { plan: BackupPlan; base: string; targets: StorageTarget[]; sites: string[]; server: boolean; onClose: () => void; onSaved: (p: BackupPlan) => void }) {
  const [p, setP] = useState<BackupPlan>(plan);
  const [ex, setEx] = useState((plan.excludes ?? []).join("\n"));
  const [dbs, setDbs] = useState((plan.databases ?? []).join(", "));
  const [pw, setPw] = useState("");
  const r = p.retention;
  return (
    <FormModal open onClose={onClose} title={plan.id ? `Edit ${plan.name}` : "New backup plan"} submit="Save plan" onSubmit={async () => {
      const body = { ...p, excludes: ex.split("\n").map((x) => x.trim()).filter(Boolean), databases: dbs.split(/[\s,]+/).filter(Boolean), recovery_password: pw };
      onSaved(await api.call<BackupPlan>(plan.id ? "PUT" : "POST", plan.id ? `${base}/${enc(plan.id)}` : base, body));
    }}>
      <TextField label="Name" value={p.name} onChange={(v) => setP({ ...p, name: v })} placeholder="Nightly off-site" />
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
        <SelectField label="Back up" value={p.scope} onChange={(v) => setP({ ...p, scope: v })}
          options={(server ? ["server", "account", "site", "databases", "mail"] : ["account", "site", "databases", "mail"]).map((s) => ({ value: s as BackupPlan["scope"], label: scopeLabel[s] }))} />
        {p.scope === "site" ? <SelectField label="Website" value={p.site ?? ""} onChange={(v) => setP({ ...p, site: v })} options={[{ value: "", label: "Choose…" }, ...sites.map((s) => ({ value: s, label: s }))]} /> : null}
        {server && p.scope !== "server" && p.scope !== "site" ? <TextField label="Account" value={p.account ?? ""} onChange={(v) => setP({ ...p, account: v })} mono /> : null}
        <SelectField label="Store in" value={p.target} onChange={(v) => setP({ ...p, target: v })} options={targets.map((t) => ({ value: t.id, label: t.name }))} />
      </div>
      {p.scope === "databases" ? <TextField label="Databases (empty = all)" value={dbs} onChange={setDbs} mono /> : null}
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(130px, 1fr))" }}>
        <SelectField label="How often" value={p.schedule.frequency} onChange={(v) => setP({ ...p, schedule: { ...p.schedule, frequency: v } })}
          options={[{ value: "hourly", label: "Every hour" }, { value: "daily", label: "Every day" }, { value: "weekly", label: "Every week" }, { value: "manual", label: "Only by hand" }]} />
        {p.schedule.frequency === "weekly" ? <SelectField label="Day" value={String(p.schedule.weekday)} onChange={(v) => setP({ ...p, schedule: { ...p.schedule, weekday: Number(v) } })} options={dayNames.map((d, i) => ({ value: String(i), label: d }))} /> : null}
        {p.schedule.frequency === "daily" || p.schedule.frequency === "weekly" ? <NumberField label="Hour" value={p.schedule.hour} onChange={(v) => setP({ ...p, schedule: { ...p.schedule, hour: v } })} max={23} /> : null}
        {p.schedule.frequency !== "manual" ? <NumberField label="Minute" value={p.schedule.minute} onChange={(v) => setP({ ...p, schedule: { ...p.schedule, minute: v } })} max={59} /> : null}
      </div>
      <Field label="Keep" hint="Older backups are removed once newer ones cover them. Unchanged files are stored once, so keeping many costs little.">{() => (
        <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(90px, 1fr))" }}>
          <NumberField label="Latest" value={r.keep_last} onChange={(v) => setP({ ...p, retention: { ...r, keep_last: v } })} />
          <NumberField label="Hourly" value={r.keep_hourly} onChange={(v) => setP({ ...p, retention: { ...r, keep_hourly: v } })} />
          <NumberField label="Daily" value={r.keep_daily} onChange={(v) => setP({ ...p, retention: { ...r, keep_daily: v } })} />
          <NumberField label="Weekly" value={r.keep_weekly} onChange={(v) => setP({ ...p, retention: { ...r, keep_weekly: v } })} />
          <NumberField label="Monthly" value={r.keep_monthly} onChange={(v) => setP({ ...p, retention: { ...r, keep_monthly: v } })} />
        </div>)}</Field>
      <Field label="Leave out" hint="One pattern per line, like node_modules or *.log">{(id) => <textarea id={id} className="input mono" rows={3} value={ex} onChange={(e) => setEx(e.target.value)} />}</Field>
      <NumberField label="Check backups can be read every (days)" value={p.check_days} onChange={(v) => setP({ ...p, check_days: v })} hint="0 = never. Checks read part of the data back to catch damage early." />
      {!plan.id ? <TextField label="Encryption password (optional)" value={pw} onChange={setPw} type="password" hint="Leave empty to generate one. Either way you'll get a recovery key to keep somewhere safe." /> : null}
      <Toggle checked={p.enabled} onChange={(v) => setP({ ...p, enabled: v })} label="Plan is on" />
    </FormModal>
  );
}

function RestoreDrawer({ plan, base, admin, onClose, onJob }: { plan: BackupPlan; base: string; admin: boolean; onClose: () => void; onJob: (j: Job) => void }) {
  const toast = useToast();
  const snaps = useAsync(() => api.call<BackupSnapshot[]>("GET", `${base}/snapshots`), [base]);
  const [snap, setSnap] = useState<string>("");
  const [path, setPath] = useState("/");
  const nodes = useAsync(() => (snap ? api.call<BackupNode[]>("GET", `${base}/snapshots/${enc(snap)}/browse?path=${enc(path)}`) : Promise.resolve([])), [snap, path]);
  const [picked, setPicked] = useState<Set<string>>(new Set());
  const [overwrite, setOverwrite] = useState(false);
  const [withDBs, setWithDBs] = useState(true);
  const [key, setKey] = useState<{ repository: string; password: string } | null>(null);
  const { busy, run } = useAction();
  return (
    <Modal open onClose={onClose} title={`Restore from ${plan.name}`} description="Pick a backup, then everything in it or just some files."
      footer={<>
        {admin ? <Button variant="ghost" onClick={() => run("key", () => api.call<{ repository: string; password: string }>("GET", `${base}/recovery-key`)).then((k) => k && setKey(k))}><KeyRound size={15} />Recovery key</Button> : null}
        <Button onClick={onClose}>Close</Button>
        <Button variant="primary" disabled={!snap} loading={busy === "restore"} onClick={() => run("restore", () => api.call<Job>("POST", `${base}/restore`, {
          snapshot: snap, paths: [...picked], databases: withDBs ? ["*"] : [], overwrite,
        })).then((j) => { if (j) { onJob(j); onClose(); toast("ok", "Restoring"); } })}><RotateCcw size={15} />{picked.size ? `Restore ${picked.size} selected` : "Restore everything"}</Button>
      </>}>
      <div className="form-grid">
        {!snaps.data ? <Skeleton /> : snaps.data.length === 0 ? <p className="muted">No backups yet. Run the plan first.</p> : (
          <SelectField label="Backup" value={snap} onChange={(v) => { setSnap(v); setPath("/"); setPicked(new Set()); }}
            options={[{ value: "", label: "Choose a backup…" }, ...snaps.data.map((s) => ({ value: s.id, label: `${dateTime(s.time)}${s.size ? ` (${bytes(s.size)})` : ""}` }))]} />
        )}
        {snap ? (
          <div>
            <div className="row" style={{ marginBottom: 6 }}><Button size="sm" variant="ghost" disabled={path === "/"} onClick={() => setPath(path.split("/").slice(0, -1).join("/") || "/")}>Up</Button><span className="mono small">{path}</span></div>
            {!nodes.data ? <Skeleton /> : <ul style={{ listStyle: "none", padding: 0, margin: 0, maxHeight: 280, overflowY: "auto", border: "1px solid var(--line)", borderRadius: "var(--radius)" }}>
              {nodes.data.map((n) => (
                <li key={n.path} className="row" style={{ padding: "6px 10px", borderBottom: "1px solid var(--line)", flexWrap: "nowrap" }}>
                  <input type="checkbox" aria-label={`Restore ${n.name}`} checked={picked.has(n.path)} onChange={(e) => { const s = new Set(picked); if (e.target.checked) s.add(n.path); else s.delete(n.path); setPicked(s); }} />
                  <button className="linklike row mono small" style={{ gap: 6, minWidth: 0 }} onClick={() => n.type === "dir" && setPath(n.path)}>{n.type === "dir" ? <Folder size={14} /> : <File size={14} />}<span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{n.name}</span></button>
                  <span className="small muted" style={{ marginLeft: "auto" }}>{n.type === "dir" ? "" : bytes(n.size)}</span>
                </li>))}
            </ul>}
          </div>
        ) : null}
        <Toggle checked={overwrite} onChange={setOverwrite} label="Replace files that exist now (otherwise they're kept and the backup copy is skipped)" />
        {plan.scope !== "mail" ? <Toggle checked={withDBs} onChange={setWithDBs} label="Also restore the databases in this backup" /> : null}
        {key ? <div className="callout"><div className="form-grid" style={{ width: "100%" }}><strong>Recovery key for {plan.name}</strong>
          <Field label="Repository">{() => <CopyField value={key.repository} label="Repository" />}</Field>
          <Field label="Password">{() => <CopyField value={key.password} label="Password" />}</Field></div></div> : null}
      </div>
    </Modal>
  );
}

function Recover({ targets, onJob }: { targets: StorageTarget[]; onJob: (j: Job) => void }) {
  const [f, setF] = useState({ target: targets[0]?.id ?? "", repository: "", password: "", snapshot: "latest" });
  const [confirm, setConfirm] = useState(false);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Recover a lost server</h2><p className="small muted">On a fresh Ike install, restore a whole-server backup made on another machine: websites, accounts, databases, mail, DNS and settings.</p></div></div>
      <div className="panel-body form-grid">
        <div className="callout warn"><LifeBuoy size={18} />This replaces what's on this server. Use it on a new server, not one that's already hosting sites.</div>
        <SelectField label="Storage the backups are in" value={f.target} onChange={(v) => setF({ ...f, target: v })} options={targets.map((t) => ({ value: t.id, label: t.name }))} hint="Add the same bucket under Storage first." />
        <TextField label="Repository (from the recovery key)" value={f.repository} onChange={(v) => setF({ ...f, repository: v })} mono />
        <TextField label="Password (from the recovery key)" value={f.password} onChange={(v) => setF({ ...f, password: v })} type="password" />
        <TextField label="Backup" value={f.snapshot} onChange={(v) => setF({ ...f, snapshot: v })} mono hint="latest, or a backup ID" />
        <div><Button variant="danger" disabled={!f.repository || !f.password} onClick={() => setConfirm(true)}>Recover this server</Button></div>
      </div>
      <FormModal open={confirm} onClose={() => setConfirm(false)} title="Replace this server with the backup?" submit="Recover" danger
        onSubmit={async () => onJob(await api.call<Job>("POST", "/backups/recover", f))}>
        <p>Everything on this server is replaced with the backup's contents. This takes a while for large servers.</p>
      </FormModal>
    </section>
  );
}
