import { useEffect, useState } from "react";
import { api } from "./api/client";
import { Construction } from "lucide-react";
import { useRoute, href } from "./router";
import { Shell } from "./layouts/Shell";
import { visibleGroups, isActive } from "./layouts/nav";
import { Login } from "./pages/Login";
import { Dashboard } from "./pages/Dashboard";
import { Accounts } from "./pages/Accounts";
import { Sites } from "./pages/Sites";
import { SiteDetail } from "./pages/SiteDetail";
import { Email } from "./pages/Email";
import { Cron, Databases } from "./pages/AccountTools";
import { ActivityPage, AiAgents, AppInstaller, Firewall, Software, Terminal } from "./pages/ServerPages";
import { Appearance } from "./pages/Appearance";
import { Migration, Security, Updates, Usage } from "./pages/Operations";
import { Alerts, Billing, People } from "./pages/Business";
import { Profile } from "./pages/Profile";
import { Empty } from "./components/ui";
import { PageHead } from "./components/common";
import type { Role } from "./api/types";

const soonText: Record<string, [string, string]> = {
  ssl: ["SSL certificates", "Free Let's Encrypt certificates for every website and mail domain, renewed automatically, plus wildcard and custom certificates."],
  dns: ["DNS zones", "A zone editor for every record type, DNSSEC, and Cloudflare sync, powered by PowerDNS."],
  backups: ["Backups", "Scheduled, encrypted, incremental backups of websites, databases and email to S3, Backblaze B2 or another server, with one-click restore."],
  plans: ["Hosting plans", "Named plans that bundle disk, bandwidth, website and email limits, with recurring invoices and reseller branding. Invoices, payments and limits already work on their own."],
  phpmyadmin: ["phpMyAdmin", "phpMyAdmin opens here with single sign-on once the control-plane API ships. Until then, reach it through an SSH tunnel on port 8090."],
};

function Soon({ id }: { id: string }) {
  const [title, text] = soonText[id] || ["Coming soon", "This section is on the roadmap."];
  return (
    <div className="stack">
      <PageHead title={title} />
      <section className="panel"><Empty icon={<Construction size={22} />} title="On the roadmap" text={text}
        action={<a className="btn" href="https://github.com/YOUR-ORG/ike/blob/main/docs/FEATURES.md" target="_blank" rel="noreferrer">See the feature tracker</a>} /></section>
    </div>
  );
}

function More({ role, route }: { role: Role; route: string }) {
  return (
    <div className="stack">
      <PageHead title="Everything" />
      {visibleGroups(role).map((g, i) => (
        <section key={i} className="panel">
          {g.title ? <div className="panel-head"><h2>{g.title}</h2></div> : null}
          <div className="panel-body" style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill, minmax(200px, 1fr))", gap: 6 }}>
            {g.items.map((it) => (
              <a key={it.to} className="nav-link" href={href(it.to)} aria-current={isActive(route, it.to) ? "page" : undefined}>
                <it.icon size={18} aria-hidden />{it.label}
              </a>
            ))}
          </div>
        </section>
      ))}
    </div>
  );
}

function loadSession(): { user: string; role: Role } | null {
  try { return JSON.parse(sessionStorage.getItem("ike.session") || "null"); } catch { return null; }
}
function saveSession(s: { user: string; role: Role } | null) {
  try { if (s) sessionStorage.setItem("ike.session", JSON.stringify(s)); else sessionStorage.removeItem("ike.session"); } catch { /* private mode */ }
}

export function App() {
  const route = useRoute();
  const [session, setSession] = useState(loadSession);
  // The panel answers 401 when a session expires; go back to sign-in.
  useEffect(() => {
    const out = () => { saveSession(null); setSession(null); };
    window.addEventListener("ike:signed-out", out);
    return () => window.removeEventListener("ike:signed-out", out);
  }, []);
  if (!session) {
    return <Login onLogin={(user, role) => { const s = { user, role }; saveSession(s); setSession(s); }} />;
  }
  const path = route.split("?")[0];
  const { role } = session;
  let page: JSX.Element;
  if (path === "/") page = <Dashboard user={session.user} />;
  else if (path === "/accounts") page = <Accounts role={role} />;
  else if (path === "/sites") page = <Sites route={route} role={role} />;
  else if (path.startsWith("/sites/")) page = <SiteDetail domain={decodeURIComponent(path.slice(7))} />;
  else if (path === "/email") page = <Email />;
  else if (path === "/databases") page = <Databases route={route} />;
  else if (path === "/cron") page = <Cron route={route} />;
  else if (path === "/apps") page = <AppInstaller />;
  else if (path === "/terminal") page = <Terminal />;
  else if (path === "/firewall") page = <Firewall />;
  else if (path === "/software") page = <Software />;
  else if (path === "/activity") page = <ActivityPage />;
  else if (path === "/ai") page = <AiAgents />;
  else if (path === "/appearance") page = <Appearance />;
  else if (path === "/updates") page = <Updates />;
  else if (path === "/security") page = <Security />;
  else if (path === "/usage") page = <Usage />;
  else if (path === "/migrate") page = <Migration />;
  else if (path === "/billing") page = <Billing role={role} />;
  else if (path === "/people") page = <People role={role} />;
  else if (path === "/alerts") page = <Alerts />;
  else if (path === "/profile") page = <Profile user={session.user} />;
  else if (path === "/more") page = <More role={role} route={route} />;
  else if (path.startsWith("/soon/")) page = <Soon id={path.slice(6)} />;
  else page = <Soon id="" />;

  return (
    <Shell route={path} user={session.user} role={role}
      onRole={(r) => { const s = { ...session, role: r }; saveSession(s); setSession(s); }}
      onLogout={() => { api.logout().catch(() => {}); saveSession(null); setSession(null); }}>
      {page}
    </Shell>
  );
}
