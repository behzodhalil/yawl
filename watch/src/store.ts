// The watcher's D1 work. Each function keeps to one read batch and one
// write batch: on the free plan every D1 call is a subrequest (Plan 12).
import { addressed } from "./telegram";
import type { Expectation, Incident } from "./types";
import { MINUTE, SILENT_AFTER } from "./types";

const B32 = "abcdefghijklmnopqrstuvwxyz234567";
export function newID(prefix: string): string {
  const b = crypto.getRandomValues(new Uint8Array(12));
  return prefix + [...b].map((x) => B32[x & 31]).join("");
}

export type Config = Record<string, string>;

export function link(config: Config, panelAt: number | undefined, now: number): string {
  if (panelAt !== undefined && now - panelAt <= SILENT_AFTER && config.panel_url) return config.panel_url + "/incidents";
  return (config.watch_url ?? "") + "/status";
}

export function queue(db: D1Database, now: number, text: string, chats?: string[]): D1PreparedStatement[] {
  return addressed(text, chats).map((t) =>
    db.prepare("INSERT INTO outbox (id, text, attempts, next_at, created_at) VALUES (?, ?, 0, ?, ?)").bind(newID("obx_"), t, now, now),
  );
}

export function configOf(rows: { key: string; value: string }[]): Config {
  return Object.fromEntries(rows.map((r) => [r.key, r.value]));
}

export function chatsOf(config: Config, subject: string): string[] | undefined {
  try {
    return (JSON.parse(config.chats || "{}") as Record<string, string[]>)[subject];
  } catch {
    return undefined;
  }
}

export function openStmt(db: D1Database, now: number, i: Omit<Incident, "id" | "opened_at" | "closed_at" | "note">, id = newID("inc_")) {
  return db
    .prepare(
      "INSERT INTO incidents (id, subject, kind, label, source, opened_at, cause, public) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
    )
    .bind(id, i.subject, i.kind, i.label, i.source, now, i.cause, i.public);
}

export function closeStmt(db: D1Database, now: number, id: string) {
  return db.prepare("UPDATE incidents SET closed_at = ? WHERE id = ?").bind(now, id);
}

export async function replaceExpectations(db: D1Database, now: number, config: Config, list: Expectation[], resetPanel = false) {
  const [old, open] = await db.batch([
    db.prepare("SELECT id, subject, created_at FROM expectations"),
    db.prepare("SELECT id, subject FROM incidents WHERE closed_at IS NULL"),
  ]);
  const created = new Map((old.results as { id: string; created_at: number }[]).map((r) => [r.id, r.created_at]));
  const keep = new Set(list.map((e) => e.id));
  const subjects = new Set(list.map((e) => e.subject));
  const watched = new Set((old.results as { subject: string }[]).map((r) => r.subject));
  const w: D1PreparedStatement[] = [];
  for (const id of created.keys()) {
    if (!keep.has(id)) {
      w.push(db.prepare("DELETE FROM expectations WHERE id = ?").bind(id));
      w.push(db.prepare("DELETE FROM state WHERE id = ?").bind(id));
    }
  }
  for (const e of list) {
    w.push(
      db
        .prepare(
          `INSERT INTO expectations (id, kind, subject, label, target, server, period_s, grace_s, public, created_at)
           VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
           ON CONFLICT (id) DO UPDATE SET kind = excluded.kind, subject = excluded.subject, label = excluded.label,
             target = excluded.target, server = excluded.server, period_s = excluded.period_s,
             grace_s = excluded.grace_s, public = excluded.public`,
        )
        .bind(e.id, e.kind, e.subject, e.label, e.target, e.server, e.period_s, e.grace_s, e.public ? 1 : 0, created.get(e.id) ?? now),
    );
  }
  for (const i of open.results as { id: string; subject: string }[]) {
    if (watched.has(i.subject) && !subjects.has(i.subject)) {
      w.push(db.prepare("UPDATE incidents SET closed_at = ?, cause = 'removed' WHERE id = ?").bind(now, i.id));
    }
  }
  if (resetPanel) w.push(db.prepare("UPDATE incidents SET closed_at = ? WHERE source = 'panel' AND closed_at IS NULL").bind(now));
  for (const [k, v] of Object.entries(config)) {
    w.push(
      db.prepare("INSERT INTO config (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value").bind(k, v),
    );
  }
  if (w.length) await db.batch(w);
}

export async function setSignal(db: D1Database, subject: string, at: number) {
  await db
    .prepare("INSERT INTO signals (subject, at) VALUES (?, ?) ON CONFLICT (subject) DO UPDATE SET at = excluded.at")
    .bind(subject, at)
    .run();
}

export interface PanelIncident {
  subject: string;
  action: "open" | "close" | "note";
  label?: string;
  text?: string;
  id?: string;
  silent?: boolean;
  telegram?: Record<string, unknown>;
  chats?: string[];
}

export async function panelIncident(db: D1Database, now: number, p: PanelIncident): Promise<boolean> {
  if (p.action === "note") {
    await db.prepare("UPDATE incidents SET note = ? WHERE id = ?").bind(p.text ?? "", p.id ?? "").run();
    return true;
  }
  const [watched, open, cfg, panel] = await db.batch([
    db.prepare("SELECT count(*) AS n FROM expectations WHERE subject = ?").bind(p.subject),
    db.prepare("SELECT id, source FROM incidents WHERE subject = ? AND closed_at IS NULL").bind(p.subject),
    db.prepare("SELECT key, value FROM config"),
    db.prepare("SELECT at FROM signals WHERE subject = 'panel'"),
  ]);
  if ((watched.results[0] as { n: number }).n > 0 && !p.subject.startsWith("cron:")) return false;
  const current = open.results[0] as { id: string; source: string } | undefined;
  const text = p.text ?? "";
  const tail = "\n" + link(configOf(cfg.results as never), (panel.results[0] as { at: number } | undefined)?.at, now);
  const msg = p.telegram ? JSON.stringify(p.telegram) : text + tail;
  const w: D1PreparedStatement[] = [];
  if (p.action === "open" && !current) {
    w.push(openStmt(db, now, { subject: p.subject, kind: "panel", label: p.label ?? p.subject, source: "panel", cause: text, public: 0 }));
    if (text) w.push(...queue(db, now, msg, p.chats));
  }
  if (p.action === "open" && current?.source === "panel" && text) {
    w.push(db.prepare("UPDATE incidents SET cause = ? WHERE id = ?").bind(text, current.id));
    w.push(...queue(db, now, msg, p.chats));
  }
  if (p.action === "close" && current?.source === "panel") {
    w.push(closeStmt(db, now, current.id));
    if (text && !p.silent) w.push(...queue(db, now, msg, p.chats));
  }
  if (w.length) await db.batch(w);
  return true;
}

export { MINUTE };
