import type { CheckResult } from "./types";
import type { BadgeKind } from "./ui";
import { isDb } from "./serviceForm";

const MON = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const two = (n: number) => String(n).padStart(2, "0");

const DAY_MS = 86_400_000;
export const RANGES = { "1h": 3_600_000, "24h": DAY_MS, "7d": 7 * DAY_MS, "30d": 30 * DAY_MS, "90d": 90 * DAY_MS };
export type Range = keyof typeof RANGES;

export function stateKind(state: string): BadgeKind {
  switch (state) {
    case "live":
      return "ok";
    case "failed":
      return "bad";
    case "queued": case "building": case "pulling": case "built": case "snapshotting": case "releasing":
    case "starting": case "checking": case "switching": case "draining":
      return "busy";
  }
  return "off";
}

export function ago(ms: number | undefined, now = Date.now()): string {
  if (!ms) return "never";
  const s = Math.max(0, Math.round((now - ms) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  const d = Math.floor(s / 86400);
  return d === 1 ? "1 day ago" : `${d} days ago`;
}

export function secondsAgo(ms: number, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - ms) / 1000));
  return s < 60 ? `${s} s ago` : ago(ms, now);
}

const hhmm = (ms: number) => {
  const d = new Date(ms);
  return `${two(d.getHours())}:${two(d.getMinutes())}`;
};

export function outsideLine(c: CheckResult | undefined): string {
  if (!c) return "";
  return c.ok ? `From outside: ${c.ms ?? 0} ms` : `From outside: down since ${hhmm(c.since)}`;
}

export const commitSubject = (subject: string) => subject.replace(/^Merge pull request (#\d+) from [^/\s]+\/(\S+)/, "PR $1 · $2");

export const actorName = (by: string) => by.replace(/\s*\(.*\)\s*$/, "");

export const shortSha = (sha?: string) => (sha ?? "").slice(0, 7);

export const shortRef = (d: { commit?: string; digest?: string }) => (d.digest ? d.digest.replace(/^sha256:/, "").slice(0, 12) : shortSha(d.commit));

export const changeLine = (c: { op: string; kind: string; name: string; detail?: string }) =>
  [c.op, c.kind, c.name, c.detail].filter(Boolean).join(" ");

export function bytes(n: number): string {
  const units: [number, string][] = [[1e12, "TB"], [1e9, "GB"], [1e6, "MB"], [1e3, "kB"]];
  for (const [u, name] of units) {
    if (n >= u) {
      const v = n / u;
      return `${v >= 100 ? Math.round(v) : Math.round(v * 10) / 10} ${name}`;
    }
  }
  return `${Math.round(n)} B`;
}

export type Forecast = { days: number | null; gb_per_day: number; growing: boolean };

export function forecastLine(pct: number, f: Forecast): [string, boolean] {
  if (!f.growing || f.days === null) return [`Disk ${pct}% full, not growing.`, false];
  return [`Disk ${pct}% full, growing ${f.gb_per_day.toFixed(1)} GB a day: full in about ${f.days} days.`, f.days <= 7];
}

export function dayMonth(ms: number): string {
  const d = new Date(ms);
  return `${d.getDate()} ${MON[d.getMonth()]}`;
}

export function dateTime(ms: number): string {
  const d = new Date(ms);
  return `${dayMonth(ms)} ${d.getFullYear()} ${two(d.getHours())}:${two(d.getMinutes())}`;
}

export function dayTime(ms: number, now = Date.now()): string {
  const d = new Date(ms), n = new Date(now);
  const time = `${two(d.getHours())}:${two(d.getMinutes())}`;
  const dayStart = (x: Date) => new Date(x.getFullYear(), x.getMonth(), x.getDate()).getTime();
  const days = Math.round((dayStart(n) - dayStart(d)) / DAY_MS);
  if (days === 0) return `Today, ${time}`;
  if (days === 1) return `Yesterday, ${time}`;
  return `${dayMonth(ms)}${d.getFullYear() === n.getFullYear() ? "" : ` ${d.getFullYear()}`}, ${time}`;
}

export function parseDay(s: string): number | null {
  const t = s.trim();
  const iso = /^(\d{4})-(\d{2})-(\d{2})$/.exec(t);
  const dmy = /^(\d{1,2})\s+([a-z]{3})\s+(\d{4})$/i.exec(t);
  const [y, m, d] = iso ? [+iso[1], +iso[2] - 1, +iso[3]]
    : dmy ? [+dmy[3], MON.findIndex((x) => x.toLowerCase() === dmy[2].toLowerCase()), +dmy[1]] : [NaN, NaN, NaN];
  const at = new Date(y, m, d);
  return at.getFullYear() === y && at.getMonth() === m && at.getDate() === d ? at.getTime() : null;
}

export function took(ms: number | undefined): string {
  if (!ms || ms <= 0) return "";
  const s = Math.round(ms / 1000);
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`;
}

export function niceTicks(max: number, whole = false): number[] {
  if (!(max > 0)) return [0, 1];
  const p = 10 ** Math.floor(Math.log10(max / 4));
  const steps = [1, 2, 2.5, 5, 10].map((m) => m * p).filter((s) => !whole || (s >= 1 && Number.isInteger(s)));
  const step = steps.find((s) => s * 4 >= max) ?? 1;
  const n = Math.ceil(max / step - 1e-9);
  return Array.from({ length: n + 1 }, (_, i) => Number((i * step).toPrecision(12)));
}

const STATE_WORD: Record<string, [string, BadgeKind]> = {
  failed: ["Failed", "bad"], superseded: ["Skipped", "off"], rolled_back: ["Rolled back", "bad"],
};

export function deployWord(state: string, live: boolean, rolledBack = false, cause = ""): [string, BadgeKind] {
  if (state === "superseded" && cause === "Canceled.") return ["Canceled", "off"];
  if (state === "live") return live ? ["Deployed", "ok"] : rolledBack ? ["Rolled back", "bad"] : ["Replaced", "off"];
  return STATE_WORD[state] ?? [state.charAt(0).toUpperCase() + state.slice(1), "busy"];
}

const STEP: Record<string, string> = {
  build: "Build", start: "Start", check: "Health check", switch: "Route traffic", drain: "Stop old version", stop_old: "Stop old version",
  ensure: "Start database", restore: "Restore backup", restore_volume: "Restore volume", snapshot: "Snapshot database",
  release: "Release command", schedule: "Schedule job",
};

export function stepName(step: string): string {
  return STEP[step] ?? (step.charAt(0).toUpperCase() + step.slice(1)).replaceAll("_", " ");
}

export function serviceWord(kind: string, state: string): [string, BadgeKind] {
  if (state === "live") return [isDb(kind) ? "Available" : kind === "cron" ? "Scheduled" : "Deployed", "ok"];
  if (state === "failed") return ["Failed", "bad"];
  if (!state || state === "never deployed" || state === "none") return ["Not deployed", "off"];
  const k = stateKind(state);
  if (k === "busy") return [state === "queued" ? "Queued" : state === "building" ? "Building" : isDb(kind) ? "Starting" : "Deploying", "busy"];
  return [state.charAt(0).toUpperCase() + state.slice(1), k];
}

export function backupWord(state: string): [string, BadgeKind] {
  if (state === "ok") return ["Completed", "ok"];
  if (state === "failed") return ["Failed", "bad"];
  return [state === "running" || state === "pending" ? "Backing up" : state.charAt(0).toUpperCase() + state.slice(1), "busy"];
}
