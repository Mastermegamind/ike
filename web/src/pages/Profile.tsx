import { useState } from "react";
import { CircleAlert, ShieldCheck, Smartphone } from "lucide-react";
import { api } from "../api/client";
import { Badge, Button, CopyBlock, CopyField, Field } from "../components/ui";
import { PageHead } from "../components/common";

/** Sign-in security for the person who is signed in: two-factor setup and recovery codes. */
export function Profile({ user }: { user: string }) {
  const [step, setStep] = useState<"off" | "scan" | "codes" | "on">("on");
  const [secret, setSecret] = useState<{ secret: string; otpauth_url: string } | null>(null);
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const begin = async () => {
    setBusy(true); setError(null);
    try { setSecret(await api.setup2fa()); setStep("scan"); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
  };

  return (
    <div className="stack">
      <PageHead title="Sign-in security" text={`For ${user}. Two-factor sign-in asks for a code from your phone after your password, so a leaked password alone can't get in.`} />
      <section className="panel" style={{ maxWidth: 720 }}>
        <div className="panel-head">
          <div className="row" style={{ gap: 10 }}><Smartphone size={20} aria-hidden /><div><h2>Authenticator app</h2>
            <p className="small muted">Google Authenticator, Microsoft Authenticator, 2FAS, Aegis, 1Password or any TOTP app.</p></div></div>
          {step === "on" ? <Badge tone="ok"><ShieldCheck size={13} />On</Badge> : <Badge tone="warn">Off</Badge>}
        </div>
        <div className="panel-body stack">
          {step === "on" && (
            <>
              <p>Two-factor sign-in is on. If you get a new phone, set it up again here. Your old codes stop working once the new app is confirmed.</p>
              <div><Button loading={busy} onClick={begin}>Set up a new phone</Button></div>
            </>
          )}
          {step === "scan" && secret && (
            <form className="form-grid" onSubmit={async (e) => {
              e.preventDefault(); setBusy(true); setError(null);
              try { setCodes((await api.enable2fa(code.trim())).recovery_codes); setStep("codes"); setCode(""); }
              catch (err) { setError((err as Error).message); } finally { setBusy(false); }
            }}>
              <ol className="stack" style={{ gap: 14, paddingLeft: 20, margin: 0 }}>
                <li>In your authenticator app, add an account and choose <strong>Enter a setup key</strong>. Name it Ike and paste this key:
                  <div style={{ marginTop: 8 }}><CopyField value={secret.secret.replace(/(.{4})/g, "$1 ").trim()} label="Setup key" /></div>
                  <p className="small muted" style={{ marginTop: 6 }}>On this phone? <a href={secret.otpauth_url}>Open in your authenticator app</a>.</p>
                </li>
                <li>Type the 6-digit code it shows.</li>
              </ol>
              <Field label="Code" error={error}>
                {(id) => <input id={id} className="input mono" style={{ maxWidth: 160, letterSpacing: "0.2em", fontSize: "1.1rem" }} inputMode="numeric" autoComplete="one-time-code"
                  maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} autoFocus />}
              </Field>
              <div className="row"><Button variant="primary" type="submit" loading={busy} disabled={code.length !== 6}>Turn on two-factor</Button>
                <Button type="button" variant="ghost" onClick={() => setStep("on")}>Cancel</Button></div>
            </form>
          )}
          {step === "codes" && (
            <>
              <div className="callout warn"><CircleAlert size={18} /><div><strong>Save these recovery codes now.</strong> Each one signs you in once if you lose your phone. Ike only stores a scrambled copy and can't show them again.</div></div>
              <CopyBlock value={codes.join("\n")} />
              <div><Button variant="primary" onClick={() => setStep("on")}>I've saved them</Button></div>
            </>
          )}
          {error && step !== "scan" ? <div className="callout bad"><CircleAlert size={18} />{error}</div> : null}
        </div>
      </section>
    </div>
  );
}
