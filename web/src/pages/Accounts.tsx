import { useState } from "react";
import { KeyRound, Pause, Play, Plus, Trash2, Users } from "lucide-react";
import { api } from "../api/client";
import { usernameError } from "../api/demo";
import type { Account, Role, SSHKey, SSHMode } from "../api/types";
import { Badge, Button, Empty, Field, Modal, Segmented, Skeleton, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead } from "../components/common";
import { href } from "../router";

const sshLabel: Record<SSHMode, string> = { none: "No login", sftp: "SFTP only", shell: "Full shell" };

export function Accounts({ role }: { role: Role }) {
  const toast = useToast();
  const list = useAsync(() => api.accounts());
  const [creating, setCreating] = useState(false);
  const [open, setOpen] = useState<Account | null>(null);
  const rows = (list.data ?? []).filter((a) => role === "admin" || a.owner === "reseller:webafrica");

  return (
    <div className="stack">
      <PageHead
        title="Accounts"
        text={role === "reseller"
          ? "Your customers. Each account is isolated from every other account on the server."
          : "Hosting accounts on this server. Each one is an isolated Linux user that can hold many websites."}
        actions={<Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />Create account</Button>}
      />
      <section className="panel">
        {list.loading && !list.data ? (
          <div className="panel-body stack"><Skeleton /><Skeleton /><Skeleton /></div>
        ) : rows.length === 0 ? (
          <Empty icon={<Users size={22} />} title="No accounts yet" text="Create an account for each customer or project, then add websites to it."
            action={<Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />Create account</Button>} />
        ) : (
          <div className="table-wrap">
            <table className="responsive">
              <thead><tr><th>Account</th><th>Websites</th><th>Plan</th><th>SSH</th><th>Status</th><th className="num">Created</th></tr></thead>
              <tbody>
                {rows.map((a) => (
                  <tr key={a.name} className="clickable" onClick={() => setOpen(a)}>
                    <td className="lead"><button className="btn ghost" style={{ padding: 0, height: "auto", fontWeight: 600 }}>{a.name}</button>
                      {role === "admin" && a.owner?.startsWith("reseller:") ? <div className="small muted">via {a.owner.slice(9)}</div> : null}</td>
                    <td>{a.sites} {a.sites === 1 ? "website" : "websites"}</td>
                    <td data-hide-sm>{a.plan}</td>
                    <td data-hide-sm><Badge>{sshLabel[a.ssh]}</Badge></td>
                    <td>{a.suspended ? <Badge tone="bad">Suspended</Badge> : <Badge tone="ok">Active</Badge>}</td>
                    <td className="num muted" data-hide-sm>{timeAgo(a.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      <CreateAccount open={creating} onClose={() => setCreating(false)} onDone={(a) => { list.reload(); toast("ok", `Created account ${a.name}`); }} />
      {open ? <AccountDetail account={open} onClose={() => setOpen(null)} onChange={() => list.reload()} /> : null}
    </div>
  );
}

function CreateAccount(props: { open: boolean; onClose: () => void; onDone: (a: Account) => void }) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const local = name ? usernameError(name) : null;
  const close = () => { setName(""); setError(null); props.onClose(); };
  return (
    <Modal open={props.open} onClose={close} title="Create account"
      description="The account name becomes its Linux username and the prefix of its databases."
      footer={<><Button onClick={close}>Cancel</Button>
        <Button variant="primary" loading={busy} disabled={!name || !!local} onClick={async () => {
          setBusy(true); setError(null);
          try { const a = await api.createAccount(name); props.onDone(a); close(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>Create account</Button></>}>
      <Field label="Account name" error={local || error} hint="3–16 lowercase letters or numbers, e.g. adaeze">
        {(id) => <input id={id} className="input mono" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} autoFocus autoComplete="off" spellCheck={false} />}
      </Field>
    </Modal>
  );
}

function AccountDetail({ account, onClose, onChange }: { account: Account; onClose: () => void; onChange: () => void }) {
  const toast = useToast();
  const [a, setA] = useState(account);
  const keys = useAsync(() => api.sshKeys(a.name), [a.name]);
  const [newKey, setNewKey] = useState("");
  const [keyOpts, setKeyOpts] = useState({ access: "" as string, from: "", expires: "" });
  const [keyErr, setKeyErr] = useState<string | null>(null);
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(label);
    try { await fn(); onChange(); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
  };

  return (
    <Modal open onClose={onClose} title={a.name} description={`${a.sites} ${a.sites === 1 ? "website" : "websites"}, created ${timeAgo(a.created_at)}`}>
      <div className="stack">
        {a.suspended ? (
          <div className="callout bad"><Pause size={18} /><div><strong>Suspended.</strong> <span className="muted">{a.suspend_reason || "No reason given."} Websites show the suspended page; SSH, apps and cron jobs are off.</span></div></div>
        ) : null}

        <div className="field">
          <label>SSH access</label>
          <Segmented<SSHMode> label="SSH access" value={a.ssh}
            onChange={(m) => run("ssh", async () => { setA(await api.setSSH(a.name, m)); toast("ok", `SSH set to ${sshLabel[m].toLowerCase()}`); })}
            options={[{ value: "none", label: "No login" }, { value: "sftp", label: "SFTP only" }, { value: "shell", label: "Full shell" }]} />
          <div className="hint">{a.ssh === "shell" ? "Full shell isn't jailed. Only give it to people you trust." : a.ssh === "sftp" ? "File transfer only, locked to this account's home folder." : "Nobody can log in over SSH or SFTP."} To share just one website, add an SFTP user on that website's SFTP tab.</div>
        </div>

        <div className="field">
          <label><KeyRound size={14} style={{ display: "inline", verticalAlign: -2 }} /> SSH keys</label>
          {(keys.data ?? []).length === 0 ? <p className="small muted">No keys yet. Paste a public key to allow logins.</p> : (
            <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "grid", gap: 6 }}>
              {(keys.data as SSHKey[]).map((k) => (
                <li key={k.fingerprint} className="copy" style={{ paddingLeft: 12 }}>
                  <div style={{ flex: 1, minWidth: 0, padding: "8px 0" }}>
                    <div>{k.comment || k.type}</div>
                    <div className="small muted mono" style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{k.fingerprint}</div>
                    <div className="small muted">{keyAccessLabel(k)}</div>
                  </div>
                  <select className="select" style={{ width: "auto", height: 32 }} aria-label={`Access for ${k.comment || k.type}`} value={k.access ?? ""}
                    onChange={(e) => run("key", async () => {
                      await api.call("PUT", `/accounts/${encodeURIComponent(a.name)}/ssh/keys?fingerprint=${encodeURIComponent(k.fingerprint)}`, { access: e.target.value, allow_from: k.allow_from ?? [], expires: k.expires ?? "" });
                      keys.reload(); toast("ok", "Key access changed");
                    })}>
                    <option value="">{a.ssh === "shell" ? "Full shell" : "File transfer"}</option><option value="sftp">SFTP only</option><option value="sftp-readonly">Read-only SFTP</option>
                  </select>
                  <Button variant="ghost" size="sm" icon aria-label={`Remove key ${k.comment}`}
                    onClick={() => run("key", async () => { await api.removeSSHKey(a.name, k.fingerprint); keys.reload(); toast("ok", "Key removed"); })}>
                    <Trash2 size={15} />
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <textarea className="textarea" rows={2} placeholder="ssh-ed25519 AAAA… you@laptop" value={newKey} onChange={(e) => { setNewKey(e.target.value); setKeyErr(null); }} aria-label="New public key" />
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(150px, 1fr))" }}>
            <label className="small" style={{ display: "grid", gap: 4 }}>This key can
              <select className="select" value={keyOpts.access} onChange={(e) => setKeyOpts({ ...keyOpts, access: e.target.value as SSHKey["access"] & string })}>
                <option value="">Do what the account allows</option><option value="sftp">Transfer files only</option><option value="sftp-readonly">Download files only</option>
              </select></label>
            <label className="small" style={{ display: "grid", gap: 4 }}>Only from (IPs, optional)
              <input className="input mono" value={keyOpts.from} onChange={(e) => setKeyOpts({ ...keyOpts, from: e.target.value })} placeholder="203.0.113.0/24" /></label>
            <label className="small" style={{ display: "grid", gap: 4 }}>Stops working after
              <input className="input" type="date" value={keyOpts.expires} onChange={(e) => setKeyOpts({ ...keyOpts, expires: e.target.value })} /></label>
          </div>
          {keyErr ? <div className="error">{keyErr}</div> : null}
          <div><Button size="sm" disabled={!newKey.trim()} loading={busy === "add"} onClick={async () => {
            setBusy("add");
            try {
              await api.call("POST", `/accounts/${encodeURIComponent(a.name)}/ssh/keys`, { public_key: newKey.trim(), access: keyOpts.access,
                allow_from: keyOpts.from.split(/[\s,]+/).filter(Boolean), expires: keyOpts.expires });
              setNewKey(""); setKeyOpts({ access: "", from: "", expires: "" }); keys.reload(); toast("ok", "Key added");
            } catch (e) { setKeyErr((e as Error).message); } finally { setBusy(null); }
          }}>Add key</Button></div>
        </div>

        <div className="row">
          <a className="btn sm" href={href(`/sites?account=${a.name}`)}>Websites</a>
          <a className="btn sm" href={href(`/cron?account=${a.name}`)}>Cron jobs</a>
          <a className="btn sm" href={href(`/databases?account=${a.name}`)}>Databases</a>
        </div>

        <div className="panel" style={{ padding: 16, display: "grid", gap: 12 }}>
          {a.suspended ? (
            <Button loading={busy === "unsuspend"} onClick={() => run("unsuspend", async () => { setA(await api.unsuspendAccount(a.name)); toast("ok", `Reactivated ${a.name}`); })}>
              <Play size={16} />Reactivate account
            </Button>
          ) : (
            <>
              <Field label="Suspend reason (optional)">
                {(id) => <input id={id} className="input" value={reason} onChange={(e) => setReason(e.target.value)} placeholder="e.g. Invoice unpaid" />}
              </Field>
              <Button loading={busy === "suspend"} onClick={() => run("suspend", async () => { setA(await api.suspendAccount(a.name, reason)); toast("ok", `Suspended ${a.name}`); })}>
                <Pause size={16} />Suspend account
              </Button>
            </>
          )}
          <Button variant="ghost" style={{ color: "var(--bad)" }} onClick={() => setDeleting(true)}><Trash2 size={16} />Delete account</Button>
        </div>
      </div>
      <ConfirmDelete open={deleting} onClose={() => setDeleting(false)} title={`Delete ${a.name}?`} name={a.name}
        description="This removes the Linux user, every website, database and file in the account. It can't be undone."
        confirmLabel="Delete account"
        onConfirm={async () => { await api.deleteAccount(a.name, true); toast("ok", `Deleted ${a.name}`); onChange(); onClose(); }} />
    </Modal>
  );
}

function keyAccessLabel(k: SSHKey) {
  const parts = [k.access === "sftp" ? "SFTP only" : k.access === "sftp-readonly" ? "Read-only SFTP" : ""];
  if (k.allow_from?.length) parts.push(`from ${k.allow_from.join(", ")}`);
  if (k.expires) parts.push(`until ${k.expires}`);
  return parts.filter(Boolean).join(" · ");
}
