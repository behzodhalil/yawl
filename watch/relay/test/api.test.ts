import { beforeEach, describe, expect, it } from "vitest";
import { T0, call, link, register, reset, rows, start, tg } from "./helpers";

beforeEach(reset);

const msg = { text: "<b>api is down</b>", parse_mode: "HTML", disable_web_page_preview: true };

describe("panels and codes", () => {
  it("answers health with the bot's name", async () => {
    const r = await call(tg(), "GET", "/v1/health");
    expect(await r.json()).toEqual({ ok: true, bot: "useyawl_bot" });
  });

  it("registers a panel and keeps only the key's hash (R2)", async () => {
    const fake = tg();
    const key = await register(fake, "Panel.Acme.test");
    expect(key).toMatch(/^[0-9a-f]{64}$/);
    const [p] = await rows<{ key_hash: string; host: string }>("SELECT key_hash, host FROM panels");
    expect(p.host).toBe("panel.acme.test");
    expect(p.key_hash).not.toContain(key);
    expect(p.key_hash).toMatch(/^[0-9a-f]{64}$/);
    for (const host of ["", "a b", "<x>", 5]) expect((await call(fake, "POST", "/v1/panels", { host })).status).toBe(400);
    expect((await call(fake, "POST", "/v1/panels", "{")).status).toBe(400);
  });

  it("lets one address register ten panels an hour, then 429", async () => {
    const fake = tg();
    for (let i = 0; i < 10; i++) expect((await call(fake, "POST", "/v1/panels", { host: `p${i}.example.test` })).status).toBe(201);
    const r = await call(fake, "POST", "/v1/panels", { host: "p10.example.test" });
    expect(r.status).toBe(429);
    expect(r.headers.get("retry-after")).toBe("3600");
    expect((await call(fake, "POST", "/v1/panels", { host: "later.example.test" }, undefined, T0 + 3_600_000)).status).toBe(201);
  });

  it("refuses a missing or unknown key", async () => {
    const fake = tg();
    expect((await call(fake, "GET", "/v1/chats")).status).toBe(401);
    expect((await call(fake, "GET", "/v1/chats", undefined, "ab".repeat(32))).status).toBe(401);
  });

  it("makes a 16 character code with its t.me link, good for 10 minutes", async () => {
    const fake = tg();
    const key = await register(fake);
    const r = await call(fake, "POST", "/v1/codes", { host: "ops.acme.test" }, key);
    expect(r.status).toBe(201);
    const c = (await r.json()) as { code: string; link: string; expires_at: number; bot: string };
    expect(c.code).toMatch(/^[A-Za-z0-9]{16}$/);
    expect(c.link).toBe("https://t.me/useyawl_bot?start=" + c.code);
    expect(c.expires_at).toBe(T0 + 600_000);
    expect(c.bot).toBe("useyawl_bot");
    expect(await rows("SELECT host FROM panels")).toEqual([{ host: "ops.acme.test" }]);
    await call(fake, "POST", "/v1/codes", undefined, key, T0 + 600_000);
    expect((await rows("SELECT code FROM codes")).length).toBe(1);
  });

  it("refuses a code past 10 chats", async () => {
    const fake = tg();
    const key = await register(fake);
    for (let i = 1; i <= 10; i++) await link(fake, { id: -i, type: "group", title: "g" + i }, key);
    const r = await call(fake, "POST", "/v1/codes", undefined, key);
    expect(r.status).toBe(409);
    expect(await r.text()).toBe("This panel has 10 chats, the most it can have.");
  });
});

describe("codes", () => {
  it("says what became of a code: waiting, used by a new chat, used by a chat linked before", async () => {
    const fake = tg();
    const key = await register(fake);
    const get = async (c: string, k = key) => {
      const r = await call(fake, "GET", "/v1/codes/" + c, undefined, k);
      return r.status === 200 ? r.json() : r.status;
    };
    const mk = async () => ((await (await call(fake, "POST", "/v1/codes", undefined, key)).json()) as { code: string }).code;
    const a = await mk();
    expect(await get(a)).toEqual({ expires_at: T0 + 600_000, used_at: 0, already: false, chat: null });
    await start(fake, "/start " + a);
    const me = { id: "7", type: "private", title: "Behzod Halil", linked_at: T0, gone_at: null };
    expect(await get(a)).toEqual({ expires_at: T0 + 600_000, used_at: T0, already: false, chat: me });
    const b = await mk();
    await start(fake, "/start " + b);
    expect(await get(b)).toEqual({ expires_at: T0 + 600_000, used_at: T0, already: true, chat: me });
    expect(await get(b, await register(fake, "other.test"))).toBe(404);
    expect(await get("AAAAAAAAAAAAAAAA")).toBe(404);
  });
});

describe("chats", () => {
  it("lists the panel's chats only, and unlinks one", async () => {
    const fake = tg();
    const key = await link(fake);
    const other = await link(fake, { id: -100, type: "supergroup", title: "Other ops" });
    const r = await call(fake, "GET", "/v1/chats", undefined, key);
    expect(await r.json()).toEqual({ bot: "useyawl_bot", chats: [{ id: "7", type: "private", title: "Behzod", linked_at: T0, gone_at: null }] });
    expect((await call(fake, "DELETE", "/v1/chats/-100", undefined, key)).status).toBe(404);
    expect((await call(fake, "DELETE", "/v1/chats/7", undefined, key)).status).toBe(204);
    expect(await (await call(fake, "GET", "/v1/chats", undefined, key)).json()).toEqual({ bot: "useyawl_bot", chats: [] });
    expect(((await (await call(fake, "GET", "/v1/chats", undefined, other)).json()) as { chats: unknown[] }).chats.length).toBe(1);
  });
});

describe("send", () => {
  it("sends to every chat of the panel, and to one when named (R4)", async () => {
    const fake = tg();
    const key = await link(fake);
    await link(fake, { id: -5, type: "group", title: "Acme ops" }, key);
    await link(fake, { id: 99, type: "private", first_name: "Stranger" });
    fake.calls.length = 0;
    const r = await call(fake, "POST", "/v1/send", { telegram: msg }, key);
    expect(r.status).toBe(200);
    expect(await r.json()).toEqual({ sent: 2, gone: 0 });
    expect(fake.sent().map((c) => c.body.chat_id).sort()).toEqual(["-5", "7"]);
    expect(fake.sent()[0].body).toMatchObject(msg);
    fake.calls.length = 0;
    expect((await call(fake, "POST", "/v1/send", { chat_id: -5, telegram: msg }, key)).status).toBe(200);
    expect(fake.sent().map((c) => c.body.chat_id)).toEqual(["-5"]);
    fake.calls.length = 0;
    const r2 = await call(fake, "POST", "/v1/send", { chat_id: "99", telegram: msg }, key);
    expect(r2.status).toBe(403);
    expect(fake.sent()).toEqual([]);
  });

  it("keeps only a record 71 payload: link buttons, no callbacks, no other fields", async () => {
    const fake = tg();
    const key = await link(fake);
    fake.calls.length = 0;
    const telegram = {
      ...msg,
      chat_id: 12345,
      disable_notification: true,
      reply_markup: { inline_keyboard: [[{ text: "Open", url: "https://panel.test/x", callback_data: "stop:pnl_0000000000000000" }]] },
    };
    expect((await call(fake, "POST", "/v1/send", { telegram }, key)).status).toBe(200);
    expect(fake.sent()[0].body).toEqual({ ...msg, disable_notification: true, reply_markup: { inline_keyboard: [[{ text: "Open", url: "https://panel.test/x" }]] }, chat_id: "7" });
  });

  it("refuses a message without text, and a panel with no chat", async () => {
    const fake = tg();
    const key = await register(fake);
    expect((await call(fake, "POST", "/v1/send", { telegram: { text: "" } }, key)).status).toBe(400);
    expect((await call(fake, "POST", "/v1/send", { telegram: { text: "x".repeat(4097) } }, key)).status).toBe(400);
    expect((await call(fake, "POST", "/v1/send", {}, key)).status).toBe(400);
    const r = await call(fake, "POST", "/v1/send", { telegram: msg }, key);
    expect(r.status).toBe(409);
    expect(await r.text()).toBe("No chat is linked.");
  });

  it("keeps no message text anywhere (R5)", async () => {
    const fake = tg((chat) => (chat === "-5" ? 403 : 200));
    const key = await link(fake);
    await link(fake, { id: -5, type: "group", title: "Acme ops" }, key);
    await call(fake, "POST", "/v1/send", { telegram: { text: "secret-words" } }, key);
    for (const t of ["panels", "codes", "chats"]) expect(JSON.stringify(await rows(`SELECT * FROM ${t}`))).not.toContain("secret-words");
  });

  it("waits out Telegram's retry_after, then gives up past 10 seconds (R6)", async () => {
    const fake = tg((chat, n) => (chat === "7" && n === 2 ? { status: 429, retry_after: 3 } : chat === "8" ? { status: 429, retry_after: 6 } : 200));
    const key = await link(fake);
    fake.calls.length = 0;
    expect((await call(fake, "POST", "/v1/send", { telegram: msg }, key)).status).toBe(200);
    expect(fake.waits).toEqual([3000]);
    expect(fake.sent().length).toBe(2);
    await link(fake, { id: 8, type: "private", first_name: "Slow" }, key);
    fake.waits.length = 0;
    const r = await call(fake, "POST", "/v1/send", { chat_id: 8, telegram: msg }, key);
    expect(r.status).toBe(502);
    expect(await r.text()).toBe("Telegram did not take the message in 1 of 1 chats.");
    expect(fake.waits).toEqual([6000]);
  });

  it("marks a chat gone on 403 and says so through /v1/chats (R6)", async () => {
    const fake = tg((chat) => (chat === "-5" ? 403 : 200));
    const key = await link(fake);
    await link(fake, { id: -5, type: "group", title: "Acme ops" }, key);
    const r = await call(fake, "POST", "/v1/send", { telegram: msg }, key, T0 + 1000);
    expect(await r.json()).toEqual({ sent: 1, gone: 1 });
    const { chats } = (await (await call(fake, "GET", "/v1/chats", undefined, key)).json()) as { chats: { id: string; gone_at: number | null }[] };
    expect(chats.find((c) => c.id === "-5")!.gone_at).toBe(T0 + 1000);
    fake.calls.length = 0;
    await call(fake, "POST", "/v1/send", { telegram: msg }, key);
    expect(fake.sent().map((c) => c.body.chat_id)).toEqual(["7"]);
    expect((await call(fake, "POST", "/v1/send", { chat_id: -5, telegram: msg }, key)).status).toBe(409);
    const { code } = (await (await call(fake, "POST", "/v1/codes", undefined, key)).json()) as { code: string };
    await start(fake, "/start@useyawl_bot " + code, { id: -5, type: "group", title: "Acme ops" });
    expect(await rows("SELECT gone_at FROM chats WHERE chat_id = '-5'")).toEqual([{ gone_at: null }]);
  });

  it("allows 60 sends a minute per panel, then 429 until the next minute (R6)", async () => {
    const fake = tg();
    const key = await link(fake);
    const other = await link(fake, { id: 9, type: "private", first_name: "B" });
    for (let i = 0; i < 60; i++) expect((await call(fake, "POST", "/v1/send", { telegram: msg }, key, T0 + i)).status).toBe(200);
    const r = await call(fake, "POST", "/v1/send", { telegram: msg }, key, T0 + 100);
    expect(r.status).toBe(429);
    expect(Number(r.headers.get("retry-after"))).toBeGreaterThan(0);
    expect((await call(fake, "POST", "/v1/send", { telegram: msg }, other, T0 + 100)).status).toBe(200);
    const next = Math.floor(T0 / 60_000) * 60_000 + 60_000;
    expect((await call(fake, "POST", "/v1/send", { telegram: msg }, key, next)).status).toBe(200);
  });
});
