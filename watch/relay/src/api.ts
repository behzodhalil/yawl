import { deliver } from "./tg";
import { webhook } from "./webhook";
import { CODE_TTL, MAX_CHATS, MINUTE, SENDS_A_MINUTE, hex, json, sha256, text, type Deps, type Env } from "./types";

const HOST = /^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?(:\d{1,5})?$/i;
const CODE_CHARS = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
const BYTE_VALUES = 256;
const UNBIASED_BYTE_LIMIT = BYTE_VALUES - (BYTE_VALUES % CODE_CHARS.length);

function code(): string {
  let out = "";
  while (out.length < 16) {
    for (const b of crypto.getRandomValues(new Uint8Array(24))) if (b < UNBIASED_BYTE_LIMIT && out.length < 16) out += CODE_CHARS[b % CODE_CHARS.length];
  }
  return out;
}

type Panel = { id: string; host: string };

async function panel(req: Request, db: D1Database): Promise<Panel | null> {
  const m = /^Bearer ([0-9a-f]{64})$/.exec(req.headers.get("authorization") ?? "");
  if (!m) return null;
  return db.prepare("SELECT id, host FROM panels WHERE key_hash = ?").bind(await sha256(m[1])).first<Panel>();
}

export async function handle(req: Request, env: Env, d: Deps): Promise<Response> {
  const url = new URL(req.url);
  const route = `${req.method} ${url.pathname}`;
  try {
    if (route === "GET /v1/health") return json({ ok: true, bot: env.BOT_USERNAME });
    if (route === "POST /telegram/webhook") return await webhook(req, env, d);
    if (route === "POST /v1/panels") return await register(await req.json(), env.DB, d.now, req.headers.get("cf-connecting-ip") ?? "");
    if (!url.pathname.startsWith("/v1/")) return text("not found", 404);
    const p = await panel(req, env.DB);
    if (!p) return text("unknown panel key", 401);
    return await authed(req, route, url, p, env, d);
  } catch (e) {
    if (e instanceof SyntaxError || e instanceof TypeError) return text("bad request", 400);
    throw e;
  }
}

const REG_PER_HOUR = 10;

async function register(body: { host?: unknown }, db: D1Database, now: number, ip: string): Promise<Response> {
  if (typeof body.host !== "string" || !HOST.test(body.host)) return text("host is the panel's host name", 400);
  const hour = Math.floor(now / 3_600_000), ipHash = await sha256(ip);
  const r = await db.prepare("INSERT INTO regs (ip_hash, hour, n) VALUES (?, ?, 1) ON CONFLICT (ip_hash, hour) DO UPDATE SET n = n + 1 RETURNING n").bind(ipHash, hour).first<number>("n");
  if ((r ?? 0) > REG_PER_HOUR) return new Response("Too many panels registered from this address; try in an hour.", { status: 429, headers: { "retry-after": "3600" } });
  await db.prepare("DELETE FROM regs WHERE hour < ?").bind(hour - 1).run();
  const key = hex(crypto.getRandomValues(new Uint8Array(32)));
  const id = "pnl_" + hex(crypto.getRandomValues(new Uint8Array(8)));
  await db.prepare("INSERT INTO panels (id, key_hash, host, created_at) VALUES (?, ?, ?, ?)").bind(id, await sha256(key), body.host.toLowerCase(), now).run();
  return json({ key }, 201);
}

async function authed(req: Request, route: string, url: URL, p: Panel, env: Env, d: Deps): Promise<Response> {
  const db = env.DB;
  const one = /^\/v1\/chats\/(-?\d+)$/.exec(url.pathname);
  if (req.method === "DELETE" && one) {
    const r = await db.prepare("DELETE FROM chats WHERE panel_id = ? AND chat_id = ?").bind(p.id, one[1]).run();
    return r.meta.changes ? new Response(null, { status: 204 }) : text("not this panel's chat", 404);
  }
  const used = /^\/v1\/codes\/([A-Za-z0-9]{16})$/.exec(url.pathname);
  if (req.method === "GET" && used) {
    const c = await db
      .prepare(
        `SELECT c.expires_at, c.used_at, c.already, ch.chat_id, ch.type, ch.title, ch.linked_at, ch.gone_at
         FROM codes c LEFT JOIN chats ch ON ch.panel_id = c.panel_id AND ch.chat_id = c.chat_id WHERE c.code = ? AND c.panel_id = ?`,
      )
      .bind(used[1], p.id)
      .first<{ expires_at: number; used_at: number | null; already: number; chat_id: string | null; type: string; title: string; linked_at: number; gone_at: number | null }>();
    if (!c) return text("no such code", 404);
    const chat = c.chat_id === null ? null : { id: c.chat_id, type: c.type, title: c.title, linked_at: c.linked_at, gone_at: c.gone_at };
    return json({ expires_at: c.expires_at, used_at: c.used_at ?? 0, already: c.already === 1, chat });
  }
  switch (route) {
    case "POST /v1/codes": {
      const raw = await req.text();
      const host = raw ? (JSON.parse(raw) as { host?: unknown }).host : undefined;
      const n = await db.prepare("SELECT count(*) AS n FROM chats WHERE panel_id = ?").bind(p.id).first<number>("n");
      if ((n ?? 0) >= MAX_CHATS) return text(`This panel has ${MAX_CHATS} chats, the most it can have.`, 409);
      const c = code();
      const expires = d.now + CODE_TTL;
      const w = [
        db.prepare("DELETE FROM codes WHERE expires_at <= ?").bind(d.now),
        db.prepare("INSERT INTO codes (code, panel_id, expires_at) VALUES (?, ?, ?)").bind(c, p.id, expires),
      ];
      if (typeof host === "string" && HOST.test(host)) w.push(db.prepare("UPDATE panels SET host = ? WHERE id = ?").bind(host.toLowerCase(), p.id));
      await db.batch(w);
      return json({ code: c, link: `https://t.me/${env.BOT_USERNAME}?start=${c}`, expires_at: expires, bot: env.BOT_USERNAME }, 201);
    }
    case "GET /v1/chats": {
      const { results } = await db
        .prepare("SELECT chat_id AS id, type, title, linked_at, gone_at FROM chats WHERE panel_id = ? ORDER BY linked_at")
        .bind(p.id)
        .all();
      return json({ bot: env.BOT_USERNAME, chats: results });
    }
    case "POST /v1/send":
      return send(JSON.parse(await req.text()), p, env, d);
  }
  return text("not found", 404);
}

type SendBody = { chat_id?: string | number; telegram?: Record<string, unknown> };

const COPIED_OPTIONS = ["parse_mode", "disable_notification", "disable_web_page_preview"] as const;
type UrlOnlyButton = { text: string; url: string };
type RelayedMessage = { text: string; reply_markup?: { inline_keyboard: UrlOnlyButton[][] } } & {
  [K in (typeof COPIED_OPTIONS)[number]]?: unknown;
};

function message(t: SendBody["telegram"]): RelayedMessage | null {
  if (!t || typeof t.text !== "string" || !t.text || t.text.length > 4096) return null;
  const m: RelayedMessage = { text: t.text };
  for (const k of COPIED_OPTIONS) if (k in t) m[k] = t[k];
  const kb = (t.reply_markup as { inline_keyboard?: { text: unknown; url: unknown }[][] } | undefined)?.inline_keyboard;
  if (kb) m.reply_markup = { inline_keyboard: kb.map((row) => row.map((b) => ({ text: String(b.text), url: String(b.url) }))) };
  return m;
}

async function send(b: SendBody, p: Panel, env: Env, d: Deps): Promise<Response> {
  const db = env.DB;
  const msg = message(b.telegram);
  if (!msg) return text("telegram.text is needed, at most 4096 characters", 400);
  const minute = Math.floor(d.now / MINUTE) * MINUTE;
  const n = await db
    .prepare("UPDATE panels SET win_n = CASE WHEN win_at = ?1 THEN win_n + 1 ELSE 1 END, win_at = ?1 WHERE id = ?2 RETURNING win_n")
    .bind(minute, p.id)
    .first<number>("win_n");
  if ((n ?? 0) > SENDS_A_MINUTE) {
    return text(`more than ${SENDS_A_MINUTE} messages this minute`, 429, { "retry-after": String(Math.ceil((minute + MINUTE - d.now) / 1000)) });
  }
  const { results } = await db.prepare("SELECT chat_id, gone_at FROM chats WHERE panel_id = ?").bind(p.id).all<{ chat_id: string; gone_at: number | null }>();
  let to = results;
  if (b.chat_id !== undefined) {
    to = results.filter((c) => c.chat_id === String(b.chat_id));
    if (!to.length) return text("not this panel's chat", 403);
  }
  to = to.filter((c) => c.gone_at === null);
  if (!to.length) return text(b.chat_id === undefined ? "No chat is linked." : "This chat blocked or removed the bot.", 409);
  const out = await Promise.all(to.map((c) => deliver(env, d, c.chat_id, msg)));
  const gone = to.filter((_, i) => out[i] === "gone");
  if (gone.length) await db.batch(gone.map((c) => db.prepare("UPDATE chats SET gone_at = ? WHERE panel_id = ? AND chat_id = ?").bind(d.now, p.id, c.chat_id)));
  const sent = out.filter((o) => o === "sent").length;
  const failed = out.filter((o) => o === "failed").length;
  console.log(JSON.stringify({ send: to.length, sent, gone: gone.length, failed }));
  if (failed) return text(`Telegram did not take the message in ${failed} of ${to.length} chats.`, 502);
  if (!sent) return text("Every chat blocked or removed the bot.", 409);
  return json({ sent, gone: gone.length });
}
