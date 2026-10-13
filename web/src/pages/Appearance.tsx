import { Laptop, Moon, Sun } from "lucide-react";
import { themes, type Theme } from "../theme/themes";
import { useAppearance, type Mode, type TemplateId } from "../theme/appearance";
import { PageHead } from "../components/common";

function ThemePreview({ t, mode }: { t: Theme; mode: "light" | "dark" }) {
  const p = t[mode];
  const r = Math.min(t.radius, 10);
  return (
    <div className="preview" style={{ background: p.bg }} aria-hidden>
      <div className="pv-side" style={{ background: p.surface, borderRight: `1px solid ${p.line}` }}>
        <span className="pv-bar" style={{ background: p.accent, width: "70%", borderRadius: r }} />
        <span className="pv-bar" style={{ background: p.line, width: "85%" }} />
        <span className="pv-bar" style={{ background: p.line, width: "60%" }} />
        <span className="pv-bar" style={{ background: p.line, width: "75%" }} />
      </div>
      <div className="pv-main">
        <span style={{ fontFamily: t.fontDisplay, color: p.ink, fontWeight: 700, fontSize: 15 }}>2.4 GB</span>
        <span className="pv-bar" style={{ background: p.surface2, width: "100%" }}><span style={{ display: "block", height: "100%", width: "62%", background: p.accent, borderRadius: 99 }} /></span>
        <span style={{ display: "flex", gap: 5 }}>
          <span style={{ height: 16, width: 46, borderRadius: r / 2, background: p.accent }} />
          <span style={{ height: 16, width: 34, borderRadius: r / 2, border: `1px solid ${p.line}`, background: p.surface }} />
        </span>
        <span style={{ display: "flex", gap: 4 }}>{[p.ok, p.warn, p.bad].map((c) => <span key={c} style={{ width: 8, height: 8, borderRadius: 4, background: c }} />)}</span>
      </div>
    </div>
  );
}

const templates: { id: TemplateId; name: string; text: string; wire: JSX.Element }[] = [
  {
    id: "command", name: "Command", text: "Sidebar with every section. The default.",
    wire: <div className="wire" style={{ gridTemplateColumns: "28% 1fr" }}><i className="a" style={{ gridRow: "span 3" }} /><i style={{ height: 12 }} /><i /><i /></div>,
  },
  {
    id: "horizon", name: "Horizon", text: "Top navigation and centered content, like a classic control panel.",
    wire: <div className="wire" style={{ gridTemplateRows: "14px 1fr" }}><i className="a" /><i style={{ margin: "0 12%" }} /></div>,
  },
  {
    id: "rail", name: "Rail", text: "Icon rail and dense tables, for admins with many accounts.",
    wire: <div className="wire" style={{ gridTemplateColumns: "12% 1fr", gridTemplateRows: "10px 1fr 1fr" }}><i className="a" style={{ gridRow: "span 3" }} /><i /><i /><i /></div>,
  },
];

export function Appearance() {
  const a = useAppearance();
  const modes: { id: Mode; label: string; text: string; icon: JSX.Element }[] = [
    { id: "system", label: "Match my device", text: "Switches with your operating system.", icon: <Laptop size={18} /> },
    { id: "light", label: "Light", text: "Always light.", icon: <Sun size={18} /> },
    { id: "dark", label: "Dark", text: "Always dark.", icon: <Moon size={18} /> },
  ];
  return (
    <div className="stack" style={{ gap: 32 }}>
      <PageHead title="Appearance" text="Saved for your login on this browser. Admins can set a default theme and their own branding for every user." />

      <section className="stack" aria-labelledby="mode-h">
        <h2 id="mode-h">Mode</h2>
        <div className="mode-options" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
          {modes.map((m) => (
            <button key={m.id} type="button" className="mode-option" aria-pressed={a.mode === m.id} onClick={() => a.set({ mode: m.id })}>
              {m.icon}<strong>{m.label}</strong><span className="small muted">{m.text}</span>
            </button>
          ))}
        </div>
      </section>

      <section className="stack" aria-labelledby="theme-h">
        <h2 id="theme-h">Theme</h2>
        <div className="theme-grid">
          {themes.map((t) => (
            <button key={t.id} type="button" className="theme-card" aria-pressed={a.theme === t.id} onClick={() => a.set({ theme: t.id })}>
              <ThemePreview t={t} mode={a.resolvedMode} />
              <div className="meta"><strong>{t.name}</strong><span className="small muted">{t.description}</span></div>
            </button>
          ))}
        </div>
      </section>

      <section className="stack" aria-labelledby="tpl-h">
        <h2 id="tpl-h">Layout</h2>
        <div className="template-grid">
          {templates.map((t) => (
            <button key={t.id} type="button" className="template-card" aria-pressed={a.template === t.id} onClick={() => a.set({ template: t.id })}>
              {t.wire}
              <div><strong>{t.name}</strong><p className="small muted">{t.text}</p></div>
            </button>
          ))}
        </div>
        <p className="small muted">On phones every layout becomes a top bar with tabs at the bottom.</p>
      </section>
    </div>
  );
}
