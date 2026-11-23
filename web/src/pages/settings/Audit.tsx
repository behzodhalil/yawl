import { useState } from "react";
import { dateTime, parseDay } from "../../format";
import { Field, Loading, useLoad } from "../../ui";
import { Pager } from "../../table";

type Row = { at: number; actor: string; text: string; kind: string };

const SENTENCES: Record<string, string> = {
  "terminal.opened": "opened a terminal session",
  "deploy.queued": "started a deployment",
  "deploy.live": "completed a deployment",
  "deploy.canceled": "canceled a deployment",
  "backup.queued": "started a backup",
  "backup.ok": "completed a backup",
  "restore.queued": "started a backup restore",
  "spec.applied": "applied yawl.yaml",
  "env.revealed": "revealed a variable value",
  "member.joined": "joined the team",
  "panel.adopted": "adopted the services already running on the servers",
  "owner.claimed": "claimed ownership of the panel",
  "service.stopped": "stopped a service",
  "service.copied": "copied a service",
  "env.compared": "compared two environments",
  "env.move.started": "started an environment migration",
  "env.move.retried": "retried an environment migration",
  "firewall.ban": "blocked an IP address in the firewall",
};

export const plain = (text: string) => SENTENCES[text] ?? text;

const BAD_DAY = "Use a date like 23 Nov 2026";

function dayAfter(ms: number): number {
  const d = new Date(ms);
  d.setDate(d.getDate() + 1);
  return d.getTime();
}

export function auditQuery(who: string, text: string, from: string, to: string): string {
  const params = new URLSearchParams();
  const start = parseDay(from);
  const end = parseDay(to);
  if (who) params.set("actor", who);
  if (text) params.set("q", text);
  if (start !== null) params.set("from", String(start));
  if (end !== null) params.set("to", String(dayAfter(end)));
  return params.toString();
}

const badDay = (s: string) => s.trim() !== "" && parseDay(s) === null;

export function Audit() {
  const [who, setWho] = useState("");
  const [text, setText] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(0);
  const filter = auditQuery(who, text, from, to);
  const list = useLoad<{ rows: Row[]; more: boolean }>(`/api/audit?${filter}${filter ? "&" : ""}page=${page}`);
  const reset = (f: () => void) => (f(), setPage(0));
  return (
    <div className="card pad col">
      <h2>Audit log</h2>
      <p className="muted">Every change through the panel or the API, kept a year. Variable values are never recorded, only names.</p>
      <div className="row">
        <Field label="Who">{(id) => <input id={id} className="in" placeholder="owner or API token ci" value={who} onChange={(e) => reset(() => setWho(e.target.value))} />}</Field>
        <Field label="Text">{(id) => <input id={id} className="in" placeholder="variable, deployed…" value={text} onChange={(e) => reset(() => setText(e.target.value))} />}</Field>
        <Field label="From" hint={badDay(from) && <span className="bad-text">{BAD_DAY}</span>}>{(id) => <input id={id} className="in" placeholder="23 Nov 2026" aria-invalid={badDay(from)} value={from} onChange={(e) => reset(() => setFrom(e.target.value))} />}</Field>
        <Field label="To" hint={badDay(to) && <span className="bad-text">{BAD_DAY}</span>}>{(id) => <input id={id} className="in" placeholder="23 Nov 2026" aria-invalid={badDay(to)} value={to} onChange={(e) => reset(() => setTo(e.target.value))} />}</Field>
        <a className="btn" href={`/api/audit.csv${filter ? "?" + filter : ""}`}>Export CSV</a>
      </div>
      {!list.data ? <Loading what={list} /> : list.data.rows.length === 0 ? <p className="muted">Nothing matches.</p> : (
        <table className="tbl">
          <thead><tr><th>When</th><th>Who</th><th>What</th></tr></thead>
          <tbody>
            {list.data.rows.map((r, i) => (
              <tr key={r.at + ":" + i}><td className="mono dim">{dateTime(r.at)}</td><td>{r.actor}</td><td title={r.text}>{plain(r.text)}</td></tr>
            ))}
          </tbody>
        </table>
      )}
      <Pager page={page} more={list.data?.more} onPage={setPage} />
    </div>
  );
}
