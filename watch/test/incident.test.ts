import { beforeEach, describe, expect, it } from "vitest";
import { send } from "../src/telegram";
import { T0, call, exp, expect_, reset, rows } from "./helpers";

beforeEach(async () => {
  await reset();
  await expect_([exp({ id: "u1", kind: "url", subject: "service:svc_1" })], {
    panel_url: "https://panel.test",
    watch_url: "https://watch.test",
  });
  await call("POST", "/signal", { subject: "panel" });
});

const post = (b: object, now = T0) => call("POST", "/incident", b, now);

describe("panel incidents", () => {
  it("opens once and queues its text with the link", async () => {
    const r = await post({ subject: "disk:srv_1:85", action: "open", label: "hel1", text: "hel1 disk is 85% full." });
    expect(r.status).toBe(200);
    expect(await rows("SELECT subject, source, cause, closed_at FROM incidents")).toEqual([
      { subject: "disk:srv_1:85", source: "panel", cause: "hel1 disk is 85% full.", closed_at: null },
    ]);
    expect(await rows("SELECT text FROM outbox")).toEqual([{ text: "hel1 disk is 85% full.\nhttps://panel.test/incidents" }]);
  });

  it("queues the panel's Telegram payload in place of the text", async () => {
    const tg = { text: "\u24E7 <b>Production deploy failed</b>", parse_mode: "HTML", disable_web_page_preview: true };
    await post({ subject: "deploy:svc_2", action: "open", label: "a", text: "a deploy failed: x.", telegram: tg });
    expect(await rows("SELECT cause FROM incidents")).toEqual([{ cause: "a deploy failed: x." }]);
    const [{ text }] = await rows<{ text: string }>("SELECT text FROM outbox");
    expect(JSON.parse(text)).toEqual(tg);
    const calls: string[] = [];
    const ok = await send({ BOT_TOKEN: "T", CHAT_ID: "42", TELEGRAM_API: "http://telegram.test" }, text, (async (_: RequestInfo | URL, init?: RequestInit) => {
      calls.push(String(init?.body));
      return new Response("{}");
    }) as typeof fetch);
    expect(ok).toBe(true);
    expect(JSON.parse(calls[0])).toEqual({ ...tg, chat_id: "42" });
  });

  it("sends through the relay when it has one", async () => {
    const tg = { text: "<b>The panel is not answering</b>", parse_mode: "HTML" };
    const calls: { url: string; auth: string; body: unknown }[] = [];
    const env = { BOT_TOKEN: "", CHAT_ID: "", TELEGRAM_API: "http://telegram.test", RELAY_URL: "https://relay.test", RELAY_KEY: "ab".repeat(32) };
    const f = (async (u: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(u), auth: new Headers(init?.headers).get("authorization") ?? "", body: JSON.parse(String(init?.body)) });
      return Response.json({ sent: 1, gone: 0 });
    }) as typeof fetch;
    expect(await send(env, JSON.stringify(tg), f)).toBe(true);
    expect(await send(env, "hel1 is back after 3 minutes.", f)).toBe(true);
    expect(calls).toEqual([
      { url: "https://relay.test/v1/send", auth: "Bearer " + "ab".repeat(32), body: { telegram: tg } },
      { url: "https://relay.test/v1/send", auth: "Bearer " + "ab".repeat(32), body: { telegram: { text: "hel1 is back after 3 minutes.", disable_web_page_preview: true } } },
    ]);
    expect(await send(env, "x", (async () => new Response("No chat is linked.", { status: 409 })) as typeof fetch)).toBe(false);
  });

  it("says a second failure on an open incident too", async () => {
    await post({ subject: "deploy:svc_2", action: "open", label: "a", text: "a deploy failed: one." });
    await post({ subject: "deploy:svc_2", action: "open", label: "a", text: "a deploy failed: two." }, T0 + 1000);
    expect(await rows("SELECT cause FROM incidents")).toEqual([{ cause: "a deploy failed: two." }]);
    expect((await rows<{ text: string }>("SELECT text FROM outbox ORDER BY created_at")).map((r) => r.text.split("\n")[0])).toEqual([
      "a deploy failed: one.",
      "a deploy failed: two.",
    ]);
  });

  it("closes silently when told the text was already sent", async () => {
    await post({ subject: "service:svc_9", action: "open", label: "a", text: "a is down." });
    await post({ subject: "service:svc_9", action: "close", label: "a", text: "a recovered.", silent: true }, T0 + 1000);
    expect(await rows("SELECT closed_at FROM incidents")).toEqual([{ closed_at: T0 + 1000 }]);
    expect(await rows("SELECT count(*) AS n FROM outbox")).toEqual([{ n: 1 }]);
  });

  it("closes with its text only when something was open", async () => {
    await post({ subject: "deploy:svc_2", action: "close", label: "a", text: "a deployed." });
    expect(await rows("SELECT count(*) AS n FROM outbox")).toEqual([{ n: 0 }]);
    await post({ subject: "deploy:svc_2", action: "open", label: "a", text: "a deploy failed: x." });
    await post({ subject: "deploy:svc_2", action: "close", label: "a", text: "a deployed." }, T0 + 5000);
    expect(await rows("SELECT closed_at FROM incidents")).toEqual([{ closed_at: T0 + 5000 }]);
    expect((await rows<{ text: string }>("SELECT text FROM outbox ORDER BY created_at, text")).map((r) => r.text)).toContain(
      "a deployed.\nhttps://panel.test/incidents",
    );
  });

  it("closes silently when the text is empty", async () => {
    await post({ subject: "disk:srv_1:95", action: "open", label: "hel1", text: "hel1 disk is 95% full." });
    await post({ subject: "disk:srv_1:95", action: "close", label: "hel1", text: "" });
    expect(await rows("SELECT count(*) AS n FROM outbox")).toEqual([{ n: 1 }]);
  });

  it("takes a note", async () => {
    await post({ subject: "deploy:svc_2", action: "open", label: "a", text: "x" });
    const [{ id }] = await rows<{ id: string }>("SELECT id FROM incidents");
    expect((await post({ subject: "", action: "note", id, text: "rolling back" })).status).toBe(200);
    expect(await rows("SELECT note FROM incidents")).toEqual([{ note: "rolling back" }]);
  });

  it("ignores subjects it watches", async () => {
    const r = await post({ subject: "service:svc_1", action: "open", label: "acme/production/api", text: "down" });
    expect(await r.json()).toEqual({ ignored: true });
    expect(await rows("SELECT count(*) AS n FROM incidents")).toEqual([{ n: 0 }]);
  });

  it("does not close what the watcher opened", async () => {
    await post({ subject: "x", action: "open", label: "x", text: "x" });
    await (await import("cloudflare:workers")).env.DB.prepare("UPDATE incidents SET source = 'watch'").run();
    await post({ subject: "x", action: "close", label: "x", text: "back" });
    expect(await rows("SELECT closed_at FROM incidents")).toEqual([{ closed_at: null }]);
  });

  it("links to the status page when the panel is silent", async () => {
    await post({ subject: "x", action: "open", label: "x", text: "x" }, T0 + 4 * 60_000);
    expect(await rows("SELECT text FROM outbox")).toEqual([{ text: "x\nhttps://watch.test/status" }]);
  });
});
