import { useEffect, useRef, useState, type ReactNode } from "react";
import { Globe, Gauge, Mail, Menu, Moon, Search, Sun, Users, Database, X } from "lucide-react";
import { Logo } from "../components/brand";
import { useAppearance } from "../theme/appearance";
import { href, navigate } from "../router";
import { isDemo } from "../api/client";
import type { Role } from "../api/types";
import { isActive, roleLabel, visibleGroups, type NavItem } from "./nav";

interface Props {
  route: string;
  user: string;
  role: Role;
  onRole: (r: Role) => void;
  onLogout: () => void;
  children: ReactNode;
}

function NavLink({ item, route, withTip }: { item: NavItem; route: string; withTip?: boolean }) {
  const Icon = item.icon;
  return (
    <a className="nav-link" href={href(item.to)} aria-current={isActive(route, item.to) ? "page" : undefined}>
      <Icon size={18} aria-hidden />
      {withTip ? <span className="tip">{item.label}</span> : <span>{item.label}</span>}
      {!withTip && item.soon ? <span className="soon">Soon</span> : null}
    </a>
  );
}

function ServerSwitch() {
  return (
    <button className="server-switch" type="button" title="Switch server (multi-server arrives in phase 6)">
      <span className="dot" aria-hidden />
      <span>
        <span className="name">vps-lagos-1</span>
        <span className="sub" style={{ display: "block" }}>Ubuntu 24.04</span>
      </span>
    </button>
  );
}

function GlobalSearch() {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        ref.current?.focus();
      }
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);
  return (
    <form
      className="search desktop-only"
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        const q = ref.current?.value.trim();
        if (q) navigate(`/sites?q=${encodeURIComponent(q)}`);
      }}
    >
      <Search size={16} aria-hidden />
      <input ref={ref} placeholder="Search websites, accounts, mailboxes" aria-label="Search" />
      <kbd>Ctrl K</kbd>
    </form>
  );
}

function TopRight({ user, role, onRole, onLogout }: Pick<Props, "user" | "role" | "onRole" | "onLogout">) {
  const { resolvedMode, set } = useAppearance();
  const [open, setOpen] = useState(false);
  return (
    <>
      <label className="desktop-only" style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span className="small muted">View as</span>
        <select className="select" style={{ height: 34, width: "auto" }} value={role} onChange={(e) => onRole(e.target.value as Role)}>
          {(Object.keys(roleLabel) as Role[]).map((r) => <option key={r} value={r}>{roleLabel[r]}</option>)}
        </select>
      </label>
      <button
        className="btn ghost icon"
        type="button"
        onClick={() => set({ mode: resolvedMode === "dark" ? "light" : "dark" })}
        aria-label={resolvedMode === "dark" ? "Switch to light mode" : "Switch to dark mode"}
      >
        {resolvedMode === "dark" ? <Sun size={18} /> : <Moon size={18} />}
      </button>
      <div style={{ position: "relative" }}>
        <button className="avatar" type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open} aria-label="Account menu">
          {user.slice(0, 2).toUpperCase()}
        </button>
        {open && (
          <div className="panel" style={{ position: "absolute", right: 0, top: 44, width: 220, padding: 6, boxShadow: "var(--shadow-pop)", zIndex: 40 }}>
            <div style={{ padding: "8px 10px" }}>
              <strong>{user}</strong>
              <div className="small muted">{roleLabel[role]}</div>
            </div>
            <label className="mobile-only" style={{ display: "grid", gap: 4, padding: "6px 10px" }}>
              <span className="small muted">View as</span>
              <select className="select" value={role} onChange={(e) => { onRole(e.target.value as Role); setOpen(false); }}>
                {(Object.keys(roleLabel) as Role[]).map((r) => <option key={r} value={r}>{roleLabel[r]}</option>)}
              </select>
            </label>
            <a className="nav-link" href={href("/appearance")} onClick={() => setOpen(false)}>Appearance</a>
            <button className="nav-link" type="button" onClick={onLogout}>Sign out</button>
          </div>
        )}
      </div>
    </>
  );
}

function MobileTabs({ route, role }: { route: string; role: Role }) {
  const [more, setMore] = useState(false);
  useEffect(() => setMore(false), [route]);
  const tabs = [
    { to: "/", label: "Home", icon: Gauge },
    { to: "/sites", label: "Websites", icon: Globe },
    { to: "/email", label: "Email", icon: Mail },
    role === "user" ? { to: "/databases", label: "Databases", icon: Database } : { to: "/accounts", label: "Accounts", icon: Users },
  ];
  return (
    <>
      <nav className="bottom-tabs" aria-label="Main">
        {tabs.map((t) => (
          <a key={t.to} href={href(t.to)} aria-current={isActive(route, t.to) ? "page" : undefined}>
            <t.icon size={20} aria-hidden />
            {t.label}
          </a>
        ))}
        <button type="button" onClick={() => setMore(true)} aria-expanded={more}>
          <Menu size={20} aria-hidden />
          More
        </button>
      </nav>
      {more && (
        <div
          role="dialog"
          aria-label="All sections"
          style={{ position: "fixed", inset: 0, zIndex: 60, background: "color-mix(in srgb, var(--ink) 40%, transparent)" }}
          onClick={() => setMore(false)}
        >
          <div
            className="panel"
            style={{ position: "absolute", inset: "auto 0 0 0", maxHeight: "80vh", overflowY: "auto", borderRadius: "var(--r-lg) var(--r-lg) 0 0", padding: "12px 12px 28px" }}
            onClick={(e) => e.stopPropagation()}
          >
            <div className="row" style={{ justifyContent: "space-between", padding: "4px 6px 8px" }}>
              <Logo size={26} />
              <button className="btn ghost icon" onClick={() => setMore(false)} aria-label="Close"><X size={18} /></button>
            </div>
            {visibleGroups(role).map((g, i) => (
              <div key={i} className="nav-group">
                {g.title ? <div className="nav-group-title">{g.title}</div> : null}
                {g.items.map((it) => <NavLink key={it.to} item={it} route={route} />)}
              </div>
            ))}
          </div>
        </div>
      )}
    </>
  );
}

function DemoRibbon() {
  if (!isDemo) return null;
  return <div className="demo-ribbon">Preview with sample data. Changes you make here aren't saved to a server.</div>;
}

export function Shell(props: Props) {
  const { template } = useAppearance();
  const groups = visibleGroups(props.role);
  const top = (
    <TopRight user={props.user} role={props.role} onRole={props.onRole} onLogout={props.onLogout} />
  );

  if (template === "horizon") {
    const flat = groups.flatMap((g) => g.items).filter((i) => !i.soon);
    const primary = flat.slice(0, 6);
    return (
      <div className="app layout-horizon">
        <div>
          <DemoRibbon />
          <header className="topbar">
            <a href={href("/")} aria-label="Ike home"><Logo size={28} /></a>
            <nav className="hnav" aria-label="Main">
              {primary.map((it) => (
                <a key={it.to} className="nav-link" href={href(it.to)} aria-current={isActive(props.route, it.to) ? "page" : undefined}>
                  {it.label}
                </a>
              ))}
              <a className="nav-link" href={href("/more")} aria-current={props.route === "/more" ? "page" : undefined}>More</a>
            </nav>
            <div className="spacer" />
            <span className="desktop-only"><ServerSwitch /></span>
            {top}
          </header>
        </div>
        <main className="main"><div className="content">{props.children}</div></main>
        <MobileTabs route={props.route} role={props.role} />
      </div>
    );
  }

  if (template === "rail") {
    return (
      <div className="app layout-rail">
        <nav className="rail" aria-label="Main">
          <a className="brand" href={href("/")} aria-label="Ike home"><Logo size={32} withWord={false} /></a>
          {groups.map((g, i) => (
            <div key={i} style={{ display: "contents" }}>
              {i > 0 ? <div className="sep" /> : null}
              {g.items.filter((it) => !it.soon).map((it) => <NavLink key={it.to} item={it} route={props.route} withTip />)}
            </div>
          ))}
        </nav>
        <div className="main">
          <DemoRibbon />
          <header className="topbar">
            <span className="mobile-only"><Logo size={26} /></span>
            <span className="desktop-only"><ServerSwitch /></span>
            <GlobalSearch />
            <div className="spacer" />
            {top}
          </header>
          <main className="content">{props.children}</main>
        </div>
        <MobileTabs route={props.route} role={props.role} />
      </div>
    );
  }

  return (
    <div className="app layout-command">
      <aside className="sidebar" aria-label="Main">
        <a className="brand" href={href("/")} aria-label="Ike home"><Logo /></a>
        <ServerSwitch />
        <nav>
          {groups.map((g, i) => (
            <div key={i} className="nav-group">
              {g.title ? <div className="nav-group-title">{g.title}</div> : null}
              {g.items.map((it) => <NavLink key={it.to} item={it} route={props.route} />)}
            </div>
          ))}
        </nav>
        <div className="sidebar-foot small muted">Ike 0.2, open source under AGPL-3.0</div>
      </aside>
      <div className="main">
        <DemoRibbon />
        <header className="topbar">
          <span className="mobile-only"><Logo size={26} /></span>
          <GlobalSearch />
          <div className="spacer" />
          {top}
        </header>
        <main className="content">{props.children}</main>
      </div>
      <MobileTabs route={props.route} role={props.role} />
    </div>
  );
}
