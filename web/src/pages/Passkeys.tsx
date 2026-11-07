// Passkeys: sign in with a fingerprint, face, PIN or security key.
import { useState } from "react";
import { CircleAlert, Fingerprint, KeyRound, Trash2 } from "lucide-react";
import { api, isDemo } from "../api/client";
import { passkeyBlocker, type PasskeyInfo } from "../api/passkey";
import { Badge, Button, Skeleton, timeAgo } from "../components/ui";
import { ConfirmDelete } from "../components/common";
import { FormModal, TextField, enc, useGet } from "../components/kit5";

function guessName() {
  const ua = typeof navigator !== "undefined" ? navigator.userAgent : "";
  if (/iPhone|iPad/.test(ua)) return "iPhone";
  if (/Android/.test(ua)) return "Android phone";
  if (/Mac/.test(ua)) return "Mac";
  if (/Windows/.test(ua)) return "Windows Hello";
  return "Passkey";
}

export function PasskeysPanel() {
  const list = useGet<PasskeyInfo[]>("/me/passkeys");
  const [adding, setAdding] = useState(false);
  const [renaming, setRenaming] = useState<PasskeyInfo | null>(null);
  const [del, setDel] = useState<PasskeyInfo | null>(null);
  const blocked = isDemo ? null : passkeyBlocker();
  const keys = list.data ?? [];

  return (
    <section className="panel" style={{ maxWidth: 720 }}>
      <div className="panel-head">
        <div className="row" style={{ gap: 10, flexWrap: "nowrap", alignItems: "flex-start" }}><Fingerprint size={20} aria-hidden style={{ flexShrink: 0, marginTop: 2 }} /><div style={{ minWidth: 0 }}><h2>Passkeys</h2>
          <p className="small muted">Sign in with your fingerprint, face, phone or a security key instead of a password and code. A passkey only works on this panel's address, so a fake sign-in page can't use it.</p></div></div>
      </div>
      <div className="panel-body stack">
        {blocked ? <div className="callout warn"><CircleAlert size={18} /><div>{blocked}</div></div> : null}
        {!list.data ? <Skeleton h={80} /> : keys.length === 0 ? (
          <p className="muted">No passkeys yet. Add one on each device you sign in from, or a security key you keep with you.</p>
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Passkey</th><th data-hide-sm>Added</th><th>Last used</th><th /></tr></thead>
            <tbody>{keys.map((k) => (
              <tr key={k.id}>
                <td className="lead">{k.name}
                  <div className="row" style={{ gap: 4, marginTop: 4, flexWrap: "wrap" }}>
                    {k.synced ? <Badge>Synced to your account</Badge> : <Badge>On one device</Badge>}
                    {k.disabled ? <Badge tone="bad">Turned off: it may have been copied</Badge> : null}
                  </div></td>
                <td className="small muted" data-hide-sm>{timeAgo(k.created_at)}</td>
                <td className="small">{k.last_used_at && !k.last_used_at.startsWith("0001") ? timeAgo(k.last_used_at) : <span className="muted">Never</span>}</td>
                <td><div className="row-actions">
                  <Button size="sm" variant="ghost" onClick={() => setRenaming(k)}>Rename</Button>
                  <Button size="sm" variant="ghost" icon aria-label={`Remove ${k.name}`} onClick={() => setDel(k)}><Trash2 size={15} /></Button>
                </div></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
        <div><Button variant="primary" onClick={() => setAdding(true)} disabled={!!blocked}><KeyRound size={16} />Add a passkey</Button></div>
        <p className="small muted">Keep your password and authenticator app too: they still work if you lose every device with a passkey.</p>
      </div>

      {adding ? <AddModal onClose={() => setAdding(false)} onDone={() => list.reload()} /> : null}
      {renaming ? <RenameModal k={renaming} onClose={() => setRenaming(null)} onDone={() => list.reload()} /> : null}
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Remove ${del?.name ?? ""}?`} name={del?.name ?? ""} confirmLabel="Remove passkey"
        description="It can't sign in to this panel any more. Remove it from the device or password manager too."
        onConfirm={async () => { await api.call("DELETE", `/me/passkeys/${enc(del!.id)}`); list.reload(); }} />
    </section>
  );
}

function AddModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(guessName());
  const [password, setPassword] = useState("");
  return (
    <FormModal open onClose={onClose} title="Add a passkey" submit="Continue"
      description="Your browser asks you to confirm with your fingerprint, face, screen lock or security key."
      onSubmit={async () => { await api.addPasskey(name.trim(), password); onDone(); }}>
      <TextField label="Name" value={name} onChange={setName} hint="So you can tell your passkeys apart, e.g. Work laptop." autoFocus />
      <TextField label="Your password" type="password" value={password} onChange={setPassword} hint="Adding a way to sign in needs your password." />
    </FormModal>
  );
}

function RenameModal({ k, onClose, onDone }: { k: PasskeyInfo; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState(k.name);
  return (
    <FormModal open onClose={onClose} title="Rename passkey" submit="Save" onSubmit={async () => {
      await api.call("PATCH", `/me/passkeys/${enc(k.id)}`, { name: name.trim() }); onDone();
    }}>
      <TextField label="Name" value={name} onChange={setName} autoFocus />
    </FormModal>
  );
}
