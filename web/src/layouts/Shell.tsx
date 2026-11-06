import { useEffect, useState, type ReactNode } from "react";
import { Globe, Gauge, Mail, Menu, Moon, Sun, Users, Database, X } from "lucide-react";
import { BrandLogo as Logo, CommandPalette, HelpButton, HelpDrawer, NotificationBell, PaletteButton, PoweredBy, ServerSwitcher,
  ShortcutsDialog, useShortcuts } from "../components/shellx";
import { languages, t, useI18n, type Lang } from "../i18n";
import { useAppearance } from "../theme/appearance";
import { href } from "../router";
import { isDemo } from "../api/client";
import type { Role } from "../api/types";
import { isActive, roleLabel, visibleGroups, type NavItem } from "./nav";

interface Props {
  route: string;
  user: string;
  role: Role;
  onRole: (r: Role) => void;
  onLogout: () => void;
  onServerChange: () => void;
  children: ReactNode;
}

function NavLink({ item, route, withTip }: { item: NavItem; route: string; withTip?: boolean }) {
  const Icon = item.icon;
  return (
    <a className="nav-link" href={href(item.to)} aria-current={isActive(route, item.to) ? "page" : undefined}>
      <Icon size={18} aria-hidden />
      {withTip ? <span className="tip">{t(item.label)}</span> : <span>{t(item.label)}</span>}
      {!withTip && item.soon ? <span className="soon">Soon</span> : null}
    </a>
  );
}

function TopRight({ user, role, onRole, onLogout, onHelp }: Pick<Props, "user" | "role" | "onRole" | "onLogout"> & { onHelp: () => void }) {
  const { resolvedMode, set } = useAppearance();
  const { lang, setLang, t } = useI18n();
  const [open, setOpen] = useState(false);
  return (
    <>
      {isDemo ? <label className="desktop-only" style={{ display: "flex", alignItems: "center", gap: 8 }}>
        <span className="small muted">View as</span>
        <select className="select" style={{ height: 34, width: "auto" }} value={role} onChange={(e) => onRole(e.target.value as Role)}>
          {(Object.keys(roleLabel) as Role[]).map((r) => <option key={r} value={r}>{roleLabel[r]}</option>)}
        </select>
      </label> : null}
      <button
        className="btn ghost icon"
        type="button"
        onClick={() => set({ mode: resolvedMode === "dark" ? "light" : "dark" })}
        aria-label={resolvedMode === "dark" ? "Switch to light mode" : "Switch to dark mode"}
      >
        {resolvedMode === "dark" ? <Sun size={18} /> : <Moon size={18} />}
      </button>
      <HelpButton onOpen={onHelp} />
      <NotificationBell />
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
            {isDemo ? <label className="mobile-only" style={{ display: "grid", gap: 4, padding: "6px 10px" }}>
              <span className="small muted">View as</span>
              <select className="select" value={role} onChange={(e) => { onRole(e.target.value as Role); setOpen(false); }}>
                {(Object.keys(roleLabel) as Role[]).map((r) => <option key={r} value={r}>{roleLabel[r]}</option>)}
              </select>
            </label> : null}
            <label style={{ display: "grid", gap: 4, padding: "6px 10px" }}>
              <span className="small muted">{t("Language")}</span>
              <select className="select" value={lang} onChange={(e) => setLang(e.target.value as Lang)}>
                {languages.map((l) => <option key={l.id} value={l.id}>{l.name}{l.id !== "en" ? ` (${l.english})` : ""}</option>)}
              </select>
            </label>
            <a className="nav-link" href={href("/appearance")} onClick={() => setOpen(false)}>{t("Appearance")}</a>
            <a className="nav-link" href={href("/profile")} onClick={() => setOpen(false)}>{t("Sign-in security")}</a>
            <a className="nav-link" href={href("/tokens")} onClick={() => setOpen(false)}>{t("API tokens")}</a>
            <button className="nav-link" type="button" onClick={onLogout}>{t("Sign out")}</button>
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
    { to: "/", label: t("Dashboard"), icon: Gauge },
    { to: "/sites", label: t("Websites"), icon: Globe },
    { to: "/email", label: t("Email"), icon: Mail },
    role === "user" ? { to: "/databases", label: t("Databases"), icon: Database } : { to: "/accounts", label: t("Accounts"), icon: Users },
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
                {g.title ? <div className="nav-group-title">{t(g.title)}</div> : null}
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
  useI18n(); // re-render on language change
  const groups = visibleGroups(props.role);
  const [palette, setPalette] = useState(false);
  const [keys, setKeys] = useState(false);
  const [helpOpen, setHelp] = useState(false);
  useShortcuts({ palette: () => setPalette(true), shortcuts: () => setKeys(true), help: () => setHelp(true) });
  const top = (
    <TopRight user={props.user} role={props.role} onRole={props.onRole} onLogout={props.onLogout} onHelp={() => setHelp(true)} />
  );
  const overlays = (
    <>
      <CommandPalette open={palette} onClose={() => setPalette(false)} role={props.role} onLogout={props.onLogout}
        onHelp={() => setHelp(true)} onShortcuts={() => setKeys(true)} />
      <ShortcutsDialog open={keys} onClose={() => setKeys(false)} />
      <HelpDrawer open={helpOpen} onClose={() => setHelp(false)} route={props.route} />
    </>
  );
  const switcher = <ServerSwitcher role={props.role} onChange={props.onServerChange} />;
  const search = <PaletteButton onOpen={() => setPalette(true)} />;

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
                  {t(it.label)}
                </a>
              ))}
              <a className="nav-link" href={href("/more")} aria-current={props.route === "/more" ? "page" : undefined}>{t("Everything")}</a>
            </nav>
            <div className="spacer" />
            <span className="desktop-only">{switcher}</span>
            {top}
          </header>
        </div>
        <main className="main"><div className="content">{props.children}</div></main>
        <MobileTabs route={props.route} role={props.role} />
        {overlays}
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
            <span className="desktop-only">{switcher}</span>
            {search}
            <div className="spacer" />
            {top}
          </header>
          <main className="content">{props.children}</main>
        </div>
        <MobileTabs route={props.route} role={props.role} />
        {overlays}
      </div>
    );
  }

  return (
    <div className="app layout-command">
      <aside className="sidebar" aria-label="Main">
        <a className="brand" href={href("/")} aria-label="Ike home"><Logo /></a>
        {switcher}
        <nav>
          {groups.map((g, i) => (
            <div key={i} className="nav-group">
              {g.title ? <div className="nav-group-title">{t(g.title)}</div> : null}
              {g.items.map((it) => <NavLink key={it.to} item={it} route={props.route} />)}
            </div>
          ))}
        </nav>
        <div className="sidebar-foot small muted"><PoweredBy /></div>
      </aside>
      <div className="main">
        <DemoRibbon />
        <header className="topbar">
          <span className="mobile-only"><Logo size={26} /></span>
          {search}
          <div className="spacer" />
          {top}
        </header>
        <main className="content">{props.children}</main>
      </div>
      <MobileTabs route={props.route} role={props.role} />
      {overlays}
    </div>
  );
}
