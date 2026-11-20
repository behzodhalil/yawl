import { env } from "cloudflare:workers";
import { beforeEach, describe, expect, it } from "vitest";
import vec from "../../testdata/watchsig.json";
import { agentKey, fromHex, hex, sign } from "../src/sign";
import { handle } from "../src/routes";
import { T0, call, panelKey, reset, signed } from "./helpers";

beforeEach(reset);

describe("signing", () => {
  it("matches the Go vector", async () => {
    expect(await sign(fromHex(vec.key), vec.method, vec.path, vec.time, vec.body)).toBe(vec.mac);
    expect(hex(await agentKey(fromHex(vec.key), vec.server))).toBe(vec.agent_key);
  });

  it("refuses replay, other id, changed body", async () => {
    const body = JSON.stringify({ subject: "panel" });
    const old = await signed("POST", "/signal", body, T0);
    const late = await handle(old, env, T0 + 61_000);
    expect([late.status, await late.text()]).toEqual([401, "clock is 61 s off"]);
    const ka = await agentKey(panelKey, "srv_a");
    const other = await signed("POST", "/signal", JSON.stringify({ subject: "server:srv_b" }), T0, "srv_b", ka);
    expect((await handle(other, env, T0)).status).toBe(401);
    const good = await signed("POST", "/signal", body, T0);
    const changed = new Request(good.url, { method: "POST", headers: good.headers, body: JSON.stringify({ subject: "x" }) });
    const bad = await handle(changed, env, T0);
    expect([bad.status, await bad.text()]).toEqual([401, "unauthorized"]);
    expect((await call("POST", "/signal", { subject: "panel" })).status).toBe(200);
  });

  it("refuses an unsigned request", async () => {
    const r = await handle(new Request("https://watch.test/checks"), env, T0);
    expect(r.status).toBe(401);
  });
});
