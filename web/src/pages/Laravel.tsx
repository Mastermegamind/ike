// Laravel on a PHP website: queue workers, the scheduler and Reverb
// (Laravel's own WebSocket server for broadcasting), run by Ike.
import { useState } from "react";
import { Radio } from "lucide-react";
import { api } from "../api/client";
import type { Site } from "../api/types";
import { Button, CopyBlock } from "../components/ui";
import { Toggle } from "../components/common";
import { enc, NumberField, TextField, useAction } from "../components/kit5";

export interface LaravelSettings {
  path?: string; workers: number; connection?: string; queues?: string; tries?: number; timeout?: number;
  scheduler: boolean; reverb: boolean; reverb_domain?: string; reverb_port?: number;
}

export function LaravelTab({ site, onChange }: { site: Site & { laravel?: LaravelSettings | null }; onChange: (s: Site) => void }) {
  const cur = site.laravel ?? { workers: 0, scheduler: false, reverb: false };
  const [f, setF] = useState<LaravelSettings>({ ...cur });
  const [note, setNote] = useState("");
  const { busy, run } = useAction();
  const dirty = JSON.stringify(f) !== JSON.stringify({ ...cur });
  const reverbHost = f.reverb_domain || `ws.${site.domain}`;
  const save = (body: LaravelSettings | null) => run("save", () => api.call<{ site: Site; note: string }>("PUT", `/sites/${enc(site.domain)}/laravel`, body), "Laravel settings saved")
    .then((r) => {
      if (!r) return;
      const next = (r.site ?? { ...site, laravel: body }) as Site & { laravel?: LaravelSettings | null };
      setNote(r.note); onChange(next); setF({ ...(next.laravel ?? { workers: 0, scheduler: false, reverb: false }) });
    });
  return (
    <div className="grid-2">
      <section className="panel">
        <div className="panel-head"><div><h2>Laravel</h2><p className="small muted">Ike runs these as services for this website, restarts them after each deploy, and stops them if the account is suspended.</p></div></div>
        <div className="panel-body form-grid">
          <NumberField label="Queue workers" value={f.workers} onChange={(v) => setF({ ...f, workers: v })} min={0} max={8} hint="php artisan queue:work, kept running. 0 = none." />
          {f.workers > 0 ? <div className="grid-3">
            <TextField label="Queues" value={f.queues ?? ""} onChange={(v) => setF({ ...f, queues: v })} placeholder="default" mono />
            <NumberField label="Tries" value={f.tries ?? 3} onChange={(v) => setF({ ...f, tries: v })} min={1} max={100} />
            <NumberField label="Timeout (seconds)" value={f.timeout ?? 60} onChange={(v) => setF({ ...f, timeout: v })} min={1} max={86400} />
          </div> : null}
          <Toggle checked={f.scheduler} onChange={(v) => setF({ ...f, scheduler: v })} label="Run the scheduler every minute (php artisan schedule:run)" />
          <Toggle checked={f.reverb} onChange={(v) => setF({ ...f, reverb: v })} label="Run Reverb for broadcasting (no Pusher account needed)" />
          {f.reverb ? <TextField label="Reverb's address" value={f.reverb_domain ?? ""} onChange={(v) => setF({ ...f, reverb_domain: v })} placeholder={`ws.${site.domain}`} mono
            hint="Ike makes it a website that forwards WebSockets to Reverb, with HTTPS. Point the name at this server." /> : null}
          <TextField label="Laravel folder (optional)" value={f.path ?? ""} onChange={(v) => setF({ ...f, path: v })} placeholder="found automatically" mono
            hint="The folder with artisan, inside the website folder. Git deploys use the live release." />
          {note ? <div className="callout warn small">{note}</div> : null}
          <div className="row">
            <Button variant="primary" loading={busy === "save"} disabled={!dirty} onClick={() => save({ ...f, reverb_port: undefined })}>Save</Button>
            {site.laravel ? <Button variant="ghost" onClick={() => save(null)}>Stop everything</Button> : null}
          </div>
        </div>
      </section>
      {cur.reverb ? (
        <section className="panel">
          <div className="panel-head"><div className="row" style={{ gap: 8 }}><Radio size={18} aria-hidden /><h2>Connect your app</h2></div></div>
          <div className="panel-body form-grid">
            <p className="small muted">Put these in the app's .env (Env vars tab), with REVERB_APP_ID, REVERB_APP_KEY and REVERB_APP_SECRET from <code>php artisan reverb:install</code>, then save here to restart Reverb.</p>
            <CopyBlock value={`BROADCAST_CONNECTION=reverb
REVERB_SERVER_HOST=127.0.0.1
REVERB_SERVER_PORT=${site.laravel?.reverb_port ?? ""}
REVERB_HOST=${reverbHost}
REVERB_PORT=443
REVERB_SCHEME=https`} />
          </div>
        </section>
      ) : null}
    </div>
  );
}
