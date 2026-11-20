import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { ServiceFrame } from "./Service";
import { POLL_MS } from "../../ui";
import { apiView, S } from "./fixtures";

describe("ServiceFrame", () => {
  it("shows the watcher's line under the URL", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      [`GET ${S}/events`]: [],
      "GET /api/checks": { at: Date.now() - 40_000, results: { "service:svc_1": { ok: true, ms: 120, detail: "", since: 0 } } },
    });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    expect(await screen.findByText("From outside: up, 120 ms, checked 40 s ago")).toBeTruthy();
  });

  it("shows nothing without a watcher", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true } });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="settings">{null}</ServiceFrame>);
    await screen.findByText(apiView.url!);
    expect(screen.queryByText(/From outside/)).toBeNull();
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

  it("gives Postgres, MySQL and MariaDB a Data tab after Info, and Valkey none (design record 68)", async () => {
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

describe("ServiceFrame start and stop (design record 36)", () => {
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
  it("deploys without cache from its menu (design record 40)", async () => {
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
    expect(document.querySelector(".state")!.textContent).toContain("Fix invoice rounding");
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
