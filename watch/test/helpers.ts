import { env } from "cloudflare:workers";
import { agentKey, fromHex, sign } from "../src/sign";
import { handle } from "../src/routes";
import { forgetStatus } from "../src/status";
import type { Expectation } from "../src/types";

export const panelKey = fromHex("07".repeat(32));
export const T0 = 1_798_000_000_000;

export async function reset() {
  forgetStatus();
  await env.DB.batch(
    ["expectations", "state", "signals", "incidents", "outbox", "config", "last_run", "cron"].map((t) =>
      env.DB.prepare(`DELETE FROM ${t}`),
    ),
  );
}

export async function signed(method: string, path: string, body = "", now = T0, id = "panel", key = panelKey) {
  const t = Math.floor(now / 1000);
  const mac = await sign(key, method, path, t, body);
  return new Request("https://watch.test" + path, {
    method,
    body: method === "GET" ? undefined : body,
    headers: { "X-Yawl-Id": id, "X-Yawl-Time": String(t), "X-Yawl-Mac": mac },
  });
}

export async function call(method: string, path: string, body: unknown = undefined, now = T0, id = "panel", key?: Uint8Array) {
  const text = body === undefined ? "" : JSON.stringify(body);
  const k = key ?? (id === "panel" ? panelKey : await agentKey(panelKey, id));
  return handle(await signed(method, path, text, now, id, k), env, now);
}

export function exp(e: Partial<Expectation> & { id: string; kind: Expectation["kind"]; subject: string }): Expectation {
  return { label: e.id, target: "", server: "", period_s: 0, grace_s: 0, public: false, ...e };
}

export async function expect_(list: Expectation[], config: Record<string, string> = {}, now = T0) {
  const r = await call("POST", "/expectations", { config, expectations: list }, now);
  if (r.status !== 200) throw new Error("expectations: " + r.status + " " + (await r.text()));
}

export const rows = async <T = Record<string, unknown>>(sql: string, ...p: unknown[]) =>
  (await env.DB.prepare(sql).bind(...p).all<T>()).results;
