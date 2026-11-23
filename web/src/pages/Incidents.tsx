import { useState } from "react";
import { Shell } from "../Shell";
import { api } from "../api";
import { ago } from "../format";
import type { Incident, Off, ProjectView } from "../types";
import { Pager, TableBar, useTable } from "../table";
import { Callout, Empty, Link, Loading, StatusBadge, errText, useLoad } from "../ui";

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
    const stepText = step && !/^(Error|Caused by)$/.test(step[1]) ? step[1] : "";
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
      <p className="sub">Opened by the watcher when a check fails. Status pages have their own public incidents.</p>
      {!list.data ? <Loading what={list} /> : "off" in list.data ? (
        <Empty text="Incidents come from the watcher, and it is off."><Link to="/notifications/watcher" className="btn pri">Set up the watcher</Link></Empty>
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
    <div className="col">
      <h2>Open</h2>
      {open.length === 0 ? <p className="muted">Nothing is open.</p> : (
        <>
          {groups.map((g) => <OfflineCard key={g.server} g={g} />)}
          {rest.map((i) => <Row key={i.id} i={i} reload={reload} />)}
        </>
      )}
      <h2>Last 90 days</h2>
      {past.length === 0 ? <p className="muted">No incidents.</p> : (
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
    <div className="card pad col" style={{ gap: 6 }}>
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        <b>{g.server} is offline</b>
        <StatusBadge kind="bad">open</StatusBadge>
        <Link to={`/servers/${encodeURIComponent(g.server)}`}>Open {g.server}</Link>
      </div>
      {g.incidents.map((i) => <span key={i.id}>{i.label} <span className="muted">since {ago(i.opened_at)}</span></span>)}
    </div>
  );
}

function Row({ i, reload }: { i: Incident; reload: () => void }) {
  const [note, setNote] = useState(i.note);
  const [err, setErr] = useState("");
  const isOpen = i.closed_at === null;
  const summary = summarize(i.cause);
  return (
    <div className="card pad col" style={{ gap: 6 }}>
      <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
        <b>{i.label}</b>
        {isOpen ? <StatusBadge kind="bad">open</StatusBadge> : <StatusBadge kind="ok">resolved</StatusBadge>}
        <span className="muted">{isOpen ? `since ${ago(i.opened_at)}` : `${ago(i.opened_at)}`}</span>
        {!isOpen && <span className="muted">{`lasted ${minutes(i.closed_at! - i.opened_at)}`}</span>}
      </div>
      <span>{summary}</span>
      {summary !== i.cause && <details><summary>Show the full log</summary><pre className="mono" style={{ whiteSpace: "pre-wrap" }}>{i.cause}</pre></details>}
      {isOpen ? (
        <div className="row" style={{ gap: 8 }}>
          <input className="in" aria-label={`Note for ${i.label}`} placeholder="A note for your team" value={note} onChange={(e) => setNote(e.target.value)} />
          <button className="btn sm" onClick={async () => {
            setErr("");
            try { await api("POST", `/api/incidents/${i.id}/note`, { text: note }); reload(); } catch (e) { setErr(errText(e)); }
          }}>Save note</button>
        </div>
      ) : i.note && <span className="muted">{i.note}</span>}
      {err && <Callout tone="bad">{err}</Callout>}
    </div>
  );
}
