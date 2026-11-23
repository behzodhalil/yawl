import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { StatusPage, StatusPages } from "./StatusPages";

const now = Date.now();
const page = {
  id: "sp_1", slug: "acme", title: "Acme status", server_id: "srv_1", domain: "status.acme.uz", watcher: false,
  rows: [{ id: "sr_1", name: "API", subject: "service:svc_1" }], url: "https://acme.status.1-2-3-4.sslip.io",
  hosts: [
    { host: "acme.status.1-2-3-4.sslip.io", dns: "ok", dns_detail: "", cert: "issued" },
    { host: "status.acme.uz", dns: "wrong", dns_detail: "status.acme.uz has no A record yet. Set one: status.acme.uz → 1.2.3.4.", cert: "pending" },
  ],
};
const list = {
  pages: [page],
  choices: [{ subject: "panel", label: "The panel" }, { subject: "project:prj_1", label: "Project acme, every service in it" }, { subject: "service:svc_1", label: "acme/production/api" }],
  servers: [{ id: "srv_1", name: "hel1" }, { id: "srv_2", name: "hk1" }],
};
const draft = { id: "si_1", kind: "incident", title: "API is down", rows: ["sr_1"], state: "draft", auto: true, starts_at: 0, ends_at: 0, resolved_at: 0, created_at: now - 60_000, updates: [{ id: "su_1", at: now - 60_000, body: "We are looking into it." }] };
const open = { ...draft, id: "si_2", title: "Slow API", state: "published", auto: false };

describe("Status pages", () => {
  it("lists pages with each address and how its domain stands", async () => {
    fakeApi({ "GET /api/status-pages": list });
    render(<StatusPages />);
    expect(await screen.findByText("Acme status")).toBeTruthy();
    expect(screen.getByText("Live")).toBeTruthy();
    expect(screen.getByText("DNS not set")).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: "Services" })).toBeTruthy();
    expect(screen.getByRole("table").parentElement!.classList.contains("scroll")).toBe(true);
  });

  it("calls a status page's incidents public incidents", async () => {
    fakeApi({ "GET /api/status-pages": list, "GET /api/status-pages/sp_1/incidents": [] });
    render(<StatusPage id="sp_1" />);
    expect(await screen.findByText("Public incidents")).toBeTruthy();
    expect(await screen.findByText("No public incidents in the last 90 days.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "New public incident" }));
    expect(screen.getByRole("heading", { name: "New public incident" })).toBeTruthy();
  });

  it("makes a page with rows under public names", async () => {
    const calls = fakeApi({ "GET /api/status-pages": { ...list, pages: [] }, "POST /api/status-pages": { ...page, id: "sp_9" } });
    render(<StatusPage id="new" />);
    fireEvent.change(await screen.findByLabelText("Title"), { target: { value: "Acme" } });
    fireEvent.change(screen.getByLabelText("Short name"), { target: { value: "acme" } });
    fireEvent.change(screen.getByLabelText("Server"), { target: { value: "srv_2" } });
    fireEvent.click(screen.getByRole("button", { name: "Add row" }));
    fireEvent.change(screen.getByLabelText("Public name, row 1"), { target: { value: "Website" } });
    fireEvent.change(screen.getByLabelText("Covers, row 1"), { target: { value: "service:svc_1" } });
    fireEvent.click(screen.getByLabelText("Also on the watcher"));
    fireEvent.click(screen.getByRole("button", { name: "Make the page" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({
      title: "Acme", slug: "acme", server_id: "srv_2", domain: "", watcher: true, rows: [{ name: "Website", subject: "service:svc_1" }],
    });
    await waitFor(() => expect(location.pathname).toBe("/status-pages/sp_9"));
  });

  it("publishes an automatic draft, dismisses one, and posts an update that resolves", async () => {
    const calls = fakeApi({
      "GET /api/status-pages": list,
      "GET /api/status-pages/sp_1/incidents": [draft, open],
      "POST /api/status-incidents/si_1/publish": null,
      "DELETE /api/status-incidents/si_1": null,
      "POST /api/status-incidents/si_2/updates": null,
    });
    render(<StatusPage id="sp_1" />);
    expect(await screen.findByText("Automatic draft")).toBeTruthy();
    expect(screen.getByText("status.acme.uz has no A record yet. Set one: status.acme.uz → 1.2.3.4.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    fireEvent.click(screen.getByRole("button", { name: "Dismiss" }));
    fireEvent.change(screen.getByLabelText("Update for Slow API"), { target: { value: "Fixed." } });
    fireEvent.click(screen.getByRole("button", { name: "Post and resolve" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/si_2/updates"))).toBe(true));
    expect(calls.find((c) => c.url.endsWith("/si_2/updates"))!.body).toEqual({ body: "Fixed.", resolve: true });
    expect(calls.some((c) => c.url.endsWith("/si_1/publish"))).toBe(true);
    expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/si_1"))).toBe(true);
  });

  it("schedules maintenance for chosen rows", async () => {
    const calls = fakeApi({ "GET /api/status-pages": list, "GET /api/status-pages/sp_1/incidents": [], "POST /api/status-pages/sp_1/incidents": {} });
    render(<StatusPage id="sp_1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Schedule maintenance" }));
    fireEvent.change(screen.getAllByLabelText("Title")[0], { target: { value: "Database upgrade" } });
    fireEvent.change(screen.getByLabelText("Starts"), { target: { value: "2026-11-06T02:00" } });
    fireEvent.change(screen.getByLabelText("Ends"), { target: { value: "2026-11-06T03:00" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "API" }));
    fireEvent.click(screen.getByRole("button", { name: "Publish" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const body = calls.find((c) => c.method === "POST")!.body as { kind: string; rows: string[]; publish: boolean; starts_at: number; ends_at: number };
    expect(body.kind).toBe("maintenance");
    expect(body.rows).toEqual(["sr_1"]);
    expect(body.publish).toBe(true);
    expect(body.ends_at - body.starts_at).toBe(3_600_000);
  });

  it("dismisses an automatic draft in one click, with no confirm", async () => {
    const calls = fakeApi({
      "GET /api/status-pages": list,
      "GET /api/status-pages/sp_1/incidents": [draft],
      "DELETE /api/status-incidents/si_1": null,
    });
    render(<StatusPage id="sp_1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Dismiss" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/si_1"))).toBe(true));
    expect(screen.queryByText("Delete this public incident?")).toBeNull();
  });

  it("asks before deleting a published incident", async () => {
    const calls = fakeApi({
      "GET /api/status-pages": list,
      "GET /api/status-pages/sp_1/incidents": [open],
      "DELETE /api/status-incidents/si_2": null,
    });
    render(<StatusPage id="sp_1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Delete" }));
    await screen.findByText("Delete this public incident?");
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, delete" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/si_2"))).toBe(true));
  });
});
