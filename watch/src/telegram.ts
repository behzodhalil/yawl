import type { Env } from "./types";

export function payload(text: string): Record<string, unknown> | null {
  if (!text.startsWith("{")) return null;
  try {
    const p = JSON.parse(text);
    return p && typeof p === "object" && typeof p.text === "string" ? p : null;
  } catch {
    return null;
  }
}

export function addressed(text: string, chats?: string[]): string[] {
  if (!chats) return [text];
  const p = payload(text) ?? { text, disable_web_page_preview: true };
  return chats.map((c) => JSON.stringify({ ...p, chat_id: c }));
}

export async function send(env: Pick<Env, "BOT_TOKEN" | "CHAT_ID" | "TELEGRAM_API" | "RELAY_URL" | "RELAY_KEY">, text: string, fetcher: typeof fetch): Promise<boolean> {
  const { chat_id, ...msg } = payload(text) ?? { text, disable_web_page_preview: true };
  const relay = env.RELAY_URL && env.RELAY_KEY;
  try {
    const r = await fetcher(relay ? `${env.RELAY_URL}/v1/send` : `${env.TELEGRAM_API}/bot${env.BOT_TOKEN}/sendMessage`, {
      method: "POST",
      headers: { "content-type": "application/json", ...(relay ? { authorization: "Bearer " + env.RELAY_KEY } : {}) },
      body: JSON.stringify(relay ? { telegram: msg, ...(chat_id === undefined ? {} : { chat_id }) } : { ...msg, chat_id: env.CHAT_ID }),
      signal: AbortSignal.timeout(10_000),
    });
    return r.ok;
  } catch {
    return false;
  }
}
