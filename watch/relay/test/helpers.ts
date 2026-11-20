import { env } from "cloudflare:workers";
import { handle } from "../src/api";

export const T0 = 1_798_000_000_000;

export async function reset() {
  await env.DB.batch(["panels", "codes", "chats", "regs"].map((t) => env.DB.prepare(`DELETE FROM ${t}`)));
}

type Call = { method: string; body: Record<string, unknown> };

export function tg(answer: (chat: string, n: number) => number | { status: 429; retry_after: number } = () => 200) {
  const calls: Call[] = [];
  const waits: number[] = [];
  const tries = new Map<string, number>();
  const f = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (!url.startsWith("http://telegram.test/botbot-token/")) throw new Error("not Telegram: " + url);
    const method = url.slice("http://telegram.test/botbot-token/".length);
    const body = JSON.parse(String(init?.body));
    calls.push({ method, body });
    if (method !== "sendMessage") return Response.json({ ok: true, result: true });
    const chat = String(body.chat_id);
    const n = (tries.get(chat) ?? 0) + 1;
    tries.set(chat, n);
    const a = answer(chat, n);
    if (typeof a === "object") return Response.json({ ok: false, error_code: 429, parameters: { retry_after: a.retry_after } }, { status: 429 });
    return Response.json({ ok: a === 200, error_code: a }, { status: a });
  }) as typeof fetch;
  const sent = () => calls.filter((c) => c.method === "sendMessage");
  return { f, calls, waits, sent, deps: (now = T0) => ({ now, fetch: f, sleep: async (ms: number) => void waits.push(ms) }) };
}

export type Fake = ReturnType<typeof tg>;

export function req(method: string, path: string, body?: unknown, key?: string, headers: Record<string, string> = {}) {
  return new Request("https://tg.test" + path, {
    method,
    body: body === undefined ? undefined : typeof body === "string" ? body : JSON.stringify(body),
    headers: { ...(key ? { authorization: "Bearer " + key } : {}), ...headers },
  });
}

export const call = (fake: Fake, method: string, path: string, body?: unknown, key?: string, now = T0) =>
  handle(req(method, path, body, key), env, fake.deps(now));

export async function register(fake: Fake, host = "panel.acme.test"): Promise<string> {
  const r = await call(fake, "POST", "/v1/panels", { host });
  if (r.status !== 201) throw new Error("register: " + r.status);
  return ((await r.json()) as { key: string }).key;
}

let updateID = 0;
export const hook = (fake: Fake, update: object, now = T0, secret = "hook-secret") =>
  handle(req("POST", "/telegram/webhook", { update_id: ++updateID, ...update }, undefined, { "x-telegram-bot-api-secret-token": secret }), env, fake.deps(now));

export const start = (fake: Fake, text: string, chat: object = { id: 7, type: "private", first_name: "Behzod", last_name: "Halil" }, now = T0) =>
  hook(fake, { message: { message_id: 1, text, chat } }, now);

export async function link(fake: Fake, chat: object = { id: 7, type: "private", first_name: "Behzod" }, key?: string): Promise<string> {
  key ??= await register(fake);
  const { code } = (await (await call(fake, "POST", "/v1/codes", undefined, key)).json()) as { code: string };
  await start(fake, "/start " + code, chat);
  return key;
}

export const rows = async <T = Record<string, unknown>>(sql: string, ...p: unknown[]) =>
  (await env.DB.prepare(sql).bind(...p).all<T>()).results;
