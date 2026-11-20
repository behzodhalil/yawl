const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? "" : "s"}`;

export function lasted(ms: number): string {
  const m = Math.floor(ms / 60_000);
  if (m < 1) return "less than a minute";
  const h = Math.floor(m / 60);
  if (h === 0) return plural(m, "minute");
  return m % 60 ? `${plural(h, "hour")} ${plural(m % 60, "minute")}` : plural(h, "hour");
}

const parts = (ms: number, tz: string) =>
  Object.fromEntries(
    new Intl.DateTimeFormat("en-GB", { timeZone: tz, day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" })
      .formatToParts(ms)
      .map((p) => [p.type, p.value]),
  ) as Record<string, string>;

export function clock(ms: number, tz: string): string {
  const p = parts(ms, tz);
  return `${p.hour}:${p.minute}`;
}

export function when(ms: number | undefined, now: number, tz: string): string {
  if (ms === undefined) return "never";
  const p = parts(ms, tz);
  const n = parts(now, tz);
  return p.day === n.day && p.month === n.month ? `${p.hour}:${p.minute}` : `${p.day} ${p.month} ${p.hour}:${p.minute}`;
}

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

export function zone(ms: number, tz: string): string {
  const z = new Intl.DateTimeFormat("en-US", { timeZone: tz, timeZoneName: "shortOffset" }).formatToParts(ms).find((p) => p.type === "timeZoneName");
  return (z?.value ?? "GMT").replace("GMT", "UTC").replace(/^UTC[+-]0$/, "UTC");
}

export function panelOffline(panel: string, silentMs: number, limitMs: number, now: number, tz: string, link: string): Record<string, unknown> {
  const p: Record<string, unknown> = {
    text: `\u24E7 <b>Panel is offline</b>\n\nPanel: <code>${esc(panel)}</code>\nValue: no signal for ${lasted(silentMs)} (limit ${lasted(limitMs)})\nTime: ${clock(now, tz)} ${zone(now, tz)}`,
    parse_mode: "HTML",
    disable_web_page_preview: true,
  };
  if (link.startsWith("https://")) p.reply_markup = { inline_keyboard: [[{ text: "Status page", url: link }]] };
  return p;
}

export function took(ms: number): string {
  return ms < 60_000 ? `${Math.floor(ms / 1000)} s` : lasted(ms);
}

export interface CronRow {
  subject: string;
  heard: number;
  started_at: number;
  sched_at: number;
  finished_at: number;
  exit: number;
  ms: number;
  cause: string;
  timed_out: number;
}

export function cronFailure(label: string, tz: string, limitMs: number, r: { sched_at: number; exit: number; ms: number; cause: string; timed_out: number | boolean }): string {
  const head = `${label} failed at ${clock(r.sched_at, tz)} (${tz})`;
  if (r.timed_out) return `${head}: stopped at its ${lasted(limitMs)} limit.`;
  if (r.cause) return `${head}: ${r.cause.replace(/\.+$/, "")}.`;
  if (r.exit !== 0) return `${head}: exit ${r.exit} after ${took(r.ms)}.`;
  return "";
}

export function cronMissed(label: string, tz: string, minute: number, last: CronRow | undefined, now: number): string {
  const head = `${label} did not run at ${clock(minute, tz)} (${tz}).`;
  if (!last || !last.finished_at) return `${head} Last run: never.`;
  return `${head} Last run: ${when(last.sched_at, now, tz)}, ${took(last.ms)}, exit ${last.exit}.`;
}

export function cronUnfinished(label: string, tz: string, limitMs: number, r: CronRow): string {
  return `${label} started at ${clock(r.sched_at, tz)} (${tz}) and has not finished within its ${lasted(limitMs)} limit.`;
}
