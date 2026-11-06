// Small helpers shared by the 0.5 screens.
import { useState, type ReactNode } from "react";
import { CircleAlert, Download, Upload } from "lucide-react";
import { api } from "../api/client";
import type { Job } from "../api/types";
import { Button, Field, Modal, useAsync, useToast } from "./ui";

/** GET through api.call; null path waits. */
export function useGet<T>(path: string | null, deps: unknown[] = []) {
  return useAsync<T | null>(() => (path ? api.call<T>("GET", path) : Promise.resolve(null)), [path, ...deps]);
}

/** Runs a change with a busy flag and a toast, returning the result. */
export function useAction() {
  const toast = useToast();
  const [busy, setBusy] = useState<string | null>(null);
  const run = async <T,>(key: string, fn: () => Promise<T>, ok?: string): Promise<T | undefined> => {
    setBusy(key);
    try {
      const r = await fn();
      if (ok) toast("ok", ok);
      return r;
    } catch (e) {
      toast("error", (e as Error).message);
      return undefined;
    } finally {
      setBusy(null);
    }
  };
  return { busy, run };
}

export const enc = encodeURIComponent;

export function bytes(n: number | undefined): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v >= 10 || i === 0 ? Math.round(v) : v.toFixed(1)} ${u[i]}`;
}

export function dateTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d.getTime()) || d.getFullYear() < 2000) return "";
  return d.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

export function ErrorNote({ text }: { text: string | null }) {
  if (!text) return null;
  return <div className="callout bad" role="alert"><CircleAlert size={18} />{text}</div>;
}

/** A form in a modal with Save/Cancel and an error line. */
export function FormModal(props: {
  open: boolean; onClose: () => void; title: string; description?: ReactNode; submit: string;
  onSubmit: () => Promise<void>; children: ReactNode; danger?: boolean; disabled?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const close = () => { setError(null); props.onClose(); };
  return (
    <Modal open={props.open} onClose={close} title={props.title} description={props.description}
      footer={<>
        <Button onClick={close}>Cancel</Button>
        <Button variant={props.danger ? "danger" : "primary"} loading={busy} disabled={props.disabled} onClick={async () => {
          setBusy(true); setError(null);
          try { await props.onSubmit(); close(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>{props.submit}</Button>
      </>}>
      <form className="form-grid" onSubmit={(e) => e.preventDefault()}>
        {props.children}
        <ErrorNote text={error} />
      </form>
    </Modal>
  );
}

export function TextField(props: { label: string; value: string; onChange: (v: string) => void; hint?: ReactNode; placeholder?: string; mono?: boolean; type?: string; autoFocus?: boolean }) {
  return (
    <Field label={props.label} hint={props.hint}>
      {(id) => <input id={id} className={`input${props.mono ? " mono" : ""}`} type={props.type ?? "text"} value={props.value} placeholder={props.placeholder}
        autoFocus={props.autoFocus} onChange={(e) => props.onChange(e.target.value)} spellCheck={false} autoComplete="off" />}
    </Field>
  );
}

export function NumberField(props: { label: string; value: number; onChange: (v: number) => void; hint?: ReactNode; min?: number; max?: number }) {
  return (
    <Field label={props.label} hint={props.hint}>
      {(id) => <input id={id} className="input" type="number" inputMode="numeric" min={props.min ?? 0} max={props.max} value={Number.isFinite(props.value) ? props.value : 0}
        onChange={(e) => props.onChange(Number(e.target.value))} />}
    </Field>
  );
}

export function SelectField<T extends string>(props: { label: string; value: T; onChange: (v: T) => void; options: { value: T; label: string }[]; hint?: ReactNode }) {
  return (
    <Field label={props.label} hint={props.hint}>
      {(id) => <select id={id} className="select" value={props.value} onChange={(e) => props.onChange(e.target.value as T)}>
        {props.options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>}
    </Field>
  );
}

/** A download link that works with the real panel; the preview says so instead. */
export function DownloadLink({ path, label, small }: { path: string; label: string; small?: boolean }) {
  const toast = useToast();
  const url = api.fileUrl(path);
  if (!url) {
    return <Button size={small ? "sm" : undefined} onClick={() => toast("ok", "Downloads work on a real server. This preview has no files to send.")}><Download size={15} />{label}</Button>;
  }
  return <a className={`btn${small ? " sm" : ""}`} href={url} download><Download size={15} />{label}</a>;
}

/** A file picker that sends the file as the request body. */
export function UploadButton({ method = "PUT", path, label, onDone, accept }: { method?: "PUT" | "POST"; path: (f: File) => string; label: string; onDone?: (r: unknown) => void; accept?: string }) {
  const toast = useToast();
  const [pct, setPct] = useState<number | null>(null);
  return (
    <label className="btn" style={{ cursor: "pointer" }}>
      <Upload size={15} />{pct !== null ? `${pct}%` : label}
      <input type="file" hidden accept={accept} onChange={async (e) => {
        const f = e.target.files?.[0];
        e.target.value = "";
        if (!f) return;
        setPct(0);
        try { const r = await api.upload(method, path(f), f, setPct); toast("ok", `${f.name} uploaded`); onDone?.(r); }
        catch (err) { toast("error", (err as Error).message); }
        finally { setPct(null); }
      }} />
    </label>
  );
}

/** Shows the credentials or secret that the server returns only once. */
export function OnceModal({ open, onClose, title, children }: { open: boolean; onClose: () => void; title: string; children: ReactNode }) {
  return (
    <Modal open={open} onClose={onClose} title={title} description="Copy this now. It won't be shown again."
      footer={<Button variant="primary" onClick={onClose}>I've saved it</Button>}>
      <div className="form-grid">{children}</div>
    </Modal>
  );
}

export type JobStart = (j: Job) => void;
