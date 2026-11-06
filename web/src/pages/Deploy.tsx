// Git and image deployments for one website.
import { useEffect, useState } from "react";
import { CircleAlert, GitBranch, GitCommitHorizontal, History, KeyRound, Play, RefreshCw, RotateCcw, Rocket, Settings2, Square, Unplug, Webhook } from "lucide-react";
import { api, isDemo } from "../api/client";
import { isApp } from "../api/runtime";
import type { DeployInfo, DeploySetup, Deployment, Site } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Modal, Segmented, Skeleton, timeAgo, useAsync, useToast } from "../components/ui";
import { Toggle } from "../components/common";
import { useAppearance } from "../theme/appearance";

const statusTone: Record<Deployment["status"], "ok" | "warn" | "bad" | "accent" | undefined> = {
  live: "ok", building: "accent", queued: "accent", failed: "bad", cancelled: undefined, superseded: undefined, replaced: undefined,
};
const statusText: Record<Deployment["status"], string> = {
  live: "Live", building: "Building", queued: "Waiting", failed: "Failed", cancelled: "Cancelled", superseded: "Skipped", replaced: "Earlier",
};
const triggerText: Record<Deployment["trigger"], string> = { manual: "by hand", push: "on push", api: "by API", rollback: "rollback", preview: "preview" };

function duration(d: Deployment) {
  if (!d.started_at || !d.finished_at) return "";
  const s = Math.round((new Date(d.finished_at).getTime() - new Date(d.started_at).getTime()) / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

export function DeployTab({ site, onChange }: { site: Site; onChange: (s: Site) => void }) {
  const toast = useToast();
  const { saving } = useAppearance();
  const info = useAsync(() => api.deployInfo(site.domain), [site.domain]);
  const list = useAsync(() => api.deployments(site.domain), [site.domain]);
  const [editing, setEditing] = useState(false);
  const [secret, setSecret] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [refOpen, setRefOpen] = useState(false);
  const [busy, setBusy] = useState<string | null>(null);
  const [disconnect, setDisconnect] = useState(false);
  const active = (list.data ?? []).some((d) => d.status === "queued" || d.status === "building");

  // Follow running deployments; data saver checks less often.
  useEffect(() => {
    if (!active) return;
    const t = setTimeout(() => { list.reload(); api.site(site.domain).then(onChange).catch(() => {}); }, saving ? 5000 : 1500);
    return () => clearTimeout(t);
  }, [active, list.data, saving]); // eslint-disable-line react-hooks/exhaustive-deps

  const d = info.data?.deploy;
  const connected = !!d && (!!d.git || d.type === "image");
  const saved = (i: DeployInfo) => {
    info.setData(i);
    if (i.webhook_secret) setSecret(i.webhook_secret);
    setEditing(false);
    api.site(site.domain).then(onChange).catch(() => {});
  };
  const deployNow = async (ref?: { ref_type: string; ref: string }) => {
    setBusy("deploy");
    try { const dep = await api.deploy(site.domain, ref); list.reload(); setOpen(dep.id); toast("ok", "Deployment queued"); }
    catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); setRefOpen(false); }
  };

  if (info.loading && !info.data) return <Skeleton h={240} />;
  if (d?.preview_of) {
    return (
      <section className="panel"><div className="panel-body stack">
        <p>This is the preview of pull request #{d.preview_number} for <a href={`#/sites/${d.preview_of}`}><strong>{d.preview_of}</strong></a>. It's removed when the pull request closes.</p>
        <History_ list={list.data} loading={list.loading} onOpen={setOpen} />
      </div><DeploymentLog domain={site.domain} id={open} onClose={() => setOpen(null)} /></section>
    );
  }
  if (!connected || editing) {
    return <DeployForm site={site} current={d ?? undefined} hasToken={!!info.data?.has_token} onCancel={connected ? () => setEditing(false) : undefined} onSaved={saved} />;
  }

  const base = isDemo ? "https://vps-lagos-1.ike.host:2083" : window.location.origin;
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head">
          <div className="row" style={{ gap: 10 }}>
            <Rocket size={20} aria-hidden />
            <div>
              <h2>{d.type === "image" ? d.image : d.git!.url.replace(/^https:\/\/|\.git$/g, "").replace(/^git@([^:]+):/, "$1/")}</h2>
              <p className="small muted">
                {d.type === "image" ? "Container image" : <>{d.git!.ref_type === "tag" ? "Tag" : d.git!.ref_type === "commit" ? "Commit" : "Branch"} <code>{d.git!.ref}</code>{d.git!.subdir ? <> in <code>{d.git!.subdir}</code></> : null}
                  {d.type === "dockerfile" ? " · built from the Dockerfile" : d.type === "buildpack" ? " · built with buildpacks" : ""}</>}
                {" · "}{d.strategy === "blue-green" ? "Zero-downtime switch" : site.runtime === "php" || site.runtime === "static" ? "Instant switch" : "Restart on deploy"}
              </p>
            </div>
          </div>
          <div className="row">
            {d.git ? <Badge tone={d.git.auto_deploy ? "ok" : undefined}>{d.git.auto_deploy ? "Deploys on push" : "Manual deploys"}</Badge> : null}
          </div>
        </div>
        <div className="panel-body row">
          <Button variant="primary" loading={busy === "deploy"} onClick={() => deployNow()}><Play size={15} />Deploy now</Button>
          {d.git ? <Button onClick={() => setRefOpen(true)}><GitBranch size={16} />Deploy a branch, tag or commit</Button> : null}
          <Button variant="ghost" onClick={() => setEditing(true)}><Settings2 size={16} />Settings</Button>
          <Button variant="ghost" style={{ marginLeft: "auto" }} onClick={() => setDisconnect(true)}><Unplug size={16} />Disconnect</Button>
        </div>
        {d.current ? <div className="panel-body small muted" style={{ paddingTop: 0 }}>Live release <code>{d.current}</code>{d.strategy === "blue-green" && d.alt_port ? ` on instance ${(d.color || "a").toUpperCase()}` : ""}.</div> : null}
      </section>

      {d.git ? (
        <section className="panel">
          <div className="panel-head"><div className="row" style={{ gap: 10 }}><Webhook size={20} aria-hidden /><div><h2>Connect {d.git.provider === "github" ? "GitHub" : d.git.provider === "gitlab" ? "GitLab" : d.git.provider === "bitbucket" ? "Bitbucket" : "your Git host"}</h2>
            <p className="small muted">So pushes deploy by themselves{d.previews?.enabled ? " and pull requests get previews" : ""}.</p></div></div></div>
          <div className="panel-body form-grid">
            {info.data?.deploy_key ? (
              <Field label="Deploy key (read-only access to the repository)">{() => <CopyField value={info.data!.deploy_key!} label="Deploy key" />}</Field>
            ) : null}
            <Field label="Webhook URL">{() => <CopyField value={base + info.data!.webhook_path} label="Webhook URL" />}</Field>
            {secret ? (
              <div className="callout warn"><CircleAlert size={18} /><div style={{ minWidth: 0, flex: 1 }}><strong>Webhook secret: copy it now.</strong> Ike keeps only the server's copy and won't show it again.
                <div style={{ marginTop: 8 }}><CopyField value={secret} label="Webhook secret" /></div></div></div>
            ) : <p className="small muted">The webhook secret was shown when you connected. Lost it? Make a new one and update it in your Git host.</p>}
            <ol className="small" style={{ margin: 0, paddingLeft: 20, display: "grid", gap: 6 }}>{info.data!.instructions.map((s) => <li key={s}>{s}</li>)}</ol>
            <div className="row">
              <Button size="sm" loading={busy === "secret"} onClick={async () => { setBusy("secret"); try { const i = await api.regenerateWebhookSecret(site.domain); setSecret(i.webhook_secret || null); } finally { setBusy(null); } }}><RefreshCw size={14} />New webhook secret</Button>
              {info.data?.deploy_key ? <Button size="sm" loading={busy === "key"} onClick={async () => { setBusy("key"); try { info.setData(await api.regenerateDeployKey(site.domain)); toast("ok", "New deploy key. Replace the old one in your Git host."); } finally { setBusy(null); } }}><KeyRound size={14} />New deploy key</Button> : null}
            </div>
          </div>
        </section>
      ) : null}

      <section className="panel">
        <div className="panel-head"><div className="row" style={{ gap: 10 }}><History size={20} aria-hidden /><h2>Deployments</h2></div>
          <Button size="sm" variant="ghost" onClick={list.reload} loading={list.loading}><RefreshCw size={14} />Refresh</Button></div>
        <History_ list={list.data} loading={list.loading} onOpen={setOpen} current={d.current}
          onRollback={async (dep) => { try { await api.rollback(site.domain, dep.id); list.reload(); api.site(site.domain).then(onChange); toast("ok", `Back on ${dep.release}`); } catch (e) { toast("error", (e as Error).message); } }}
          onCancel={async (dep) => { await api.cancelDeployment(site.domain, dep.id); list.reload(); }} />
      </section>

      <DeploymentLog domain={site.domain} id={open} onClose={() => setOpen(null)} />
      <RefDialog open={refOpen} onClose={() => setRefOpen(false)} onDeploy={deployNow} busy={busy === "deploy"} />
      <Modal open={disconnect} onClose={() => setDisconnect(false)} title="Disconnect the repository?"
        description="The live release keeps serving. The deploy key, token and webhook secret are deleted, and pushes stop deploying."
        footer={<><Button onClick={() => setDisconnect(false)}>Cancel</Button><Button variant="danger" onClick={async () => {
          await api.disconnectDeploy(site.domain); setDisconnect(false); info.reload(); api.site(site.domain).then(onChange); toast("ok", "Disconnected");
        }}>Disconnect</Button></>}><p className="small">You can upload changes over SFTP afterwards, or connect again.</p></Modal>
    </div>
  );
}

function History_(props: { list: Deployment[] | null; loading: boolean; current?: string; onOpen: (id: string) => void;
  onRollback?: (d: Deployment) => void; onCancel?: (d: Deployment) => void }) {
  const list = props.list ?? [];
  if (props.loading && !props.list) return <div className="panel-body"><Skeleton /></div>;
  if (list.length === 0) return <Empty icon={<Rocket size={22} />} title="No deployments yet" text="Deploy now, or push to the branch once the webhook is set up." />;
  return (
    <div className="table-wrap"><table className="responsive">
      <thead><tr><th>Status</th><th>Change</th><th>When</th><th /></tr></thead>
      <tbody>{list.map((d) => (
        <tr key={d.id}>
          <td style={{ whiteSpace: "nowrap" }}><Badge tone={statusTone[d.status]}>{statusText[d.status]}</Badge></td>
          <td className="lead" style={{ minWidth: 0 }}>
            <button className="link" onClick={() => props.onOpen(d.id)} style={{ fontWeight: 500 }}>{d.message || (d.image ? `Image ${d.image}` : d.rollback_of ? `Back to ${d.rollback_of}` : `${d.ref_type} ${d.ref}`)}</button>
            <div className="small muted row" style={{ gap: 6 }}>
              {d.commit ? <><GitCommitHorizontal size={13} aria-hidden /><code>{d.commit.slice(0, 7)}</code></> : null}
              {d.author ? <span>{d.author}</span> : null}<span>{triggerText[d.trigger]}</span>
            </div>
            {d.error ? <div className="small" style={{ color: "var(--bad)", marginTop: 4 }}>{d.error}</div> : null}
          </td>
          <td className="small muted" style={{ whiteSpace: "nowrap" }}>{timeAgo(d.queued_at)}{duration(d) ? <div>{duration(d)}</div> : null}</td>
          <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
            {(d.status === "queued" || d.status === "building") && props.onCancel ? <Button size="sm" variant="ghost" onClick={() => props.onCancel!(d)}><Square size={13} />Cancel</Button> : null}
            {props.onRollback && d.release && d.release !== props.current && (d.status === "replaced" || d.status === "live") ? (
              <Button size="sm" variant="ghost" onClick={() => props.onRollback!(d)}><RotateCcw size={14} />Roll back</Button>) : null}
          </td>
        </tr>
      ))}</tbody>
    </table></div>
  );
}

function DeploymentLog({ domain, id, onClose }: { domain: string; id: string | null; onClose: () => void }) {
  const { saving } = useAppearance();
  const [data, setData] = useState<{ deployment: Deployment; log: string } | null>(null);
  useEffect(() => {
    if (!id) { setData(null); return; }
    let alive = true;
    let t: ReturnType<typeof setTimeout>;
    const tick = async () => {
      try {
        const r = await api.deployment(domain, id);
        if (!alive) return;
        setData(r);
        if (r.deployment.status === "queued" || r.deployment.status === "building") t = setTimeout(tick, saving ? 4000 : 1000);
      } catch { /* closed */ }
    };
    tick();
    return () => { alive = false; clearTimeout(t); };
  }, [domain, id, saving]);
  const d = data?.deployment;
  return (
    <Modal open={!!id} onClose={onClose} title={d ? (d.message || d.image || "Deployment") : "Deployment"}
      description={d ? <>{statusText[d.status]}{d.commit ? <> · <code>{d.commit.slice(0, 7)}</code></> : null}{d.release ? <> · release <code>{d.release}</code></> : null}</> : undefined}
      footer={<Button variant="primary" onClick={onClose}>Close</Button>}>
      {!data ? <Skeleton h={160} /> : (
        <div className="stack" style={{ gap: 10 }}>
          {d?.error ? <div className="callout bad"><CircleAlert size={18} />{d.error}</div> : null}
          <pre className="code" style={{ maxHeight: 380, overflow: "auto", margin: 0, whiteSpace: "pre-wrap", wordBreak: "break-word" }}>{data.log || "Waiting for a build slot…"}</pre>
        </div>
      )}
    </Modal>
  );
}

function RefDialog({ open, onClose, onDeploy, busy }: { open: boolean; onClose: () => void; onDeploy: (r: { ref_type: string; ref: string }) => void; busy: boolean }) {
  const [type, setType] = useState("branch");
  const [ref, setRef] = useState("");
  return (
    <Modal open={open} onClose={onClose} title="Deploy something else" description="One deployment only; the website keeps its usual branch for the next push."
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={busy} disabled={!ref.trim()} onClick={() => onDeploy({ ref_type: type, ref: ref.trim() })}>Deploy</Button></>}>
      <div className="form-grid">
        <Segmented label="Kind" value={type} onChange={setType} options={[{ value: "branch", label: "Branch" }, { value: "tag", label: "Tag" }, { value: "commit", label: "Commit" }]} />
        <Field label={type === "commit" ? "Commit hash" : type === "tag" ? "Tag" : "Branch"}>
          {(id) => <input id={id} className="input mono" value={ref} onChange={(e) => setRef(e.target.value)} placeholder={type === "commit" ? "9f3c2ab" : type === "tag" ? "v2.1.0" : "staging"} spellCheck={false} autoFocus />}
        </Field>
      </div>
    </Modal>
  );
}

function DeployForm({ site, current, hasToken, onCancel, onSaved }: { site: Site; current?: DeploySetup; hasToken: boolean; onCancel?: () => void; onSaved: (i: DeployInfo) => void }) {
  const container = site.runtime === "docker";
  const app = isApp(site.runtime);
  const files = site.runtime === "php" || site.runtime === "static";
  const [f, setF] = useState<DeploySetup>(current ?? {
    type: container ? "dockerfile" : "native", strategy: app || container ? "blue-green" : "restart",
    git: { url: "", ref_type: "branch", ref: "main", auth: "none", auto_deploy: true }, shared_paths: files || app ? [".env"] : [], keep_releases: 5, health_path: "/",
    container_port: container ? 8080 : undefined,
  });
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const g = f.git ?? { url: "", ref_type: "branch" as const, ref: "main", auth: "none" as const, auto_deploy: true };
  const setG = (patch: Partial<typeof g>) => setF({ ...f, git: { ...g, ...patch } });
  const fromImage = f.type === "image";
  const sshURL = /^[A-Za-z0-9._-]+@/.test(g.url);

  return (
    <section className="panel">
      <div className="panel-head"><div><h2>{current ? "Deployment settings" : "Deploy from Git"}</h2>
        <p className="small muted">{current ? "Changes apply to the next deployment." : "Connect a repository. Each deployment builds into its own release folder, then switches over, so a failed build never touches the live website."}</p></div></div>
      <form className="panel-body form-grid" onSubmit={async (e) => {
        e.preventDefault(); setBusy(true); setError(null);
        try {
          const body: DeploySetup = { ...f, git: fromImage ? undefined : { ...g, auth: g.auth }, token: token || undefined };
          onSaved(await api.configureDeploy(site.domain, body));
        } catch (err) { setError((err as Error).message); } finally { setBusy(false); }
      }}>
        {container ? (
          <div className="field"><label>Source</label>
            <Segmented label="Source" value={f.type} onChange={(v) => setF({ ...f, type: v })} options={[
              { value: "dockerfile", label: "Git + Dockerfile" }, { value: "buildpack", label: "Git + buildpacks" }, { value: "image", label: "Registry image" }]} />
            <p className="hint">{f.type === "buildpack" ? "No Dockerfile needed: buildpacks detect Node.js, Python, Go, Java, PHP and more." : f.type === "image" ? "Runs an image from Docker Hub, GitHub, GitLab or a private registry (add logins on the Containers page)." : "Builds the Dockerfile at the top of the repository (or the subfolder)."}</p>
          </div>
        ) : null}

        {fromImage ? (
          <Field label="Image">{(id) => <input id={id} className="input mono" value={f.image || ""} onChange={(e) => setF({ ...f, image: e.target.value.trim() })} placeholder="ghcr.io/owner/app:1.4" spellCheck={false} />}</Field>
        ) : (
          <>
            <Field label="Repository" hint="An https:// URL, or an SSH address like git@github.com:owner/repo.git for private repositories with a deploy key.">
              {(id) => <input id={id} className="input mono" value={g.url} onChange={(e) => {
                const url = e.target.value.trim();
                setG({ url, auth: /^[A-Za-z0-9._-]+@/.test(url) ? "deploy_key" : g.auth === "deploy_key" ? "none" : g.auth });
              }} placeholder="https://github.com/you/website.git" spellCheck={false} />}
            </Field>
            <div className="field"><label>Access</label>
              <Segmented label="Access" value={g.auth} onChange={(v) => setG({ auth: v })} options={sshURL
                ? [{ value: "deploy_key", label: "Deploy key" }]
                : [{ value: "none", label: "Public repository" }, { value: "token", label: "Access token" }]} />
              <p className="hint">{g.auth === "deploy_key" ? "Ike makes a key for this website; you add it to the repository as a read-only deploy key." : g.auth === "token" ? "A fine-grained GitHub token, a GitLab project token, or a Bitbucket access token with read access." : "No login needed."}</p>
            </div>
            {g.auth === "token" ? (
              <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
                <Field label="Token" hint={hasToken ? "Leave empty to keep the saved token." : "Stored readable by root only."}>{(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={token} onChange={(e) => setToken(e.target.value.trim())} />}</Field>
                <Field label="Username (optional)" hint="Usually not needed.">{(id) => <input id={id} className="input mono" value={g.username || ""} onChange={(e) => setG({ username: e.target.value.trim() || undefined })} />}</Field>
              </div>
            ) : null}
            <div className="grid-2" style={{ gridTemplateColumns: "minmax(0, auto) minmax(0, 1fr)", alignItems: "end" }}>
              <div className="field"><label>Deploy</label>
                <Segmented label="Ref type" value={g.ref_type} onChange={(v) => setG({ ref_type: v, ref: v === "branch" ? "main" : v === "tag" ? "v*" : "" })}
                  options={[{ value: "branch", label: "Branch" }, { value: "tag", label: "Tags" }, { value: "commit", label: "Commit" }]} /></div>
              <Field label={g.ref_type === "tag" ? "Tag or pattern" : g.ref_type === "commit" ? "Commit hash" : "Branch name"} hint={g.ref_type === "tag" ? "v* deploys the newest matching tag." : undefined}>
                {(id) => <input id={id} className="input mono" value={g.ref} onChange={(e) => setG({ ref: e.target.value.trim() })} spellCheck={false} />}
              </Field>
            </div>
            <Field label="Subfolder (optional)" hint="For monorepos: deploy only this folder.">{(id) => <input id={id} className="input mono" value={g.subdir || ""} onChange={(e) => setG({ subdir: e.target.value.trim() || undefined })} placeholder="apps/web" spellCheck={false} />}</Field>
            {g.ref_type !== "commit" ? <Toggle checked={g.auto_deploy} onChange={(v) => setG({ auto_deploy: v })} label={`Deploy automatically when ${g.ref_type === "tag" ? "a matching tag is pushed" : `${g.ref || "the branch"} gets a push`}`} /> : null}
          </>
        )}

        <details>
          <summary style={{ cursor: "pointer", fontWeight: 500 }}>Build and release</summary>
          <div className="form-grid" style={{ marginTop: 14 }}>
            {files ? <Field label="Public folder" hint="The folder the website serves, e.g. public for Laravel, dist or build for static generators. Empty: the repository root.">
              {(id) => <input id={id} className="input mono" value={f.public_dir || ""} onChange={(e) => setF({ ...f, public_dir: e.target.value.trim() || undefined })} placeholder="public" spellCheck={false} />}</Field> : null}
            {!fromImage && f.type !== "buildpack" && f.type !== "dockerfile" ? (
              <>
                <Field label="Build command" hint={<>Empty: detected from the files. {site.runtime === "php" ? "composer install, and npm run build if there's a package.json." : site.runtime === "go" ? "go build -o app ." : site.runtime === "java" ? "Maven or Gradle." : site.runtime === "python" ? "a virtualenv and pip install." : site.runtime === "dart" ? "dart pub get." : "npm ci (or yarn/pnpm) and npm run build."} Runs as {site.account}, sandboxed.</>}>
                  {(id) => <input id={id} className="input mono" value={f.build_command || ""} onChange={(e) => setF({ ...f, build_command: e.target.value || undefined })} placeholder="npm ci && npm run build" spellCheck={false} />}</Field>
                <Field label="Release command (optional)" hint="Runs in the new release before it goes live, e.g. database migrations.">
                  {(id) => <input id={id} className="input mono" value={f.release_command || ""} onChange={(e) => setF({ ...f, release_command: e.target.value || undefined })} placeholder="php artisan migrate --force" spellCheck={false} />}</Field>
                <Field label="Shared between releases" hint="Files and folders kept from one release to the next, comma-separated. .env is the file the Env vars tab edits.">
                  {(id) => <input id={id} className="input mono" value={(f.shared_paths ?? []).join(", ")} onChange={(e) => setF({ ...f, shared_paths: e.target.value.split(",").map((x) => x.trim()).filter(Boolean) })} placeholder=".env, storage, public/uploads" spellCheck={false} />}</Field>
              </>
            ) : null}
            {container ? <Field label="Container port" hint="The port the app listens on inside the container.">{(id) => <input id={id} className="input mono" style={{ maxWidth: 120 }} inputMode="numeric" value={f.container_port || ""} onChange={(e) => setF({ ...f, container_port: Number(e.target.value.replace(/\D/g, "")) || undefined })} />}</Field> : null}
            {app ? (
              <div className="field"><label>Switching</label>
                <Segmented label="Strategy" value={f.strategy || "restart"} onChange={(v) => setF({ ...f, strategy: v })} options={[{ value: "blue-green", label: "Zero downtime" }, { value: "restart", label: "Restart" }]} />
                <p className="hint">{f.strategy === "blue-green" ? "The new release starts next to the old one and gets traffic only after it answers. Uses a second port and twice the memory for a moment." : "The app restarts on the new release; visitors may see an error for a few seconds."}</p>
              </div>
            ) : null}
            {app || container ? <Field label="Health check path" hint="Must answer without a server error before traffic moves.">{(id) => <input id={id} className="input mono" style={{ maxWidth: 240 }} value={f.health_path || "/"} onChange={(e) => setF({ ...f, health_path: e.target.value })} />}</Field> : null}
            <Field label="Releases to keep" hint="For rollbacks. Older ones are deleted.">{(id) => <input id={id} className="input mono" style={{ maxWidth: 100 }} inputMode="numeric" value={f.keep_releases || 5} onChange={(e) => setF({ ...f, keep_releases: Number(e.target.value.replace(/\D/g, "")) || 5 })} />}</Field>
            <Field label="Notify a URL after each deployment (optional)" hint="Ike POSTs JSON signed with the webhook secret (X-Ike-Signature). n8n, Zapier or your own endpoint.">
              {(id) => <input id={id} className="input mono" value={f.notify_url || ""} onChange={(e) => setF({ ...f, notify_url: e.target.value.trim() || undefined })} placeholder="https://hooks.example.com/ike" spellCheck={false} />}</Field>
            {!fromImage ? (
              <>
                <Toggle checked={!!f.previews?.enabled} onChange={(v) => setF({ ...f, previews: { enabled: v, base_domain: f.previews?.base_domain || `preview.${site.domain}` } })} label="Preview every pull request at its own address" />
                {f.previews?.enabled ? <Field label="Preview domain" hint={<>Pull request 12 opens at <code>pr-12.{f.previews.base_domain}</code>. Point a <code>*.{f.previews.base_domain}</code> DNS record at this server. Previews never get the live .env, and pull requests from forks are never built.</>}>
                  {(id) => <input id={id} className="input mono" value={f.previews!.base_domain} onChange={(e) => setF({ ...f, previews: { enabled: true, base_domain: e.target.value.trim() } })} spellCheck={false} />}</Field> : null}
              </>
            ) : null}
          </div>
        </details>
        {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
        <div className="row"><Button variant="primary" type="submit" loading={busy}>{current ? "Save settings" : "Connect"}</Button>{onCancel ? <Button type="button" onClick={onCancel}>Cancel</Button> : null}</div>
      </form>
    </section>
  );
}
