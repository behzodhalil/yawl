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

const PAGE_SHARE = 0.02;
const CAPTION: Record<Range, string> = { "1h": "Last hour", "24h": "Last 24 hours", "7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days" };

export function servesPages(d: { views: number; requests: number }): boolean {
  return d.views >= d.requests * PAGE_SHARE;
}

export function share(n: number, total: number): string {
  if (!total || !n) return "0%";
  const tenths = Math.round((1000 * n) / total) / 10;
  return tenths < 10 ? `${tenths.toFixed(1)}%` : `${Math.round(tenths)}%`;
}

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
  const api = d ? !servesPages(d) : false;
  const pts: Point[] = d?.series.map((b) => ({ at: b.at, values: api ? [b.requests, b.errors] : [b.views] })) ?? [];
  const tiles: [string, string][] = d ? [
    ...(api ? [] : [["Visitors", String(d.visitors)], ["Page views", String(d.views)]] as [string, string][]),
    ["Requests", String(d.requests)],
    ["4xx", share(d.s4, d.requests)],
    ["5xx", share(d.s5, d.requests)],
    ["Bandwidth", bytes(d.bytes)],
  ] : [];
  return (
    <div className="col">
      <div className="row" role="group" aria-label="Range">
        {SHOWN.map((k) => (
          <button key={k} className={"btn sm" + (k === range ? " pri" : " ghost")} aria-pressed={k === range} onClick={() => setRange(k)}>{k}</button>
        ))}
      </div>
      {!d ? <Loading what={data} /> : (
        <>
          <p className="muted">{CAPTION[range]}</p>
          <div className="row">
            {tiles.map(([label, value]) => (
              <div key={label} className="card tile">
                <div className="muted">{label}</div>
                <div className="mono">{value}</div>
              </div>
            ))}
          </div>
          <LineChart title={`${api ? "Requests" : "Page views"} per ${range === "24h" ? "hour" : "day"}`} whole points={pts} series={api ? [{ name: "Requests" }, { name: "5xx" }] : [{ name: "Page views" }]} stepMs={step} format={(v) => String(Math.round(v))} range={range} from={to - RANGES[range]} to={to} />
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
          {!api && <TopList title="Pages" rows={d.paths} />}
          {!api && <TopList title="Referrers" rows={d.refs} />}
          <p className="muted">From the proxy's log.{!api && " Visitors are each day's distinct visitors (UTC), added up over 7 and 30 days; bots are requests, not visitors."}</p>
        </>
      )}
    </div>
  );
}
