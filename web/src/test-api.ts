import { vi } from "vitest";

export type Call = { method: string; url: string; body?: unknown };

export function fakeApi(routes: Record<string, unknown | ((body: unknown, url: string) => unknown)>) {
  const calls: Call[] = [];
  const f = vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? "GET";
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    const path = url.split("?")[0];
    const hit = routes[`${method} ${url}`] ?? routes[`${method} ${path}`];
    const shellSignal = method === "GET" && (path === "/api/incidents" || path === "/api/servers");
    if (hit === undefined && shellSignal) return new Response("no fake", { status: 599 });
    calls.push({ method, url, body });
    if (hit === undefined) return new Response("no fake for " + method + " " + url, { status: 599 });
    let out = typeof hit === "function" ? await (hit as (b: unknown, u: string) => unknown)(body, url) : hit;
    if (out instanceof Response) return out;
    if (out === null) return new Response(null, { status: 204 });
    return new Response(JSON.stringify(out), { status: 200 });
  });
  vi.stubGlobal("fetch", f);
  return calls;
}

export const fail = (status: number, text: string) => new Response(text, { status });
