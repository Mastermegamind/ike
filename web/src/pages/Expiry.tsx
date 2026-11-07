// Expiry dates: registered domains, SSL certificates and hosting in one list,
// soonest first, plus a small "running out soon" card for the dashboard.
import { useState } from "react";
import { CalendarClock, CalendarX2, Globe, LockKeyhole, RefreshCw, RotateCw, Server } from "lucide-react";
import { api } from "../api/client";
import type { Role } from "../api/types";
import type { ExpiryItem, ExpiryKind, ExpiryList, ExpiryState } from "../api/types5";
import { Badge, Button, Empty, Skeleton, Tabs } from "../components/ui";
import { PageHead, Toggle } from "../components/common";
import { enc, FormModal, NumberField, TextField, useAction, useGet } from "../components/kit5";
import { href } from "../router";

const kindLabel: Record<ExpiryKind, string> = { domain: "Domain", ssl: "SSL certificate", hosting: "Hosting" };
const kindIcon: Record<ExpiryKind, typeof Globe> = { domain: Globe, ssl: LockKeyhole, hosting: Server };
const tone: Record<ExpiryState, "ok" | "warn" | "bad" | undefined> = { ok: "ok", soon: "warn", urgent: "bad", expired: "bad", unknown: undefined };

function dateOnly(iso?: string) {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) || d.getFullYear() < 2000 ? "" : d.toLocaleDateString(undefined, { dateStyle: "medium" });
}

/** "12 days", "Tomorrow", "Expired 3 days ago", "Unknown". */
export function whenLabel(it: ExpiryItem) {
  if (it.state === "unknown") return "Unknown";
  if (it.state === "expired") {
    const ago = Math.abs(it.days_left);
    return ago < 1 ? "Expired today" : `Expired ${ago} day${ago === 1 ? "" : "s"} ago`;
  }
  if (it.days_left === 0) return "Today";
  if (it.days_left === 1) return "Tomorrow";
  return `${it.days_left} days`;
}

/** A note worth showing in red: it's expired, or it won't renew by itself and is close. */
function problem(it: ExpiryItem) {
  return it.state === "expired" || (it.state === "urgent" && !it.auto_renews) || (it.kind === "ssl" && it.state !== "ok" && !it.auto_renews);
}

function linkFor(it: ExpiryItem) {
  if (it.kind === "ssl") return href(`/sites/${it.name}`);
  if (it.kind === "hosting") return href("/billing");
  return href(`/dns?account=${enc(it.account)}`);
}

export function ExpiryPage({ role }: { role: Role }) {
  const [kind, setKind] = useState<"all" | ExpiryKind>("all");
  const list = useGet<ExpiryList>("/expiry");
  const [dateFor, setDateFor] = useState<string | null>(null);
  const [date, setDate] = useState("");
  const [domainFor, setDomainFor] = useState<ExpiryItem | null>(null);
  const [dom, setDom] = useState({ auto: false, years: 1, days: 30, manual: "" });
  const [renewing, setRenewing] = useState<ExpiryItem | null>(null);
  const { busy, run } = useAction();
  const canSetDates = role === "admin" || role === "reseller";
  const items = (list.data?.items ?? []).filter((i) => kind === "all" || i.kind === kind);
  const sm = list.data?.summary;
  const refresh = (domain: string) => run(`r:${domain}`, () => api.call("POST", `/expiry/domains/${enc(domain)}/refresh`), `Checked ${domain} with its registry`).then(() => list.reload());
  return (
    <div className="stack">
      <PageHead title="Expiry dates" text="When domains, SSL certificates and hosting run out, soonest first. Domain dates come from each domain's registry and are checked daily; reminders go to the alert channels at 30, 14, 7, 3 and 1 days."
        actions={<Button onClick={list.reload} loading={list.loading}><RefreshCw size={16} />Refresh</Button>} />
      <section className="vitals" aria-label="Summary">
        <Tile label="Expired" value={sm?.expired} bad />
        <Tile label="Within 7 days" value={sm?.within_7} bad />
        <Tile label="Within 30 days" value={sm?.within_30} />
        <Tile label="Date unknown" value={sm?.unknown} />
      </section>
      <Tabs value={kind} onChange={setKind} tabs={[{ value: "all", label: "Everything" }, { value: "domain", label: "Domains" }, { value: "ssl", label: "SSL" }, { value: "hosting", label: "Hosting" }]} />
      <section className="panel">
        {!list.data ? <div className="panel-body"><Skeleton h={120} /></div> : items.length === 0 ? (
          <Empty icon={<CalendarClock size={22} />} title="Nothing to show" text={kind === "hosting" ? "Hosting dates come from Billing subscriptions, or a date set for accounts billed elsewhere." : "Domains appear when they have a website or DNS zone here; certificates when a website has HTTPS."} />
        ) : (
          <div className="table-wrap"><table className="responsive">
            <thead><tr><th>What</th><th>Account</th><th>Expires</th><th>Left</th><th /></tr></thead>
            <tbody>{items.map((it) => {
              const Icon = kindIcon[it.kind];
              return (
                <tr key={`${it.kind}:${it.name}`}>
                  <td className="lead"><a href={linkFor(it)} style={{ fontWeight: 600, display: "inline-flex", gap: 8, alignItems: "center" }}><Icon size={16} aria-hidden />{it.name}</a>
                    <div className="small muted">{kindLabel[it.kind]}{it.auto_renews ? " · renews automatically" : ""}{it.kind === "domain" && it.source === "website" ? " · DNS elsewhere" : ""}</div>
                    {it.detail ? <div className="small" style={{ color: problem(it) ? "var(--bad)" : undefined }}>{it.detail}</div> : null}</td>
                  <td className="small">{it.account}</td>
                  <td className="small">{dateOnly(it.expires) || "—"}</td>
                  <td><Badge tone={tone[it.state]}>{whenLabel(it)}</Badge>
                    {it.kind === "hosting" && it.suspends_on && it.state !== "ok" ? <div className="small" style={{ color: "var(--bad)", marginTop: 4, whiteSpace: "nowrap" }}>Suspends {dateOnly(it.suspends_on)} if unpaid</div> : null}</td>
                  <td style={{ textAlign: "right", whiteSpace: "nowrap" }}>
                    {it.kind === "domain" ? <Button size="sm" variant="ghost" loading={busy === `r:${it.name}`} onClick={() => refresh(it.name)} aria-label={`Check ${it.name} now`}><RefreshCw size={15} />Check</Button> : null}
                    {it.kind === "domain" && canSetDates ? <Button size="sm" variant="ghost" onClick={() => {
                      setDomainFor(it); setDom({ auto: it.auto_renews, years: 1, days: 30, manual: it.source === "manual" ? it.expires?.slice(0, 10) ?? "" : "" });
                    }}>Renewal</Button> : null}
                    {it.kind === "domain" && it.at_registrar && role === "admin" ? <Button size="sm" variant="ghost" onClick={() => setRenewing(it)}><RotateCw size={15} />Renew</Button> : null}
                    {it.kind === "hosting" && it.invoice_id && role !== "admin" && role !== "reseller" ? <a className="btn sm primary" href={href(`/billing?invoice=${enc(it.invoice_id)}`)}>Pay now</a> : null}
                    {it.kind === "hosting" && it.source === "manual" && canSetDates ? <Button size="sm" variant="ghost" onClick={() => { setDateFor(it.account); setDate(it.expires?.slice(0, 10) ?? ""); }}>Change</Button> : null}
                  </td>
                </tr>
              );
            })}</tbody>
          </table></div>
        )}
      </section>
      {canSetDates ? (
        <section className="panel">
          <div className="panel-head"><div><h2>Accounts billed elsewhere</h2><p className="small muted">Accounts on a Billing subscription get their date from it. For one billed in WHMCS, Blesta or by hand, set when its hosting ends so it shows here and you get reminders.</p></div>
            <Button onClick={() => { setDateFor(""); setDate(""); }}><CalendarX2 size={16} />Set an end date</Button></div>
        </section>
      ) : null}
      <FormModal open={!!domainFor} onClose={() => setDomainFor(null)} title={domainFor ? `Renewing ${domainFor.name}` : ""} submit="Save"
        description={domainFor?.at_registrar ? "This domain is in the Namecheap account connected under DNS settings." : "This domain is registered elsewhere. Set its renewal date if its registry doesn't report one, so reminders go out on time."}
        onSubmit={async () => {
          const body: Record<string, unknown> = { manual_expires: dom.manual };
          if (role === "admin" && domainFor?.at_registrar) Object.assign(body, { auto_renew: dom.auto, years: dom.years, days_before: dom.days });
          await api.call("PUT", `/expiry/domains/${enc(domainFor!.name)}`, body); list.reload();
        }}>
        {role === "admin" && domainFor?.at_registrar ? <>
          <Toggle checked={dom.auto} onChange={(v) => setDom({ ...dom, auto: v })} label="Renew it automatically through Namecheap" />
          {dom.auto ? <div className="grid-2">
            <NumberField label="Days before it expires" value={dom.days} onChange={(v) => setDom({ ...dom, days: v })} min={1} max={90} />
            <NumberField label="Years each time" value={dom.years} onChange={(v) => setDom({ ...dom, years: v })} min={1} max={10} />
          </div> : null}
          {dom.auto ? <p className="small muted">Renewals are paid from your Namecheap balance. A failed renewal is reported on your alert channels and shown here.</p> : null}
        </> : null}
        <TextField label="Renewal date set by hand (optional)" type="date" value={dom.manual} onChange={(v) => setDom({ ...dom, manual: v })}
          hint="Leave empty to use the date from the registry." />
      </FormModal>
      <FormModal open={!!renewing} onClose={() => setRenewing(null)} title={renewing ? `Renew ${renewing.name} now?` : ""} submit="Renew for 1 year"
        description="Namecheap charges your account balance now. The new expiry date shows here once the registry updates."
        onSubmit={async () => { await api.call("POST", `/expiry/domains/${enc(renewing!.name)}/renew`, { years: 1 }); list.reload(); }}><span /></FormModal>
      <FormModal open={dateFor !== null} onClose={() => setDateFor(null)} title="Hosting end date" submit="Save"
        description="Leave the date empty to remove it."
        onSubmit={async () => { await api.call("PUT", `/expiry/hosting/${enc(dateFor || "")}`, { expires: date }); list.reload(); }}
        disabled={!dateFor}>
        <TextField label="Account" value={dateFor ?? ""} onChange={setDateFor} placeholder="shop" mono />
        <TextField label="Hosting ends on" type="date" value={date} onChange={setDate} />
      </FormModal>
    </div>
  );
}

function Tile({ label, value, bad }: { label: string; value?: number; bad?: boolean }) {
  return (
    <div className="vital">
      <div className="vital-label">{label}</div>
      {value === undefined ? <Skeleton h={40} w={60} /> : <div className="vital-value" style={{ color: bad && value > 0 ? "var(--bad)" : undefined }}>{value}</div>}
    </div>
  );
}

/** Dashboard card: what runs out in the next 30 days. Hidden when nothing does. */
export function ExpiringSoon() {
  const list = useGet<ExpiryList>("/expiry");
  const soon = (list.data?.items ?? []).filter((i) => i.state === "expired" || i.state === "urgent" || i.state === "soon").slice(0, 5);
  if (!list.data || soon.length === 0) return null;
  return (
    <section className="panel">
      <div className="panel-head">
        <div><h2>Running out soon</h2><p className="small muted">Domains, SSL and hosting expiring in the next 30 days.</p></div>
        <a className="btn ghost sm" href={href("/expiry")}>All dates</a>
      </div>
      <div className="table-wrap"><table className="responsive"><tbody>
        {soon.map((it) => {
          const Icon = kindIcon[it.kind];
          return (
            <tr key={`${it.kind}:${it.name}`}>
              <td className="lead"><a href={linkFor(it)} style={{ display: "inline-flex", gap: 8, alignItems: "center", fontWeight: 600 }}><Icon size={16} aria-hidden />{it.name}</a>
                <div className="small muted">{kindLabel[it.kind]} · {it.account}</div></td>
              <td style={{ textAlign: "right" }}><Badge tone={tone[it.state]}>{whenLabel(it)}</Badge></td>
            </tr>
          );
        })}
      </tbody></table></div>
    </section>
  );
}
