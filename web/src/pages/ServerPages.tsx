// Admin pages: firewall, activity, AI agents, server software, app installer, terminal.
import { useEffect, useRef, useState } from "react";
import { AppWindow, Ban as BanIcon, Bot, Lock, Plus, ShieldCheck, ShieldOff, SquareTerminal, Trash2 } from "lucide-react";
import { api } from "../api/client";
import { phpEol, runtimeName } from "../api/demo";
import type { AuditEntry, Site } from "../api/types";
import { Badge, Button, CopyBlock, Empty, Field, Segmented, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { PageHead, webServerLabel } from "../components/common";
import { ActorIcon, describeAudit } from "./Dashboard";
import { WordPressDialog } from "./SiteDetail";

export function firewallLabel(b: string) {
  return b === "nftables" ? "Uses nftables" : b === "firewalld" ? "Uses firewalld" : "Uses ufw";
}

export function Firewall() {
  const toast = useToast();
  const fw = useAsync(() => api.firewall());
  const bans = useAsync(() => api.bans());
  const [tab, setTab] = useState<"ports" | "blocks" | "bans">("ports");
  const [port, setPort] = useState("");
  const [proto, setProto] = useState<"tcp" | "udp">("tcp");
  const [note, setNote] = useState("");
  const [from, setFrom] = useState("");
  const [addr, setAddr] = useState("");
  const [why, setWhy] = useState("");
  const [busy, setBusy] = useState(false);
  const run = async (fn: () => Promise<void>, ok: string) => {
    setBusy(true);
    try { await fn(); fw.reload(); bans.reload(); toast("ok", ok); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(false); }
  };

  return (
    <div className="stack">
      <PageHead title="Firewall" text="Choose which ports are open and which addresses are blocked. Ports that keep you connected can't be closed here."
        actions={fw.data?.backend ? <Badge>{firewallLabel(fw.data.backend)}</Badge> : undefined} />
      <Tabs value={tab} onChange={setTab} tabs={[
        { value: "ports", label: "Open ports" }, { value: "blocks", label: `Blocked addresses${fw.data ? ` (${fw.data.blocks.length})` : ""}` },
        { value: "bans", label: `Automatic bans${bans.data ? ` (${bans.data.length})` : ""}` },
      ]} />
      {tab === "ports" && (
        <section className="panel">
          <div className="panel-body row" style={{ alignItems: "flex-end" }}>
            <Field label="Port or range">{(id) => <input id={id} className="input mono" style={{ width: 150 }} value={port} onChange={(e) => setPort(e.target.value)} placeholder="8080 or 6000:6100" />}</Field>
            <Segmented label="Protocol" value={proto} onChange={setProto} options={[{ value: "tcp", label: "TCP" }, { value: "udp", label: "UDP" }]} />
            <Field label="Only from (optional)" hint="An IP address or range; empty lets anyone connect.">{(id) => <input id={id} className="input mono" style={{ width: 190 }} value={from} onChange={(e) => setFrom(e.target.value)} placeholder="Anyone" />}</Field>
            <Field label="Note">{(id) => <input id={id} className="input" style={{ width: 200 }} value={note} onChange={(e) => setNote(e.target.value)} placeholder="What uses it" />}</Field>
            <Button variant="primary" loading={busy} disabled={!port} onClick={() => run(async () => { await api.openPort(port, proto, note, from.trim()); setPort(""); setNote(""); setFrom(""); }, `Opened ${port}/${proto}${from.trim() ? ` for ${from.trim()}` : ""}`)}><Plus size={16} />Open port</Button>
          </div>
          {!fw.data ? <div className="panel-body"><Skeleton /></div> : (
            <div className="table-wrap"><table>
              <thead><tr><th>Port</th><th>Protocol</th><th>Who can connect</th><th>Used for</th><th /></tr></thead>
              <tbody>{fw.data.ports.map((p) => (
                <tr key={p.port + p.proto + (p.from ?? "")}>
                  <td className="mono">{p.port}</td><td>{p.proto.toUpperCase()}</td>
                  <td className={p.from ? "mono small" : "small muted"}>{p.from || "Anyone"}</td><td>{p.comment}</td>
                  <td style={{ textAlign: "right" }}>{p.protected ? <Badge><Lock size={12} />Always open</Badge> :
                    <Button variant="ghost" size="sm" onClick={() => run(() => api.closePort(p.port, p.proto, p.from), `Closed ${p.port}/${p.proto}`)}>Close</Button>}</td>
                </tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
      )}
      {tab === "blocks" && (
        <section className="panel">
          <div className="panel-body row" style={{ alignItems: "flex-end" }}>
            <Field label="IP address or range">{(id) => <input id={id} className="input mono" style={{ width: 220 }} value={addr} onChange={(e) => setAddr(e.target.value)} placeholder="203.0.113.7 or 203.0.113.0/24" />}</Field>
            <Field label="Reason">{(id) => <input id={id} className="input" style={{ width: 220 }} value={why} onChange={(e) => setWhy(e.target.value)} />}</Field>
            <Button variant="primary" loading={busy} disabled={!addr} onClick={() => run(async () => { await api.block(addr, why); setAddr(""); setWhy(""); }, `Blocked ${addr}`)}><BanIcon size={16} />Block</Button>
          </div>
          {fw.data && fw.data.blocks.length === 0 ? <Empty icon={<ShieldCheck size={22} />} title="Nothing blocked" text="Block an address or range to drop all its traffic." /> : (
            <div className="table-wrap"><table className="responsive">
              <thead><tr><th>Address</th><th>Reason</th><th className="num">Since</th><th /></tr></thead>
              <tbody>{(fw.data?.blocks ?? []).map((b) => (
                <tr key={b.address}><td className="lead mono">{b.address}</td><td className="muted">{b.comment}</td><td className="num muted">{timeAgo(b.added_at)}</td>
                  <td style={{ textAlign: "right" }}><Button variant="ghost" size="sm" onClick={() => run(() => api.unblock(b.address), `Unblocked ${b.address}`)}>Unblock</Button></td></tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
      )}
      {tab === "bans" && (
        <section className="panel">
          <div className="panel-head"><p className="small muted">fail2ban bans addresses after repeated failed logins. Bans lift on their own after an hour.</p></div>
          {(bans.data ?? []).length === 0 ? <Empty icon={<ShieldCheck size={22} />} title="No active bans" text="Nobody is being blocked for failed logins right now." /> : (
            <div className="table-wrap"><table>
              <thead><tr><th>Address</th><th>Caught by</th><th /></tr></thead>
              <tbody>{(bans.data ?? []).map((b) => (
                <tr key={b.jail + b.ip}><td className="mono">{b.ip}</td><td>{b.jail === "sshd" ? "SSH logins" : b.jail}</td>
                  <td style={{ textAlign: "right" }}><Button variant="ghost" size="sm" onClick={() => run(() => api.unban(b.jail, b.ip), `Unbanned ${b.ip}`)}><ShieldOff size={15} />Lift ban</Button></td></tr>
              ))}</tbody>
            </table></div>
          )}
        </section>
      )}
    </div>
  );
}

export function ActivityPage() {
  const log = useAsync(() => api.audit());
  const [who, setWho] = useState<"all" | "people" | "ai">("all");
  const rows = (log.data ?? []).filter((e: AuditEntry) => who === "all" || (who === "ai") === e.actor.startsWith("ai:"));
  return (
    <div className="stack">
      <PageHead title="Activity" text="Every change made on this server, by people and AI agents. Kept in /var/log/ike/agent-audit.log."
        actions={<Segmented label="Show" value={who} onChange={setWho} options={[{ value: "all", label: "Everyone" }, { value: "people", label: "People" }, { value: "ai", label: "AI agents" }]} />} />
      <section className="panel">
        {!log.data ? <div className="panel-body"><Skeleton h={200} /></div> : (
          <ul className="activity">{rows.map((e, i) => (
            <li key={i}>
              <ActorIcon actor={e.actor} />
              <div><div>{describeAudit(e)}</div><div className="small muted">{e.actor.startsWith("ai:") ? `AI agent (${e.actor.slice(3)})` : e.actor}, {timeAgo(e.time)}<span className="mono"> {e.method} {e.path}</span></div></div>
              {e.result === "ok" ? <Badge tone="ok">Done</Badge> : <Badge tone={e.result === "denied" ? "warn" : "bad"}>{e.result === "denied" ? "Blocked" : "Failed"}</Badge>}
            </li>
          ))}</ul>
        )}
      </section>
    </div>
  );
}

export function AiAgents() {
  const [write, setWrite] = useState(false);
  const args = write ? `["root@203.0.113.24", "ike-mcp", "--allow-write"]` : `["root@203.0.113.24", "ike-mcp"]`;
  return (
    <div className="stack">
      <PageHead title="AI agents" text="Let Claude, Cursor, VS Code and other MCP clients manage this server through the same safe actions as the panel. No raw shell." />
      <div className="grid-2">
        <section className="panel">
          <div className="panel-head"><h2>Connect a client</h2></div>
          <div className="panel-body stack">
            <Segmented label="Access" value={write ? "rw" : "ro"} onChange={(v) => setWrite(v === "rw")}
              options={[{ value: "ro", label: "Read-only" }, { value: "rw", label: "Can make changes" }]} />
            <p className="small muted">Add this to your MCP client's configuration on your own computer. It connects over SSH, so no extra port is opened.</p>
            <CopyBlock value={`{\n  "mcpServers": {\n    "vps-lagos-1": {\n      "command": "ssh",\n      "args": ${args}\n    }\n  }\n}`} />
          </div>
        </section>
        <section className="panel">
          <div className="panel-head"><h2>Safety rules</h2></div>
          <div className="panel-body">
            <ul style={{ margin: 0, paddingLeft: 18, display: "grid", gap: 10 }}>
              <li>Read-only unless started with <code>--allow-write</code>.</li>
              <li>Deleting a website or account needs the name typed twice.</li>
              <li>SSH and panel ports can't be closed, so an agent can't lock you out.</li>
              <li>Every call, allowed or blocked, appears in Activity.</li>
              <li>Tip: give the AI its own SSH key restricted to <code>ike-mcp</code>.</li>
            </ul>
          </div>
        </section>
      </div>
      <section className="panel">
        <div className="panel-head"><h2>What an agent can do</h2><Badge>63 tools</Badge></div>
        <div className="panel-body" style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(220px, 1fr))", gap: 8 }}>
          {["Read server health and services", "List accounts, websites and runtimes", "Create accounts and websites", "Switch PHP, Node.js or Python versions",
            "Put a site in maintenance", "Start, stop and restart apps", "Read access, error and app logs", "Add cron jobs", "Create databases",
            "Install WordPress", "Fix file permissions", "Manage SSH keys", "Open ports and block IPs", "Suspend and reactivate accounts",
            "Diagnose a broken website", "Check and install updates", "Create staging copies", "Run the security scan", "Find and undo config drift", "Read usage and set limits", "Deploy from Git and roll back", "Get HTTPS certificates", "Manage containers and stacks", "Install PHP extensions"].map((t) => (
            <div key={t} className="row small" style={{ gap: 8 }}><Bot size={15} className="uli-line" aria-hidden />{t}</div>
          ))}
        </div>
      </section>
    </div>
  );
}

export function Software() {
  const rt = useAsync(() => api.runtimes());
  if (!rt.data) return <Skeleton h={240} />;
  const r = rt.data;
  const stack = { nginx: "Nginx only", apache: "Apache only", hybrid: "Nginx in front, Apache per website" }[r.web_stack];
  return (
    <div className="stack">
      <PageHead title="Server software" text="What the server admin installed. Account owners can only pick from these." />
      <div className="grid-2">
        <section className="panel">
          <div className="panel-head"><h2>Web server</h2></div>
          <div className="panel-body stack">
            <p><strong>{stack}</strong></p>
            <div className="row">{r.web_servers.map((w, i) => <Badge key={w} tone={i === 0 ? "accent" : undefined}>{webServerLabel[w]}{i === 0 ? " (default)" : ""}</Badge>)}</div>
            <p className="small muted">Change with <code>install.sh --web nginx|apache|hybrid</code>.</p>
          </div>
        </section>
        <section className="panel">
          <div className="panel-head"><h2>Databases</h2></div>
          <div className="panel-body stack">
            <p>MariaDB with phpMyAdmin <Badge>stable 5.2</Badge></p>
            <p className="small muted">phpMyAdmin 6.0 is still in development. Switch with <code>--phpmyadmin 6</code> to test the preview.</p>
          </div>
        </section>
      </div>
      <section className="panel">
        <div className="panel-head"><h2>Languages</h2><span className="small muted">Install more with <code>--php all</code>, <code>--node</code>, <code>--python</code>, <code>--java</code>, <code>--go</code>, <code>--dart</code></span></div>
        <div className="table-wrap"><table>
          <tbody>
            {(["php", "node", "python", "java", "go", "dart"] as const).map((k) => (
              <tr key={k}><td style={{ width: 120 }}><strong>{runtimeName(k)}</strong></td>
                <td style={{ padding: "12px 20px" }}><div className="row">{(r[k] ?? []).length === 0 ? <span className="small muted">Not installed</span> : null}{(r[k] ?? []).map((v) => (
                  <Badge key={v} tone={k === "php" && phpEol(v) ? "warn" : k === "php" && v === r.default_php ? "accent" : undefined}>
                    {v}{k === "php" && v === r.default_php ? " (default)" : ""}{k === "php" && phpEol(v) ? " end of life" : ""}</Badge>
                ))}</div></td></tr>
            ))}
          </tbody>
        </table></div>
      </section>
      <section className="panel">
        <div className="panel-head"><h2>Tools</h2></div>
        <div className="panel-body row">
          {[["Composer", r.composer], ["Docker", r.docker], ["Buildpacks", r.buildpacks], ["Kubernetes (k3s)", r.kubernetes],
            [`HTTP/3${r.web?.nginx_version ? ` (Nginx ${r.web.nginx_version})` : ""}`, r.web?.http3]].map(([name, on]) => (
            <Badge key={String(name)} tone={on ? "ok" : undefined}>{String(name)}: {on ? "yes" : "no"}</Badge>
          ))}
        </div>
      </section>
    </div>
  );
}

export function AppInstaller() {
  const apps = useAsync(() => api.apps());
  const sites = useAsync(() => api.sites());
  const [target, setTarget] = useState<Site | null>(null);
  const [pick, setPick] = useState(false);
  const phpSites = (sites.data ?? []).filter((s) => s.runtime === "php" && s.status === "active");
  return (
    <div className="stack">
      <PageHead title="App installer" text="Open-source apps, installed into one of your websites in a minute. Each download is checksum-verified." />
      <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(240px, 1fr))", gap: 14 }}>
        {(apps.data ?? []).map((a) => (
          <section key={a.id} className="panel" style={{ padding: 18, display: "grid", gap: 10, alignContent: "start" }}>
            <div className="row" style={{ justifyContent: "space-between" }}>
              <h3>{a.name}</h3><Badge>{runtimeName(a.runtime)}</Badge>
            </div>
            <p className="small muted">{a.description}</p>
            {a.available ? <Button variant="primary" size="sm" onClick={() => setPick(true)}>Install</Button> : <Badge>Coming soon</Badge>}
          </section>
        ))}
      </div>
      {pick && !target ? (
        <section className="panel">
          <div className="panel-head"><h2>Install WordPress into</h2><Button variant="ghost" size="sm" onClick={() => setPick(false)}>Cancel</Button></div>
          {phpSites.length === 0 ? <Empty icon={<AppWindow size={22} />} title="No PHP websites" text="Create a PHP website first, then install WordPress into it." /> : (
            <div className="table-wrap"><table><tbody>{phpSites.map((s) => (
              <tr key={s.domain} className="clickable" onClick={() => setTarget(s)}><td>{s.domain}</td><td className="muted">{s.account}</td><td style={{ textAlign: "right" }}><Button size="sm">Choose</Button></td></tr>
            ))}</tbody></table></div>
          )}
        </section>
      ) : null}
      {target ? <WordPressDialog open site={target} onClose={() => { setTarget(null); setPick(false); }} /> : null}
    </div>
  );
}

/** Browser terminal. In the preview it simulates a shell; the live version streams
 *  a PTY that runs as the account user, never root. */
export function Terminal() {
  const accounts = useAsync(() => api.accounts());
  const [account, setAccount] = useState("");
  const [lines, setLines] = useState<string[]>([]);
  const [input, setInput] = useState("");
  const endRef = useRef<HTMLDivElement>(null);
  useEffect(() => { if (!account && accounts.data?.length) setAccount(accounts.data.find((a) => a.ssh === "shell")?.name || accounts.data[0].name); }, [accounts.data, account]);
  useEffect(() => { if (account) setLines([`Connected to vps-lagos-1 as ${account}. Type “help” to see what this preview can do.`]); }, [account]);
  useEffect(() => endRef.current?.scrollIntoView({ block: "end" }), [lines]);
  const prompt = `${account}@vps-lagos-1:~$`;
  const respond = (cmd: string): string[] => {
    const c = cmd.trim();
    if (!c) return [];
    if (c === "help") return ["Preview commands: whoami, pwd, ls, php -v, node -v, uptime, clear"];
    if (c === "whoami") return [account];
    if (c === "pwd") return [`/home/${account}`];
    if (c === "ls" || c === "ls web") return ["web  tmp"];
    if (c === "php -v") return ["PHP 8.4.13 (cli) (NTS)", "Copyright (c) The PHP Group"];
    if (c === "node -v") return ["v22.20.0"];
    if (c === "uptime") return [" 11:42:07 up 23 days,  9:36,  1 user,  load average: 0.62, 0.48, 0.41"];
    if (c === "sudo -i" || c.startsWith("sudo")) return [`${account} is not in the sudoers file. This incident has been logged.`];
    return [`${c.split(" ")[0]}: available when connected to a real server`];
  };
  return (
    <div className="stack">
      <PageHead title="Terminal" text="A shell in your browser that runs as the account's own user, with the same limits as SSH. Sessions are recorded in Activity."
        actions={<select className="select" style={{ width: "auto", minWidth: 180 }} value={account} onChange={(e) => setAccount(e.target.value)} aria-label="Account">
          {(accounts.data ?? []).filter((a) => !a.suspended).map((a) => <option key={a.name} value={a.name}>{a.name}</option>)}</select>} />
      <section className="panel" style={{ overflow: "hidden" }}>
        <div className="panel-head" style={{ background: "var(--surface-2)" }}>
          <span className="row small" style={{ gap: 8 }}><SquareTerminal size={16} />{account || "…"}</span>
          <Button size="sm" variant="ghost" onClick={() => setLines([])}><Trash2 size={14} />Clear</Button>
        </div>
        <div style={{ background: "#0c0d14", color: "#d9dcef", fontFamily: "var(--font-mono)", fontSize: "0.86rem", lineHeight: 1.6, padding: 16, minHeight: 360, maxHeight: 520, overflowY: "auto" }}
          onClick={() => document.getElementById("term-input")?.focus()}>
          {lines.map((l, i) => <div key={i} style={{ whiteSpace: "pre-wrap" }}>{l}</div>)}
          <form onSubmit={(e) => {
            e.preventDefault();
            if (input.trim() === "clear") { setLines([]); setInput(""); return; }
            setLines((ls) => [...ls, `${prompt} ${input}`, ...respond(input)]);
            setInput("");
          }} style={{ display: "flex", gap: 8 }}>
            <span style={{ color: "#7ee0a1" }}>{prompt}</span>
            <input id="term-input" value={input} onChange={(e) => setInput(e.target.value)} autoComplete="off" spellCheck={false} aria-label="Terminal input"
              style={{ flex: 1, background: "none", border: 0, outline: 0, color: "inherit", font: "inherit", padding: 0 }} />
          </form>
          <div ref={endRef} />
        </div>
      </section>
    </div>
  );
}
