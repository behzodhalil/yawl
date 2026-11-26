import { useState } from "react";
import { dateTime, dayMonth, hhmm, parseDay } from "../../format";
import { InputGroup, LayerCard, Select, buttonVariants, cn } from "@cloudflare/kumo";
import { Field, Icon, Loading, useLoad, Input } from "../../ui";
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

export const plain = (r: Row) => SENTENCES[r.text] ?? (r.text === r.kind ? r.text.replace(/[._]/g, " ") : r.text);

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

const ANYONE = "*";

export function actorName(actor: string): string {
  if (actor === "system") return "System";
  const token = /^API token (.+?)(?: \((.+)\))?$/.exec(actor);
  return token ? [token[1], token[2]].filter(Boolean).join(" · ") : actor;
}

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
  const shown = left && badDay(value);
  return (
    <Field label={label} hint={shown && <span className="text-kumo-danger">{BAD_DAY}</span>}>{(id) => (
      <Input id={id} placeholder="23 Nov 2026" aria-invalid={shown} value={value}
        onChange={(e) => (setLeft(false), onChange(e.target.value))} onBlur={() => setLeft(true)} />
    )}</Field>
  );
}

export function Audit() {
  const [who, setWho] = useState(ANYONE);
  const [text, setText] = useState("");
  const [range, setRange] = useState("7d");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [page, setPage] = useState(0);
  const back = RANGES.find(([k]) => k === range)?.[2];
  const filter = back === undefined ? auditQuery(who === ANYONE ? "" : who, text, from, to) : auditQuery(who === ANYONE ? "" : who, text, daysBack(back), "");
  const list = useLoad<{ rows: Row[]; more: boolean }>(`/api/audit?${filter}${filter ? "&" : ""}page=${page}`);
  const reset = (f: () => void) => (f(), setPage(0));
  const actors = [...new Set([who, ...(list.data?.rows ?? []).map((r) => r.actor)].filter((a) => a && a !== ANYONE))];
  return (
    <LayerCard className="flex flex-col gap-4 p-4">
      <p className="text-kumo-subtle">Every change through the panel or the API, kept a year. Variable values are never recorded, only names.</p>
      <div className="flex flex-wrap items-center gap-2 max-sm:[&>*]:flex-[1_1_100%]">
        <InputGroup className="flex-[1_1_220px] sm:max-w-[340px]">
          <InputGroup.Addon><Icon name="search" /></InputGroup.Addon>
          <InputGroup.Input type="search" aria-label="Search events" placeholder="Search events…" value={text} onChange={(e) => reset(() => setText(e.target.value))} />
        </InputGroup>
        <Select aria-label="Who" value={who} onValueChange={(v) => reset(() => setWho(String(v)))}
          items={{ [ANYONE]: "Anyone", ...Object.fromEntries(actors.map((a) => [a, actorName(a)])) }} />
        <Select aria-label="Range" value={range} onValueChange={(v) => reset(() => setRange(String(v)))}
          items={{ ...Object.fromEntries(RANGES), custom: "Custom" }} />
        <a className={cn(buttonVariants(), "sm:ml-auto")} href={`/api/audit.csv${filter ? "?" + filter : ""}`}>Export CSV</a>
      </div>
      {range === "custom" && (
        <div className="flex flex-wrap items-start gap-2.5">
          <DayField label="From" value={from} onChange={(v) => reset(() => setFrom(v))} />
          <DayField label="To" value={to} onChange={(v) => reset(() => setTo(v))} />
        </div>
      )}
      {!list.data ? <Loading what={list} /> : list.data.rows.length === 0 ? <p className="text-kumo-subtle">Nothing matches.</p> : byDay(list.data.rows).map((g) => (
        <section key={g.head}>
          <h3 className="border-b border-kumo-hairline py-2">{g.head}</h3>
          <ul className="m-0 list-none p-0">
            {g.rows.map((r, i) => (
              <li key={r.at + ":" + i} className="flex items-baseline gap-2.5 border-b border-kumo-hairline py-2 [overflow-wrap:anywhere] [&>.ico]:flex-none [&>.ico]:self-center [&>.ico]:text-kumo-subtle">
                <time className="font-mono text-sm text-kumo-subtle" dateTime={new Date(r.at).toISOString()} title={dateTime(r.at)}>{hhmm(r.at)}</time>
                <Icon name={kindIcon(r.kind)} />
                <span><span className={r.actor === "system" ? "font-semibold text-kumo-subtle" : "font-semibold"}>{actorName(r.actor)}</span> <span title={r.text}>{plain(r)}</span></span>
              </li>
            ))}
          </ul>
        </section>
      ))}
      <Pager page={page} more={list.data?.more} onPage={setPage} />
    </LayerCard>
  );
}
