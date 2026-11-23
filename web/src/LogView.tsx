import { useMemo, useState, type ReactNode } from "react";
import { Icon } from "./ui";

export type Row = { time?: string; text: string; bad?: boolean; stderr?: boolean };

const URLS = /https?:\/\/[^\s<>"'`]+/g;
const HEAD = /^==> /;

type Structured = { level?: string; status?: number; message: string };

export function parseStructured(text: string): Structured | null {
  if (!text.startsWith("{")) return null;
  let o: unknown;
  try { o = JSON.parse(text); } catch { return null; }
  if (typeof o !== "object" || o === null || Array.isArray(o)) return null;
  const f = o as Record<string, unknown>;
  const level = [f.level, f.severity].find((v) => typeof v === "string") as string | undefined;
  const code = Number([f.statusCode, f.status].find((v) => v !== undefined && v !== ""));
  const message = [f.message, f.msg].find((v) => typeof v === "string") as string | undefined;
  return { level, status: Number.isInteger(code) && code >= 100 && code < 600 ? code : undefined, message: message ?? "" };
}

export function isFailureLine(text: string, stderr?: boolean): boolean {
  const j = parseStructured(text);
  if (!j) return !!stderr;
  return (j.status ?? 0) >= 500 || /^(error|fatal|critical|severe)$/i.test(j.level ?? "") || !!stderr;
}

function statusClass(code: number): string {
  return code >= 500 ? "bad" : code >= 400 ? "warn" : code < 300 ? "ok" : "";
}

function levelClass(level: string): string {
  return /^(error|fatal|critical|severe)$/i.test(level) ? "bad" : /^warn/i.test(level) ? "warn" : "";
}

export function LogLines({ rows, utc = false }: { rows: Row[]; utc?: boolean }) {
  const fmt = useMemo(() => new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
    second: "2-digit", hourCycle: "h23", timeZone: utc ? "UTC" : undefined }), [utc]);
  return rows.map((r, i) => <LogLine key={i} row={r} fmt={fmt} />);
}

function LogLine({ row: r, fmt }: { row: Row; fmt: Intl.DateTimeFormat }) {
  const [open, setOpen] = useState(false);
  const head = HEAD.test(r.text);
  const j = parseStructured(r.text);
  const tint = j?.status === undefined ? "" : j.status >= 500 ? " s5" : j.status >= 400 ? " s4" : "";
  const toggle = j ? { role: "button", tabIndex: 0, "aria-expanded": open, onClick: () => setOpen(!open),
    onKeyDown: (e: React.KeyboardEvent) => { if (e.key === "Enter") setOpen(!open); } } : {};
  return (
    <>
      <div className={"ln" + (r.bad ? " er" : r.stderr ? " se" : "") + (head ? " hd" : "") + (j ? " js" : "") + tint} {...toggle}>
        {r.time !== undefined && <span className="ts">{stamp(fmt, r.time)}</span>}
        <span className="lv"><Icon name={r.bad || r.stderr ? "log-alert" : "log-info"} /></span>
        {j ? (
          <span className="tx">
            {j.level && <span className={"pill " + levelClass(j.level)}>{j.level}</span>}
            {j.status !== undefined && <span className={"st " + statusClass(j.status)}>{j.status}</span>}
            {links(j.message || r.text)}
          </span>
        ) : (
          <span className="tx">{head ? <><span className="mk">==&gt;</span>{links(r.text.slice(3))}</> : links(r.text)}</span>
        )}
      </div>
      {j && open && <div className="raw">{r.text}</div>}
    </>
  );
}

function stamp(fmt: Intl.DateTimeFormat, iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const p = Object.fromEntries(fmt.formatToParts(d).map((x) => [x.type, x.value]));
  return `${p.day} ${p.month} ${p.hour}:${p.minute}:${p.second}`;
}

export function zoneName(utc: boolean): string {
  if (utc) return "UTC";
  return new Intl.DateTimeFormat("en-US", { timeZoneName: "short" }).formatToParts(new Date()).find((x) => x.type === "timeZoneName")?.value ?? "Local";
}

function links(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let at = 0;
  for (const m of text.matchAll(URLS)) {
    const url = m[0].replace(/[.,;:!?)\]}]+$/, "");
    out.push(text.slice(at, m.index), <a key={m.index} className="l" href={url} target="_blank" rel="noopener noreferrer">{url}</a>);
    at = m.index + url.length;
  }
  out.push(text.slice(at));
  return out;
}
