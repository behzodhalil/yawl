// A group gets "/start@bot <code>" from a
// ?startgroup link the moment the bot is added; with privacy mode on, a
// command addressed to the bot is still delivered.
import { call, reply } from "./tg";
import { MAX_CHATS, sha256, text, type Deps, type Env } from "./types";

type Chat = { id: number; type: string; title?: string; first_name?: string; last_name?: string; username?: string };
type Update = {
  message?: { text?: string; chat: Chat };
  callback_query?: { id: string; data?: string; message?: { chat: Chat } };
};

// same compares the secret in constant time: both sides are hashed first so
// their lengths match.
async function same(a: string, b: string): Promise<boolean> {
  const enc = new TextEncoder();
  return b !== "" && crypto.subtle.timingSafeEqual(enc.encode(await sha256(a)), enc.encode(await sha256(b)));
}

export async function webhook(req: Request, env: Env, d: Deps): Promise<Response> {
  if (!(await same(req.headers.get("x-telegram-bot-api-secret-token") ?? "", env.WEBHOOK_SECRET))) return text("forbidden", 403);
  const u = (await req.json()) as Update;
  if (u.callback_query) await stopOne(u.callback_query, env, d);
  else if (u.message?.text) await command(u.message.text, u.message.chat, env, d);
  return new Response("ok"); // anything else Telegram would send again
}

async function command(t: string, chat: Chat, env: Env, d: Deps) {
  // "/start CODE" in a private chat, "/start@bot CODE" in a group; a command to another bot is not ours
  const m = /^\/(start|stop)(?:@(\w+))?(?:\s+(\S+))?/.exec(t.trim());
  if (!m || (m[2] && m[2].toLowerCase() !== env.BOT_USERNAME.toLowerCase())) return;
  if (m[1] === "stop") return listPanels(chat, env, d);
  if (!m[3]) return void (await reply(env, d, chat.id, "Press Connect Telegram in your panel to link this chat."));
  const db = env.DB;
  const id = String(chat.id);
  const row = await db
    .prepare("SELECT c.panel_id, c.expires_at, p.host FROM codes c JOIN panels p ON p.id = c.panel_id WHERE c.code = ? AND c.used_at IS NULL")
    .bind(m[3])
    .first<{ panel_id: string; expires_at: number; host: string }>();
  if (!row || row.expires_at <= d.now) return void (await reply(env, d, chat.id, "This link has expired. Make a new one in your panel."));
  const had = await db.prepare("SELECT gone_at FROM chats WHERE panel_id = ? AND chat_id = ?").bind(row.panel_id, id).first<{ gone_at: number | null }>();
  const already = had !== null && had.gone_at === null;
  const n = await db.prepare("SELECT count(*) AS n FROM chats WHERE panel_id = ? AND chat_id != ?").bind(row.panel_id, id).first<number>("n");
  if ((n ?? 0) >= MAX_CHATS) return void (await reply(env, d, chat.id, `This panel has ${MAX_CHATS} chats already. Disconnect one in the panel first.`));
  const title = chat.title || [chat.first_name, chat.last_name].filter(Boolean).join(" ") || chat.username || "";
  await db.batch([
    db
      .prepare(
        `INSERT INTO chats (panel_id, chat_id, type, title, linked_at) VALUES (?, ?, ?, ?, ?)
         ON CONFLICT (panel_id, chat_id) DO UPDATE SET type = excluded.type, title = excluded.title,
           linked_at = CASE WHEN gone_at IS NULL THEN linked_at ELSE excluded.linked_at END, gone_at = NULL`,
      )
      .bind(row.panel_id, id, chat.type, title, d.now),
    db.prepare("UPDATE codes SET used_at = ?, chat_id = ?, already = ? WHERE code = ?").bind(d.now, id, already ? 1 : 0, m[3]),
  ]);
  await reply(env, d, chat.id, already ? `This chat was already linked to ${row.host}.` : `Linked to ${row.host}.`);
}

async function listPanels(chat: Chat, env: Env, d: Deps) {
  const { results } = await env.DB.prepare("SELECT p.id, p.host FROM chats c JOIN panels p ON p.id = c.panel_id WHERE c.chat_id = ? ORDER BY p.host")
    .bind(String(chat.id))
    .all<{ id: string; host: string }>();
  if (!results.length) return void (await reply(env, d, chat.id, "This chat gets no alerts."));
  await reply(env, d, chat.id, "This chat gets alerts from these panels. Press one to stop it.", {
    reply_markup: { inline_keyboard: results.map((p) => [{ text: "Stop " + p.host, callback_data: "stop:" + p.id }]) },
  });
}

async function stopOne(q: NonNullable<Update["callback_query"]>, env: Env, d: Deps) {
  const m = /^stop:(pnl_[0-9a-f]{16})$/.exec(q.data ?? "");
  const chat = q.message?.chat.id;
  let said = "";
  if (m && chat !== undefined) {
    const row = await env.DB.prepare("DELETE FROM chats WHERE panel_id = ? AND chat_id = ? RETURNING (SELECT host FROM panels WHERE id = panel_id) AS host")
      .bind(m[1], String(chat))
      .first<{ host: string }>();
    said = row ? `No more alerts from ${row.host} here.` : "Already stopped.";
    if (row) await reply(env, d, chat, said);
  }
  await call(env, d, "answerCallbackQuery", { callback_query_id: q.id, text: said });
}
