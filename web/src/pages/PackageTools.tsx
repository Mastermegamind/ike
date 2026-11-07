// PECL and PEAR on the PHP extensions page, and the list of package managers
// deploys use.
import { useState } from "react";
import { Hammer, PackagePlus } from "lucide-react";
import { api } from "../api/client";
import type { Job } from "../api/types";
import { Badge, Button, Field, Skeleton } from "../components/ui";
import { enc, useAction, useGet } from "../components/kit5";

export interface PackageTool { name: string; language: string; installed: boolean; detects: string; note?: string }

/** PECLAndPEAR builds PECL extensions and installs PEAR libraries. */
export function PECLAndPEAR({ version, onJob }: { version: string; onJob: (j: Job) => void }) {
  const [pecl, setPecl] = useState("");
  const [pear, setPear] = useState("");
  const { busy, run } = useAction();
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>From PECL and PEAR</h2>
        <p className="small muted">For what isn't in the list above. PECL builds an extension from source for PHP {version}; Ubuntu's packages are better when they exist, because they get security updates. PEAR packages are PHP libraries every website can use.</p></div></div>
      <div className="panel-body grid-2">
        <form className="row" style={{ alignItems: "flex-end", flexWrap: "wrap" }} onSubmit={(e) => { e.preventDefault(); void run("pecl", async () => { onJob(await api.call<Job>("POST", `/php/${enc(version)}/pecl/${enc(pecl.trim())}`)); setPecl(""); }); }}>
          <Field label="PECL extension" hint="e.g. swoole, or swoole-5.1.4 for one version">{(id) => <input id={id} className="input mono" value={pecl} onChange={(e) => setPecl(e.target.value.toLowerCase())} placeholder="swoole" />}</Field>
          <Button type="submit" loading={busy === "pecl"} disabled={!pecl.trim()}><Hammer size={16} />Build for PHP {version}</Button>
        </form>
        <form className="row" style={{ alignItems: "flex-end", flexWrap: "wrap" }} onSubmit={(e) => { e.preventDefault(); void run("pear", async () => { onJob(await api.call<Job>("POST", `/php/pear?name=${enc(pear.trim())}`)); setPear(""); }); }}>
          <Field label="PEAR package" hint="e.g. Mail or Net_SMTP">{(id) => <input id={id} className="input mono" value={pear} onChange={(e) => setPear(e.target.value)} placeholder="Mail" />}</Field>
          <Button type="submit" loading={busy === "pear"} disabled={!pear.trim()}><PackagePlus size={16} />Install</Button>
        </form>
      </div>
    </section>
  );
}

/** PackageToolsPanel lists the package managers and what a deploy looks for. */
export function PackageToolsPanel() {
  const tools = useGet<PackageTool[]>("/package-tools");
  return (
    <section className="panel">
      <div className="panel-head"><div><h2>Package managers</h2>
        <p className="small muted">A Git deploy picks the right one from the files in the project, at the version the project pins. A build command set on the website replaces this. SSH users can run them too.</p></div></div>
      {!tools.data ? <div className="panel-body"><Skeleton h={120} /></div> : (
        <div className="table-wrap"><table className="responsive">
          <thead><tr><th>Tool</th><th>Language</th><th>Used when the project has</th><th /></tr></thead>
          <tbody>{tools.data.map((t) => (
            <tr key={t.name}>
              <td className="lead">{t.name}{t.note ? <div className="small muted">{t.note}</div> : null}</td>
              <td className="small">{t.language}</td>
              <td className="mono small">{t.detects || "—"}</td>
              <td>{t.installed ? <Badge tone="ok">Ready</Badge> : <Badge>Not installed</Badge>}</td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
    </section>
  );
}
