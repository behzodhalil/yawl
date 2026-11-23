import { useState } from "react";
import { LineChart, type Point } from "../../chart";
import { RANGES, bytes, type Range } from "../../format";
import { Loading, useLoad } from "../../ui";

type Bucket = { at: number; views: number; visitors?: number; requests: number; errors: number; bytes: number };
type Top = { value: string; n: number };
type View = {
  range: string; visitors: number; views: number; requests: number; errors: number; bytes: number;
  s2: number; s3: number; s4: number; s5: number; series: Bucket[]; paths: Top[]; refs: Top[];
};
const SHOWN: Range[] = ["24h", "7d", "30d"];

function TopList({ title, rows }: { title: string; rows: Top[] }) {
  return (
    <div className="card scroll">
      <table className="tbl">
        <thead><tr><th>{title}</th><th className="num">Views</th></tr></thead>
        <tbody>
          {rows.length === 0 ? <tr><td className="dim" colSpan={2}>None yet.</td></tr>
            : rows.map((r) => <tr key={r.value}><td className="mono">{r.value}</td><td className="num">{r.n}</td></tr>)}
        </tbody>
      </table>
    </div>
  );
}

export function Analytics({ project, env, service }: { project: string; env: string; service: string }) {
  const [range, setRange] = useState<Range>("24h");
  const data = useLoad<View>(`/api/services/${project}/${env}/${service}/analytics?range=${range}`);
  const d = data.data;
  const to = Date.now();
  const step = range === "24h" ? 3_600_000 : 86_400_000;
  const pts: Point[] = d?.series.map((b) => ({ at: b.at, values: [b.views] })) ?? [];
  const share = (n: number) => (d && d.requests ? `${Math.round((100 * n) / d.requests)}%` : "0%");
  return (
    <div className="col" style={{ maxWidth: 820 }}>
      <div className="row" role="group" aria-label="Range">
        {SHOWN.map((k) => (
          <button key={k} className={"btn sm" + (k === range ? " pri" : " ghost")} aria-pressed={k === range} onClick={() => setRange(k)}>{k}</button>
        ))}
      </div>
      {!d ? <Loading what={data} /> : (
        <>
          <div className="card scroll">
            <table className="tbl">
              <thead><tr><th>{range === "24h" ? "Visitors today" : "Visitors"}</th><th>Page views</th><th>Requests</th><th>Bandwidth</th><th>5xx</th></tr></thead>
              <tbody><tr><td className="num">{d.visitors}</td><td className="num">{d.views}</td><td className="num">{d.requests}</td><td className="num">{bytes(d.bytes)}</td><td className="num">{share(d.errors)}</td></tr></tbody>
            </table>
          </div>
          <LineChart title="Page views" whole points={pts} series={[{ name: "Page views" }]} stepMs={step} format={(v) => String(Math.round(v))} range={range} from={to - RANGES[range]} to={to} />
          <div className="card scroll">
            <table className="tbl">
              <thead><tr><th>Status</th><th className="num">Requests</th></tr></thead>
              <tbody>
                {([["2xx", d.s2], ["3xx", d.s3], ["4xx", d.s4], ["5xx", d.s5]] as [string, number][]).map(([k, n]) => (
                  <tr key={k}><td>{k}</td><td className="num">{n}</td></tr>
                ))}
              </tbody>
            </table>
          </div>
          <TopList title="Pages" rows={d.paths} />
          <TopList title="Referrers" rows={d.refs} />
          <p className="muted">From the proxy's log. Visitors are each day's distinct visitors (UTC), added up over 7 and 30 days; bots are requests, not visitors.</p>
        </>
      )}
    </div>
  );
}
