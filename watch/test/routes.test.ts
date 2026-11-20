import { env } from "cloudflare:workers";
import { beforeEach, describe, expect, it } from "vitest";
import { handle } from "../src/routes";
import { agentKey } from "../src/sign";
import { T0, call, exp, expect_, panelKey, reset, rows, signed } from "./helpers";

beforeEach(reset);

describe("routes", () => {
  it("takes a server's signal only with its own key", async () => {
    const r = await call("POST", "/signal", { subject: "server:srv_a" }, T0, "srv_a");
    expect(r.status).toBe(200);
    expect(await r.json()).toEqual({ ack: T0 });
    expect(await rows("SELECT subject, at FROM signals")).toEqual([{ subject: "server:srv_a", at: T0 }]);
    expect((await call("POST", "/signal", { subject: "server:srv_b" }, T0, "srv_a")).status).toBe(403);
  });

  it("stamps signals with its own clock except a panel backup", async () => {
    await call("POST", "/signal", { subject: "panel", at: 5 });
    await call("POST", "/signal", { subject: "backup:db_1", at: 5 });
    await call("POST", "/signal", { subject: "server:srv_a", at: 5 }, T0, "srv_a");
    expect(await rows("SELECT subject, at FROM signals ORDER BY subject")).toEqual([
      { subject: "backup:db_1", at: 5 },
      { subject: "panel", at: T0 },
      { subject: "server:srv_a", at: T0 },
    ]);
  });

  it("replaces the list and closes what was removed", async () => {
    await expect_([exp({ id: "u1", kind: "url", subject: "service:a" }), exp({ id: "u2", kind: "url", subject: "service:b" })], {
      panel_url: "https://panel.test",
    });
    await env.DB.prepare(
      "INSERT INTO incidents (id, subject, kind, label, source, opened_at, cause) VALUES ('inc_1','service:b','url','b','watch',?, 'down')",
    )
      .bind(T0)
      .run();
    await expect_([exp({ id: "u1", kind: "url", subject: "service:a" })], {}, T0 + 1000);
    expect(await rows("SELECT id FROM expectations")).toEqual([{ id: "u1" }]);
    expect(await rows("SELECT closed_at, cause FROM incidents")).toEqual([{ closed_at: T0 + 1000, cause: "removed" }]);
    expect(await rows("SELECT count(*) AS n FROM outbox")).toEqual([{ n: 0 }]);
    expect(await rows("SELECT created_at FROM expectations")).toEqual([{ created_at: T0 }]);
    expect(await rows("SELECT value FROM config WHERE key = 'panel_url'")).toEqual([{ value: "https://panel.test" }]);
  });

  it("tells the panel when it last ran and how old its backlog is", async () => {
    let r = await call("POST", "/signal", { subject: "panel" });
    expect(await r.json()).toEqual({ ack: T0, last_run: 0, backlog_at: 0 });
    await env.DB.prepare("INSERT INTO last_run (id, at, results) VALUES (1, ?, '{}')").bind(T0 - 5000).run();
    await env.DB.prepare("INSERT INTO outbox (id, text, attempts, next_at, created_at) VALUES ('o1', 'x', 1, 0, ?), ('o2', 'y', 0, 0, ?)").bind(T0 - 9000, T0 - 1000).run();
    await env.DB.prepare("INSERT INTO outbox (id, text, attempts, next_at, created_at, sent_at) VALUES ('o0', 'z', 0, 0, ?, ?)").bind(T0 - 99999, T0).run();
    r = await call("POST", "/signal", { subject: "panel" });
    expect(await r.json()).toEqual({ ack: T0, last_run: T0 - 5000, backlog_at: T0 - 9000 });
  });

  it("closes the panel's incidents on a reset, without a message", async () => {
    await call("POST", "/incident", { subject: "deploy:svc_1", action: "open", label: "a", text: "x" });
    await env.DB.prepare(
      "INSERT INTO incidents (id, subject, kind, label, source, opened_at) VALUES ('w1','server:srv_1','server','hel1','watch',?)",
    ).bind(T0).run();
    await call("POST", "/expectations", { config: {}, expectations: [], reset_panel: true }, T0 + 1000);
    expect(await rows("SELECT id, closed_at FROM incidents WHERE source = 'panel'")).toEqual([{ id: expect.any(String), closed_at: T0 + 1000 }]);
    expect(await rows("SELECT closed_at FROM incidents WHERE id = 'w1'")).toEqual([{ closed_at: null }]);
    expect(await rows("SELECT count(*) AS n FROM outbox")).toEqual([{ n: 1 }]);
  });

  it("takes a heartbeat by its token, unsigned", async () => {
    await expect_([exp({ id: "h1", kind: "heartbeat", subject: "hb:h1", target: "tok123", period_s: 60, grace_s: 30 })]);
    expect((await handle(new Request("https://watch.test/hb/tok123"), env, T0 + 1)).status).toBe(200);
    expect((await handle(new Request("https://watch.test/hb/tok123", { method: "POST" }), env, T0 + 2)).status).toBe(200);
    expect(await rows("SELECT subject, at FROM signals")).toEqual([{ subject: "hb:h1", at: T0 + 2 }]);
    expect((await handle(new Request("https://watch.test/hb/nope"), env, T0)).status).toBe(404);
  });

  it("finds a heartbeat token by index", async () => {
    const plan = await rows<{ detail: string }>("EXPLAIN QUERY PLAN SELECT subject FROM expectations WHERE kind = 'heartbeat' AND target = ?", "nope");
    expect(plan.map((p) => p.detail).join(" ")).toContain("USING INDEX expectations_target");
  });

  it("returns the last run as checks", async () => {
    await env.DB.prepare("INSERT INTO last_run (id, at, results) VALUES (1, ?, ?)")
      .bind(T0, JSON.stringify({ "service:a": { ok: true, ms: 120, detail: "", since: T0 } }))
      .run();
    const r = await call("GET", "/checks");
    expect(await r.json()).toEqual({ at: T0, results: { "service:a": { ok: true, ms: 120, detail: "", since: T0 } } });
  });

  it("lists open incidents first, then recent ones", async () => {
    const day = 86_400_000;
    const ins = (id: string, opened: number, closed: number | null) =>
      env.DB.prepare(
        "INSERT INTO incidents (id, subject, kind, label, source, opened_at, closed_at) VALUES (?, 's', 'url', 'l', 'watch', ?, ?)",
      )
        .bind(id, opened, closed)
        .run();
    await ins("old", T0 - 100 * day, T0 - 99 * day);
    await ins("closed", T0 - 2 * day, T0 - day);
    await ins("open", T0 - 3 * day, null);
    await ins("newer", T0 - day / 2, T0 - day / 4);
    const r = await call("GET", "/incidents?days=90");
    const list = (await r.json()) as { id: string }[];
    expect(list.map((i) => i.id)).toEqual(["open", "newer", "closed"]);
  });

  it("answers a malformed signed body with 400", async () => {
    for (const [path, body] of [["/signal", "{"], ["/signal", "{}"], ["/signal", "null"], ["/expectations", "{}"], ["/cron", "{"]] as const) {
      const id = path === "/cron" ? "srv_a" : "panel";
      const key = id === "panel" ? panelKey : await agentKey(panelKey, id);
      const r = await handle(await signed("POST", path, body, T0, id, key), env, T0);
      expect([path, body, r.status, await r.text()]).toEqual([path, body, 400, "bad request"]);
    }
  });
});
