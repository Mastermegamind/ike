import { useEffect, useState } from "react";
import { Bot, Clock, Cpu, HardDrive, MemoryStick, Plus, UserRound } from "lucide-react";
import { api } from "../api/client";
import type { AuditEntry, Site, SystemInfo } from "../api/types";
import { Badge, Meter, Skeleton, timeAgo, useAsync } from "../components/ui";
import { PageHead, RuntimeBadge, StatusBadge } from "../components/common";
import { href } from "../router";

function uptime(s: number) {
  const d = Math.floor(s / 86400);
  const h = Math.floor((s % 86400) / 3600);
  return d > 0 ? { v: d, unit: d === 1 ? "day" : "days", rest: `${h} h` } : { v: h, unit: "hours", rest: "" };
}

export function describeAudit(e: AuditEntry): string {
  const parts = e.path.replace(/^\/v1\//, "").split("/");
  const thing = decodeURIComponent(parts[1] || "");
  const verb = { POST: "created", PUT: "changed", PATCH: "updated", DELETE: "removed", GET: "viewed" }[e.method] || e.method;
  const what: Record<string, string> = {
    sites: "website", accounts: "account", firewall: "firewall rule", mail: "email setting", audit: "audit log",
  };
  const tail = parts.slice(2).join(" ");
  if (parts[0] === "sites" && parts.length === 1) return e.method === "GET" ? "Listed websites" : "Created a website";
  if (tail === "suspend") return `Suspended ${thing}`;
  if (tail === "unsuspend") return `Reactivated ${thing}`;
  if (tail === "status") return `Changed the status of ${thing}`;
  if (tail.startsWith("ssh")) return `Changed SSH access for ${thing}`;
  if (tail.startsWith("cron")) return `Edited cron jobs for ${thing}`;
  if (tail.startsWith("databases")) return `Changed databases for ${thing}`;
  if (tail.startsWith("app")) return `${parts[3] ? parts[3][0].toUpperCase() + parts[3].slice(1) + "ed" : "Managed"} the app on ${thing}`;
  if (parts[0] === "firewall") return `Firewall: ${verb} ${parts[1]?.replace(/s$/, "") || "rule"}`;
  return `${verb[0].toUpperCase() + verb.slice(1)} ${what[parts[0]] || parts[0]}${thing ? " " + thing : ""}`;
}

export function ActorIcon({ actor }: { actor: string }) {
  const ai = actor.startsWith("ai:");
  return <span className={`actor ${ai ? "ai" : ""}`} aria-hidden>{ai ? <Bot size={15} /> : <UserRound size={15} />}</span>;
}

export function Dashboard({ user }: { user: string }) {
  const [sys, setSys] = useState<SystemInfo | null>(null);
  const services = useAsync(() => api.services());
  const sites = useAsync(() => api.sites());
  const activity = useAsync(() => api.audit());

  useEffect(() => {
    let alive = true;
    const tick = () => api.system().then((s) => alive && setSys(s));
    tick();
    const t = setInterval(tick, 5000);
    return () => { alive = false; clearInterval(t); };
  }, []);

  const memUsed = sys ? sys.memory_total_mb - sys.memory_available_mb : 0;
  const memPct = sys ? (memUsed / sys.memory_total_mb) * 100 : 0;
  const diskUsed = sys ? sys.disk_total_gb - sys.disk_free_gb : 0;
  const diskPct = sys ? (diskUsed / sys.disk_total_gb) * 100 : 0;
  const loadPct = sys ? (Number(sys.load_average[0]) / sys.cpus) * 100 : 0;
  const up = sys ? uptime(sys.uptime_seconds) : null;
  const hour = new Date().getHours();
  const greeting = hour < 12 ? "Good morning" : hour < 17 ? "Good afternoon" : "Good evening";
  const down = services.data?.filter((s) => s.active !== "active") ?? [];

  return (
    <div className="stack">
      <PageHead
        title={`${greeting}, ${user}`}
        text={down.length ? `${down.length} service${down.length > 1 ? "s need" : " needs"} attention.` : "Everything on vps-lagos-1 is running."}
        actions={<a className="btn primary" href={href("/sites?new=1")}><Plus size={17} />Create website</a>}
      />

      <section className="vitals" aria-label="Server health">
        <div className="vital">
          <div className="vital-label"><Cpu size={16} aria-hidden />CPU load</div>
          {sys ? <div className="vital-value">{sys.load_average[0]}<small>of {sys.cpus} cores</small></div> : <Skeleton h={40} w={120} />}
          <Meter value={loadPct} label="CPU load" />
        </div>
        <div className="vital">
          <div className="vital-label"><MemoryStick size={16} aria-hidden />Memory</div>
          {sys ? <div className="vital-value">{(memUsed / 1024).toFixed(1)}<small>of {(sys.memory_total_mb / 1024).toFixed(1)} GB</small></div> : <Skeleton h={40} w={120} />}
          <Meter value={memPct} label="Memory used" />
        </div>
        <div className="vital">
          <div className="vital-label"><HardDrive size={16} aria-hidden />Disk</div>
          {sys ? <div className="vital-value">{diskUsed.toFixed(0)}<small>of {sys.disk_total_gb.toFixed(0)} GB</small></div> : <Skeleton h={40} w={120} />}
          <Meter value={diskPct} label="Disk used" />
        </div>
        <div className="vital">
          <div className="vital-label"><Clock size={16} aria-hidden />Uptime</div>
          {up ? <div className="vital-value">{up.v}<small>{up.unit}{up.rest ? `, ${up.rest}` : ""}</small></div> : <Skeleton h={40} w={120} />}
          <p className="small muted">{sys ? `${sys.os} ${sys.os_version}, kernel ${sys.kernel}` : " "}</p>
        </div>
      </section>

      <div className="grid-2">
        <section className="panel">
          <div className="panel-head">
            <h2>Websites</h2>
            <a className="btn ghost sm" href={href("/sites")}>View all</a>
          </div>
          <div className="table-wrap">
            <table className="responsive">
              <tbody>
                {(sites.data ?? []).slice(0, 6).map((s: Site) => (
                  <tr key={s.domain} className="clickable" onClick={() => (window.location.hash = `/sites/${s.domain}`)}>
                    <td className="lead"><a href={href(`/sites/${s.domain}`)} className="primary-cell" style={{ textDecoration: "none" }}>{s.domain}</a>
                      <div className="small muted">{s.account}</div></td>
                    <td><RuntimeBadge runtime={s.runtime} version={s.version} /></td>
                    <td style={{ textAlign: "right" }}><StatusBadge status={s.status} /></td>
                  </tr>
                ))}
                {sites.loading && !sites.data ? <tr><td colSpan={3} style={{ padding: 20 }}><Skeleton h={18} /></td></tr> : null}
              </tbody>
            </table>
          </div>
        </section>

        <section className="panel">
          <div className="panel-head"><h2>Services</h2><Badge tone={down.length ? "bad" : "ok"}>{down.length ? `${down.length} down` : "All running"}</Badge></div>
          <div className="services">
            {(services.data ?? []).map((s) => (
              <div className="service" key={s.name}>
                <span className={`dot ${s.active === "active" ? "" : "bad"}`} aria-hidden />
                {s.name}
                <span className="sr-only">{s.active}</span>
              </div>
            ))}
          </div>
        </section>
      </div>

      <section className="panel">
        <div className="panel-head">
          <div><h2>Recent activity</h2><p className="small muted">Changes by people and AI agents on this server.</p></div>
          <a className="btn ghost sm" href={href("/activity")}>Full log</a>
        </div>
        <ul className="activity">
          {(activity.data ?? []).filter((e) => e.method !== "GET").slice(0, 5).map((e, i) => (
            <li key={i}>
              <ActorIcon actor={e.actor} />
              <div>
                <div>{describeAudit(e)}</div>
                <div className="small muted">{e.actor.startsWith("ai:") ? "AI agent" : e.actor}, {timeAgo(e.time)}</div>
              </div>
              {e.result === "ok" ? null : <Badge tone={e.result === "denied" ? "warn" : "bad"}>{e.result === "denied" ? "Blocked" : "Failed"}</Badge>}
            </li>
          ))}
        </ul>
      </section>
    </div>
  );
}
