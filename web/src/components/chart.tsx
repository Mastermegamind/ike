/**
 * A small area chart for the last 24 hours of usage. The plot stretches to
 * any width while labels stay real text at a readable size. Every mark, tick
 * and label uses the same scale, and a dashed line shows the account's limit.
 */
export function UsageChart(props: {
  points: { t: string; v: number }[];
  unit: string;
  limit?: number;
  label: string;
  format?: (v: number) => string;
  /** An optional second line on the same scale, e.g. sent next to received. */
  second?: { points: { t: string; v: number }[]; label: string };
  /** Legend name for the main line when there are two. */
  name?: string;
  /** Upper end of the scale when the value has one, such as total memory. */
  max?: number;
}) {
  const W = 1000, H = 100;
  const fmt = props.format ?? ((v: number) => `${Math.round(v)}${props.unit}`);
  const peak = Math.max(1, ...props.points.map((p) => p.v), ...(props.second?.points ?? []).map((p) => p.v));
  const limit = props.limit && props.limit > 0 ? props.limit : 0;
  const top = props.max && props.max > 0 ? props.max : niceCeil(Math.max(peak * 1.15, limit && limit < peak * 3 ? limit * 1.05 : 0));
  const x = (i: number) => (i / Math.max(1, props.points.length - 1)) * W;
  const y = (v: number) => (1 - v / top) * H;
  const line = props.points.map((p, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(p.v).toFixed(2)}`).join("");
  const area = `${line}L${W},${H}L0,${H}Z`;
  const line2 = (props.second?.points ?? []).map((p, i, a) => `${i ? "L" : "M"}${((i / Math.max(1, a.length - 1)) * W).toFixed(1)},${y(p.v).toFixed(2)}`).join("");
  const last2 = props.second?.points[props.second.points.length - 1]?.v;
  const ticks = [top, top / 2, 0];
  const last = props.points[props.points.length - 1]?.v ?? 0;
  const showLimit = limit > 0 && limit <= top;
  const pct = (v: number) => `${(1 - v / top) * 100}%`;

  return (
    <figure style={{ margin: 0 }} aria-label={`${props.label}: now ${fmt(last)}${props.second && last2 !== undefined ? `, ${props.second.label} ${fmt(last2)}` : ""}, peak ${fmt(peak)}${limit ? `, limit ${fmt(limit)}` : ""}`} role="img">
      <div style={{ display: "grid", gridTemplateColumns: "auto 1fr", gap: 10 }} aria-hidden>
        <div style={{ position: "relative", height: 180, minWidth: 48 }}>
          {ticks.map((t) => (
            <span key={t} className="small muted" style={{ position: "absolute", right: 0, top: pct(t), transform: "translateY(-50%)", fontVariantNumeric: "tabular-nums", whiteSpace: "nowrap" }}>{fmt(t)}</span>
          ))}
        </div>
        <div style={{ position: "relative", height: 180, minWidth: 0 }}>
          <svg viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" width="100%" height="100%" style={{ display: "block", overflow: "visible" }}>
            <defs>
              <linearGradient id="ike-usage-fill" x1="0" x2="0" y1="0" y2="1">
                <stop offset="0" stopColor="var(--accent)" stopOpacity="0.28" />
                <stop offset="1" stopColor="var(--accent)" stopOpacity="0" />
              </linearGradient>
            </defs>
            {ticks.map((t) => <line key={t} x1={0} x2={W} y1={y(t)} y2={y(t)} stroke="var(--line)" strokeWidth="1" vectorEffect="non-scaling-stroke" />)}
            <path d={area} fill="url(#ike-usage-fill)" />
            <path d={line} fill="none" stroke="var(--accent)" strokeWidth="2" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
            {line2 ? <path d={line2} fill="none" stroke="var(--ink-2)" strokeWidth="1.75" strokeDasharray="5 4" strokeLinejoin="round" vectorEffect="non-scaling-stroke" /> : null}
            {showLimit ? <line x1={0} x2={W} y1={y(limit)} y2={y(limit)} stroke="var(--bad)" strokeWidth="1.5" strokeDasharray="6 5" vectorEffect="non-scaling-stroke" /> : null}
          </svg>
          {showLimit ? <span className="small" style={{ position: "absolute", right: 0, top: pct(limit), transform: "translateY(-120%)", color: "var(--bad)" }}>limit {fmt(limit)}</span> : null}
        </div>
        <span />
        <div className="small muted" style={{ display: "flex", justifyContent: "space-between" }}><span>24 h ago</span><span>12 h ago</span><span>now</span></div>
        {props.second ? <>
          <span />
          <div className="chart-legend small">
            <span><i style={{ background: "var(--accent)" }} />{props.name ?? props.label} <b>{fmt(last)}</b></span>
            <span><i className="dashed" />{props.second.label} <b>{last2 !== undefined ? fmt(last2) : ""}</b></span>
          </div>
        </> : null}
      </div>
    </figure>
  );
}

function niceCeil(v: number) {
  if (v <= 0) return 1;
  const mag = 10 ** Math.floor(Math.log10(v));
  const n = v / mag;
  const step = n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 5 ? 5 : 10;
  return step * mag;
}
