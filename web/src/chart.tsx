import { useId, useMemo, useState } from "react";
import { MON, niceTicks, two } from "./format";

export type Point = { at: number; values: (number | null)[] };
export type Series = { name: string };

const W = 640;
const H = 140;
const PAD = { l: 52, r: 12, t: 10, b: 22 };

export function when(ms: number, range: string): string {
  const d = new Date(ms);
  const hm = `${two(d.getHours())}:${two(d.getMinutes())}`;
  return range === "1h" || range === "24h" ? hm : `${d.getDate()} ${MON[d.getMonth()]} ${hm}`;
}

const HALF_DAY_MS = 12 * 3_600_000;

export function edgeLabel(ms: number, edge: "start" | "end", windowMs: number, stepMs: number, range: string, now = Date.now()): string {
  if (edge === "end" && now - ms <= 2 * stepMs) return "Now";
  if (windowMs <= HALF_DAY_MS) return when(ms, "1h");
  const d = new Date(ms);
  const hm = `${two(d.getHours())}:${two(d.getMinutes())}`;
  const day = new Date(now);
  if (d.toDateString() === day.toDateString()) return hm;
  day.setDate(day.getDate() - 1);
  if (d.toDateString() === day.toDateString()) return `Yesterday ${hm}`;
  return when(ms, range === "24h" || range === "1h" ? "7d" : range);
}

export function footer(title: string, points: Point[], format: (v: number) => string, range: string, stepMs = 60_000, now = Date.now()): string {
  const vals = points.filter((p) => p.values[0] !== null && p.values[0] !== undefined);
  if (vals.length === 0) return "";
  const last = vals[vals.length - 1].values[0]!;
  const peak = vals.reduce((a, b) => (b.values[0]! > a.values[0]! ? b : a));
  const newest = vals[vals.length - 1].at;
  const head = now - newest > 2 * stepMs ? `${title} last ${format(last)} at ${when(newest, range)}` : `${title} now ${format(last)}`;
  return `${head}, peak ${format(peak.values[0]!)} at ${when(peak.at, range)}`;
}

const tipAt = (pct: number) => (pct > 50 ? { right: `calc(${100 - pct}% + 10px)` } : { left: `calc(${pct}% + 10px)` });

export function LineChart({ title, unit, whole, limit, points, series, stepMs, format, range, compact, from, to, max: top }: {
  title: string; unit?: string; whole?: boolean;
  limit?: number;
  points: Point[]; series: Series[]; stepMs: number; format: (v: number) => string; range: string; compact?: boolean;
  from?: number; to?: number; max?: number;
}) {
  const [hover, setHover] = useState<number | null>(null);
  const fill = "viz-fill" + useId().replace(/:/g, "");
  const w = compact ? 120 : W;
  const h = compact ? 32 : H;
  const pad = compact ? { l: 0, r: 0, t: 4, b: 4 } : PAD;
  const geo = useMemo(() => {
    if (points.length === 0) return null;
    const x0 = from ?? points[0].at;
    const x1 = Math.max(to ?? points[points.length - 1].at, x0 + stepMs);
    let max = 0;
    for (const p of points) for (const v of p.values) if (v !== null && v > max) max = v;
    const ticks = niceTicks(top ?? Math.max(max, limit ?? 0), whole);
    max = ticks[ticks.length - 1];
    const x = (t: number) => pad.l + ((t - x0) / (x1 - x0)) * (w - pad.l - pad.r);
    const y = (v: number) => pad.t + (1 - v / max) * (h - pad.t - pad.b);
    const paths = series.map((_, si) => {
      let d = "";
      let prev: Point | null = null;
      for (const p of points) {
        const v = p.values[si];
        if (v === null || v === undefined) { prev = null; continue; }
        const gap = !prev || p.at - prev.at > stepMs * 1.5;
        d += `${gap ? "M" : "L"}${x(p.at).toFixed(1)},${y(v).toFixed(1)}`;
        prev = p;
      }
      return d;
    });
    const base = h - pad.b;
    const area = series.length !== 1 ? "" : paths[0].split("M").filter(Boolean).map((run) => {
      const pts = run.split("L");
      const first = pts[0].split(",")[0], last = pts[pts.length - 1].split(",")[0];
      return `M${run}L${last},${base}L${first},${base}Z`;
    }).join("");
    return { x, y, max, ticks, paths, area, x0, x1 };
  }, [points, series, stepMs, h, w, from, to, top, whole, limit, pad.l, pad.r, pad.t, pad.b]);

  if (!geo) return <div className="viz"><b className="viz-title">{title}</b><p className="muted">No samples in this range yet.</p></div>;
  const hp = hover === null ? null : points[hover];
  const onMove = (e: React.PointerEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - r.left) / r.width) * w;
    let best = 0;
    for (let i = 1; i < points.length; i++) if (Math.abs(geo.x(points[i].at) - px) < Math.abs(geo.x(points[best].at) - px)) best = i;
    setHover(best);
  };
  const now = (i: number) => {
    const p = [...points].reverse().find((q) => q.values[i] !== null && q.values[i] !== undefined);
    if (!p) return "";
    const v = format(p.values[i]!);
    return (to ?? Date.now()) - p.at > 2 * stepMs ? `last ${v} at ${when(p.at, range)}` : v;
  };
  return (
    <figure className={"viz" + (compact ? " compact" : "")}>
      {!compact && (
        <figcaption className="row" style={{ justifyContent: "space-between" }}>
          <span><b className="viz-title">{title}</b>{unit && <span className="muted viz-unit"> {unit}</span>}</span>
          <span className="row viz-now" style={{ gap: 12 }}>
            {series.length > 1 ? series.map((s, i) => (
              <span key={s.name} className="row" style={{ gap: 6 }}><i className={"viz-key s" + (i + 1)} />{s.name} {now(i)}</span>
            )) : now(0)}
          </span>
        </figcaption>
      )}
      <svg viewBox={`0 0 ${w} ${h}`} role="img" aria-label={footer(title, points, format, range, stepMs, to) || title} onPointerMove={compact ? undefined : onMove} onPointerLeave={() => setHover(null)} style={{ width: "100%", height: "auto", display: "block" }}>
        {!compact && geo.ticks.map((v) => (
          <g key={v}>
            <line x1={pad.l} x2={w - pad.r} y1={geo.y(v)} y2={geo.y(v)} className="viz-grid" />
            <text x={pad.l - 6} y={geo.y(v) + 4} textAnchor="end" className="viz-axis">{format(v)}</text>
          </g>
        ))}
        {!compact && (
          <>
            <text x={pad.l} y={h - 6} className="viz-axis">{edgeLabel(geo.x0, "start", geo.x1 - geo.x0, stepMs, range)}</text>
            <text x={w - pad.r} y={h - 6} textAnchor="end" className="viz-axis">{edgeLabel(geo.x1, "end", geo.x1 - geo.x0, stepMs, range)}</text>
          </>
        )}
        {limit ? <line x1={pad.l} x2={w - pad.r} y1={geo.y(limit)} y2={geo.y(limit)} className="viz-limit" /> : null}
        {geo.area && (
          <>
            <defs><linearGradient id={fill} x1="0" x2="0" y1="0" y2="1"><stop offset="0" className="viz-fill-top" /><stop offset="1" className="viz-fill-end" /></linearGradient></defs>
            <path d={geo.area} fill={`url(#${fill})`} className="viz-area" />
          </>
        )}
        {geo.paths.map((d, i) => <path key={i} d={d} data-series={i} className={"viz-line s" + (i + 1)} />)}
        {hp && (
          <g>
            <line x1={geo.x(hp.at)} x2={geo.x(hp.at)} y1={pad.t} y2={h - pad.b} className="viz-cross" />
            {hp.values.map((v, i) => v !== null && <circle key={i} cx={geo.x(hp.at)} cy={geo.y(v)} r={4} className={"viz-dot s" + (i + 1)} />)}
          </g>
        )}
      </svg>
      {hp && !compact && (
        <div className="viz-tip" style={tipAt((geo.x(hp.at) / w) * 100)}>
          {`${when(hp.at, range)} ${hp.values.map((v, i) => (series.length > 1 ? series[i].name + " " : "") + (v === null ? "no sample" : format(v))).join(" · ")}`}
        </div>
      )}
    </figure>
  );
}
