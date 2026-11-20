import type { Env } from "./types";

const enc = new TextEncoder();

export const hex = (b: ArrayBuffer | Uint8Array) =>
  [...new Uint8Array(b)].map((x) => x.toString(16).padStart(2, "0")).join("");

export function fromHex(s: string): Uint8Array {
  const out = new Uint8Array(s.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(s.slice(i * 2, i * 2 + 2), 16);
  return out;
}

const hmacKey = (key: Uint8Array) =>
  crypto.subtle.importKey("raw", key, { name: "HMAC", hash: "SHA-256" }, false, ["sign", "verify"]);

const message = (method: string, path: string, t: number, body: string) => enc.encode(`${method}\n${path}\n${t}\n${body}`);

export async function sign(key: Uint8Array, method: string, path: string, t: number, body: string): Promise<string> {
  return hex(await crypto.subtle.sign("HMAC", await hmacKey(key), message(method, path, t, body)));
}

export async function agentKey(panelKey: Uint8Array, serverID: string): Promise<Uint8Array> {
  return new Uint8Array(await crypto.subtle.sign("HMAC", await hmacKey(panelKey), enc.encode("agent:" + serverID)));
}

export async function verify(req: Request, body: string, env: Env, now: number): Promise<{ id: string } | { error: string }> {
  const id = req.headers.get("X-Yawl-Id") ?? "";
  const t = Number(req.headers.get("X-Yawl-Time"));
  const mac = req.headers.get("X-Yawl-Mac") ?? "";
  if (!id || !Number.isInteger(t) || !/^[0-9a-f]{64}$/.test(mac)) return { error: "unauthorized" };
  const off = Math.abs(now / 1000 - t);
  if (off > 60) return { error: `clock is ${Math.round(off)} s off` };
  const panel = fromHex(env.PANEL_KEY);
  const key = id === "panel" ? panel : await agentKey(panel, id);
  const u = new URL(req.url);
  const ok = await crypto.subtle.verify("HMAC", await hmacKey(key), fromHex(mac), message(req.method, u.pathname + u.search, t, body));
  return ok ? { id } : { error: "unauthorized" };
}
