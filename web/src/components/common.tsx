import { useState, type ReactNode } from "react";
import { ArrowLeft } from "lucide-react";
import { Badge, Button, Field, Modal } from "./ui";
import { href } from "../router";
import { runtimeName } from "../api/demo";
import type { Runtime, SiteStatus, WebServer } from "../api/types";

export function PageHead(props: { title: string; text?: ReactNode; actions?: ReactNode; back?: { to: string; label: string } }) {
  return (
    <div className="page-head">
      <div>
        {props.back ? (
          <a className="crumb" href={href(props.back.to)}><ArrowLeft size={15} aria-hidden />{props.back.label}</a>
        ) : null}
        <h1>{props.title}</h1>
        {props.text ? <p className="muted">{props.text}</p> : null}
      </div>
      {props.actions ? <div className="row">{props.actions}</div> : null}
    </div>
  );
}

export function RuntimeBadge({ runtime, version }: { runtime: Runtime; version?: string }) {
  return <Badge>{runtimeName(runtime)}{version ? ` ${version}` : ""}</Badge>;
}

export function StatusBadge({ status }: { status: SiteStatus }) {
  if (status === "active") return <Badge tone="ok">Active</Badge>;
  if (status === "maintenance") return <Badge tone="warn">Maintenance</Badge>;
  return <Badge tone="bad">Suspended</Badge>;
}

export const webServerLabel: Record<WebServer, string> = {
  nginx: "Nginx",
  apache: "Apache",
  "nginx+apache": "Nginx + Apache",
};

/** Destructive confirmation that asks the person to type the name. */
export function ConfirmDelete(props: {
  open: boolean;
  onClose: () => void;
  title: string;
  name: string;
  description: ReactNode;
  confirmLabel: string;
  onConfirm: () => Promise<void>;
  extra?: ReactNode;
}) {
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const close = () => { setTyped(""); setError(null); props.onClose(); };
  return (
    <Modal
      open={props.open}
      onClose={close}
      title={props.title}
      description={props.description}
      footer={
        <>
          <Button onClick={close}>Cancel</Button>
          <Button
            variant="danger"
            loading={busy}
            disabled={typed !== props.name}
            onClick={async () => {
              setBusy(true);
              setError(null);
              try { await props.onConfirm(); close(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
            }}
          >
            {props.confirmLabel}
          </Button>
        </>
      }
    >
      <div className="form-grid">
        {props.extra}
        <Field label={`Type ${props.name} to confirm`} error={error}>
          {(id) => <input id={id} className="input mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />}
        </Field>
      </div>
    </Modal>
  );
}

export function Toggle(props: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <label style={{ display: "inline-flex", alignItems: "center", gap: 10, cursor: "pointer" }}>
      <span
        role="switch"
        aria-checked={props.checked}
        tabIndex={0}
        onKeyDown={(e) => { if (e.key === " " || e.key === "Enter") { e.preventDefault(); props.onChange(!props.checked); } }}
        onClick={() => props.onChange(!props.checked)}
        style={{
          width: 38, height: 22, borderRadius: 99, padding: 2, display: "inline-flex",
          background: props.checked ? "var(--accent)" : "var(--surface-2)", border: "1px solid var(--line)",
          transition: "background .15s",
        }}
      >
        <span style={{
          width: 16, height: 16, borderRadius: "50%", background: props.checked ? "var(--accent-ink)" : "var(--ink-2)",
          transform: props.checked ? "translateX(16px)" : "none", transition: "transform .15s",
        }} />
      </span>
      <span>{props.label}</span>
    </label>
  );
}
