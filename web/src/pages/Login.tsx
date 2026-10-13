import { useState } from "react";
import { api } from "../api/client";
import { Logo, UliPattern } from "../components/brand";
import { Button, Field } from "../components/ui";

export function Login({ onLogin }: { onLogin: (name: string) => void }) {
  const [user, setUser] = useState("andre");
  const [pass, setPass] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  return (
    <div className="login">
      <div className="login-form">
        <form
          onSubmit={async (e) => {
            e.preventDefault();
            setBusy(true);
            setError(null);
            try {
              const r = await api.login(user.trim(), pass);
              onLogin(r.name);
            } catch (err) {
              setError((err as Error).message);
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
          <Field label="Username">
            {(id) => <input id={id} className="input" value={user} onChange={(e) => setUser(e.target.value)} autoComplete="username" />}
          </Field>
          <Field label="Password" error={error} hint="In this preview any password works.">
            {(id) => (
              <input id={id} className="input" type="password" value={pass} onChange={(e) => setPass(e.target.value)}
                autoComplete="current-password" aria-invalid={!!error} />
            )}
          </Field>
          <Button variant="primary" type="submit" loading={busy} style={{ height: 44 }}>Sign in</Button>
          <p className="small muted">Two-factor sign-in with an authenticator app or passkey can be required for every role.</p>
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
