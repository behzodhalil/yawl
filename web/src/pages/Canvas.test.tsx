import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { route } from "../router";
import { Canvas, scheduleText } from "./Canvas";
import { apiView } from "./service/fixtures";

const env = { name: "production", server: "hel1", services: [
  { project: "acme", env: "production", service: "web", kind: "web", state: "live", health: "ok", url: "https://acme.uz" },
  { project: "acme", env: "production", service: "report", kind: "cron", state: "live", health: "ok" },
  { project: "acme", env: "production", service: "db", kind: "postgres", state: "live", health: "ok" },
] };
const S = "/api/services/acme/production";

function draw() {
  fakeApi({
    "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "198.51.100.10", offline: false }],
    [`GET ${S}/web`]: { ...apiView, url: "https://acme.uz", form: { ...apiView.form, name: "web", kind: "web", replicas: 2, cpu: 0.5, memory: "512MB", port: 8080 }, domains: [{ host: "acme.uz", dns_state: "ok", cert_state: "issued" }, { host: "www.acme.uz", dns_state: "wrong", cert_state: "pending" }] },
    [`GET ${S}/report`]: { ...apiView, form: { ...apiView.form, name: "report", kind: "cron", schedule: "0 6 * * *", timezone: "Asia/Tashkent", command: ["/app/report", "--daily"] }, domains: [] },
    [`GET ${S}/db`]: { ...apiView, form: { ...apiView.form, name: "db", kind: "postgres", version: 17, backup_at: "03:00", volumes: [] }, domains: [], used_by: [{ env: "production", service: "web" }] },
  });
  return render(<Canvas project="acme" env={env} base="/p/acme/production" />);
}

describe("scheduleText", () => {
  it("reads a daily schedule and leaves any other as written", () => {
    expect(scheduleText("0 6 * * *")).toBe("Every day at 06:00");
    expect(scheduleText("30 18 * * *")).toBe("Every day at 18:30");
    expect(scheduleText("*/5 * * * *")).toBe("*/5 * * * *");
    expect(scheduleText("0 6 * * 1")).toBe("0 6 * * 1");
  });
});

describe("Canvas", () => {
  it("groups the network and the server, and draws real properties", async () => {
    const { container } = draw();
    const server = await screen.findByRole("region", { name: "hel1 · 198.51.100.10" });
    const web = (await within(server).findByRole("link", { name: /web/ })).closest("[data-node]") as HTMLElement;
    await waitFor(() => expect(within(web).getByText("2")).toBeTruthy());
    expect(within(web).getByText("0.5 vCPU")).toBeTruthy();
    expect(within(web).getByText("512 MB")).toBeTruthy();
    expect(within(web).getByText("8080")).toBeTruthy();
    expect(within(web).getByRole("button", { name: "Copy the address of web" })).toBeTruthy();
    expect(within(server).getByText("Every day at 06:00 · Asia/Tashkent")).toBeTruthy();
    expect(within(server).getByText("/app/report --daily")).toBeTruthy();
    expect(within(server).getByText("Postgres 17")).toBeTruthy();
    expect(within(server).getByText("Daily at 03:00")).toBeTruthy();
    const network = screen.getByRole("region", { name: "Network" });
    expect(within(network).getByText("acme.uz").parentElement!.textContent).toContain("Ready");
    expect(within(network).getByText("www.acme.uz").parentElement!.textContent).toContain("DNS pending");
    await waitFor(() => expect([...container.querySelectorAll("[data-edge]")].map((e) => e.getAttribute("data-edge")).sort()).toEqual(["Domains>web", "web>db"]));
    expect(container.querySelector("[data-edge]")!.getAttribute("d")).toMatch(/^M[\d.-]+,[\d.-]+ H[\d.-]+ V[\d.-]+ H[\d.-]+$/);
  });

  it("adds a service from a menu that names each kind and opens New service with it", async () => {
    draw();
    fireEvent.click(await screen.findByRole("button", { name: /Add service/ }));
    const pg = await screen.findByRole("menuitem", { name: "Postgres", description: "Postgres 17 with daily backups off this server." });
    expect(pg.getAttribute("href")).toBe("/p/acme/production/new/postgres");
    expect(screen.getAllByRole("menuitem")).toHaveLength(9);
    expect(route("/p/acme/production/new/postgres")).toEqual({ page: "new-service", params: { project: "acme", env: "production", kind: "postgres" } });
    expect(route("/p/acme/production/new")).toEqual({ page: "new-service", params: { project: "acme", env: "production", kind: "" } });
  });
});
