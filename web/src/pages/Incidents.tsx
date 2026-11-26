import { useState } from "react";
import { Button, Collapsible, LayerCard, linkVariants } from "@cloudflare/kumo";
import { Shell } from "../Shell";
import { api } from "../api";
import type { Incident, Off, ProjectView } from "../types";
import { Pager, TableBar, useTable } from "../table";
import { Ago, Callout, Empty, Link, Loading, StatusBadge, errText, notify, useLoad, ButtonLink, Input } from "../ui";
import { Cause, saysNoStorage } from "./service/Backups";

const minutes = (ms: number) => {
  const m = Math.floor(ms / 60_000);
  if (m < 1) return "less than a minute";
  if (m < 60) return m === 1 ? "1 minute" : `${m} minutes`;
  const h = Math.floor(m / 60);
  return `${h} h ${m % 60} min`;
};

const SUMMARY_MAX = 160;
const clip = (t: string) => (t.length > SUMMARY_MAX ? `${t.slice(0, SUMMARY_MAX - 1).trimEnd()}…` : t);

export function summarize(cause: string): string {
  const text = cause.replace(/ \(exit code 0\)/g, "").trim();
  const failed = /^(.*?) deploy failed: (.*)$/s.exec(text);
  if (failed) {
    const detail = /(?:^|\n|\s)((?:Error|Caused by):[^\n]*)/.exec(failed[2]);
    const step = /^([^:\n]+?): /.exec(failed[2]);
    const stepText = step && !/^(Error|Caused by)$/.test(step[1]) ? step[1].replace(/ failed$/, "") : "";
    if (detail) return clip(`${failed[1]} deploy failed${stepText ? ` at ${stepText}` : ""}: ${detail[1]}`);
  }
  return clip(text.split(/(?<=[.!?])\s/)[0]);
}

export type OfflineGroup = { server: string; incidents: Incident[] };

export function groupOffline(open: Incident[], projects: ProjectView[]): { groups: OfflineGroup[]; rest: Incident[] } {
  const serverOf = (i: Incident) => {
    if (!i.subject.startsWith("service:")) return "";
    const env = projects.flatMap((p) => p.envs.map((e) => ({ prefix: `${p.name}/${e.name}/`, e }))).find((x) => i.label.startsWith(x.prefix));
    return env?.e.server_offline ? env.e.server : "";
  };
  const byServer = new Map<string, Incident[]>();
  const rest: Incident[] = [];
  for (const i of open) {
    const server = serverOf(i);
    if (!server) rest.push(i);
    else byServer.set(server, [...(byServer.get(server) ?? []), i]);
  }
  return { groups: [...byServer].map(([server, incidents]) => ({ server, incidents })), rest };
}

export function Incidents() {
  const list = useLoad<Incident[] | Off>("/api/incidents?days=90");
  return (
    <Shell crumb={["Incidents"]} active="/incidents">
      <h1>Incidents</h1>
      <p className="text-kumo-subtle">Opened by the watcher when a check fails. Status pages have their own public incidents.</p>
      {!list.data ? <Loading what={list} /> : "off" in list.data ? (
        <Empty text="Incidents come from the watcher, and it is off."><ButtonLink to="/notifications/watcher" variant="primary">Set up the watcher</ButtonLink></Empty>
      ) : (
        <IncidentList items={list.data} reload={list.reload} />
      )}
    </Shell>
  );
}

function IncidentList({ items, reload }: { items: Incident[]; reload: () => void }) {
  const open = items.filter((i) => i.closed_at === null);
  const past = items.filter((i) => i.closed_at !== null);
  const projects = useLoad<ProjectView[]>("/api/projects");
  const { groups, rest } = groupOffline(open, projects.data ?? []);
  const tbl = useTable(past, { text: (i) => [i.label, i.cause, i.note].join(" "), sort: {} });
  return (
    <div className="flex flex-col gap-3">
      <h2>Open</h2>
      {open.length === 0 ? <p className="text-kumo-subtle">Nothing is open.</p> : (
        <>
          {groups.map((g) => <OfflineCard key={g.server} g={g} />)}
          {rest.map((i) => <Row key={i.id} i={i} reload={reload} />)}
        </>
      )}
      <h2>Last 90 days</h2>
      {past.length === 0 ? <p className="text-kumo-subtle">No incidents.</p> : (
        <>
          <TableBar t={tbl} noun="incidents" find="Search incidents" />
          {tbl.total === 0 ? <Empty text="Nothing matches the search." /> : tbl.rows.map((i) => <Row key={i.id} i={i} reload={reload} />)}
          <Pager page={tbl.page} pages={tbl.pages} onPage={tbl.setPage} />
        </>
      )}
    </div>
  );
}

function OfflineCard({ g }: { g: OfflineGroup }) {
  return (
    <LayerCard className="flex flex-col gap-1.5 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <b className="font-semibold">{g.server} is offline</b>
        <StatusBadge kind="bad">open</StatusBadge>
        <Link to={`/servers/${encodeURIComponent(g.server)}`} className={linkVariants({ variant: "inline" })}>Open {g.server}</Link>
      </div>
      {g.incidents.map((i) => <span key={i.id}>{i.label} <span className="text-kumo-subtle">since <Ago ms={i.opened_at} /></span></span>)}
    </LayerCard>
  );
}

function Row({ i, reload }: { i: Incident; reload: () => void }) {
  const [note, setNote] = useState(i.note);
  const [err, setErr] = useState("");
  const isOpen = i.closed_at === null;
  const summary = summarize(i.cause);
  return (
    <LayerCard className="flex flex-col gap-1.5 p-4">
      <div className="flex flex-wrap items-center gap-2">
        <b className="font-semibold">{i.label}</b>
        {isOpen ? <StatusBadge kind="bad">open</StatusBadge> : <StatusBadge kind="ok">resolved</StatusBadge>}
        <span className="text-kumo-subtle">{isOpen && "since "}<Ago ms={i.opened_at} /></span>
        {!isOpen && <span className="text-kumo-subtle">{`lasted ${minutes(i.closed_at! - i.opened_at)}`}</span>}
      </div>
      {saysNoStorage(i.cause) ? <Cause text={i.cause} muted={false} /> : <span>{summary}</span>}
      {summary !== i.cause && !saysNoStorage(i.cause) && <Collapsible><Collapsible.DefaultTrigger>Show the full log</Collapsible.DefaultTrigger><Collapsible.DefaultPanel><pre className="font-mono text-sm whitespace-pre-wrap">{i.cause}</pre></Collapsible.DefaultPanel></Collapsible>}
      {isOpen ? (
        <div className="flex flex-wrap items-center gap-2">
          <Input className="min-w-0 flex-1" aria-label={`Note for ${i.label}`} placeholder="A note for your team" value={note} onChange={(e) => setNote(e.target.value)} />
          <Button size="sm" onClick={async () => {
            setErr("");
            try { await api("POST", `/api/incidents/${i.id}/note`, { text: note }); notify("Note saved"); reload(); } catch (e) { setErr(errText(e)); }
          }}>Save note</Button>
        </div>
      ) : i.note && <span className="text-kumo-subtle">{i.note}</span>}
      {err && <Callout tone="bad">{err}</Callout>}
    </LayerCard>
  );
}
