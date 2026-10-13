import { useState } from "react";
import { api, isDemo, TwoFactorRequired } from "../api/client";
import type { Role } from "../api/types";
import { Logo, UliPattern } from "../components/brand";
import { Button, Field } from "../components/ui";

export function Login({ onLogin }: { onLogin: (name: string, role: Role) => void }) {
  const [user, setUser] = useState(isDemo ? "andre" : "");
  const [pass, setPass] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);

  return (
    <div className="login">
      <div className="login-form">
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError(null);
            try {
              const r = await api.login(user.trim(), pass, needCode ? code.trim() : undefined);
              onLogin(r.name, r.role);
            } catch (err) {
              if (err instanceof TwoFactorRequired) { setNeedCode(true); setError(null); }
              else setError((err as Error).message);
            } finally {
              setBusy(false);
            }
          }}
        >
          <Logo size={36} />
          <div>
            <h1>Sign in</h1>
            <p className="muted" style={{ marginTop: 6 }}>Manage your servers, websites and email.</p>
          </div>
          {needCode ? (
            <>
              <p>Open your authenticator app and enter the code for <strong>{user}</strong>.</p>
              <Field label="Authentication code" error={error} hint={`Lost your phone? Enter one of your recovery codes instead.${isDemo ? " In this preview any 6 digits work." : ""}`}>
                {(id) => <input id={id} className="input mono" style={{ letterSpacing: "0.2em", fontSize: "1.1rem" }} value={code} onChange={(e) => setCode(e.target.value)}
                  inputMode="numeric" autoComplete="one-time-code" autoFocus aria-invalid={!!error} />}
              </Field>
            </>
          ) : (
            <>
              <Field label="Username">
                {(id) => <input id={id} className="input" value={user} onChange={(e) => setUser(e.target.value)} autoComplete="username" />}
              </Field>
              <Field label="Password" error={error} hint={isDemo ? "In this preview any password works." : undefined}>
                {(id) => (
                  <input id={id} className="input" type="password" value={pass} onChange={(e) => setPass(e.target.value)}
                    autoComplete="current-password" aria-invalid={!!error} />
                )}
              </Field>
            </>
          )}
          <Button variant="primary" type="submit" loading={busy} style={{ height: 44 }}>{needCode ? "Verify" : "Sign in"}</Button>
          {needCode ? <button type="button" className="link small muted" onClick={() => { setNeedCode(false); setCode(""); setPass(""); setError(null); }}>Use a different account</button>
            : <p className="small muted">Accounts with two-factor sign-in are asked for a code next.</p>}
        </form>
      </div>
      <div className="login-art" aria-hidden>
        <UliPattern />
        <p className="quote">
          Ike means strength.
          <span>Open-source hosting control for Linux servers.</span>
        </p>
      </div>
    </div>
  );
}
