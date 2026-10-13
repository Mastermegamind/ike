import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { getTheme, MONO_FONT, type Palette } from "./themes";

export type Mode = "light" | "dark" | "system";
export type TemplateId = "command" | "horizon" | "rail";

export type DataSaver = "auto" | "on" | "off";

export interface Appearance {
  theme: string;
  mode: Mode;
  template: TemplateId;
  dataSaver: DataSaver;
}

const KEY = "ike.appearance";
const DEFAULTS: Appearance = { theme: "uli", mode: "system", template: "command", dataSaver: "auto" };

function load(): Appearance {
  try {
    return { ...DEFAULTS, ...JSON.parse(localStorage.getItem(KEY) || "{}") };
  } catch {
    return DEFAULTS;
  }
}

function save(a: Appearance) {
  try {
    localStorage.setItem(KEY, JSON.stringify(a));
  } catch {
    /* private mode: the choice lasts for this visit only */
  }
}

const cssVar: Record<keyof Palette, string> = {
  bg: "--bg", surface: "--surface", surface2: "--surface-2", line: "--line", ink: "--ink", ink2: "--ink-2",
  accent: "--accent", accentInk: "--accent-ink", accentSoft: "--accent-soft", ok: "--ok", warn: "--warn", bad: "--bad",
};

function systemDark() {
  return typeof matchMedia !== "undefined" && matchMedia("(prefers-color-scheme: dark)").matches;
}

/** True when the browser or OS asks pages to use less data (Chrome's Lite mode, Android Data Saver). */
export function browserSavesData(): boolean {
  const c = (navigator as Navigator & { connection?: { saveData?: boolean; effectiveType?: string } }).connection;
  return !!c && (c.saveData === true || c.effectiveType === "2g" || c.effectiveType === "slow-2g");
}

const FONTS = "https://fonts.googleapis.com/css2?family=Bricolage+Grotesque:opsz,wght@12..96,500;12..96,700&family=Geist:wght@400;500;600&family=Geist+Mono:wght@400;500&display=swap";

/** Web fonts are about 150 KB, so they load only when data saver is off. */
function loadFonts() {
  if (document.getElementById("ike-fonts")) return;
  const l = document.createElement("link");
  l.id = "ike-fonts";
  l.rel = "stylesheet";
  l.href = FONTS;
  document.head.appendChild(l);
}

interface Ctx extends Appearance {
  resolvedMode: "light" | "dark";
  /** Data saver is in effect: no web fonts, no motion, no background refresh. */
  saving: boolean;
  set: (patch: Partial<Appearance>) => void;
}

const AppearanceContext = createContext<Ctx | null>(null);

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const [a, setA] = useState<Appearance>(load);
  const [sysDark, setSysDark] = useState(systemDark);

  useEffect(() => {
    const mq = matchMedia("(prefers-color-scheme: dark)");
    const on = () => setSysDark(mq.matches);
    mq.addEventListener("change", on);
    return () => mq.removeEventListener("change", on);
  }, []);

  const resolvedMode: "light" | "dark" = a.mode === "system" ? (sysDark ? "dark" : "light") : a.mode;
  const saving = a.dataSaver === "on" || (a.dataSaver === "auto" && browserSavesData());

  useEffect(() => {
    document.documentElement.dataset.saver = saving ? "on" : "off";
    if (!saving) loadFonts();
  }, [saving]);

  useEffect(() => {
    const t = getTheme(a.theme);
    const p = t[resolvedMode];
    const root = document.documentElement;
    (Object.keys(cssVar) as (keyof Palette)[]).forEach((k) => root.style.setProperty(cssVar[k], p[k]));
    root.style.setProperty("--radius", `${t.radius}px`);
    root.style.setProperty("--font-ui", t.fontUi);
    root.style.setProperty("--font-display", t.fontDisplay);
    root.style.setProperty("--font-mono", MONO_FONT);
    root.dataset.mode = resolvedMode;
    root.dataset.theme = t.id;
    root.dataset.template = a.template;
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", p.surface);
  }, [a.theme, a.template, resolvedMode]);

  const value = useMemo<Ctx>(
    () => ({
      ...a,
      resolvedMode,
      saving,
      set: (patch) =>
        setA((prev) => {
          const next = { ...prev, ...patch };
          save(next);
          return next;
        }),
    }),
    [a, resolvedMode, saving],
  );

  return <AppearanceContext.Provider value={value}>{children}</AppearanceContext.Provider>;
}

export function useAppearance() {
  const c = useContext(AppearanceContext);
  if (!c) throw new Error("useAppearance must be used inside AppearanceProvider");
  return c;
}
