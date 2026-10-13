/**
 * Themes are plain data: a light and a dark palette plus shape and type.
 * To add a theme, append an object to `themes` (or load one from JSON with
 * `registerTheme`). Every value becomes a CSS custom property on <html>.
 */

export interface Palette {
  bg: string;        // page background
  surface: string;   // panels, tables
  surface2: string;  // hover, inputs, secondary panels
  line: string;      // borders and dividers
  ink: string;       // main text
  ink2: string;      // secondary text
  accent: string;    // primary actions, focus, selection
  accentInk: string; // text on accent
  accentSoft: string;// subtle accent backgrounds
  ok: string;
  warn: string;
  bad: string;
}

export interface Theme {
  id: string;
  name: string;
  description: string;
  light: Palette;
  dark: Palette;
  radius: number;        // base corner radius in px
  fontUi: string;
  fontDisplay: string;
}

const UI = `"Geist", system-ui, -apple-system, "Segoe UI", sans-serif`;
const DISPLAY = `"Bricolage Grotesque", "Geist", system-ui, sans-serif`;
const MONO = `"Geist Mono", ui-monospace, "SFMono-Regular", Menlo, monospace`;

export const themes: Theme[] = [
  {
    id: "uli",
    name: "Uli",
    description: "Indigo ink and camwood red, after Igbo uli art. The default.",
    radius: 10, fontUi: UI, fontDisplay: DISPLAY,
    light: {
      bg: "#f3f4f9", surface: "#ffffff", surface2: "#eceef6", line: "#dcdfeb",
      ink: "#1e2142", ink2: "#5d6182", accent: "#a8323e", accentInk: "#ffffff", accentSoft: "#f8e7e9",
      ok: "#1f8a5b", warn: "#b7791f", bad: "#c2283a",
    },
    dark: {
      bg: "#121428", surface: "#1a1d37", surface2: "#23274a", line: "#2e3358",
      ink: "#e9eaf6", ink2: "#a2a6c8", accent: "#f0808c", accentInk: "#2a0d12", accentSoft: "#3a2036",
      ok: "#4cc38a", warn: "#e5b25d", bad: "#ff7a86",
    },
  },
  {
    id: "naija",
    name: "Naija",
    description: "Flag green on clean white. Bright and confident.",
    radius: 12, fontUi: UI, fontDisplay: DISPLAY,
    light: {
      bg: "#f2f5f3", surface: "#ffffff", surface2: "#e8eeea", line: "#d6e0da",
      ink: "#14261c", ink2: "#56685d", accent: "#008751", accentInk: "#ffffff", accentSoft: "#dff2e8",
      ok: "#008751", warn: "#b7791f", bad: "#c62f3c",
    },
    dark: {
      bg: "#0d1a13", surface: "#13241b", surface2: "#1b3125", line: "#25402f",
      ink: "#e3f1e8", ink2: "#97b3a2", accent: "#3fcf8e", accentInk: "#062615", accentSoft: "#173a29",
      ok: "#3fcf8e", warn: "#e5b25d", bad: "#ff7f88",
    },
  },
  {
    id: "lagoon",
    name: "Lagoon",
    description: "Cool blues and teal, calm for long sessions.",
    radius: 14, fontUi: UI, fontDisplay: DISPLAY,
    light: {
      bg: "#eef4f7", surface: "#ffffff", surface2: "#e3edf2", line: "#cfdee6",
      ink: "#0f2633", ink2: "#4f6b7a", accent: "#0b7a8f", accentInk: "#ffffff", accentSoft: "#d8eef2",
      ok: "#1f8a5b", warn: "#b7791f", bad: "#c2283a",
    },
    dark: {
      bg: "#0a1820", surface: "#10222c", surface2: "#173039", line: "#21404c",
      ink: "#e1eff4", ink2: "#8fb0bd", accent: "#4fd1e0", accentInk: "#032229", accentSoft: "#123a44",
      ok: "#4cc38a", warn: "#e5b25d", bad: "#ff7a86",
    },
  },
  {
    id: "harmattan",
    name: "Harmattan",
    description: "Dusty sand and ochre, soft on the eyes.",
    radius: 8, fontUi: UI, fontDisplay: DISPLAY,
    light: {
      bg: "#efece6", surface: "#faf8f4", surface2: "#e6e1d8", line: "#d8d1c4",
      ink: "#2d261c", ink2: "#6e6455", accent: "#9a6214", accentInk: "#ffffff", accentSoft: "#f1e3cb",
      ok: "#3d7d4a", warn: "#9a6214", bad: "#b23a2e",
    },
    dark: {
      bg: "#1c1813", surface: "#252019", surface2: "#302920", line: "#3e352a",
      ink: "#f1e9dc", ink2: "#b4a690", accent: "#e3a64a", accentInk: "#2b1a03", accentSoft: "#3d2e18",
      ok: "#7cc48b", warn: "#e3a64a", bad: "#f08a7c",
    },
  },
  {
    id: "terminal",
    name: "Terminal",
    description: "Monospace, sharp corners and amber. For people who live in SSH.",
    radius: 2, fontUi: MONO, fontDisplay: MONO,
    light: {
      bg: "#f4f4f2", surface: "#ffffff", surface2: "#ebebe8", line: "#cfcfca",
      ink: "#141414", ink2: "#5c5c58", accent: "#141414", accentInk: "#f4f4f2", accentSoft: "#e4e4e0",
      ok: "#1d7a3e", warn: "#946200", bad: "#b3261e",
    },
    dark: {
      bg: "#000000", surface: "#0c0c0c", surface2: "#171717", line: "#2a2a2a",
      ink: "#e6e6e6", ink2: "#8f8f8f", accent: "#ffb000", accentInk: "#000000", accentSoft: "#2b2100",
      ok: "#33d17a", warn: "#ffb000", bad: "#ff5f56",
    },
  },
];

export function registerTheme(t: Theme) {
  const i = themes.findIndex((x) => x.id === t.id);
  if (i >= 0) themes[i] = t;
  else themes.push(t);
}

export function getTheme(id: string): Theme {
  return themes.find((t) => t.id === id) || themes[0];
}

export const MONO_FONT = MONO;
