// S3-compatible storage (Garage): buckets and an access key per account, and
// for admins, where the S3 endpoint is served.
import { useState } from "react";
import { Archive, KeyRound, Plus, Trash2 } from "lucide-react";
import { api } from "../api/client";
import type { Role } from "../api/types";
import { Badge, Button, CopyBlock, CopyField, Empty, Field, Skeleton, useAsync } from "../components/ui";
import { ConfirmDelete, PageHead } from "../components/common";
import { enc, FormModal, NumberField, OnceModal, SelectField, TextField, useAction, useGet } from "../components/kit5";
import { useAccountParam } from "./AccountTools";

interface Bucket { name: string; quota_mb: number; used_mb: number; objects: number; created_at: string }
interface View { installed: boolean; endpoint: string; region: string; access_key?: string; buckets: Bucket[] }
interface Creds { endpoint: string; region: string; access_key: string; secret_key?: string; path_style: boolean }

function size(mb: number) { return mb >= 1024 ? `${(mb / 1024).toFixed(1)} GB` : `${mb.toFixed(mb < 10 ? 1 : 0)} MB` }

export function StoragePage({ route, role }: { route: string; role: Role }) {
  const { account, picker } = useAccountParam(route);
  const v = useGet<View>(account ? `/accounts/${enc(account)}/objects` : null);
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [quota, setQuota] = useState(1024);
  const [cred, setCred] = useState<Creds | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const [rekey, setRekey] = useState(false);
  const base = `/accounts/${enc(account)}/objects`;
  const d = v.data;
  return (
    <div className="stack">
      <PageHead title="S3 storage" text="Buckets for files, uploads and backups that apps reach with any S3 client or SDK (Laravel's s3 disk, rclone, the AWS SDKs). One access key reaches all of this account's buckets, and nothing else." actions={picker} />
      {role === "admin" ? <EndpointPanel /> : null}
      {!d ? <Skeleton h={200} /> : !d.installed ? (
        <section className="panel"><Empty icon={<Archive size={22} />} title="S3 storage isn't installed" text="Ask your hosting provider. The admin adds it with the installer's --add garage." /></section>
      ) : <>
        <section className="panel">
          <div className="panel-head"><div><h2>Buckets</h2><p className="small muted">Names start with the account name.</p></div><Button onClick={() => { setAdding(true); setName(""); }}><Plus size={16} />Add bucket</Button></div>
          {d.buckets.length === 0 ? <div className="panel-body"><Empty icon={<Archive size={22} />} title="No buckets yet" text="Add one; the first also gives you the access key." /></div> : (
            <div className="table-wrap"><table className="responsive"><thead><tr><th>Bucket</th><th>Used</th><th>Objects</th><th /></tr></thead>
              <tbody>{d.buckets.map((b) => (
                <tr key={b.name}>
                  <td className="lead mono">{b.name}</td>
                  <td>{size(b.used_mb)}{b.quota_mb ? <span className="small muted"> of {size(b.quota_mb)}</span> : null}
                    {b.quota_mb && b.used_mb > b.quota_mb * 0.9 ? <> <Badge tone="warn">Nearly full</Badge></> : null}</td>
                  <td>{b.objects}</td>
                  <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" aria-label={`Delete ${b.name}`} onClick={() => setDel(b.name)}><Trash2 size={15} /></Button></td>
                </tr>))}</tbody></table></div>
          )}
        </section>
        {d.access_key ? (
          <section className="panel">
            <div className="panel-head"><h2>Connect</h2><Button variant="ghost" onClick={() => setRekey(true)}><KeyRound size={16} />New key</Button></div>
            <div className="panel-body form-grid">
              <dl className="kv"><dt>Endpoint</dt><dd className="mono">{d.endpoint || "not set yet (ask your hosting provider)"}</dd><dt>Region</dt><dd className="mono">{d.region}</dd>
                <dt>Access key</dt><dd className="mono">{d.access_key}</dd><dt>Addressing</dt><dd>Path-style (use_path_style_endpoint)</dd></dl>
              <Field label="Laravel .env">{() => <CopyBlock value={`FILESYSTEM_DISK=s3
AWS_ACCESS_KEY_ID=${d.access_key}
AWS_SECRET_ACCESS_KEY=(shown when the key was made)
AWS_DEFAULT_REGION=${d.region}
AWS_BUCKET=${d.buckets[0]?.name ?? ""}
AWS_ENDPOINT=${d.endpoint}
AWS_USE_PATH_STYLE_ENDPOINT=true`} />}</Field>
            </div>
          </section>
        ) : null}
      </>}
      <FormModal open={adding} onClose={() => setAdding(false)} title="Add a bucket" submit="Add bucket"
        onSubmit={async () => { const r = await api.call<{ credentials?: Creds }>("POST", `${base}/buckets`, { name, quota_mb: quota }); if (r.credentials?.secret_key) setCred(r.credentials); v.reload(); }}>
        <TextField label="Name" value={name} onChange={setName} placeholder="media" mono hint={`The bucket is called ${account}-${name || "…"}.`} autoFocus />
        <NumberField label="Size limit (MB)" value={quota} onChange={setQuota} min={0} hint="0 = no limit" />
      </FormModal>
      <OnceModal open={!!cred} onClose={() => setCred(null)} title="S3 access key">
        {cred ? <><CopyField value={cred.access_key} label="Access key" /><Field label="Secret key">{() => <CopyField value={cred.secret_key ?? ""} label="Secret key" />}</Field>
          <Field label="Endpoint">{() => <CopyField value={cred.endpoint} label="Endpoint" />}</Field></> : null}
      </OnceModal>
      <FormModal open={rekey} onClose={() => setRekey(false)} title="Make a new access key?" submit="New key" danger
        description="The new key gets every bucket, then the old key stops working. Update your apps right after."
        onSubmit={async () => { const c = await api.call<Creds>("POST", `${base}/key`); setCred(c); v.reload(); }}><span /></FormModal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title="Delete this bucket?" name={del ?? ""} confirmLabel="Delete bucket"
        description="Only empty buckets can be deleted. Remove its files first."
        onConfirm={async () => { await api.call("DELETE", `${base}/buckets/${enc(del ?? "")}`); v.reload(); }} />
    </div>
  );
}

function EndpointPanel() {
  const s = useGet<{ installed: boolean; settings: { domain?: string; account?: string }; endpoint: string }>("/objects");
  const accounts = useAsync(() => api.accounts());
  const [f, setF] = useState<{ domain: string; account: string } | null>(null);
  const [note, setNote] = useState("");
  const { busy, run } = useAction();
  if (!s.data?.installed) return null;
  const cur = f ?? { domain: s.data.settings.domain ?? "", account: s.data.settings.account ?? accounts.data?.[0]?.name ?? "" };
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>S3 address</h2><p className="small muted">Garage listens on this server only; Ike serves it on a website of yours with HTTPS.</p></div></div>
      <div className="panel-body form-grid">
        <div className="grid-2">
          <TextField label="Domain" value={cur.domain} onChange={(v) => setF({ ...cur, domain: v })} placeholder="s3.example.com" mono />
          <SelectField label="Website owner" value={cur.account} onChange={(v) => setF({ ...cur, account: v })} options={(accounts.data ?? []).map((a) => ({ value: a.name, label: a.name }))} />
        </div>
        {note ? <div className="callout warn small">{note}</div> : null}
        <div className="row"><Button variant="primary" disabled={!f} loading={busy === "e"} onClick={() => run("e", () => api.call<{ note: string }>("PUT", "/objects", cur), "S3 address saved")
          .then((r) => { if (r) { setNote(r.note); setF(null); s.reload(); } })}>Save</Button>
          {s.data.endpoint ? <span className="small muted">Apps use <code>{s.data.endpoint}</code></span> : null}</div>
      </div>
    </section>
  );
}
