import { useMemo, type ReactNode } from "react";
import { Icon } from "./ui";

export type Row = { time?: string; text: string; bad?: boolean; stderr?: boolean };

const URLS = /https?:\/\/[^\s<>"'`]+/g;
const HEAD = /^==> /;

export function LogLines({ rows, utc = false }: { rows: Row[]; utc?: boolean }) {
  const fmt = useMemo(() => new Intl.DateTimeFormat("en-US", { month: "short", day: "2-digit", hour: "2-digit", minute: "2-digit",
    second: "2-digit", hour12: true, timeZone: utc ? "UTC" : undefined }), [utc]);
  return rows.map((r, i) => {
    const head = HEAD.test(r.text);
    return (
      <div key={i} className={"ln" + (r.bad ? " er" : r.stderr ? " se" : "") + (head ? " hd" : "")}>
        {r.time !== undefined && <span className="ts">{stamp(fmt, r.time)}</span>}
        <span className="lv"><Icon name={r.bad || r.stderr ? "log-alert" : "log-info"} /></span>
        <span className="tx">{head ? <><span className="mk">==&gt;</span>{links(r.text.slice(3))}</> : links(r.text)}</span>
      </div>
    );
  });
}

function stamp(fmt: Intl.DateTimeFormat, iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return iso;
  const p = Object.fromEntries(fmt.formatToParts(d).map((x) => [x.type, x.value]));
  return `${p.month} ${p.day} ${p.hour}:${p.minute}:${p.second} ${p.dayPeriod}`;
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
