import {
  createContext, useCallback, useContext, useEffect, useId, useRef, useState,
  type ButtonHTMLAttributes, type ReactNode,
} from "react";
import { Check, CircleAlert, Copy, Loader2, X } from "lucide-react";

/* ---------------------------------------------------------------- Button */

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "secondary" | "ghost" | "danger";
  size?: "md" | "sm";
  icon?: boolean;
  loading?: boolean;
};

export function Button({ variant = "secondary", size = "md", icon, loading, className = "", children, disabled, ...rest }: BtnProps) {
  const cls = ["btn", variant !== "secondary" && variant, size === "sm" && "sm", icon && "icon", className].filter(Boolean).join(" ");
  return (
    <button className={cls} disabled={disabled || loading} {...rest}>
      {loading ? <Loader2 size={16} className="spin" aria-hidden /> : null}
      {children}
    </button>
  );
}

/* ----------------------------------------------------------------- Badge */

export function Badge({ tone, children }: { tone?: "ok" | "warn" | "bad" | "accent"; children: ReactNode }) {
  return <span className={`badge ${tone ?? ""}`}>{children}</span>;
}

/* ----------------------------------------------------------------- Field */

export function Field(props: { label: string; hint?: ReactNode; error?: string | null; children: (id: string) => ReactNode }) {
  const id = useId();
  return (
    <div className="field">
      <label htmlFor={id}>{props.label}</label>
      {props.children(id)}
      {props.error ? <div className="error" role="alert">{props.error}</div> : props.hint ? <div className="hint">{props.hint}</div> : null}
    </div>
  );
}

/* ------------------------------------------------------------- Segmented */

export function Segmented<T extends string>(props: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string; icon?: ReactNode }[];
  label: string;
}) {
  return (
    <div className="segmented" role="group" aria-label={props.label}>
      {props.options.map((o) => (
        <button key={o.value} type="button" aria-pressed={props.value === o.value} onClick={() => props.onChange(o.value)}>
          {o.icon}
          {o.label}
        </button>
      ))}
    </div>
  );
}

/* ------------------------------------------------------------------ Tabs */

export function Tabs<T extends string>(props: { value: T; onChange: (v: T) => void; tabs: { value: T; label: string; icon?: ReactNode }[] }) {
  return (
    <div className="tabs" role="tablist">
      {props.tabs.map((t) => (
        <button key={t.value} role="tab" aria-selected={props.value === t.value} onClick={() => props.onChange(t.value)}>
          {t.icon}
          {t.label}
        </button>
      ))}
    </div>
  );
}

/* ----------------------------------------------------------------- Modal */

export function Modal(props: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (props.open && !d.open) d.showModal();
    if (!props.open && d.open) d.close();
  }, [props.open]);
  return (
    <dialog
      ref={ref}
      className="modal"
      onClose={props.onClose}
      onClick={(e) => { if (e.target === ref.current) props.onClose(); }}
      aria-labelledby="modal-title"
    >
      {props.open && (
        <>
          <div className="modal-head">
            <div>
              <h2 id="modal-title">{props.title}</h2>
              {props.description ? <p className="muted small">{props.description}</p> : null}
            </div>
            <Button variant="ghost" icon size="sm" onClick={props.onClose} aria-label="Close"><X size={18} /></Button>
          </div>
          <div className="modal-body">{props.children}</div>
          {props.footer ? <div className="modal-foot">{props.footer}</div> : null}
        </>
      )}
    </dialog>
  );
}

/* ---------------------------------------------------------------- Toasts */

interface Toast { id: number; tone: "ok" | "error"; text: string }
const ToastCtx = createContext<(tone: Toast["tone"], text: string) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [list, setList] = useState<Toast[]>([]);
  const push = useCallback((tone: Toast["tone"], text: string) => {
    const id = Date.now() + Math.random();
    setList((l) => [...l, { id, tone, text }]);
    setTimeout(() => setList((l) => l.filter((t) => t.id !== id)), tone === "error" ? 6000 : 3200);
  }, []);
  return (
    <ToastCtx.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {list.map((t) => (
          <div key={t.id} className={`toast ${t.tone}`}>
            {t.tone === "ok" ? <Check size={18} /> : <CircleAlert size={18} />}
            {t.text}
          </div>
        ))}
      </div>
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);

/* ------------------------------------------------------------- CopyField */

export function CopyField({ value, label }: { value: string; label?: string }) {
  const [done, setDone] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setDone(true);
      setTimeout(() => setDone(false), 1500);
    } catch {
      /* clipboard blocked: the text is still selectable */
    }
  };
  return (
    <div className="copy">
      <code aria-label={label}>{value}</code>
      <Button variant="ghost" size="sm" icon onClick={copy} aria-label={done ? "Copied" : `Copy ${label ?? ""}`.trim()}>
        {done ? <Check size={16} /> : <Copy size={16} />}
      </Button>
    </div>
  );
}

export function CopyBlock({ value }: { value: string }) {
  const [done, setDone] = useState(false);
  return (
    <div style={{ position: "relative" }}>
      <pre className="code">{value}</pre>
      <Button
        size="sm" variant="secondary" style={{ position: "absolute", top: 8, right: 8 }}
        onClick={async () => {
          try { await navigator.clipboard.writeText(value); setDone(true); setTimeout(() => setDone(false), 1500); } catch { /* ignore */ }
        }}
      >
        {done ? <Check size={15} /> : <Copy size={15} />}
        {done ? "Copied" : "Copy"}
      </Button>
    </div>
  );
}

/* ----------------------------------------------------------------- Meter */

export function Meter({ value, label }: { value: number; label: string }) {
  const pct = Math.max(0, Math.min(100, value));
  const tone = pct >= 90 ? "bad" : pct >= 75 ? "warn" : "";
  return (
    <div className={`meter ${tone}`} role="meter" aria-label={label} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(pct)}>
      <span style={{ width: `${pct}%` }} />
    </div>
  );
}

/* ----------------------------------------------------------------- Empty */

export function Empty(props: { icon: ReactNode; title: string; text: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      <div className="icon-wrap">{props.icon}</div>
      <h3>{props.title}</h3>
      <p className="muted">{props.text}</p>
      {props.action}
    </div>
  );
}

export function Skeleton({ h = 16, w = "100%" }: { h?: number; w?: number | string }) {
  return <div className="skeleton" style={{ height: h, width: w }} />;
}

/* -------------------------------------------------------------- useAsync */

export function useAsync<T>(fn: () => Promise<T>, deps: unknown[] = []) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const reload = useCallback(() => {
    setLoading(true);
    fn()
      .then((d) => { setData(d); setError(null); })
      .catch((e: Error) => setError(e.message))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  useEffect(reload, [reload]);
  return { data, error, loading, reload, setData };
}

/* ------------------------------------------------------------- formatting */

export function timeAgo(iso: string): string {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  const d = Math.floor(s / 86400);
  return d === 1 ? "yesterday" : `${d} days ago`;
}

export function formatMB(mb: number): string {
  return mb >= 1024 ? `${(mb / 1024).toFixed(mb >= 10240 ? 0 : 1)} GB` : `${mb} MB`;
}
