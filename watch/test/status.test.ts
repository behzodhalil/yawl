import { env } from "cloudflare:workers";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { handle } from "../src/routes";
import { T0, exp, expect_, reset } from "./helpers";

const MIN = 60_000;
const DAY = 86_400_000;
const page = async (now = T0, path = "/status") => {
  const r = await handle(new Request("https://watch.test" + path), env, now);
  return { r, html: await r.text() };
};
const incident = (id: string, subject: string, opened: number, closed: number | null) =>
  env.DB.prepare(
    "INSERT INTO incidents (id, subject, kind, label, source, opened_at, closed_at, cause, note) VALUES (?, ?, 'url', 'acme/production/api', 'watch', ?, ?, 'GET https://api.test/ answered 502', 'a private note')",
  )
    .bind(id, subject, opened, closed)
    .run();

const published = (incidents: unknown[] = []) =>
  JSON.stringify({
    title: "Yawl",
    rows: [
      { id: "r1", name: "Website", subjects: ["service:site"] },
      { id: "r2", name: "Cloud panels", subjects: ["service:a", "service:b"] },
      { id: "r3", name: "Gone", subjects: ["service:nothing"] },
    ],
    incidents,
  });

const world = (config: Record<string, string>) =>
  expect_(
    [
      exp({ id: "s1", kind: "server", subject: "server:srv_1", label: "hel1" }),
      exp({ id: "u1", kind: "url", subject: "service:site", label: "site/production/web", target: "https://site.test/", server: "srv_1" }),
      exp({ id: "u2", kind: "url", subject: "service:a", label: "cells/a/panel", target: "https://a.test/", server: "srv_2" }),
      exp({ id: "u3", kind: "url", subject: "service:b", label: "cells/b/panel", target: "https://b.test/", server: "srv_2" }),
      exp({ id: "u4", kind: "url", subject: "service:secret", label: "acme/production/secret-admin", target: "https://admin.test/" }),
    ],
    { tz: "UTC", ...config },
    T0 - 10 * DAY,
  );

beforeEach(async () => {
  await reset();
  await world({ status: published() });
});

describe("status page", () => {
  it("says so when no page is published", async () => {
    await world({ status: "" });
    const { r, html } = await page();
    expect(r.status).toBe(200);
    expect(html).toContain("No status page is published here");
    expect(html).not.toContain("site/production");
  });

  it("shows the published rows by their public names, never a label, host or cause", async () => {
    await incident("i1", "service:a", T0 - 5 * MIN, null);
    const { r, html } = await page();
    expect(r.headers.get("content-type")).toBe("text/html; charset=utf-8");
    expect(r.headers.get("cache-control")).toBe("max-age=30");
    expect(html).toContain("<title>Yawl</title>");
    expect(html).toContain("Website");
    expect(html).toContain("Cloud panels");
    expect(html).not.toContain("Gone");
    for (const leak of ["site/production", "cells/a", "secret-admin", "hel1", "https://", "502", "private note"]) expect(html).not.toContain(leak);
  });

  it("folds a project: some members down is Degraded, all is Down", async () => {
    await incident("i1", "service:a", T0 - 5 * MIN, null);
    expect((await page()).html).toMatch(/Cloud panels<\/b><span class="badge busy">.*Degraded/);
    await reset();
    await world({ status: published() });
    await incident("m1", "server:srv_2", T0 - 40 * MIN, null);
    const { html } = await page();
    expect(html).toMatch(/Cloud panels<\/b><span class="badge bad">.*Down/);
    expect(html).toContain("Some services are down");
    expect(html).toContain("40 minutes down");
  });

  it("draws 90 days, before the first check grey", async () => {
    await incident("i1", "service:site", T0 - 2 * DAY, T0 - 2 * DAY + 30 * MIN);
    const { html } = await page();
    const bars = html.slice(html.indexOf("Website"), html.indexOf("Cloud panels")).match(/<i class="(\w+)"/g) ?? [];
    expect(bars.length).toBe(90);
    expect(bars.filter((b) => b.includes("off")).length).toBe(79);
    expect(html).toContain("30 minutes down");
    expect(html).toMatch(/99\.\d\d% uptime/);
  });

  it("shows published incidents, and an open one makes an up row degraded", async () => {
    await world({
      status: published([
        { kind: "incident", title: "Slow sign-in & more", rows: ["r1"], updates: [{ at: T0 - MIN, body: "We are on it" }], starts_at: 0, ends_at: 0, resolved_at: 0, created_at: T0 - 10 * MIN },
        { kind: "maintenance", title: "Database upgrade", rows: ["r2"], updates: [], starts_at: T0 + DAY, ends_at: T0 + DAY + 60 * MIN, resolved_at: 0, created_at: T0 },
      ]),
    });
    const { html } = await page();
    expect(html).toContain("Slow sign-in &amp; more");
    expect(html).toContain("We are on it");
    expect(html).toMatch(/Website<\/b><span class="badge busy">.*Degraded/);
    expect(html).toContain("Scheduled maintenance");
    expect(html).toContain("Since 23 Dec 04:16 (UTC). Affects Website");
  });

  it("answers at / too, for the owner's own domain", async () => {
    const { r, html } = await page(T0, "/");
    expect(r.status).toBe(200);
    expect(html).toContain("Website");
  });

  it("builds its date formats once, not per bar", async () => {
    const spy = vi.spyOn(Intl, "DateTimeFormat");
    await page();
    expect(spy.mock.calls.length).toBeLessThanOrEqual(3);
    spy.mockRestore();
  });

  it("draws the page from D1 at most every 30 s", async () => {
    await page(T0);
    await incident("i1", "service:site", T0 - 10 * MIN, null);
    expect((await page(T0 + 29_000)).html).not.toMatch(/Website<\/b><span class="badge bad">/);
    expect((await page(T0 + 30_000)).html).toMatch(/Website<\/b><span class="badge bad">/);
  });
});
