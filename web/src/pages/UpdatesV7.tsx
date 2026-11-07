// Updates, part two: Ike's own versions (approve, automatic, skip, go back),
// the history of package updates with rollback, new Ubuntu releases, and the
// Ike version on every server the panel manages.
import { useState } from "react";
import { ArrowUpCircle, History, RotateCcw, ShieldCheck, TriangleAlert } from "lucide-react";
import { api, getServer, setServer } from "../api/client";
import type { Job } from "../api/types";
import type { RemoteServer } from "../api/types5";
import { Badge, Button, Empty, Modal, Segmented, Skeleton, timeAgo, useToast } from "../components/ui";
import { dateTime, enc, useAction, useGet } from "../components/kit5";
import { JobModal, useJob } from "../components/job";

export interface Upgrade { id: string; kind: "update" | "rollback"; from: string; to: string; by?: string; status: string; reason?: string; started_at: string; finished_at?: string }
export interface SelfStatus {
  current: string; latest?: { latest: string; update_available: boolean; url: string; notes: string; published_at?: string };
  checked_at?: string; check_error?: string; settings: { mode: "notify" | "auto" | "off"; skip?: string }; kept_versions: string[]; history: Upgrade[]; busy: boolean;
}
export interface PackageChange { name: string; from?: string; to?: string }
export interface PackageRun { id: string; kind: "security" | "all" | "rollback" | "release"; by?: string; status: string; error?: string; changes: PackageChange[]; skipped?: string[]; rollback_of?: string; rolled_back_by?: string; started_at: string; finished_at?: string }
export interface ReleaseCheck { name: string; ok: boolean; note?: string }
export interface UbuntuRelease { current: string; available?: string; supported: boolean; checked_at: string; checks?: ReleaseCheck[] }

const statusBadge = (s: string) => {
  switch (s) {
    case "done": return <Badge tone="ok">Done</Badge>;
    case "rolled_back": return <Badge tone="warn">Went back by itself</Badge>;
    case "failed": return <Badge tone="bad">Didn't go through</Badge>;
    case "checking": return <Badge tone="accent">Checking</Badge>;
    default: return <Badge>{s}</Badge>;
  }
};

/** IkeVersions is the Ike card: what's new, approve, automatic mode, going back. */
export function IkeVersions() {
  const toast = useToast();
  const st = useGet<SelfStatus>("/ike");
  const { busy, run } = useAction();
  const { job, start, clear } = useJob((j) => { st.reload(); toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Installed. The panel reconnects in a few seconds" : "The update didn't go through"); });
  const [back, setBack] = useState<string | null>(null);
  const d = st.data;
  if (!d) return <section className="panel"><div className="panel-body"><Skeleton h={140} /></div></section>;
  const avail = d.latest?.update_available && d.latest.latest !== d.settings.skip ? d.latest : undefined;
  const older = (d.kept_versions ?? []).filter((v) => v !== d.current);

  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Ike</h2><p className="small muted">Version {d.current}{d.checked_at ? `, checked ${timeAgo(d.checked_at)}` : ""}</p></div>
        {d.busy ? <Badge tone="accent">Updating…</Badge> : avail ? <Badge tone="accent">{avail.latest} available</Badge> : <Badge tone="ok">Latest</Badge>}
      </div>
      <div className="panel-body stack" style={{ gap: 14 }}>
        {avail ? (
          <>
            {avail.notes ? <p className="small" style={{ whiteSpace: "pre-line", maxHeight: 160, overflow: "auto" }}>{avail.notes}</p> : null}
            <p className="small muted">Ike keeps version {d.current} on this server. After the update, {d.current} checks that the new agent and panel are healthy and goes back by itself if they aren't. Websites stay online.</p>
            <div className="row" style={{ flexWrap: "wrap" }}>
              <Button variant="primary" loading={busy === "up"} disabled={d.busy} onClick={() => run("up", async () => start(await api.call<Job>("POST", "/ike/update")))}><ArrowUpCircle size={16} />Update to {avail.latest}</Button>
              <a className="btn ghost" href={avail.url} target="_blank" rel="noreferrer">Release notes</a>
              <Button variant="ghost" loading={busy === "skip"} onClick={() => run("skip", async () => { await api.call("PUT", "/ike/settings", { ...d.settings, skip: avail.latest }); st.reload(); }, `Skipping ${avail.latest}; newer versions are offered again`)}>Skip this version</Button>
            </div>
          </>
        ) : d.check_error ? <p className="small muted">Couldn't check for new versions: {d.check_error}.</p>
          : <p className="small muted">You're on the newest release{d.settings.skip ? ` (skipping ${d.settings.skip})` : ""}.</p>}

        <div className="stack" style={{ gap: 6 }}>
          <span className="small" style={{ fontWeight: 500 }}>New versions</span>
          <Segmented<"notify" | "auto" | "off"> label="New versions" value={d.settings.mode}
            onChange={(mode) => run("mode", async () => { await api.call("PUT", "/ike/settings", { ...d.settings, mode }); st.reload(); }, mode === "auto" ? "Ike installs new versions by itself" : mode === "notify" ? "Ike asks before installing" : "Ike stops checking")}
            options={[{ value: "notify", label: "Ask me" }, { value: "auto", label: "Install by itself" }, { value: "off", label: "Don't check" }]} />
          <p className="small muted">{d.settings.mode === "notify" ? "Admins are told in the panel and on their alert channels, and nothing installs until one of you presses Update." : d.settings.mode === "auto" ? "New versions install within a few hours of release, with the same health check and automatic way back." : "Ike won't look for new versions. Check again by hand from here."}</p>
        </div>

        <div className="row" style={{ flexWrap: "wrap" }}>
          <Button size="sm" loading={busy === "check"} onClick={async () => { await run("check", () => api.call("POST", "/ike/check")); st.reload(); }}>Check now</Button>
          {older.map((v) => <Button key={v} size="sm" variant="ghost" disabled={d.busy} onClick={() => setBack(v)}><RotateCcw size={14} />Go back to {v}</Button>)}
        </div>

        {d.history?.length ? (
          <details>
            <summary className="small" style={{ cursor: "pointer" }}>History ({d.history.length})</summary>
            <div className="table-wrap" style={{ marginTop: 8 }}><table className="responsive">
              <thead><tr><th>Change</th><th>By</th><th>When</th><th>Result</th></tr></thead>
              <tbody>{d.history.map((u) => (
                <tr key={u.id}>
                  <td className="mono small">{u.kind === "rollback" ? "back " : ""}{u.from} → {u.to}</td>
                  <td className="small">{u.by === "auto" ? "Automatic" : u.by || "—"}</td>
                  <td className="small muted">{dateTime(u.started_at)}</td>
                  <td>{statusBadge(u.status)}{u.reason ? <div className="small muted">{u.reason}</div> : null}</td>
                </tr>
              ))}</tbody>
            </table></div>
          </details>
        ) : null}
      </div>
      <JobModal job={job} onClose={clear} />
      <Modal open={!!back} onClose={() => setBack(null)} title={`Go back to Ike ${back ?? ""}?`}
        description={`Ike ${d.current} is kept too, so you can come forward again. Your settings, websites and data stay as they are.`}
        footer={<><Button onClick={() => setBack(null)}>Cancel</Button>
          <Button variant="primary" loading={busy === "back"} onClick={() => run("back", async () => { const j = await api.call<Job>("POST", "/ike/rollback", { version: back }); setBack(null); start(j); })}><RotateCcw size={16} />Go back to {back}</Button></>}>
        <p className="small muted">The panel restarts and reconnects in a few seconds.</p>
      </Modal>
    </section>
  );
}

const kindName: Record<PackageRun["kind"], string> = { security: "Security updates", all: "All updates", rollback: "Rollback", release: "New Ubuntu release" };

/** PackageHistory lists approved package updates and rolls one back. */
export function PackageHistory({ onJob }: { onJob?: () => void }) {
  const toast = useToast();
  const runs = useGet<PackageRun[]>("/updates/history");
  const { busy, run } = useAction();
  const { job, start, clear } = useJob((j) => { runs.reload(); onJob?.(); toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Rolled back" : "Rollback didn't finish"); });
  const [confirm, setConfirm] = useState<PackageRun | null>(null);
  const list = runs.data ?? [];
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Package update history</h2><p className="small muted">Every run you approved, with the versions it changed. A run can be rolled back while apt can still get the earlier versions.</p></div></div>
      {!runs.data ? <div className="panel-body"><Skeleton h={80} /></div> : list.length === 0 ? (
        <Empty icon={<History size={22} />} title="No updates installed from Ike yet" text="When you install updates here, each run is listed with what it changed." />
      ) : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Run</th><th>Changed</th><th data-hide-sm>By</th><th>When</th><th /></tr></thead>
          <tbody>{list.map((r) => (
            <tr key={r.id}>
              <td className="lead">{kindName[r.kind]}<div>{r.status === "failed" ? <Badge tone="bad">Failed</Badge> : r.status === "running" ? <Badge tone="accent">Running</Badge> : r.rolled_back_by ? <Badge tone="warn">Rolled back</Badge> : null}</div>
                {r.error ? <div className="small muted">{r.error}</div> : null}</td>
              <td className="small">
                <details><summary style={{ cursor: "pointer" }}>{(r.changes ?? []).length} {(r.changes ?? []).length === 1 ? "package" : "packages"}</summary>
                  <ul className="mono small" style={{ margin: "6px 0 0", paddingLeft: 16, maxHeight: 180, overflow: "auto" }}>{r.changes.map((c) => (
                    <li key={c.name}>{c.name} <span className="muted">{c.from || "new"} → {c.to || "removed"}</span></li>
                  ))}</ul>
                  {r.skipped?.length ? <p className="small muted">Couldn't get the earlier version of {r.skipped.join(", ")}.</p> : null}
                </details></td>
              <td className="small" data-hide-sm>{r.by || "—"}</td>
              <td className="small muted">{dateTime(r.started_at)}</td>
              <td>{r.kind !== "rollback" && r.kind !== "release" && r.status === "done" && !r.rolled_back_by && r.changes.some((c) => c.from && c.to) ?
                <Button size="sm" variant="ghost" onClick={() => setConfirm(r)}><RotateCcw size={14} />Roll back</Button> : null}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <JobModal job={job} onClose={clear} />
      <Modal open={!!confirm} onClose={() => setConfirm(null)} title="Roll back this update?"
        description="Ike reinstalls the earlier version of each package it changed, where apt can still get it, and tells you which it couldn't. Security fixes in this run are undone too."
        footer={<><Button onClick={() => setConfirm(null)}>Cancel</Button>
          <Button variant="danger" loading={busy === "rb"} onClick={() => run("rb", async () => { const j = await api.call<Job>("POST", `/updates/runs/${enc(confirm!.id)}/rollback`); setConfirm(null); start(j); })}><RotateCcw size={16} />Roll back</Button></>}>
        <p className="small muted">{confirm?.changes.filter((c) => c.from && c.to).length ?? 0} packages go back.</p>
      </Modal>
    </section>
  );
}

/** UbuntuRelease offers a move to a newer Ubuntu LTS, after checks and a snapshot. */
export function UbuntuReleaseCard() {
  const toast = useToast();
  const rel = useGet<UbuntuRelease>("/updates/release");
  const { busy, run } = useAction();
  const { job, start, clear } = useJob((j) => { rel.reload(); toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Moved. Restart the server to finish" : "The move didn't finish; see the log"); });
  const [open, setOpen] = useState(false);
  const [snap, setSnap] = useState(false);
  const r = rel.data;
  const ready = !!r?.checks?.length && r.checks.every((c) => c.ok);
  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Ubuntu release</h2><p className="small muted">{r ? `Ubuntu ${r.current}, checked ${timeAgo(r.checked_at)}` : "Checking…"}</p></div>
        {r?.available ? <Badge tone="accent">{r.available} available</Badge> : r ? <Badge tone="ok">Newest</Badge> : null}
      </div>
      <div className="panel-body stack" style={{ gap: 10 }}>
        {!r ? <Skeleton h={60} /> : !r.available ? (
          <p className="small muted">No newer Ubuntu LTS is offered for this server. Ike checks twice a day.</p>
        ) : (
          <>
            <ul className="stack" style={{ gap: 6, listStyle: "none", padding: 0, margin: 0 }}>{(r.checks ?? []).map((c) => (
              <li key={c.name} className="row small" style={{ gap: 8, alignItems: "flex-start" }}>
                {c.ok ? <ShieldCheck size={15} style={{ color: "var(--ok)", flexShrink: 0 }} /> : <TriangleAlert size={15} style={{ color: "var(--warn)", flexShrink: 0 }} />}
                <span>{c.name}{!c.ok && c.note ? <span className="muted">: {c.note}</span> : null}</span>
              </li>
            ))}</ul>
            <p className="small muted">This can't be rolled back from Ike. Take a snapshot at your provider first; restoring it is the way back.</p>
            <div className="row"><Button disabled={!ready} onClick={() => setOpen(true)}>Move to Ubuntu {r.available}</Button>
              <Button size="sm" variant="ghost" loading={rel.loading} onClick={() => { void api.call("GET", "/updates/release?refresh=1").then(() => rel.reload()); }}>Check again</Button></div>
          </>
        )}
      </div>
      <JobModal job={job} onClose={clear} />
      <Modal open={open} onClose={() => setOpen(false)} title={`Move to Ubuntu ${r?.available ?? ""}?`}
        description="This takes 20 to 60 minutes. Websites keep running until the restart at the end. Ike turns its PHP, Nginx and other package sources back on for the new release."
        footer={<><Button onClick={() => setOpen(false)}>Cancel</Button>
          <Button variant="danger" disabled={!snap} loading={busy === "rel"} onClick={() => run("rel", async () => { const j = await api.call<Job>("POST", "/updates/release", { have_snapshot: true }); setOpen(false); start(j); })}>Start the move</Button></>}>
        <label className="row small" style={{ gap: 8 }}><input type="checkbox" checked={snap} onChange={(e) => setSnap(e.target.checked)} /> I have a snapshot of this server at my provider, taken just now.</label>
      </Modal>
    </section>
  );
}

/** ServerVersions shows the Ike version on every server and updates them one at a time. */
export function ServerVersions({ latest }: { latest?: string }) {
  const servers = useGet<RemoteServer[]>("/servers");
  const { busy, run } = useAction();
  const { job, start, clear } = useJob(() => servers.reload());
  const list = servers.data ?? [];
  if (!servers.data || list.length === 0) return null;
  const older = (v?: string) => !!(v && latest && v !== latest && cmp(v, latest) < 0);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Ike on your servers</h2><p className="small muted">Each server updates itself with the same health check and way back. Update one first, then the rest.</p></div></div>
      <div className="table-wrap"><table className="responsive">
        <thead><tr><th>Server</th><th>Ike</th><th /></tr></thead>
        <tbody>{list.map((s) => (
          <tr key={s.id}>
            <td className="lead">{s.name}<div className="small muted mono">{s.address}</div></td>
            <td>{s.version ? <span className="mono small">{s.version}</span> : <span className="muted small">unknown</span>} {older(s.version) ? <Badge tone="accent">{latest} available</Badge> : s.status === "offline" ? <Badge tone="warn">Offline</Badge> : null}</td>
            <td>{older(s.version) && s.status !== "offline" ? (
              <Button size="sm" loading={busy === s.id} onClick={() => run(s.id, async () => {
                const prev = getServer();
                setServer(s.id);
                try { start(await api.call<Job>("POST", "/ike/update")); } finally { setServer(prev); }
              })}><ArrowUpCircle size={14} />Update</Button>) : null}</td>
          </tr>
        ))}</tbody>
      </table></div>
      <JobModal job={job} onClose={clear} />
    </section>
  );
}

function cmp(a: string, b: string) {
  const pa = a.replace(/^v/, "").split(/[.-]/).map((x) => parseInt(x, 10) || 0);
  const pb = b.replace(/^v/, "").split(/[.-]/).map((x) => parseInt(x, 10) || 0);
  for (let i = 0; i < 3; i++) if (pa[i] !== pb[i]) return (pa[i] ?? 0) - (pb[i] ?? 0);
  return 0;
}
