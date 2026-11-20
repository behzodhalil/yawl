export interface Env {
  DB: D1Database;
  BOT_TOKEN: string;
  WEBHOOK_SECRET: string; // Telegram echoes it in X-Telegram-Bot-Api-Secret-Token (R1)
  BOT_USERNAME: string;
  TELEGRAM_API: string;
}

export interface Deps {
  now: number;
  fetch: typeof fetch;
  sleep: (ms: number) => Promise<void>;
}

export const MINUTE = 60_000;
export const CODE_TTL = 10 * MINUTE;
export const MAX_CHATS = 10;
export const SENDS_A_MINUTE = 60;

export const json = (v: unknown, status = 200, headers: Record<string, string> = {}) =>
  new Response(JSON.stringify(v), { status, headers: { "content-type": "application/json", ...headers } });
export const text = (t: string, status: number, headers: Record<string, string> = {}) => new Response(t, { status, headers });

export const hex = (b: ArrayBuffer | Uint8Array) => [...new Uint8Array(b)].map((x) => x.toString(16).padStart(2, "0")).join("");
export const sha256 = async (s: string) => hex(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s)));
