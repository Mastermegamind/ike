// Running a hosting business: invoices and payment gateways, uptime alerts, and the people who sign in.
import { useState } from "react";
import {
  BellRing, CircleAlert, CreditCard, ExternalLink, FileText, KeyRound, Landmark, Mail, MessageCircle, MessageSquareText, Plus, Send, ShieldCheck, UserPlus, Users,
} from "lucide-react";
import { api } from "../api/client";
import type { AlertSettings, BankTransfer, BillingSettings, Invoice, PaymentProvider, Person, Role } from "../api/types";
import { Badge, Button, CopyField, Empty, Field, Modal, Segmented, Skeleton, Tabs, timeAgo, useAsync, useToast } from "../components/ui";
import { PageHead, Toggle } from "../components/common";
import { roleLabel } from "../layouts/nav";
import { DeveloperFields, areaBadges } from "./Developers";

export function money(minor: number, currency: string) {
  try {
    return new Intl.NumberFormat(currency === "NGN" ? "en-NG" : "en", { style: "currency", currency, minimumFractionDigits: 2 }).format(minor / 100);
  } catch {
    return `${currency} ${(minor / 100).toFixed(2)}`;
  }
}

const providerName: Record<PaymentProvider, string> = { bachs: "Bachs", paystack: "Paystack", flutterwave: "Flutterwave" };
const providerText: Record<PaymentProvider, string> = {
  bachs: "Cards, bank transfer and stablecoins, settled in naira or dollars.",
  paystack: "Cards, bank transfer, USSD and Opay across Nigeria, Ghana, Kenya and South Africa.",
  flutterwave: "Cards, bank transfer, mobile money and USSD across Africa.",
};

/* =============================================================== Billing */

function InvoiceStatus({ inv }: { inv: Invoice }) {
  if (inv.status === "paid") return <Badge tone="ok">Paid{inv.provider ? ` · ${providerName[inv.provider]}` : ""}</Badge>;
  if (inv.status === "void") return <Badge>Void</Badge>;
  return new Date(inv.due_at).getTime() < Date.now() ? <Badge tone="bad">Overdue</Badge> : <Badge tone="warn">Unpaid</Badge>;
}

export function Billing({ role }: { role: Role }) {
  const toast = useToast();
  const isAdmin = role === "admin";
  const [tab, setTab] = useState<"invoices" | "settings">("invoices");
  const inv = useAsync(() => api.invoices());
  const bs = useAsync(() => api.billingSettings());
  const accounts = useAsync(() => api.accounts());
  const [creating, setCreating] = useState(false);
  const [paying, setPaying] = useState<Invoice | null>(null);
  const [voiding, setVoiding] = useState<Invoice | null>(null);
  const list = inv.data ?? [];
  const cur = bs.data?.settings.currency ?? "NGN";
  const unpaid = list.filter((i) => i.status === "unpaid");
  const overdue = unpaid.filter((i) => new Date(i.due_at).getTime() < Date.now());
  const sum = (xs: Invoice[]) => {
    const by: Record<string, number> = {};
    xs.forEach((i) => { by[i.currency] = (by[i.currency] || 0) + i.amount; });
    return Object.entries(by).map(([c, n]) => money(n, c)).join(" + ") || money(0, cur);
  };

  const markPaid = async (i: Invoice) => {
    try { await api.markPaid(i.id); inv.reload(); toast("ok", `${i.id} marked as paid`); } catch (e) { toast("error", (e as Error).message); }
  };

  return (
    <div className="stack">
      <PageHead title="Billing" text={isAdmin
        ? "Invoices in naira or any other currency, paid through Bachs, Paystack or Flutterwave. Payments are confirmed by signed webhooks, never by the browser."
        : "Your invoices. Pay with card, bank transfer or USSD."}
        actions={isAdmin ? <Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />New invoice</Button> : undefined} />

      {isAdmin ? <Tabs value={tab} onChange={setTab} tabs={[{ value: "invoices", label: "Invoices" }, { value: "settings", label: "Payment settings" }]} /> : null}

      {tab === "invoices" && (
        <>
          <div className="vitals panel" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(200px, 1fr))" }}>
            <div className="vital"><span className="vital-label">Waiting for payment</span><span className="vital-value" style={{ fontSize: "1.5rem" }}>{inv.data ? sum(unpaid) : "…"}</span></div>
            <div className="vital"><span className="vital-label">Overdue</span><span className="vital-value" style={{ fontSize: "1.5rem", color: overdue.length ? "var(--bad)" : undefined }}>{inv.data ? `${overdue.length} ${overdue.length === 1 ? "invoice" : "invoices"}` : "…"}</span></div>
            <div className="vital"><span className="vital-label">Overdue accounts</span><span className="vital-value" style={{ fontSize: "1.5rem" }}>{bs.data ? (bs.data.settings.auto_suspend ? `Suspended after ${bs.data.settings.grace_days} days` : "Not suspended") : "…"}</span></div>
          </div>
          <section className="panel">
            {inv.loading && !inv.data ? <div className="panel-body stack"><Skeleton /><Skeleton /></div> : list.length === 0 ? (
              <Empty icon={<FileText size={22} />} title="No invoices yet" text="Create one for an account. The customer gets a link to pay it." />
            ) : (
              <div className="table-wrap"><table className="responsive">
                <thead><tr><th>Invoice</th><th>Account</th><th className="num">Amount</th><th>Due</th><th>Status</th><th /></tr></thead>
                <tbody>{list.map((i) => (
                  <tr key={i.id}>
                    <td className="lead"><div className="mono">{i.id}</div><div className="small muted">{i.description}</div></td>
                    <td>{i.account}</td>
                    <td className="num" style={{ fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{money(i.amount, i.currency)}</td>
                    <td className="small" data-hide-sm>{i.status === "paid" && i.paid_at ? `Paid ${timeAgo(i.paid_at)}` : new Date(i.due_at).toLocaleDateString("en-NG", { day: "numeric", month: "short", year: "numeric" })}</td>
                    <td><InvoiceStatus inv={i} /></td>
                    <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                      {i.status === "unpaid" ? <>
                        <Button size="sm" variant={isAdmin ? "ghost" : "primary"} onClick={() => setPaying(i)}><CreditCard size={15} />Pay</Button>
                        {isAdmin ? <Button size="sm" variant="ghost" onClick={() => markPaid(i)}>Mark paid</Button> : null}
                        {isAdmin ? <Button size="sm" variant="ghost" onClick={() => setVoiding(i)}>Void</Button> : null}
                      </> : null}
                    </td>
                  </tr>
                ))}</tbody>
              </table></div>
            )}
          </section>
        </>
      )}

      {tab === "settings" && isAdmin && (bs.data ? <BillingSettingsForm initial={bs.data.settings} webhooks={bs.data.webhook_urls} onSaved={bs.reload} /> : <Skeleton h={300} />)}

      <NewInvoice open={creating} onClose={() => setCreating(false)} accounts={(accounts.data ?? []).map((a) => a.name)} currency={cur}
        onCreated={(i) => { setCreating(false); inv.reload(); toast("ok", `${i.id} created`); }} />
      <PayInvoice inv={paying} onClose={() => setPaying(null)} settings={bs.data?.settings} />
      <Modal open={!!voiding} onClose={() => setVoiding(null)} title={`Void ${voiding?.id}?`}
        description="The customer can no longer pay it, and it no longer counts toward suspension."
        footer={<><Button onClick={() => setVoiding(null)}>Keep invoice</Button><Button variant="danger" onClick={async () => { await api.voidInvoice(voiding!.id); setVoiding(null); inv.reload(); toast("ok", "Invoice voided"); }}>Void invoice</Button></>}>
        <p>{voiding ? `${voiding.description}, ${money(voiding.amount, voiding.currency)} for ${voiding.account}.` : ""}</p>
      </Modal>
    </div>
  );
}

function NewInvoice(props: { open: boolean; onClose: () => void; accounts: string[]; currency: string; onCreated: (i: Invoice) => void }) {
  const [account, setAccount] = useState("");
  const [description, setDescription] = useState("");
  const [amount, setAmount] = useState("");
  const [currency, setCurrency] = useState("");
  const [due, setDue] = useState("7");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const acct = account || props.accounts[0] || "";
  const cur = currency || props.currency;
  return (
    <Modal open={props.open} onClose={props.onClose} title="New invoice" description="The account owner sees it in their panel and can pay it right away."
      footer={<><Button onClick={props.onClose}>Cancel</Button><Button variant="primary" loading={busy} disabled={!acct || !amount || !description.trim()} onClick={async () => {
        setBusy(true); setError(null);
        try { const i = await api.createInvoice({ account: acct, description, amount, currency: cur, due_in_days: Number(due) }); setDescription(""); setAmount(""); props.onCreated(i); }
        catch (e) { setError((e as Error).message); } finally { setBusy(false); }
      }}>Create invoice</Button></>}>
      <div className="form-grid">
        <Field label="Account">{(id) => <select id={id} className="select" value={acct} onChange={(e) => setAccount(e.target.value)}>{props.accounts.map((a) => <option key={a}>{a}</option>)}</select>}</Field>
        <Field label="Description">{(id) => <input id={id} className="input" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Business plan, November" />}</Field>
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 110px" }}>
          <Field label="Amount" error={error}>{(id) => <input id={id} className="input mono" inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value.replace(/[^\d.]/g, ""))} placeholder="25000.00" />}</Field>
          <Field label="Currency">{(id) => <select id={id} className="select" value={cur} onChange={(e) => setCurrency(e.target.value)}>{["NGN", "GHS", "KES", "ZAR", "USD"].map((c) => <option key={c}>{c}</option>)}</select>}</Field>
        </div>
        <Field label="Due in">{(id) => <select id={id} className="select" value={due} onChange={(e) => setDue(e.target.value)}>
          <option value="0">Today</option><option value="7">7 days</option><option value="14">14 days</option><option value="30">30 days</option></select>}</Field>
      </div>
    </Modal>
  );
}

function PayInvoice({ inv, onClose, settings }: { inv: Invoice | null; onClose: () => void; settings?: BillingSettings }) {
  const enabled = (Object.keys(providerName) as PaymentProvider[]).filter((p) => settings?.providers[p]?.enabled);
  const [provider, setProvider] = useState<PaymentProvider | "">("");
  const [url, setUrl] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [transfer, setTransfer] = useState<BankTransfer | null>(null);
  const p = provider || enabled[0];
  const canTransfer: PaymentProvider[] = enabled.filter((x) => x === "paystack" || x === "flutterwave");
  const close = () => { setUrl(null); setError(null); setTransfer(null); onClose(); };
  if (inv && transfer) {
    return (
      <Modal open onClose={close} title={`Pay ${money(transfer.amount, transfer.currency)} by bank transfer`} description={`${inv.id}: ${inv.description}`}
        footer={<Button variant="primary" onClick={close}>Done</Button>}>
        <div className="form-grid">
          <p>Send exactly this amount from any Nigerian bank app. The invoice updates on its own once the transfer arrives.</p>
          <dl className="kv"><dt>Bank</dt><dd>{transfer.bank}</dd><dt>Account name</dt><dd>{transfer.account_name}</dd></dl>
          <CopyField value={transfer.account_number} label="Account number" />
          <Field label="Amount">{() => <CopyField value={(transfer.amount / 100).toFixed(2)} label="Amount" />}</Field>
          <p className="small muted">This account number works until {new Date(transfer.expires).toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" })}. Send one transfer only.</p>
        </div>
      </Modal>
    );
  }
  return (
    <Modal open={!!inv} onClose={close} title={inv ? `Pay ${money(inv.amount, inv.currency)}` : ""} description={inv ? `${inv.id}: ${inv.description}` : undefined}
      footer={url ? <><Button onClick={close}>Close</Button><a className="btn primary" href={url} target="_blank" rel="noreferrer"><ExternalLink size={16} />Open checkout</a></>
        : <><Button onClick={close}>Cancel</Button><Button variant="primary" loading={busy} disabled={!p} onClick={async () => {
          setBusy(true); setError(null);
          try { setUrl((await api.payInvoice(inv!.id, p as PaymentProvider)).checkout_url); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>Continue to {p ? providerName[p as PaymentProvider] : "checkout"}</Button></>}>
      {url ? (
        <div className="stack" style={{ gap: 10 }}>
          <p>Your checkout is ready. Finish paying on {providerName[p as PaymentProvider]}'s page; this invoice updates on its own once they confirm the payment.</p>
          <CopyField value={url} label="Checkout link" />
        </div>
      ) : enabled.length === 0 ? (
        <div className="callout warn"><CircleAlert size={18} />No payment method is set up on this server yet.</div>
      ) : (
        <div className="stack" style={{ gap: 10 }} role="radiogroup" aria-label="Payment method">
          {enabled.map((x) => (
            <label key={x} className="mode-option" style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: 12, alignItems: "start", textAlign: "left" }} aria-checked={p === x}>
              <input type="radio" name="provider" checked={p === x} onChange={() => setProvider(x)} style={{ marginTop: 4 }} />
              <span><strong>{providerName[x]}</strong><span className="small muted" style={{ display: "block" }}>{providerText[x]}</span></span>
            </label>
          ))}
          {canTransfer.length ? (
            <Button loading={busy} onClick={async () => {
              setBusy(true); setError(null);
              try { setTransfer(await api.call<BankTransfer>("POST", `/billing/invoices/${encodeURIComponent(inv!.id)}/transfer`, { provider: canTransfer.includes(p as PaymentProvider) ? p : canTransfer[0] })); }
              catch (e) { setError((e as Error).message); } finally { setBusy(false); }
            }}><Landmark size={16} />Pay by bank transfer instead</Button>
          ) : null}
          {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
        </div>
      )}
    </Modal>
  );
}

function BillingSettingsForm({ initial, webhooks, onSaved }: { initial: BillingSettings; webhooks: Record<PaymentProvider, string>; onSaved: () => void }) {
  const toast = useToast();
  const [s, setS] = useState<BillingSettings>(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const setP = (k: PaymentProvider, patch: Partial<NonNullable<BillingSettings["providers"][PaymentProvider]>>) =>
    setS({ ...s, providers: { ...s.providers, [k]: { enabled: false, ...s.providers[k], ...patch } } });

  return (
    <div className="stack">
      <section className="panel">
        <div className="panel-head"><h2>Invoicing</h2></div>
        <div className="panel-body form-grid">
          <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(180px, 1fr))" }}>
            <Field label="Default currency" hint="Naira by default. Each invoice can use its own.">
              {(id) => <select id={id} className="select" value={s.currency} onChange={(e) => setS({ ...s, currency: e.target.value })}>{["NGN", "GHS", "KES", "ZAR", "USD"].map((c) => <option key={c}>{c}</option>)}</select>}
            </Field>
            <Field label="Grace period" hint="Days after the due date before suspension.">
              {(id) => <div className="input-group"><input id={id} className="input mono" inputMode="numeric" value={s.grace_days} onChange={(e) => setS({ ...s, grace_days: Number(e.target.value.replace(/\D/g, "")) || 0 })} /><span className="addon">days</span></div>}
            </Field>
          </div>
          <Toggle checked={s.auto_suspend} onChange={(v) => setS({ ...s, auto_suspend: v })} label="Suspend accounts with overdue invoices, and reactivate them when paid" />
        </div>
      </section>

      {(Object.keys(providerName) as PaymentProvider[]).map((k) => {
        const p = s.providers[k] ?? { enabled: false };
        return (
          <section key={k} className="panel">
            <div className="panel-head">
              <div><h2>{providerName[k]}</h2><p className="small muted">{providerText[k]}</p></div>
              <Toggle checked={p.enabled} onChange={(v) => setP(k, { enabled: v })} label={p.enabled ? "On" : "Off"} />
            </div>
            {p.enabled ? (
              <div className="panel-body form-grid">
                <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(220px, 1fr))" }}>
                  <Field label="Secret key" hint={k === "paystack" ? "Starts with sk_live_ or sk_test_. Also used to verify webhooks." : k === "flutterwave" ? "Starts with FLWSECK-." : "From Developers → API keys in the Bachs dashboard."}>
                    {(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={p.secret_key ?? ""} onChange={(e) => setP(k, { secret_key: e.target.value })} />}
                  </Field>
                  {k !== "paystack" ? (
                    <Field label={k === "flutterwave" ? "Secret hash" : "Webhook signing secret"} hint={k === "flutterwave" ? "Set the same value under Settings → Webhooks." : "Shown when you add the webhook endpoint."}>
                      {(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={p.webhook_secret ?? ""} onChange={(e) => setP(k, { webhook_secret: e.target.value })} />}
                    </Field>
                  ) : null}
                </div>
                {k === "bachs" ? <Toggle checked={!!p.sandbox} onChange={(v) => setP(k, { sandbox: v })} label="Sandbox mode (test payments)" /> : null}
                <Field label="Webhook URL" hint={`Add this in your ${providerName[k]} dashboard. Ike checks the signature on every call and confirms the amount with ${providerName[k]} before marking an invoice paid.`}>
                  {() => <CopyField value={webhooks[k]} label={`${providerName[k]} webhook URL`} />}
                </Field>
              </div>
            ) : null}
          </section>
        );
      })}
      {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
      <div><Button variant="primary" loading={busy} onClick={async () => {
        setBusy(true); setError(null);
        try { await api.saveBillingSettings(s); toast("ok", "Payment settings saved"); onSaved(); } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
      }}>Save payment settings</Button></div>
    </div>
  );
}

/* ================================================================ Alerts */

export function Alerts() {
  const toast = useToast();
  const st = useAsync(() => api.alertSettings());
  const ev = useAsync(() => api.alertEvents());
  const [a, setA] = useState<AlertSettings | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [results, setResults] = useState<Record<string, string> | null>(null);
  const cur = a ?? st.data;
  if (!cur) return <div className="stack"><PageHead title="Alerts" /><Skeleton h={300} /></div>;
  const set = (patch: Partial<AlertSettings>) => setA({ ...cur, ...patch });

  return (
    <div className="stack">
      <PageHead title="Alerts" text="Ike checks every website and app, and tells you when one goes down and when it's back."
        actions={<Button loading={busy === "test"} onClick={async () => {
          setBusy("test"); setResults(null);
          try { setResults(await api.testAlert()); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
        }}><Send size={16} />Send a test alert</Button>} />

      {results ? (
        <div className="callout info" role="status"><BellRing size={18} /><div>
          {Object.entries(results).map(([k, v]) => <div key={k}><strong style={{ textTransform: "capitalize" }}>{k}</strong>: {v === "sent" ? "sent" : <span style={{ color: "var(--bad)" }}>{v}</span>}</div>)}
        </div></div>
      ) : null}

      <section className="panel">
        <div className="panel-head"><div><h2>Uptime checks</h2><p className="small muted">Each website is fetched through this server. Two failures in a row count as down.</p></div>
          <Segmented label="Check every" value={String(cur.uptime_interval_min)} onChange={(v) => set({ uptime_interval_min: Number(v) })}
            options={[{ value: "0", label: "Off" }, { value: "1", label: "1 min" }, { value: "5", label: "5 min" }, { value: "15", label: "15 min" }]} />
        </div>
      </section>

      <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", alignItems: "start" }}>
        <section className="panel">
          <div className="panel-head"><div className="row" style={{ gap: 8 }}><Send size={18} aria-hidden /><h2>Telegram</h2></div>
            <Toggle checked={cur.telegram.enabled} onChange={(v) => set({ telegram: { ...cur.telegram, enabled: v } })} label={cur.telegram.enabled ? "On" : "Off"} /></div>
          {cur.telegram.enabled ? <div className="panel-body form-grid">
            <Field label="Bot token" hint="Create a bot with @BotFather.">{(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={cur.telegram.bot_token ?? ""} onChange={(e) => set({ telegram: { ...cur.telegram, bot_token: e.target.value } })} />}</Field>
            <Field label="Chat ID" hint="Your user ID, or a group's ID (starts with -100).">{(id) => <input id={id} className="input mono" value={cur.telegram.chat_id} onChange={(e) => set({ telegram: { ...cur.telegram, chat_id: e.target.value } })} />}</Field>
          </div> : null}
        </section>

        <section className="panel">
          <div className="panel-head"><div className="row" style={{ gap: 8 }}><MessageCircle size={18} aria-hidden /><h2>WhatsApp</h2></div>
            <Toggle checked={cur.whatsapp.enabled} onChange={(v) => set({ whatsapp: { ...cur.whatsapp, enabled: v } })} label={cur.whatsapp.enabled ? "On" : "Off"} /></div>
          {cur.whatsapp.enabled ? <div className="panel-body form-grid">
            <Field label="Access token" hint="A permanent token from Meta's WhatsApp Cloud API.">{(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={cur.whatsapp.token ?? ""} onChange={(e) => set({ whatsapp: { ...cur.whatsapp, token: e.target.value } })} />}</Field>
            <Field label="Phone number ID">{(id) => <input id={id} className="input mono" value={cur.whatsapp.phone_number_id} onChange={(e) => set({ whatsapp: { ...cur.whatsapp, phone_number_id: e.target.value } })} />}</Field>
            <Field label="Send to" hint="In international format, like 2348031234567.">{(id) => <input id={id} className="input mono" inputMode="tel" value={cur.whatsapp.to} onChange={(e) => set({ whatsapp: { ...cur.whatsapp, to: e.target.value.replace(/[^\d]/g, "") } })} />}</Field>
            <Field label="Message template (optional)" hint="WhatsApp only delivers plain messages within 24 hours of your last message to the number. An approved template works any time.">
              {(id) => <input id={id} className="input mono" value={cur.whatsapp.template ?? ""} onChange={(e) => set({ whatsapp: { ...cur.whatsapp, template: e.target.value } })} placeholder="ike_alert" />}</Field>
          </div> : null}
        </section>

        <section className="panel">
          <div className="panel-head"><div className="row" style={{ gap: 8 }}><Mail size={18} aria-hidden /><h2>Email</h2></div>
            <Toggle checked={cur.email.enabled} onChange={(v) => set({ email: { ...cur.email, enabled: v } })} label={cur.email.enabled ? "On" : "Off"} /></div>
          {cur.email.enabled ? <div className="panel-body form-grid">
            <div className="grid-2" style={{ gridTemplateColumns: "1fr 90px" }}>
              <Field label="SMTP server">{(id) => <input id={id} className="input mono" value={cur.email.host} onChange={(e) => set({ email: { ...cur.email, host: e.target.value } })} />}</Field>
              <Field label="Port">{(id) => <input id={id} className="input mono" inputMode="numeric" value={cur.email.port} onChange={(e) => set({ email: { ...cur.email, port: Number(e.target.value.replace(/\D/g, "")) || 0 } })} />}</Field>
            </div>
            <p className="small muted" style={{ marginTop: -8 }}>Port 465 uses TLS. Any other port must offer STARTTLS, or Ike won't send.</p>
            <Field label="Username">{(id) => <input id={id} className="input mono" value={cur.email.username} onChange={(e) => set({ email: { ...cur.email, username: e.target.value } })} />}</Field>
            <Field label="Password">{(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={cur.email.password ?? ""} onChange={(e) => set({ email: { ...cur.email, password: e.target.value } })} />}</Field>
            <Field label="From">{(id) => <input id={id} className="input mono" value={cur.email.from} onChange={(e) => set({ email: { ...cur.email, from: e.target.value } })} />}</Field>
            <Field label="Send to">{(id) => <input id={id} className="input mono" value={cur.email.to} onChange={(e) => set({ email: { ...cur.email, to: e.target.value } })} />}</Field>
          </div> : null}
        </section>
      </div>

      <SmsAndCustomers cur={cur} set={set} />

      <div><Button variant="primary" disabled={!a} loading={busy === "save"} onClick={async () => {
        setBusy("save");
        try { st.setData(await api.saveAlertSettings(cur)); setA(null); toast("ok", "Alert settings saved"); } catch (e) { toast("error", (e as Error).message); } finally { setBusy(null); }
      }}>Save alert settings</Button></div>

      <section className="panel">
        <div className="panel-head"><h2>Recent alerts</h2></div>
        {(ev.data ?? []).length === 0 ? <Empty icon={<BellRing size={22} />} title="No alerts yet" text="When a website goes down or comes back, it's listed here." /> : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>When</th><th>Website</th><th>What happened</th></tr></thead>
            <tbody>{(ev.data ?? []).map((e, i) => (
              <tr key={i}>
                <td className="small muted" style={{ whiteSpace: "nowrap" }}>{timeAgo(e.time)}</td>
                <td className="lead">{e.domain}</td>
                <td>{e.kind === "down" ? <Badge tone="bad">Down</Badge> : <Badge tone="ok">Back up</Badge>} <span className="small muted">{e.detail}</span></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
    </div>
  );
}

const defaultSms: NonNullable<AlertSettings["sms"]> = { enabled: false, provider: "termii", to: "" };

/** SMS alerts (Termii, Africa's Talking) and expiry emails to customers. */
function SmsAndCustomers({ cur, set }: { cur: AlertSettings; set: (p: Partial<AlertSettings>) => void }) {
  const sms = { ...defaultSms, ...(cur.sms ?? {}) };
  const cust = cur.customers ?? { enabled: false, days: null };
  const days = (cust.days?.length ? cust.days : [30, 14, 7, 1]).join(", ");
  const setSms = (p: Partial<typeof sms>) => set({ sms: { ...sms, ...p } });
  return (
    <div className="grid-2" style={{ gridTemplateColumns: "repeat(auto-fit, minmax(300px, 1fr))", alignItems: "start" }}>
      <section className="panel">
        <div className="panel-head"><div className="row" style={{ gap: 8 }}><MessageSquareText size={18} aria-hidden /><h2>SMS</h2></div>
          <Toggle checked={sms.enabled} onChange={(v) => setSms({ enabled: v })} label={sms.enabled ? "On" : "Off"} /></div>
        {sms.enabled ? <div className="panel-body form-grid">
          <Segmented label="Provider" value={sms.provider || "termii"} onChange={(v) => setSms({ provider: v as "termii" | "africastalking" })}
            options={[{ value: "termii", label: "Termii" }, { value: "africastalking", label: "Africa's Talking" }]} />
          <Field label="API key">{(id) => <input id={id} className="input mono" type="password" autoComplete="off" value={sms.api_key ?? ""} onChange={(e) => setSms({ api_key: e.target.value })} />}</Field>
          {sms.provider === "africastalking" ? (
            <Field label="App username" hint="“sandbox” sends to the simulator.">{(id) => <input id={id} className="input mono" value={sms.username ?? ""} onChange={(e) => setSms({ username: e.target.value })} />}</Field>
          ) : null}
          <Field label={sms.provider === "termii" ? "Sender ID" : "Sender ID (optional)"} hint={sms.provider === "termii" ? "An approved sender ID from Termii (Rental → Sender IDs)." : undefined}>
            {(id) => <input id={id} className="input mono" maxLength={11} value={sms.sender_id ?? ""} onChange={(e) => setSms({ sender_id: e.target.value })} />}</Field>
          {sms.provider === "termii" ? <>
            <Segmented label="Route" value={sms.channel || "dnd"} onChange={(v) => setSms({ channel: v as "dnd" | "generic" })}
              options={[{ value: "dnd", label: "DND (reaches every number)" }, { value: "generic", label: "Generic" }]} />
            <Field label="Base URL (optional)" hint="Only if your Termii dashboard shows an account-specific one.">{(id) => <input id={id} className="input mono" placeholder="https://api.ng.termii.com" value={sms.base_url ?? ""} onChange={(e) => setSms({ base_url: e.target.value })} />}</Field>
          </> : null}
          <Field label="Send to" hint="One or more numbers, separated by commas: 0803… or +234803… both work.">{(id) => <input id={id} className="input mono" inputMode="tel" value={sms.to} onChange={(e) => setSms({ to: e.target.value })} />}</Field>
        </div> : null}
      </section>
      <section className="panel">
        <div className="panel-head"><div><h2>Expiry emails to customers</h2><p className="small muted">Account owners get an email before their hosting, and domains and certificates that don't renew themselves, run out. Hosting emails have a Pay now link. Sent in each reseller's branding.</p></div>
          <Toggle checked={cust.enabled} onChange={(v) => set({ customers: { ...cust, enabled: v } })} label={cust.enabled ? "On" : "Off"} /></div>
        {cust.enabled ? <div className="panel-body form-grid">
          <Field label="Days before" hint="Up to six, separated by commas.">{(id) => <input id={id} className="input mono" defaultValue={days}
            onBlur={(e) => set({ customers: { ...cust, days: e.target.value.split(/[ ,]+/).map(Number).filter((n) => Number.isInteger(n) && n >= 0 && n <= 90).slice(0, 6) } })} />}</Field>
          {!cur.email.host || !cur.email.from ? <div className="callout warn small"><CircleAlert size={18} />These go through the SMTP server under Email. Switch Email on to set it up (you can switch it off again afterwards).</div> : null}
        </div> : null}
      </section>
    </div>
  );
}

/* ================================================================ People */

export function People({ role }: { role: Role }) {
  const toast = useToast();
  const list = useAsync(() => api.people());
  const accounts = useAsync(() => api.accounts());
  const [adding, setAdding] = useState(false);
  const rows = (list.data ?? []).filter((p) => role === "admin" || p.reseller === "webafrica");

  const toggle = async (p: Person) => {
    try { await api.updatePerson(p.username, { disabled: !p.disabled }); list.reload(); toast("ok", p.disabled ? `${p.username} can sign in again` : `${p.username} can no longer sign in`); }
    catch (e) { toast("error", (e as Error).message); }
  };

  return (
    <div className="stack">
      <PageHead title="People" text={role === "admin"
        ? "Everyone who signs in to this server. Admins use port 2087, resellers 2086, and account owners 2083."
        : "Your customers' sign-ins. Each one only sees the accounts you give them."}
        actions={<Button variant="primary" onClick={() => setAdding(true)}><UserPlus size={17} />Add person</Button>} />
      <section className="panel">
        {!list.data ? <div className="panel-body stack"><Skeleton /><Skeleton /></div> : rows.length === 0 ? (
          <Empty icon={<Users size={22} />} title="Nobody here yet" text="Add a sign-in for each customer, so they can manage their own websites." />
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>Person</th><th>Role</th><th>Accounts</th><th>2FA</th><th>Last sign-in</th><th /></tr></thead>
            <tbody>{rows.map((p) => (
              <tr key={p.username} style={p.disabled ? { opacity: 0.6 } : undefined}>
                <td className="lead"><div className="primary-cell">{p.name || p.username}</div><div className="small muted">{p.username} · {p.email}</div></td>
                <td>{roleLabel[p.role]}{p.reseller ? <div className="small muted">via {p.reseller}</div> : null}</td>
                <td className="small" data-hide-sm>{p.role === "admin" ? "All" : p.role === "reseller" ? `Up to ${p.max_accounts ?? "∞"}` : p.accounts.join(", ") || "None"}
                  {p.role === "developer" ? <div className="row" style={{ flexWrap: "wrap", gap: 4, marginTop: 4 }}>{areaBadges(p)}</div> : null}</td>
                <td>{p.totp_enabled ? <Badge tone="ok"><ShieldCheck size={13} />On</Badge> : <Badge tone="warn">Off</Badge>}</td>
                <td className="small muted" data-hide-sm>{p.disabled ? "Disabled" : p.last_login_at ? timeAgo(p.last_login_at) : "Never"}</td>
                <td style={{ textAlign: "right" }}><Button size="sm" variant="ghost" onClick={() => toggle(p)}>{p.disabled ? "Enable" : "Disable"}</Button></td>
              </tr>
            ))}</tbody>
          </table></div>
        )}
      </section>
      <AddPerson open={adding} onClose={() => setAdding(false)} canAddAdmins={role === "admin"} accounts={(accounts.data ?? []).map((a) => a.name)}
        onAdded={(p) => { setAdding(false); list.reload(); toast("ok", `${p.username} added`); }} />
    </div>
  );
}

function AddPerson(props: { open: boolean; onClose: () => void; canAddAdmins: boolean; accounts: string[]; onAdded: (p: Person) => void }) {
  const [f, setF] = useState({ username: "", email: "", name: "", role: "user" as Role, password: "", accounts: [] as string[], max: "10" });
  const [areas, setAreas] = useState<Record<string, string>>({ sites: "write", files: "write" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const gen = () => {
    const chars = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
    setF({ ...f, password: Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => chars[b % chars.length]).join("") });
  };
  const roles: Role[] = props.canAddAdmins ? ["user", "developer", "reseller", "admin"] : ["user", "developer"];
  return (
    <Modal open={props.open} onClose={props.onClose} title="Add person" description="They'll be asked to turn on two-factor sign-in the first time."
      footer={<><Button onClick={props.onClose}>Cancel</Button><Button variant="primary" loading={busy} onClick={async () => {
        setBusy(true); setError(null);
        try {
          const p = await api.createPerson({ username: f.username, email: f.email, name: f.name || undefined, role: f.role, password: f.password,
            accounts: f.role === "user" || f.role === "developer" ? f.accounts : undefined, max_accounts: f.role === "reseller" ? Number(f.max) : undefined,
            areas: f.role === "developer" ? areas : undefined });
          setF({ ...f, username: "", email: "", name: "", password: "", accounts: [] });
          props.onAdded(p);
        } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
      }}>Add person</Button></>}>
      <div className="form-grid">
        {roles.length > 1 ? <Segmented label="Role" value={f.role} onChange={(r) => setF({ ...f, role: r })} options={roles.map((r) => ({ value: r, label: roleLabel[r] }))} /> : null}
        <div className="grid-2" style={{ gridTemplateColumns: "1fr 1fr" }}>
          <Field label="Username">{(id) => <input id={id} className="input mono" value={f.username} onChange={(e) => setF({ ...f, username: e.target.value.toLowerCase() })} autoComplete="off" />}</Field>
          <Field label="Name (optional)">{(id) => <input id={id} className="input" value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />}</Field>
        </div>
        <Field label="Email">{(id) => <input id={id} className="input" type="email" value={f.email} onChange={(e) => setF({ ...f, email: e.target.value })} />}</Field>
        <Field label="Password" hint="At least 10 characters. Send it to them privately.">
          {(id) => <div className="input-group"><input id={id} className="input mono" value={f.password} onChange={(e) => setF({ ...f, password: e.target.value })} autoComplete="new-password" />
            <Button type="button" onClick={gen}><KeyRound size={15} />Generate</Button></div>}
        </Field>
        {f.role === "user" ? (
          <fieldset style={{ border: 0, padding: 0, margin: 0, display: "grid", gap: 8 }}>
            <legend className="small" style={{ fontWeight: 500, marginBottom: 6 }}>Accounts they manage</legend>
            <div className="row">{props.accounts.map((a) => (
              <label key={a} className="row" style={{ gap: 6 }}><input type="checkbox" checked={f.accounts.includes(a)}
                onChange={(e) => setF({ ...f, accounts: e.target.checked ? [...f.accounts, a] : f.accounts.filter((x) => x !== a) })} />{a}</label>
            ))}</div>
          </fieldset>
        ) : f.role === "developer" ? (
          <DeveloperFields accounts={props.accounts} value={{ accounts: f.accounts, areas }} onChange={(v) => { setF({ ...f, accounts: v.accounts }); setAreas(v.areas); }} />
        ) : f.role === "reseller" ? (
          <Field label="Accounts they can create">{(id) => <input id={id} className="input mono" inputMode="numeric" style={{ maxWidth: 120 }} value={f.max} onChange={(e) => setF({ ...f, max: e.target.value.replace(/\D/g, "") })} />}</Field>
        ) : <p className="small muted">Server admins can change everything on this server, including other admins.</p>}
        {error ? <div className="callout bad" role="alert"><CircleAlert size={18} />{error}</div> : null}
      </div>
    </Modal>
  );
}
