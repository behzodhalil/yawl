import { describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { chooseOption, fail, fakeApi } from "../../test-api";
import { ServiceSettings } from "./Settings";
import { toasts } from "../../ui";
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
    fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
    fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
    fireEvent.change(screen.getByLabelText("Domain 3"), { target: { value: " shop.acme.uz " } });
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await screen.findByText("update service api health_path");
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Apply only" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(2));
    expect(calls.filter((c) => c.method === "POST").at(-1)!.body).toEqual({ ...apiView.form, health_path: "/up", domains: ["api.acme.uz", "shop.acme.uz"] });
  });

  it("puts the cursor in Domains when opened from a URL's edit control", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    const scrolled: Element[] = [];
    Element.prototype.scrollIntoView = function () { scrolled.push(this); };
    history.pushState(null, "", "/p/acme/production/s/api/settings#domains");
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await waitFor(() => expect(document.activeElement).toBe(screen.getByLabelText("Domain 1")));
    expect(scrolled).toContain(document.getElementById("domains"));
    history.pushState(null, "", "/");
    delete (Element.prototype as Partial<Element>).scrollIntoView;
  });
  it("builds with Railpack when asked, through the plan", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "builder" }] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("radio", { name: "Railpack" }));
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await screen.findByText("update service api builder");
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ builder: "railpack" });
    expect(screen.getByRole("radio", { name: "Railpack" }).getAttribute("aria-checked")).toBe("true");
  });

  it("builds a folder of the repository when one is named", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "api", detail: "root  -> landing" }] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Folder"), { target: { value: " landing " } });
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await screen.findByText("update service api root -> landing");
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ root: "landing" });
    fireEvent.change(screen.getByLabelText("Folder"), { target: { value: "" } });
    expect(screen.queryByRole("button", { name: "Reset" })).toBeNull();
  });

  it("changing a value and changing it back leaves nothing unsaved", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("radio", { name: "www.name sends to name" }));
    expect(screen.getByText("1 section changed · Domains")).toBeTruthy();
    fireEvent.click(screen.getByRole("radio", { name: "Both names serve" }));
    fireEvent.click(screen.getByRole("switch", { name: /Hide from search engines/ }));
    fireEvent.click(screen.getByRole("switch", { name: /Hide from search engines/ }));
    fireEvent.click(screen.getByRole("radio", { name: "Railpack" }));
    fireEvent.click(screen.getByRole("radio", { name: "Automatic" }));
    expect(screen.queryByRole("button", { name: "Reset" })).toBeNull();
  });

  it("adding to an empty Domains keeps one row and puts the cursor in it", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, domains: [] }, domains: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const add = await screen.findByRole("button", { name: "Add domain" });
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
    fireEvent.click(await screen.findByRole("button", { name: "Add domain" }));
    expect(screen.getByLabelText("Domain 2")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Add volume" }));
    expect(screen.getByLabelText("Name, volume 1")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Reset" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Name, volume 1"), { target: { value: "uploads" } });
    expect(screen.getByText("1 section changed · Runtime")).toBeTruthy();
  });

  it("typing in an empty Domains and clearing it leaves nothing unsaved", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, domains: [] } } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const domains = await screen.findByLabelText("Domain 1");
    fireEvent.change(domains, { target: { value: "a.acme.uz" } });
    expect(screen.getByText("1 section changed · Domains")).toBeTruthy();
    fireEvent.change(domains, { target: { value: "" } });
    expect(screen.queryByRole("button", { name: "Reset" })).toBeNull();
  });
});

describe("cron command round trip", () => {
  it("keeps each argument as it was", async () => {
    const view = { ...apiView, form: { ...apiView.form, kind: "cron", port: 0, health_path: "", domains: [], release: [], schedule: "0 3 * * *", timezone: "UTC", command: ["sh", "-c", "pg_dump app | gzip"], timeout: 3600 } };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Time limit (minutes)"), { target: { value: "90" } });
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
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
    expect(screen.getByText("Empty: the panel's own.")).toBeTruthy();
    fireEvent.change(limit, { target: { value: "2.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect((calls.find((c) => c.method === "POST")!.body as { timeout: number }).timeout).toBe(150);
  });
});

describe("ServiceSettings, replicas", () => {
  const replicas = () => screen.findByRole("spinbutton", { name: "Replicas" });
  it("sets replicas within 1 to 10", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const box = await replicas();
    fireEvent.change(box, { target: { value: "40" } });
    expect((box as HTMLInputElement).value).toBe("10");
    fireEvent.change(box, { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ replicas: 2 });
  });
  it("cannot raise replicas when the service has a volume", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, volumes: [{ name: "data", path: "/data" }] } } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    expect((await replicas()).hasAttribute("disabled")).toBe(true);
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
    fireEvent.click(screen.getByRole("switch", { name: /Maintenance page/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, take it offline" }));
    await waitFor(() => expect(screen.getByRole("switch", { name: /Maintenance page/ }).getAttribute("aria-checked")).toBe("true"));
    fireEvent.click(screen.getByRole("radio", { name: "www.name sends to name" }));
    fireEvent.click(screen.getByRole("switch", { name: /Hide from search engines/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
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
    fireEvent.click(await screen.findByRole("switch", { name: /Maintenance page/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, take it offline" }));
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
    fireEvent.click(await screen.findByRole("switch", { name: /Maintenance page/ }));
    expect(await screen.findByRole("dialog", { name: "Show the maintenance page?" })).toBeTruthy();
    expect(screen.getByText("Visitors see the maintenance page until you turn it off.")).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove the password" }));
    expect(screen.getByRole("dialog", { name: "Remove the password?" })).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, remove it" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
  });

  it("ends with a single Delete service card", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(screen.queryByRole("button", { name: "Roll back" })).toBeNull();
    expect(screen.queryByLabelText("To")).toBeNull();
    expect(within(document.getElementById("danger")!).getByRole("button", { name: "Delete service" })).toBeTruthy();
  });

  it("shows the save block after an edit, until the edit is undone", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      "POST /api/environments/acme/production/services?dry=1": { changes: [] },
      "POST /api/environments/acme/production/services": { changes: [] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const branch = await screen.findByLabelText("Branch");
    expect(screen.queryByRole("button", { name: "Save only" })).toBeNull();
    fireEvent.change(branch, { target: { value: "dev" } });
    expect(screen.getByText("1 section changed · Git automation")).toBeTruthy();
    fireEvent.change(branch, { target: { value: "main" } });
    expect(screen.queryByRole("button", { name: "Save only" })).toBeNull();
  });
});

describe("ServiceSettings, sections", () => {
  it("shows the sections in order with a link to each", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    const titles = ["Source", "Git automation", "Runtime", "Resources", "Domains", "Access", "Delete service"];
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(titles);
    const links = within(screen.getByRole("navigation", { name: "On this page" })).getAllByRole("link");
    expect(links.map((a) => [a.querySelector(".t")?.textContent ?? a.textContent, a.getAttribute("href")])).toEqual(
      titles.map((t, i) => [t, `#${["source", "git", "runtime", "resources", "domains", "access", "danger"][i]}`]));
    for (const a of links) expect(document.querySelector(a.getAttribute("href")!)).toBeTruthy();
    expect(within(document.getElementById("git")!).getByLabelText("Branch")).toBeTruthy();
    expect(screen.getAllByLabelText("Branch")).toHaveLength(1);
    expect(within(document.getElementById("runtime")!).getByLabelText("Health path")).toBeTruthy();
    expect(within(document.getElementById("resources")!).getByLabelText("CPU")).toBeTruthy();
    expect(within(document.getElementById("domains")!).getByLabelText("www")).toBeTruthy();
  });

  it("leaves out Source, Domains and Access for a database", async () => {
    const view = { ...apiView, form: { ...apiView.form, kind: "postgres", repo: "", branch: "", port: 0, health_path: "", domains: [], release: [], version: 17, backup_at: "03:00", backup: true }, domains: [] };
    fakeApi({ [`GET ${S}`]: view, "GET /api/projects": [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(screen.getAllByRole("heading", { level: 2 }).map((h) => h.textContent)).toEqual(["Source", "Runtime", "Resources", "Delete service"]);
    expect(within(document.getElementById("source")!).getByLabelText("Version")).toBeTruthy();
    expect(within(document.getElementById("runtime")!).getByLabelText("Daily backup (UTC)")).toBeTruthy();
  });

  it("shows DNS and certificate on the row of their domain, and removes a row", async () => {
    const view = {
      ...apiView, form: { ...apiView.form, domains: ["api.acme.uz", "shop.acme.uz", "new.acme.uz"] },
      domains: [apiView.domains[0], { host: "shop.acme.uz", dns_state: "ok", cert_state: "issued" }, { host: "new.acme.uz", dns_state: "pending", cert_state: "pending" }],
    };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    const row = async (n: number) => within((await screen.findByLabelText(`Domain ${n}`)).closest(".flex-col") as HTMLElement);
    const api = await row(1);
    expect(api.getByText("DNS wrong").className).toContain("text-kumo-danger");
    expect(api.getByText("Certificate pending").className).toBe("text-kumo-subtle");
    expect(api.getByText("api.acme.uz points to 9.9.9.9. Set an A record: api.acme.uz → 1.2.3.4.")).toBeTruthy();
    const shop = await row(2);
    expect(shop.getByText("DNS ok").className).toContain("text-kumo-success");
    expect(shop.getByText("Certificate issued").className).toContain("text-kumo-success");
    const fresh = await row(3);
    expect(fresh.getByText("DNS pending").className).toBe("text-kumo-subtle");
    fireEvent.click(screen.getByRole("button", { name: "Remove shop.acme.uz" }));
    expect((screen.getByLabelText("Domain 2") as HTMLInputElement).value).toBe("new.acme.uz");
    expect((await row(2)).getByText("DNS pending")).toBeTruthy();
    expect(screen.queryByLabelText("Domain 3")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
    expect(document.activeElement).toBe(screen.getByLabelText("Domain 3"));
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ domains: ["api.acme.uz", "new.acme.uz"] });
  });

  it("adds and removes a volume and says what is wrong on its row", async () => {
    const view = { ...apiView, form: { ...apiView.form, volumes: [{ name: "uploads", path: "/app/uploads" }] } };
    const calls = fakeApi({ [`GET ${S}`]: view, "POST /api/environments/acme/production/services?dry=1": { changes: [] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    expect(((await screen.findByLabelText("Path, volume 1")) as HTMLInputElement).value).toBe("/app/uploads");
    fireEvent.click(screen.getByRole("button", { name: "Add volume" }));
    expect(document.activeElement).toBe(screen.getByLabelText("Name, volume 2"));
    fireEvent.change(screen.getByLabelText("Name, volume 2"), { target: { value: "index" } });
    fireEvent.change(screen.getByLabelText("Path, volume 2"), { target: { value: "data/index" } });
    const said = screen.getByLabelText("Path, volume 2").getAttribute("aria-describedby")!;
    expect(document.getElementById(said)!.textContent).toBe("The path starts with /, like /app/uploads.");
    expect(screen.getAllByText("The path starts with /, like /app/uploads.")).toHaveLength(1);
    fireEvent.change(screen.getByLabelText("Path, volume 2"), { target: { value: " /data/index " } });
    expect(screen.queryByText("The path starts with /, like /app/uploads.")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remove volume 1" }));
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    const said = vi.spyOn(toasts, "add");
    fireEvent.click(await screen.findByRole("button", { name: "Apply only" }));
    await waitFor(() => expect(said).toHaveBeenCalledWith(expect.objectContaining({ title: "Saved", description: "It takes effect on the next deploy." })));
    said.mockRestore();
  });
});

describe("ServiceSettings, the side rail", () => {
  const nav = () => within(screen.getByRole("navigation", { name: "On this page" }));
  const item = (title: string) => nav().getByRole("link", { name: new RegExp(`^${title}`) });
  const sum = (title: string) => item(title).querySelector(".s")!.textContent;

  it("sums up each section under its title", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(sum("Source")).toBe("acme/api · main");
    expect(sum("Runtime")).toBe(":10000 · /health");
    expect(sum("Resources")).toBe("1 replica");
    expect(sum("Domains")).toBe("api.acme.uz");
    expect(sum("Access")).toBe("Public");
  });

  it("sums up limits, no domain, a password and the maintenance page", async () => {
    const view = { ...apiView, password_user: "ops", maintenance: true, domains: [], form: { ...apiView.form, memory: "512 MB", cpu: 0.5, replicas: 2, domains: [] } };
    fakeApi({ [`GET ${S}`]: view });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(sum("Resources")).toBe("512 MB · 0.5 CPU · 2 replicas");
    expect(sum("Domains")).toBe("No custom domain");
    expect(sum("Access")).toBe("Password · maintenance");
    expect(nav().queryByRole("img")).toBeNull();
  });

  it("marks edited sections and says what changed in the unsaved-changes bar", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Branch"), { target: { value: "dev" } });
    fireEvent.change(screen.getByLabelText("Health path"), { target: { value: "/up" } });
    expect(screen.getByText("2 sections changed · Git automation, Runtime")).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "Unsaved changes" })).getByRole("button", { name: "Save only" })).toBeTruthy();
    expect(within(item("Git automation")).getByText("Edited")).toBeTruthy();
    expect(within(item("Source")).queryByText("Edited")).toBeNull();
    expect(within(item("Runtime")).getByText("Edited")).toBeTruthy();
    expect(within(item("Resources")).queryByText("Edited")).toBeNull();
    expect(sum("Source")).toBe("acme/api · dev");
  });

  it("flags Domains while a domain is not ready, and Edited takes its place", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(within(item("Domains")).getByRole("img", { name: "A domain is not ready yet" })).toBeTruthy();
    fireEvent.click(screen.getByRole("radio", { name: "www.name sends to name" }));
    expect(within(item("Domains")).queryByRole("img")).toBeNull();
    expect(within(item("Domains")).getByText("Edited")).toBeTruthy();
  });

  it("does not flag Domains when DNS and the certificate are ready", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, domains: [{ host: "api.acme.uz", dns_state: "ok", cert_state: "issued" }] } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(nav().queryByRole("img")).toBeNull();
  });

  it("discards the edits back to the saved settings", async () => {
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Health path"), { target: { value: "/up" } });
    fireEvent.change(screen.getByLabelText("CPU"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    expect((screen.getByLabelText("Health path") as HTMLInputElement).value).toBe("/health");
    expect((screen.getByLabelText("CPU") as HTMLInputElement).value).toBe("");
    expect(screen.queryByRole("button", { name: "Save only" })).toBeNull();
    expect(nav().queryByText("Edited")).toBeNull();
  });

  it("drops a save error on Reset so the next edit starts clean", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "POST /api/environments/acme/production/services?dry=1": () => fail(500, "plan broke") });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("Health path"), { target: { value: "/up" } });
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    expect(await screen.findByText(/plan broke/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Reset" }));
    fireEvent.change(screen.getByLabelText("Health path"), { target: { value: "/again" } });
    expect(screen.queryByText(/plan broke/)).toBeNull();
  });

  it("marks the current section, by click, by Jump to and by scrolling", async () => {
    let seen: (entries: { target: Element; isIntersecting: boolean }[]) => void = () => {};
    vi.stubGlobal("IntersectionObserver", class { constructor(cb: typeof seen) { seen = cb; } observe() {} disconnect() {} });
    fakeApi({ [`GET ${S}`]: apiView });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(item("Source").getAttribute("aria-current")).toBe("location");
    fireEvent.click(item("Resources"));
    expect(item("Resources").getAttribute("aria-current")).toBe("location");
    expect(item("Source").getAttribute("aria-current")).toBeNull();
    const del = nav().getByRole("link", { name: "Delete service" });
    expect(del.getAttribute("href")).toBe("#danger");
    fireEvent.click(del);
    expect(del.getAttribute("aria-current")).toBe("location");
    await chooseOption("Jump to", "Access");
    expect(item("Access").getAttribute("aria-current")).toBe("location");
    vi.spyOn(Date, "now").mockReturnValue(Date.now() + 1000);
    act(() => seen(["runtime", "resources"].map((id) => ({ target: document.getElementById(id)!, isIntersecting: true }))));
    expect(item("Resources").getAttribute("aria-current")).toBe("location");
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
});

describe("ServiceSettings, Git automation", () => {
  const P = "/api/projects/acme/previews";
  const git = () => within(document.getElementById("git")!);

  it("saves deploy on push, watch paths and previews with the form", async () => {
    const calls = fakeApi({
      [`GET ${S}`]: { ...apiView, skip_words: ["[skip ci]", "[skip yawl]"] },
      "POST /api/environments/acme/production/services?dry=1": { changes: [] },
      "POST /api/environments/acme/production/services": { changes: [] },
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Git automation" });
    expect(git().getByRole("link", { name: "github.com/acme/api" }).getAttribute("href")).toBe("https://github.com/acme/api");
    expect(within(git().getByRole("list")).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["[skip ci]", "[skip yawl]"]);
    const watch = git().getByRole("textbox", { name: "Watch paths" });
    fireEvent.change(watch, { target: { value: "api/**" } });
    expect(screen.getByRole("region", { name: "Unsaved changes" }).textContent).toContain("Git automation");
    fireEvent.blur(watch);
    fireEvent.click(git().getByRole("switch", { name: "Deploy on push" }));
    expect(screen.getByText("1 section changed · Git automation")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save only" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ auto_deploy: true, watch: ["api/**"] });
  });

  it("shows whether previews copy the service, the server's limits, and links to the one place that changes it", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, preview_max: 3, preview_idle_days: 10, form: { ...apiView.form, no_preview: true } }, "GET /api/projects": [], [`GET ${P}`]: [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    expect(await screen.findByText("Not copied into previews")).toBeTruthy();
    expect(git().getByText(/At most 3 at once; gone when the pull request closes or after 10 days without a push\./)).toBeTruthy();
    expect(git().queryByRole("switch", { name: "Pull request previews" })).toBeNull();
    expect(git().getByRole("link", { name: "Choose what previews copy" }).getAttribute("href")).toBe("/p/acme/production#previews");
  });

  it("counts this environment's running previews and shows the comment of the latest", async () => {
    fakeApi({
      [`GET ${S}`]: apiView,
      "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [], preview: true }] }],
      [`GET ${P}`]: [
        { number: 42, state: "live", source: "production", comment: "Preview: https://pr-42.acme.uz (deployed a1b2c3d)" },
        { number: 41, state: "making", source: "production" },
        { number: 40, state: "waiting", source: "production" },
        { number: 9, state: "live", source: "staging", comment: "Preview: elsewhere" },
      ],
    });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    expect(await screen.findByText("2 running now")).toBeTruthy();
    expect(git().getByText("The comment on #42 now")).toBeTruthy();
    expect(git().getByText("Preview: https://pr-42.acme.uz (deployed a1b2c3d)")).toBeTruthy();
    expect(git().queryByText("This environment makes no previews. Turn them on from the environment menu.")).toBeNull();
  });

  it("says when the environment makes no previews", async () => {
    fakeApi({ [`GET ${S}`]: apiView, "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }] }], [`GET ${P}`]: [] });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    expect(await screen.findByText("This environment makes no previews. Turn them on from the environment menu.")).toBeTruthy();
    expect(git().getByText("No pull request from this environment yet.")).toBeTruthy();
  });

  it("is left out for a service from an image", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, repo: "", branch: "", image: "nginx:1" } } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByRole("heading", { name: "Delete service" });
    expect(screen.queryByRole("heading", { name: "Git automation" })).toBeNull();
    expect(screen.queryByLabelText("Branch")).toBeNull();
  });
});
