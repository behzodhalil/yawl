import { exports } from "cloudflare:workers";
import { beforeEach, expect, it } from "vitest";
import { req, reset } from "./helpers";

const worker = (r: Request) => exports.default.fetch(r);
const sent = async () => (await (await fetch("http://telegram.test/sent")).json()) as { method: string; body: Record<string, unknown> }[];

beforeEach(async () => {
  await reset();
  await sent();
});

async function linked(chat = 7): Promise<string> {
  const { key } = (await (await worker(req("POST", "/v1/panels", { host: "panel.acme.test" }))).json()) as { key: string };
  const { code } = (await (await worker(req("POST", "/v1/codes", undefined, key))).json()) as { code: string };
  const hook = req("POST", "/telegram/webhook", { update_id: 1, message: { message_id: 1, text: "/start " + code, chat: { id: chat, type: "private", first_name: "Behzod" } } }, undefined, {
    "x-telegram-bot-api-secret-token": "hook-secret",
  });
  expect((await worker(hook)).status).toBe(200);
  return key;
}

it("the webhook's reply reaches Telegram", async () => {
  await linked();
  expect(await sent()).toEqual([{ method: "sendMessage", body: { chat_id: 7, text: "Linked to panel.acme.test." } }]);
});

it("POST /v1/send reaches Telegram", async () => {
  const key = await linked();
  await sent();
  const r = await worker(req("POST", "/v1/send", { chat_id: "7", telegram: { text: "api is down" } }, key));
  expect(r.status).toBe(200);
  expect(await r.json()).toEqual({ sent: 1, gone: 0 });
  expect(await sent()).toEqual([{ method: "sendMessage", body: { text: "api is down", chat_id: "7" } }]);
});
