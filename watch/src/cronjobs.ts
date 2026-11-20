import { split } from "./cron";
import { chatsOf, closeStmt, configOf, link, openStmt, queue } from "./store";
import { cronFailure, lasted, type CronRow } from "./texts";
import type { Incident } from "./types";

export interface CronReport {
  subject: string;
  event: "start" | "skip" | "finish";
  scheduled_at: number;
  exit?: number;
  ms?: number;
  cause?: string;
  timed_out?: boolean;
}

export async function cronReport(db: D1Database, now: number, id: string, r: CronReport): Promise<number> {
  const [ex, cr, inc, cf, pn] = await db.batch([
    db.prepare("SELECT label, target, server, period_s, public FROM expectations WHERE subject = ? AND kind = 'cron'").bind(r.subject),
    db.prepare("SELECT * FROM cron WHERE subject = ?").bind(r.subject),
    db.prepare("SELECT * FROM incidents WHERE subject = ? AND closed_at IS NULL").bind(r.subject),
    db.prepare("SELECT key, value FROM config"),
    db.prepare("SELECT at FROM signals WHERE subject = 'panel'"),
  ]);
  const e = ex.results[0] as { label: string; target: string; server: string; period_s: number; public: number } | undefined;
  if (!e || e.server !== id) return 403;
  const row = (cr.results[0] as CronRow | undefined) ?? { subject: r.subject, heard: 0, started_at: 0, sched_at: 0, finished_at: 0, exit: 0, ms: 0, cause: "", timed_out: 0 };
  const w: D1PreparedStatement[] = [];
  if (r.event === "start" || r.event === "skip") {
    row.heard = Math.max(row.heard, r.scheduled_at);
    if (r.event === "start") Object.assign(row, { started_at: now, sched_at: r.scheduled_at });
  } else {
    Object.assign(row, { finished_at: now, sched_at: r.scheduled_at, exit: r.exit ?? 0, ms: r.ms ?? 0, cause: r.cause ?? "", timed_out: r.timed_out ? 1 : 0 });
    const { tz } = split(e.target);
    const open = inc.results[0] as Incident | undefined;
    const config = configOf(cf.results as never);
    const tail = "\n" + link(config, (pn.results[0] as { at: number } | undefined)?.at, now);
    const failed = cronFailure(e.label, tz, e.period_s * 1000, row);
    if (failed && !open) {
      w.push(openStmt(db, now, { subject: r.subject, kind: "cron", label: e.label, source: "watch", cause: failed, public: e.public }));
      w.push(...queue(db, now, failed + tail, chatsOf(config, r.subject)));
    } else if (!failed && open) {
      w.push(closeStmt(db, now, open.id));
      w.push(...queue(db, now, `${e.label} is back after ${lasted(now - open.opened_at)}.` + tail, chatsOf(config, r.subject)));
    }
  }
  w.push(
    db
      .prepare(
        `INSERT INTO cron (subject, heard, started_at, sched_at, finished_at, exit, ms, cause, timed_out) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
         ON CONFLICT (subject) DO UPDATE SET heard = excluded.heard, started_at = excluded.started_at, sched_at = excluded.sched_at,
           finished_at = excluded.finished_at, exit = excluded.exit, ms = excluded.ms, cause = excluded.cause, timed_out = excluded.timed_out`,
      )
      .bind(row.subject, row.heard, row.started_at, row.sched_at, row.finished_at, row.exit, row.ms, row.cause, row.timed_out),
  );
  await db.batch(w);
  return 200;
}
