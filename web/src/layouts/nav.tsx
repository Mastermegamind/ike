import {
  Box, Cloud, Puzzle, Ship,
  ArrowUpCircle, BarChart3, BellRing, CreditCard, FolderInput, KeyRound, ShieldHalf, UserCog,
  Activity, AppWindow, Bot, Boxes, CalendarClock, Database, Gauge, Globe, HardDriveDownload, LockKeyhole, Mail,
  Network, Palette, Server, ShieldCheck, SquareTerminal, Users, FolderOpen, ServerCog, Bug, Inbox, Settings2, Brush, Bell, KeySquare,
  type LucideIcon,
} from "lucide-react";
import type { Role } from "../api/types";

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  roles: Role[];
  soon?: boolean;
}

export interface NavGroup {
  title?: string;
  items: NavItem[];
}

const all: Role[] = ["admin", "reseller", "user"];

export const navGroups: NavGroup[] = [
  { items: [{ to: "/", label: "Dashboard", icon: Gauge, roles: all }] },
  {
    title: "Hosting",
    items: [
      { to: "/accounts", label: "Accounts", icon: Users, roles: ["admin", "reseller"] },
      { to: "/sites", label: "Websites", icon: Globe, roles: all },
      { to: "/email", label: "Email", icon: Mail, roles: all },
      { to: "/dns", label: "DNS zones", icon: Network, roles: all },
      { to: "/databases", label: "Databases", icon: Database, roles: all },
      { to: "/files", label: "Files", icon: FolderOpen, roles: all },
      { to: "/backups", label: "Backups", icon: HardDriveDownload, roles: all },
      { to: "/cron", label: "Cron jobs", icon: CalendarClock, roles: all },
      { to: "/apps", label: "App installer", icon: AppWindow, roles: all },
      { to: "/containers", label: "Containers", icon: Box, roles: all },
      { to: "/ssl", label: "SSL certificates", icon: LockKeyhole, roles: all },
      { to: "/terminal", label: "Terminal", icon: SquareTerminal, roles: all },
    ],
  },
  {
    title: "Server",
    items: [
      { to: "/servers", label: "Infrastructure", icon: ServerCog, roles: ["admin"] },
      { to: "/firewall", label: "Firewall", icon: ShieldCheck, roles: ["admin"] },
      { to: "/malware", label: "Malware scanner", icon: Bug, roles: all },
      { to: "/security", label: "Security scan", icon: ShieldHalf, roles: ["admin"] },
      { to: "/software", label: "Server software", icon: Server, roles: ["admin"] },
      { to: "/mail-settings", label: "Mail settings", icon: Inbox, roles: ["admin"] },
      { to: "/dns-settings", label: "DNS settings", icon: Settings2, roles: ["admin"] },
      { to: "/php-extensions", label: "PHP extensions", icon: Puzzle, roles: ["admin"] },
      { to: "/docker", label: "Docker", icon: Ship, roles: ["admin"] },
      { to: "/kubernetes", label: "Kubernetes", icon: Cloud, roles: ["admin"] },
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
      { to: "/billing", label: "Billing", icon: CreditCard, roles: all },
      { to: "/people", label: "People", icon: UserCog, roles: ["admin", "reseller"] },
      { to: "/branding", label: "Branding", icon: Brush, roles: ["admin", "reseller"] },
      { to: "/alerts", label: "Alerts", icon: BellRing, roles: ["admin"] },
    ],
  },
  { title: "Settings", items: [
    { to: "/notifications", label: "Notifications", icon: Bell, roles: all },
    { to: "/appearance", label: "Appearance", icon: Palette, roles: all },
    { to: "/profile", label: "Sign-in security", icon: KeyRound, roles: all },
    { to: "/tokens", label: "API tokens", icon: KeySquare, roles: all },
  ] },
];

export function visibleGroups(role: Role): NavGroup[] {
  return navGroups
    .map((g) => ({ ...g, items: g.items.filter((i) => i.roles.includes(role)) }))
    .filter((g) => g.items.length > 0);
}

export function isActive(route: string, to: string) {
  if (to === "/") return route === "/";
  return route === to || route.startsWith(to + "/");
}

export const roleLabel: Record<Role, string> = { admin: "Server admin", reseller: "Reseller", user: "Account owner" };
