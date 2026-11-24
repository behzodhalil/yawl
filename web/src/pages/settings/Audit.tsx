import { useId, useState } from "react";
import { dateTime, dayMonth, hhmm, parseDay } from "../../format";
import { Field, Icon, Loading, useLoad } from "../../ui";
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

const plain = (r: Row) => SENTENCES[r.text] ?? (r.text === r.kind ? r.text.replace(/[._]/g, " ") : r.text);

const KIND_ICONS: Record<string, string> = {
  deploy: "rocket", backup: "db", restore: "db", terminal: "code", env: "key", variable: "key",
  member: "users", owner: "users", firewall: "server",
};

const kindIcon = (kind: string) => KIND_ICONS[kind.split(".")[0]] ?? "log";

const RANGES = [["today", "Today", 0], ["7d", "Last 7 days", 6], ["30d", "Last 30 days", 29]] as const;

const day = (ms: number) => `${dayMonth(ms)} ${new Date(ms).getFullYear()}`;

function daysBack(n: number): string {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return day(d.getTime());
}

function heading(ms: number): string {
  const label = day(ms);
  return label === daysBack(0) ? "Today" : label === daysBack(1) ? "Yesterday" : label;
}

function byDay(rows: Row[]): { head: string; rows: Row[] }[] {
  const groups: { head: string; rows: Row[] }[] = [];
  for (const r of rows) {
    const head = heading(r.at);
    const last = groups.at(-1);
    if (last?.head === head) last.rows.push(r);
    else groups.push({ head, rows: [r] });
  }
  return groups;
}

const actorName = (actor: string) => (actor === "system" ? "System" : actor);

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

function DayField({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  const [left, setLeft] = useState(false);
  const message = useId();
  const shown = left && badDay(value);
  return (
    <Field label={label} hint={shown && <span id={message} className="bad-text">{BAD_DAY}</span>}>{(id) => (
      <input id={id} className="in" placeholder="23 Nov 2026" aria-invalid={shown} aria-describedby={shown ? message : undefined} value={value}
        onChange={(e) => (setLeft(false), onChange(e.target.value))} onBlur={() => setLeft(true)} />
    )}</Field>
  );
}

export function Audit() {
  const [who, setWho] = useState("");
  const [text, setText] = useState("");
  const [range, setRange] = useState("7d");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(0);
  const back = RANGES.find(([k]) => k === range)?.[2];
  const filter = back === undefined ? auditQuery(who, text, from, to) : auditQuery(who, text, daysBack(back), "");
  const list = useLoad<{ rows: Row[]; more: boolean }>(`/api/audit?${filter}${filter ? "&" : ""}page=${page}`);
  const reset = (f: () => void) => (f(), setPage(0));
  const actors = [...new Set([who, ...(list.data?.rows ?? []).map((r) => r.actor)].filter(Boolean))];
  return (
    <div className="card pad col">
      <h2>Audit log</h2>
      <p className="muted">Every change through the panel or the API, kept a year. Variable values are never recorded, only names.</p>
      <div className="auditbar">
        <label className="tsearch"><Icon name="search" /><input type="search" aria-label="Search events" placeholder="Search events…" value={text} onChange={(e) => reset(() => setText(e.target.value))} /></label>
        <select aria-label="Who" value={who} onChange={(e) => reset(() => setWho(e.target.value))}>
          <option value="">Anyone</option>
          {actors.map((a) => <option key={a} value={a}>{actorName(a)}</option>)}
        </select>
        <select aria-label="Range" value={range} onChange={(e) => reset(() => setRange(e.target.value))}>
          {RANGES.map(([k, label]) => <option key={k} value={k}>{label}</option>)}
          <option value="custom">Custom</option>
        </select>
        <a className="btn" href={`/api/audit.csv${filter ? "?" + filter : ""}`}>Export CSV</a>
      </div>
      {range === "custom" && (
        <div className="row">
          <DayField label="From" value={from} onChange={(v) => reset(() => setFrom(v))} />
          <DayField label="To" value={to} onChange={(v) => reset(() => setTo(v))} />
        </div>
      )}
      {!list.data ? <Loading what={list} /> : list.data.rows.length === 0 ? <p className="muted">Nothing matches.</p> : byDay(list.data.rows).map((g) => (
        <section key={g.head} className="auditday">
          <h3>{g.head}</h3>
          <ul>
            {g.rows.map((r, i) => (
              <li key={r.at + ":" + i}>
                <time className="mono dim" dateTime={new Date(r.at).toISOString()} title={dateTime(r.at)}>{hhmm(r.at)}</time>
                <Icon name={kindIcon(r.kind)} />
                <span><span className={r.actor === "system" ? "who dim" : "who"}>{actorName(r.actor)}</span> <span title={r.text}>{plain(r)}</span></span>
              </li>
            ))}
          </ul>
        </section>
      ))}
      <Pager page={page} more={list.data?.more} onPage={setPage} />
    </div>
  );
}
