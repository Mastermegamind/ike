// Top-bar extras: notification centre, command palette (Ctrl K), keyboard
// shortcuts, the help drawer, the server switcher and white-label branding.
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  ArrowRight, Bell, CircleHelp, Command, Globe, Keyboard, LogOut, Moon, Plus, Search, Server, Users, X,
} from "lucide-react";
import { api, getServer, isDemo, setServer } from "../api/client";
import type { Branding, RemoteServer, ServerEvent } from "../api/types5";
import type { Role } from "../api/types";
import { navigate } from "../router";
import { visibleGroups } from "../layouts/nav";
import { useI18n } from "../i18n";
import { applyBrandAccent, useAppearance } from "../theme/appearance";
import { Logo } from "./brand";
import { Button, Modal, timeAgo } from "./ui";

/* ------------------------------------------------------------ branding */

let brandCache: Branding | null = null;
const brandListeners = new Set<(b: Branding) => void>();

function inkFor(hex: string) {
  const n = parseInt(hex.slice(1), 16);
  const [r, g, b] = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b > 0.4 ? "#111418" : "#ffffff";
}

/** Loads the panel's brand (the admin's, or a reseller's for their customers). */
export function loadBranding() {
  api.call<Branding>("GET", "/branding").then((b) => {
    brandCache = b;
    const root = document.documentElement;
    if (b.primary_color) {
      root.style.setProperty("--brand-accent", b.primary_color);
      root.style.setProperty("--brand-accent-ink", inkFor(b.primary_color));
      root.dataset.brand = "1";
      applyBrandAccent();
    } else {
      delete root.dataset.brand;
    }
    document.title = b.product_name && b.product_name !== "Ike" ? b.product_name : "Ike";
    brandListeners.forEach((fn) => fn(b));
  }).catch(() => { /* default look */ });
}

export function useBranding(): Branding {
  const [b, setB] = useState<Branding>(brandCache ?? { product_name: "Ike", primary_color: "" });
  useEffect(() => {
    brandListeners.add(setB);
    return () => { brandListeners.delete(setB); };
  }, []);
  return b;
}

export function BrandLogo({ size = 30, withWord = true }: { size?: number; withWord?: boolean }) {
  const b = useBranding();
  if (!b.logo && (!b.product_name || b.product_name === "Ike")) return <Logo size={size} withWord={withWord} />;
  return (
    <span className="brand" aria-label={b.product_name}>
      {b.logo ? <img src={b.logo} alt="" width={size} height={size} style={{ borderRadius: 8, objectFit: "contain" }} /> : <Logo size={size} withWord={false} />}
      {withWord ? <span className="brand-word">{b.product_name}</span> : null}
    </span>
  );
}

export function PoweredBy() {
  const b = useBranding();
  if (b.hide_powered_by) return b.support_email ? <span>Help: {b.support_email}</span> : null;
  return <span>{b.product_name && b.product_name !== "Ike" ? "Powered by Ike 0.5" : "Ike 0.5, open source under AGPL-3.0"}</span>;
}

/* ------------------------------------------------------- notifications */

const levelColor = { info: "var(--accent)", warn: "var(--warn)", bad: "var(--bad)" };

export function describeEvent(e: ServerEvent): string {
  const words: Record<string, string> = {
    "dns.zone.created": "DNS zone added", "dns.zone.changed": "DNS records changed", "dns.zone.deleted": "DNS zone removed",
    "dns.domain.expiring": "Domain expires soon", "dns.domain.registered": "Domain registered", "dns.domain.renewed": "Domain renewed",
    "mail.mailbox.created": "Mailbox created", "mail.domain.created": "Email turned on", "mail.mailbox.restored": "Mailbox restored",
    "backup.finished": "Backup finished", "backup.failed": "Backup failed", "backup.check.failed": "Backup check failed", "backup.missed": "Backup missed",
    "malware.infected": "Malware found", "malware.scan.finished": "Malware scan finished", "service.restart": "Service restarted",
    "service.stop": "Service stopped", "service.start": "Service started", "secrets.rotated": "Master key rotated",
    "migrate.cpanel.restored": "cPanel backup restored", "site.created": "Website created", "site.deleted": "Website removed",
    "cluster.failover": "Cluster failover", "maintenance.started": "Maintenance started", "maintenance.finished": "Maintenance finished",
  };
  const base = words[e.type] ?? e.type.replace(/\./g, " ");
  const days = e.data?.days_left;
  return typeof days === "number" ? `${base} (${days} days left)` : base;
}

export function NotificationBell() {
  const { t } = useI18n();
  const { saving } = useAppearance();
  const [items, setItems] = useState<ServerEvent[]>([]);
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const load = useCallback(() => { api.call<ServerEvent[]>("GET", "/notifications").then(setItems).catch(() => {}); }, []);
  useEffect(() => {
    load();
    const id = setInterval(load, saving ? 300_000 : 60_000);
    return () => clearInterval(id);
  }, [load, saving]);
  useEffect(() => {
    if (!open) return;
    const close = (e: MouseEvent) => { if (box.current && !box.current.contains(e.target as Node)) setOpen(false); };
    document.addEventListener("mousedown", close);
    return () => document.removeEventListener("mousedown", close);
  }, [open]);
  const unread = items.filter((i) => i.unread).length;
  const markRead = async () => {
    if (!items.length) return;
    await api.call("POST", "/notifications/read", { up_to: Math.max(...items.map((i) => i.id)) }).catch(() => {});
    setItems(items.map((i) => ({ ...i, unread: false })));
  };
  return (
    <div ref={box} style={{ position: "relative" }}>
      <button className="btn ghost icon" type="button" onClick={() => setOpen((o) => !o)} aria-expanded={open}
        aria-label={unread ? `${t("Notifications")}: ${unread} ${t("Unread").toLowerCase()}` : t("Notifications")}>
        <Bell size={18} />
        {unread ? <span className="bell-dot" aria-hidden>{unread > 9 ? "9+" : unread}</span> : null}
      </button>
      {open && (
        <div className="panel popover notif-pop" role="dialog" aria-label={t("Notifications")}>
          <div className="row" style={{ justifyContent: "space-between", padding: "10px 14px", borderBottom: "1px solid var(--line)" }}>
            <strong>{t("Notifications")}</strong>
            <Button size="sm" variant="ghost" onClick={markRead} disabled={!unread}>{t("Mark all as read")}</Button>
          </div>
          {items.length === 0 ? <p className="muted small" style={{ padding: 16 }}>{t("No notifications yet")}</p> : (
            <ul className="notif-list">
              {items.slice(0, 12).map((e) => (
                <li key={e.id} data-unread={e.unread ? "1" : undefined}>
                  <span className="dot" style={{ background: levelColor[e.level] }} aria-hidden />
                  <div style={{ minWidth: 0 }}>
                    <div style={{ fontWeight: e.unread ? 600 : 500 }}>{describeEvent(e)}</div>
                    <div className="small muted" style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{e.subject} · {timeAgo(e.time)}</div>
                  </div>
                </li>
              ))}
            </ul>
          )}
          <a className="nav-link" style={{ justifyContent: "center", borderTop: "1px solid var(--line)" }} href="#/notifications" onClick={() => setOpen(false)}>
            See all<ArrowRight size={15} aria-hidden />
          </a>
        </div>
      )}
    </div>
  );
}

/* ------------------------------------------------------ command palette */

interface PaletteItem { id: string; label: string; hint?: string; group: string; icon?: ReactNode; run: () => void; keywords?: string }

export function CommandPalette({ open, onClose, role, onLogout, onHelp, onShortcuts }: {
  open: boolean; onClose: () => void; role: Role; onLogout: () => void; onHelp: () => void; onShortcuts: () => void;
}) {
  const { t } = useI18n();
  const { resolvedMode, set } = useAppearance();
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const [found, setFound] = useState<PaletteItem[]>([]);
  const input = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (!open) return;
    setQ(""); setSel(0);
    setTimeout(() => input.current?.focus(), 30);
    // websites and accounts to jump to
    Promise.all([api.sites().catch(() => []), role !== "user" ? api.accounts().catch(() => []) : Promise.resolve([])]).then(([sites, accts]) => {
      setFound([
        ...sites.map((s) => ({ id: "site:" + s.domain, label: s.domain, hint: s.account, group: t("Websites"), icon: <Globe size={16} />, run: () => navigate(`/sites/${encodeURIComponent(s.domain)}`) })),
        ...accts.map((a) => ({ id: "acct:" + a.name, label: a.name, hint: `${a.sites} websites`, group: t("Accounts"), icon: <Users size={16} />, run: () => navigate(`/accounts?account=${encodeURIComponent(a.name)}`) })),
      ]);
    });
  }, [open, role, t]);
  const items = useMemo(() => {
    const go: PaletteItem[] = visibleGroups(role).flatMap((g) => g.items).map((it) => ({
      id: "nav:" + it.to, label: t(it.label), group: t("Go to"), icon: <it.icon size={16} />, run: () => navigate(it.to), keywords: it.label,
    }));
    const acts: PaletteItem[] = [
      { id: "a:site", label: t("Add website"), group: t("Actions"), icon: <Plus size={16} />, run: () => navigate("/sites?new=1") },
      ...(role !== "user" ? [{ id: "a:acct", label: t("Add account"), group: t("Actions"), icon: <Plus size={16} />, run: () => navigate("/accounts?new=1") }] : []),
      { id: "a:zone", label: t("Add zone"), hint: "DNS", group: t("Actions"), icon: <Plus size={16} />, run: () => navigate("/dns?new=1") },
      { id: "a:mailbox", label: t("Add mailbox"), group: t("Actions"), icon: <Plus size={16} />, run: () => navigate("/email?new=1") },
      { id: "a:backup", label: t("New backup plan"), group: t("Actions"), icon: <Plus size={16} />, run: () => navigate("/backups?new=1") },
      { id: "a:theme", label: t("Switch theme"), hint: resolvedMode === "dark" ? "light" : "dark", group: t("Actions"), icon: <Moon size={16} />, run: () => set({ mode: resolvedMode === "dark" ? "light" : "dark" }) },
      { id: "a:help", label: t("Open help"), hint: "?", group: t("Actions"), icon: <CircleHelp size={16} />, run: onHelp },
      { id: "a:keys", label: t("Keyboard shortcuts"), group: t("Actions"), icon: <Keyboard size={16} />, run: onShortcuts },
      { id: "a:out", label: t("Sign out"), group: t("Actions"), icon: <LogOut size={16} />, run: onLogout },
    ];
    const all = [...acts, ...go, ...found];
    const needle = q.trim().toLowerCase();
    if (!needle) return [...acts.slice(0, 5), ...go.slice(0, 8)];
    return all.filter((i) => (i.label + " " + (i.hint ?? "") + " " + (i.keywords ?? "")).toLowerCase().includes(needle)).slice(0, 30);
  }, [q, found, role, t, resolvedMode, set, onHelp, onShortcuts, onLogout]);
  useEffect(() => setSel(0), [q]);
  const pick = (i: PaletteItem | undefined) => { if (!i) return; onClose(); i.run(); };
  let lastGroup = "";
  return (
    <Modal open={open} onClose={onClose} title={t("Type a command or search")}>
      <div className="palette">
        <div className="search" style={{ width: "100%" }}>
          <Search size={16} aria-hidden />
          <input ref={input} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t("Type a command or search") + "…"}
            aria-label={t("Search")} role="combobox" aria-expanded aria-controls="palette-list"
            aria-activedescendant={items[sel] ? "pal-" + items[sel].id : undefined}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") { e.preventDefault(); setSel((s) => Math.min(s + 1, items.length - 1)); }
              else if (e.key === "ArrowUp") { e.preventDefault(); setSel((s) => Math.max(s - 1, 0)); }
              else if (e.key === "Enter") { e.preventDefault(); pick(items[sel]); }
            }} />
        </div>
        <ul id="palette-list" role="listbox" className="palette-list">
          {items.length === 0 ? <li className="muted small" style={{ padding: 12 }}>{t("No matches")}</li> : items.map((it, i) => {
            const head = it.group !== lastGroup ? it.group : null;
            lastGroup = it.group;
            return (
              <li key={it.id} style={{ display: "contents" }}>
                {head ? <div className="palette-group">{head}</div> : null}
                <div id={"pal-" + it.id} role="option" aria-selected={i === sel} className="palette-item"
                  onMouseEnter={() => setSel(i)} onClick={() => pick(it)}>
                  {it.icon}<span style={{ minWidth: 0, overflow: "hidden", textOverflow: "ellipsis" }}>{it.label}</span>
                  {it.hint ? <span className="small muted" style={{ marginLeft: "auto" }}>{it.hint}</span> : null}
                </div>
              </li>
            );
          })}
        </ul>
        <p className="small muted" style={{ marginTop: 10 }}><kbd>↑</kbd> <kbd>↓</kbd> to move, <kbd>Enter</kbd> to open, <kbd>Esc</kbd> to close</p>
      </div>
    </Modal>
  );
}

export function PaletteButton({ onOpen }: { onOpen: () => void }) {
  const { t } = useI18n();
  const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);
  return (
    <button type="button" className="search desktop-only" onClick={onOpen} aria-label={t("Type a command or search")} style={{ cursor: "pointer", textAlign: "left" }}>
      <Search size={16} aria-hidden />
      <span className="muted" style={{ flex: 1 }}>{t("Search websites, accounts, mailboxes")}</span>
      <kbd>{mac ? "⌘" : "Ctrl"} K</kbd>
    </button>
  );
}

/* ------------------------------------------------------------ shortcuts */

export const shortcutList: [string, string][] = [
  ["Ctrl K  or  /", "Command palette and search"],
  ["?", "Show keyboard shortcuts"],
  ["g then d", "Dashboard"], ["g then s", "Websites"], ["g then a", "Accounts"], ["g then e", "Email"],
  ["g then n", "DNS zones"], ["g then b", "Backups"], ["g then f", "Files"], ["g then t", "Terminal"],
  ["g then h", "Help for this page"], ["Esc", "Close a dialog"],
];

const goKeys: Record<string, string> = { d: "/", s: "/sites", a: "/accounts", e: "/email", n: "/dns", b: "/backups", f: "/files", t: "/terminal", u: "/usage", m: "/malware" };

export function useShortcuts(h: { palette: () => void; shortcuts: () => void; help: () => void }) {
  const ref = useRef(h);
  ref.current = h;
  useEffect(() => {
    let g = 0;
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); ref.current.palette(); return; }
      const el = e.target as HTMLElement;
      if (el && (el.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(el.tagName) || el.closest(".xterm"))) return;
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (document.querySelector("dialog[open]")) return;
      if (e.key === "/") { e.preventDefault(); ref.current.palette(); return; }
      if (e.key === "?") { e.preventDefault(); ref.current.shortcuts(); return; }
      if (e.key === "g") { g = Date.now(); return; }
      if (Date.now() - g < 1200) {
        g = 0;
        if (e.key === "h") { ref.current.help(); return; }
        const to = goKeys[e.key];
        if (to) { e.preventDefault(); navigate(to); }
      }
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, []);
}

export function ShortcutsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { t } = useI18n();
  return (
    <Modal open={open} onClose={onClose} title={t("Keyboard shortcuts")} footer={<Button variant="primary" onClick={onClose}>{t("Close")}</Button>}>
      <table className="table">
        <tbody>
          {shortcutList.map(([k, what]) => (
            <tr key={k}><td style={{ whiteSpace: "nowrap" }}>{k.split("  or  ").map((x, i) => <span key={x}>{i ? " or " : ""}{x.split(" then ").map((y, j) => <span key={y}>{j ? " then " : ""}<kbd>{y}</kbd></span>)}</span>)}</td><td>{t(what)}</td></tr>
          ))}
        </tbody>
      </table>
    </Modal>
  );
}

/* ---------------------------------------------------------- help drawer */

const help: [string, string, string[]][] = [
  ["/dns", "DNS zones", [
    "A DNS zone holds the records for one domain: where the website is (A and AAAA), where mail goes (MX), and text records for SPF, DKIM, DMARC and site verification.",
    "Records only take effect once your registrar points the domain at the name servers shown on the zone. The Check tab asks public resolvers what they see.",
    "Templates add the usual records for a website, email on this server, Google Workspace, Microsoft 365 or Zoho in one step.",
  ]],
  ["/email", "Email", [
    "Turn email on for a domain, then add mailboxes and forwarders. Each mailbox has its own password and storage limit.",
    "The Deliverability tab checks MX, SPF, DKIM, DMARC, reverse DNS and blocklists. If the domain's DNS zone is here, Ike adds the missing records for you.",
    "Webmail is at the address shown on the domain. Mail apps use mail.<your domain> with ports 993 (IMAP) and 465 or 587 (sending).",
  ]],
  ["/files", "Files", [
    "The file manager works as the account's own user, so it can only reach that account's files.",
    "Snapshots are quick restore points of the whole home folder. Share links let someone download a file without an account, with an expiry and an optional password.",
  ]],
  ["/backups", "Backups", [
    "A backup plan copies a server, an account, a website, databases or mail to a storage target on a schedule. Backups are incremental and encrypted.",
    "Keep the recovery key shown when you create a plan somewhere safe: with it, you can restore onto a new server even if this one is gone.",
    "Integrity checks read the backups back regularly so you find out about damage before you need a restore.",
  ]],
  ["/databases", "Databases", [
    "Each account can have MariaDB, PostgreSQL and MongoDB databases and its own Redis or Valkey instance, if the admin installed them.",
    "Connection details are shown once, when a database is created. Make a new password any time from the database's menu.",
  ]],
  ["/malware", "Malware scanner", [
    "Scans use ClamAV signatures plus checks for common PHP backdoors. Found files can be moved to quarantine, where they can't run, and restored if they were fine.",
  ]],
  ["/servers", "Infrastructure", [
    "Add other Ike servers with a one-time join command. After joining, pick a server in the switcher at the top to manage it from here.",
    "Clusters keep MariaDB or Redis copies on other servers in sync, and let you promote a copy if the primary fails.",
  ]],
  ["/packages", "Hosting packages", [
    "A package sets an account's limits: websites, domains, databases, mailboxes, disk, monthly bandwidth and features like shell access.",
    "Subscriptions bill an account for its package every month or year; invoices go out 7 days before they're due.",
  ]],
  ["/sites", "Websites", [
    "Each website belongs to an account and runs PHP, static files, Node.js, Python, Java, Go, Dart, a container or a proxy.",
    "Open a website to manage its domains, HTTPS, PHP settings, deployments, staging copy and logs.",
  ]],
  ["/accounts", "Accounts", ["An account is one customer: a Linux user with its own home folder, limits, websites, databases and email."]],
  ["/migrate", "Migrate from cPanel", [
    "Upload a cpmove backup, check what Ike found, then restore it: websites, files, databases, mailboxes with their passwords, forwarders, cron jobs and DNS zones.",
  ]],
  ["/branding", "Branding", ["Your product name, colour and logo replace Ike's for your customers. Add the panel addresses you give them, such as panel.yourcompany.com."]],
  ["/tokens", "API tokens", ["Tokens sign in scripts, the ike command-line tool and billing systems such as WHMCS and Blesta. Send them as: Authorization: Bearer ike_…"]],
  ["/", "Dashboard", ["The dashboard shows your websites, the services this server runs and what changed recently. Press Ctrl K anywhere to search or jump to a page."]],
];

export function HelpDrawer({ open, onClose, route }: { open: boolean; onClose: () => void; route: string }) {
  const { t } = useI18n();
  const entry = help.find(([p]) => (p === "/" ? route === "/" : route === p || route.startsWith(p + "/") || route.startsWith(p + "?"))) ??
    ["", "Help", ["Press Ctrl K to search or jump to any page, and ? for keyboard shortcuts."]];
  useEffect(() => {
    if (!open) return;
    const on = (e: KeyboardEvent) => { if (e.key === "Escape") onClose(); };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="drawer-scrim" onClick={onClose}>
      <aside className="drawer panel" role="dialog" aria-label={t("Help for this page")} onClick={(e) => e.stopPropagation()}>
        <div className="row" style={{ justifyContent: "space-between" }}>
          <h2>{t(entry[1])}</h2>
          <Button variant="ghost" icon size="sm" onClick={onClose} aria-label={t("Close")}><X size={18} /></Button>
        </div>
        <div className="stack" style={{ gap: 12 }}>{entry[2].map((p, i) => <p key={i}>{p}</p>)}</div>
        <div className="callout" style={{ marginTop: 8 }}><Command size={18} />Ctrl K searches everything. Press ? for shortcuts.</div>
        <a className="btn" href="https://github.com/Mastermegamind/ike/tree/main/docs" target="_blank" rel="noreferrer">{t("Read the documentation")}</a>
      </aside>
    </div>
  );
}

export function HelpButton({ onOpen }: { onOpen: () => void }) {
  const { t } = useI18n();
  return <button className="btn ghost icon" type="button" onClick={onOpen} aria-label={t("Help")}><CircleHelp size={18} /></button>;
}

/* ------------------------------------------------------- server switcher */

export function ServerSwitcher({ role, onChange }: { role: Role; onChange: () => void }) {
  const [servers, setServers] = useState<RemoteServer[]>([]);
  const [open, setOpen] = useState(false);
  const [sys, setSys] = useState<{ hostname?: string; os?: string }>({});
  const cur = getServer();
  useEffect(() => {
    api.system().then((s) => setSys({ hostname: s.hostname, os: s.os })).catch(() => {});
    if (role === "admin") api.call<RemoteServer[]>("GET", "/servers").then(setServers).catch(() => {});
  }, [role, cur]);
  const current = servers.find((s) => s.id === cur);
  const name = current ? current.name : sys.hostname || (isDemo ? "vps-lagos-1" : "This server");
  const pick = (id: string) => {
    setServer(id);
    try { sessionStorage.setItem("ike.server", id); } catch { /* ignore */ }
    setOpen(false);
    onChange();
  };
  return (
    <div style={{ position: "relative" }}>
      <button className="server-switch" type="button" onClick={() => role === "admin" && setOpen((o) => !o)} aria-expanded={open}
        title={role === "admin" ? "Switch server" : undefined}>
        <span className="dot" aria-hidden style={current?.status === "offline" ? { background: "var(--bad)" } : undefined} />
        <span>
          <span className="name">{name}</span>
          <span className="sub" style={{ display: "block" }}>{current ? (current.group || current.address) : sys.os || "Ubuntu"}</span>
        </span>
      </button>
      {open && (
        <div className="panel popover" style={{ left: 0, width: 260 }} role="menu">
          <button className="nav-link" role="menuitem" onClick={() => pick("local")} aria-current={cur === "local" ? "true" : undefined}>
            <Server size={16} />{sys.hostname || "This server"}<span className="small muted" style={{ marginLeft: "auto" }}>panel</span>
          </button>
          {servers.map((s) => (
            <button key={s.id} className="nav-link" role="menuitem" onClick={() => pick(s.id)} aria-current={cur === s.id ? "true" : undefined}>
              <Server size={16} />{s.name}
              <span className="small" style={{ marginLeft: "auto", color: s.status === "offline" ? "var(--bad)" : "var(--ok)" }}>{s.status ?? ""}</span>
            </button>
          ))}
          <a className="nav-link" href="#/servers" onClick={() => setOpen(false)}><Plus size={16} />Add a server</a>
        </div>
      )}
    </div>
  );
}

/** Restores the chosen server after a reload. */
export function restoreServer() {
  try {
    const id = sessionStorage.getItem("ike.server");
    if (id) setServer(id);
  } catch { /* ignore */ }
}
