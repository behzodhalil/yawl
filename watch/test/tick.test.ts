import { env } from "cloudflare:workers";
import { beforeEach, describe, expect, it } from "vitest";
import { tick } from "../src/tick";
import { T0, call, exp, expect_, reset, rows } from "./helpers";

const MIN = 60_000;
type Answer = number | "hang";

function fake(answers: Record<string, Answer>, tg: number = 200) {
  const calls: { url: string; init?: RequestInit }[] = [];
  const f = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.startsWith("http://telegram.test/")) return new Response("{}", { status: tg });
    const a = answers[url] ?? 200;
    if (a === "hang")
      return new Promise<Response>((_, reject) => init?.signal?.addEventListener("abort", () => reject(new Error("aborted"))));
    return new Response("", { status: a });
  }) as typeof fetch;
  return { f, calls, sent: () => calls.filter((c) => c.url.startsWith("http://telegram.test/")).map((c) => JSON.parse(String(c.init?.body)).text as string) };
}

function counted() {
  let batches = 0;
  const db = new Proxy(env.DB, {
    get(t, k) {
      if (k === "batch") return (s: D1PreparedStatement[]) => (batches++, t.batch(s));
      const v = Reflect.get(t, k);
      return typeof v === "function" ? v.bind(t) : v;
    },
  });
  return { db, batches: () => batches };
}

const cfg = { panel_url: "https://panel.test", watch_url: "https://watch.test", tz: "UTC" };
const run = (f: typeof fetch, now: number, db: D1Database = env.DB) => tick(db, env, now, f, { timeoutMs: 50 });
const signal = (subject: string, now: number, id = "panel") => call("POST", "/signal", { subject }, now, id);

const api = (n = 1) =>
  Array.from({ length: n }, (_, i) =>
    exp({ id: `u${i}`, kind: "url", subject: `service:s${i}`, label: `acme/production/api${i}`, target: `https://api${i}.test/`, server: "srv_1" }),
  );
const base = [exp({ id: "srv_1", kind: "server", subject: "server:srv_1", label: "hel1" }), exp({ id: "panel", kind: "panel", subject: "panel", label: "The panel", server: "srv_0" })];

async function alive(now: number) {
  await signal("panel", now);
  await signal("server:srv_1", now, "srv_1");
}

beforeEach(reset);

describe("tick", () => {
  it("url opens after two misses and closes with its length", async () => {
    await expect_([...base, ...api()], cfg);
    const bad = fake({ "https://api0.test/": 502 });
    await alive(T0 + MIN);
    await run(bad.f, T0 + MIN);
    expect(await rows("SELECT count(*) AS n FROM incidents")).toEqual([{ n: 0 }]);
    await alive(T0 + 2 * MIN);
    await run(bad.f, T0 + 2 * MIN);
    const since = new Date(T0 + MIN).toISOString().slice(11, 16);
    expect(bad.sent()).toEqual([`acme/production/api0 is down: GET https://api0.test/ answered 502 twice. Since ${since}.\nhttps://panel.test/incidents`]);
    const good = fake({});
    await alive(T0 + 9 * MIN);
    await run(good.f, T0 + 9 * MIN);
    expect(good.sent()).toEqual(["acme/production/api0 is back after 7 minutes.\nhttps://panel.test/incidents"]);
    expect(await rows("SELECT closed_at FROM incidents")).toEqual([{ closed_at: T0 + 9 * MIN }]);
  });

  it("a timeout is a miss", async () => {
    await expect_([...base, ...api()], cfg);
    const hang = fake({ "https://api0.test/": "hang" });
    for (const m of [1, 2]) {
      await alive(T0 + m * MIN);
      await run(hang.f, T0 + m * MIN);
    }
    expect(hang.sent()[0]).toMatch(/^acme\/production\/api0 is down: GET https:\/\/api0.test\/ did not answer within 10 s twice\. Since /);
  });

  it("a 3xx is up and is not followed", async () => {
    await expect_([...base, ...api()], cfg);
    const moved = fake({ "https://api0.test/": 301 });
    for (const m of [1, 2]) {
      await alive(T0 + m * MIN);
      await run(moved.f, T0 + m * MIN);
    }
    expect(moved.sent()).toEqual([]);
    expect(moved.calls[0].init?.redirect).toBe("manual");
  });

  it("server folds its services", async () => {
    await expect_([...base, ...api(3)], cfg);
    await signal("panel", T0);
    await signal("server:srv_1", T0, "srv_1");
    const f = fake({ "https://api0.test/": 502, "https://api1.test/": 502, "https://api2.test/": 502 });
    await signal("panel", T0 + 3 * MIN + 1000);
    await run(f.f, T0 + 3 * MIN + 1000);
    expect(f.calls.filter((c) => !c.url.startsWith("http://telegram")).length).toBe(0);
    expect(f.sent()).toEqual(["hel1 is not answering. 3 services on it are not checked separately until it is back.\nhttps://panel.test/incidents"]);
    await signal("panel", T0 + 4 * MIN + 1000);
    await run(f.f, T0 + 4 * MIN + 1000);
    expect(await rows("SELECT subject FROM incidents")).toEqual([{ subject: "server:srv_1" }]);
    await alive(T0 + 5 * MIN);
    const g = fake({});
    await run(g.f, T0 + 5 * MIN);
    expect(g.sent()).toEqual(["hel1 is back after 1 minute.\nhttps://panel.test/incidents"]);
    expect(g.calls.filter((c) => !c.url.startsWith("http://telegram")).length).toBe(3);
  });

  it("says 1 service, and nothing when none", async () => {
    await expect_([...base, ...api(1), exp({ id: "srv_2", kind: "server", subject: "server:srv_2", label: "hk1" })], cfg);
    await signal("panel", T0 + 4 * MIN);
    const f = fake({});
    await run(f.f, T0 + 4 * MIN);
    expect(f.sent().sort()).toEqual([
      "hel1 is not answering. 1 service on it is not checked separately until it is back.\nhttps://panel.test/incidents",
      "hk1 is not answering.\nhttps://panel.test/incidents",
    ]);
  });

  it("panel alone", async () => {
    await expect_([...base, ...api()], cfg);
    await signal("server:srv_1", T0 + 4 * MIN, "srv_1");
    const f = fake({});
    await run(f.f, T0 + 4 * MIN);
    const body = JSON.parse(String(f.calls.find((c) => c.url.startsWith("http://telegram.test/"))?.init?.body));
    expect(body).toEqual({
      text: "\u24E7 <b>Panel is offline</b>\n\nPanel: <code>panel.test</code>\nValue: no signal for 4 minutes (limit 3 minutes)\nTime: 04:30 UTC",
      parse_mode: "HTML",
      disable_web_page_preview: true,
      reply_markup: { inline_keyboard: [[{ text: "Status page", url: "https://watch.test/status" }]] },
      chat_id: body.chat_id,
    });
    expect((await rows("SELECT cause FROM incidents WHERE subject = 'panel'"))[0]).toEqual({ cause: "The panel is not answering." });
    expect(f.calls.some((c) => c.url === "https://api0.test/")).toBe(true);
  });

  it("panel folds into its machine", async () => {
    await expect_([exp({ id: "srv_1", kind: "server", subject: "server:srv_1", label: "hel1" }), exp({ id: "panel", kind: "panel", subject: "panel", label: "The panel", server: "srv_1" })], cfg);
    const f = fake({});
    await run(f.f, T0 + 4 * MIN);
    expect(await rows("SELECT subject FROM incidents")).toEqual([{ subject: "server:srv_1" }]);
  });

  it("heartbeat opens at once and a call closes it", async () => {
    await expect_([...base, exp({ id: "h1", kind: "heartbeat", subject: "hb:h1", label: "laptop backup", target: "tok", period_s: 60, grace_s: 30 })], cfg);
    await alive(T0 + 91_000);
    const f = fake({});
    await run(f.f, T0 + 91_000);
    expect(f.sent()).toEqual(["laptop backup did not check in: expected every 1 minute, last call never.\nhttps://panel.test/incidents"]);
    await (await import("../src/routes")).handle(new Request("https://watch.test/hb/tok"), env, T0 + 100_000);
    const g = fake({});
    await run(g.f, T0 + 120_000);
    expect(g.sent()).toEqual(["laptop backup is back after less than a minute.\nhttps://panel.test/incidents"]);
  });

  it("backup opens at once", async () => {
    const H = 3_600_000;
    await expect_([...base, exp({ id: "b1", kind: "backup", subject: "backup:db_1", label: "acme/production/db backups" })], cfg, T0 - 30 * H);
    await call("POST", "/signal", { subject: "backup:db_1", at: T0 - 27 * H }, T0);
    await alive(T0);
    const f = fake({});
    await run(f.f, T0);
    expect(f.sent()).toEqual(["acme/production/db backups: no backup has succeeded in 26 hours (last: 22 Dec 01:26).\nhttps://panel.test/incidents"]);
  });

  it("stays under the subrequest budget", async () => {
    const urls = api(60);
    const crons = Array.from({ length: 20 }, (_, i) =>
      exp({ id: `c${i}`, kind: "cron", subject: `cron:c${i}`, label: `job${i}`, target: "Asia/Tashkent * * * * *", server: "srv_1", period_s: 3600 }),
    );
    await expect_([...base, ...urls, ...crons], cfg);
    const answers = Object.fromEntries(urls.map((u) => [u.target, 500])) as Record<string, Answer>;
    const f = fake(answers, 500);
    const seen = new Set<string>();
    for (const m of [1, 2]) {
      await alive(T0 + m * MIN);
      const c = counted();
      const before = f.calls.length;
      await run(f.f, T0 + m * MIN, c.db);
      const these = f.calls.slice(before);
      const fetched = these.filter((x) => !x.url.startsWith("http://telegram"));
      fetched.forEach((x) => seen.add(x.url));
      expect(fetched.length).toBe(40);
      expect(these.length - fetched.length).toBeLessThanOrEqual(5);
      expect(c.batches()).toBeLessThanOrEqual(2);
    }
    expect(seen.size).toBe(60);
    const out = await rows<{ attempts: number; next_at: number; sent_at: number | null }>("SELECT attempts, next_at, sent_at FROM outbox");
    expect(out.length).toBe(20);
    expect(out.every((r) => r.sent_at === null)).toBe(true);
    expect(out.filter((r) => r.attempts === 1 && r.next_at > T0 + 2 * MIN).length).toBe(5);
  });

  it("outbox gives up after 24 h", async () => {
    await expect_([...base], cfg);
    await env.DB.prepare("INSERT INTO outbox (id, text, attempts, next_at, created_at) VALUES ('o1', 'old', 3, 0, ?)").bind(T0 - 25 * 3_600_000).run();
    await alive(T0);
    const f = fake({});
    await run(f.f, T0);
    expect(f.sent()).toEqual([]);
    expect(await rows("SELECT count(*) AS n FROM outbox")).toEqual([{ n: 0 }]);
  });

  it("each result keeps the time it was checked", async () => {
    await expect_([...base, ...api(1)], cfg);
    await alive(T0);
    await run(fake({}).f, T0);
    await signal("panel", T0 + 4 * MIN);
    await run(fake({}).f, T0 + 4 * MIN);
    const [r] = await rows<{ results: string }>("SELECT results FROM last_run");
    const res = JSON.parse(r.results);
    expect(res["service:s0"].at).toBe(T0);
    expect(res["server:srv_1"].at).toBe(T0 + 4 * MIN);
  });

  it("last_run written once", async () => {
    await expect_([...base, ...api(2)], cfg);
    await alive(T0);
    const c = counted();
    await run(fake({}).f, T0, c.db);
    const [r] = await rows<{ at: number; results: string }>("SELECT at, results FROM last_run");
    expect(r.at).toBe(T0);
    const res = JSON.parse(r.results);
    expect(res["service:s0"]).toMatchObject({ ok: true, detail: "" });
    expect(typeof res["service:s0"].ms).toBe("number");
    expect(res["server:srv_1"]).toMatchObject({ ok: true });
    expect(c.batches()).toBe(2);
  });
});
