import { env } from "cloudflare:workers";
import { beforeEach, describe, expect, it } from "vitest";
import vec from "../../testdata/cron.json";
import { matches, parse, partsIn } from "../src/cron";
import { tick } from "../src/tick";
import { T0, call, exp, expect_, reset, rows } from "./helpers";

const MIN = 60_000;
const D = Date.UTC(2026, 9, 27); // 27 Oct 2026 00:00 UTC
const at = (h: number, m: number) => D + h * 3_600_000 + m * MIN;

describe("schedule syntax", () => {
  it("matches the Go vectors", () => {
    for (const c of vec.matches) {
      const s = parse(c.expr);
      if (typeof s === "string") throw new Error(c.expr + ": " + s);
      expect([c.expr, c.tz, c.at, matches(s, partsIn(Date.parse(c.at), new Intl.DateTimeFormat("en-GB", fmtOpts(c.tz))))]).toEqual([c.expr, c.tz, c.at, c.want]);
    }
    for (const c of vec.bad) expect(parse(c.expr)).toBe(c.error);
  });
});

function fmtOpts(tz: string): Intl.DateTimeFormatOptions {
  return { timeZone: tz, minute: "numeric", hour: "numeric", day: "numeric", month: "numeric", weekday: "short", hourCycle: "h23" };
}

const job = exp({ id: "cron:svc_1", kind: "cron", subject: "cron:svc_1", label: "acme/production/report", target: "UTC 0 3 * * *", server: "srv_a", period_s: 3600 });
const cfg = { panel_url: "https://panel.test", watch_url: "https://watch.test", tz: "UTC" };

function fake() {
  const sent: string[] = [];
  const f = (async (input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).startsWith("http://telegram.test/")) sent.push(JSON.parse(String(init?.body)).text.split("\n")[0]);
    return new Response("{}");
  }) as typeof fetch;
  return { f, sent };
}

const report = (body: object, now: number, id = "srv_a") => call("POST", "/cron", { subject: "cron:svc_1", ...body }, now, id);
const run = async (now: number) => {
  const t = fake();
  await call("POST", "/signal", { subject: "panel" }, now);
  await tick(env.DB, env, now, t.f, { timeoutMs: 50 });
  return t.sent;
};

beforeEach(async () => {
  await reset();
  await expect_([job], cfg, D);
});

describe("cron jobs", () => {
  it("a run that never starts", async () => {
    expect(await run(at(3, 0))).toEqual([]);
    expect(await run(at(3, 1))).toEqual([]);
    expect(await run(at(3, 2))).toEqual(["acme/production/report did not run at 03:00 (UTC). Last run: never."]);
    expect(await run(at(3, 3))).toEqual([]);
  });

  it("names the last run when one is missed", async () => {
    const y = at(3, 0) - 86_400_000;
    await report({ event: "start", scheduled_at: y }, y + 1000);
    await report({ event: "finish", scheduled_at: y, exit: 0, ms: 41_000 }, y + 42_000);
    await run(at(3, 1));
    expect(await run(at(3, 2))).toEqual(["acme/production/report did not run at 03:00 (UTC). Last run: 26 Oct 03:00, 41 s, exit 0."]);
  });

  it("a run that starts on time is met", async () => {
    await report({ event: "start", scheduled_at: at(3, 0) }, at(3, 0) + 500);
    await run(at(3, 1));
    expect(await run(at(3, 2))).toEqual([]);
  });

  it("a skip is not a miss", async () => {
    await report({ event: "skip", scheduled_at: at(3, 0) }, at(3, 0) + 500);
    await run(at(3, 1));
    expect(await run(at(3, 2))).toEqual([]);
  });

  it("a failed finish opens at once and a clean one closes it", async () => {
    await report({ event: "start", scheduled_at: at(3, 0) }, at(3, 0) + 500);
    await report({ event: "finish", scheduled_at: at(3, 0), exit: 1, ms: 41_000 }, at(3, 0) + 41_500);
    expect(await rows("SELECT subject, source, cause FROM incidents")).toEqual([
      { subject: "cron:svc_1", source: "watch", cause: "acme/production/report failed at 03:00 (UTC): exit 1 after 41 s." },
    ]);
    const next = at(3, 0) + 86_400_000;
    await report({ event: "start", scheduled_at: next }, next + 500);
    await report({ event: "finish", scheduled_at: next, exit: 0, ms: 5000 }, next + 5500);
    expect(await rows("SELECT closed_at FROM incidents")).toEqual([{ closed_at: next + 5500 }]);
    expect((await rows<{ text: string }>("SELECT text FROM outbox ORDER BY created_at")).map((r) => r.text.split("\n")[0])).toEqual([
      "acme/production/report failed at 03:00 (UTC): exit 1 after 41 s.",
      "acme/production/report is back after 23 hours 59 minutes.",
    ]);
  });

  it("a run past its limit", async () => {
    await report({ event: "start", scheduled_at: at(3, 0) }, at(3, 0) + 500);
    await report({ event: "finish", scheduled_at: at(3, 0), exit: -1, timed_out: true, ms: 3_600_000 }, at(4, 0) + 500);
    expect(await rows("SELECT cause FROM incidents")).toEqual([{ cause: "acme/production/report failed at 03:00 (UTC): stopped at its 1 hour limit." }]);
  });

  it("a run that does not finish", async () => {
    await report({ event: "start", scheduled_at: at(3, 0) }, at(3, 0) + 500);
    expect(await run(at(4, 1))).toEqual([]);
    expect(await run(at(4, 3))).toEqual(["acme/production/report started at 03:00 (UTC) and has not finished within its 1 hour limit."]);
  });

  it("only the job's own server may report", async () => {
    const r = await report({ event: "finish", scheduled_at: at(3, 0), exit: 1, ms: 1 }, at(3, 0), "srv_b");
    expect(r.status).toBe(403);
    expect(await rows("SELECT count(*) AS n FROM incidents")).toEqual([{ n: 0 }]);
    const p = await call("POST", "/cron", { subject: "cron:svc_1", event: "finish", scheduled_at: at(3, 0), exit: 1 }, at(3, 0));
    expect(p.status).toBe(403);
  });

  it("takes the panel's word for a failed run it did not hear", async () => {
    const r = await call("POST", "/incident", { subject: "cron:svc_1", action: "open", label: "acme/production/report", text: "acme/production/report failed at 03:00 (UTC): exit 1 after 41 s." }, at(3, 2));
    expect(await r.json()).toEqual({ ok: true });
    expect(await rows("SELECT source FROM incidents WHERE closed_at IS NULL")).toEqual([{ source: "panel" }]);
    await report({ event: "start", scheduled_at: at(3, 0) + 86_400_000 }, at(3, 0) + 86_400_000);
    await report({ event: "finish", scheduled_at: at(3, 0) + 86_400_000, exit: 0, ms: 1000 }, at(3, 0) + 86_401_000);
    expect(await rows("SELECT count(*) AS n FROM incidents WHERE closed_at IS NULL")).toEqual([{ n: 0 }]);
  });

  it("opens one incident for two misses in one tick", async () => {
    await reset();
    await expect_([{ ...job, target: "UTC 0,1 3 * * *" }], cfg, D);
    expect(await run(at(2, 50))).toEqual([]);
    expect(await run(at(3, 5))).toEqual(["acme/production/report did not run at 03:00 (UTC). Last run: never."]);
    expect(await rows("SELECT count(*) AS n FROM incidents")).toEqual([{ n: 1 }]);
    const checks = (await (await call("GET", "/checks", undefined, at(3, 5))).json()) as { results: Record<string, { ok: boolean }> };
    expect(checks.results["cron:svc_1"].ok).toBe(false);
  });

  it("a new job is not judged before it existed", async () => {
    await reset();
    await expect_([job], cfg, at(3, 1));
    expect(await run(at(3, 2))).toEqual([]);
    expect(await run(at(3, 3))).toEqual([]);
  });

  it("follows the job's zone", async () => {
    await reset();
    await expect_([{ ...job, target: "Asia/Tashkent 0 3 * * *" }], cfg, D - 86_400_000);
    await run(at(22, 1) - 86_400_000);
    expect(await run(at(22, 2) - 86_400_000)).toEqual(["acme/production/report did not run at 03:00 (Asia/Tashkent). Last run: never."]);
  });
});
