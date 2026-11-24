import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { ServiceFrame } from "./Service";
import { POLL_MS } from "../../ui";
import { apiView, S } from "./fixtures";

describe("ServiceFrame", () => {
  it("shows the outside response time in the second row", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      [`GET ${S}/events`]: [],
      "GET /api/checks": { at: Date.now() - 40_000, results: { "service:svc_1": { ok: true, ms: 120, detail: "", since: 0 } } },
    });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    expect(await screen.findByText("From outside: 120 ms")).toBeTruthy();
  });

  it("shows No signal instead of Deployed when the server is offline", async () => {
    fakeApi({
      [`GET ${S}`]: { ...apiView, server_offline: true, server_last_seen: Date.now() - 2 * 3_600_000 },
      [`GET ${S}/deploys`]: [{ id: "dep_2", state: "live", live: true, created_at: Date.now() - 3_600_000, commit: "a1f9c2e88", rollback: false, can_roll_back: false }],
      "GET /api/checks": { off: true },
    });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    const badge = await screen.findByText((_, el) => !!el?.classList.contains("badge") && el.textContent === "No signal · 2 h ago");
    expect([...badge.classList]).toEqual(["badge", "none"]);
    expect(screen.queryByText("Deployed")).toBeNull();
  });

  it("warns on an outside response above one second", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      [`GET ${S}/events`]: [],
      "GET /api/checks": { at: Date.now(), results: { "service:svc_1": { ok: true, ms: 1500, detail: "", since: 0 } } },
    });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    expect((await screen.findByText("From outside: 1500 ms")).className).toBe("slow");
  });

  it("says since when a failing outside check has been down", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      [`GET ${S}/events`]: [],
      "GET /api/checks": { at: Date.now(), results: { "service:svc_1": { ok: false, ms: 10_000, detail: "502", since: new Date(2026, 9, 27, 14, 2).getTime() } } },
    });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    expect(await screen.findByText("From outside: down since 14:02")).toBeTruthy();
  });

  it("shows nothing without a watcher", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true } });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    await screen.findByText(apiView.url!);
    expect(screen.queryByText(/From outside/)).toBeNull();
  });
});

describe("ServiceFrame database chip", () => {
  const db = { ...apiView, form: { ...apiView.form, kind: "postgres", name: "acme-db" } };
  const tree = (state?: string) => [{ name: "acme", envs: [{ name: "production", server: "hel1", services: state === undefined ? [] : [{ project: "acme", env: "production", service: "api", kind: "postgres", state, health: "ok" }] }] }];
  const frame = () => render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="info">{null}</ServiceFrame>);
  const setup = (state?: string, extra = {}) => (calls = fakeApi({ [`GET ${S}`]: { ...db, ...extra }, "GET /api/checks": { off: true }, "GET /api/projects": tree(state) }));
  const loaded = () => waitFor(() => expect(calls.some((c) => c.url === "/api/projects")).toBe(true));
  let calls: ReturnType<typeof fakeApi> = [];

  it("shows Available for a live database", async () => {
    setup("live");
    frame();
    expect(await screen.findByText("Available")).toBeTruthy();
  });

  it("shows Starting, not Available, while the database restarts", async () => {
    setup("starting");
    frame();
    await screen.findByText("Starting");
    expect(screen.queryByText("Available")).toBeNull();
  });

  it("shows Failed after a failed deploy", async () => {
    setup("failed");
    frame();
    await screen.findByText("Failed");
    expect(screen.queryByText("Available")).toBeNull();
  });

  it("shows Not deployed for a database never deployed and nothing without a row", async () => {
    setup("never deployed");
    const a = frame();
    await screen.findByText("Not deployed");
    a.unmount();
    setup();
    frame();
    await screen.findByText(apiView.url!);
    expect(screen.queryByText("Available")).toBeNull();
  });

  it("reserves the chip place until the projects load", async () => {
    let release = () => {};
    const gate = new Promise<void>((r) => (release = r));
    calls = fakeApi({ [`GET ${S}`]: db, "GET /api/checks": { off: true }, "GET /api/projects": async () => (await gate, tree("live")) });
    const { container } = frame();
    await screen.findByText(apiView.url!);
    expect(container.querySelector(".skel-chip")).toBeTruthy();
    expect(screen.queryByText("Available")).toBeNull();
    release();
    await screen.findByText("Available");
    expect(container.querySelector(".skel-chip")).toBeNull();
  });

  it("drops the placeholder when the projects fail to load", async () => {
    calls = fakeApi({ [`GET ${S}`]: db, "GET /api/checks": { off: true }, "GET /api/projects": () => fail(500, "boom") });
    const { container } = frame();
    await screen.findByText(apiView.url!);
    await waitFor(() => expect(container.querySelector(".skel-chip")).toBeNull());
  });

  it("shows no placeholder for a stopped database", async () => {
    setup("live", { stopped: true });
    const { container } = frame();
    await screen.findByText("Stopped");
    expect(container.querySelector(".skel-chip")).toBeNull();
  });

  it("keeps Stopped and Down", async () => {
    setup("live", { stopped: true });
    const a = frame();
    await screen.findByText("Stopped");
    await loaded();
    expect(screen.queryByText("Available")).toBeNull();
    a.unmount();
    setup("live", { health: "down" });
    frame();
    await screen.findByText("Down");
    await loaded();
    expect(screen.queryByText("Available")).toBeNull();
  });
});

describe("ServiceFrame tabs", () => {
  it("gives an app with volumes a Backups tab", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, volumes: [{ name: "uploads", path: "/u" }] } }, "GET /api/checks": { off: true } });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    expect(await screen.findByRole("link", { name: "Backups" })).toBeTruthy();
    const names = screen.getAllByRole("link").map((l) => l.textContent);
    expect(names.indexOf("Backups")).toBe(names.indexOf("Analytics") + 1);
  });

  it("gives Postgres, MySQL and MariaDB a Data tab after Info, and Valkey none", async () => {
    for (const [kind, want] of [["postgres", true], ["mariadb", true], ["valkey", false]] as const) {
      fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, kind } }, "GET /api/checks": { off: true } });
      const { unmount } = render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
      await screen.findByRole("link", { name: "Info" });
      const names = screen.getAllByRole("link").map((l) => l.textContent);
      expect(names.indexOf("Data")).toBe(want ? names.indexOf("Info") + 1 : -1);
      unmount();
    }
  });

  it("gives an app without volumes none", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true } });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    await screen.findByText(apiView.url!);
    expect(screen.queryByRole("link", { name: "Backups" })).toBeNull();
  });
});

describe("ServiceFrame start and stop", () => {
  it("restarts, stops and starts without a build", async () => {
    let stopped = false;
    const calls = fakeApi({
      [`GET ${S}`]: () => ({ ...apiView, stopped }),
      "GET /api/checks": { off: true },
      [`POST ${S}/restart`]: { deploy_id: "dep_r" },
      [`POST ${S}/stop`]: () => ((stopped = true), { note: "" }),
      [`POST ${S}/start`]: () => ((stopped = false), { deploy_id: "dep_s" }),
    });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Restart" }));
    await waitFor(() => expect(calls.some((c) => c.url === `${S}/restart`)).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Deploy options" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Stop" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, stop" }));
    expect(await screen.findByText("Stopped")).toBeTruthy();
    fireEvent.click(await screen.findByRole("button", { name: "Start" }));
    await waitFor(() => expect(calls.some((c) => c.url === `${S}/start`)).toBe(true));
  });
});

describe("ServiceFrame deploy", () => {
  const p = { project: "acme", env: "production", service: "api" };
  it("Deploy starts a deploy and opens it", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: [], [`POST ${S}/deploys`]: { deploy_id: "dep_6" } });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_6"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ commit: "" });
  });
  it("deploys without cache from its menu", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: [], [`POST ${S}/deploys`]: { deploy_id: "dep_n" } });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Deploy without cache" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST")?.body).toEqual({ commit: "", no_cache: true }));
  });
  it("says the live version under the name", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true },
      [`GET ${S}/deploys`]: [{ id: "d1", state: "live", live: true, can_roll_back: false, rollback: false, commit: "c03d9e5aa", subject: "Fix invoice rounding", by: "dilnoza", created_at: Date.now() - 3_600_000 }] });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    expect(await screen.findByText("Deployed")).toBeTruthy();
    expect(screen.getByText("c03d9e5")).toBeTruthy();
    expect(document.querySelector(".svc-meta")!.textContent).toBe("c03d9e5 · Fix invoice rounding · 1 h ago · by dilnoza");
  });
});

describe("ServiceFrame header menu", () => {
  const p = { project: "acme", env: "production", service: "api" };
  const dep = (can_roll_back: boolean) => [{ id: "d1", state: "live", live: true, can_roll_back, rollback: false, created_at: 1 }];
  it("rolls back from the menu after saying the database is not touched", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: dep(true), [`POST ${S}/rollback`]: { deploy_id: "dep_7" } });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Roll back…" }));
    expect(screen.getByText("The database is not touched. Migrations that ran stay in place.")).toBeTruthy();
    expect(calls.some((c) => c.url.endsWith("/rollback"))).toBe(false);
    const b = screen.getByRole("button", { name: "Roll back to the previous version" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_7"));
    expect(calls.filter((c) => c.url.endsWith("/rollback"))).toHaveLength(1);
  });
  it("shows a failed rollback inside its dialog", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: dep(true), [`POST ${S}/rollback`]: () => fail(409, "a deploy is running") });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Roll back…" }));
    fireEvent.click(screen.getByRole("button", { name: "Roll back to the previous version" }));
    expect(await within(screen.getByRole("dialog")).findByText(/a deploy is running/)).toBeTruthy();
  });
  it("leaves Roll back out when there is nothing to go back to", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: dep(false) });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    expect(screen.getByRole("menuitem", { name: "Restart" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Roll back…" })).toBeNull();
  });
  it("opens Copy or move in a dialog", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: dep(false),
      "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }, { name: "staging", server: "hel1", services: [] }] }] });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Copy or move…" }));
    const dialog = await screen.findByRole("dialog", { name: "Copy or move api" });
    expect(await within(dialog).findByLabelText("To")).toBeTruthy();
  });
  it("opens Roll back and Copy or move while stopped", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, stopped: true }, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: dep(true), "GET /api/projects": [] });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    expect(await screen.findByRole("button", { name: "Start" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    expect(screen.queryByRole("menuitem", { name: "Stop" })).toBeNull();
    fireEvent.click(screen.getByRole("menuitem", { name: "Roll back…" }));
    expect(screen.getByRole("dialog", { name: "Roll back api?" })).toBeTruthy();
    expect(screen.getByText("It starts again.")).toBeTruthy();
  });
  it("does not mention a start when rolling back a running service", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: dep(true) });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Roll back…" }));
    expect(screen.queryByText("It starts again.")).toBeNull();
    expect(screen.getByText(/Migrations that ran stay in place/)).toBeTruthy();
  });
  it("offers Roll back in a database's menu", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, kind: "postgres", name: "acme-db" } }, "GET /api/checks": { off: true }, "GET /api/projects": [] });
    render(<ServiceFrame p={p} tab="info">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Roll back…" }));
    expect(screen.getByText(/Runs the previous image on the same data/)).toBeTruthy();
    expect(screen.queryByText(/Migrations that ran/)).toBeNull();
  });
});

describe("ServiceFrame errors and double clicks", () => {
  const p = { project: "acme", env: "production", service: "api" };
  afterEach(() => vi.useRealTimers());
  it("shows a failed stop inside the dialog", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: [], [`POST ${S}/stop`]: () => fail(500, "boom") });
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    fireEvent.click(await screen.findByRole("button", { name: "Deploy options" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Stop" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, stop" }));
    expect(await within(screen.getByRole("dialog")).findByText(/boom/)).toBeTruthy();
  });
  it("sends one POST for two clicks on Deploy, Restart and Start", async () => {
    let stopped = false;
    const calls = fakeApi({ [`GET ${S}`]: () => ({ ...apiView, stopped }), "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: [],
      [`POST ${S}/deploys`]: { deploy_id: "d" }, [`POST ${S}/restart`]: {}, [`POST ${S}/start`]: {} });
    const { unmount } = render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    const d = await screen.findByRole("button", { name: "Deploy" });
    fireEvent.click(d); fireEvent.click(d);
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Deploy options" }));
    const r = screen.getByRole("menuitem", { name: "Restart" });
    fireEvent.click(r); fireEvent.click(r);
    await waitFor(() => expect(calls.filter((c) => c.url === `${S}/restart`)).toHaveLength(1));
    unmount();
    stopped = true;
    render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    const s = await screen.findByRole("button", { name: "Start" });
    fireEvent.click(s); fireEvent.click(s);
    await waitFor(() => expect(calls.filter((c) => c.url === `${S}/start`)).toHaveLength(1));
  });
  it("re-GETs the header after POLL_MS", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, [`GET ${S}/deploys`]: [] });
    vi.useFakeTimers();
    const { unmount } = render(<ServiceFrame p={p} tab="settings">{null}</ServiceFrame>);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const n = calls.filter((c) => c.url === S).length;
    await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS); });
    expect(calls.filter((c) => c.url === S).length).toBe(n + 1);
    unmount();
  });
});
