import { Suspense, lazy, useMemo } from "react";
import { ChartPalette } from "@cloudflare/kumo";
import { MON, dayMonth, niceTicks, two } from "./format";
import { useDarkMode } from "./Shell";

export type Point = { at: number; values: (number | null)[] };
export type Series = { name: string };

export function when(ms: number, range: string): string {
  const d = new Date(ms);
  const hm = `${two(d.getHours())}:${two(d.getMinutes())}`;
  return range === "1h" || range === "24h" ? hm : `${d.getDate()} ${MON[d.getMonth()]} ${hm}`;
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

const Plot = lazy(() => import("./plot"));
const PLOT_HEIGHT = 180;
const GAP = Number.NaN;

function line(points: Point[], index: number, stepMs: number, from?: number, to?: number): [number, number][] {
  const out: [number, number][] = from !== undefined ? [[from, GAP]] : [];
  let prev: number | null = null;
  for (const p of points) {
    const v = p.values[index];
    if (v === null || v === undefined) continue;
    if (prev !== null && p.at - prev > stepMs * 1.5) out.push([prev + stepMs, GAP]);
    out.push([p.at, v]);
    prev = p.at;
  }
  if (to !== undefined) out.push([to, GAP]);
  return out;
}

export function gaps(points: Point[], stepMs: number, range: string): { timestamp: number; label: string; description: string; lineStyle: "dotted" }[] {
  const seen = points.filter((p) => p.values.some((v) => v !== null && v !== undefined));
  return seen.slice(1).flatMap((p, i) => p.at - seen[i].at > stepMs * 1.5
    ? [{ timestamp: seen[i].at, label: "No data", description: `No samples from ${when(seen[i].at, range)} to ${when(p.at, range)}`, lineStyle: "dotted" as const }]
    : []);
}

export function LineChart({ title, unit, whole, limit, points, series, stepMs, format, range, from, to, max }: {
  title: string; unit?: string; whole?: boolean;
  limit?: number;
  points: Point[]; series: Series[]; stepMs: number; format: (v: number) => string; range: string;
  from?: number; to?: number; max?: number;
}) {
  const dark = useDarkMode();
  const data = useMemo(() => series.map((s, i) => ({ name: s.name, color: ChartPalette.categorical(i, dark), data: line(points, i, stepMs, from, to) })), [series, points, stepMs, from, to, dark]);
  const markers = useMemo(() => gaps(points, stepMs, range), [points, stepMs, range]);
  const top = useMemo(() => {
    const peak = Math.max(limit ?? 0, max ?? 0, ...points.flatMap((p) => p.values.map((v) => v ?? 0)));
    return peak > 0 ? niceTicks(peak, whole).at(-1) : undefined;
  }, [points, limit, max, whole]);
  const thresholds = useMemo(() => [
    { value: 0, color: "transparent" },
    ...(limit ? [{ value: limit, label: `Limit ${format(limit)}`, color: ChartPalette.semantic("Attention", dark) }] : []),
    ...(top ? [{ value: top, color: "transparent" }] : []),
  ], [limit, top, format, dark]);
  if (points.length === 0) return <div className="viz"><b className="viz-title">{title}</b><p className="text-kumo-subtle">No samples in this range yet.</p></div>;
  const now = (i: number) => {
    const p = [...points].reverse().find((q) => q.values[i] !== null && q.values[i] !== undefined);
    if (!p) return "";
    const v = format(p.values[i]!);
    return (to ?? Date.now()) - p.at > 2 * stepMs ? `last ${v} at ${when(p.at, range)}` : v;
  };
  return (
    <figure className="viz flex flex-col gap-2">
      <figcaption className="flex flex-wrap items-center justify-between gap-2.5">
        <span><b className="viz-title">{title}</b>{unit && <span className="viz-unit text-kumo-subtle"> {unit}</span>}</span>
        <span className="viz-now flex flex-wrap items-center gap-3">
          {series.length > 1 ? series.map((s, i) => (
            <span key={s.name} className="flex items-center gap-1.5"><i className="inline-block h-0.5 w-3" style={{ background: data[i].color }} />{s.name} {now(i)}</span>
          )) : now(0)}
        </span>
      </figcaption>
      <Suspense fallback={<div style={{ height: PLOT_HEIGHT }} />}>
        <Plot isDarkMode={dark} data={data} height={PLOT_HEIGHT} gradient={series.length === 1}
          thresholds={thresholds} markers={markers} yAxisMinInterval={whole ? 1 : undefined} yAxisTickFormat={format} tooltipValueFormat={format} tooltipFollowCursor="x"
          xAxisTickFormat={(ms) => range === "30d" || range === "90d" ? dayMonth(ms) : when(ms, range)} tooltipTimestampFormat={(ms) => when(ms, range)}
          ariaDescription={footer(title, points, format, range, stepMs, to) || title} />
      </Suspense>
    </figure>
  );
}
