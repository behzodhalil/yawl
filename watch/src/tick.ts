import { chatsOf, closeStmt, configOf, link, newID, openStmt } from "./store";
import { addressed, send } from "./telegram";
import { formatter, matches, parse, partsIn, split } from "./cron";
import { clock, cronMissed, cronUnfinished, lasted, panelOffline, when, type CronRow } from "./texts";
import type { Env, Expectation, Incident } from "./types";
import { SILENT_AFTER } from "./types";

// The free plan allows 50 subrequests a run, and D1 calls count.
export const MAX_FETCHES = 40;
export const MAX_SENDS = 5;
const DAY = 86_400_000;
const BACKUP_AFTER = 26 * 3_600_000;

type Row = Expectation & { created_at: number; public: boolean | number };
type State = { id: string; ok: number; misses: number; since: number; detail: string };
type Result = { ok: boolean; ms?: number; detail: string; since: number; at: number };
type Out = { id: string; text: string; attempts: number; created_at: number; fresh: boolean };

export async function tick(db: D1Database, env: Env, now: number, fetcher: typeof fetch, opts: { timeoutMs?: number } = {}) {
  const [ex, st, sg, inc, cf, ob, lr, cr] = await db.batch([
    db.prepare("SELECT * FROM expectations ORDER BY id"),
    db.prepare("SELECT * FROM state"),
    db.prepare("SELECT subject, at FROM signals"),
    db.prepare("SELECT * FROM incidents WHERE closed_at IS NULL"),
    db.prepare("SELECT key, value FROM config"),
    db.prepare("SELECT id, text, attempts, created_at FROM outbox WHERE sent_at IS NULL AND next_at <= ? AND created_at >= ? ORDER BY created_at LIMIT ?").bind(now, now - DAY, MAX_SENDS),
    db.prepare("SELECT at, results FROM last_run WHERE id = 1"),
    db.prepare("SELECT * FROM cron"),
  ]);
  const list = ex.results as Row[];
  const state = new Map((st.results as State[]).map((s) => [s.id, s]));
  const signals = new Map((sg.results as { subject: string; at: number }[]).map((s) => [s.subject, s.at]));
  const open = new Map((inc.results as Incident[]).map((i) => [i.subject, i]));
  const config = configOf(cf.results as never);
  const tz = config.tz || "UTC";
  const prev: Record<string, Result> = lr.results[0] ? JSON.parse((lr.results[0] as { results: string }).results) : {};
  const results: Record<string, Result> = {};
  const w: D1PreparedStatement[] = [];
  const out: Out[] = (ob.results as Omit<Out, "fresh">[]).map((o) => ({ ...o, fresh: false }));
  const tail = "\n" + link(config, signals.get("panel"), now);
  const say = (e: Row, text: string, tg?: object) => {
    for (const t of addressed(tg ? JSON.stringify(tg) : text + tail, e.kind === "panel" ? undefined : chatsOf(config, e.subject)))
      out.push({ id: newID("obx_"), text: t, attempts: 0, created_at: now, fresh: true });
  };

  const openIf = (e: Row, cause: string, tg?: object) => {
    if (open.has(e.subject)) return;
    const i: Incident = { id: newID("inc_"), subject: e.subject, kind: e.kind, label: e.label, source: "watch", opened_at: now, closed_at: null, cause, note: "", public: e.public ? 1 : 0 };
    open.set(e.subject, i);
    w.push(openStmt(db, now, i, i.id));
    say(e, cause, tg);
  };
  const closeIf = (e: Row) => {
    const i = open.get(e.subject);
    if (!i || i.source !== "watch") return;
    open.delete(e.subject);
    w.push(closeStmt(db, now, i.id));
    say(e, `${e.label} is back after ${lasted(now - i.opened_at)}.`);
  };
  const last = (e: Row) => signals.get(e.subject) ?? e.created_at;
  const silent = (e: Row) => now - last(e) > SILENT_AFTER;

  const down = new Set<string>();
  const urls = list.filter((e) => e.kind === "url");
  for (const e of list.filter((e) => e.kind === "server")) {
    const id = e.subject.slice("server:".length);
    if (silent(e)) {
      down.add(id);
      const n = new Set(urls.filter((u) => u.server === id).map((u) => u.subject)).size;
      const more = n === 0 ? "" : n === 1 ? " 1 service on it is not checked separately until it is back." : ` ${n} services on it are not checked separately until it is back.`;
      openIf(e, `${e.label} is not answering.${more}`);
      results[e.subject] = { ok: false, detail: "silent", since: last(e), at: now };
    } else {
      closeIf(e);
      results[e.subject] = { ok: true, detail: "", since: last(e), at: now };
    }
  }
  for (const e of list.filter((e) => e.kind === "panel")) {
    const quiet = silent(e);
    const panel = config.panel_url?.replace(/^https?:\/\//, "") || e.label;
    if (quiet && !down.has(e.server))
      openIf(e, "The panel is not answering.", panelOffline(panel, now - last(e), SILENT_AFTER, now, tz, tail.slice(1)));
    if (!quiet) closeIf(e);
    results[e.subject] = { ok: !quiet, detail: quiet ? "silent" : "", since: last(e), at: now };
  }
  for (const e of list.filter((e) => e.kind === "heartbeat")) {
    const at = signals.get(e.subject);
    const late = now - last(e) > (e.period_s + e.grace_s) * 1000;
    if (late) openIf(e, `${e.label} did not check in: expected every ${lasted(e.period_s * 1000)}, last call ${when(at, now, tz)}.`);
    else closeIf(e);
    results[e.subject] = { ok: !late, detail: late ? "late" : "", since: last(e), at: now };
  }
  for (const e of list.filter((e) => e.kind === "backup")) {
    const at = signals.get(e.subject);
    const late = now - last(e) > BACKUP_AFTER;
    if (late) openIf(e, `${e.label}: no backup has succeeded in 26 hours (last: ${when(at, now, tz)}).`);
    else closeIf(e);
    results[e.subject] = { ok: !late, detail: late ? "late" : "", since: last(e), at: now };
  }

  const lastAt = lr.results[0] ? (lr.results[0] as { at: number }).at : now - 60_000;
  const cronRows = new Map((cr.results as CronRow[]).map((c) => [c.subject, c]));
  const fmts = new Map<string, Intl.DateTimeFormat>();
  for (const e of list.filter((e) => e.kind === "cron")) {
    const { tz, expr } = split(e.target);
    const s = parse(expr);
    if (typeof s === "string") continue;
    let f = fmts.get(tz);
    if (!f) fmts.set(tz, (f = formatter(tz)));
    const row = cronRows.get(e.subject);
    const until = Math.floor((now - 2 * 60_000) / 60_000) * 60_000;
    const from = Math.max(Math.floor((lastAt - 2 * 60_000) / 60_000) * 60_000 + 60_000, until - 9 * 60_000, Math.ceil(e.created_at / 60_000) * 60_000);
    for (let m = from; m <= until; m += 60_000) {
      if (matches(s, partsIn(m, f)) && (row?.heard ?? 0) < m) openIf(e, cronMissed(e.label, tz, m, row, now));
    }
    if (row && row.started_at > row.finished_at && now - row.started_at > e.period_s * 1000 + 2 * 60_000) {
      openIf(e, cronUnfinished(e.label, tz, e.period_s * 1000, row));
    }
    results[e.subject] = { ok: !open.has(e.subject), detail: "", since: row?.finished_at ?? e.created_at, at: now };
  }

  const live = urls.filter((u) => !down.has(u.server));
  let due = live;
  if (live.length > MAX_FETCHES) {
    const start = (Math.floor(now / 60_000) * MAX_FETCHES) % live.length;
    due = Array.from({ length: MAX_FETCHES }, (_, i) => live[(start + i) % live.length]);
  }
  const checked = await Promise.all(due.map((u) => check(u.target, fetcher, opts.timeoutMs ?? 10_000)));
  const fresh = new Map<string, State>();
  due.forEach((u, i) => {
    const c = checked[i];
    const was = state.get(u.id) ?? { id: u.id, ok: 1, misses: 0, since: u.created_at, detail: "" };
    const next: State = c.ok
      ? { id: u.id, ok: 1, misses: 0, since: was.ok ? was.since : now, detail: "" }
      : { id: u.id, ok: 0, misses: Math.min(was.misses + 1, 2), since: was.ok ? now : was.since, detail: c.detail };
    fresh.set(u.id, next);
    if (next.ok !== was.ok || next.misses !== was.misses || next.detail !== was.detail || !state.has(u.id)) {
      w.push(
        db
          .prepare(
            "INSERT INTO state (id, ok, misses, since, detail) VALUES (?, ?, ?, ?, ?) ON CONFLICT (id) DO UPDATE SET ok = excluded.ok, misses = excluded.misses, since = excluded.since, detail = excluded.detail",
          )
          .bind(next.id, next.ok, next.misses, next.since, next.detail),
      );
    }
    const r = results[u.subject];
    const mine: Result = { ok: c.ok, ms: c.ms, detail: c.detail, since: next.since, at: now };
    results[u.subject] = !r || (r.ok && !mine.ok) ? mine : r;
  });
  const bySubject = new Map<string, Row[]>();
  for (const u of live) bySubject.set(u.subject, [...(bySubject.get(u.subject) ?? []), u]);
  for (const [subject, us] of bySubject) {
    const now_ = us.map((u) => fresh.get(u.id) ?? state.get(u.id));
    const worst = us.find((u, i) => (now_[i]?.misses ?? 0) >= 2);
    if (worst) {
      const s = (fresh.get(worst.id) ?? state.get(worst.id))!;
      const how = s.detail === "timeout" ? "did not answer within 10 s twice" : `answered ${s.detail} twice`;
      openIf(worst, `${worst.label} is down: GET ${worst.target} ${how}. Since ${clock(s.since, tz)}.`);
    } else if (now_.every((s) => !s || s.ok)) {
      closeIf(us[0]);
    }
    if (!results[subject] && prev[subject]) results[subject] = prev[subject];
  }
  for (const u of urls.filter((u) => down.has(u.server))) if (prev[u.subject]) results[u.subject] = prev[u.subject];

  const sending = out.slice(0, MAX_SENDS);
  const sent = await Promise.all(sending.map((o) => send(env, o.text, fetcher)));
  out.forEach((o, i) => {
    const done = i < sending.length && sent[i];
    const tried = i < sending.length;
    const attempts = o.attempts + (tried && !done ? 1 : 0);
    const next = tried && !done ? now + Math.min(60_000 * 2 ** (attempts - 1), 3_600_000) : now;
    if (o.fresh) {
      w.push(
        db
          .prepare("INSERT INTO outbox (id, text, attempts, next_at, created_at, sent_at) VALUES (?, ?, ?, ?, ?, ?)")
          .bind(o.id, o.text, attempts, next, o.created_at, done ? now : null),
      );
    } else if (tried) {
      w.push(db.prepare("UPDATE outbox SET attempts = ?, next_at = ?, sent_at = ? WHERE id = ?").bind(attempts, next, done ? now : null, o.id));
    }
  });
  w.push(db.prepare("DELETE FROM outbox WHERE created_at < ?").bind(now - DAY));
  w.push(
    db
      .prepare("INSERT INTO last_run (id, at, results) VALUES (1, ?, ?) ON CONFLICT (id) DO UPDATE SET at = excluded.at, results = excluded.results")
      .bind(now, JSON.stringify(results)),
  );
  await db.batch(w);
}

async function check(url: string, fetcher: typeof fetch, timeoutMs: number): Promise<{ ok: boolean; ms: number; detail: string }> {
  const t = Date.now();
  try {
    const r = await fetcher(url, { redirect: "manual", signal: AbortSignal.timeout(timeoutMs), headers: { "user-agent": "yawl-watch" } });
    const ms = Date.now() - t;
    return r.status >= 200 && r.status < 400 ? { ok: true, ms, detail: "" } : { ok: false, ms, detail: String(r.status) };
  } catch {
    return { ok: false, ms: Date.now() - t, detail: "timeout" };
  }
}
