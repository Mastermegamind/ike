// Passkeys in the browser: turns the panel's WebAuthn options into
// navigator.credentials calls and the answers back into JSON (base64url).

export interface PasskeyInfo { id: string; name: string; synced: boolean; disabled?: boolean; created_at: string; last_used_at?: string }

const toB64 = (buf: ArrayBuffer | ArrayBufferView) => {
  const bytes = buf instanceof ArrayBuffer ? new Uint8Array(buf) : new Uint8Array(buf.buffer, buf.byteOffset, buf.byteLength);
  let s = "";
  bytes.forEach((b) => { s += String.fromCharCode(b); });
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

const fromB64 = (s: string) => {
  const b = atob(s.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((s.length + 3) % 4));
  return Uint8Array.from(b, (c) => c.charCodeAt(0));
};

export function passkeysSupported() {
  return typeof window !== "undefined" && typeof window.PublicKeyCredential === "function" && !!navigator.credentials;
}

/** Why passkeys can't work on this page, or null when they can. */
export function passkeyBlocker(): string | null {
  if (!passkeysSupported()) return "This browser can't use passkeys. Try a recent Chrome, Edge, Safari or Firefox.";
  const host = location.hostname;
  if (/^\d+\.\d+\.\d+\.\d+$/.test(host) || host.includes(":")) return "Passkeys need the panel opened by its name (like panel.example.com), not by an IP address.";
  if (location.protocol !== "https:" && host !== "localhost") return "Passkeys need the panel on HTTPS.";
  return null;
}

type Json = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any

function friendly(e: unknown): Error {
  const name = (e as { name?: string }).name;
  if (name === "NotAllowedError" || name === "AbortError") return new Error("The passkey request was cancelled or timed out.");
  if (name === "InvalidStateError") return new Error("This device already has a passkey for your account here.");
  if (name === "SecurityError") return new Error("The browser refused: the panel's address doesn't allow passkeys.");
  return e instanceof Error ? e : new Error(String(e));
}

/** Runs navigator.credentials.create() with the panel's creation options. */
export async function createPasskey(options: { publicKey: Json }) {
  const pk = options.publicKey;
  const publicKey: PublicKeyCredentialCreationOptions = {
    ...pk,
    challenge: fromB64(pk.challenge),
    user: { ...pk.user, id: fromB64(pk.user.id) },
    excludeCredentials: (pk.excludeCredentials ?? []).map((c: Json) => ({ ...c, id: fromB64(c.id) })),
  } as unknown as PublicKeyCredentialCreationOptions;
  let cred: PublicKeyCredential;
  try { cred = (await navigator.credentials.create({ publicKey })) as PublicKeyCredential; } catch (e) { throw friendly(e); }
  const r = cred.response as AuthenticatorAttestationResponse;
  return {
    id: toB64(cred.rawId),
    client_data_json: toB64(r.clientDataJSON),
    attestation_object: toB64(r.attestationObject),
    transports: typeof r.getTransports === "function" ? r.getTransports() : [],
  };
}

/** Runs navigator.credentials.get() with the panel's request options. */
export async function getPasskey(options: { publicKey: Json }) {
  const pk = options.publicKey;
  const publicKey: PublicKeyCredentialRequestOptions = {
    ...pk,
    challenge: fromB64(pk.challenge),
    allowCredentials: (pk.allowCredentials ?? []).map((c: Json) => ({ ...c, id: fromB64(c.id) })),
  } as unknown as PublicKeyCredentialRequestOptions;
  let cred: PublicKeyCredential;
  try { cred = (await navigator.credentials.get({ publicKey })) as PublicKeyCredential; } catch (e) { throw friendly(e); }
  const r = cred.response as AuthenticatorAssertionResponse;
  return {
    id: toB64(cred.rawId),
    client_data_json: toB64(r.clientDataJSON),
    authenticator_data: toB64(r.authenticatorData),
    signature: toB64(r.signature),
    user_handle: r.userHandle ? toB64(r.userHandle) : "",
  };
}

export const _test = { toB64, fromB64 };
