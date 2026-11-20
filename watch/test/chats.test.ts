import { env } from "cloudflare:workers";
import { beforeEach, expect, it } from "vitest";
import { tick } from "../src/tick";
import { T0, call, exp, expect_, reset, rows } from "./helpers";

const MIN = 60_000;
const relay = { ...env, RELAY_URL: "https://relay.test", RELAY_KEY: "ab".repeat(32) };

function fake(answers: Record<string, number> = {}) {
  const sent: string[] = [];
  const f = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    if (url !== "https://relay.test/v1/send") return new Response("", { status: answers[url] ?? 200 });
    const b = JSON.parse(String(init?.body)) as { chat_id?: string; telegram: { text: string } };
    sent.push(`${b.chat_id ?? "all"} ${b.telegram.text.split("\n")[0]}`);
    return Response.json({ sent: 1, gone: 0 });
  }) as typeof fetch;
  return { f, sent };
}

const run = (f: typeof fetch, now: number) => tick(env.DB, relay, now, f, { timeoutMs: 50 });
const signal = (subject: string, now: number, id = "panel") => call("POST", "/signal", { subject }, now, id);
const list = [
  exp({ id: "srv_1", kind: "server", subject: "server:srv_1", label: "hel1" }),
  exp({ id: "panel", kind: "panel", subject: "panel", label: "The panel", server: "srv_0" }),
  exp({ id: "u0", kind: "url", subject: "service:s0", label: "acme/production/api", target: "https://api0.test/", server: "srv_1" }),
];
const cfg = { panel_url: "https://panel.test", watch_url: "https://watch.test", tz: "UTC", chats: JSON.stringify({ "service:s0": ["7", "-200"], panel: ["-200"] }) };

beforeEach(async () => {
  await reset();
  await expect_(list, cfg);
});

it("sends a panel incident once per listed chat, its close the same, and once to all with no list", async () => {
  await signal("panel", T0);
  await signal("server:srv_1", T0, "srv_1");
  const post = (b: object) => call("POST", "/incident", b, T0);
  await post({ subject: "deploy:s1", action: "open", label: "a", text: "a deploy failed.", chats: ["7", "-200"] });
  await post({ subject: "deploy:s1", action: "close", label: "a", text: "a deployed.", chats: ["7", "-200"] });
  await post({ subject: "disk:srv_1:85", action: "open", label: "hel1", text: "hel1 disk is 85% full." });
  const f = fake();
  await run(f.f, T0);
  expect(f.sent).toEqual(["7 a deploy failed.", "-200 a deploy failed.", "7 a deployed.", "-200 a deployed.", "all hel1 disk is 85% full."]);
});

it("keeps a panel incident no chat takes, and says it to nobody", async () => {
  await signal("panel", T0);
  await signal("server:srv_1", T0, "srv_1");
  expect((await call("POST", "/incident", { subject: "deploy:s1", action: "open", label: "a", text: "a deploy failed.", chats: [] }, T0)).status).toBe(200);
  expect(await rows("SELECT subject FROM incidents WHERE closed_at IS NULL")).toEqual([{ subject: "deploy:s1" }]);
  expect(await rows("SELECT * FROM outbox")).toEqual([]);
});

it("sends its own checks by the panel's list, and the panel offline to all even when listed", async () => {
  const bad = fake({ "https://api0.test/": 502 });
  for (const m of [1, 2]) {
    await signal("panel", T0 + m * MIN);
    await signal("server:srv_1", T0 + m * MIN, "srv_1");
    await run(bad.f, T0 + m * MIN);
  }
  expect(bad.sent).toEqual([
    "7 acme/production/api is down: GET https://api0.test/ answered 502 twice. Since 04:27.",
    "-200 acme/production/api is down: GET https://api0.test/ answered 502 twice. Since 04:27.",
  ]);
  const later = fake();
  await signal("server:srv_1", T0 + 8 * MIN, "srv_1");
  await run(later.f, T0 + 8 * MIN);
  expect(later.sent).toEqual(["all ⓧ <b>Panel is offline</b>", "7 acme/production/api is back after 6 minutes.", "-200 acme/production/api is back after 6 minutes."]);
});
