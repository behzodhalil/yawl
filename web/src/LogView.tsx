import { linkVariants } from "@cloudflare/kumo";
import { useMemo, useState, type ReactNode } from "react";
import { Icon } from "./ui";

export type Row = { time?: string; text: string; bad?: boolean; stderr?: boolean; deploy?: string };

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

const PLAIN_FAILURE = /^\s*(?:\[?(?:error|fatal)\b|panic:|traceback\b)/i;

export function isFailureLine(text: string, stderr?: boolean): boolean {
  const j = parseStructured(text);
  if (!j) return !!stderr || PLAIN_FAILURE.test(text);
  return (j.status ?? 0) >= 500 || /^(error|fatal|critical|severe)$/i.test(j.level ?? "") || !!stderr;
}

function statusClass(code: number): string {
  return code >= 500 ? "bad" : code >= 400 ? "warn" : code < 300 ? "ok" : "";
}

function levelClass(level: string): string {
  return /^(error|fatal|critical|severe)$/i.test(level) ? "bad" : /^warn/i.test(level) ? "warn" : "";
}

export const stampFormat = (utc: boolean) => new Intl.DateTimeFormat("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit",
  second: "2-digit", hourCycle: "h23", timeZone: utc ? "UTC" : undefined });

export function LogLines({ rows, utc = false, subjects }: { rows: Row[]; utc?: boolean; subjects?: Record<string, string> }) {
  const fmt = useMemo(() => stampFormat(utc), [utc]);
  const seen = new Map<string, number>();
  return rows.flatMap((r, i) => {
    const base = `${r.time ?? ""}|${r.text}`;
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    const line = <LogLine key={`${base}|${n}`} row={r} fmt={fmt} />;
    const prev = rows[i - 1]?.deploy;
    if (!r.deploy || !prev || prev === r.deploy) return [line];
    return [<DeployMark key={`deploy|${base}|${n}`} id={r.deploy} subject={subjects?.[r.deploy]} />, line];
  });
}

function DeployMark({ id, subject }: { id: string; subject?: string }) {
  const name = `Deploy ${id.replace(/^dep_/, "").slice(0, 7)}`;
  return (
    <div role="separator" aria-label={subject ? `${name} · ${subject}` : name} className="my-1 flex items-center gap-2 border-y border-kumo-hairline py-0.5 font-sans text-xs text-kumo-subtle">
      <span className="font-medium">{name}</span>{subject && <span className="truncate">{subject}</span>}
    </div>
  );
}

function LogLine({ row: r, fmt }: { row: Row; fmt: Intl.DateTimeFormat }) {
  const [open, setOpen] = useState(false);
  const head = HEAD.test(r.text);
  const j = parseStructured(r.text);
  const tint = j?.status === undefined ? "" : j.status >= 500 ? " s5" : j.status >= 400 ? " s4" : "";
  const icon = <Icon name={r.bad || r.stderr ? "log-alert" : "log-info"} />;
  return (
    <>
      <div className={"ln" + (r.bad ? " er" : r.stderr ? " se" : "") + (head ? " hd" : "") + (j ? " js" : "") + tint}
        onClick={j ? (e) => { if (!(e.target as Element).closest("a")) setOpen(!open); } : undefined}>
        {r.time !== undefined && <span className="ts">{stamp(fmt, r.time)}</span>}
        {j ? <button type="button" className="lv" aria-expanded={open} aria-label="Show fields">{icon}</button> : <span className="lv">{icon}</span>}
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

export function stamp(fmt: Intl.DateTimeFormat, iso: string): string {
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
    out.push(text.slice(at, m.index), <a key={m.index} className={linkVariants({ variant: "inline" })} href={url} target="_blank" rel="noopener noreferrer">{url}</a>);
    at = m.index + url.length;
  }
  out.push(text.slice(at));
  return out;
}
