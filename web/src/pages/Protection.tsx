// Password protection for a website: users, and the paths they unlock.
import { useState } from "react";
import { KeyRound, Lock, Plus, Trash2, UserPlus } from "lucide-react";
import { api } from "../api/client";
import type { Site } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Skeleton } from "../components/ui";
import { ConfirmDelete } from "../components/common";
import { ErrorNote, FormModal, OnceModal, TextField, dateTime, enc, useAction, useGet } from "../components/kit5";

export interface ProtectedPath { id?: string; path: string; realm: string; users?: string[]; allow_ips?: string[] }
export interface AuthUser { name: string; created: string }
export interface Protection { paths: ProtectedPath[]; users: AuthUser[] }

export function ProtectionTab({ site }: { site: Site }) {
  const base = `/sites/${enc(site.domain)}/protection`;
  const p = useGet<Protection>(base);
  const { busy, run } = useAction();
  const [editing, setEditing] = useState<{ index: number; path: ProtectedPath } | null>(null);
  const [userModal, setUserModal] = useState<{ name: string; isNew: boolean } | null>(null);
  const [made, setMade] = useState<{ user: string; password: string } | null>(null);
  const [delUser, setDelUser] = useState<string | null>(null);
  if (!p.data) return <section className="panel"><div className="panel-body"><Skeleton h={160} /></div></section>;
  const data = p.data;
  const paths = data.paths ?? [];
  const users = data.users ?? [];
  const savePaths = async (next: ProtectedPath[]) => {
    const out = await api.call<Protection>("PUT", `${base}/paths`, next);
    p.setData(out);
  };
  const whole = paths.find((x) => x.path === "/");

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head">
          <div><h2>Protected paths</h2>
            <p className="small muted">Visitors must sign in before they can open these paths. Protect <span className="mono">/</span> to lock the whole website, for example while it's being built.</p></div>
          <Button variant="primary" disabled={users.length === 0} onClick={() => setEditing({ index: -1, path: { path: whole ? "/admin/" : "/", realm: "", users: [], allow_ips: [] } })}><Plus size={16} />Protect a path</Button>
        </div>
        {users.length === 0 ? (
          <div className="panel-body"><Empty icon={<Lock size={22} />} title="Add a user first" text="Then choose the paths they can open. Users here are only for this website, not panel accounts." /></div>
        ) : paths.length === 0 ? (
          <div className="panel-body"><Empty icon={<Lock size={22} />} title="Nothing is protected yet" text="Common choices: / for the whole website, /wp-admin/ and /wp-login.php for WordPress, or a /private/ folder." /></div>
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Path</th><th>Sign-in message</th><th>Who can open it</th><th data-hide-sm>No password from</th><th /></tr></thead>
            <tbody>{paths.map((x, i) => (
              <tr key={x.id ?? x.path}>
                <td className="lead mono">{x.path === "/" ? <>/ <span className="small muted" style={{ fontFamily: "var(--font-ui)" }}>whole website</span></> : x.path}</td>
                <td>{x.realm}</td>
                <td>{x.users?.length ? x.users.map((u) => <Badge key={u}>{u}</Badge>) : <span className="muted">Every user</span>}</td>
                <td className="small mono" data-hide-sm>{(x.allow_ips ?? []).join(", ") || <span className="muted" style={{ fontFamily: "var(--font-ui)" }}>Nobody</span>}</td>
                <td><div className="row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setEditing({ index: i, path: { ...x, users: [...(x.users ?? [])], allow_ips: [...(x.allow_ips ?? [])] } })}>Edit</Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Stop protecting ${x.path}`} loading={busy === "rm" + i}
                    onClick={() => run("rm" + i, () => savePaths(paths.filter((_, j) => j !== i)), `${x.path} is open again`)}><Trash2 size={15} /></Button>
                </div></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head">
          <div><h2>Users</h2><p className="small muted">Names and passwords for this website's protected paths. Share each password privately.</p></div>
          <Button onClick={() => setUserModal({ name: "", isNew: true })}><UserPlus size={16} />Add user</Button>
        </div>
        {users.length === 0 ? <div className="panel-body"><p className="muted">No users yet.</p></div> : (
          <div className="table-wrap"><table>
            <thead><tr><th>User</th><th data-hide-sm>Added</th><th /></tr></thead>
            <tbody>{users.map((u) => (
              <tr key={u.name}>
                <td className="lead mono">{u.name}</td>
                <td className="small muted" data-hide-sm>{dateTime(u.created)}</td>
                <td><div className="row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setUserModal({ name: u.name, isNew: false })}><KeyRound size={15} />New password</Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Remove ${u.name}`} onClick={() => setDelUser(u.name)}><Trash2 size={15} /></Button>
                </div></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>

      <p className="small muted">Ike checks passwords in {site.web_server === "apache" ? "Apache" : "Nginx"}, before the request reaches {site.runtime === "php" ? "PHP" : "the website"}, so a website's own login and .htaccess files can't skip it. Certificate renewal and Ike's error pages always stay reachable.</p>

      {editing ? <PathModal value={editing.path} users={users.map((u) => u.name)} isNew={editing.index < 0} onClose={() => setEditing(null)}
        onSave={async (v) => {
          const next = editing.index < 0 ? [...paths, v] : paths.map((x, i) => (i === editing.index ? v : x));
          await savePaths(next);
        }} /> : null}
      {userModal ? <UserModal name={userModal.name} isNew={userModal.isNew} onClose={() => setUserModal(null)} onSave={async (name, password) => {
        const r = await api.call<{ user: string; password?: string }>("PUT", `${base}/users/${enc(name)}`, { password });
        if (r.password) setMade({ user: name, password: r.password });
        p.reload();
      }} /> : null}
      <OnceModal open={!!made} onClose={() => setMade(null)} title={`Password for ${made?.user ?? ""}`}>
        <CopyField label="User" value={made?.user ?? ""} />
        <CopyField label="Password" value={made?.password ?? ""} />
      </OnceModal>
      <ConfirmDelete open={!!delUser} onClose={() => setDelUser(null)} title={`Remove ${delUser ?? ""}?`} name={delUser ?? ""} confirmLabel="Remove user"
        description="They can no longer sign in to this website's protected paths."
        onConfirm={async () => { await api.call("DELETE", `${base}/users/${enc(delUser!)}`); p.reload(); }} />
    </div>
  );
}

function PathModal({ value, users, isNew, onClose, onSave }: { value: ProtectedPath; users: string[]; isNew: boolean; onClose: () => void; onSave: (v: ProtectedPath) => Promise<void> }) {
  const [v, setV] = useState<ProtectedPath>(value);
  const [ips, setIps] = useState((value.allow_ips ?? []).join("\n"));
  const all = !v.users?.length;
  return (
    <FormModal open onClose={onClose} title={isNew ? "Protect a path" : `Edit ${value.path}`} submit={isNew ? "Protect" : "Save"}
      onSubmit={() => onSave({ ...v, allow_ips: ips.split(/[\s,]+/).filter(Boolean) })}>
      <TextField label="Path" mono value={v.path} onChange={(path) => setV({ ...v, path })} placeholder="/admin/"
        hint="/ is the whole website. /admin/ also covers everything inside it; /wp-login.php covers one page." />
      <TextField label="Sign-in message" value={v.realm} onChange={(realm) => setV({ ...v, realm })} placeholder="Restricted area" hint="Browsers show it in the sign-in box." />
      <Field label="Who can open it">{() => (
        <div className="stack" style={{ gap: 6 }}>
          <label className="row small"><input type="radio" checked={all} onChange={() => setV({ ...v, users: [] })} /> Every user of this website</label>
          <label className="row small"><input type="radio" checked={!all} onChange={() => setV({ ...v, users: users.slice(0, 1) })} /> Only these users</label>
          {!all ? <div className="row" style={{ flexWrap: "wrap", gap: 10, paddingLeft: 22 }}>{users.map((u) => (
            <label key={u} className="row small"><input type="checkbox" checked={v.users?.includes(u)}
              onChange={(e) => setV({ ...v, users: e.target.checked ? [...(v.users ?? []), u] : (v.users ?? []).filter((x) => x !== u) })} /> <span className="mono">{u}</span></label>
          ))}</div> : null}
        </div>
      )}</Field>
      <Field label="Let these addresses in without a password (optional)" hint="One IP address or range per line, such as your office: 203.0.113.0/24">{(id) => (
        <textarea id={id} className="input mono" rows={3} value={ips} onChange={(e) => setIps(e.target.value)} />
      )}</Field>
      {!all && !v.users?.length ? <ErrorNote text="Choose at least one user." /> : null}
    </FormModal>
  );
}

function UserModal({ name, isNew, onClose, onSave }: { name: string; isNew: boolean; onClose: () => void; onSave: (name: string, password: string) => Promise<void> }) {
  const [n, setN] = useState(name);
  const [pw, setPw] = useState("");
  return (
    <FormModal open onClose={onClose} title={isNew ? "Add user" : `New password for ${name}`} submit={isNew ? "Add user" : "Change password"} onSubmit={() => onSave(n.trim(), pw)}>
      {isNew ? <TextField label="User name" mono value={n} onChange={setN} placeholder="ada" autoFocus hint="Letters, digits and . _ @ -" /> : null}
      <TextField label="Password" type="password" value={pw} onChange={setPw} hint="At least 8 characters. Leave it empty and Ike makes one up and shows it once." />
    </FormModal>
  );
}
