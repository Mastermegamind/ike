// Developers: people who work inside some hosting accounts, in chosen areas.
import { useState } from "react";
import { KeyRound, UserCog, UserPlus } from "lucide-react";
import { api } from "../api/client";
import type { Person } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Segmented, Skeleton, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead } from "../components/common";
import { FormModal, OnceModal, TextField } from "../components/kit5";

export const developerAreas: { id: string; label: string; help: string }[] = [
  { id: "sites", label: "Websites", help: "settings, deployments, logs, cron jobs and certificates of existing websites" },
  { id: "files", label: "Files", help: "file manager, browser terminal, snapshots and website SFTP users" },
  { id: "databases", label: "Databases", help: "MariaDB, PostgreSQL, MongoDB and Redis" },
  { id: "containers", label: "Containers", help: "Docker containers and Compose stacks" },
  { id: "dns", label: "DNS", help: "zones and records" },
  { id: "mail", label: "Email", help: "mailboxes and forwarders" },
  { id: "backups", label: "Backups", help: "backup plans and restores" },
  { id: "security", label: "Security", help: "malware scans and the web firewall" },
];

type Level = "none" | "read" | "write";

/** DeveloperFields picks a developer's accounts and areas. */
export function DeveloperFields({ accounts, value, onChange }: {
  accounts: string[]; value: { accounts: string[]; areas: Record<string, string> };
  onChange: (v: { accounts: string[]; areas: Record<string, string> }) => void;
}) {
  return (
    <>
      <fieldset style={{ border: 0, padding: 0, margin: 0, display: "grid", gap: 8 }}>
        <legend className="small" style={{ fontWeight: 500, marginBottom: 6 }}>Accounts they work on</legend>
        <div className="row" style={{ flexWrap: "wrap" }}>{accounts.map((a) => (
          <label key={a} className="row" style={{ gap: 6 }}><input type="checkbox" checked={value.accounts.includes(a)}
            onChange={(e) => onChange({ ...value, accounts: e.target.checked ? [...value.accounts, a] : value.accounts.filter((x) => x !== a) })} /><span className="mono">{a}</span></label>
        ))}</div>
      </fieldset>
      <fieldset style={{ border: 0, padding: 0, margin: 0, display: "grid", gap: 4 }}>
        <legend className="small" style={{ fontWeight: 500, marginBottom: 6 }}>What they can do there</legend>
        {developerAreas.map((a) => (
          <div key={a.id} className="scope-row">
            <div style={{ minWidth: 0 }}><strong className="small">{a.label}</strong><div className="small muted">{a.help}</div></div>
            <Segmented<Level> label={a.label} value={(value.areas[a.id] as Level) ?? "none"}
              onChange={(l) => { const areas = { ...value.areas }; if (l === "none") delete areas[a.id]; else areas[a.id] = l; onChange({ ...value, areas }); }}
              options={[{ value: "none", label: "No" }, { value: "read", label: "Look" }, { value: "write", label: "Change" }]} />
          </div>
        ))}
        <p className="small muted" style={{ marginTop: 6 }}>Developers never see billing or plans, can't add or delete websites, accounts or people, and sign in with their own password and two-factor code.</p>
      </fieldset>
    </>
  );
}

export function areaBadges(p: Person) {
  const a = p.areas ?? {};
  return developerAreas.filter((x) => a[x.id]).map((x) => <Badge key={x.id} tone={a[x.id] === "write" ? "accent" : undefined}>{x.label}{a[x.id] === "read" ? " (look)" : ""}</Badge>);
}

function password() {
  const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
  return Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => chars[b % chars.length]).join("");
}

/** DevelopersPage is where account owners invite developers to their accounts. */
export function DevelopersPage() {
  const toast = useToast();
  const people = useAsync(() => api.people());
  const accounts = useAsync(() => api.accounts());
  const [inviting, setInviting] = useState(false);
  const [editing, setEditing] = useState<Person | null>(null);
  const [made, setMade] = useState<{ user: string; password: string } | null>(null);
  const [removing, setRemoving] = useState<Person | null>(null);
  const devs = (people.data ?? []).filter((p) => p.role === "developer");
  const mine = (accounts.data ?? []).map((a) => a.name);

  return (
    <div className="stack">
      <PageHead title="Developers" text="Give a web developer or agency access to your accounts without sharing your own sign-in. Choose what they can do, and remove them when the work is done."
        actions={<Button variant="primary" onClick={() => setInviting(true)} disabled={!mine.length}><UserPlus size={16} />Invite developer</Button>} />
      <section className="panel">
        {!people.data ? <div className="panel-body"><Skeleton h={120} /></div> : devs.length === 0 ? (
          <div className="panel-body"><Empty icon={<UserCog size={22} />} title="No developers yet" text="Invite one to work on your websites, files or databases. They get their own sign-in on this page's address." /></div>
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Developer</th><th>Accounts</th><th>Can do</th><th data-hide-sm>Last sign-in</th><th /></tr></thead>
            <tbody>{devs.map((p) => (
              <tr key={p.username}>
                <td className="lead"><span className="mono">{p.username}</span>{p.name ? <div className="small muted">{p.name}</div> : null}
                  {p.disabled ? <Badge tone="warn">Paused</Badge> : null}</td>
                <td className="mono small">{p.accounts.join(", ")}</td>
                <td><div className="row" style={{ flexWrap: "wrap", gap: 4 }}>{areaBadges(p)}</div></td>
                <td className="small muted" data-hide-sm>{p.last_login_at ? timeAgo(p.last_login_at) : "Never"}</td>
                <td><div className="row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setEditing(p)}>Edit</Button>
                  <Button size="sm" variant="ghost" onClick={async () => {
                    try { await api.updatePerson(p.username, { disabled: !p.disabled }); people.reload(); toast("ok", p.disabled ? `${p.username} can sign in again` : `${p.username} is paused`); }
                    catch (e) { toast("error", (e as Error).message); }
                  }}>{p.disabled ? "Resume" : "Pause"}</Button>
                  <Button size="sm" variant="ghost" onClick={() => setRemoving(p)}>Remove</Button>
                </div></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>

      {inviting ? <InviteModal accounts={mine} onClose={() => setInviting(false)} onDone={(u, pw) => { setMade({ user: u, password: pw }); people.reload(); }} /> : null}
      {editing ? <EditModal person={editing} accounts={mine} onClose={() => setEditing(null)} onDone={() => people.reload()} /> : null}
      <OnceModal open={!!made} onClose={() => setMade(null)} title={`Sign-in for ${made?.user ?? ""}`}>
        <CopyField label="Panel address" value={typeof location !== "undefined" && location.protocol !== "file:" ? location.origin : "https://your-panel:2083"} />
        <CopyField label="Username" value={made?.user ?? ""} />
        <CopyField label="Password" value={made?.password ?? ""} />
        <p className="small muted"><KeyRound size={13} style={{ verticalAlign: -2 }} /> They'll be asked to turn on two-factor sign-in.</p>
      </OnceModal>
      <ConfirmDelete open={!!removing} onClose={() => setRemoving(null)} title={`Remove ${removing?.username ?? ""}?`} name={removing?.username ?? ""} confirmLabel="Remove developer"
        description="They're signed out now and can't sign in again. Their API tokens stop working. Nothing they changed is undone."
        onConfirm={async () => { await api.call("DELETE", `/people/${encodeURIComponent(removing!.username)}`); people.reload(); }} />
    </div>
  );
}

function InviteModal({ accounts, onClose, onDone }: { accounts: string[]; onClose: () => void; onDone: (user: string, pw: string) => void }) {
  const [f, setF] = useState({ username: "", email: "", name: "" });
  const [d, setD] = useState<{ accounts: string[]; areas: Record<string, string> }>({ accounts: accounts.slice(0, 1), areas: { sites: "write", files: "write" } });
  return (
    <FormModal open onClose={onClose} title="Invite developer" submit="Invite" onSubmit={async () => {
      const pw = password();
      await api.createPerson({ username: f.username.trim(), email: f.email.trim(), name: f.name.trim() || undefined, role: "developer", password: pw, accounts: d.accounts, areas: d.areas });
      onDone(f.username.trim(), pw);
    }}>
      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(160px, 1fr))" }}>
        <TextField label="Username" mono value={f.username} onChange={(v) => setF({ ...f, username: v.toLowerCase() })} placeholder="emeka.dev" autoFocus />
        <TextField label="Name (optional)" value={f.name} onChange={(v) => setF({ ...f, name: v })} placeholder="Emeka (Pixelforge)" />
      </div>
      <TextField label="Email" type="email" value={f.email} onChange={(v) => setF({ ...f, email: v })} />
      <DeveloperFields accounts={accounts} value={d} onChange={setD} />
      <Field label="Password">{() => <p className="small muted">Ike makes one up and shows it once, to send them privately.</p>}</Field>
    </FormModal>
  );
}

function EditModal({ person, accounts, onClose, onDone }: { person: Person; accounts: string[]; onClose: () => void; onDone: () => void }) {
  const [d, setD] = useState<{ accounts: string[]; areas: Record<string, string> }>({ accounts: person.accounts, areas: { ...(person.areas ?? {}) } });
  return (
    <FormModal open onClose={onClose} title={`Edit ${person.username}`} submit="Save" onSubmit={async () => {
      await api.updatePerson(person.username, { accounts: d.accounts, areas: d.areas });
      onDone();
    }}>
      <DeveloperFields accounts={accounts} value={d} onChange={setD} />
    </FormModal>
  );
}
