import {
  Box, Cloud, Puzzle, Ship,
  ArrowUpCircle, BarChart3, BellRing, CreditCard, FolderInput, KeyRound, ShieldHalf, UserCog,
  Activity, AppWindow, Bot, Boxes, CalendarClock, Database, Gauge, Globe, HardDriveDownload, LockKeyhole, Mail,
  Network, Palette, Server, CalendarX2, RadioTower, Video, ShieldCheck, SquareTerminal, Users, FolderOpen, ServerCog, Bug, Inbox, Settings2, Brush, Bell, KeySquare,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "../api/types";

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  roles: Role[];
  soon?: boolean;
  /** For developers: the area a page belongs to; hidden without it. */
  area?: string;
}

export interface NavGroup {
  title?: string;
  items: NavItem[];
}

const all: Role[] = ["admin", "reseller", "user", "developer"];

export const navGroups: NavGroup[] = [
  { items: [{ to: "/", label: "Dashboard", icon: Gauge, roles: all }] },
  {
    title: "Hosting",
    items: [
      { to: "/accounts", label: "Accounts", icon: Users, roles: ["admin", "reseller"] },
      { to: "/sites", label: "Websites", icon: Globe, roles: all, area: "sites" },
      { to: "/email", label: "Email", icon: Mail, roles: all, area: "mail" },
      { to: "/dns", label: "DNS zones", icon: Network, roles: all, area: "dns" },
      { to: "/databases", label: "Databases", icon: Database, roles: all, area: "databases" },
      { to: "/files", label: "Files", icon: FolderOpen, roles: all, area: "files" },
      { to: "/backups", label: "Backups", icon: HardDriveDownload, roles: all, area: "backups" },
      { to: "/cron", label: "Cron jobs", icon: CalendarClock, roles: all, area: "sites" },
      { to: "/apps", label: "App installer", icon: AppWindow, roles: ["admin", "reseller", "user"] },
      { to: "/containers", label: "Containers", icon: Box, roles: all, area: "containers" },
      { to: "/ssl", label: "SSL certificates", icon: LockKeyhole, roles: all, area: "sites" },
      { to: "/expiry", label: "Expiry dates", icon: CalendarX2, roles: ["admin", "reseller", "user"] },
      { to: "/realtime", label: "Video & calls", icon: Video, roles: ["reseller", "user", "developer"], area: "sites" },
      { to: "/terminal", label: "Terminal", icon: SquareTerminal, roles: all, area: "files" },
    ],
  },
  {
    title: "Server",
    items: [
      { to: "/servers", label: "Infrastructure", icon: ServerCog, roles: ["admin"] },
      { to: "/firewall", label: "Firewall", icon: ShieldCheck, roles: ["admin"] },
      { to: "/malware", label: "Malware scanner", icon: Bug, roles: all, area: "security" },
      { to: "/security", label: "Security scan", icon: ShieldHalf, roles: ["admin"] },
      { to: "/software", label: "Server software", icon: Server, roles: ["admin"] },
      { to: "/mail-settings", label: "Mail settings", icon: Inbox, roles: ["admin"] },
      { to: "/dns-settings", label: "DNS settings", icon: Settings2, roles: ["admin"] },
      { to: "/php-extensions", label: "PHP extensions", icon: Puzzle, roles: ["admin"] },
      { to: "/docker", label: "Docker", icon: Ship, roles: ["admin"] },
      { to: "/kubernetes", label: "Kubernetes", icon: Cloud, roles: ["admin"] },
      { to: "/realtime", label: "Real-time media", icon: RadioTower, roles: ["admin"] },
      { to: "/usage", label: "Resource usage", icon: BarChart3, roles: ["admin", "reseller"] },
      { to: "/updates", label: "Updates", icon: ArrowUpCircle, roles: ["admin"] },
      { to: "/migrate", label: "Migrate from cPanel", icon: FolderInput, roles: ["admin"] },
      { to: "/activity", label: "Activity", icon: Activity, roles: ["admin", "reseller"] },
      { to: "/ai", label: "AI agents", icon: Bot, roles: ["admin"] },
    ],
  },
  {
    title: "Business",
    items: [
      { to: "/packages", label: "Hosting packages", icon: Boxes, roles: ["admin", "reseller"] },
      { to: "/billing", label: "Billing", icon: CreditCard, roles: ["admin", "reseller", "user"] },
      { to: "/people", label: "People", icon: UserCog, roles: ["admin", "reseller"] },
      { to: "/branding", label: "Branding", icon: Brush, roles: ["admin", "reseller"] },
      { to: "/alerts", label: "Alerts", icon: BellRing, roles: ["admin"] },
    ],
  },
  { title: "Settings", items: [
    { to: "/team", label: "Developers", icon: UserCog, roles: ["user"] },
    { to: "/notifications", label: "Notifications", icon: Bell, roles: all },
    { to: "/appearance", label: "Appearance", icon: Palette, roles: all },
    { to: "/profile", label: "Sign-in security", icon: KeyRound, roles: all },
    { to: "/tokens", label: "API tokens", icon: KeySquare, roles: all },
  ] },
];

let devAreas: Record<string, string> = {};
let signedInAs: Role = "user";
/** setSignedInRole records the real role (pages see developers as account owners). */
export function setSignedInRole(r: Role) { signedInAs = r; }
/** isDeveloper is true when a developer is signed in. */
export function isDeveloper() { return signedInAs === "developer"; }
/** setDeveloperAreas records what a signed-in developer may reach (from GET /session). */
export function setDeveloperAreas(a: Record<string, string> | undefined) { devAreas = a ?? {}; }

export function visibleGroups(role: Role): NavGroup[] {
  return navGroups
    .map((g) => ({ ...g, items: g.items.filter((i) => i.roles.includes(role) && (role !== "developer" || !i.area || !!devAreas[i.area])) }))
    .filter((g) => g.items.length > 0);
}

export function isActive(route: string, to: string) {
  if (to === "/") return route === "/";
  return route === to || route.startsWith(to + "/");
}

export const roleLabel: Record<Role, string> = { admin: "Server admin", reseller: "Reseller", user: "Account owner", developer: "Developer" };
