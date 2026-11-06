// File manager, snapshots, share links and storage targets, plus the web
// terminal (xterm.js over a WebSocket to the account's shell).
import { useEffect, useMemo, useRef, useState } from "react";
import {
  Archive, ChevronRight, Copy, Download, File, FileArchive, FilePlus, Folder, FolderPlus, HardDrive, Home, Link2, Lock, MoveRight, Pencil,
  RefreshCw, Search, Trash2, Undo2, Upload,
} from "lucide-react";
import { api, isDemo } from "../api/client";
import type { Job, Role } from "../api/types";
import type { FileEntry, FileHit, FileSnapshot, FolderSize, Share, StorageTarget } from "../api/types5";
import { Badge, Button, CopyField, Empty, Modal, Segmented, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { JobModal, useJob } from "../components/job";
import { bytes, dateTime, enc, ErrorNote, FormModal, NumberField, SelectField, TextField, useAction, useGet } from "../components/kit5";
import { useAccountParam } from "./AccountTools";
import { Terminal as DemoTerminal } from "./ServerPages";

const join = (a: string, b: string) => (a ? `${a}/${b}` : b);
const textExt = /\.(php|js|mjs|ts|tsx|jsx|json|html?|css|scss|md|txt|ini|conf|env|xml|yml|yaml|sh|py|rb|go|java|sql|htaccess|log|csv|svg|vue|twig|blade\.php)$|(^|\/)\.[a-z]+$/i;

export function FilesPage({ route }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const [tab, setTab] = useState<"files" | "snapshots" | "shares" | "storage">("files");
  return (
    <div className="stack">
      <PageHead title="Files" text="Browse, edit, upload and share the account's files. Everything runs as the account's own user, so nothing outside its home folder can be touched." actions={picker} />
      <Tabs value={tab} onChange={setTab} tabs={[{ value: "files", label: "File manager" }, { value: "snapshots", label: "Snapshots" }, { value: "shares", label: "Share links" }, { value: "storage", label: "Cloud storage" }]} />
      {!account ? <Skeleton h={200} /> : tab === "files" ? <FileManager account={account} /> : tab === "snapshots" ? <Snapshots account={account} />
        : tab === "shares" ? <Shares account={account} /> : <Storage base={`/accounts/${enc(account)}/storage`} />}
    </div>
  );
}

function FileManager({ account }: { account: string }) {
  const toast = useToast();
  const base = `/accounts/${enc(account)}/files`;
  const [path, setPath] = useState("web");
  const list = useAsync(() => api.call<FileEntry[]>("GET", `${base}?path=${enc(path)}`), [base, path]);
  const sizes = useAsync<FolderSize[] | null>(() => Promise.resolve(null), [path]);
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [edit, setEdit] = useState<string | null>(null);
  const [ask, setAsk] = useState<null | "mkdir" | "newfile" | "rename" | "move" | "copy" | "chmod" | "archive" | "share">(null);
  const [val, setVal] = useState("");
  const [recursive, setRecursive] = useState(false);
  const [del, setDel] = useState(false);
  const [search, setSearch] = useState("");
  const [hits, setHits] = useState<FileHit[] | null>(null);
  const [shared, setShared] = useState<Share | null>(null);
  const { busy, run } = useAction();
  useEffect(() => { setSel(new Set()); setHits(null); }, [path]);
  const entries = useMemo(() => [...(list.data ?? [])].sort((a, b) => (a.type === "dir" ? 0 : 1) - (b.type === "dir" ? 0 : 1) || a.name.localeCompare(b.name)), [list.data]);
  const crumbs = path ? path.split("/") : [];
  const chosen = [...sel].map((n) => join(path, n));
  const op = (name: string, body: unknown, msg: string) => run(name, () => api.call("POST", `${base}/${name}`, body), msg).then(() => { list.reload(); setSel(new Set()); });
  const sizeOf = (n: string) => sizes.data?.find((s) => s.path === join(path, n))?.bytes;
  const one = sel.size === 1 ? [...sel][0] : "";
  const openAsk = (k: typeof ask, v = "") => { setVal(v); setAsk(k); };
  return (
    <section className="panel">
      <div className="panel-head" style={{ flexWrap: "wrap", gap: 10 }}>
        <nav className="row" aria-label="Folder" style={{ gap: 2, minWidth: 0 }}>
          <Button size="sm" variant="ghost" onClick={() => setPath("")} aria-label="Home folder"><Home size={15} /></Button>
          {crumbs.map((c, i) => (
            <span key={i} className="row" style={{ gap: 2 }}><ChevronRight size={14} className="muted" aria-hidden />
              <Button size="sm" variant="ghost" onClick={() => setPath(crumbs.slice(0, i + 1).join("/"))}>{c}</Button></span>
          ))}
        </nav>
        <div className="row">
          <form className="search" style={{ width: 220 }} onSubmit={(e) => { e.preventDefault(); if (search.trim()) run("search", () => api.call<FileHit[]>("GET", `${base}/search?path=${enc(path)}&name=${enc(search)}`)).then((h) => h && setHits(h)); }}>
            <Search size={15} aria-hidden /><input value={search} onChange={(e) => setSearch(e.target.value)} placeholder="Find files here" aria-label="Find files" />
          </form>
          <Button size="sm" onClick={() => openAsk("mkdir")}><FolderPlus size={15} />Folder</Button>
          <Button size="sm" onClick={() => openAsk("newfile")}><FilePlus size={15} />File</Button>
          <label className="btn sm" style={{ cursor: "pointer" }}><Upload size={15} />Upload
            <input type="file" multiple hidden onChange={async (e) => {
              const files = [...(e.target.files ?? [])];
              e.target.value = "";
              for (const f of files) {
                try { await api.upload("PUT", `${base}/upload?path=${enc(join(path, f.name))}&overwrite=true`, f); }
                catch (err) { toast("error", `${f.name}: ${(err as Error).message}`); }
              }
              if (files.length) { toast("ok", `${files.length} uploaded`); list.reload(); }
            }} /></label>
          <Button size="sm" loading={sizes.loading && !!sizes.data} onClick={() => api.call<FolderSize[]>("GET", `${base}/sizes?path=${enc(path)}`).then((d) => sizes.setData(d)).catch((e) => toast("error", e.message))}><HardDrive size={15} />Sizes</Button>
          <Button size="sm" variant="ghost" icon aria-label="Refresh" onClick={list.reload}><RefreshCw size={15} /></Button>
        </div>
      </div>
      {sel.size ? (
        <div className="row" style={{ padding: "8px 16px", borderBottom: "1px solid var(--line)", background: "var(--accent-soft)" }}>
          <strong className="small">{sel.size} selected</strong>
          {one ? <Button size="sm" variant="ghost" onClick={() => openAsk("rename", one)}><Pencil size={14} />Rename</Button> : null}
          <Button size="sm" variant="ghost" onClick={() => openAsk("move", path)}><MoveRight size={14} />Move</Button>
          <Button size="sm" variant="ghost" onClick={() => openAsk("copy", path)}><Copy size={14} />Copy</Button>
          <Button size="sm" variant="ghost" onClick={() => openAsk("chmod", entries.find((x) => x.name === one)?.mode ?? "0644")}><Lock size={14} />Permissions</Button>
          <Button size="sm" variant="ghost" onClick={() => openAsk("archive", sel.size === 1 ? one : "archive")}><Archive size={14} />Compress</Button>
          {one && /\.(zip|tar|tgz|tar\.gz|tar\.xz|tar\.bz2|gz)$/i.test(one) ? <Button size="sm" variant="ghost" loading={busy === "extract"} onClick={() => op("extract", { path: join(path, one), into: path }, "Extracted")}><FileArchive size={14} />Extract</Button> : null}
          {one ? <a className="btn sm ghost" href={api.fileUrl(`${base}/download?path=${enc(join(path, one))}`) || undefined}
            onClick={(e) => { if (!api.fileUrl("x")) { e.preventDefault(); toast("ok", "Downloads work on a real server."); } }}><Download size={14} />Download</a> : null}
          {one ? <Button size="sm" variant="ghost" onClick={() => openAsk("share", "72")}><Link2 size={14} />Share</Button> : null}
          <Button size="sm" variant="ghost" onClick={() => setDel(true)}><Trash2 size={14} />Delete</Button>
        </div>
      ) : null}
      {hits ? (
        <div className="panel-body">
          <div className="row" style={{ justifyContent: "space-between" }}><strong>{hits.length} found</strong><Button size="sm" variant="ghost" onClick={() => setHits(null)}>Back to folder</Button></div>
          <ul style={{ listStyle: "none", padding: 0 }}>{hits.map((h) => (
            <li key={h.path} style={{ padding: "6px 0", borderBottom: "1px solid var(--line)" }}>
              <button className="linklike mono small" onClick={() => { const dir = h.path.split("/").slice(0, -1).join("/"); setPath(h.type === "dir" ? h.path : dir); }}>{h.path}</button>
              {h.line ? <div className="small muted mono">{h.line}</div> : null}
            </li>))}</ul>
        </div>
      ) : !list.data ? <div className="panel-body"><Skeleton /><Skeleton /><Skeleton /></div> : list.error ? <div className="panel-body"><ErrorNote text={list.error} /></div> : entries.length === 0 ? (
        <Empty icon={<Folder size={22} />} title="This folder is empty" text="Upload files or create a folder." />
      ) : (
        <div className="table-wrap"><table className="responsive files">
          <thead><tr>
            <th style={{ width: 32 }}><input type="checkbox" aria-label="Select all" checked={sel.size === entries.length} onChange={(e) => setSel(e.target.checked ? new Set(entries.map((x) => x.name)) : new Set())} /></th>
            <th>Name</th><th className="num">Size</th><th data-hide-sm>Modified</th><th data-hide-sm>Mode</th></tr></thead>
          <tbody>{entries.map((f) => (
            <tr key={f.name} aria-selected={sel.has(f.name)}>
              <td><input type="checkbox" aria-label={`Select ${f.name}`} checked={sel.has(f.name)} onChange={(e) => { const n = new Set(sel); if (e.target.checked) n.add(f.name); else n.delete(f.name); setSel(n); }} /></td>
              <td className="lead">
                <button className="linklike row" style={{ gap: 8 }} onDoubleClick={() => f.type !== "dir" && textExt.test(f.name) && setEdit(join(path, f.name))}
                  onClick={() => { if (f.type === "dir") setPath(join(path, f.name)); else if (textExt.test(f.name) && f.size < 2 << 20) setEdit(join(path, f.name)); else setSel(new Set([f.name])); }}>
                  {f.type === "dir" ? <Folder size={16} style={{ color: "var(--accent)" }} aria-hidden /> : <File size={16} className="muted" aria-hidden />}
                  <span className="mono" style={{ wordBreak: "break-all", textAlign: "left" }}>{f.name}</span>
                  {f.type === "link" ? <Badge>→ {f.target}</Badge> : null}
                </button>
              </td>
              <td className="num muted">{f.type === "dir" ? (sizeOf(f.name) !== undefined ? bytes(sizeOf(f.name)) : "") : bytes(f.size)}</td>
              <td className="muted small" data-hide-sm>{timeAgo(f.modified)}</td>
              <td className="mono small muted" data-hide-sm>{f.mode}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      {edit ? <Editor base={base} path={edit} onClose={() => { setEdit(null); list.reload(); }} /> : null}
      <FormModal open={!!ask} onClose={() => setAsk(null)} title={{ mkdir: "New folder", newfile: "New file", rename: "Rename", move: "Move to", copy: "Copy to", chmod: "Permissions", archive: "Compress", share: "Share link" }[ask ?? "mkdir"]}
        submit={ask === "share" ? "Create link" : "OK"} onSubmit={async () => {
          const v = val.trim();
          if (ask === "mkdir") await api.call("POST", `${base}/mkdir`, { path: join(path, v) });
          else if (ask === "newfile") { await api.call("PUT", `${base}/content?path=${enc(join(path, v))}`, { content: "", overwrite: false }); setEdit(join(path, v)); }
          else if (ask === "rename") await api.call("POST", `${base}/move`, { from: [join(path, one)], to: join(path, v), rename: true });
          else if (ask === "move" || ask === "copy") await api.call("POST", `${base}/${ask}`, { from: chosen, to: v });
          else if (ask === "chmod") await api.call("POST", `${base}/chmod`, { paths: chosen, mode: v, recursive });
          else if (ask === "archive") await api.call("POST", `${base}/archive`, { dir: path, items: [...sel], name: v, format: v.endsWith(".zip") ? "zip" : "tar.gz" });
          else if (ask === "share") setShared(await api.call<Share>("POST", `/accounts/${enc(account)}/shares`, { path: join(path, one), expires_hours: Number(v) || 72 }));
          list.reload(); setSel(new Set());
        }}>
        {ask === "chmod" ? <>
          <TextField label="Mode" value={val} onChange={setVal} mono hint="0644 for files, 0755 for folders. Ike never allows world-writable (777)." />
          <Toggle checked={recursive} onChange={setRecursive} label="Apply inside folders too" />
        </> : ask === "share" ? <NumberField label="Link works for (hours)" value={Number(val)} onChange={(v) => setVal(String(v))} min={1} max={720} />
          : <TextField label={ask === "move" || ask === "copy" ? "Folder (from the home folder)" : "Name"} value={val} onChange={setVal} mono autoFocus
            hint={ask === "archive" ? "End with .zip or .tar.gz" : undefined} />}
      </FormModal>
      <OnceShare share={shared} onClose={() => setShared(null)} />
      <ConfirmDelete open={del} onClose={() => setDel(false)} title={`Delete ${sel.size} ${sel.size === 1 ? "item" : "items"}?`} name="delete" confirmLabel="Delete"
        description={<>Deleted files can be brought back from a snapshot or backup if you have one. <span className="mono">{chosen.slice(0, 5).join(", ")}</span></>}
        onConfirm={async () => { await api.call("POST", `${base}/delete`, { paths: chosen }); list.reload(); setSel(new Set()); }} />
    </section>
  );
}

function OnceShare({ share, onClose }: { share: Share | null; onClose: () => void }) {
  const url = share ? (isDemo ? `https://panel.example.com${share.url_path}` : new URL(share.url_path, window.location.href).toString()) : "";
  return (
    <Modal open={!!share} onClose={onClose} title="Share link ready" footer={<Button variant="primary" onClick={onClose}>Done</Button>}
      description={share ? `Anyone with this link can download ${share.path} until ${dateTime(share.expires_at)}.` : undefined}>
      <CopyField value={url} label="Share link" />
    </Modal>
  );
}

function Editor({ base, path, onClose }: { base: string; path: string; onClose: () => void }) {
  const toast = useToast();
  const doc = useAsync(() => api.call<{ content: string }>("GET", `${base}/content?path=${enc(path)}`), [path]);
  const [text, setText] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const value = text ?? doc.data?.content ?? "";
  const save = async () => {
    setBusy(true);
    try { await api.call("PUT", `${base}/content?path=${enc(path)}`, { content: value, overwrite: true }); setText(null); doc.setData({ content: value }); toast("ok", "Saved"); }
    catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
  };
  return (
    <Modal open onClose={onClose} title={path.split("/").pop() ?? path} description={<span className="mono">{path}</span>}
      footer={<><Button onClick={onClose}>Close</Button><Button variant="primary" loading={busy} disabled={text === null} onClick={save}>Save</Button></>}>
      {doc.error ? <ErrorNote text={doc.error} /> : !doc.data ? <Skeleton h={300} /> : (
        <textarea className="input mono editor" value={value} spellCheck={false} aria-label="File contents"
          onChange={(e) => setText(e.target.value)} rows={22}
          onKeyDown={(e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === "s") { e.preventDefault(); save(); }
            if (e.key === "Tab") { e.preventDefault(); const t = e.currentTarget; const s = t.selectionStart; const v = value.slice(0, s) + "\t" + value.slice(t.selectionEnd); setText(v); requestAnimationFrame(() => { t.selectionStart = t.selectionEnd = s + 1; }); }
          }} />
      )}
    </Modal>
  );
}

function Snapshots({ account }: { account: string }) {
  const toast = useToast();
  const base = `/accounts/${enc(account)}/snapshots`;
  const list = useGet<FileSnapshot[]>(base);
  const [browse, setBrowse] = useState<FileSnapshot | null>(null);
  const { job, start, clear } = useJob((j) => { toast(j.status === "done" ? "ok" : "error", j.status === "done" ? "Snapshot taken" : j.error || "Failed"); list.reload(); });
  const { busy, run } = useAction();
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Snapshots</h2><p className="small muted">Quick restore points of the whole home folder, on this server. Unchanged files take no extra space. For off-site copies, use Backups.</p></div>
        <Button variant="primary" loading={busy === "snap"} onClick={() => run("snap", () => api.call<Job>("POST", base, { keep: 10 })).then((j) => j && start(j))}>Take snapshot</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<Undo2 size={22} />} title="No snapshots yet" text="Take one before a big change, like an update or a theme switch." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Taken</th><th /></tr></thead>
          <tbody>{list.data.map((s) => (
            <tr key={s.id}><td className="lead">{dateTime(s.time)} <span className="muted small">({timeAgo(s.time)})</span></td>
              <td style={{ textAlign: "right" }}><Button size="sm" onClick={() => setBrowse(s)}>Browse and restore</Button>
                <Button size="sm" variant="ghost" icon aria-label="Delete snapshot" onClick={() => run("d" + s.id, () => api.call("DELETE", `${base}/${enc(s.id)}`), "Snapshot deleted").then(() => list.reload())}><Trash2 size={15} /></Button></td></tr>
          ))}</tbody>
        </table></div>
      )}
      {browse ? <SnapshotBrowser base={`${base}/${enc(browse.id)}`} title={dateTime(browse.time)} onClose={() => setBrowse(null)} /> : null}
      <JobModal job={job} onClose={clear} />
    </section>
  );
}

function SnapshotBrowser({ base, title, onClose }: { base: string; title: string; onClose: () => void }) {
  const toast = useToast();
  const [path, setPath] = useState("");
  const list = useAsync(() => api.call<FileEntry[]>("GET", `${base}/files?path=${enc(path)}`), [path]);
  const [overwrite, setOverwrite] = useState(false);
  const restore = async (p: string) => {
    try { await api.call("POST", `${base}/restore`, { path: p, overwrite }); toast("ok", `${p || "Everything"} restored`); } catch (e) { toast("error", (e as Error).message); }
  };
  return (
    <Modal open onClose={onClose} title={`Snapshot from ${title}`} footer={<><Toggle checked={overwrite} onChange={setOverwrite} label="Replace current files" /><Button onClick={onClose}>Close</Button></>}>
      <div className="row" style={{ marginBottom: 8 }}><Button size="sm" variant="ghost" onClick={() => setPath(path.split("/").slice(0, -1).join("/"))} disabled={!path}>Up</Button><span className="mono small">~/{path}</span>
        <Button size="sm" onClick={() => restore(path)} style={{ marginLeft: "auto" }}>Restore this folder</Button></div>
      {!list.data ? <Skeleton /> : <ul style={{ listStyle: "none", padding: 0, margin: 0, maxHeight: 360, overflowY: "auto" }}>{list.data.map((f) => (
        <li key={f.name} className="row" style={{ justifyContent: "space-between", padding: "6px 0", borderBottom: "1px solid var(--line)" }}>
          <button className="linklike row mono small" style={{ gap: 6 }} onClick={() => f.type === "dir" && setPath(join(path, f.name))}>{f.type === "dir" ? <Folder size={14} /> : <File size={14} />}{f.name}</button>
          <Button size="sm" variant="ghost" onClick={() => restore(join(path, f.name))}>Restore</Button>
        </li>))}</ul>}
    </Modal>
  );
}

function Shares({ account }: { account: string }) {
  const base = `/accounts/${enc(account)}/shares`;
  const list = useGet<Share[]>(base);
  const [add, setAdd] = useState(false);
  const [f, setF] = useState({ path: "", expires_hours: 72, password: "", max_downloads: 0 });
  const [made, setMade] = useState<Share | null>(null);
  const { run } = useAction();
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Share links</h2><p className="small muted">Let someone download a file or folder without an account. Links expire, and can need a password or stop after a number of downloads.</p></div>
        <Button variant="primary" onClick={() => setAdd(true)}><Link2 size={16} />New link</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<Link2 size={22} />} title="No share links" text="Select a file in the file manager and choose Share, or make a link here." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>File</th><th>Expires</th><th className="num">Downloads</th><th /></tr></thead>
          <tbody>{list.data.map((s) => (
            <tr key={s.token}>
              <td className="lead mono small">{s.path}{s.folder ? " (folder)" : ""}{s.has_password ? <> <Badge>Password</Badge></> : null}</td>
              <td className="small">{new Date(s.expires_at) < new Date() ? <Badge tone="warn">Expired</Badge> : dateTime(s.expires_at)}</td>
              <td className="num">{s.downloads}{s.max_downloads ? ` / ${s.max_downloads}` : ""}</td>
              <td style={{ textAlign: "right", whiteSpace: "nowrap" }}><Button size="sm" variant="ghost" onClick={() => setMade(s)}>Link</Button>
                <Button size="sm" variant="ghost" icon aria-label="Delete link" onClick={() => run("d", () => api.call("DELETE", `${base}/${enc(s.token)}`), "Link removed").then(() => list.reload())}><Trash2 size={15} /></Button></td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      <FormModal open={add} onClose={() => setAdd(false)} title="New share link" submit="Create link" onSubmit={async () => { setMade(await api.call<Share>("POST", base, f)); list.reload(); }}>
        <TextField label="File or folder (from the home folder)" value={f.path} onChange={(v) => setF({ ...f, path: v })} mono placeholder="web/example.com/public_html/brochure.pdf" />
        <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))" }}>
          <NumberField label="Expires after (hours)" value={f.expires_hours} onChange={(v) => setF({ ...f, expires_hours: v })} min={1} max={720} />
          <NumberField label="Downloads allowed" value={f.max_downloads} onChange={(v) => setF({ ...f, max_downloads: v })} hint="0 = no limit" />
        </div>
        <TextField label="Password (optional)" value={f.password} onChange={(v) => setF({ ...f, password: v })} type="password" />
      </FormModal>
      <OnceShare share={made} onClose={() => setMade(null)} />
    </section>
  );
}

const kindLabel: Record<string, string> = { s3: "Amazon S3", b2: "Backblaze B2", r2: "Cloudflare R2", wasabi: "Wasabi", spaces: "DigitalOcean Spaces", minio: "MinIO", sftp: "SFTP server", local: "Folder on this server" };

export function Storage({ base }: { base: string }) {
  const list = useGet<StorageTarget[]>(base);
  const [edit, setEdit] = useState<StorageTarget | null>(null);
  const { busy, run } = useAction();
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Storage targets</h2><p className="small muted">S3-compatible buckets (Amazon S3, Backblaze B2, Cloudflare R2, Wasabi, Spaces, MinIO), SFTP servers or a local folder. Backups and file uploads go here.</p></div>
        <Button variant="primary" onClick={() => setEdit({ id: "", name: "", kind: "b2", created_at: "" })}>Add storage</Button></div>
      {!list.data ? <div className="panel-body"><Skeleton /></div> : list.data.length === 0 ? <Empty icon={<HardDrive size={22} />} title="No storage yet" text="Add a bucket or server to keep backups off this machine." /> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Name</th><th>Kind</th><th data-hide-sm>Where</th><th>Last check</th><th /></tr></thead>
          <tbody>{list.data.map((t) => (
            <tr key={t.id}>
              <td className="lead">{t.name}</td><td>{kindLabel[t.kind] ?? t.kind}</td>
              <td className="mono small" data-hide-sm>{t.kind === "sftp" ? `${t.user}@${t.host}:${t.prefix}` : t.kind === "local" ? t.prefix : `${t.bucket}/${t.prefix ?? ""}`}</td>
              <td className="small">{t.last_check ? (t.last_check.startsWith("ok") ? <Badge tone="ok">OK</Badge> : <Badge tone="bad" >{t.last_check}</Badge>) : <span className="muted">Not yet</span>}</td>
              <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                <Button size="sm" variant="ghost" loading={busy === "c" + t.id} onClick={() => run("c" + t.id, () => api.call("POST", `${base}/${enc(t.id)}/check`), "Connection works").then(() => list.reload())}>Test</Button>
                <Button size="sm" variant="ghost" icon aria-label="Edit" onClick={() => setEdit(t)}><Pencil size={15} /></Button>
                <Button size="sm" variant="ghost" icon aria-label="Delete" onClick={() => run("d" + t.id, () => api.call("DELETE", `${base}/${enc(t.id)}`), "Storage removed").then(() => list.reload())}><Trash2 size={15} /></Button>
              </td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      {edit ? <TargetModal t={edit} base={base} onClose={() => setEdit(null)} onSaved={() => list.reload()} /> : null}
    </section>
  );
}

function TargetModal({ t, base, onClose, onSaved }: { t: StorageTarget; base: string; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState<StorageTarget>({ ...t, secret_key: "" });
  const s3 = !["sftp", "local"].includes(f.kind);
  const endpointHint: Record<string, string> = { b2: "https://s3.eu-central-003.backblazeb2.com", r2: "https://<account id>.r2.cloudflarestorage.com", wasabi: "https://s3.eu-central-1.wasabisys.com", spaces: "https://fra1.digitaloceanspaces.com", minio: "https://minio.example.com", s3: "https://s3.eu-west-1.amazonaws.com" };
  return (
    <FormModal open onClose={onClose} title={t.id ? `Edit ${t.name}` : "Add storage"} submit="Save" onSubmit={async () => {
      await api.call(t.id ? "PUT" : "POST", t.id ? `${base}/${enc(t.id)}` : base, f); onSaved();
    }}>
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
        <TextField label="Name" value={f.name} onChange={(v) => setF({ ...f, name: v })} placeholder="Off-site backups" />
        <SelectField label="Kind" value={f.kind} onChange={(v) => setF({ ...f, kind: v })} options={Object.entries(kindLabel).map(([value, label]) => ({ value, label }))} />
      </div>
      {s3 ? <>
        <TextField label="Endpoint" value={f.endpoint ?? ""} onChange={(v) => setF({ ...f, endpoint: v })} placeholder={endpointHint[f.kind]} mono />
        <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))" }}>
          <TextField label="Bucket" value={f.bucket ?? ""} onChange={(v) => setF({ ...f, bucket: v })} mono />
          <TextField label="Region" value={f.region ?? ""} onChange={(v) => setF({ ...f, region: v })} placeholder="auto" mono />
          <TextField label="Folder in the bucket" value={f.prefix ?? ""} onChange={(v) => setF({ ...f, prefix: v })} placeholder="ike/server1" mono />
        </div>
        <TextField label="Access key ID" value={f.access_key ?? ""} onChange={(v) => setF({ ...f, access_key: v })} mono />
        <TextField label={`Secret key${t.id ? " (leave empty to keep)" : ""}`} value={f.secret_key ?? ""} onChange={(v) => setF({ ...f, secret_key: v })} type="password" />
      </> : f.kind === "sftp" ? <>
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 100px" }}>
          <TextField label="Host" value={f.host ?? ""} onChange={(v) => setF({ ...f, host: v })} mono />
          <NumberField label="Port" value={f.port ?? 22} onChange={(v) => setF({ ...f, port: v })} />
        </div>
        <TextField label="User" value={f.user ?? ""} onChange={(v) => setF({ ...f, user: v })} mono />
        <TextField label="Folder" value={f.prefix ?? ""} onChange={(v) => setF({ ...f, prefix: v })} mono />
        <p className="small muted">Ike signs in with this server's SSH key. Add the key shown under Backups → Storage to the remote user's authorized_keys.</p>
      </> : <TextField label="Folder" value={f.prefix ?? ""} onChange={(v) => setF({ ...f, prefix: v })} placeholder="/mnt/backup-disk/ike" mono hint="Best on a separate disk." />}
    </FormModal>
  );
}

/* ---------------------------------------------------------------- terminal */

export function TerminalPage({ route, role }: { route: string; role: Role }) {
  if (isDemo) return <DemoTerminal />;
  return <RealTerminal route={route} role={role} />;
}

function RealTerminal({ route, role }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const [target, setTarget] = useState<"account" | "root">("account");
  const [connected, setConnected] = useState(false);
  const [note, setNote] = useState<string | null>(null);
  const box = useRef<HTMLDivElement>(null);
  const [nonce, setNonce] = useState(0);
  useEffect(() => {
    if (!box.current || (target === "account" && !account)) return;
    let disposed = false;
    let cleanup = () => {};
    (async () => {
      const [{ Terminal }, { FitAddon }] = await Promise.all([import("@xterm/xterm"), import("@xterm/addon-fit")]);
      await import("@xterm/xterm/css/xterm.css");
      if (disposed || !box.current) return;
      const css = getComputedStyle(document.documentElement);
      const term = new Terminal({ cursorBlink: true, fontFamily: css.getPropertyValue("--font-mono") || "monospace", fontSize: 14,
        theme: { background: "#0f1115", foreground: "#e6e6e6", cursor: css.getPropertyValue("--accent").trim() || "#4f8cff" } });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(box.current);
      fit.fit();
      const path = target === "root" ? `/terminal?cols=${term.cols}&rows=${term.rows}` : `/accounts/${enc(account)}/terminal?cols=${term.cols}&rows=${term.rows}`;
      const ws = new WebSocket(api.socketUrl(path)!);
      ws.binaryType = "arraybuffer";
      const enc8 = new TextEncoder();
      ws.onopen = () => { setConnected(true); setNote(null); term.focus(); };
      ws.onmessage = (ev) => term.write(typeof ev.data === "string" ? ev.data : new Uint8Array(ev.data));
      ws.onclose = (ev) => { setConnected(false); setNote(ev.reason || "The session ended."); term.write("\r\n\x1b[2m[disconnected]\x1b[0m\r\n"); };
      const d1 = term.onData((d) => { if (ws.readyState === 1) ws.send(enc8.encode(d)); });
      const resize = () => { fit.fit(); if (ws.readyState === 1) ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows })); };
      const ro = new ResizeObserver(resize);
      ro.observe(box.current);
      cleanup = () => { ro.disconnect(); d1.dispose(); ws.close(); term.dispose(); };
    })().catch((e) => setNote((e as Error).message));
    return () => { disposed = true; cleanup(); };
  }, [account, target, nonce]);
  return (
    <div className="stack">
      <PageHead title="Terminal" text="A shell in your browser. Account terminals run as the account's own user, inside its home folder. Sessions end after 30 idle minutes."
        actions={<>{role === "admin" ? <Segmented label="Shell" value={target} onChange={setTarget} options={[{ value: "account", label: "Account" }, { value: "root", label: "Server (root)" }]} /> : null}
          {target === "account" ? picker : null}<Button onClick={() => setNonce(nonce + 1)}><RefreshCw size={16} />Reconnect</Button></>} />
      {note && !connected ? <div className="callout warn">{note}</div> : null}
      <section className="panel" style={{ padding: 8, background: "#0f1115" }}>
        <div ref={box} style={{ height: "min(70vh, 620px)" }} aria-label="Terminal" />
      </section>
    </div>
  );
}
