import { useEffect, useRef, useState } from "react";
import { CircleAlert, CircleCheck, Loader2 } from "lucide-react";
import { api } from "../api/client";
import type { Job } from "../api/types";
import { useAppearance } from "../theme/appearance";
import { Button, Modal } from "./ui";

/** Follows a background job (updates, staging copies) until it finishes. */
export function useJob(onFinish?: (j: Job) => void) {
  const [job, setJob] = useState<Job | null>(null);
  const { saving } = useAppearance();
  const done = useRef(onFinish);
  done.current = onFinish;
  useEffect(() => {
    if (!job || job.status !== "running") return;
    // Data saver polls less often; the job runs the same either way.
    const t = setTimeout(async () => {
      try {
        const next = await api.job(job.id);
        setJob(next);
        if (next.status !== "running") done.current?.(next);
      } catch { /* keep the last state; the next tick retries */ }
    }, saving ? 3000 : 800);
    return () => clearTimeout(t);
  }, [job, saving]);
  return { job, start: setJob, clear: () => setJob(null) };
}

export function JobLog({ job }: { job: Job }) {
  const end = useRef<HTMLDivElement>(null);
  useEffect(() => { end.current?.scrollIntoView({ block: "nearest" }); }, [job.log.length]);
  return (
    <div className="stack" style={{ gap: 12 }}>
      <div className="row" style={{ gap: 8 }} role="status">
        {job.status === "running" ? <Loader2 size={18} className="spin" aria-hidden />
          : job.status === "done" ? <CircleCheck size={18} style={{ color: "var(--ok)" }} aria-hidden />
          : <CircleAlert size={18} style={{ color: "var(--bad)" }} aria-hidden />}
        <strong>{job.status === "running" ? "Working…" : job.status === "done" ? "Finished" : "Failed"}</strong>
      </div>
      <pre className="code" style={{ maxHeight: 280, overflow: "auto", margin: 0 }}>
        {job.log.length ? job.log.join("\n") : "Starting…"}
        <div ref={end} />
      </pre>
      {job.error ? <div className="callout bad"><CircleAlert size={18} />{job.error}</div> : null}
    </div>
  );
}

export function JobModal({ job, onClose }: { job: Job | null; onClose: () => void }) {
  return (
    <Modal open={!!job} onClose={onClose} title={job?.title ?? ""}
      description={job?.status === "running" ? "You can close this window. The job keeps running on the server." : undefined}
      footer={<Button variant={job?.status === "running" ? "secondary" : "primary"} onClick={onClose}>{job?.status === "running" ? "Run in background" : "Done"}</Button>}>
      {job ? <JobLog job={job} /> : null}
    </Modal>
  );
}
