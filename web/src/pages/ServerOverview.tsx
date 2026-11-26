import type { ReactNode } from "react";
import { LineChart, type Point } from "../chart";
import { bytes, secondsAgo } from "../format";
import { Shell } from "../Shell";
import type { ServerView } from "../types";
import type { View as FirewallView } from "./Firewall";
import { rebootText, updatesText, type Status as OsStatus } from "./OSUpdates";
import { cn, InlineCopyText, LayerCard, linkVariants, Meter as KumoMeter } from "@cloudflare/kumo";
import { Ago, Link, Loading, POLL_MS, useLoad } from "../ui";
import { FILL, TEXT, attention, diskText, level } from "./Servers";
import { ServerTabs } from "./ServerTabs";

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
const NOT = <span className="text-base font-normal tracking-normal text-kumo-subtle">Not reported</span>;
const DANGER = "text-kumo-danger";
const SUBTLE = "text-sm font-normal text-kumo-subtle";
const CARD_HEAD = "px-4 pt-4 text-sm font-medium text-kumo-subtle";

export function ServerOverview({ name }: { name: string }) {
  const list = useLoad<ServerView[]>("/api/servers", POLL_MS);
  const m = useLoad<Machine>(`/api/servers/${encodeURIComponent(name)}/metrics?range=24h`);
  const s = list.data?.find((x) => x.name === name);
  const latest = m.data?.points.at(-1);
  return (
    <Shell crumb={[<Link to="/servers">Servers</Link>, name]} active="/servers">
      <ServerTabs name={name} active="" server={s} />
      {!list.data ? <Loading what={list} /> : !s ? <p className="text-kumo-subtle">{`No server ${name}.`}</p> : (
        <div className="viz grid grid-cols-1 gap-x-4 gap-y-5 min-[901px]:grid-cols-2">
          <NeedsAttention s={s} memPct={latest && share(latest.mem, latest.mem_total)} />
          <Info s={s} />
          {!m.data ? <div className="col-span-full"><Loading what={m} /></div> : <Graphs s={s} m={m.data} />}
        </div>
      )}
    </Shell>
  );
}

const MEM_ATTENTION_PCT = 85;

function NeedsAttention({ s, memPct }: { s: ServerView; memPct?: number | null }) {
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
    if (c.key === "disk") return { ...c, to: `${tab}/disk` };
    return c;
  });
  if (memPct != null && memPct >= MEM_ATTENTION_PCT) {
    lines.push({ key: "memory", text: `Memory ${Math.round(memPct)}% used`, bad: level(memPct) === "bad" });
  }
  if (os.data && os.data.Updates > 0) {
    lines.push({ key: "os", text: updatesText(os.data), to: `${tab}/os` });
  }
  if (os.data?.Reboot) {
    lines.push({ key: "reboot", text: rebootText(os.data.RebootPkgs), to: `${tab}/os` });
  }
  if (lines.length === 0) return null;
  return (
    <LayerCard render={<section />} className="col-span-full" aria-label="Needs attention">
      <div className={CARD_HEAD}>Needs attention</div>
      <ul className="m-0 grid list-none gap-1.5 px-4 pt-2.5 pb-4">
        {lines.map((l) => {
          const tone = l.bad ? DANGER : undefined;
          return <li key={l.key}>{l.to ? <Link to={l.to} className={cn(linkVariants({ variant: "inline" }), tone)}>{l.text}</Link> : <span className={tone}>{l.text}</span>}</li>;
        })}
      </ul>
    </LayerCard>
  );
}

function Fact({ k, children }: { k: string; children: ReactNode }) {
  return <div><dt className="mb-0.5 text-sm text-kumo-subtle">{k}</dt><dd className="m-0 font-medium [overflow-wrap:anywhere]">{children}</dd></div>;
}

function Info({ s }: { s: ServerView }) {
  return (
    <LayerCard render={<section />} className="col-span-full" aria-label="Info">
      <dl className="m-0 grid grid-cols-[repeat(auto-fill,minmax(170px,1fr))] gap-x-6 gap-y-[18px] p-[18px]">
        <Fact k="System">{s.os_name || NOT}{s.kernel && <div className={cn(SUBTLE, "font-mono")}>{s.kernel}</div>}</Fact>
        <Fact k="CPU">{s.cores ? `${s.cores} ${s.cores === 1 ? "core" : "cores"}` : NOT}</Fact>
        <Fact k="Memory">{s.mem_total ? bytes(s.mem_total) : NOT}</Fact>
        <Fact k="Disk">{s.disk_total ? bytes(s.disk_total) : NOT}</Fact>
        <Fact k="Public IP"><InlineCopyText variant="mono">{s.public_ip}</InlineCopyText></Fact>
        <Fact k="Private IP">
          {s.private_ip ? <InlineCopyText variant="mono">{s.private_ip}</InlineCopyText> : <span className={SUBTLE}>None</span>}
          {s.network && <div className={SUBTLE}>{s.network}</div>}
          {s.peers?.map((p) => <div key={p.name} className={SUBTLE}>{p.name}: {p.last_handshake ? `handshake ${secondsAgo(p.last_handshake)}` : "no handshake yet"}</div>)}
        </Fact>
        <Fact k="Agent"><span className="font-mono">{s.agent_version || "unknown"}</span><div className={SUBTLE}>seen <Ago ms={s.last_heartbeat} /></div></Fact>
        <Fact k="Booted">{s.booted_at ? <Ago ms={s.booted_at} /> : NOT}</Fact>
      </dl>
    </LayerCard>
  );
}

function Card({ title, now, sub, tone, children }: { title: string; now: ReactNode; sub?: ReactNode; tone?: string; children: ReactNode }) {
  return (
    <LayerCard render={<section />} className="flex min-w-0 flex-col p-4 [&_.viz]:mt-3 [&_figcaption]:hidden [&>p]:mt-2 [&>p]:text-sm" aria-label={title}>
      <div className="text-sm font-medium text-kumo-subtle">{title}</div>
      <div className={cn("mt-0.5 text-[28px] leading-tight font-semibold tracking-tight tabular-nums", tone)}>{now}</div>
      {sub && <div className="text-sm text-kumo-subtle">{sub}</div>}
      {children}
    </LayerCard>
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
  const growth = s.forecast?.growing && s.forecast.days !== null ? `, growing ${bytes(s.forecast.gb_per_day * 1e9)} a day` : "";
  const memNow = last ? share(last.mem, last.mem_total) ?? 0 : null;
  const diskNow = last ? share(last.disk, last.disk_total) ?? 0 : s.disk_used_pct;
  return (
    <>
      <Card title="CPU" now={last ? pct(last.cpu) : "None"} tone={last ? TEXT[level(last.cpu)] : undefined} sub={s.cores ? `of ${s.cores} ${s.cores === 1 ? "core" : "cores"}` : undefined}>
        <LineChart title="CPU" whole max={100} points={pts((x) => [x.cpu])} series={[{ name: "CPU" }]} format={pct} {...span} />
      </Card>
      <Card title="Memory" now={memNow === null ? "None" : pct(memNow)} tone={memNow === null ? undefined : TEXT[level(memNow)]} sub={last && `${bytes(last.mem)} of ${bytes(last.mem_total)}`}>
        <LineChart title="Memory" whole max={100} points={pts((x) => [share(x.mem, x.mem_total)])} series={[{ name: "Memory" }]} format={pct} {...span} />
      </Card>
      <Card title="Disk" now={pct(diskNow)} tone={TEXT[level(diskNow)]} sub={last && `${bytes(last.disk)} of ${bytes(last.disk_total)}`}>
        <LineChart title="Disk" whole max={100} points={pts((x) => [share(x.disk, x.disk_total)])} series={[{ name: "Disk" }]} format={pct} {...span} />
        {s.forecast && <p className={attention(s).find((c) => c.key === "disk")?.bad ? DANGER : "text-kumo-subtle"}>{diskText(s) + growth}</p>}
      </Card>
      <Card title="Network" now={netLast ? <span className="flex flex-wrap gap-5 text-xl"><span className="inline-flex items-center gap-2"><i className="viz-key s1" />In {rate(netLast.net_rx!)}</span><span className="inline-flex items-center gap-2"><i className="viz-key s2" />Out {rate(netLast.net_tx!)}</span></span> : NOT}>
        {net.length > 0 && <LineChart title="Network" whole points={pts((x) => [x.net_rx == null ? null : x.net_rx / unit, x.net_tx == null ? null : x.net_tx / unit])}
          series={[{ name: "In" }, { name: "Out" }]} format={(v) => `${Number.isInteger(v) ? v : v.toFixed(1)} ${unitName}`} {...span} />}
      </Card>
      <Services s={s} list={m.services ?? []} memTotal={last?.mem_total ?? s.mem_total ?? 0} />
    </>
  );
}

const SVC_ROW = "grid grid-cols-1 items-center gap-2.5 border-t border-kumo-hairline px-4 py-3 text-inherit no-underline first:border-t-0 hover:bg-kumo-tint focus-visible:bg-kumo-tint focus-visible:-outline-offset-2 min-[701px]:grid-cols-[minmax(160px,1.2fr)_minmax(0,1fr)_minmax(0,1fr)] min-[701px]:gap-7";

function Services({ s, list, memTotal }: { s: ServerView; list: ServiceNow[]; memTotal: number }) {
  const byCpu = [...list].sort((a, b) => b.cpu - a.cpu);
  const running = byCpu.filter((v) => v.mem !== 0);
  const stopped = byCpu.filter((v) => v.mem === 0);
  const row = (v: ServiceNow) => {
    const link = `/p/${v.project}/${v.env}/s/${v.name}`;
    const id = <div><b>{v.name}</b><div className="text-sm text-kumo-subtle">{v.project}/{v.env}</div></div>;
    if (v.mem === 0) return <Link key={`${v.project}/${v.env}/${v.name}`} to={link} className={SVC_ROW}>{id}<span className="text-sm text-kumo-subtle">Stopped</span></Link>;
    const cores = s.cores || 1;
    const cpuOf = (v.cpu_limit || cores) * 100;
    const cpuShare = (v.cpu * 100) / cpuOf;
    const cpuUnit = v.cpu_limit ? `${v.cpu_limit} vCPU` : `${cores} ${cores === 1 ? "core" : "cores"}`;
    const memOf = v.mem_limit || memTotal;
    const memShare = memOf ? (v.mem * 100) / memOf : 0;
    return (
      <Link key={`${v.project}/${v.env}/${v.name}`} to={link} className={SVC_ROW}>
        {id}
        <Usage label="CPU" value={`${cpuShare.toFixed(1)}% of ${cpuUnit}`} share={cpuShare} />
        <Usage label="Memory" value={v.mem_limit ? `${bytes(v.mem)} of ${bytes(v.mem_limit)}` : bytes(v.mem)} share={memShare} />
      </Link>
    );
  };
  return (
    <LayerCard render={<section />} className="col-span-full" aria-label="Services">
      <div className={CARD_HEAD}>Services</div>
      {running.length === 0 && <p className="px-4 py-4 text-kumo-subtle">No service is running here.</p>}
      {running.length > 0 && <div className="flex flex-col pt-2">{running.map(row)}</div>}
      {stopped.length > 0 && (
        <details className="border-t border-kumo-hairline">
          <summary className="cursor-pointer px-4 py-3">{stopped.length} stopped {stopped.length === 1 ? "container" : "containers"}</summary>
          <div className="flex flex-col">{stopped.map(row)}</div>
        </details>
      )}
    </LayerCard>
  );
}

function Usage({ label, value, share, title }: { label: string; value: string; share: number; title?: string }) {
  const l = level(share);
  return <KumoMeter label={label} customValue={value} value={Math.min(100, Math.max(0, share))} title={title} trackClassName="h-1" indicatorClassName={l === "ok" ? undefined : FILL[l]} />;
}
