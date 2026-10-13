// Databases and cron jobs: both belong to an account, so they share an account picker.
import { useEffect, useState } from "react";
import { CalendarClock, Database as DbIcon, ExternalLink, Pencil, Plus, Trash2 } from "lucide-react";
import { api } from "../api/client";
import { cronError, describeCron } from "../api/demo";
import type { CronJob, NewDatabase } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Modal, Skeleton, timeAgo, useAsync, useToast } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";

export function useAccountParam(route: string) {
  const accounts = useAsync(() => api.accounts());
  const initial = new URLSearchParams(route.split("?")[1] || "").get("account") || "";
  const [account, setAccount] = useState(initial);
  useEffect(() => { if (!account && accounts.data?.length) setAccount(accounts.data[0].name); }, [accounts.data, account]);
  const picker = (
    <select className="select" style={{ width: "auto", minWidth: 180 }} value={account} onChange={(e) => setAccount(e.target.value)} aria-label="Account">
      {(accounts.data ?? []).map((a) => <option key={a.name} value={a.name}>{a.name}</option>)}
    </select>
  );
  return { account, picker };
}

export function Databases({ route }: { route: string }) {
  const toast = useToast();
  const { account, picker } = useAccountParam(route);
  const list = useAsync(() => (account ? api.databases(account) : Promise.resolve([])), [account]);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [created, setCreated] = useState<NewDatabase | null>(null);
  const [del, setDel] = useState<string | null>(null);

  return (
    <div className="stack">
      <PageHead title="Databases" text="MariaDB databases. Names start with the account name so accounts never clash."
        actions={<>{picker}
          <a className="btn" href="#/soon/phpmyadmin"><ExternalLink size={16} />phpMyAdmin</a>
          <Button variant="primary" onClick={() => setCreating(true)} disabled={!account}><Plus size={17} />Create database</Button></>} />
      <section className="panel">
        {list.loading && !list.data ? <div className="panel-body"><Skeleton /></div> : (list.data ?? []).length === 0 ? (
          <Empty icon={<DbIcon size={22} />} title={`No databases in ${account}`} text="Create one for an app, or install WordPress and Ike makes it for you."
            action={<Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />Create database</Button>} />
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Database</th><th>User</th><th>Note</th><th className="num">Created</th><th /></tr></thead>
            <tbody>{(list.data ?? []).map((d) => (
              <tr key={d.name}>
                <td className="lead mono">{d.name}</td><td className="mono" data-hide-sm>{d.user}</td>
                <td className="muted">{d.note || ""}</td><td className="num muted" data-hide-sm>{timeAgo(d.created_at)}</td>
                <td style={{ textAlign: "right" }}><Button variant="ghost" size="sm" icon aria-label={`Delete ${d.name}`} onClick={() => setDel(d.name)}><Trash2 size={15} /></Button></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>

      <Modal open={creating} onClose={() => { setCreating(false); setCreated(null); setError(null); }}
        title={created ? "Database created" : "Create database"}
        description={created ? "Copy the password now. Ike doesn't keep it." : `A user with the same name gets full access to it, and nothing else.`}
        footer={created ? <Button variant="primary" onClick={() => { setCreating(false); setCreated(null); }}>Done</Button> : <>
          <Button onClick={() => setCreating(false)}>Cancel</Button>
          <Button variant="primary" loading={busy} disabled={!name} onClick={async () => {
            setBusy(true); setError(null);
            try { setCreated(await api.createDatabase(account, name, note)); setName(""); setNote(""); list.reload(); }
            catch (e) { setError((e as Error).message); } finally { setBusy(false); }
          }}>Create database</Button></>}>
        {created ? (
          <div className="form-grid">
            <Field label="Database and user">{() => <CopyField value={created.name} label="Database" />}</Field>
            <Field label="Password">{() => <CopyField value={created.password} label="Password" />}</Field>
            <Field label="Host">{() => <CopyField value={created.host} label="Host" />}</Field>
          </div>
        ) : (
          <div className="form-grid">
            <Field label="Name" error={error} hint="1–16 lowercase letters or numbers.">
              {(id) => <div className="input-group"><span className="addon" style={{ borderLeft: "1px solid var(--line)", borderRight: 0, borderRadius: "var(--r-sm) 0 0 var(--r-sm)" }}>{account}_</span>
                <input id={id} className="input mono" style={{ borderRadius: "0 var(--r-sm) var(--r-sm) 0" }} value={name} onChange={(e) => setName(e.target.value.toLowerCase())} autoFocus /></div>}
            </Field>
            <Field label="Note (optional)">{(id) => <input id={id} className="input" value={note} onChange={(e) => setNote(e.target.value)} placeholder="What it's for" />}</Field>
          </div>
        )}
      </Modal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title={`Delete ${del}?`} name={del || ""}
        description="All tables and data in this database are deleted. This can't be undone." confirmLabel="Delete database"
        onConfirm={async () => { await api.deleteDatabase(account, del!); list.reload(); toast("ok", `Deleted ${del}`); }} />
    </div>
  );
}

const presets = [
  { label: "Every 5 minutes", value: "*/5 * * * *" },
  { label: "Every hour", value: "0 * * * *" },
  { label: "Every day at 02:30", value: "30 2 * * *" },
  { label: "Every Monday at 08:00", value: "0 8 * * 1" },
  { label: "Every week", value: "@weekly" },
];

export function Cron({ route }: { route: string }) {
  const toast = useToast();
  const { account, picker } = useAccountParam(route);
  const list = useAsync(() => (account ? api.cron(account) : Promise.resolve([])), [account]);
  const [edit, setEdit] = useState<(Omit<CronJob, "id"> & { id?: string }) | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const sErr = edit ? cronError(edit.schedule) : null;

  const save = async () => {
    if (!edit) return;
    setBusy(true); setError(null);
    try {
      const { id, ...job } = edit;
      if (id) await api.updateCron(account, id, job); else await api.addCron(account, job);
      list.reload(); setEdit(null); toast("ok", id ? "Cron job saved" : "Cron job added");
    } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };

  return (
    <div className="stack">
      <PageHead title="Cron jobs" text="Commands that run on a schedule, as the account's own user, starting in its home folder."
        actions={<>{picker}<Button variant="primary" disabled={!account} onClick={() => setEdit({ schedule: "*/5 * * * *", command: "", comment: "", enabled: true })}><Plus size={17} />Add cron job</Button></>} />
      <section className="panel">
        {list.loading && !list.data ? <div className="panel-body"><Skeleton /></div> : (list.data ?? []).length === 0 ? (
          <Empty icon={<CalendarClock size={22} />} title={`No cron jobs in ${account}`} text="Schedule tasks like WordPress cron, Laravel's scheduler or nightly backups."
            action={<Button variant="primary" onClick={() => setEdit({ schedule: "*/5 * * * *", command: "", comment: "", enabled: true })}><Plus size={17} />Add cron job</Button>} />
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>When</th><th>Command</th><th>State</th><th /></tr></thead>
            <tbody>{(list.data ?? []).map((j) => (
              <tr key={j.id}>
                <td className="lead"><div className="primary-cell">{describeCron(j.schedule)}</div><div className="small muted mono">{j.schedule}</div></td>
                <td><code style={{ wordBreak: "break-all" }}>{j.command}</code>{j.comment ? <div className="small muted">{j.comment}</div> : null}</td>
                <td>{j.enabled ? <Badge tone="ok">On</Badge> : <Badge>Paused</Badge>}</td>
                <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                  <Button variant="ghost" size="sm" icon aria-label="Edit" onClick={() => setEdit({ ...j })}><Pencil size={15} /></Button>
                  <Button variant="ghost" size="sm" icon aria-label="Delete" onClick={async () => { await api.deleteCron(account, j.id); list.reload(); toast("ok", "Cron job deleted"); }}><Trash2 size={15} /></Button>
                </td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <Modal open={!!edit} onClose={() => setEdit(null)} title={edit?.id ? "Edit cron job" : "Add cron job"}
        footer={<><Button onClick={() => setEdit(null)}>Cancel</Button><Button variant="primary" loading={busy} disabled={!!sErr || !edit?.command.trim()} onClick={save}>{edit?.id ? "Save" : "Add cron job"}</Button></>}>
        {edit ? (
          <div className="form-grid">
            <div className="row">{presets.map((p) => <button key={p.value} type="button" className="badge" style={{ cursor: "pointer", border: 0 }} onClick={() => setEdit({ ...edit, schedule: p.value })}>{p.label}</button>)}</div>
            <Field label="Schedule" error={sErr} hint={!sErr ? describeCron(edit.schedule) : undefined}>
              {(id) => <input id={id} className="input mono" value={edit.schedule} onChange={(e) => setEdit({ ...edit, schedule: e.target.value })} spellCheck={false} />}
            </Field>
            <Field label="Command" error={error}>
              {(id) => <input id={id} className="input mono" value={edit.command} onChange={(e) => setEdit({ ...edit, command: e.target.value })} placeholder="php web/example.com/public_html/artisan schedule:run" spellCheck={false} />}
            </Field>
            <Field label="Note (optional)">{(id) => <input id={id} className="input" value={edit.comment || ""} onChange={(e) => setEdit({ ...edit, comment: e.target.value })} />}</Field>
            <Toggle checked={edit.enabled} onChange={(v) => setEdit({ ...edit, enabled: v })} label="Run this job" />
          </div>
        ) : null}
      </Modal>
    </div>
  );
}
