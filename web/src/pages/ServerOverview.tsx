import type { ReactNode } from "react";
import { LineChart, type Point } from "../chart";
import { ago, bytes, forecastLine, secondsAgo } from "../format";
import { Shell } from "../Shell";
import type { ServerView } from "../types";
import { Link, Loading, POLL_MS, useLoad } from "../ui";
import { ServerState } from "./Servers";
import { ServerTabs } from "./ServerTabs";
import "../server.css";

type MachinePoint = { at: number; cpu: number; mem: number; mem_total: number; disk: number; disk_total: number; net_rx?: number | null; net_tx?: number | null };
type ServiceNow = { project: string; env: string; name: string; cpu: number; mem: number; cpu_limit: number; mem_limit: number };
type Machine = { step_s: number; points: MachinePoint[]; services?: ServiceNow[] };

const DAY = 86_400_000;
const pct = (v: number) => `${Math.round(v)}%`;
const share = (a: number, b: number) => (b ? (a * 100) / b : null);
export const rate = (bps: number) => {
  const b = bps * 8;
  return b >= 1e9 ? `${(b / 1e9).toFixed(1)} Gbps` : b >= 1e6 ? `${(b / 1e6).toFixed(1)} Mbps` : `${Math.round(b / 1e3)} kbps`;
};
const netScale = (peakBps: number): [number, string] => (peakBps * 8 >= 1e6 ? [1e6 / 8, "Mbps"] : [1e3 / 8, "kbps"]);
const NOT = <span className="dim">Not reported</span>;

export function ServerOverview({ name }: { name: string }) {
  const list = useLoad<ServerView[]>("/api/servers", POLL_MS);
  const m = useLoad<Machine>(`/api/servers/${encodeURIComponent(name)}/metrics?range=24h`);
  const s = list.data?.find((x) => x.name === name);
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name]} active="/servers">
      <div className="row sv-head"><h1>{name}</h1>{s && <ServerState s={s} />}</div>
      <ServerTabs name={name} active="" />
      {!list.data ? <Loading what={list} /> : !s ? <p className="muted">{`No server ${name}.`}</p> : (
        <div className="sv viz">
          <Info s={s} />
          {!m.data ? <div className="sv-wide"><Loading what={m} /></div> : <Graphs s={s} m={m.data} />}
        </div>
      )}
    </Shell>
  );
}

function Fact({ k, children }: { k: string; children: ReactNode }) {
  return <div><dt>{k}</dt><dd>{children}</dd></div>;
}

function Info({ s }: { s: ServerView }) {
  return (
    <section className="card sv-wide" aria-label="Info">
      <dl className="sv-facts">
        <Fact k="System">{s.os_name || NOT}{s.kernel && <div className="dim mono">{s.kernel}</div>}</Fact>
        <Fact k="CPU">{s.cores ? `${s.cores} ${s.cores === 1 ? "core" : "cores"}` : NOT}</Fact>
        <Fact k="Memory">{s.mem_total ? bytes(s.mem_total) : NOT}</Fact>
        <Fact k="Disk">{s.disk_total ? bytes(s.disk_total) : NOT}</Fact>
        <Fact k="Public IP"><span className="mono">{s.public_ip}</span></Fact>
        <Fact k="Private IP">
          {s.private_ip ? <span className="mono">{s.private_ip}</span> : <span className="dim">None</span>}
          {s.network && <div className="dim">{s.network}</div>}
          {s.peers?.map((p) => <div key={p.name} className="dim">{p.name}: {p.last_handshake ? `handshake ${secondsAgo(p.last_handshake)}` : "no handshake yet"}</div>)}
        </Fact>
        <Fact k="Agent"><span className="mono">{s.agent_version || "unknown"}</span><div className="dim">seen {ago(s.last_heartbeat)}</div></Fact>
        <Fact k="Booted">{s.booted_at ? ago(s.booted_at) : NOT}</Fact>
      </dl>
    </section>
  );
}

function Card({ title, now, sub, children }: { title: string; now: ReactNode; sub?: ReactNode; children: ReactNode }) {
  return (
    <section className="card sv-card" aria-label={title}>
      <div className="sv-k">{title}</div>
      <div className="sv-now">{now}</div>
      {sub && <div className="muted sv-sub">{sub}</div>}
      {children}
    </section>
  );
}

function Graphs({ s, m }: { s: ServerView; m: Machine }) {
  const p = m.points;
  const last = p[p.length - 1];
  const to = Date.now();
  const span = { stepMs: m.step_s * 1000, range: "24h", from: to - DAY, to };
  const pts = (f: (x: MachinePoint) => (number | null)[]): Point[] => p.map((x) => ({ at: x.at, values: f(x) }));
  const net = p.filter((x) => x.net_rx != null);
  const netLast = net[net.length - 1];
  const [unit, unitName] = netScale(Math.max(0, ...net.map((x) => Math.max(x.net_rx!, x.net_tx ?? 0))));
  const course = s.forecast && forecastLine(s.disk_used_pct, s.forecast);
  return (
    <>
      <Card title="CPU" now={last ? pct(last.cpu) : "None"} sub={s.cores ? `of ${s.cores} ${s.cores === 1 ? "core" : "cores"}` : undefined}>
        <LineChart title="CPU" whole max={100} points={pts((x) => [x.cpu])} series={[{ name: "CPU" }]} format={pct} {...span} />
      </Card>
      <Card title="Memory" now={last ? pct(share(last.mem, last.mem_total) ?? 0) : "None"} sub={last && `${bytes(last.mem)} of ${bytes(last.mem_total)}`}>
        <LineChart title="Memory" whole max={100} points={pts((x) => [share(x.mem, x.mem_total)])} series={[{ name: "Memory" }]} format={pct} {...span} />
      </Card>
      <Card title="Disk" now={last ? pct(share(last.disk, last.disk_total) ?? 0) : `${s.disk_used_pct}%`} sub={last && `${bytes(last.disk)} of ${bytes(last.disk_total)}`}>
        <LineChart title="Disk" whole max={100} points={pts((x) => [share(x.disk, x.disk_total)])} series={[{ name: "Disk" }]} format={pct} {...span} />
        {course && <p className={course[1] ? "word bad" : "muted"}>{course[0]}</p>}
      </Card>
      <Card title="Network" now={netLast ? <span className="sv-io"><span><i className="viz-key s1" />In {rate(netLast.net_rx!)}</span><span><i className="viz-key s2" />Out {rate(netLast.net_tx!)}</span></span> : NOT}>
        {net.length > 0 && <LineChart title="Network" whole points={pts((x) => [x.net_rx == null ? null : x.net_rx / unit, x.net_tx == null ? null : x.net_tx / unit])}
          series={[{ name: "In" }, { name: "Out" }]} format={(v) => `${Number.isInteger(v) ? v : v.toFixed(1)} ${unitName}`} {...span} />}
      </Card>
      <Services s={s} list={m.services ?? []} memTotal={last?.mem_total ?? s.mem_total ?? 0} />
    </>
  );
}

function Services({ s, list, memTotal }: { s: ServerView; list: ServiceNow[]; memTotal: number }) {
  return (
    <section className="card sv-wide" aria-label="Services">
      <div className="sv-k sv-pad">Services</div>
      {list.length === 0 ? <p className="muted sv-pad">No service is running here.</p> : (
        <div className="sv-list">
          {list.map((v) => {
            const cpuOf = v.cpu_limit ? v.cpu_limit * 100 : (s.cores || 1) * 100;
            const memOf = v.mem_limit || memTotal;
            const memShare = memOf ? (v.mem * 100) / memOf : 0;
            return (
              <div key={`${v.project}/${v.env}/${v.name}`} className="sv-svc">
                <div><Link to={`/p/${v.project}/${v.env}/s/${v.name}`}><b>{v.name}</b></Link><div className="dim">{v.project}/{v.env}</div></div>
                <Meter label="CPU" value={`${Math.round(v.cpu)}%`} share={(v.cpu * 100) / cpuOf} title="% of one core" />
                <Meter label="Memory" value={v.mem_limit ? `${bytes(v.mem)} of ${bytes(v.mem_limit)}` : bytes(v.mem)} share={memShare} warn={memShare > 90} />
              </div>
            );
          })}
        </div>
      )}
    </section>
  );
}

function Meter({ label, value, share, warn, title }: { label: string; value: string; share: number; warn?: boolean; title?: string }) {
  return (
    <div className="sv-meter" title={title}>
      <div className="row" data-u="between"><span className="dim">{label}</span><span className="num">{value}</span></div>
      <div className={"sv-bar" + (warn ? " warn" : "")} role="meter" aria-label={label} aria-valuenow={Math.round(share)} aria-valuemin={0} aria-valuemax={100}>
        <i style={{ width: `${Math.min(100, Math.max(0, share))}%` }} />
      </div>
    </div>
  );
}
