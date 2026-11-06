// Databases for every engine (MariaDB, PostgreSQL, MongoDB), per-account
// Redis or Valkey, import/export, connection diagnostics, live queries, the
// slow query log and MariaDB replication.
import { useState } from "react";
import { Activity, Database, KeyRound, Plus, RefreshCw, Stethoscope, Trash2, Users, Zap } from "lucide-react";
import { api } from "../api/client";
import type { Job, Role } from "../api/types";
import type { DBCredentials, DBProcess, EngineDB, Engines, Replication, RedisInstance, SlowQuery } from "../api/types5";
import { Badge, Button, CopyField, Empty, Field, Modal, Skeleton, Tabs, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { DownloadLink, enc, ErrorNote, FormModal, NumberField, OnceModal, SelectField, TextField, UploadButton, useAction, useGet } from "../components/kit5";
import { useAccountParam } from "./AccountTools";

const engineName: Record<string, string> = { mariadb: "MariaDB", postgres: "PostgreSQL", mongodb: "MongoDB" };

interface DBList { engines: Engines; mariadb: string[] | null; databases: EngineDB[] | null; redis?: RedisInstance | null }

export function DatabasesPage({ route, role }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const [tab, setTab] = useState<"dbs" | "redis" | "activity" | "replication">("dbs");
  return (
    <div className="stack">
      <PageHead title="Databases" text="MariaDB, PostgreSQL and MongoDB databases, and a private Redis for each account. Names start with the account name so accounts never clash."
        actions={picker} />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "dbs", label: "Databases" }, { value: "redis", label: "Redis" }, { value: "activity", label: "Running queries" },
        ...(role === "admin" ? [{ value: "replication" as const, label: "Replication" }] : [])]} />
      {!account && tab !== "replication" ? <Skeleton h={120} /> : tab === "dbs" ? <DbList account={account} /> : tab === "redis" ? <RedisPanel account={account} />
        : tab === "activity" ? <ActivityPanel base={`/accounts/${enc(account)}/dbs`} /> : <ReplicationPanel />}
    </div>
  );
}

function DbList({ account }: { account: string }) {
  const toast = useToast();
  const base = `/accounts/${enc(account)}/dbs`;
  const list = useGet<DBList>(base);
  const [create, setCreate] = useState(false);
  const [form, setForm] = useState({ engine: "mariadb", name: "", note: "" });
  const [cred, setCred] = useState<DBCredentials | null>(null);
  const [del, setDel] = useState<EngineDB | null>(null);
  const [diag, setDiag] = useState<EngineDB | null>(null);
  const [roleFor, setRoleFor] = useState<EngineDB | null>(null);
  const { busy, run } = useAction();
  const engines = list.data?.engines;
  const all: EngineDB[] = [
    ...(list.data?.mariadb ?? []).map((n) => ({ engine: "mariadb" as const, name: n, owner: n, created_at: "" })),
    ...(list.data?.databases ?? []),
  ];
  const engineOptions = engines ? [engines.mariadb && "mariadb", engines.postgres && "postgres", engines.mongodb && "mongodb"].filter(Boolean) as string[] : ["mariadb"];
  return (
    <section className="panel">
      <div className="panel-head">
        <div className="small muted">{engines ? `On this server: ${engineOptions.map((e) => engineName[e]).join(", ")}${engines.redis ? `, ${engines.redis === "valkey" ? "Valkey" : "Redis"}` : ""}` : ""}</div>
        <div className="row">
          <Button size="sm" loading={busy === "sizes"} onClick={() => run("sizes", () => api.call("POST", `${base}/sizes`)).then(() => list.reload())}><RefreshCw size={15} />Measure sizes</Button>
          <Button variant="primary" onClick={() => { setForm({ ...form, engine: engineOptions[0] }); setCreate(true); }}><Plus size={16} />Create database</Button>
        </div>
      </div>
      {!list.data ? <div className="panel-body"><Skeleton /><Skeleton /></div> : all.length === 0 ? (
        <Empty icon={<Database size={22} />} title="No databases yet" text="Create one for WordPress, Laravel or any other app. The connection details are shown once." />
      ) : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Database</th><th>Engine</th><th data-hide-sm>Users</th><th className="num" data-hide-sm>Size</th><th /></tr></thead>
          <tbody>{all.map((d) => {
            const path = `${base}/${d.engine}/${enc(d.name)}`;
            return (
              <tr key={d.engine + d.name}>
                <td className="lead mono">{d.name}{d.note ? <div className="small muted" style={{ fontFamily: "var(--font-ui)" }}>{d.note}</div> : null}</td>
                <td><Badge>{engineName[d.engine]}</Badge></td>
                <td className="small mono" data-hide-sm>{[d.owner, ...(d.roles ?? []).map((r) => `${r.name} (${r.access})`)].join(", ")}</td>
                <td className="num muted" data-hide-sm>{d.size_mb ? `${d.size_mb.toFixed(1)} MB` : ""}</td>
                <td><div className="row-actions">
                  <DownloadLink small path={`${path}/export`} label="Export" />
                  <UploadButton method="POST" path={() => `${path}/import`} label="Import" accept=".sql,.gz,.archive" onDone={() => toast("ok", `Imported into ${d.name}`)} />
                  <Button size="sm" variant="ghost" icon aria-label="New password" loading={busy === "pw" + d.name}
                    onClick={() => run("pw" + d.name, () => api.call<DBCredentials>("POST", `${path}/password`, { role: "" })).then((c) => c && setCred(c))}><KeyRound size={15} /></Button>
                  {d.engine !== "mariadb" ? <Button size="sm" variant="ghost" icon aria-label="Add a user" onClick={() => setRoleFor(d)}><Users size={15} /></Button> : null}
                  <Button size="sm" variant="ghost" icon aria-label="Diagnose connection" onClick={() => setDiag(d)}><Stethoscope size={15} /></Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Delete ${d.name}`} onClick={() => setDel(d)}><Trash2 size={15} /></Button>
                </div></td>
              </tr>
            );
          })}</tbody>
        </table></div>
      )}
      <FormModal open={create} onClose={() => setCreate(false)} title="Create a database" submit="Create database" onSubmit={async () => {
        const c = form.engine === "mariadb"
          ? await api.createDatabase(account, form.name, form.note).then((n) => ({ engine: "mariadb", database: n.name, user: n.user, password: n.password, host: n.host, url: `mysql://${n.user}:${n.password}@${n.host}/${n.name}` }))
          : await api.call<DBCredentials>("POST", base, form);
        setCred(c); list.reload();
      }}>
        <SelectField label="Engine" value={form.engine} onChange={(v) => setForm({ ...form, engine: v })} options={engineOptions.map((e) => ({ value: e, label: engineName[e] }))} />
        <Field label="Name" hint={`Becomes ${account}_${form.name || "name"}`}>{(id) => <div className="row" style={{ gap: 4, flexWrap: "nowrap" }}><span className="mono muted">{account}_</span>
          <input id={id} className="input mono" value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value.toLowerCase().replace(/[^a-z0-9]/g, "") })} maxLength={16} autoFocus /></div>}</Field>
        <TextField label="Note (optional)" value={form.note} onChange={(v) => setForm({ ...form, note: v })} placeholder="shop.ng WordPress" />
      </FormModal>
      <OnceModal open={!!cred} onClose={() => setCred(null)} title={`Connection details for ${cred?.database}`}>
        {cred ? <dl className="kv"><dt>Host</dt><dd className="mono">{cred.host}{cred.port ? `:${cred.port}` : ""}</dd><dt>Database</dt><dd className="mono">{cred.database}</dd>
          <dt>User</dt><dd className="mono">{cred.user}</dd><dt>Password</dt><dd><CopyField value={cred.password} label="Password" /></dd>
          <dt>URL</dt><dd><CopyField value={cred.url} label="Connection URL" /></dd></dl> : null}
      </OnceModal>
      {roleFor ? <RoleModal db={roleFor} base={base} onClose={() => setRoleFor(null)} onCreated={(c) => { setCred(c); list.reload(); }} /> : null}
      {diag ? <DiagnoseModal db={diag} base={base} onClose={() => setDiag(null)} /> : null}
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete ${del?.name}?`} name={del?.name ?? ""} confirmLabel="Delete database"
        description="The database and all its data are deleted. Export it first if you might need it." onConfirm={async () => {
          if (del!.engine === "mariadb") await api.deleteDatabase(account, del!.name); else await api.call("DELETE", `${base}/${del!.engine}/${enc(del!.name)}`);
          list.reload();
        }} />
    </section>
  );
}

function RoleModal({ db, base, onClose, onCreated }: { db: EngineDB; base: string; onClose: () => void; onCreated: (c: DBCredentials) => void }) {
  const [f, setF] = useState({ name: "", access: "read" });
  return (
    <FormModal open onClose={onClose} title={`Add a user to ${db.name}`} submit="Add user" onSubmit={async () => onCreated(await api.call<DBCredentials>("POST", `${base}/${db.engine}/${enc(db.name)}/roles`, f))}>
      <TextField label="User name suffix" value={f.name} onChange={(v) => setF({ ...f, name: v.toLowerCase().replace(/[^a-z0-9]/g, "") })} mono hint="For example 'reports' for a read-only reporting user." />
      <SelectField label="Access" value={f.access} onChange={(v) => setF({ ...f, access: v })} options={[{ value: "read", label: "Read only" }, { value: "write", label: "Read and write" }]} />
    </FormModal>
  );
}

function DiagnoseModal({ db, base, onClose }: { db: EngineDB; base: string; onClose: () => void }) {
  const [f, setF] = useState({ user: db.owner, password: "", php: "" });
  const [out, setOut] = useState<{ title: string; severity: string; detail: string; fix?: string }[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <Modal open onClose={onClose} title={`Why can't my app connect to ${db.name}?`} footer={<Button onClick={onClose}>Close</Button>}
      description="Enter the user and password your app uses. Ike tries them the way your app would and explains what fails. The password isn't stored.">
      <div className="form-grid">
        <TextField label="User" value={f.user} onChange={(v) => setF({ ...f, user: v })} mono />
        <TextField label="Password" value={f.password} onChange={(v) => setF({ ...f, password: v })} type="password" />
        {db.engine !== "mongodb" ? <TextField label="Website's PHP version (optional)" value={f.php} onChange={(v) => setF({ ...f, php: v })} placeholder="8.3" hint="Checks that PHP has the extension for this database." /> : null}
        <div><Button variant="primary" loading={busy} onClick={async () => {
          setBusy(true); setError(null);
          try { setOut(await api.call("POST", `${base}/${db.engine}/${enc(db.name)}/diagnose`, f)); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}><Stethoscope size={16} />Check</Button></div>
        <ErrorNote text={error} />
        {out ? <ul className="stack" style={{ listStyle: "none", padding: 0, margin: 0, gap: 8 }}>{out.map((c, i) => (
          <li key={i} className={`callout ${c.severity === "ok" || c.severity === "info" ? "info" : c.severity === "warn" ? "warn" : "bad"}`}>
            <div><strong>{c.title}</strong><p className="small">{c.detail}</p>{c.fix ? <p className="small"><strong>What to do:</strong> {c.fix}</p> : null}</div></li>))}</ul> : null}
      </div>
    </Modal>
  );
}

function RedisPanel({ account }: { account: string }) {
  const base = `/accounts/${enc(account)}/redis`;
  const st = useAsync(() => api.call<RedisInstance["stats"]>("GET", base).then((x) => x).catch(() => null), [base]);
  const list = useGet<DBList>(`/accounts/${enc(account)}/dbs`);
  const inst = list.data?.redis ?? null;
  const [f, setF] = useState<{ memory_mb: number; policy: string; persist: boolean } | null>(null);
  const [cred, setCred] = useState<DBCredentials | null>(null);
  const [del, setDel] = useState(false);
  const { busy, run } = useAction();
  const form = f ?? (inst ? { memory_mb: inst.memory_mb, policy: inst.policy, persist: inst.persist } : { memory_mb: 128, policy: "allkeys-lru", persist: false });
  if (list.data && !list.data.engines.redis) return <section className="panel"><Empty icon={<Zap size={22} />} title="Redis isn't installed" text="Ask your hosting provider. The admin adds it with the installer's --redis or --valkey option." /></section>;
  const save = (newPassword: boolean) => run("save", () => api.call<{ credentials?: DBCredentials; redis: RedisInstance }>("PUT", base, { ...form, new_password: newPassword }),
    inst ? "Redis settings saved" : "Redis is running").then((r) => { if (r) { if (r.credentials?.password) setCred(r.credentials); list.reload(); st.reload(); } });
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2>{inst ? "Your Redis" : "Start a private Redis"}</h2><p className="small muted">For caches, sessions and queues. Only this account can reach it, through a socket file.</p></div></div>
        <div className="panel-body form-grid">
          <NumberField label="Memory (MB)" value={form.memory_mb} onChange={(v) => setF({ ...form, memory_mb: v })} min={16} />
          <SelectField label="When memory is full" value={form.policy} onChange={(v) => setF({ ...form, policy: v })} options={[
            { value: "allkeys-lru", label: "Remove the least used keys (cache)" }, { value: "volatile-lru", label: "Remove least used keys that expire" }, { value: "noeviction", label: "Refuse new writes (queues, sessions)" }]} />
          <Toggle checked={form.persist} onChange={(v) => setF({ ...form, persist: v })} label="Save data to disk (survives restarts)" />
          <div className="row">
            <Button variant="primary" loading={busy === "save"} onClick={() => save(false)}>{inst ? "Save" : "Start Redis"}</Button>
            {inst ? <><Button onClick={() => save(true)}><KeyRound size={16} />New password</Button>
              <Button loading={busy === "flush"} onClick={() => run("flush", () => api.call("POST", `${base}/flush`), "Every key deleted")}>Empty it</Button>
              <Button variant="ghost" onClick={() => setDel(true)}><Trash2 size={16} />Remove</Button></> : null}
          </div>
          {inst ? <Field label="Socket">{() => <CopyField value={inst.socket} label="Socket" />}</Field> : null}
        </div>
      </section>
      {inst ? (
        <section className="panel">
          <div className="panel-head"><h2>Status</h2><Button size="sm" onClick={st.reload} loading={st.loading}><RefreshCw size={15} /></Button></div>
          <div className="panel-body">{!st.data ? <Skeleton /> : (
            <dl className="kv"><dt>State</dt><dd>{st.data.running ? <Badge tone="ok">Running</Badge> : <Badge tone="bad">Stopped</Badge>}</dd>
              <dt>Memory used</dt><dd>{st.data.used_mb.toFixed(1)} of {inst.memory_mb} MB</dd><dt>Keys</dt><dd>{st.data.keys}</dd>
              <dt>Hit rate</dt><dd>{st.data.hit_rate.toFixed(0)}%</dd><dt>Clients</dt><dd>{st.data.clients}</dd><dt>Version</dt><dd>{st.data.version}</dd></dl>
          )}</div>
        </section>
      ) : null}
      <OnceModal open={!!cred} onClose={() => setCred(null)} title="Redis password">
        {cred ? <><CopyField value={cred.password} label="Password" /><Field label="URL">{() => <CopyField value={cred.url} label="URL" />}</Field></> : null}
      </OnceModal>
      <ConfirmDelete open={del} onClose={() => setDel(false)} title="Remove this Redis?" name="redis" confirmLabel="Remove Redis" description="It stops and its data is deleted."
        onConfirm={async () => { await api.call("DELETE", base); list.reload(); }} />
    </div>
  );
}

function ActivityPanel({ base }: { base: string }) {
  const procs = useAsync(() => api.call<DBProcess[]>("GET", `${base}/processes`), [base]);
  const slow = useAsync(() => api.call<SlowQuery[]>("GET", `${base}/slow-queries?sort=duration`), [base]);
  const { busy, run } = useAction();
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><div><h2>Running now</h2><p className="small muted">MariaDB queries in progress. Stop one that's stuck.</p></div><Button size="sm" onClick={procs.reload} loading={procs.loading}><RefreshCw size={15} />Refresh</Button></div>
        {!procs.data ? <div className="panel-body"><Skeleton /></div> : procs.data.length === 0 ? <Empty icon={<Activity size={22} />} title="Nothing running" text="No queries are running right now." /> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Query</th><th>User</th><th className="num">Seconds</th><th /></tr></thead>
            <tbody>{procs.data.map((p) => (
              <tr key={p.id}>
                <td className="lead"><code className="small" style={{ wordBreak: "break-all" }}>{p.query || p.command}</code><div className="small muted">{p.db} · {p.state}</div></td>
                <td className="mono small">{p.user}</td><td className="num">{p.seconds}</td>
                <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" loading={busy === String(p.id)} onClick={() => run(String(p.id), () => api.call("POST", `${base}/processes/${p.id}/kill`), "Query stopped").then(() => procs.reload())}>Stop</Button></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Slow queries</h2><p className="small muted">Queries that took longer than the slow log's limit, slowest first. Add an index or cache the result.</p></div></div>
        {!slow.data ? <div className="panel-body"><Skeleton /></div> : slow.data.length === 0 ? <Empty icon={<Zap size={22} />} title="No slow queries" text="Either everything is fast, or the slow query log is off." /> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Query</th><th className="num">Seconds</th><th className="num" data-hide-sm>Rows read</th></tr></thead>
            <tbody>{slow.data.slice(0, 50).map((q, i) => (
              <tr key={i}><td className="lead"><code className="small" style={{ wordBreak: "break-all" }}>{q.query}</code><div className="small muted">{q.db} · {q.time}</div></td>
                <td className="num">{q.query_secs.toFixed(2)}</td><td className="num" data-hide-sm>{q.rows_examined.toLocaleString()}</td></tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
    </div>
  );
}

function ReplicationPanel() {
  const rep = useAsync(() => api.call<Replication>("GET", "/databases/replication"));
  const slowlog = useAsync(() => api.call<{ enabled: boolean; long_query_secs: number; log_queries_not_using_indexes: boolean }>("GET", "/databases/slowlog"));
  const [src, setSrc] = useState({ replica_ip: "", server_id: 1 });
  const [repl, setRepl] = useState({ primary_host: "", user: "", password: "", server_id: 2, copy_data: true });
  const [out, setOut] = useState<{ user: string; password: string; restart_needed: boolean } | null>(null);
  const { job, start, clear } = useJob(() => rep.reload());
  const { busy, run } = useAction();
  const r = rep.data;
  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><div><h2>MariaDB replication</h2><p className="small muted">Keep a live copy of every database on another server (GTID replication). Use the Infrastructure page to manage whole clusters.</p></div>
          <Button size="sm" onClick={rep.reload} loading={rep.loading}><RefreshCw size={15} /></Button></div>
        <div className="panel-body">{!r ? <Skeleton /> : r.configured ? (
          <dl className="kv"><dt>This server</dt><dd>Replica of <span className="mono">{r.primary_host}</span></dd>
            <dt>Copying</dt><dd>{r.io_running && r.sql_running ? <Badge tone="ok">Running</Badge> : <Badge tone="bad">Stopped</Badge>} {r.seconds_behind ? `${r.seconds_behind}s behind` : "up to date"}</dd>
            <dt>GTID</dt><dd className="mono small">{r.gtid}</dd>{r.last_error ? <><dt>Error</dt><dd style={{ color: "var(--bad)" }}>{r.last_error}</dd></> : null}</dl>
        ) : <p className="muted small">This server isn't a replica.{r.binary_logging ? " Binary logging is on, so it can be a primary." : ""}</p>}
          {r?.configured ? <div className="row" style={{ marginTop: 12 }}>
            <Button onClick={() => run("stop", () => api.call("POST", "/databases/replication/stop", { promote: false }), "Replication stopped").then(() => rep.reload())}>Stop copying</Button>
            <Button variant="danger" onClick={() => run("promote", () => api.call("POST", "/databases/replication/stop", { promote: true }), "This server is now the primary").then(() => rep.reload())}>Make this the primary</Button>
          </div> : null}
        </div>
      </section>
      <div className="grid-2">
        <section className="panel">
          <div className="panel-head"><div><h2>Be the primary</h2><p className="small muted">Allows a replica to copy from this server.</p></div></div>
          <div className="panel-body form-grid">
            <TextField label="Replica's IP address" value={src.replica_ip} onChange={(v) => setSrc({ ...src, replica_ip: v })} mono />
            <NumberField label="Server ID (unique per server)" value={src.server_id} onChange={(v) => setSrc({ ...src, server_id: v })} min={1} />
            <div><Button variant="primary" loading={busy === "src"} onClick={() => run("src", () => api.call<{ user: string; password: string; restart_needed: boolean }>("POST", "/databases/replication/source", src)).then((x) => x && setOut(x))}>Prepare</Button></div>
          </div>
        </section>
        <section className="panel">
          <div className="panel-head"><div><h2>Copy from a primary</h2><p className="small muted">Turns this server into a replica. Its own databases are replaced when copying the data.</p></div></div>
          <div className="panel-body form-grid">
            <TextField label="Primary's address" value={repl.primary_host} onChange={(v) => setRepl({ ...repl, primary_host: v })} mono />
            <TextField label="Replication user" value={repl.user} onChange={(v) => setRepl({ ...repl, user: v })} mono />
            <TextField label="Password" value={repl.password} onChange={(v) => setRepl({ ...repl, password: v })} type="password" />
            <NumberField label="Server ID" value={repl.server_id} onChange={(v) => setRepl({ ...repl, server_id: v })} min={1} />
            <Toggle checked={repl.copy_data} onChange={(v) => setRepl({ ...repl, copy_data: v })} label="Copy the current data first" />
            <div><Button variant="primary" loading={busy === "rep"} onClick={() => run("rep", () => api.call<Job>("POST", "/databases/replication/replica", repl)).then((j) => j && start(j))}>Start replicating</Button></div>
          </div>
        </section>
      </div>
      <section className="panel">
        <div className="panel-head"><h2>Slow query log</h2></div>
        <div className="panel-body form-grid">{!slowlog.data ? <Skeleton /> : <>
          <Toggle checked={slowlog.data.enabled} onChange={(v) => slowlog.setData({ ...slowlog.data!, enabled: v })} label="Log slow queries" />
          <NumberField label="Slower than (seconds)" value={slowlog.data.long_query_secs} onChange={(v) => slowlog.setData({ ...slowlog.data!, long_query_secs: v })} />
          <Toggle checked={slowlog.data.log_queries_not_using_indexes} onChange={(v) => slowlog.setData({ ...slowlog.data!, log_queries_not_using_indexes: v })} label="Also log queries that don't use an index" />
          <div><Button variant="primary" onClick={() => run("sl", () => api.call("PUT", "/databases/slowlog", slowlog.data), "Slow log saved")}>Save</Button></div>
        </>}</div>
      </section>
      <OnceModal open={!!out} onClose={() => setOut(null)} title="Replication user">
        {out ? <><dl className="kv"><dt>User</dt><dd className="mono">{out.user}</dd><dt>Password</dt><dd><CopyField value={out.password} label="Password" /></dd></dl>
          {out.restart_needed ? <div className="callout warn">MariaDB needs a restart to turn on binary logging. Restart it from Server software.</div> : null}</> : null}
      </OnceModal>
      <JobModal job={job} onClose={clear} />
    </div>
  );
}
