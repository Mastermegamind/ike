// A public status page for the admin or a reseller: chosen websites, their
// uptime over 90 days, in their own branding.
import { useState } from "react";
import { Activity, ExternalLink, Plus, Trash2 } from "lucide-react";
import { api } from "../api/client";
import { Button, CopyField, Empty, Field, Skeleton } from "../components/ui";
import { PageHead, Toggle } from "../components/common";
import { TextField, useAction, useGet } from "../components/kit5";

interface Entry { domain: string; name?: string }
interface Page { enabled: boolean; slug: string; title?: string; intro?: string; entries: Entry[] }
interface Resp { page: Page; url: string; websites: string[] }

export function StatusPageSettings() {
  const r = useGet<Resp>("/status-page");
  const [f, setF] = useState<Page | null>(null);
  const [url, setUrl] = useState("");
  const { busy, run } = useAction();
  if (!r.data) return <div className="stack"><PageHead title="Status page" /><Skeleton h={240} /></div>;
  const cur = f ?? r.data.page;
  const set = (p: Partial<Page>) => setF({ ...cur, ...p });
  const listed = new Set(cur.entries.map((e) => e.domain));
  const free = r.data.websites.filter((w) => !listed.has(w));
  const link = url || r.data.url;
  return (
    <div className="stack">
      <PageHead title="Status page" text="A public page your customers can check: the websites you choose, whether they're up, and 90 days of uptime from the uptime monitor, in your branding."
        actions={link && cur.enabled ? <a className="btn" href={link} target="_blank" rel="noreferrer"><ExternalLink size={16} />Open</a> : undefined} />
      <section className="panel">
        <div className="panel-head"><h2>Page</h2><Toggle checked={cur.enabled} onChange={(v) => set({ enabled: v })} label={cur.enabled ? "Public" : "Off"} /></div>
        <div className="panel-body form-grid">
          <TextField label="Address" value={cur.slug} onChange={(v) => set({ slug: v.toLowerCase() })} placeholder="mycompany" mono hint="Your page lives at /status/ and this name." />
          <TextField label="Title (optional)" value={cur.title ?? ""} onChange={(v) => set({ title: v })} placeholder="Service status" />
          <TextField label="Note at the top (optional)" value={cur.intro ?? ""} onChange={(v) => set({ intro: v })} placeholder="Planned maintenance: Sunday 2–4 am" />
          {link ? <Field label="Public address">{() => <CopyField value={link} label="Public address" />}</Field> : null}
        </div>
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Websites on the page</h2><p className="small muted">Only what you list here is shown. Give a website a friendlier name if you'd rather not show its address.</p></div></div>
        {cur.entries.length === 0 ? <div className="panel-body"><Empty icon={<Activity size={22} />} title="No websites yet" text="Add the websites your customers care about." /></div> : (
          <div className="table-wrap"><table className="responsive"><thead><tr><th>Website</th><th>Name shown</th><th /></tr></thead>
            <tbody>{cur.entries.map((e, i) => (
              <tr key={e.domain}>
                <td className="lead mono">{e.domain}</td>
                <td><input className="input" aria-label={`Name shown for ${e.domain}`} value={e.name ?? ""} placeholder={e.domain}
                  onChange={(ev) => set({ entries: cur.entries.map((x, j) => (j === i ? { ...x, name: ev.target.value } : x)) })} /></td>
                <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" aria-label={`Remove ${e.domain}`} onClick={() => set({ entries: cur.entries.filter((_, j) => j !== i) })}><Trash2 size={15} /></Button></td>
              </tr>))}</tbody></table></div>
        )}
        {free.length ? <div className="panel-body row">
          <select className="select" style={{ width: "auto", minWidth: 220 }} aria-label="Website to add" id="add-site" defaultValue="">
            <option value="" disabled>Choose a website…</option>
            {free.map((w) => <option key={w} value={w}>{w}</option>)}
          </select>
          <Button onClick={() => { const el = document.getElementById("add-site") as HTMLSelectElement; if (el.value) set({ entries: [...cur.entries, { domain: el.value }] }); }}><Plus size={16} />Add</Button>
        </div> : null}
      </section>
      <div><Button variant="primary" disabled={!f} loading={busy === "save"} onClick={() => run("save", () => api.call<{ page: Page; url: string }>("PUT", "/status-page", cur), "Status page saved")
        .then((x) => { if (x) { setUrl(x.url); setF(null); r.reload(); } })}>Save</Button></div>
    </div>
  );
}
