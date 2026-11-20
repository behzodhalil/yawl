import type { Deps, Env } from "./types";

type Answer = { ok: boolean; status: number; retryAfter?: number };

export async function call(env: Env, d: Deps, method: string, body: object): Promise<Answer> {
  try {
    const r = await d.fetch(`${env.TELEGRAM_API}/bot${env.BOT_TOKEN}/${method}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
      signal: AbortSignal.timeout(10_000),
    });
    const j = (await r.json().catch(() => ({}))) as { ok?: boolean; parameters?: { retry_after?: number } };
    const ok = r.ok && j.ok !== false;
    if (!ok) console.log(JSON.stringify({ telegram: method, status: r.status }));
    return { ok, status: r.status, retryAfter: j.parameters?.retry_after };
  } catch (e) {
    console.log(JSON.stringify({ telegram: method, error: e instanceof Error ? e.name : "error" }));
    return { ok: false, status: 0 };
  }
}

export const MAX_WAIT = 10_000;

// Telegram answers 403 when the bot was blocked or removed.
export async function deliver(env: Env, d: Deps, chat: string, msg: object): Promise<"sent" | "gone" | "failed"> {
  let waited = 0;
  for (;;) {
    const r = await call(env, d, "sendMessage", { ...msg, chat_id: chat });
    if (r.ok) return "sent";
    if (r.status === 403) return "gone";
    const wait = (r.retryAfter ?? 1) * 1000;
    if (r.status !== 429 || waited + wait > MAX_WAIT) return "failed";
    await d.sleep(wait);
    waited += wait;
  }
}

export const reply = (env: Env, d: Deps, chat: number | string, text: string, extra: object = {}) =>
  call(env, d, "sendMessage", { chat_id: chat, text, ...extra });
