import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { ServiceSettings } from "./Settings";
import { apiView, S } from "./fixtures";

describe("ServiceSettings", () => {
  it("sends the whole form, shows the plan first, and shows DNS advice", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "health_path" }] },
      "POST /api/environments/acme/production/services": { changes: [] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByText("api.acme.uz points to 9.9.9.9. Set an A record: api.acme.uz → 1.2.3.4.");
    fireEvent.change(screen.getByLabelText("Health path"), { target: { value: "/up" } });
    fireEvent.change(screen.getByLabelText("Domains"), { target: { value: "api.acme.uz\n\nshop.acme.uz\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("update service api health_path");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2));
    expect(calls[calls.length - 1].body).toEqual({ ...apiView.form, health_path: "/up", domains: ["api.acme.uz", "shop.acme.uz"] });
  });

  it("puts the cursor in Domains when opened from a URL's edit control", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    history.pushState(null, "", "/p/acme/production/s/api/settings#domains");
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Domains")));
    history.pushState(null, "", "/");
  });
  it("builds with Railpack when asked, through the plan (design record 32)", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "builder" }] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Build with Railpack" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("update service api builder");
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ builder: "railpack" });
    expect(screen.getByRole("button", { name: "Choose the builder automatically" })).toBeTruthy();
  });

  it("builds a folder of the repository when one is named", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "root  -> landing" }] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Folder"), { target: { value: " landing " } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("update service api root -> landing");
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ root: "landing" });
    fireEvent.change(screen.getByLabelText("Folder"), { target: { value: "" } });
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  it("changing a value and changing it back leaves nothing unsaved", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const www = await screen.findByLabelText("www");
    fireEvent.change(www, { target: { value: "bare" } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    fireEvent.change(www, { target: { value: "" } });
    fireEvent.change(screen.getByLabelText("Search engines"), { target: { value: "noindex" } });
    fireEvent.change(screen.getByLabelText("Search engines"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Build with Railpack" }));
    fireEvent.click(screen.getByRole("button", { name: "Choose the builder automatically" }));
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  it("typing in an empty Domains and clearing it leaves nothing unsaved", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, domains: [] } } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const domains = await screen.findByLabelText("Domains");
    fireEvent.change(domains, { target: { value: "a.acme.uz" } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    fireEvent.change(domains, { target: { value: "" } });
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });

  it("rolls back after saying the database is not touched", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, [`POST ${S}/rollback`]: { deploy_id: "dep_7", to: "dep_1", note: "" } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Roll back" }));
    expect(screen.getByText("The database is not touched. Migrations that ran stay in place.")).toBeTruthy();
    expect(calls.some((c) => c.url.endsWith("/rollback"))).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Roll back to the previous version" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_7"));
  });
});

describe("cron command round trip", () => {
  it("keeps each argument as it was", async () => {
    const view = { ...apiView, form: { ...apiView.form, kind: "cron", port: 0, health_path: "", domains: [], release: [], schedule: "0 3 * * *", timezone: "UTC", command: ["sh", "-c", "pg_dump app | gzip"], timeout: 3600 } };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Time limit (minutes)"), { target: { value: "90" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect((calls.find((c) => c.method === "POST")!.body as { command: string[] }).command).toEqual(["sh", "-c", "pg_dump app | gzip"]);
  });
});

describe("cron time limit (plan 13 minor)", () => {
  it("shows a 90 s limit as it is and keeps it exact through an edit", async () => {
    const view = { ...apiView, form: { ...apiView.form, kind: "cron", port: 0, health_path: "", domains: [], release: [], schedule: "0 3 * * *", timezone: "", command: ["true"], timeout: 90 } };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const limit = (await screen.findByLabelText("Time limit (minutes)")) as HTMLInputElement;
    expect(limit.value).toBe("1.5");
    expect(screen.getByText("Empty: the panel's own. For example Asia/Tashkent.")).toBeTruthy();
    fireEvent.change(limit, { target: { value: "2.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect((calls.find((c) => c.method === "POST")!.body as { timeout: number }).timeout).toBe(150);
  });
});

describe("delete service", () => {
  it("opens the dialog with the purge time", async () => {
    fakeApi({ [`GET ${S}`]: apiView, [`GET ${S}/delete-info`]: { purge_at: Date.UTC(2026, 9, 30, 12, 2), users: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete service" }));
    expect(await screen.findByText("It stops now and is purged on 30 Oct 12:02 UTC unless restored.")).toBeTruthy();
    expect(screen.getByLabelText("Type api to delete it.")).toBeTruthy();
  });
});

describe("ServiceSettings, the address (design record 38)", () => {
  it("sets a password, turns maintenance on, and sends www and noindex with the form", async () => {
    let view = { ...apiView };
    const calls = fakeApi({
      [`GET ${S}`]: () => view,
      [`PUT ${S}/password`]: () => ((view = { ...view, password_user: "ops" }), null),
      [`PUT ${S}/maintenance`]: () => ((view = { ...view, maintenance: true }), null),
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "www, noindex" }] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("User"), { target: { value: "ops" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "s3cret-pass" } });
    fireEvent.click(screen.getByRole("button", { name: "Set the password" }));
    expect(await screen.findByText("Asks for a password: user ops.")).toBeTruthy();
    expect(calls.find((c) => c.url === `${S}/password`)!.body).toEqual({ user: "ops", password: "s3cret-pass" });
    fireEvent.click(screen.getByRole("button", { name: "Show the maintenance page" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, take it offline" }));
    expect(await screen.findByRole("button", { name: "Back to the app" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("www"), { target: { value: "bare" } });
    fireEvent.change(screen.getByLabelText("Search engines"), { target: { value: "noindex" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("update service api www, noindex");
    expect(calls.find((c) => c.url.endsWith("?dry=1"))!.body).toMatchObject({ www: "bare", noindex: true });
  });
});

describe("ServiceSettings, a change that waits (record 38, review I3)", () => {
  it("shows the server's note", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      [`PUT ${S}/maintenance`]: { note: "Saved. A deploy is running; it takes the change when it switches." },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Show the maintenance page" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, take it offline" }));
    expect(await screen.findAllByText("Saved. A deploy is running; it takes the change when it switches.")).toHaveLength(1);
  });
});

describe("ServiceSettings, confirms and order (task 7)", () => {
  it("asks before the maintenance page and before removing the password", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: { ...apiView, password_user: "ops" },
      [`PUT ${S}/maintenance`]: null,
      [`PUT ${S}/password`]: null,
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Show the maintenance page" }));
    expect(screen.getByRole("dialog", { name: "Show the maintenance page?" })).toBeTruthy();
    expect(screen.getByText("Visitors see the maintenance page until you turn it off.")).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove the password" }));
    expect(screen.getByRole("dialog", { name: "Remove the password?" })).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, remove it" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
  });

  it("puts Delete last, after Copy or move and Rollback", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete" });
    const names = screen.getAllByRole("heading").map((h) => h.textContent);
    expect(names.slice(-3)).toEqual(["Copy or move", "Rollback", "Delete"]);
  });

  it("sends a double-clicked Roll back once", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, [`POST ${S}/rollback`]: { deploy_id: "dep_7" } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Roll back" }));
    const b = screen.getByRole("button", { name: "Roll back to the previous version" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_7"));
    expect(calls.filter((c) => c.url.endsWith("/rollback"))).toHaveLength(1);
  });

  it("says Unsaved changes after an edit, until it is saved", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [] },
      "POST /api/environments/acme/production/services": { changes: [] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const branch = await screen.findByLabelText("Branch");
    expect(screen.queryByText("Unsaved changes")).toBeNull();
    fireEvent.change(branch, { target: { value: "dev" } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    fireEvent.change(branch, { target: { value: "main" } });
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });
});
