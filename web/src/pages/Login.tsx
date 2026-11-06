import { useState } from "react";
import { api, isDemo, TwoFactorRequired } from "../api/client";
import type { Role } from "../api/types";
import { Logo, UliPattern } from "../components/brand";
import { Button, Field } from "../components/ui";
import { languages, useI18n, type Lang } from "../i18n";

export function Login({ onLogin }: { onLogin: (name: string, role: Role) => void }) {
  const [user, setUser] = useState(isDemo ? "andre" : "");
  const [pass, setPass] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const { t, lang, setLang } = useI18n();

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
            <h1>{t("Sign in")}</h1>
            <p className="muted" style={{ marginTop: 6 }}>{t("Manage your servers, websites and email.")}</p>
          </div>
          {needCode ? (
            <>
              <p>{t("Open your authenticator app and enter the code for")} <strong>{user}</strong>.</p>
              <Field label={t("Authentication code")} error={error} hint={t("Lost your phone? Enter one of your recovery codes instead.") + (isDemo ? " In this preview any 6 digits work." : "")}>
                {(id) => <input id={id} className="input mono" style={{ letterSpacing: "0.2em", fontSize: "1.1rem" }} value={code} onChange={(e) => setCode(e.target.value)}
                  inputMode="numeric" autoComplete="one-time-code" autoFocus aria-invalid={!!error} />}
              </Field>
            </>
          ) : (
            <>
              <Field label={t("Username")}>
                {(id) => <input id={id} className="input" value={user} onChange={(e) => setUser(e.target.value)} autoComplete="username" />}
              </Field>
              <Field label={t("Password")} error={error} hint={isDemo ? "In this preview any password works." : undefined}>
                {(id) => (
                  <input id={id} className="input" type="password" value={pass} onChange={(e) => setPass(e.target.value)}
                    autoComplete="current-password" aria-invalid={!!error} />
                )}
              </Field>
            </>
          )}
          <Button variant="primary" type="submit" loading={busy} style={{ height: 44 }}>{needCode ? t("Verify") : t("Sign in")}</Button>
          {needCode ? <button type="button" className="link small muted" onClick={() => { setNeedCode(false); setCode(""); setPass(""); setError(null); }}>{t("Use a different account")}</button>
            : <p className="small muted">{t("Accounts with two-factor sign-in are asked for a code next.")}</p>}
        </form>
        <label className="login-lang small muted">
          {t("Language")}{" "}
          <select className="input" value={lang} onChange={(e) => setLang(e.target.value as Lang)} aria-label={t("Language")}>
            {languages.map((l) => <option key={l.id} value={l.id}>{l.name}{l.id !== "en" ? ` (${l.english})` : ""}</option>)}
          </select>
        </label>
      </div>
      <div className="login-art" aria-hidden>
        <UliPattern />
        <p className="quote">
          {t("Ike means strength.")}
          <span>{t("Open-source hosting control for Linux servers.")}</span>
        </p>
      </div>
    </div>
  );
}
