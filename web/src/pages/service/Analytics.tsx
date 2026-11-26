import { Suspense, lazy, useState } from "react";
import { LineChart, when, type Point } from "../../chart";
import { RANGES, bytes, count, type Range } from "../../format";
import { Button, ChartPalette, LayerCard, Table, Tabs } from "@cloudflare/kumo";
import { api } from "../../api";
import { useDarkMode } from "../../Shell";
import { Card, Confirm, Loading, StatusBadge, notify, useLoad } from "../../ui";

type Bucket = { at: number; views: number; visitors?: number; requests: number; errors: number; bytes: number };
type Top = { value: string; n: number };
type Who = { at: number; humans: number; bots: number; ai: number };
type Crawler = { name: string; company: string; n: number; top: string; blocked: boolean };
type View = {
  range: string; visitors: number; views: number; requests: number; errors: number; bytes: number;
  s2: number; s3: number; s4: number; s5: number; series: Bucket[]; paths: Top[]; refs: Top[];
  who?: Who[]; crawlers?: Crawler[]; bots?: Top[];
};
const SENDERS = [["Humans", "humans"], ["Search and other bots", "bots"], ["AI crawlers", "ai"]] as const;
const SHOWN: Range[] = ["24h", "7d", "30d"];

const PAGE_SHARE = 0.02;
const WHO_HEIGHT = 180;
const Plot = lazy(() => import("../../plot"));
const CAPTION: Record<Range, string> = { "1h": "Last hour", "24h": "Last 24 hours", "7d": "Last 7 days", "30d": "Last 30 days", "90d": "Last 90 days" };

export function servesPages(d: { views: number; requests: number }): boolean {
  return d.views >= d.requests * PAGE_SHARE;
}

export function share(n: number, total: number): string {
  if (!total || !n) return "0%";
  const tenths = Math.round((1000 * n) / total) / 10;
  return tenths < 10 ? `${tenths.toFixed(1)}%` : `${Math.round(tenths)}%`;
}

function TopList({ title, unit = "Views", rows }: { title: string; unit?: string; rows: Top[] }) {
  return (
    <LayerCard className="overflow-x-auto p-0">
      <Table>
        <Table.Header><Table.Row><Table.Head>{title}</Table.Head><Table.Head>{unit}</Table.Head></Table.Row></Table.Header>
        <Table.Body>
          {rows.length === 0 ? <Table.Row><Table.Cell className="text-kumo-subtle" colSpan={2}>None yet.</Table.Cell></Table.Row>
            : rows.map((r) => <Table.Row key={r.value}><Table.Cell className="font-mono text-sm">{r.value}</Table.Cell><Table.Cell className="font-mono text-sm tabular-nums">{count(r.n)}</Table.Cell></Table.Row>)}
        </Table.Body>
      </Table>
    </LayerCard>
  );
}

const total = (w: Who) => w.humans + w.bots + w.ai;
const STATUS = [["2xx", "s2", "bg-kumo-success"], ["3xx", "s3", "bg-kumo-fill"], ["4xx", "s4", "bg-kumo-warning"], ["5xx", "s5", "bg-kumo-danger"]] as const;

function StatusBar({ d }: { d: View }) {
  const sum = d.s2 + d.s3 + d.s4 + d.s5;
  return (
    <LayerCard className="flex flex-col gap-3 px-4 py-3">
      <b className="text-sm font-medium">Status codes</b>
      <div role="img" aria-label={STATUS.map(([k, f]) => `${k} ${count(d[f])}`).join(", ")} className="flex h-2 overflow-hidden rounded-sm bg-kumo-recessed">
        {sum > 0 && STATUS.map(([k, f, bg]) => <div key={k} className={bg} style={{ flex: `${d[f]} 1 0` }} />)}
      </div>
      <p className="m-0 flex flex-wrap gap-x-6 gap-y-1 text-sm">
        {STATUS.map(([k, f, bg]) => (
          <span key={k} className="flex items-center gap-1.5">
            <i className={`inline-block size-2 ${bg}`} />
            <span className="text-kumo-subtle">{k}</span>
            <span className="font-mono tabular-nums">{count(d[f])}</span>
          </span>
        ))}
      </p>
    </LayerCard>
  );
}

function WhoChart({ rows, range }: { rows: Who[]; range: Range }) {
  const dark = useDarkMode();
  const sum = (k: "humans" | "bots" | "ai") => rows.reduce((a, r) => a + r[k], 0);
  const all = rows.reduce((a, r) => a + total(r), 0);
  return (
    <figure className="viz flex flex-col gap-2">
      <figcaption className="flex flex-wrap items-center justify-between gap-2.5">
        <b className="viz-title">Requests by who sent them</b>
        <dl className="viz-now m-0 flex flex-wrap items-center gap-x-5 gap-y-1">
          {SENDERS.map(([name, k], i) => (
            <div key={name} className="flex items-center gap-1.5">
              <i className="inline-block size-2" style={{ background: ChartPalette.categorical(i, dark) }} />
              <dt>{name}</dt>
              <dd className="m-0 font-mono tabular-nums">{count(sum(k))}</dd>
              <dd className="m-0 text-kumo-subtle">{share(sum(k), all)}</dd>
            </div>
          ))}
        </dl>
      </figcaption>
      <div role="img" aria-label={SENDERS.map(([name, k]) => `${name} ${sum(k)}`).join(", ")}>
        <Suspense fallback={<div style={{ height: WHO_HEIGHT }} />}>
          <Plot type="bar" isDarkMode={dark} height={WHO_HEIGHT} yAxisMinInterval={1} yAxisTickFormat={count} tooltipValueFormat={count}
            xAxisTickFormat={(ms) => when(ms, range)} tooltipTimestampFormat={(ms) => when(ms, range)}
            data={SENDERS.map(([name, k], i) => ({ name, color: ChartPalette.categorical(i, dark), data: rows.map((r): [number, number] => [r.at, r[k]]) }))} />
        </Suspense>
      </div>
    </figure>
  );
}

function Crawlers({ path, rows, onChange }: { path: string; rows: Crawler[]; onChange: () => void }) {
  const [asked, setAsked] = useState<Crawler | null>(null);
  return (
    <Card title="AI crawlers">
      <p className="text-sm text-kumo-subtle">Told apart by user agent, checked against each company's published IP ranges.</p>
      <div className="overflow-x-auto">
        <Table>
          <Table.Header><Table.Row><Table.Head>Crawler</Table.Head><Table.Head>Requests</Table.Head><Table.Head>Reads most</Table.Head><Table.Head>Access</Table.Head><Table.Head><span className="sr-only">Action</span></Table.Head></Table.Row></Table.Header>
          <Table.Body>
            {rows.map((c) => (
              <Table.Row key={c.name}>
                <Table.Cell><b className="font-medium">{c.name}</b><span className="block text-xs text-kumo-subtle">{c.company}</span></Table.Cell>
                <Table.Cell className="font-mono text-sm tabular-nums">{count(c.n)}</Table.Cell>
                <Table.Cell className="font-mono text-sm">{c.top}</Table.Cell>
                <Table.Cell>{c.blocked ? <StatusBadge kind="bad">Blocked · 403</StatusBadge> : <StatusBadge kind="ok">Allowed</StatusBadge>}</Table.Cell>
                <Table.Cell className="text-right"><Button size="sm" variant="secondary" onClick={() => setAsked(c)}>{c.blocked ? "Allow" : "Block"}</Button></Table.Cell>
              </Table.Row>
            ))}
          </Table.Body>
        </Table>
      </div>
      {asked && (
        <Confirm title={`${asked.blocked ? "Allow" : "Block"} ${asked.name}?`} yes={asked.blocked ? "Allow" : "Block"} onClose={() => setAsked(null)} onYes={async () => {
          await api("PUT", `${path}/crawlers/${encodeURIComponent(asked.name)}`, { blocked: !asked.blocked });
          notify(`${asked.name} ${asked.blocked ? "allowed" : "blocked"}`);
          onChange();
        }}>
          <p>{asked.blocked ? `${asked.name} can read this service again.` : `The proxy answers ${asked.name} with 403 on every domain of this service. Its requests still count here.`}</p>
        </Confirm>
      )}
    </Card>
  );
}

export function Analytics({ project, env, service }: { project: string; env: string; service: string }) {
  const [range, setRange] = useState<Range>("24h");
  const base = `/api/services/${project}/${env}/${service}`;
  const data = useLoad<View>(`${base}/analytics?range=${range}`);
  const d = data.data;
  const to = Date.now();
  const step = range === "24h" ? 3_600_000 : 86_400_000;
  const api = d ? !servesPages(d) : false;
  const pts: Point[] = d?.series.map((b) => ({ at: b.at, values: api ? [b.requests, b.errors] : [b.views] })) ?? [];
  const tiles: [string, string][] = d ? [
    ...(api ? [] : [["Visitors", count(d.visitors)], ["Page views", count(d.views)]] as [string, string][]),
    ["Requests", count(d.requests)],
    ["4xx", share(d.s4, d.requests)],
    ["5xx", share(d.s5, d.requests)],
    ["Bandwidth", bytes(d.bytes)],
  ] : [];
  return (
    <div className="flex flex-col gap-3">
      <div role="group" aria-label="Range" className="self-start">
        <Tabs variant="segmented" size="sm" tabs={SHOWN.map((k) => ({ value: k, label: k }))} value={range} onValueChange={(v) => setRange(v as Range)} />
      </div>
      {!d ? <Loading what={data} /> : (
        <>
          <p className="text-kumo-subtle">{CAPTION[range]}</p>
          <div className="grid grid-cols-[repeat(auto-fit,minmax(9rem,1fr))] gap-2.5">
            {tiles.map(([label, value]) => (
              <LayerCard key={label} render={<dl />} className="m-0 px-4 py-3">
                <dt className="text-xs text-kumo-subtle">{label}</dt>
                <dd className="m-0 font-mono text-[22px]">{value}</dd>
              </LayerCard>
            ))}
          </div>
          <LineChart title={`${api ? "Requests" : "Page views"} per ${range === "24h" ? "hour" : "day"}`} whole points={pts} series={api ? [{ name: "Requests" }, { name: "5xx" }] : [{ name: "Page views" }]} stepMs={step} format={(v) => String(Math.round(v))} range={range} from={to - RANGES[range]} to={to} />
          <StatusBar d={d} />
          {d.who && <WhoChart rows={d.who} range={range} />}
          {d.crawlers && <Crawlers path={base} rows={d.crawlers} onChange={data.reload} />}
          {d.bots && <TopList title="Search and other bots" unit="Requests" rows={d.bots} />}
          {!api && <TopList title="Pages" rows={d.paths} />}
          {!api && <TopList title="Referrers" rows={d.refs} />}
          <p className="text-kumo-subtle">From the proxy's log.{!api && " Visitors are each day's distinct visitors (UTC), added up over 7 and 30 days; bots are requests, not visitors."}</p>
        </>
      )}
    </div>
  );
}
