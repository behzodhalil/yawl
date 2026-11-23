import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { ServiceSettings } from "./Settings";
import { apiView, S } from "./fixtures";

process.env.TZ = "Asia/Tashkent";

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
    fireEvent.click(screen.getByRole("button", { name: "+ Add domain" }));
    fireEvent.click(screen.getByRole("button", { name: "+ Add domain" }));
    fireEvent.change(screen.getByLabelText("Domain 3"), { target: { value: " shop.acme.uz " } });
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
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Domain 1")));
    history.pushState(null, "", "/");
  });
  it("builds with Railpack when asked, through the plan", async () => {
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

  it("adding to an empty Domains keeps one row and puts the cursor in it", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, domains: [] }, domains: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const add = await screen.findByRole("button", { name: "+ Add domain" });
    fireEvent.click(add);
    expect(document.activeElement).toBe(screen.getByLabelText("Domain 1"));
    expect(screen.queryByLabelText("Domain 2")).toBeNull();
    expect(screen.queryByRole("button", { name: /^Remove/ })).toBeNull();
    fireEvent.change(screen.getByLabelText("Domain 1"), { target: { value: "x" } });
    fireEvent.change(screen.getByLabelText("Domain 1"), { target: { value: "" } });
    fireEvent.click(add);
    expect(screen.queryByLabelText("Domain 2")).toBeNull();
    expect(screen.queryByRole("button", { name: /^Remove/ })).toBeNull();
  });

  it("an added empty domain or volume row leaves nothing unsaved", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "+ Add domain" }));
    expect(screen.getByLabelText("Domain 2")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "+ Add volume" }));
    expect(screen.getByLabelText("Name, volume 1")).toBeTruthy();
    expect(screen.queryByText("Unsaved changes")).toBeNull();
    fireEvent.change(screen.getByLabelText("Name, volume 1"), { target: { value: "uploads" } });
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
  });

  it("typing in an empty Domains and clearing it leaves nothing unsaved", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, domains: [] } } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const domains = await screen.findByLabelText("Domain 1");
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

describe("cron time limit", () => {
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
    expect(await screen.findByText("It stops now and is purged on 30 Oct 2026 17:02 unless restored.")).toBeTruthy();
    expect(screen.getByLabelText("Type api to delete it.")).toBeTruthy();
  });
});

describe("ServiceSettings, the address", () => {
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

describe("ServiceSettings, a change that waits", () => {
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

describe("ServiceSettings, confirms and order", () => {
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

  it("puts Delete last, after Copy / move and Rollback", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete" });
    const names = screen.getAllByRole("heading").map((h) => h.textContent);
    expect(names.slice(-4)).toEqual(["Copy / move", "Danger zone", "Rollback", "Delete"]);
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

describe("ServiceSettings, sections", () => {
  it("shows the sections in order with a link to each", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Danger zone" });
    const titles = ["Source", "Runtime", "Resources", "Domains", "Access", "Copy / move", "Danger zone"];
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(titles);
    const links = within(screen.getByRole("navigation", { name: "Sections" })).getAllByRole("link");
    expect(links.map((a) => [a.textContent, a.getAttribute("href"), a.className])).toEqual(
      titles.map((t, i) => [t, `#${["source", "runtime", "resources", "domains", "access", "copy", "danger"][i]}`, "l"]));
    for (const a of links) expect(document.querySelector(a.getAttribute("href")!)).toBeTruthy();
    expect(within(document.getElementById("source")!).getByLabelText("Branch")).toBeTruthy();
    expect(within(document.getElementById("runtime")!).getByLabelText("Health path")).toBeTruthy();
    expect(within(document.getElementById("resources")!).getByLabelText("CPU")).toBeTruthy();
    expect(within(document.getElementById("domains")!).getByLabelText("www")).toBeTruthy();
  });

  it("leaves out Source, Domains and Access for a database", async () => {
    const view = { ...apiView, form: { ...apiView.form, kind: "postgres", repo: "", branch: "", port: 0, health_path: "", domains: [], release: [], version: 17, backup_at: "03:00", backup: true }, domains: [] };
    fakeApi({ [`GET ${S}`]: view, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Danger zone" });
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Source", "Runtime", "Resources", "Copy / move", "Danger zone"]);
    expect(within(document.getElementById("source")!).getByLabelText("Version")).toBeTruthy();
    expect(within(document.getElementById("runtime")!).getByLabelText("Daily backup (UTC)")).toBeTruthy();
  });

  it("shows DNS and TLS on the row of their domain, and removes a row", async () => {
    const view = {
      ...apiView, form: { ...apiView.form, domains: ["api.acme.uz", "shop.acme.uz", "new.acme.uz"] },
      domains: [apiView.domains[0], { host: "shop.acme.uz", dns_state: "ok", cert_state: "issued" }, { host: "new.acme.uz", dns_state: "pending", cert_state: "pending" }],
    };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const row = async (n: number) => within((await screen.findByLabelText(`Domain ${n}`)).closest(".col") as HTMLElement);
    const api = await row(1);
    expect(api.getByText("DNS wrong").className).toBe("tag bad");
    expect(api.getByText("TLS pending").className).toBe("muted");
    expect(api.getByText("api.acme.uz points to 9.9.9.9. Set an A record: api.acme.uz → 1.2.3.4.")).toBeTruthy();
    const shop = await row(2);
    expect(shop.getByText("DNS ok").className).toBe("tag");
    expect(shop.getByText("TLS issued").className).toBe("tag");
    const fresh = await row(3);
    expect(fresh.getByText("DNS pending").className).toBe("muted");
    fireEvent.click(screen.getByRole("button", { name: "Remove shop.acme.uz" }));
    expect((screen.getByLabelText("Domain 2") as HTMLInputElement).value).toBe("new.acme.uz");
    expect((await row(2)).getByText("DNS pending")).toBeTruthy();
    expect(screen.queryByLabelText("Domain 3")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "+ Add domain" }));
    expect(document.activeElement).toBe(screen.getByLabelText("Domain 3"));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ domains: ["api.acme.uz", "new.acme.uz"] });
  });

  it("adds and removes a volume and says what is wrong on its row", async () => {
    const view = { ...apiView, form: { ...apiView.form, volumes: [{ name: "uploads", path: "/app/uploads" }] } };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    expect(((await screen.findByLabelText("Path, volume 1")) as HTMLInputElement).value).toBe("/app/uploads");
    fireEvent.click(screen.getByRole("button", { name: "+ Add volume" }));
    expect(document.activeElement).toBe(screen.getByLabelText("Name, volume 2"));
    fireEvent.change(screen.getByLabelText("Name, volume 2"), { target: { value: "index" } });
    fireEvent.change(screen.getByLabelText("Path, volume 2"), { target: { value: "data/index" } });
    const second = within(screen.getByLabelText("Path, volume 2").closest(".col") as HTMLElement);
    expect(second.getByText("The path starts with /, like /app/uploads.")).toBeTruthy();
    expect(screen.getAllByText("The path starts with /, like /app/uploads.")).toHaveLength(1);
    fireEvent.change(screen.getByLabelText("Path, volume 2"), { target: { value: " /data/index " } });
    expect(screen.queryByText("The path starts with /, like /app/uploads.")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remove volume 1" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ volumes: [{ name: "index", path: "/data/index" }] });
  });

  it("says when a save takes effect", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "cpu" }] },
      "POST /api/environments/acme/production/services": { changes: [] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("CPU"), { target: { value: "0.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    fireEvent.click(await screen.findByRole("button", { name: "Apply" }));
    expect(await screen.findByText("Saved. It takes effect on the next deploy.")).toBeTruthy();
  });
});
