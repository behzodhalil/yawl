import { configOf } from "./store";
import { lasted } from "./texts";

const DAY = 86_400_000;
const MIN = 60_000;
const RECENT = 14 * DAY;
const ESC: Record<string, string> = { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" };
const esc = (s: string) => s.replace(/[&<>"']/g, (c) => ESC[c]);

interface Row {
  id: string;
  name: string;
  subjects: string[];
}
interface Inc {
  kind: "incident" | "maintenance";
  title: string;
  rows: string[];
  updates: { at: number; body: string }[];
  starts_at: number;
  ends_at: number;
  resolved_at: number;
  created_at: number;
}
interface Published {
  title: string;
  rows: Row[];
  incidents: Inc[];
}
interface Member {
  since: number;
  down: [number, number][];
  now: boolean;
}
type State = [kind: "ok" | "busy" | "bad" | "off", word: string];

// The page is public and unsigned, so a scraper would
// spend the free D1 quota; an isolate reads D1 for it at most every 30 s.
let memo = { at: -Infinity, html: "" };
export const forgetStatus = () => (memo = { at: -Infinity, html: "" });

export async function status(db: D1Database, now: number): Promise<Response> {
  if (now < memo.at || now - memo.at >= 30_000) memo = { at: now, html: await page(db, now) };
  return new Response(memo.html, {
    headers: {
      "content-type": "text/html; charset=utf-8",
      "cache-control": "max-age=30",
      "content-security-policy": "default-src 'none'; style-src 'unsafe-inline'",
    },
  });
}

async function page(db: D1Database, now: number): Promise<string> {
  const [ex, inc, cf] = await db.batch([
    db.prepare("SELECT subject, server, created_at FROM expectations"),
    db.prepare("SELECT subject, opened_at, closed_at FROM incidents WHERE closed_at IS NULL OR closed_at > ?").bind(now - 91 * DAY),
    db.prepare("SELECT key, value FROM config"),
  ]);
  const config = configOf(cf.results as never);
  let pub: Published;
  try {
    pub = JSON.parse(config.status ?? "") as Published;
  } catch {
    return html("Status", [], [], "", "", ["off", "No status page is published here"]);
  }
  const tz = config.tz || "UTC";
  const subj = new Map<string, { since: number; keys: Set<string> }>();
  for (const e of ex.results as { subject: string; server: string; created_at: number }[]) {
    const s = subj.get(e.subject) ?? { since: e.created_at, keys: new Set([e.subject]) };
    s.since = Math.min(s.since, e.created_at);
    if (e.server) s.keys.add("server:" + e.server);
    subj.set(e.subject, s);
  }
  const bySubject = new Map<string, { opened_at: number; closed_at: number | null }[]>();
  for (const i of inc.results as { subject: string; opened_at: number; closed_at: number | null }[]) {
    bySubject.set(i.subject, [...(bySubject.get(i.subject) ?? []), i]);
  }
  const member = (subject: string): Member | undefined => {
    const s = subj.get(subject);
    if (!s) return undefined;
    const mine = [...s.keys].flatMap((k) => bySubject.get(k) ?? []);
    return { since: s.since, down: mine.map((i) => [i.opened_at, i.closed_at ?? now]), now: mine.some((i) => i.closed_at === null) };
  };

  const local = new Intl.DateTimeFormat("en-GB", { timeZone: tz, day: "numeric", month: "short", hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  const at = (ms: number) => {
    const p = Object.fromEntries(local.formatToParts(ms).map((x) => [x.type, x.value]));
    return `${p.day} ${p.month} ${p.hour}:${p.minute} (${tz})`;
  };
  const dayFmt = new Intl.DateTimeFormat("en-GB", { timeZone: "UTC", day: "numeric", month: "short" }); // once: the free plan has 10 ms of CPU
  const incidents = pub.incidents ?? [];
  const names = new Map<string, string>();
  const states: State[] = [];
  const rows: string[] = [];
  const today = Math.floor(now / DAY) * DAY;
  for (const r of pub.rows ?? []) {
    const ms = r.subjects.map(member).filter((m): m is Member => m !== undefined);
    if (ms.length === 0) continue;
    names.set(r.id, r.name);
    const st = rowState(r.id, ms, incidents, now);
    states.push(st);
    let down = 0;
    let total = 0;
    const bars = Array.from({ length: 90 }, (_, k) => {
      const from = today - (89 - k) * DAY;
      const to = Math.min(from + DAY, now);
      const label = dayFmt.format(from);
      let sum = 0;
      let worst = 0;
      let seen = 0;
      for (const m of ms) {
        const lo = Math.max(from, m.since);
        if (lo >= to) continue;
        seen++;
        const d = overlap(m.down, lo, to);
        sum += d;
        worst = Math.max(worst, d);
        down += d;
        total += to - lo;
      }
      if (seen === 0) return bar("off", `${label}: no data`);
      if (sum / seen >= MIN) return bar("bad", `${label}: ${lasted(sum / seen)} down`);
      if (worst >= MIN) return bar("warn", `${label}: some down, the longest ${lasted(worst)}`);
      return bar("ok", `${label}: no downtime`);
    }).join("");
    const up = total === 0 ? "" : down === 0 ? "100%" : `${(Math.floor(10000 * (1 - down / total)) / 100).toFixed(2)}%`;
    const label = up ? `: ${up} uptime` : "";
    rows.push(
      `<div class="row"><div class="head"><b>${esc(r.name)}</b>${badge(st)}</div>` +
        `<div class="bars" role="img" aria-label="${esc(r.name)}, last 90 days${label}">${bars}</div>` +
        `<div class="scale"><span><span class="d90">90 days ago</span><span class="d30">30 days ago</span></span>${up ? `<span>${up} uptime</span>` : ""}<span>Today</span></div></div>`,
    );
  }

  const show = (i: Inc, b: State, when: string) => {
    const aff = i.rows.map((id) => names.get(id)).filter(Boolean).join(", ");
    const ups = (i.updates ?? []).map((u) => `<li><time>${esc(at(u.at))}</time><p>${esc(u.body)}</p></li>`).join("");
    return `<div class="card"><div class="head"><b>${esc(i.title)}</b>${badge(b)}</div><p class="muted">${esc(when)}${aff ? `. Affects ${esc(aff)}` : ""}</p>${ups ? `<ul class="upd">${ups}</ul>` : ""}</div>`;
  };
  const start = (i: Inc) => (i.kind === "maintenance" ? i.starts_at : i.created_at);
  const ongoing: string[] = [];
  const scheduled: string[] = [];
  const recently: string[] = [];
  for (const i of [...incidents].sort((a, b) => start(b) - start(a))) {
    if (i.kind === "incident" && i.resolved_at === 0) ongoing.push(show(i, ["busy", "Ongoing"], `Since ${at(start(i))}`));
    else if (i.kind === "maintenance" && i.starts_at <= now && now < i.ends_at) ongoing.push(show(i, ["busy", "Maintenance"], `Until ${at(i.ends_at)}`));
    else if (i.kind === "maintenance" && i.starts_at > now) scheduled.unshift(show(i, ["off", "Scheduled"], `${at(i.starts_at)} to ${at(i.ends_at)}`));
    else if (i.kind === "incident" && now - i.resolved_at < RECENT) recently.push(show(i, ["ok", "Resolved"], `${at(start(i))}, ${lasted(i.resolved_at - start(i))}`));
    else if (i.kind === "maintenance" && now - i.ends_at < RECENT) recently.push(show(i, ["ok", "Completed"], `${at(i.starts_at)} to ${at(i.ends_at)}`));
  }
  return html(pub.title || "Status", rows, ongoing.slice(0, 20), scheduled.join(""), recently.slice(0, 30).join(""), summary(states), at(now));
}

const overlap = (spans: [number, number][], from: number, to: number) =>
  spans.reduce((n, [a, b]) => n + Math.max(0, Math.min(b, to) - Math.max(a, from)), 0);

const bar = (cls: string, title: string) => `<i class="${cls}" title="${esc(title)}"></i>`;

function rowState(id: string, ms: Member[], incidents: Inc[], now: number): State {
  if (incidents.some((i) => i.kind === "maintenance" && i.starts_at <= now && now < i.ends_at && i.rows.includes(id))) return ["busy", "Maintenance"];
  const down = ms.filter((m) => m.now).length;
  if (down > 0 && down === ms.length) return ["bad", "Down"];
  if (down > 0) return ["busy", "Degraded"];
  if (incidents.some((i) => i.kind === "incident" && i.resolved_at === 0 && i.rows.includes(id))) return ["busy", "Degraded"];
  return ["ok", "Operational"];
}

function summary(states: State[]): State {
  const n = (w: string) => states.filter((s) => s[1] === w).length;
  if (states.length === 0) return ["off", "Nothing is shown here yet"];
  if (n("Down") === states.length) return ["bad", "All services are down"];
  if (n("Down") > 0) return ["bad", "Some services are down"];
  if (n("Degraded") > 0) return ["busy", "Some services are degraded"];
  if (n("Maintenance") > 0) return ["busy", "Maintenance is under way"];
  return ["ok", "All services are operational"];
}

const GLYPH = {
  ok: '<path d="M5 14L8.5 17.5L19 6.5"/>',
  bad: '<path d="M18.9991 19L5 5.00098"/><path d="M19 5L5.00092 18.999"/>',
  busy: '<path d="M12 3V6"/><path d="M12 18V21"/><path d="M21 12L18 12"/><path d="M6 12L3 12"/><path d="M18.3635 5.63672L16.2422 7.75804"/><path d="M7.75804 16.2422L5.63672 18.3635"/><path d="M18.3635 18.3635L16.2422 16.2422"/><path d="M7.75804 7.75804L5.63672 5.63672"/>',
  off: '<path d="M20 12L4 12"/>',
};
const badge = ([kind, word]: State) =>
  `<span class="badge ${kind}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${GLYPH[kind]}</svg>${esc(word)}</span>`;

const section = (title: string, body: string) => (body ? `<section><h2>${title}</h2><div class="stack">${body}</div></section>` : "");

function html(title: string, rows: string[], ongoing: string[], scheduled: string, recently: string, sum: State, updated = ""): string {
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="referrer" content="no-referrer"><title>${esc(title)}</title><style>${CSS}</style></head><body><main>
<h1>${esc(title)}</h1>
<div class="card sum">${badge(sum)}${updated ? `<span class="muted">Updated ${esc(updated)}</span>` : ""}</div>
${section("Ongoing", ongoing.join(""))}
${rows.length ? `<section><h2>Services</h2><div class="list">${rows.join("")}</div></section>` : ""}
${section("Scheduled maintenance", scheduled)}
${section("Recent incidents", recently)}
</main></body></html>`;
}

const CSS = `:root{--bg:#0d0d0d;--raise:#1a1a1a;--fg:#f0f0f0;--mute:#a3a3a3;--line:#333333;--ok:#37cd8f;--ok-chip:color-mix(in srgb,#37cd8f 24%,transparent);--bad:#f87171;--bad-chip:color-mix(in srgb,#f87171 22%,transparent);--warn:#fbbf24;--off:#333333;color-scheme:dark}
@media (prefers-color-scheme: light){:root{--bg:#ffffff;--raise:#f5f5f5;--fg:#0d0d0d;--mute:#525252;--line:#e5e5e5;--ok:#15803d;--ok-chip:#dcfce7;--bad:#b91c1c;--bad-chip:#fee2e2;--warn:#a16207;--off:#e5e5e5;color-scheme:light}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:400 14px/1.5 "Geist",ui-sans-serif,system-ui,sans-serif;-webkit-font-smoothing:antialiased}
main{max-width:760px;margin:0 auto;padding:40px 16px 56px;display:flex;flex-direction:column;gap:28px}
h1{margin:0;font:600 24px/1.2 "Geist",ui-sans-serif,system-ui,sans-serif;letter-spacing:-.01em;overflow-wrap:anywhere}
h2{margin:0 0 10px;font:600 16px/1.3 "Geist",ui-sans-serif,system-ui,sans-serif}
p{margin:0}
.muted{color:var(--mute)}
.card{border:1px solid var(--line);border-radius:6px;padding:16px;display:flex;flex-direction:column;gap:10px}
.sum{flex-direction:row;align-items:center;justify-content:space-between;flex-wrap:wrap;gap:8px}
.list{border:1px solid var(--line);border-radius:6px}
.row{padding:16px;display:flex;flex-direction:column;gap:10px}
.row+.row{border-top:1px solid var(--line)}
.head{display:flex;align-items:center;justify-content:space-between;gap:12px}
.head b{font-weight:500;overflow-wrap:anywhere}
.badge{display:inline-flex;align-items:center;gap:6px;padding:3px 9px 3px 7px;border-radius:4px;font-size:13px;font-weight:500;line-height:18px;white-space:nowrap;background:var(--raise);color:var(--fg);flex:none}
.badge svg{width:15px;height:15px;flex:none}
.badge.ok{background:var(--ok-chip)}.badge.ok svg{color:var(--ok)}
.badge.bad{background:var(--bad-chip)}.badge.bad svg{color:var(--bad)}
.bars{display:flex;gap:2px;height:28px}
.bars i{flex:1;border-radius:2px;background:var(--ok)}
.bars i.bad{background:var(--bad)}.bars i.warn{background:var(--warn)}.bars i.off{background:var(--off)}
.scale{display:flex;justify-content:space-between;gap:8px;font-size:12.5px;color:var(--mute)}
.d30{display:none}
.upd{display:flex;flex-direction:column;gap:8px;margin:0;padding:0;list-style:none}
.upd li{display:flex;flex-direction:column;gap:2px}
.upd time{font-size:12.5px;color:var(--mute)}
.upd p{white-space:pre-wrap;overflow-wrap:anywhere}
.stack{display:flex;flex-direction:column;gap:12px}
@media (max-width:600px){.bars i:nth-child(-n+60){display:none}.d90{display:none}.d30{display:inline}main{padding-top:24px}}`;
