import { cronReport, type CronReport } from "./cronjobs";
import { verify } from "./sign";
import { status } from "./status";
import { panelIncident, replaceExpectations, setSignal, type PanelIncident } from "./store";
import type { Env, Expectation } from "./types";

const json = (v: unknown, status = 200) =>
  new Response(JSON.stringify(v), { status, headers: { "content-type": "application/json" } });
const text = (t: string, status: number) => new Response(t, { status });

export async function handle(req: Request, env: Env, now: number): Promise<Response> {
  const url = new URL(req.url);
  const db = env.DB;
  const hb = url.pathname.match(/^\/hb\/([0-9A-Za-z]+)$/);
  if (hb && (req.method === "GET" || req.method === "POST")) {
    const row = await db
      .prepare("SELECT subject FROM expectations WHERE kind = 'heartbeat' AND target = ?")
      .bind(hb[1])
      .first<{ subject: string }>();
    if (!row) return text("unknown heartbeat", 404);
    await setSignal(db, row.subject, now);
    return text("ok", 200);
  }
  if ((url.pathname === "/status" || url.pathname === "/") && req.method === "GET") {
    return status(db, now);
  }
  const body = req.method === "GET" ? "" : await req.text();
  const v = await verify(req, body, env, now);
  if ("error" in v) return text(v.error, 401);
  try {
    return await signed(req, url, body, v.id, db, now);
  } catch (e) {
    if (e instanceof SyntaxError || e instanceof TypeError) return text("bad request", 400);
    throw e;
  }
}

async function signed(req: Request, url: URL, body: string, id: string, db: D1Database, now: number): Promise<Response> {
  const route = `${req.method} ${url.pathname}`;
  if (route === "POST /signal") {
    const s = JSON.parse(body) as { subject: string; at?: number };
    const own = id === "panel" ? s.subject === "panel" || s.subject.startsWith("backup:") : s.subject === "server:" + id;
    if (!own) return text("a signal may only speak for its sender", 403);
    const at = id === "panel" && s.subject.startsWith("backup:") && typeof s.at === "number" ? s.at : now;
    await setSignal(db, s.subject, at);
    if (id !== "panel") return json({ ack: now });
    const [lr, ob] = await db.batch([
      db.prepare("SELECT at FROM last_run WHERE id = 1"),
      db.prepare("SELECT min(created_at) AS at FROM outbox WHERE sent_at IS NULL"),
    ]);
    return json({
      ack: now,
      last_run: (lr.results[0] as { at: number } | undefined)?.at ?? 0,
      backlog_at: (ob.results[0] as { at: number | null } | undefined)?.at ?? 0,
    });
  }
  if (route === "POST /cron") {
    if (id === "panel") return text("only the job's agent reports its runs", 403);
    const status = await cronReport(db, now, id, JSON.parse(body) as CronReport);
    return status === 200 ? json({ ok: true }) : text("not this server's job", status);
  }
  if (id !== "panel") return text("only the panel may do this", 403);
  switch (route) {
    case "POST /expectations": {
      const p = JSON.parse(body) as { config?: Record<string, string>; expectations: Expectation[]; reset_panel?: boolean };
      await replaceExpectations(db, now, p.config ?? {}, p.expectations, p.reset_panel === true);
      return json({ ok: true });
    }
    case "POST /incident": {
      const done = await panelIncident(db, now, JSON.parse(body) as PanelIncident);
      return json(done ? { ok: true } : { ignored: true });
    }
    case "GET /incidents": {
      const days = Math.min(Math.max(Number(url.searchParams.get("days") ?? 90) || 90, 1), 365);
      const { results } = await db
        .prepare(
          `SELECT * FROM incidents WHERE closed_at IS NULL OR closed_at > ?
           ORDER BY closed_at IS NULL DESC, opened_at DESC`,
        )
        .bind(now - days * 86_400_000)
        .all();
      return json(results);
    }
    case "GET /checks": {
      const row = await db.prepare("SELECT at, results FROM last_run WHERE id = 1").first<{ at: number; results: string }>();
      return json(row ? { at: row.at, results: JSON.parse(row.results) } : { at: 0, results: {} });
    }
  }
  return text("not found", 404);
}
