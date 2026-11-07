// Real-time media: a TURN server (coturn) and LiveKit. Admins set them up;
// account owners see where to connect and, when allowed, make their own keys
// and TURN logins.
import { useState } from "react";
import { Plus, RadioTower, RefreshCw, RotateCcw, Ticket, Trash2, Video } from "lucide-react";
import { api } from "../api/client";
import type { Role } from "../api/types";
import type { JoinToken, LiveKitKey, RealtimeAccountView, RealtimeStatus, TurnCredential } from "../api/types5";
import { Badge, Button, CopyBlock, CopyField, Empty, Field, Modal, Skeleton, useAsync } from "../components/ui";
import { ConfirmDelete, PageHead, Toggle } from "../components/common";
import { dateTime, enc, FormModal, NumberField, OnceModal, SelectField, TextField, useAction, useGet } from "../components/kit5";
import { useAccountParam } from "./AccountTools";

function stateBadge(installed: boolean, enabled: boolean, running?: string) {
  if (!installed) return <Badge>Not installed</Badge>;
  if (!enabled) return <Badge>Off</Badge>;
  return running === "active" ? <Badge tone="ok">Running</Badge> : <Badge tone="bad">{running || "Stopped"}</Badge>;
}

function iceSnippet(c: TurnCredential) {
  return `const pc = new RTCPeerConnection({ iceServers: [{
  urls: ${JSON.stringify(c.uris)},
  username: ${JSON.stringify(c.username)},
  credential: ${JSON.stringify(c.credential)},
}] });`;
}

export function RealtimePage({ route, role }: { route: string; role: Role }) {
  if (role !== "admin") return <RealtimeForAccount route={route} />;
  return <RealtimeAdmin />;
}

// ------------------------------------------------------------------ admin

function RealtimeAdmin() {
  const st = useGet<RealtimeStatus>("/realtime");
  const accounts = useAsync(() => api.accounts());
  const [notes, setNotes] = useState<string[]>([]);
  const [secret, setSecret] = useState<LiveKitKey | null>(null);
  const [cred, setCred] = useState<TurnCredential | null>(null);
  const [rotate, setRotate] = useState(false);
  const { busy, run } = useAction();
  const s = st.data;
  const [turn, setTurn] = useState<RealtimeStatus["turn"] | null>(null);
  const [lk, setLk] = useState<RealtimeStatus["livekit"] | null>(null);
  const t = turn ?? s?.turn ?? null;
  const l = lk ?? s?.livekit ?? null;
  const done = (r: RealtimeStatus | undefined) => { if (r) { setNotes(r.notes ?? []); setTurn(null); setLk(null); st.reload(); } };

  if (!s || !t || !l) return <div className="stack"><PageHead title="Real-time media" /><Skeleton h={240} /></div>;
  const saveTurn = () => run("turn", () => api.call<RealtimeStatus>("PUT", "/realtime/turn", {
    enabled: t.enabled, domain: t.domain, tls: t.tls, user_quota: t.user_quota, total_quota: t.total_quota, max_kbps: t.max_kbps, allow_accounts: t.allow_accounts,
  }), "TURN saved").then(done);
  const saveLk = () => run("lk", () => api.call<{ status: RealtimeStatus; new_key?: LiveKitKey }>("PUT", "/realtime/livekit", {
    enabled: l.enabled, domain: l.domain, account: l.account || accounts.data?.[0]?.name || "", use_turn: l.use_turn,
    empty_timeout: l.empty_timeout || 300, max_participants: l.max_participants, webhook_urls: (l.webhook_urls ?? []).filter(Boolean), allow_accounts: l.allow_accounts,
  }), "LiveKit saved").then((r) => { if (r) { if (r.new_key?.secret) setSecret(r.new_key); done(r.status); } });

  return (
    <div className="stack">
      <PageHead title="Real-time media" text="A TURN server so video and voice calls connect through strict firewalls and mobile networks, and LiveKit for video rooms, calls and live streams. Both run on this server; Ike writes their settings and opens their ports." />
      {notes.length ? <div className="callout warn"><div>{notes.map((n, i) => <div key={i}>{n}</div>)}</div></div> : null}

      <section className="panel">
        <div className="panel-head"><div><h2>TURN server</h2><p className="small muted">coturn. Logins are short-lived and signed with a secret, and it never relays into private networks.</p></div>
          {stateBadge(t.installed, s.turn.enabled, t.running)}</div>
        {!t.installed ? (
          <div className="panel-body"><Empty icon={<RadioTower size={22} />} title="coturn isn't installed" text="Add it by running the installer again with --turn." /></div>
        ) : (
          <div className="panel-body form-grid">
            <Toggle checked={t.enabled} onChange={(v) => setTurn({ ...t, enabled: v })} label="Run the TURN server" />
            <TextField label="Domain" value={t.domain} onChange={(v) => setTurn({ ...t, domain: v })} placeholder="turn.example.com" mono
              hint="A name pointing at this server. For TLS, it needs HTTPS here: a website with that name (a static one is enough), or a certificate that lists it." />
            <Toggle checked={t.tls} onChange={(v) => setTurn({ ...t, tls: v })} label="TURN over TLS on port 5349 (gets through firewalls that only allow HTTPS)" />
            {t.tls && s.turn.domain === t.domain && !s.turn.cert_ready ? <div className="callout warn small">No certificate covers {t.domain} yet. TURN runs without TLS until one does.</div> : null}
            <div className="grid-3">
              <NumberField label="Logins per user" value={t.user_quota} onChange={(v) => setTurn({ ...t, user_quota: v })} min={0} hint="0 = no limit" />
              <NumberField label="Logins in all" value={t.total_quota} onChange={(v) => setTurn({ ...t, total_quota: v })} min={0} hint="0 = no limit" />
              <NumberField label="Kbit/s per session" value={t.max_kbps} onChange={(v) => setTurn({ ...t, max_kbps: v })} min={0} hint="0 = no limit" />
            </div>
            <Toggle checked={t.allow_accounts} onChange={(v) => setTurn({ ...t, allow_accounts: v })} label="Account owners can make TURN logins for their apps" />
            <div className="row">
              <Button variant="primary" loading={busy === "turn"} onClick={saveTurn}>Save</Button>
              {s.turn.enabled ? <>
                <Button loading={busy === "cred"} onClick={() => run("cred", () => api.call<TurnCredential>("POST", "/realtime/turn/credentials", { user: "test", ttl_hours: 2 })).then((c) => c && setCred(c))}><Ticket size={16} />Test login</Button>
                <Button variant="ghost" onClick={() => setRotate(true)}><RotateCcw size={16} />New secret</Button>
              </> : null}
            </div>
            {s.turn.enabled && s.turn.uris?.length ? <Field label="Addresses">{() => <CopyBlock value={s.turn.uris!.join("\n")} />}</Field> : null}
            <p className="small muted">Ports: {t.ports.join(", ")}</p>
          </div>
        )}
      </section>

      <section className="panel">
        <div className="panel-head"><div><h2>LiveKit</h2><p className="small muted">WebRTC rooms for video calls, classes and live streams. Apps connect to wss://your-domain with an API key.{l.version ? ` Version ${l.version}.` : ""}</p></div>
          {stateBadge(l.installed, s.livekit.enabled, l.running)}</div>
        {!l.installed ? (
          <div className="panel-body"><Empty icon={<Video size={22} />} title="LiveKit isn't installed" text="Add it by running the installer again with --livekit." /></div>
        ) : (
          <div className="panel-body form-grid">
            <Toggle checked={l.enabled} onChange={(v) => setLk({ ...l, enabled: v })} label="Run LiveKit" />
            <div className="grid-2">
              <TextField label="Domain" value={l.domain} onChange={(v) => setLk({ ...l, domain: v })} placeholder="rtc.example.com" mono
                hint="Ike makes it a website that forwards to LiveKit and gets it a certificate. Point the name at this server first." />
              <SelectField label="Website owner" value={l.account || accounts.data?.[0]?.name || ""} onChange={(v) => setLk({ ...l, account: v })}
                options={(accounts.data ?? []).map((a) => ({ value: a.name, label: a.name }))} />
            </div>
            <Toggle checked={l.use_turn} onChange={(v) => setLk({ ...l, use_turn: v })} label="Give LiveKit's callers this server's TURN server" />
            {l.use_turn && !s.turn.enabled ? <div className="callout warn small">Switch the TURN server on first.</div> : null}
            <div className="grid-2">
              <NumberField label="Close empty rooms after (seconds)" value={l.empty_timeout || 300} onChange={(v) => setLk({ ...l, empty_timeout: v })} min={10} />
              <NumberField label="People per room" value={l.max_participants} onChange={(v) => setLk({ ...l, max_participants: v })} min={0} hint="0 = no limit" />
            </div>
            <TextField label="Webhook (optional)" value={(l.webhook_urls ?? [])[0] ?? ""} onChange={(v) => setLk({ ...l, webhook_urls: v ? [v] : [] })} placeholder="https://app.example.com/livekit/webhook" mono
              hint="LiveKit posts room and participant events here, signed with the first API key." />
            <Toggle checked={l.allow_accounts} onChange={(v) => setLk({ ...l, allow_accounts: v })} label="Account owners can make their own API keys" />
            <div className="row"><Button variant="primary" loading={busy === "lk"} onClick={saveLk}>Save</Button></div>
            {s.livekit.enabled && s.livekit.url ? <Field label="Server URL">{() => <CopyField value={s.livekit.url} label="Server URL" />}</Field> : null}
          </div>
        )}
      </section>

      <JitsiPanel s={s} accounts={(accounts.data ?? []).map((a) => a.name)} onSaved={st.reload} />

      {s.livekit.enabled ? <KeysPanel keys={s.livekit.keys ?? []} base="/realtime/livekit" accounts={(accounts.data ?? []).map((a) => a.name)} onChange={st.reload} /> : null}

      <OnceModal open={!!secret} onClose={() => setSecret(null)} title="LiveKit API key">
        {secret ? <><CopyField value={secret.key} label="API key" /><Field label="API secret">{() => <CopyField value={secret.secret ?? ""} label="API secret" />}</Field></> : null}
      </OnceModal>
      <TurnCredModal cred={cred} onClose={() => setCred(null)} />
      <FormModal open={rotate} onClose={() => setRotate(false)} title="Make a new TURN secret?" submit="New secret" danger
        description="Logins signed with the old secret stop working at once. Apps that make their own logins need the new secret; LiveKit is updated for you."
        onSubmit={async () => { done(await api.call<RealtimeStatus>("POST", "/realtime/turn/secret")); }}><span /></FormModal>
    </div>
  );
}

interface Jitsi { enabled: boolean; domain: string; account: string; use_turn: boolean; lobby: boolean; version?: string; docker_ready: boolean; running: string; url?: string }

function JitsiPanel({ s, accounts, onSaved }: { s: RealtimeStatus & { jitsi?: Jitsi }; accounts: string[]; onSaved: () => void }) {
  const j = s.jitsi;
  const [f, setF] = useState<Jitsi | null>(null);
  const [notes, setNotes] = useState<string[]>([]);
  const { busy, run } = useAction();
  if (!j) return null;
  const cur = f ?? j;
  const set = (p: Partial<Jitsi>) => setF({ ...cur, ...p });
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Jitsi Meet</h2><p className="small muted">Video meetings in the browser, no app or account needed. Runs the official Jitsi images in Docker, behind a website of yours.</p></div>
        {!j.docker_ready ? <Badge>Needs Docker</Badge> : !j.enabled ? <Badge>Off</Badge> : j.running === "active" ? <Badge tone="ok">Running</Badge> : <Badge tone="warn">{j.running === "partial" ? "Starting" : "Stopped"}</Badge>}</div>
      {!j.docker_ready ? <div className="panel-body"><Empty icon={<Video size={22} />} title="Docker isn't installed" text="Add it with the installer's --docker, then switch Jitsi on here." /></div> : (
        <div className="panel-body form-grid">
          <Toggle checked={cur.enabled} onChange={(v) => set({ enabled: v })} label="Run Jitsi Meet" />
          <div className="grid-2">
            <TextField label="Domain" value={cur.domain} onChange={(v) => set({ domain: v })} placeholder="meet.example.com" mono hint="Ike makes it a website with HTTPS. Point the name at this server first." />
            <SelectField label="Website owner" value={cur.account || accounts[0] || ""} onChange={(v) => set({ account: v })} options={accounts.map((a) => ({ value: a, label: a }))} />
          </div>
          <Toggle checked={cur.use_turn} onChange={(v) => set({ use_turn: v })} label="Use this server's TURN server" />
          <Toggle checked={cur.lobby} onChange={(v) => set({ lobby: v })} label="Lobby: people wait until a moderator lets them in" />
          {notes.length ? <div className="callout warn small"><div>{notes.map((n, i) => <div key={i}>{n}</div>)}</div></div> : null}
          <div className="row"><Button variant="primary" disabled={!f} loading={busy === "j"} onClick={() => run("j", () => api.call<{ notes?: string[] }>("PUT", "/realtime/jitsi",
            { enabled: cur.enabled, domain: cur.domain, account: cur.account || accounts[0] || "", use_turn: cur.use_turn, lobby: cur.lobby, version: cur.version || "stable" }),
            cur.enabled ? "Jitsi Meet is starting (the first start downloads it)" : "Jitsi Meet stopped").then((r) => { if (r) { setNotes(r.notes ?? []); setF(null); onSaved(); } })}>Save</Button>
            {j.enabled && j.url ? <a className="btn" href={j.url} target="_blank" rel="noreferrer">Open</a> : null}
            <span className="small muted">Opens 10000/udp for media.</span></div>
        </div>
      )}
    </section>
  );
}

function TurnCredModal({ cred, onClose }: { cred: TurnCredential | null; onClose: () => void }) {
  return (
    <Modal open={!!cred} onClose={onClose} title="TURN login" description={cred ? `Valid until ${dateTime(cred.expires)}. Use it in a browser's RTCPeerConnection, or test it with Trickle ICE.` : ""}
      footer={<Button variant="primary" onClick={onClose}>Done</Button>}>
      {cred ? <div className="form-grid">
        <CopyField value={cred.username} label="Username" />
        <Field label="Credential">{() => <CopyField value={cred.credential} label="Credential" />}</Field>
        <Field label="In JavaScript">{() => <CopyBlock value={iceSnippet(cred)} />}</Field>
      </div> : null}
    </Modal>
  );
}

/** API keys and a join-token tester. base is /realtime/livekit or /accounts/{a}/realtime. */
function KeysPanel({ keys, base, accounts, canAdd = true, onChange }: { keys: LiveKitKey[]; base: string; accounts?: string[]; canAdd?: boolean; onChange: () => void }) {
  const [adding, setAdding] = useState(false);
  const [name, setName] = useState("");
  const [owner, setOwner] = useState("");
  const [made, setMade] = useState<LiveKitKey | null>(null);
  const [del, setDel] = useState<string | null>(null);
  const [tok, setTok] = useState({ key: "", room: "test-room", identity: "tester", ttl_minutes: 60 });
  const [token, setToken] = useState<JoinToken | null>(null);
  const { busy, run } = useAction();
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>API keys</h2><p className="small muted">Apps sign join tokens with a key and its secret using a LiveKit server SDK. Secrets are shown once.</p></div>
        {canAdd ? <Button onClick={() => { setAdding(true); setName(""); setOwner(""); }}><Plus size={16} />Add key</Button> : null}</div>
      {keys.length === 0 ? <div className="panel-body"><Empty icon={<Ticket size={22} />} title="No keys yet" text="Add a key for each app, so you can remove one without breaking the others." /></div> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Key</th><th>Name</th>{accounts ? <th>Account</th> : null}<th>Added</th><th /></tr></thead>
          <tbody>{keys.map((k) => (
            <tr key={k.key}>
              <td className="lead mono">{k.key}</td><td>{k.name}</td>{accounts ? <td className="small">{k.account || "—"}</td> : null}
              <td className="small">{dateTime(k.created)}</td>
              <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" onClick={() => setDel(k.key)} aria-label={`Remove ${k.key}`}><Trash2 size={15} /></Button></td>
            </tr>))}</tbody>
        </table></div>
      )}
      {keys.length ? (
        <div className="panel-body form-grid" style={{ borderTop: "1px solid var(--line)" }}>
          <h3 style={{ margin: 0 }}>Try a room</h3>
          <div className="grid-3">
            <SelectField label="Key" value={tok.key || keys[0].key} onChange={(v) => setTok({ ...tok, key: v })} options={keys.map((k) => ({ value: k.key, label: `${k.name} (${k.key})` }))} />
            <TextField label="Room" value={tok.room} onChange={(v) => setTok({ ...tok, room: v })} mono />
            <TextField label="Your name in the room" value={tok.identity} onChange={(v) => setTok({ ...tok, identity: v })} mono />
          </div>
          <div className="row"><Button loading={busy === "tok"} onClick={() => { run("tok", () => api.call<JoinToken>("POST", `${base}/token`, { ...tok, key: tok.key || keys[0].key })).then((t) => t && setToken(t)); }}><Ticket size={16} />Make a join token</Button></div>
          {token ? <>
            <Field label="Join token">{() => <CopyField value={token.token} label="Join token" />}</Field>
            <p className="small muted">Open <a href={`https://meet.livekit.io/custom?liveKitUrl=${enc(token.url)}&token=${enc(token.token)}`} target="_blank" rel="noreferrer">LiveKit Meet</a> with it to check camera, microphone and TURN. Valid until {dateTime(token.expires)}.</p>
          </> : null}
        </div>
      ) : null}
      <FormModal open={adding} onClose={() => setAdding(false)} title="Add an API key" submit="Add key"
        onSubmit={async () => { const k = await api.call<LiveKitKey>("POST", `${base}/keys`, accounts ? { name, account: owner } : { name }); setMade(k); onChange(); }}>
        <TextField label="Name" value={name} onChange={setName} placeholder="School app" autoFocus />
        {accounts ? <SelectField label="For account" value={owner} onChange={setOwner} options={[{ value: "", label: "The server admin" }, ...accounts.map((a) => ({ value: a, label: a }))]}
          hint="Account owners see their keys (not the secrets) under Video & calls." /> : null}
      </FormModal>
      <OnceModal open={!!made} onClose={() => setMade(null)} title="LiveKit API key">
        {made ? <><CopyField value={made.key} label="API key" /><Field label="API secret">{() => <CopyField value={made.secret ?? ""} label="API secret" />}</Field></> : null}
      </OnceModal>
      <ConfirmDelete open={!!del} onClose={() => setDel(null)} title="Remove this API key?" name={del ?? ""} confirmLabel="Remove key"
        description="Apps using it can't make new join tokens; LiveKit restarts, so people in rooms reconnect."
        onConfirm={async () => { await api.call("DELETE", `${base}/keys/${enc(del ?? "")}`); onChange(); }} />
    </section>
  );
}

// ------------------------------------------------------------------ accounts

function RealtimeForAccount({ route }: { route: string }) {
  const { account, picker } = useAccountParam(route);
  const v = useGet<RealtimeAccountView>(account ? `/accounts/${enc(account)}/realtime` : null);
  const [cred, setCred] = useState<TurnCredential | null>(null);
  const { busy, run } = useAction();
  const d = v.data;
  return (
    <div className="stack">
      <PageHead title="Video & calls" text="Where your apps connect for video calls, classes and live streams on this server." actions={<>{picker}<Button onClick={v.reload} aria-label="Refresh"><RefreshCw size={16} /></Button></>} />
      {!d ? <Skeleton h={160} /> : !d.livekit_url && !d.turn_uris?.length ? (
        <section className="panel"><Empty icon={<Video size={22} />} title="Not offered on this server" text="Ask your hosting provider about video calls (LiveKit) and TURN." /></section>
      ) : <>
        {d.livekit_url ? (
          <>
            <section className="panel">
              <div className="panel-head"><div><h2>LiveKit</h2><p className="small muted">Use this URL with a LiveKit client SDK. Your server signs join tokens with an API key.</p></div></div>
              <div className="panel-body"><CopyField value={d.livekit_url} label="Server URL" /></div>
            </section>
            {d.can_make_keys || d.livekit_keys.length ? <KeysPanel keys={d.livekit_keys} base={`/accounts/${enc(account)}/realtime`} canAdd={d.can_make_keys} onChange={v.reload} />
              : <section className="panel"><div className="panel-body small muted">Ask your hosting provider for an API key.</div></section>}
          </>
        ) : null}
        {d.turn_uris?.length ? (
          <section className="panel">
            <div className="panel-head"><div><h2>TURN</h2><p className="small muted">Helps calls connect through strict firewalls and mobile networks.</p></div>
              {d.can_make_turn_credentials ? <Button loading={busy === "c"} onClick={() => run("c", () => api.call<TurnCredential>("POST", `/accounts/${enc(account)}/realtime/turn-credentials`, { user: "app", ttl_hours: 12 })).then((c) => c && setCred(c))}><Ticket size={16} />Make a login</Button> : null}</div>
            <div className="panel-body"><CopyBlock value={d.turn_uris.join("\n")} />
              {d.can_make_turn_credentials ? <p className="small muted">Logins last up to 7 days. Apps should ask for a new one through the API (POST /accounts/{account}/realtime/turn-credentials) rather than keep one.</p> : <p className="small muted">Ask your hosting provider for TURN logins.</p>}</div>
          </section>
        ) : null}
      </>}
      <TurnCredModal cred={cred} onClose={() => setCred(null)} />
    </div>
  );
}
