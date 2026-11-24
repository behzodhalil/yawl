import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import * as seal from "../../seal";
import { DbInfo } from "./DbInfo";

const D = "/api/services/acme/production/acme-db";
const view = { form: { name: "acme-db", kind: "postgres", repo: "", branch: "", port: 0, health_path: "", health_timeout: 0, drain_timeout: 0, auto_deploy: true,
  release: [], release_timeout: 0, domains: [], version: 17, backup_at: "03:00", outside_port: 0, allow: [], schedule: "", timezone: "", command: [], timeout: 0 },
  health: "ok", server: "hel1", domains: [] };

describe("DbInfo", () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  it("shows a connection URL for 30 seconds and hides it on request", async () => {
    vi.spyOn(seal, "reveal").mockResolvedValue("postgres://u:p@acme-db/acme");
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    await screen.findByText("acme-db.url");
    expect(screen.queryByRole("button", { name: /Reveal/ })).toBeNull();
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Show URL" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("postgres://u:p@acme-db/acme")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Hide URL" }));
    expect(screen.queryByText("postgres://u:p@acme-db/acme")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Show URL" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("postgres://u:p@acme-db/acme")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(screen.queryByText("postgres://u:p@acme-db/acme")).toBeNull();
    expect(screen.getByRole("button", { name: "Show URL" })).toBeTruthy();
  });

  it("hides a connection URL 30 seconds after its latest Show", async () => {
    vi.spyOn(seal, "reveal").mockResolvedValue("postgres://u:p@acme-db/acme");
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    await screen.findByText("acme-db.url");
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Show URL" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
    fireEvent.click(screen.getByRole("button", { name: "Hide URL" }));
    fireEvent.click(screen.getByRole("button", { name: "Show URL" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
    expect(screen.getByText("postgres://u:p@acme-db/acme")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(screen.queryByText("postgres://u:p@acme-db/acme")).toBeNull();
  });

  it("shows the reference and asks for a port and an address before planning outside access", async () => {
    const calls = fakeApi({
      [`GET ${D}`]: view,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "acme-db", detail: "outside_port, allow; acme-db is open to every address." }] },
    });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    await screen.findByText("acme-db.url");
    expect(screen.getByText("Use it as a reference: DATABASE_URL → acme-db.url")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Allow connections from outside"));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    expect(screen.getByText("Give a port and at least one address.")).toBeTruthy();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    fireEvent.change(screen.getByLabelText("Outside port"), { target: { value: "15432" } });
    fireEvent.change(screen.getByLabelText("Allowed addresses"), { target: { value: "0.0.0.0/0\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText(/is open to every address\./);
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ outside_port: 15432, allow: ["0.0.0.0/0"] });
  });

  it("a Valkey shows its reference for REDIS_URL and no outside access", async () => {
    fakeApi({ [`GET /api/services/acme/production/cache`]: { ...view, form: { ...view.form, name: "cache", kind: "valkey", version: 8 } } });
    render(<DbInfo project="acme" env="production" service="cache" />);
    await screen.findByText("cache.url");
    expect(screen.getByText("Use it as a reference: REDIS_URL → cache.url")).toBeTruthy();
    expect(screen.queryByLabelText("Allow connections from outside")).toBeNull();
  });

  it("lists the apps that use it, other environments too, each a link", async () => {
    fakeApi({ [`GET ${D}`]: { ...view, used_by: [{ env: "production", service: "api" }, { env: "staging", service: "web" }] } });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect((await screen.findByRole("link", { name: "api" })).getAttribute("href")).toBe("/p/acme/production/s/api");
    expect(screen.getByRole("link", { name: "staging/web" }).getAttribute("href")).toBe("/p/acme/staging/s/web");
  });

  it("says when nothing uses it", async () => {
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect(await screen.findByText("No service references acme-db.url yet.")).toBeTruthy();
  });

  it("keeps 7 days of changes through the plan, with a button", async () => {
    const calls = fakeApi({
      [`GET ${D}`]: view,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "acme-db", detail: "pitr" }] },
      "POST /api/environments/acme/production/services": { changes: [] },
      [`POST ${D}/deploys`]: { deploy_id: "dep_3" },
    });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    fireEvent.click(await screen.findByRole("button", { name: "Keep 7 days of changes (restarts the database)" }));
    await screen.findByText(/acme-db.*pitr/);
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ pitr: true });
    fireEvent.click(screen.getByRole("button", { name: "Apply (restarts the database)" }));
    await waitFor(() => expect(calls.some((c) => c.url === `${D}/deploys`)).toBe(true));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/acme-db/deploys/dep_3"));
  });

  it("says Unsaved changes after editing outside access", async () => {
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    fireEvent.click(await screen.findByLabelText("Allow connections from outside"));
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Allow connections from outside"));
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  it("styles each user as a link", async () => {
    fakeApi({ [`GET ${D}`]: { ...view, used_by: [{ env: "production", service: "api" }] } });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect((await screen.findByRole("link", { name: "api" })).classList.contains("l")).toBe(true);
  });

  it("warns on a production Postgres without point-in-time recovery and starts the plan", async () => {
    const calls = fakeApi({
      [`GET ${D}`]: view,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "acme-db", detail: "pitr" }] },
    });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect(await screen.findByText("Production database without point-in-time recovery. A restore can only reach the last nightly backup.")).toBeTruthy();
    const warning = screen.getByText(/^Production database without point-in-time recovery/).closest(".callout") as HTMLElement;
    fireEvent.click(within(warning).getByRole("button", { name: "Turn on point-in-time recovery" }));
    expect(await within(warning).findByText(/acme-db.*pitr/)).toBeTruthy();
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ pitr: true });
    expect(within(warning).getByRole("button", { name: "Apply (restarts the database)" })).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Apply (restarts the database)" })).toHaveLength(1);
    expect(screen.getAllByText(/acme-db.*pitr/)).toHaveLength(1);
  });

  it("does not warn outside production, with recovery on, or for another engine", async () => {
    fakeApi({ [`GET /api/services/acme/staging/acme-db`]: view });
    const a = render(<DbInfo project="acme" env="staging" service="acme-db" />);
    await screen.findByText("acme-db.url");
    expect(screen.queryByText(/without point-in-time recovery/)).toBeNull();
    a.unmount();
    fakeApi({ [`GET ${D}`]: { ...view, form: { ...view.form, pitr: true } } });
    const b = render(<DbInfo project="acme" env="production" service="acme-db" />);
    await screen.findByText("acme-db.url");
    expect(screen.queryByText(/without point-in-time recovery/)).toBeNull();
    b.unmount();
    fakeApi({ [`GET /api/services/acme/production/cache`]: { ...view, form: { ...view.form, kind: "valkey" } } });
    render(<DbInfo project="acme" env="production" service="cache" />);
    await screen.findByText("cache.url");
    expect(screen.queryByText(/without point-in-time recovery/)).toBeNull();
  });

  it("makes the plan button primary only with outside access on, and disabled while nothing changed", async () => {
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    const btn = await screen.findByRole("button", { name: "Show the plan" });
    expect(btn.className).toBe("btn");
    expect((btn as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByLabelText("Allow connections from outside"));
    expect(btn.className).toBe("btn pri");
    expect((btn as HTMLButtonElement).disabled).toBe(false);
  });

  it("shows the newest completed backup and nothing when there is none", async () => {
    const at = new Date(2026, 10, 20, 3, 0).getTime();
    fakeApi({
      [`GET ${D}`]: view,
      [`GET ${D}/backups`]: [{ id: "b2", state: "failed", created_at: at + 86_400_000 }, { id: "b1", state: "ok", created_at: at }, { id: "b0", state: "ok", created_at: at - 86_400_000 }],
    });
    const a = render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect(await screen.findByText("Last backup")).toBeTruthy();
    expect(screen.getByText("20 Nov 2026 03:00").getAttribute("title")).toMatch(/ago/);
    a.unmount();
    fakeApi({ [`GET ${D}`]: view, [`GET ${D}/backups`]: [{ id: "b2", state: "failed", created_at: at }] });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    await screen.findByText("acme-db.url");
    expect(screen.queryByText("Last backup")).toBeNull();
  });
});
