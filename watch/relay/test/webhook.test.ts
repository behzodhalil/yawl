import { env } from "cloudflare:workers";
import { beforeEach, describe, expect, it } from "vitest";
import { T0, call, hook, register, reset, rows, start, tg } from "./helpers";

beforeEach(reset);

const code = async (fake: ReturnType<typeof tg>, key: string, now = T0) =>
  ((await (await call(fake, "POST", "/v1/codes", undefined, key, now)).json()) as { code: string }).code;
const said = (fake: ReturnType<typeof tg>) => fake.sent().map((c) => c.body.text);

describe("webhook", () => {
  it("refuses a caller without Telegram's secret (R1)", async () => {
    const fake = tg();
    const key = await register(fake);
    const c = await code(fake, key);
    for (const secret of ["", "wrong"]) {
      const r = await hook(fake, { message: { text: "/start " + c, chat: { id: 7, type: "private" } } }, T0, secret);
      expect(r.status).toBe(403);
    }
    expect(await rows("SELECT * FROM chats")).toEqual([]);
  });

  it("links a private chat on /start <code>, once (R3, U5)", async () => {
    const fake = tg();
    const key = await register(fake, "panel.acme.test");
    const c = await code(fake, key);
    expect((await start(fake, "/start " + c)).status).toBe(200);
    expect(await rows("SELECT chat_id, type, title, linked_at FROM chats")).toEqual([{ chat_id: "7", type: "private", title: "Behzod Halil", linked_at: T0 }]);
    expect(said(fake)).toEqual(["Linked to panel.acme.test."]);
    expect(await rows("SELECT used_at, chat_id, already FROM codes")).toEqual([{ used_at: T0, chat_id: "7", already: 0 }]);
    await start(fake, "/start " + c, { id: 8, type: "private", first_name: "Late" });
    expect(said(fake)[1]).toBe("This link has expired. Make a new one in your panel.");
    expect((await rows("SELECT * FROM chats")).length).toBe(1);
  });

  it("links a group on /start@bot <code> and ignores commands to other bots", async () => {
    const fake = tg();
    const key = await register(fake);
    const c = await code(fake, key);
    const group = { id: -1001, type: "supergroup", title: "Acme ops" };
    await start(fake, `/start@other_bot ${c}`, group);
    expect(await rows("SELECT * FROM chats")).toEqual([]);
    expect(fake.calls).toEqual([]);
    await start(fake, `/start@UseYawl_bot ${c}`, group);
    expect(await rows("SELECT chat_id, type, title FROM chats")).toEqual([{ chat_id: "-1001", type: "supergroup", title: "Acme ops" }]);
  });

  it("links a group the bot was just added to through ?startgroup", async () => {
    const fake = tg();
    const key = await register(fake);
    const c = await code(fake, key);
    const group = { id: -42, type: "group", title: "Acme ops" };
    // Telegram first says the bot joined, then sends the command from the link
    await hook(fake, { message: { message_id: 1, chat: group, new_chat_members: [{ id: 1, is_bot: true, username: "useyawl_bot" }] } });
    await hook(fake, { my_chat_member: { chat: group, new_chat_member: { status: "member" } } });
    expect(fake.calls).toEqual([]);
    await start(fake, `/start@useyawl_bot ${c}`, group);
    expect(await rows("SELECT chat_id, type, title FROM chats")).toEqual([{ chat_id: "-42", type: "group", title: "Acme ops" }]);
    expect(said(fake)).toEqual(["Linked to panel.acme.test."]);
  });

  it("uses up a code on a chat linked before, says so, and keeps its first link time", async () => {
    const fake = tg();
    const key = await register(fake);
    await start(fake, "/start " + (await code(fake, key)));
    const c = await code(fake, key);
    await start(fake, "/start " + c, undefined, T0 + 60_000);
    expect(said(fake)).toEqual(["Linked to panel.acme.test.", "This chat was already linked to panel.acme.test."]);
    expect(await rows("SELECT chat_id, linked_at FROM chats")).toEqual([{ chat_id: "7", linked_at: T0 }]);
    expect(await rows("SELECT used_at, already FROM codes WHERE code = ?", c)).toEqual([{ used_at: T0 + 60_000, already: 1 }]);
    await env.DB.prepare("UPDATE chats SET gone_at = 5").run();
    await start(fake, "/start " + (await code(fake, key)), undefined, T0 + 120_000);
    expect(said(fake).at(-1)).toBe("Linked to panel.acme.test.");
    expect(await rows("SELECT linked_at, gone_at FROM chats")).toEqual([{ linked_at: T0 + 120_000, gone_at: null }]);
  });

  it("says when a code expired, and when no code came", async () => {
    const fake = tg();
    const key = await register(fake);
    const c = await code(fake, key);
    await start(fake, "/start " + c, undefined, T0 + 600_000);
    await start(fake, "/start");
    expect(said(fake)).toEqual(["This link has expired. Make a new one in your panel.", "Press Connect Telegram in your panel to link this chat."]);
    expect(await rows("SELECT * FROM chats")).toEqual([]);
  });

  it("links no 11th chat", async () => {
    const fake = tg();
    const key = await register(fake);
    for (let i = 1; i <= 10; i++) await start(fake, "/start " + (await code(fake, key)), { id: -i, type: "group", title: "g" });
    const [{ id }] = await rows<{ id: string }>("SELECT id FROM panels");
    await env.DB.prepare("INSERT INTO codes (code, panel_id, expires_at) VALUES ('spare0000000000', ?, ?)").bind(id, T0 + 600_000).run();
    await start(fake, "/start spare0000000000", { id: -11, type: "group", title: "g" });
    expect(said(fake).at(-1)).toBe("This panel has 10 chats already. Disconnect one in the panel first.");
    expect((await rows("SELECT * FROM chats")).length).toBe(10);
  });

  it("/stop lists the chat's panels and a button stops one (U5)", async () => {
    const fake = tg();
    const a = await register(fake, "a.test");
    const b = await register(fake, "b.test");
    await start(fake, "/start " + (await code(fake, a)));
    await start(fake, "/start " + (await code(fake, b)));
    fake.calls.length = 0;
    await start(fake, "/stop");
    const list = fake.sent()[0].body as { text: string; reply_markup: { inline_keyboard: { text: string; callback_data: string }[][] } };
    expect(list.text).toBe("This chat gets alerts from these panels. Press one to stop it.");
    expect(list.reply_markup.inline_keyboard.map((r) => r[0].text)).toEqual(["Stop a.test", "Stop b.test"]);
    fake.calls.length = 0;
    const data = list.reply_markup.inline_keyboard[0][0].callback_data;
    await hook(fake, { callback_query: { id: "cb1", data, message: { message_id: 2, chat: { id: 7, type: "private" } } } });
    expect(said(fake)).toEqual(["No more alerts from a.test here."]);
    expect(fake.calls.at(-1)).toEqual({ method: "answerCallbackQuery", body: { callback_query_id: "cb1", text: "No more alerts from a.test here." } });
    const [{ id: bID }] = await rows<{ id: string }>("SELECT id FROM panels WHERE host = 'b.test'");
    expect(await rows("SELECT panel_id FROM chats")).toEqual([{ panel_id: bID }]);
    fake.calls.length = 0;
    await hook(fake, { callback_query: { id: "cb2", data, message: { message_id: 2, chat: { id: 7, type: "private" } } } });
    expect(fake.calls).toEqual([{ method: "answerCallbackQuery", body: { callback_query_id: "cb2", text: "Already stopped." } }]);
    fake.calls.length = 0;
    await start(fake, "/stop@useyawl_bot", { id: 55, type: "group", title: "x" });
    expect(said(fake)).toEqual(["This chat gets no alerts."]);
  });

  it("answers updates it does not handle with 200, so Telegram does not send them again", async () => {
    const fake = tg();
    expect((await hook(fake, { edited_message: { text: "hi" } })).status).toBe(200);
    expect((await hook(fake, { message: { text: "hello", chat: { id: 1, type: "private" } } })).status).toBe(200);
    expect(fake.calls).toEqual([]);
  });
});

