import { useState } from "react";
import { LineChart, type Point } from "../../chart";
import { bytes } from "../../format";
import type { ServiceView } from "../../types";
import { Loading, useLoad } from "../../ui";

type AppPoint = { at: number; cpu: number; mem: number; req: number; err5: number; p95_ms: number };
const RANGES: [string, string][] = [["1h", "1 hour"], ["24h", "24 hours"], ["7d", "7 days"], ["90d", "90 days"]];
const SPAN: Record<string, number> = { "1h": 3_600_000, "24h": 86_400_000, "7d": 7 * 86_400_000, "90d": 90 * 86_400_000 };
const pct = (v: number) => `${Math.round(v)}%`;

export function Metrics({ project, env, service }: { project: string; env: string; service: string }) {
  const base = `/api/services/${project}/${env}/${service}`;
  const [range, setRange] = useState("1h");
  const view = useLoad<ServiceView>(base);
  if (!view.data) return <Loading what={view} />;
  return (
    <div className="col" style={{ maxWidth: 820 }}>
      <div className="row" role="group" aria-label="Range">
        {RANGES.map(([k, label]) => (
          <button key={k} className={"btn sm" + (k === range ? " pri" : " ghost")} aria-pressed={k === range} onClick={() => setRange(k)}>{label}</button>
        ))}
      </div>
      <Graphs key={range} base={base} range={range} view={view.data} />
    </div>
  );
}

function Graphs({ base, range, view }: { base: string; range: string; view: ServiceView }) {
  const data = useLoad<{ step_s: number; points: AppPoint[] }>(`${base}/metrics?range=${range}`);
  if (!data.data) return <Loading what={data} />;
  const web = view.form.kind === "web";
  const step = data.data.step_s * 1000;
  const to = Date.now();
  const span = { from: to - SPAN[range], to };
  const pts = (f: (p: AppPoint) => (number | null)[]): Point[] => data.data!.points.map((p) => ({ at: p.at, values: f(p) }));
  const copies = Math.max(view.form.replicas ?? 1, 1);
  return (
    <>
      <LineChart title="CPU" unit="% of one core" whole limit={view.form.cpu * 100 * copies || undefined} points={pts((p) => [p.cpu])} series={[{ name: "CPU" }]} stepMs={step} format={pct} range={range} {...span} />
      <LineChart title="Memory" limit={view.memory_limit && view.memory_limit * copies} points={pts((p) => [p.mem])} series={[{ name: "Memory" }]} stepMs={step} format={bytes} range={range} {...span} />
      {web && (
        <>
          <LineChart title="Requests" whole points={pts((p) => [p.req, p.err5])} series={[{ name: "Requests" }, { name: "5xx" }]} stepMs={step}
            format={(v) => String(Math.round(v))} range={range} {...span} />
          <LineChart title="Response time p95" whole points={pts((p) => [p.p95_ms])} series={[{ name: "p95" }]} stepMs={step} format={(v) => `${Math.round(v)} ms`} range={range} {...span} />
        </>
      )}
      <p className="muted">{range === "1h" || range === "24h" ? "One point a minute." : "One point an hour: its busiest minute, requests summed."}</p>
    </>
  );
}
