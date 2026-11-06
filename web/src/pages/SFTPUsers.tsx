// Website SFTP users: logins that only reach one website's folder.
import { useState } from "react";
import { FolderLock, KeyRound, Plus, Trash2 } from "lucide-react";
import { api } from "../api/client";
import type { Site, SSHKey } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Skeleton } from "../components/ui";
import { ConfirmDelete } from "../components/common";
import { FormModal, OnceModal, TextField, dateTime, enc, useAction, useGet } from "../components/kit5";

export interface SFTPUser { name: string; domain: string; folder: string; read_only?: boolean; password: boolean; keys: SSHKey[]; expires?: string; created: string }
interface Made { user: string; password: string; host: string }

export function SFTPUsersTab({ site }: { site: Site }) {
  const base = `/accounts/${enc(site.account)}/sftp-users`;
  const list = useGet<SFTPUser[]>(base);
  const { busy, run } = useAction();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<SFTPUser | null>(null);
  const [made, setMade] = useState<Made | null>(null);
  const [del, setDel] = useState<SFTPUser | null>(null);
  const host = typeof location !== "undefined" && location.hostname && location.protocol !== "file:" ? location.hostname : `server.${site.domain}`;
  if (!list.data) return <section className="panel"><div className="panel-body"><Skeleton h={140} /></div></section>;
  const users = (list.data ?? []).filter((u) => u.domain === site.domain);

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head">
          <div><h2>SFTP users for this website</h2>
            <p className="small muted">For a designer or developer who should only see this website. Files they upload belong to the account, so the website can still change them.</p></div>
          <Button variant="primary" onClick={() => setAdding(true)}><Plus size={16} />Add SFTP user</Button>
        </div>
        {users.length === 0 ? (
          <div className="panel-body"><Empty icon={<FolderLock size={22} />} title="No SFTP users for this website" text="The account owner's own SFTP login (on the account) reaches every website. Add one here to share just this one." /></div>
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>User</th><th>Folder</th><th>Access</th><th data-hide-sm>Signs in with</th><th data-hide-sm>Stops working</th><th /></tr></thead>
            <tbody>{users.map((u) => (
              <tr key={u.name}>
                <td className="lead mono">{u.name}<div className="small muted" style={{ fontFamily: "var(--font-ui)" }}>added {dateTime(u.created)}</div></td>
                <td className="mono small">{u.folder ? `${u.folder}/` : <span style={{ fontFamily: "var(--font-ui)" }}>Whole website</span>}</td>
                <td>{u.read_only ? <Badge>Download only</Badge> : <Badge tone="accent">Upload and edit</Badge>}</td>
                <td className="small" data-hide-sm>{[u.password ? "password" : "", (u.keys ?? []).length ? `${u.keys.length} key${u.keys.length === 1 ? "" : "s"}` : ""].filter(Boolean).join(" and ")}</td>
                <td className="small" data-hide-sm>{u.expires || <span className="muted">Never</span>}</td>
                <td><div className="row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(u)}>Edit</Button>
                  <Button size="sm" variant="ghost" loading={busy === "pw" + u.name} onClick={() => run("pw" + u.name, async () => {
                    const r = await api.call<{ password: string }>("PATCH", `${base}/${enc(u.name)}`, { password: "generate" });
                    setMade({ user: u.name, password: r.password, host }); list.reload();
                  })}><KeyRound size={15} />New password</Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Remove ${u.name}`} onClick={() => setDel(u)}><Trash2 size={15} /></Button>
                </div></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <p className="small muted">They connect with any SFTP app (FileZilla, WinSCP, Cyberduck) to <span className="mono">{host}</span>, port 22, and only ever see their folder. They can't open a shell. Suspending the account stops them too.</p>

      {adding ? <AddModal site={site} onClose={() => setAdding(false)} onDone={(r) => { if (r.password) setMade({ user: r.user, password: r.password, host }); list.reload(); }} base={base} /> : null}
      {editing ? <EditModal user={editing} base={base} onClose={() => setEditing(null)} onDone={() => list.reload()} /> : null}
      <OnceModal open={!!made} onClose={() => setMade(null)} title={`Sign-in for ${made?.user ?? ""}`}>
        <CopyField label="Server" value={made?.host ?? ""} />
        <CopyField label="User" value={made?.user ?? ""} />
        <CopyField label="Password" value={made?.password ?? ""} />
        <p className="small muted">Protocol SFTP, port 22.</p>
      </OnceModal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Remove ${del?.name ?? ""}?`} name={del?.name ?? ""} confirmLabel="Remove SFTP user"
        description="They can't sign in any more. The website's files stay as they are."
        onConfirm={async () => { await api.call("DELETE", `${base}/${enc(del!.name)}`); list.reload(); }} />
    </div>
  );
}

function AccessFields({ folder, setFolder, readOnly, setReadOnly, expires, setExpires }: {
  folder: string; setFolder: (v: string) => void; readOnly: boolean; setReadOnly: (v: boolean) => void; expires: string; setExpires: (v: string) => void;
}) {
  return (
    <>
      <Field label="Folder they can reach">{() => (
        <div className="stack" style={{ gap: 6 }}>
          <label className="row small"><input type="radio" checked={folder === "public_html"} onChange={() => setFolder("public_html")} /> <span>The public website files (<span className="mono">public_html/</span>)</span></label>
          <label className="row small"><input type="radio" checked={folder === ""} onChange={() => setFolder("")} /> <span>The whole website folder, including settings files like <span className="mono">.env</span></span></label>
          <label className="row small"><input type="radio" checked={folder !== "" && folder !== "public_html"} onChange={() => setFolder("public_html/wp-content")} /> Another folder</label>
          {folder !== "" && folder !== "public_html" ? <input className="input mono" value={folder} onChange={(e) => setFolder(e.target.value)} aria-label="Folder inside the website" /> : null}
        </div>
      )}</Field>
      <Field label="Access">{() => (
        <div className="stack" style={{ gap: 6 }}>
          <label className="row small"><input type="radio" checked={!readOnly} onChange={() => setReadOnly(false)} /> Upload, change and delete files</label>
          <label className="row small"><input type="radio" checked={readOnly} onChange={() => setReadOnly(true)} /> Download only</label>
        </div>
      )}</Field>
      <Field label="Stops working after (optional)" hint="Good for a contractor's login.">{(id) => <input id={id} className="input" type="date" value={expires} onChange={(e) => setExpires(e.target.value)} />}</Field>
    </>
  );
}

function AddModal({ site, base, onClose, onDone }: { site: Site; base: string; onClose: () => void; onDone: (r: { user: string; password: string }) => void }) {
  const [name, setName] = useState("");
  const [folder, setFolder] = useState("public_html");
  const [readOnly, setReadOnly] = useState(false);
  const [expires, setExpires] = useState("");
  const [key, setKey] = useState("");
  const [pw, setPw] = useState("");
  return (
    <FormModal open onClose={onClose} title="Add SFTP user" submit="Add SFTP user"
      description={`They'll only see ${site.domain}.`}
      onSubmit={async () => {
        const r = await api.call<{ user: SFTPUser; password: string }>("POST", base, {
          name: name.trim(), domain: site.domain, folder, read_only: readOnly, expires, public_key: key.trim(), ...(pw ? { password: pw } : {}),
        });
        onDone({ user: r.user.name, password: r.password });
      }}>
      <Field label="Name" hint={`Short, lowercase: they sign in as ${site.account}_${name.trim() || "name"}`}>{(id) => (
        <div className="input-group"><span className="addon mono">{site.account}_</span><input id={id} className="input mono" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} placeholder="designer" autoFocus /></div>
      )}</Field>
      <AccessFields folder={folder} setFolder={setFolder} readOnly={readOnly} setReadOnly={setReadOnly} expires={expires} setExpires={setExpires} />
      <Field label="Public key (optional)" hint="Paste their SSH key, or leave it empty to sign in with a password.">{(id) => (
        <textarea id={id} className="input mono" rows={2} value={key} onChange={(e) => setKey(e.target.value)} placeholder="ssh-ed25519 AAAA… them@laptop" />
      )}</Field>
      <TextField label="Password (optional)" type="password" value={pw} onChange={setPw} hint="At least 10 characters. Leave both empty and Ike makes a password up and shows it once." />
    </FormModal>
  );
}

function EditModal({ user, base, onClose, onDone }: { user: SFTPUser; base: string; onClose: () => void; onDone: () => void }) {
  const [folder, setFolder] = useState(user.folder);
  const [readOnly, setReadOnly] = useState(!!user.read_only);
  const [expires, setExpires] = useState(user.expires ?? "");
  const [key, setKey] = useState("");
  return (
    <FormModal open onClose={onClose} title={`Edit ${user.name}`} submit="Save" onSubmit={async () => {
      await api.call("PATCH", `${base}/${enc(user.name)}`, { folder, read_only: readOnly, expires, ...(key.trim() ? { public_key: key.trim() } : {}) });
      onDone();
    }}>
      <AccessFields folder={folder} setFolder={setFolder} readOnly={readOnly} setReadOnly={setReadOnly} expires={expires} setExpires={setExpires} />
      {(user.keys ?? []).length ? <Field label="Keys">{() => (
        <ul className="small" style={{ margin: 0, paddingLeft: 18 }}>{user.keys.map((k) => <li key={k.fingerprint}>{k.comment || k.type} <span className="mono muted">{k.fingerprint}</span></li>)}</ul>
      )}</Field> : null}
      <Field label="Add a public key (optional)">{(id) => <textarea id={id} className="input mono" rows={2} value={key} onChange={(e) => setKey(e.target.value)} placeholder="ssh-ed25519 AAAA…" />}</Field>
    </FormModal>
  );
}
