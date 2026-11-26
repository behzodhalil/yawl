import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { DropdownMenu, InlineCopyText, LayerCard } from "@cloudflare/kumo";
import { api } from "../api";
import { serviceWord, spaced } from "../format";
import { DB_VERSIONS, isDb } from "../serviceForm";
import type { EnvView, ServerView, ServiceView, StatusRow } from "../types";
import { Icon, Link, MenuItem, NoSignal, StatusBadge, useLoad } from "../ui";
import { KIND } from "./Project";

type Edge = { from: string; to: string };
type Line = Edge & { d: string };
type Views = Record<string, ServiceView>;

export const ADD_KINDS: [string, string][] = [
  ["web", "Answers HTTP on a URL. Deploys blue/green behind a health check."],
  ["worker", "Runs without a port: queues, schedulers, bots."],
  ["cron", "Runs a command on a schedule, on the server, even when the panel is down."],
  ["postgres", `Postgres ${DB_VERSIONS.postgres[0]} with daily backups off this server.`],
  ["valkey", "A Redis compatible cache and queue, kept on disk."],
  ["mysql", "MySQL 8.4 with daily backups off this server."],
  ["mongodb", "MongoDB 8.0 as a replica set, with daily backups."],
  ["mariadb", "MariaDB 11.4 with daily backups off this server."],
  ["clickhouse", "ClickHouse 25.8 for analytics."],
];

const DOMAINS = "Domains";

const DAILY = /^(\d{1,2}) (\d{1,2}) \* \* \*$/;

export function scheduleText(cron: string): string {
  const daily = DAILY.exec(cron.trim());
  if (!daily) return cron;
  const [, minute, hour] = daily;
  return `Every day at ${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
}

function useViews(project: string, env: EnvView): Views {
  const names = env.services.map((s) => s.service).join(",");
  const [views, setViews] = useState<Views>({});
  useEffect(() => {
    let live = true;
    Promise.all(names.split(",").filter(Boolean).map((name) =>
      api<ServiceView>("GET", `/api/services/${project}/${env.name}/${name}`).then((v) => [name, v] as const, () => null),
    )).then((all) => live && setViews(Object.fromEntries(all.filter((x) => x !== null))));
    return () => { live = false; };
  }, [project, env.name, names]);
  return views;
}

function edgesOf(env: EnvView, views: Views): Edge[] {
  return env.services.flatMap((s) => {
    const v = views[s.service];
    if (!v) return [];
    if (isDb(s.kind)) return (v.used_by ?? []).filter((u) => u.env === env.name).map((u) => ({ from: u.service, to: s.service }));
    return v.domains?.length ? [{ from: DOMAINS, to: s.service }] : [];
  });
}

function properties(s: StatusRow, v: ServiceView | undefined): [string, string, ReactNode][] {
  if (!v?.form) return [];
  const f = v.form;
  const limits: [string, string, ReactNode][] = [
    ["layers", "Replicas", String(f.replicas ?? 1)],
    ["flash", "CPU", f.cpu ? `${f.cpu} vCPU` : "No limit"],
    ["box", "Memory", spaced(f.memory) || "No limit"],
  ];
  if (isDb(s.kind)) {
    return [
      ["db", "Engine", `${KIND[s.kind]?.[0] ?? s.kind}${f.version ? " " + f.version : ""}`],
      ["history", "Backups", f.backup_at ? `Daily at ${f.backup_at}` : "Off"],
      ...f.volumes.map((x): [string, string, ReactNode] => ["folder", "Volume", x.path]),
    ];
  }
  if (s.kind === "cron") {
    return [
      ["cron", "Schedule", [f.schedule && scheduleText(f.schedule), f.timezone].filter(Boolean).join(" · ") || "Not set"],
      ...(f.command.length ? [["code", "Command", f.command.join(" ")] as [string, string, ReactNode]] : []),
    ];
  }
  if (s.kind === "worker") return limits;
  const url = v.url && <InlineCopyText value={v.url} labels={{ copyAction: `Copy the address of ${s.service}`, copied: "Address copied" }}><span title={v.url}>{v.url.replace(/^https?:\/\//, "")}</span></InlineCopyText>;
  return [...limits, ["plug", "Port", String(f.port)], ...(url ? [["globe", "URL", url] as [string, string, ReactNode]] : [])];
}

function Rows({ rows }: { rows: [string, string, ReactNode][] }) {
  if (rows.length === 0) return null;
  return (
    <dl className="divide-y divide-kumo-hairline border-t border-kumo-hairline">
      {rows.map(([icon, label, value], i) => (
        <div key={label + i} className="flex items-center gap-2 px-3 py-1.5 text-sm">
          <dt className="flex items-center gap-2 text-kumo-subtle"><Icon name={icon} />{label}</dt>
          <dd className="ml-auto min-w-0 truncate text-right font-medium" title={typeof value === "string" ? value : undefined}>{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Node({ s, env, base, view }: { s: StatusRow; env: EnvView; base: string; view?: ServiceView }) {
  const [word, kind] = serviceWord(s.kind, s.state);
  return (
    <LayerCard data-node={s.service} className="flex flex-col p-0">
      <Link to={`${base}/s/${s.service}`} className="flex flex-wrap items-center gap-2 px-3 py-2.5 hover:bg-kumo-tint">
        <Icon name={KIND[s.kind]?.[1] ?? "box"} />
        <b className="font-semibold">{KIND[s.kind]?.[0] ?? s.kind}</b>
        <span className="text-kumo-subtle">{s.service}</span>
        <span className="ml-auto flex items-center gap-1.5">
          {env.server_offline ? <NoSignal lastSeen={env.server_last_seen} /> : <StatusBadge kind={kind}>{word}</StatusBadge>}
          {s.health === "down" && <StatusBadge kind="bad">Down</StatusBadge>}
        </span>
      </Link>
      <Rows rows={properties(s, view)} />
    </LayerCard>
  );
}

function Dot({ ok, word }: { ok: boolean; word: string }) {
  return <span className="flex items-center gap-1.5 text-kumo-subtle"><span className={"size-1.5 rounded-full " + (ok ? "bg-kumo-success" : "bg-kumo-warning")} aria-hidden="true" />{word}</span>;
}

function DomainsNode({ views }: { views: Views }) {
  const all = Object.values(views).flatMap((v) => v.domains ?? []);
  return (
    <LayerCard data-node={DOMAINS} className="flex flex-col p-0">
      <span className="flex items-center gap-2 px-3 py-2.5"><Icon name="globe" /><b className="font-semibold">Domains</b></span>
      {all.length === 0 ? <p className="border-t border-kumo-hairline px-3 py-1.5 text-sm text-kumo-subtle">None yet</p> : (
        <ul className="divide-y divide-kumo-hairline border-t border-kumo-hairline">
          {all.map((d) => {
            const ok = d.dns_state === "ok" && d.cert_state === "issued";
            return (
              <li key={d.host} className="flex items-center gap-2 px-3 py-1.5 text-sm">
                <span className="min-w-0 truncate font-mono" title={d.host}>{d.host}</span>
                <span className="ml-auto"><Dot ok={ok} word={ok ? "Ready" : d.dns_state !== "ok" ? "DNS pending" : "Certificate pending"} /></span>
              </li>
            );
          })}
        </ul>
      )}
    </LayerCard>
  );
}

function AddService({ base }: { base: string }) {
  return (
    <DropdownMenu>
      <DropdownMenu.Trigger render={
        <button type="button" className="flex w-full flex-col items-start gap-0.5 rounded-sm border border-dashed border-kumo-line px-3 py-2.5 text-left hover:bg-kumo-tint focus-visible:ring-2 focus-visible:ring-kumo-brand focus-visible:outline-hidden">
          <span className="flex items-center gap-2 font-semibold"><Icon name="plus" />Add service</span>
          <span className="text-sm text-kumo-subtle">Web, worker, cron, databases</span>
        </button>
      } />
      <DropdownMenu.Content align="start">
        {ADD_KINDS.map(([k, text]) => (
          <MenuItem key={k} to={`${base}/new/${k}`} icon={KIND[k]?.[1]} description={text}>{KIND[k]?.[0] ?? k}</MenuItem>
        ))}
      </DropdownMenu.Content>
    </DropdownMenu>
  );
}

function Group({ label, className, children }: { label: string; className?: string; children: ReactNode }) {
  return (
    <section aria-label={label} className={"flex flex-col gap-3 rounded-sm bg-kumo-recessed/60 p-3 ring ring-kumo-hairline " + (className ?? "")}>
      <h3 className="px-1 text-sm font-medium text-kumo-subtle">{label}</h3>
      {children}
    </section>
  );
}

export function Canvas({ project, env, base }: { project: string; env: EnvView; base: string }) {
  const views = useViews(project, env);
  const servers = useLoad<ServerView[]>("/api/servers").data;
  const ip = Array.isArray(servers) ? servers.find((x) => x.name === env.server)?.public_ip : undefined;
  const edges = edgesOf(env, views);
  const edgeKey = edges.map((e) => e.from + ">" + e.to).join(",");
  const box = useRef<HTMLDivElement>(null);
  const [lines, setLines] = useState<Line[]>([]);
  useLayoutEffect(() => {
    const el = box.current;
    if (!el) return;
    const draw = () => {
      const origin = el.getBoundingClientRect();
      const rect = (name: string) => el.querySelector(`[data-node="${CSS.escape(name)}"]`)?.getBoundingClientRect();
      setLines(edges.flatMap((e) => {
        const a = rect(e.from), b = rect(e.to);
        if (!a || !b) return [];
        const x1 = a.right - origin.left, y1 = a.top + Math.min(a.height / 2, 20) - origin.top;
        const x2 = b.left - origin.left, y2 = b.top + Math.min(b.height / 2, 20) - origin.top;
        const mid = (x1 + x2) / 2;
        return [{ ...e, d: `M${x1},${y1} H${mid} V${y2} H${x2}` }];
      }));
    };
    draw();
    const watch = new ResizeObserver(draw);
    watch.observe(el);
    return () => watch.disconnect();
  }, [edgeKey]);
  const apps = env.services.filter((s) => !isDb(s.kind));
  const dbs = env.services.filter((s) => isDb(s.kind));
  return (
    <div ref={box} aria-label={`Services of ${env.name}`}
      className="relative flex flex-col gap-6 rounded-sm bg-[radial-gradient(var(--color-kumo-line)_1px,transparent_1px)] bg-size-[16px_16px] p-4 ring ring-kumo-hairline md:flex-row md:items-start md:gap-12">
      <svg className="pointer-events-none absolute inset-0 hidden h-full w-full md:block" aria-hidden="true">
        {lines.map((l) => <path key={l.from + ">" + l.to} data-edge={`${l.from}>${l.to}`} d={l.d} fill="none" stroke="var(--color-kumo-line)" strokeWidth={1} />)}
      </svg>
      <Group label="Network" className="md:w-64 md:shrink-0"><DomainsNode views={views} /></Group>
      <Group label={ip ? `${env.server} · ${ip}` : env.server} className="min-w-0 flex-1">
        <div className="grid gap-4 md:grid-cols-2 md:gap-x-12">
          <div className="flex flex-col gap-3">
            {apps.map((s) => <Node key={s.service} s={s} env={env} base={base} view={views[s.service]} />)}
            <AddService base={base} />
          </div>
          <div className="flex flex-col gap-3">
            {dbs.length > 0 && <span className="px-1 text-sm font-medium text-kumo-subtle">Data</span>}
            {dbs.map((s) => <Node key={s.service} s={s} env={env} base={base} view={views[s.service]} />)}
          </div>
        </div>
      </Group>
    </div>
  );
}
