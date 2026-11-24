import type { ReactNode } from "react";
import { LineChart, type Point } from "../chart";
import { bytes, forecastLine, secondsAgo } from "../format";
import { Shell } from "../Shell";
import type { ServerView } from "../types";
import type { View as FirewallView } from "./Firewall";
import type { Status as OsStatus } from "./OSUpdates";
import { Ago, Link, Loading, POLL_MS, useLoad } from "../ui";
import { ServerState, attention } from "./Servers";
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
          <NeedsAttention s={s} />
          <Info s={s} />
          {!m.data ? <div className="sv-wide"><Loading what={m} /></div> : <Graphs s={s} m={m.data} />}
        </div>
      )}
    </Shell>
  );
}

const RESTART_PKGS_SHOWN = 3;

function restartText(pkgs: string[] = []) {
  const shown = pkgs.slice(0, RESTART_PKGS_SHOWN).join(", ");
  const more = pkgs.length > RESTART_PKGS_SHOWN ? ` +${pkgs.length - RESTART_PKGS_SHOWN} more` : "";
  return `Restart needed to finish an update${pkgs.length ? ` (${shown}${more})` : ""}`;
}

function NeedsAttention({ s }: { s: ServerView }) {
  const base = `/api/servers/${encodeURIComponent(s.name)}`;
  const online = !s.offline && !!s.last_heartbeat;
  const fw = useLoad<FirewallView>(online && s.firewall_off ? `${base}/firewall` : null);
  const os = useLoad<OsStatus>(online ? `${base}/os` : null);
  const tab = `/servers/${encodeURIComponent(s.name)}`;
  const lines: { key: string; text: string; to?: string; bad?: boolean }[] = attention(s).filter((c) => c.key !== "reboot").map((c) => {
    if (c.key === "firewall") {
      const open = fw.data?.status?.ports.filter((p) => p.exposure === "everyone").length;
      return { ...c, text: !open ? c.text : `${c.text}, ${open} public ${open === 1 ? "port" : "ports"} open`, to: `${tab}/firewall` };
    }
    if (c.key === "disk" && s.forecast) {
      return { ...c, text: `Disk full in about ${s.forecast.days} ${s.forecast.days === 1 ? "day" : "days"}, growing ${s.forecast.gb_per_day.toFixed(1)} GB a day`, to: `${tab}/disk` };
    }
    return c;
  });
  if (os.data && os.data.Updates > 0) {
    lines.push({ key: "os", text: `${os.data.Updates} ${os.data.Updates === 1 ? "update" : "updates"} waiting${os.data.Security > 0 ? `, ${os.data.Security} security` : ""}`, to: `${tab}/os` });
  }
  if (os.data?.Reboot) {
    lines.push({ key: "reboot", text: restartText(os.data.RebootPkgs), to: `${tab}/os` });
  }
  if (lines.length === 0) return null;
  return (
    <section className="card sv-wide" aria-label="Needs attention">
      <div className="sv-k sv-pad">Needs attention</div>
      <ul className="sv-attn">
        {lines.map((l) => {
          const tone = l.bad ? " word bad" : "";
          return <li key={l.key}>{l.to ? <Link to={l.to} className={"l" + tone}>{l.text}</Link> : <span className={tone.trim() || undefined}>{l.text}</span>}</li>;
        })}
      </ul>
    </section>
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
        <Fact k="Agent"><span className="mono">{s.agent_version || "unknown"}</span><div className="dim">seen <Ago ms={s.last_heartbeat} /></div></Fact>
        <Fact k="Booted">{s.booted_at ? <Ago ms={s.booted_at} /> : NOT}</Fact>
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
  const byCpu = [...list].sort((a, b) => b.cpu - a.cpu);
  const running = byCpu.filter((v) => v.mem !== 0);
  const stopped = byCpu.filter((v) => v.mem === 0);
  const row = (v: ServiceNow) => {
    const link = `/p/${v.project}/${v.env}/s/${v.name}`;
    const id = <div><b>{v.name}</b><div className="dim">{v.project}/{v.env}</div></div>;
    if (v.mem === 0) return <Link key={`${v.project}/${v.env}/${v.name}`} to={link} className="sv-svc">{id}<span className="dim">Stopped</span></Link>;
    const cpuOf = v.cpu_limit ? v.cpu_limit * 100 : (s.cores || 1) * 100;
    const memOf = v.mem_limit || memTotal;
    const memShare = memOf ? (v.mem * 100) / memOf : 0;
    return (
      <Link key={`${v.project}/${v.env}/${v.name}`} to={link} className="sv-svc">
        {id}
        <Meter label="CPU" value={`${v.cpu.toFixed(1)}%`} share={(v.cpu * 100) / cpuOf} title="% of one core" />
        <Meter label="Memory" value={v.mem_limit ? `${bytes(v.mem)} of ${bytes(v.mem_limit)}` : bytes(v.mem)} share={memShare} warn={memShare > 90} />
      </Link>
    );
  };
  return (
    <section className="card sv-wide" aria-label="Services">
      <div className="sv-k sv-pad">Services</div>
      {running.length === 0 && <p className="muted sv-pad">No service is running here.</p>}
      {running.length > 0 && <div className="sv-list">{running.map(row)}</div>}
      {stopped.length > 0 && (
        <details className="sv-stopped">
          <summary className="sv-pad">{stopped.length} stopped {stopped.length === 1 ? "container" : "containers"}</summary>
          <div className="sv-list">{stopped.map(row)}</div>
        </details>
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
