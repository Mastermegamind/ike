// Containers, Compose stacks, the Docker host, Kubernetes, PHP extensions,
// certificates and the build queue.
import { useState } from "react";
import {
  Box, CircleAlert, Cloud, FileCode2, Layers, Lock, Play, Plus, RefreshCw, RotateCw, Rocket, Ship, Square, SquareTerminal, Trash2,
} from "lucide-react";
import { api } from "../api/client";
import type { Container, ContainerSpec, Role, StackInfo } from "../api/types";
import { Badge, Button, CopyBlock, Empty, Field, Modal, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { useAccountParam } from "./AccountTools";
import { href } from "../router";

const stateTone = (s: string) => (s === "running" ? "ok" : s === "restarting" ? "warn" : s === "exited" || s === "dead" ? "bad" : undefined);

/* ============================================================ Containers */

type CTab = "containers" | "stacks" | "volumes" | "registries";

export function Containers({ route }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const rt = useAsync(() => api.runtimes());
  const [tab, setTab] = useState<CTab>("containers");
  if (rt.data && !rt.data.docker) {
    return (
      <div className="stack">
        <PageHead title="Containers" />
        <section className="panel"><Empty icon={<Box size={22} />} title="Docker isn't installed" text={<>The server admin can add it with <code>install.sh --docker</code>, and let account owners use it with <code>--docker-for-accounts</code>.</>} /></section>
      </div>
    );
  }
  return (
    <div className="stack">
      <PageHead title="Containers" text="Run Docker images and Compose stacks inside the account. They share its CPU and memory limits, and their ports stay on this server until you put a website in front."
        actions={picker} />
      <Tabs<CTab> value={tab} onChange={setTab} tabs={[{ value: "containers", label: "Containers" }, { value: "stacks", label: "Compose stacks" }, { value: "volumes", label: "Volumes" }, { value: "registries", label: "Registry logins" }]} />
      {account && tab === "containers" && <ContainerList account={account} />}
      {account && tab === "stacks" && <Stacks account={account} />}
      {account && tab === "volumes" && <Volumes account={account} />}
      {account && tab === "registries" && <Registries account={account} />}
    </div>
  );
}

function ContainerList({ account }: { account: string }) {
  const toast = useToast();
  const list = useAsync(() => api.containers(account), [account]);
  const [creating, setCreating] = useState(false);
  const [logs, setLogs] = useState<{ name: string; text: string } | null>(null);
  const [shell, setShell] = useState<string | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const act = async (c: Container, a: "start" | "stop" | "restart") => {
    try { await api.containerAction(account, c.short, a); list.reload(); toast("ok", `${c.short}: ${a === "stop" ? "stopped" : a === "start" ? "started" : "restarted"}`); }
    catch (e) { toast("error", (e as Error).message); }
  };
  const own = (c: Container) => !c.site && !c.stack;
  return (
    <section className="panel">
      <div className="panel-head"><h2>Containers in {account}</h2><Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />Run a container</Button></div>
      {list.loading && !list.data ? <div className="panel-body"><Skeleton /></div> : (list.data ?? []).length === 0 ? (
        <Empty icon={<Box size={22} />} title="No containers yet" text="Run Redis, a search engine, or any image. To publish one on a domain, create a website with the Forward to a local port runtime." />
      ) : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Container</th><th>Image</th><th>State</th><th>Ports</th><th /></tr></thead>
          <tbody>{(list.data ?? []).map((c) => (
            <tr key={c.name}>
              <td className="lead"><div className="primary-cell">{own(c) ? c.short : c.name}</div>
                <div className="small muted">{c.site ? <>Website <a href={href(`/sites/${c.site}`)}>{c.site}</a></> : c.stack ? <>Stack {c.stack.replace(`ike-${account}-`, "")}</> : `Created ${timeAgo(c.created)}`}</div></td>
              <td className="mono small" data-hide-sm>{c.image}</td>
              <td><Badge tone={stateTone(c.state)}>{c.state}</Badge><div className="small muted">{c.status}</div></td>
              <td className="mono small" data-hide-sm>{c.ports || "None"}</td>
              <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                {own(c) ? <>
                  {c.state === "running"
                    ? <><Button size="sm" variant="ghost" icon aria-label={`Restart ${c.short}`} onClick={() => act(c, "restart")}><RotateCw size={15} /></Button>
                      <Button size="sm" variant="ghost" icon aria-label={`Stop ${c.short}`} onClick={() => act(c, "stop")}><Square size={14} /></Button>
                      <Button size="sm" variant="ghost" icon aria-label={`Run a command in ${c.short}`} onClick={() => setShell(c.short)}><SquareTerminal size={15} /></Button></>
                    : <Button size="sm" variant="ghost" icon aria-label={`Start ${c.short}`} onClick={() => act(c, "start")}><Play size={14} /></Button>}
                  <Button size="sm" variant="ghost" onClick={async () => setLogs({ name: c.short, text: await api.containerLogs(account, c.short) })}>Logs</Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Remove ${c.short}`} onClick={() => setDel(c.short)}><Trash2 size={15} /></Button>
                </> : <span className="small muted">Managed by its {c.site ? "website" : "stack"}</span>}
              </td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <NewContainer account={account} open={creating} onClose={() => setCreating(false)} onDone={() => { setCreating(false); list.reload(); }} />
      <Modal open={!!logs} onClose={() => setLogs(null)} title={`Logs: ${logs?.name ?? ""}`} footer={<Button variant="primary" onClick={() => setLogs(null)}>Close</Button>}>
        <pre className="code" style={{ maxHeight: 380, overflow: "auto", margin: 0, whiteSpace: "pre-wrap" }}>{logs?.text || "No output yet."}</pre>
      </Modal>
      <ExecDialog account={account} name={shell} onClose={() => setShell(null)} />
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Remove ${del}?`} name={del || ""} confirmLabel="Remove container"
        description="The container is deleted. Named volumes keep their data; anything stored only inside the container is lost."
        onConfirm={async () => { await api.containerAction(account, del!, "remove"); list.reload(); toast("ok", `Removed ${del}`); }} />
    </section>
  );
}

function ExecDialog({ account, name, onClose }: { account: string; name: string | null; onClose: () => void }) {
  const [cmd, setCmd] = useState("");
  const [out, setOut] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Modal open={!!name} onClose={() => { setOut(""); setCmd(""); onClose(); }} title={`Run a command in ${name ?? ""}`} description="Runs once with sh -c and shows the output. For an interactive shell, use the Terminal page with docker exec."
      footer={<Button onClick={onClose}>Close</Button>}>
      <form className="form-grid" onSubmit={async (e) => {
        e.preventDefault(); setBusy(true);
        try { setOut(await api.containerExec(account, name!, cmd)); } catch (err) { setOut((err as Error).message); } finally { setBusy(false); }
      }}>
        <div className="row"><input className="input mono" style={{ flex: 1 }} aria-label="Command" value={cmd} onChange={(e) => setCmd(e.target.value)} placeholder="redis-cli ping" autoFocus spellCheck={false} />
          <Button type="submit" variant="primary" loading={busy} disabled={!cmd.trim()}>Run</Button></div>
        {out ? <pre className="code" style={{ maxHeight: 300, overflow: "auto", margin: 0, whiteSpace: "pre-wrap" }}>{out}</pre> : null}
      </form>
    </Modal>
  );
}

function NewContainer({ account, open, onClose, onDone }: { account: string; open: boolean; onClose: () => void; onDone: () => void }) {
  const toast = useToast();
  const [f, setF] = useState({ name: "", image: "", cport: "", hport: "", volume: "", target: "", env: "", memory: "", cpus: "" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const presets = [
    { label: "Redis", name: "redis", image: "redis:7.4-alpine", cport: "6379", volume: "redis-data", target: "/data" },
    { label: "Valkey", name: "valkey", image: "valkey/valkey:8", cport: "6379", volume: "valkey-data", target: "/data" },
    { label: "Meilisearch", name: "search", image: "getmeili/meilisearch:v1.15", cport: "7700", volume: "meili-data", target: "/meili_data" },
    { label: "Uptime Kuma", name: "uptime", image: "louislam/uptime-kuma:1", cport: "3001", volume: "kuma", target: "/app/data" },
  ];
  return (
    <Modal open={open} onClose={onClose} title="Run a container" description={`Runs in ${account}'s network and limits. Ports listen on 127.0.0.1 only.`}
      footer={<><Button onClick={onClose}>Cancel</Button><Button variant="primary" loading={busy} disabled={!f.name || !f.image} onClick={async () => {
        setBusy(true); setError(null);
        const spec: ContainerSpec = { name: f.name, image: f.image,
          ports: f.cport ? [{ container_port: Number(f.cport), host_port: f.hport ? Number(f.hport) : undefined }] : [],
          volumes: f.volume && f.target ? [{ volume: f.volume, target: f.target }] : [],
          env: f.env.split("\n").map((l) => l.trim()).filter((l) => l.includes("=")).map((l) => ({ key: l.split("=")[0], value: l.slice(l.indexOf("=") + 1) })),
          memory_mb: f.memory ? Number(f.memory) : undefined, cpus: f.cpus ? Number(f.cpus) : undefined };
        try { await api.createContainer(account, spec); toast("ok", `${f.name} is running`); onDone(); setF({ name: "", image: "", cport: "", hport: "", volume: "", target: "", env: "", memory: "", cpus: "" }); }
        catch (e) { setError((e as Error).message); } finally { setBusy(false); }
      }}>Run container</Button></>}>
      <div className="form-grid">
        <div className="row">{presets.map((p) => <button key={p.label} type="button" className="badge" style={{ cursor: "pointer", border: 0 }} onClick={() => setF({ ...f, ...p })}>{p.label}</button>)}</div>
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 1.4fr" }}>
          <Field label="Name">{(id) => <input id={id} className="input mono" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value.toLowerCase() })} placeholder="redis" />}</Field>
          <Field label="Image">{(id) => <input id={id} className="input mono" value={f.image} onChange={(e) => setF({ ...f, image: e.target.value.trim() })} placeholder="redis:7.4-alpine" spellCheck={false} />}</Field>
        </div>
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 1fr" }}>
          <Field label="Container port">{(id) => <input id={id} className="input mono" inputMode="numeric" value={f.cport} onChange={(e) => setF({ ...f, cport: e.target.value.replace(/\D/g, "") })} placeholder="6379" />}</Field>
          <Field label="Port on this server" hint="Empty: any free port.">{(id) => <input id={id} className="input mono" inputMode="numeric" value={f.hport} onChange={(e) => setF({ ...f, hport: e.target.value.replace(/\D/g, "") })} placeholder="16379" />}</Field>
        </div>
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 1fr" }}>
          <Field label="Volume (keeps data)">{(id) => <input id={id} className="input mono" value={f.volume} onChange={(e) => setF({ ...f, volume: e.target.value.toLowerCase() })} placeholder="redis-data" />}</Field>
          <Field label="Mounted at">{(id) => <input id={id} className="input mono" value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })} placeholder="/data" />}</Field>
        </div>
        <Field label="Environment (one KEY=value per line)">{(id) => <textarea id={id} className="input mono" rows={3} value={f.env} onChange={(e) => setF({ ...f, env: e.target.value })} placeholder="TZ=Africa/Lagos" spellCheck={false} />}</Field>
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 1fr" }}>
          <Field label="Memory limit (MB, optional)">{(id) => <input id={id} className="input mono" inputMode="numeric" value={f.memory} onChange={(e) => setF({ ...f, memory: e.target.value.replace(/\D/g, "") })} />}</Field>
          <Field label="CPU limit (cores, optional)">{(id) => <input id={id} className="input mono" inputMode="decimal" value={f.cpus} onChange={(e) => setF({ ...f, cpus: e.target.value.replace(/[^\d.]/g, "") })} placeholder="0.5" />}</Field>
        </div>
        {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
      </div>
    </Modal>
  );
}

const sampleCompose = `services:
  app:
    image: ghost:5
    ports: ["2368:2368"]
    environment:
      url: https://blog.example.com
      database__client: sqlite3
    volumes: ["ghost:/var/lib/ghost/content"]
volumes:
  ghost: {}
`;

function Stacks({ account }: { account: string }) {
  const toast = useToast();
  const list = useAsync(() => api.stacks(account), [account]);
  const [edit, setEdit] = useState<{ name: string; compose: string; isNew: boolean } | null>(null);
  const [open, setOpen] = useState<StackInfo | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const [volumes, setVolumes] = useState(false);
  const { job, start, clear } = useJob(() => list.reload());
  const run = async (name: string, action: "up" | "down" | "restart" | "pull") => {
    try { start(await api.stackAction(account, name, action)); } catch (e) { toast("error", (e as Error).message); }
  };
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Compose stacks</h2><p className="small muted">Paste a compose.yaml. Ike checks it first: no privileged containers, host networking, or folders outside the account's home.</p></div>
        <Button variant="primary" onClick={() => setEdit({ name: "", compose: sampleCompose, isNew: true })}><Plus size={17} />New stack</Button></div>
      {list.loading && !list.data ? <div className="panel-body"><Skeleton /></div> : (list.data ?? []).length === 0 ? (
        <Empty icon={<Layers size={22} />} title="No stacks" text="Run multi-container apps like n8n with Postgres, Ghost, or Plausible from one compose file." />
      ) : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Stack</th><th>Status</th><th /></tr></thead>
          <tbody>{(list.data ?? []).map((s) => (
            <tr key={s.name}>
              <td className="lead"><button className="link" style={{ fontWeight: 600 }} onClick={async () => setOpen(await api.stack(account, s.name))}>{s.name}</button></td>
              <td><Badge tone={s.status.startsWith("running") ? "ok" : undefined}>{s.status}</Badge></td>
              <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                <Button size="sm" variant="ghost" onClick={async () => { const d = await api.stack(account, s.name); setEdit({ name: s.name, compose: d.compose || "", isNew: false }); }}><FileCode2 size={14} />Edit</Button>
                <Button size="sm" variant="ghost" onClick={() => run(s.name, "pull")}><RefreshCw size={14} />Update images</Button>
                {s.status.startsWith("running") ? <Button size="sm" variant="ghost" onClick={() => run(s.name, "down")}><Square size={13} />Stop</Button> : <Button size="sm" variant="ghost" onClick={() => run(s.name, "up")}><Play size={13} />Start</Button>}
                <Button size="sm" variant="ghost" icon aria-label={`Delete ${s.name}`} onClick={() => setDel(s.name)}><Trash2 size={15} /></Button>
              </td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.isNew ? "New Compose stack" : `Edit ${edit?.name}`}
        description="Saving checks the file and starts (or updates) the stack. Published ports stay on 127.0.0.1."
        footer={<><Button onClick={() => setEdit(null)}>Cancel</Button><Button variant="primary" disabled={!edit?.name || !edit?.compose.trim()} onClick={async () => {
          try { start(await api.saveStack(account, edit!.name, edit!.compose)); setEdit(null); } catch (e) { toast("error", (e as Error).message); }
        }}>Save and start</Button></>}>
        {edit ? <div className="form-grid">
          {edit.isNew ? <Field label="Stack name">{(id) => <input id={id} className="input mono" value={edit.name} onChange={(e) => setEdit({ ...edit, name: e.target.value.toLowerCase() })} placeholder="blog" />}</Field> : null}
          <Field label="compose.yaml" hint="Values like ${PASSWORD} come from the stack's .env in ~/stacks/<name>/.">{(id) => <textarea id={id} className="input mono" rows={16} value={edit.compose} onChange={(e) => setEdit({ ...edit, compose: e.target.value })} spellCheck={false} />}</Field>
        </div> : null}
      </Modal>
      <Modal open={!!open} onClose={() => setOpen(null)} title={`Stack ${open?.name ?? ""}`} footer={<Button variant="primary" onClick={() => setOpen(null)}>Close</Button>}>
        {open ? <div className="stack" style={{ gap: 10 }}>
          {(open.services ?? []).map((s) => (
            <div key={s.name} className="row" style={{ justifyContent: "space-between", border: "1px solid var(--line)", borderRadius: "var(--r-sm)", padding: "8px 12px" }}>
              <div><strong>{s.service}</strong><div className="small muted mono">{s.image}</div></div>
              <div style={{ textAlign: "right" }}><Badge tone={stateTone(s.state)}>{s.state}</Badge><div className="small muted mono">{s.ports || ""}</div></div>
            </div>
          ))}
          {open.compose ? <CopyBlock value={open.compose} /> : null}
        </div> : null}
      </Modal>
      <JobModal job={job} onClose={clear} />
      <ConfirmDelete open={!!del} onClose={() => { setDel(null); setVolumes(false); }} title={`Delete stack ${del}?`} name={del || ""} confirmLabel="Delete stack"
        description="Its containers and network are removed."
        extra={<label className="row" style={{ gap: 8 }}><input type="checkbox" checked={volumes} onChange={(e) => setVolumes(e.target.checked)} />Also delete its volumes and their data</label>}
        onConfirm={async () => { await api.deleteStack(account, del!, volumes); list.reload(); toast("ok", `Deleted ${del}`); }} />
    </section>
  );
}

function Volumes({ account }: { account: string }) {
  const toast = useToast();
  const list = useAsync(() => api.volumes(account), [account]);
  const [name, setName] = useState("");
  const [del, setDel] = useState<string | null>(null);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Volumes</h2><p className="small muted">Storage that outlives containers. Back them up before deleting.</p></div></div>
      <form className="panel-body row" onSubmit={async (e) => { e.preventDefault(); try { await api.createVolume(account, name); setName(""); list.reload(); } catch (err) { toast("error", (err as Error).message); } }}>
        <input className="input mono" style={{ maxWidth: 260 }} aria-label="Volume name" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="uploads" />
        <Button type="submit" disabled={!name}><Plus size={16} />Create volume</Button>
      </form>
      {(list.data ?? []).length === 0 ? <div className="panel-body muted small" style={{ paddingTop: 0 }}>No volumes yet.</div> : (
        <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>{(list.data ?? []).map((v) => (
          <li key={v.name} className="row" style={{ justifyContent: "space-between", padding: "10px 20px", borderTop: "1px solid var(--line)" }}>
            <span><strong>{v.short}</strong> <span className="small muted mono">{v.name}</span></span>
            <Button size="sm" variant="ghost" icon aria-label={`Delete ${v.short}`} onClick={() => setDel(v.short)}><Trash2 size={15} /></Button>
          </li>))}</ul>
      )}
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete volume ${del}?`} name={del || ""} confirmLabel="Delete volume"
        description="Everything stored in it is deleted. Containers using it must be removed first."
        onConfirm={async () => { await api.removeVolume(account, del!); list.reload(); }} />
    </section>
  );
}

function Registries({ account }: { account: string }) {
  const toast = useToast();
  const list = useAsync(() => api.registries(account), [account]);
  const [f, setF] = useState({ registry: "ghcr.io", username: "", password: "" });
  const [busy, setBusy] = useState(false);
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Private registry logins</h2><p className="small muted">Used when this account pulls images: containers, stacks and image deployments. Stored readable by root only.</p></div></div>
      <form className="panel-body grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr)) auto", alignItems: "end" }} onSubmit={async (e) => {
        e.preventDefault(); setBusy(true);
        try { await api.addRegistry(account, f); setF({ ...f, username: "", password: "" }); list.reload(); toast("ok", `Logged in to ${f.registry}`); } catch (err) { toast("error", (err as Error).message); } finally { setBusy(false); }
      }}>
        <Field label="Registry">{(id) => <input id={id} className="input mono" value={f.registry} onChange={(e) => setF({ ...f, registry: e.target.value.trim() })} />}</Field>
        <Field label="Username">{(id) => <input id={id} className="input mono" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value.trim() })} />}</Field>
        <Field label="Password or token">{(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} />}</Field>
        <div style={{ paddingBottom: 2 }}><Button type="submit" variant="primary" loading={busy} disabled={!f.username || !f.password}>Log in</Button></div>
      </form>
      <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>{(list.data ?? []).map((r) => (
        <li key={r} className="row" style={{ justifyContent: "space-between", padding: "10px 20px", borderTop: "1px solid var(--line)" }}>
          <span className="mono">{r}</span>
          <Button size="sm" variant="ghost" onClick={async () => { await api.removeRegistry(account, r); list.reload(); }}>Log out</Button>
        </li>))}</ul>
    </section>
  );
}

/* ================================================================ Docker */

export function DockerHost() {
  const toast = useToast();
  const o = useAsync(() => api.dockerOverview());
  const all = useAsync(() => api.allContainers());
  const ups = useAsync(() => api.imageUpdates());
  const [pruning, setPruning] = useState(false);
  const { job, start, clear } = useJob(() => ups.reload());
  if (o.data && !o.data.installed) {
    return <div className="stack"><PageHead title="Docker" /><section className="panel"><Empty icon={<Ship size={22} />} title="Docker isn't installed" text={<>Run <code>install.sh --docker</code> on the server to add Docker Engine, Compose and Buildx.</>} /></section></div>;
  }
  const updates = (ups.data ?? []).filter((u) => u.update_available);
  return (
    <div className="stack">
      <PageHead title="Docker" text={o.data ? `Docker ${o.data.version} · ${o.data.running} of ${o.data.containers} containers running · ${o.data.for_accounts ? "account owners can run containers" : "admins only"}` : "Loading…"}
        actions={<><Button onClick={async () => { try { start(await api.checkImageUpdates()); } catch (e) { toast("error", (e as Error).message); } }}><RefreshCw size={16} />Check for image updates</Button>
          <Button loading={pruning} onClick={async () => { setPruning(true); try { toast("ok", (await api.pruneDocker()).split("\n").pop() || "Cleaned up"); o.reload(); } finally { setPruning(false); } }}><Trash2 size={16} />Clean up</Button></>} />
      {updates.length ? (
        <div className="callout info"><CircleAlert size={18} /><div><strong>{updates.length} image {updates.length === 1 ? "update" : "updates"} available.</strong> {updates.map((u) => u.image).join(", ")}. Use “Update images” on the stack, or redeploy the website.</div></div>
      ) : null}
      <section className="panel">
        <div className="panel-head"><h2>All containers</h2></div>
        {!all.data ? <div className="panel-body"><Skeleton /></div> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Container</th><th>Account</th><th>Image</th><th>State</th></tr></thead>
            <tbody>{all.data.map((c) => (
              <tr key={c.name}><td className="lead mono small">{c.name}</td><td>{c.account}</td><td className="mono small" data-hide-sm>{c.image}</td>
                <td><Badge tone={stateTone(c.state)}>{c.state}</Badge></td></tr>))}</tbody>
          </table></div>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Images</h2><p className="small muted">Clean up removes stopped containers, unused networks, dangling images and build cache. Volumes are never touched.</p></div></div>
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Image</th><th>Size</th><th>Created</th><th>Update</th></tr></thead>
          <tbody>{(o.data?.images ?? []).map((im) => {
            const u = (ups.data ?? []).find((x) => x.image === `${im.repository}:${im.tag}`);
            return <tr key={im.id}><td className="lead mono small">{im.repository}:{im.tag}</td><td className="small">{im.size}</td><td className="small muted" data-hide-sm>{im.created}</td>
              <td>{u?.update_available ? <Badge tone="accent">Newer version</Badge> : u ? <span className="small muted">Up to date</span> : null}</td></tr>;
          })}</tbody>
        </table></div>
      </section>
      <BuildQueue />
      <JobModal job={job} onClose={clear} />
    </div>
  );
}

function BuildQueue() {
  const toast = useToast();
  const q = useAsync(() => api.deployQueue());
  const [max, setMax] = useState<number | null>(null);
  const cur = max ?? q.data?.settings.max_concurrent ?? 1;
  return (
    <section className="panel">
      <div className="panel-head"><div className="row" style={{ gap: 10 }}><Rocket size={20} aria-hidden /><div><h2>Build queue</h2>
        <p className="small muted">Deployments wait here when the server is busy. Each website builds one release at a time; a newer push replaces one still waiting.</p></div></div></div>
      <div className="panel-body form-grid">
        <div className="row" style={{ alignItems: "flex-end" }}>
          <Field label="Builds at once" hint="Each build can use up to 2 GB of memory. 1 is safest on small servers.">
            {(id) => <select id={id} className="select" style={{ width: "auto" }} value={cur} onChange={(e) => setMax(Number(e.target.value))}>{[1, 2, 3, 4, 6, 8].map((n) => <option key={n}>{n}</option>)}</select>}
          </Field>
          <div style={{ paddingBottom: 22 }}><Button disabled={max === null} onClick={async () => { await api.setDeploySettings(cur); setMax(null); q.reload(); toast("ok", "Saved"); }}>Save</Button></div>
        </div>
        {q.data && q.data.running.length + q.data.queued.length === 0 ? <p className="small muted">Nothing building right now.</p> : null}
        {[...(q.data?.running ?? []), ...(q.data?.queued ?? [])].map((d) => (
          <div key={d.id} className="row" style={{ justifyContent: "space-between" }}>
            <span><a href={href(`/sites/${d.domain}`)}>{d.domain}</a> <span className="small muted">{d.message || d.ref}</span></span>
            <Badge tone="accent">{d.status === "building" ? "Building" : "Waiting"}</Badge>
          </div>
        ))}
      </div>
    </section>
  );
}

/* ============================================================ Kubernetes */

export function Kubernetes() {
  const toast = useToast();
  const st = useAsync(() => api.kubeStatus());
  const wl = useAsync(() => (st.data?.installed ? api.kubeWorkloads() : Promise.resolve(null)), [st.data?.installed]);
  const [manifest, setManifest] = useState("");
  const [out, setOut] = useState("");
  const [busy, setBusy] = useState(false);
  const [scale, setScale] = useState<{ kind: string; ns: string; name: string; replicas: string } | null>(null);
  if (st.data && !st.data.installed) {
    return <div className="stack"><PageHead title="Kubernetes" /><section className="panel"><Empty icon={<Cloud size={22} />} title="Kubernetes isn't installed" text={<>Optional. Run <code>install.sh --k3s</code> for a single-node k3s cluster. Nginx keeps ports 80 and 443; publish services with a website that forwards to their node port.</>} /></section></div>;
  }
  const act = async (kind: string, ns: string, name: string, action: "restart" | "delete") => {
    try { await api.kubeAction(kind.toLowerCase(), ns, name, action); toast("ok", `${name}: ${action === "restart" ? "restarting" : "deleted"}`); wl.reload(); }
    catch (e) { toast("error", (e as Error).message); }
  };
  return (
    <div className="stack">
      <PageHead title="Kubernetes" text={st.data ? `k3s ${st.data.nodes[0]?.version ?? ""} · ${st.data.nodes.filter((n) => n.ready).length} of ${st.data.nodes.length} nodes ready · ${st.data.namespaces.length} namespaces` : "Loading…"}
        actions={<Button onClick={() => { st.reload(); wl.reload(); }}><RefreshCw size={16} />Refresh</Button>} />
      <section className="panel">
        <div className="panel-head"><h2>Workloads</h2></div>
        {!wl.data ? <div className="panel-body"><Skeleton /></div> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Workload</th><th>Ready</th><th>Image</th><th /></tr></thead>
            <tbody>{wl.data.workloads.map((w) => (
              <tr key={w.kind + w.namespace + w.name}>
                <td className="lead"><div className="primary-cell">{w.name}</div><div className="small muted">{w.kind} in {w.namespace}</div></td>
                <td><Badge tone={w.ready >= w.desired ? "ok" : "warn"}>{w.ready}/{w.desired}</Badge></td>
                <td className="mono small" data-hide-sm>{w.images.join(", ")}</td>
                <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                  {w.kind !== "DaemonSet" ? <Button size="sm" variant="ghost" onClick={() => setScale({ kind: w.kind.toLowerCase(), ns: w.namespace, name: w.name, replicas: String(w.desired) })}>Scale</Button> : null}
                  <Button size="sm" variant="ghost" onClick={() => act(w.kind, w.namespace, w.name, "restart")}><RotateCw size={14} />Restart</Button>
                </td>
              </tr>))}</tbody>
          </table></div>
        )}
      </section>
      <div className="grid-2">
        <section className="panel">
          <div className="panel-head"><h2>Pods</h2></div>
          <div className="table-wrap"><table className="responsive"><tbody>{(wl.data?.pods ?? []).map((p) => (
            <tr key={p.namespace + p.name}><td className="lead mono small">{p.name}<div className="small muted">{p.namespace}</div></td>
              <td><Badge tone={p.phase === "Running" ? "ok" : p.phase === "Pending" ? "warn" : "bad"}>{p.phase}</Badge></td>
              <td className="small muted">{p.restarts ? `${p.restarts} restarts` : ""}</td></tr>))}</tbody></table></div>
        </section>
        <section className="panel">
          <div className="panel-head"><h2>Services</h2></div>
          <div className="panel-body stack" style={{ gap: 8 }}>
            {(wl.data?.services ?? []).map((s) => <div key={s.namespace + s.name}><strong>{s.name}</strong> <span className="small muted">{s.type} in {s.namespace}</span><div className="small mono">{s.ports.join(", ")}</div></div>)}
            <p className="small muted">To publish a NodePort service on a domain, create a website with the “Forward to a local port” runtime and its node port.</p>
          </div>
        </section>
      </div>
      <section className="panel">
        <div className="panel-head"><div><h2>Apply a manifest</h2><p className="small muted">YAML or JSON, like kubectl apply -f.</p></div></div>
        <div className="panel-body form-grid">
          <textarea className="input mono" rows={10} aria-label="Manifest" value={manifest} onChange={(e) => setManifest(e.target.value)} placeholder={"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: shop"} spellCheck={false} />
          <div><Button variant="primary" loading={busy} disabled={!manifest.trim()} onClick={async () => {
            setBusy(true); try { setOut(await api.kubeApply(manifest)); wl.reload(); } catch (e) { setOut((e as Error).message); } finally { setBusy(false); }
          }}>Apply</Button></div>
          {out ? <pre className="code" style={{ margin: 0 }}>{out}</pre> : null}
        </div>
      </section>
      <Modal open={!!scale} onClose={() => setScale(null)} title={`Scale ${scale?.name ?? ""}`}
        footer={<><Button onClick={() => setScale(null)}>Cancel</Button><Button variant="primary" onClick={async () => {
          try { await api.kubeAction(scale!.kind, scale!.ns, scale!.name, "scale", Number(scale!.replicas)); setScale(null); wl.reload(); toast("ok", "Scaled"); } catch (e) { toast("error", (e as Error).message); }
        }}>Scale</Button></>}>
        {scale ? <Field label="Replicas">{(id) => <input id={id} className="input mono" inputMode="numeric" style={{ maxWidth: 120 }} value={scale.replicas} onChange={(e) => setScale({ ...scale, replicas: e.target.value.replace(/\D/g, "") })} />}</Field> : null}
      </Modal>
    </div>
  );
}

/* ======================================================== PHP extensions */

export function PHPExtensions() {
  const toast = useToast();
  const rt = useAsync(() => api.runtimes());
  const [version, setVersion] = useState("");
  const [filter, setFilter] = useState("");
  const v = version || rt.data?.default_php || "";
  const list = useAsync(() => (v ? api.phpExtensions(v) : Promise.resolve([])), [v]);
  const { job, start, clear } = useJob(() => list.reload());
  const shown = (list.data ?? []).filter((e) => !filter || e.name.includes(filter.toLowerCase()) || e.description.toLowerCase().includes(filter.toLowerCase()));
  return (
    <div className="stack">
      <PageHead title="PHP extensions" text="Extensions are installed per PHP version from Ubuntu or the ondrej/php repository, then PHP reloads. Websites keep running."
        actions={<select className="select" style={{ width: "auto" }} aria-label="PHP version" value={v} onChange={(e) => setVersion(e.target.value)}>
          {(rt.data?.php ?? []).slice().reverse().map((x) => <option key={x} value={x}>PHP {x}</option>)}</select>} />
      <section className="panel">
        <div className="panel-body"><input className="input" aria-label="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter: redis, image, intl…" style={{ maxWidth: 320 }} /></div>
        {!list.data ? <div className="panel-body"><Skeleton /></div> : (
          <div className="table-wrap"><table className="responsive">
            <tbody>{shown.map((e) => (
              <tr key={e.name}>
                <td className="lead"><div className="primary-cell mono">{e.name}</div><div className="small muted">{e.description}</div></td>
                <td>{e.installed ? <Badge tone="ok">Installed</Badge> : null}</td>
                <td style={{ textAlign: "right" }}>
                  <Button size="sm" variant={e.installed ? "ghost" : "secondary"} onClick={async () => { try { start(await api.changePHPExtension(v, e.name, !e.installed)); } catch (err) { toast("error", (err as Error).message); } }}>
                    {e.installed ? "Remove" : "Install"}</Button>
                </td>
              </tr>))}</tbody>
          </table></div>
        )}
      </section>
      <JobModal job={job} onClose={clear} />
    </div>
  );
}

/* ========================================================== Certificates */

export function Certificates() {
  const certs = useAsync(() => api.certificates());
  const sites = useAsync(() => api.sites());
  const without = (sites.data ?? []).filter((s) => !s.ssl && s.status === "active");
  return (
    <div className="stack">
      <PageHead title="SSL certificates" text="Free Let's Encrypt certificates renew on their own about 30 days before they expire. You get an alert if renewal fails or an uploaded certificate is about to run out." />
      <section className="panel">
        {!certs.data ? <div className="panel-body"><Skeleton /></div> : certs.data.length === 0 ? (
          <Empty icon={<Lock size={22} />} title="No certificates yet" text="Open a website's Domains & HTTPS tab to get a free certificate." />
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Website</th><th>Type</th><th>Expires</th><th /></tr></thead>
            <tbody>{certs.data.map((c) => (
              <tr key={c.domain}>
                <td className="lead"><a href={href(`/sites/${c.domain}`)} style={{ fontWeight: 600 }}>{c.domain}</a><div className="small muted">{c.names.join(", ")}</div>
                  {c.problem ? <div className="small" style={{ color: "var(--bad)" }}>{c.problem}</div> : null}</td>
                <td className="small">{c.provider === "letsencrypt" ? "Let's Encrypt" : `Uploaded (${c.issuer})`}</td>
                <td><Badge tone={c.days_left < 0 ? "bad" : c.problem ? "warn" : "ok"}>{c.days_left < 0 ? "Expired" : `${c.days_left} days`}</Badge></td>
                <td />
              </tr>))}</tbody>
          </table></div>
        )}
      </section>
      {without.length ? (
        <section className="panel">
          <div className="panel-head"><div><h2>Websites without HTTPS</h2><p className="small muted">Browsers mark these “Not secure”.</p></div></div>
          <div className="panel-body row">{without.map((s) => <a key={s.domain} className="btn" href={href(`/sites/${s.domain}`)}><Lock size={15} />{s.domain}</a>)}</div>
        </section>
      ) : null}
    </div>
  );
}

