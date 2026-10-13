import { useEffect, useMemo, useState } from "react";
import { Globe, Plus, Search } from "lucide-react";
import { api } from "../api/client";
import { domainError, phpEol, runtimeName } from "../api/demo";
import type { Account, Role, Runtime, Runtimes, Site, WebServer } from "../api/types";
import { Badge, Button, Empty, Field, Modal, Segmented, Skeleton, useAsync, useToast } from "../components/ui";
import { PageHead, RuntimeBadge, StatusBadge, webServerLabel } from "../components/common";
import { href, navigate } from "../router";

function query(route: string) {
  return new URLSearchParams(route.split("?")[1] || "");
}

export function Sites({ route, role }: { route: string; role: Role }) {
  const q = query(route);
  const list = useAsync(() => api.sites());
  const [filter, setFilter] = useState(q.get("q") || "");
  const [creating, setCreating] = useState(q.get("new") === "1");
  const account = q.get("account");
  useEffect(() => { setFilter(q.get("q") || ""); }, [route]); // eslint-disable-line react-hooks/exhaustive-deps

  const rows = useMemo(() => (list.data ?? []).filter((s) =>
    (!account || s.account === account) && (!filter || s.domain.includes(filter.toLowerCase()) || s.account.includes(filter.toLowerCase())),
  ), [list.data, filter, account]);

  return (
    <div className="stack">
      <PageHead
        title={account ? `Websites in ${account}` : "Websites"}
        text={account ? undefined : "Every website runs as its account's user, with its own PHP pool or app process."}
        back={account ? { to: "/sites", label: "All websites" } : undefined}
        actions={<Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />Create website</Button>}
      />
      <section className="panel">
        <div className="panel-head">
          <label className="search" style={{ maxWidth: 340 }}>
            <Search size={16} aria-hidden />
            <input value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="Filter by domain or account" aria-label="Filter websites" />
          </label>
          <span className="small muted">{rows.length} of {list.data?.length ?? 0}</span>
        </div>
        {list.loading && !list.data ? (
          <div className="panel-body stack"><Skeleton /><Skeleton /><Skeleton /></div>
        ) : rows.length === 0 ? (
          <Empty icon={<Globe size={22} />} title={filter ? "No websites match" : "No websites yet"}
            text={filter ? "Try another domain or clear the filter." : "Create a website for a domain you own. Point its DNS A record to this server."}
            action={filter ? <Button onClick={() => setFilter("")}>Clear filter</Button> : <Button variant="primary" onClick={() => setCreating(true)}><Plus size={17} />Create website</Button>} />
        ) : (
          <div className="table-wrap">
            <table className="responsive">
              <thead><tr><th>Website</th><th>Runtime</th><th>Web server</th><th>Status</th></tr></thead>
              <tbody>
                {rows.map((s) => (
                  <tr key={s.domain} className="clickable" onClick={() => navigate(`/sites/${s.domain}`)}>
                    <td className="lead">
                      <a href={href(`/sites/${s.domain}`)} className="primary-cell" style={{ textDecoration: "none" }} onClick={(e) => e.stopPropagation()}>{s.domain}</a>
                      <div className="small muted">{s.account}</div>
                    </td>
                    <td><span className="row" style={{ gap: 6 }}><RuntimeBadge runtime={s.runtime} version={s.version} />
                      {s.runtime === "php" && s.version && phpEol(s.version) ? <Badge tone="warn">End of life</Badge> : null}</span></td>
                    <td data-hide-sm>{webServerLabel[s.web_server]}</td>
                    <td><StatusBadge status={s.status} /></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
      <CreateSite open={creating} defaultAccount={account || undefined} role={role}
        onClose={() => { setCreating(false); if (q.get("new")) navigate("/sites"); }}
        onDone={(s) => { list.reload(); navigate(`/sites/${s.domain}`); }} />
    </div>
  );
}

function CreateSite(props: { open: boolean; onClose: () => void; onDone: (s: Site) => void; defaultAccount?: string; role: Role }) {
  const toast = useToast();
  const [rt, setRt] = useState<Runtimes | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [domain, setDomain] = useState("");
  const [account, setAccount] = useState(props.defaultAccount || "");
  const [runtime, setRuntime] = useState<Runtime>("php");
  const [version, setVersion] = useState("");
  const [web, setWeb] = useState<WebServer | "">("");
  const [start, setStart] = useState("");
  const [touched, setTouched] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!props.open) return;
    api.runtimes().then((r) => { setRt(r); setVersion(r.default_php); setWeb(r.web_servers[0]); });
    api.accounts().then((a) => { const ok = a.filter((x) => !x.suspended); setAccounts(ok); setAccount((cur) => cur || props.defaultAccount || ok[0]?.name || ""); });
  }, [props.open, props.defaultAccount]);

  const versions = rt ? (runtime === "static" ? [] : rt[runtime]) : [];
  useEffect(() => {
    if (!rt) return;
    setVersion(runtime === "php" ? rt.default_php : runtime === "static" ? "" : rt[runtime].at(-1) || "");
    setStart(runtime === "node" ? "npm start" : runtime === "python" ? "gunicorn app:app --bind 127.0.0.1:$PORT" : "");
  }, [runtime, rt]);

  const isApp = runtime === "node" || runtime === "python";
  const dErr = touched && domain ? domainError(domain) : null;
  const close = () => { setDomain(""); setTouched(false); setError(null); setRuntime("php"); props.onClose(); };

  return (
    <Modal open={props.open} onClose={close} title="Create website"
      description="Point the domain's DNS A record to this server so it loads."
      footer={<><Button onClick={close}>Cancel</Button>
        <Button variant="primary" loading={busy} disabled={!domain || !account || !!domainError(domain)} onClick={async () => {
          setBusy(true); setError(null);
          try {
            const s = await api.createSite({ domain, account, runtime, version: version || undefined,
              web_server: isApp ? undefined : (web || undefined), start_command: isApp ? start : undefined });
            toast("ok", `Created ${s.domain}`);
            close();
            props.onDone(s);
          } catch (e) { setError((e as Error).message); } finally { setBusy(false); }
        }}>Create website</Button></>}>
      <div className="form-grid">
        <Field label="Domain" error={dErr} hint="Without http:// or www, e.g. shop.example.com">
          {(id) => <input id={id} className="input" value={domain} autoFocus placeholder="example.com" autoComplete="off" spellCheck={false}
            onChange={(e) => setDomain(e.target.value.trim().toLowerCase().replace(/^https?:\/\//, "").replace(/^www\./, "").replace(/\/.*$/, ""))}
            onBlur={() => setTouched(true)} aria-invalid={!!dErr} />}
        </Field>
        <Field label="Account" hint={accounts.length === 0 ? "Create an account first." : "Websites in the same account share its files, SSH access and databases."}>
          {(id) => (
            <select id={id} className="select" value={account} onChange={(e) => setAccount(e.target.value)}>
              {accounts.map((a) => <option key={a.name} value={a.name}>{a.name}</option>)}
            </select>
          )}
        </Field>
        <div className="field">
          <label>Runtime</label>
          <Segmented<Runtime> label="Runtime" value={runtime} onChange={setRuntime}
            options={[{ value: "php", label: "PHP" }, { value: "node", label: "Node.js" }, { value: "python", label: "Python" }, { value: "static", label: "Static" }]} />
        </div>
        <div className="form-2">
          {runtime !== "static" ? (
            <Field label={`${runtimeName(runtime)} version`}
              hint={runtime === "php" && version && phpEol(version) ? "This version no longer gets security fixes." : undefined}>
              {(id) => (
                <select id={id} className="select" value={version} onChange={(e) => setVersion(e.target.value)}>
                  {versions.slice().reverse().map((v) => <option key={v} value={v}>{runtimeName(runtime)} {v}{runtime === "php" && phpEol(v) ? " (end of life)" : ""}</option>)}
                </select>
              )}
            </Field>
          ) : null}
          {!isApp && rt && rt.web_servers.length > 1 ? (
            <Field label="Web server" hint={web === "nginx+apache" ? ".htaccess files work." : "Fastest; .htaccess is ignored."}>
              {(id) => (
                <select id={id} className="select" value={web} onChange={(e) => setWeb(e.target.value as WebServer)}>
                  {rt.web_servers.map((w) => <option key={w} value={w}>{webServerLabel[w]}</option>)}
                </select>
              )}
            </Field>
          ) : null}
        </div>
        {isApp ? (
          <Field label="Start command" hint="Your app must listen on the PORT environment variable.">
            {(id) => <input id={id} className="input mono" value={start} onChange={(e) => setStart(e.target.value)} spellCheck={false} />}
          </Field>
        ) : null}
        {error ? <div className="callout bad" role="alert">{error}</div> : null}
      </div>
    </Modal>
  );
}
