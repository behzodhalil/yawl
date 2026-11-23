import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { NewProject } from "./NewProject";

describe("NewProject", () => {
  it("sends you to join a server when there is none", async () => {
    fakeApi({ "GET /api/servers": [] });
    render(<NewProject />);
    await screen.findByText("Join a server first.");
    expect(screen.getByRole("link", { name: "Add a server" }).getAttribute("href")).toBe("/servers/new");
  });
  it("creates the project and opens its production environment", async () => {
    const calls = fakeApi({
      "GET /api/servers": [{ id: "srv_1", name: "hel1", public_ip: "1.2.3.4", last_heartbeat: 1, agent_version: "", disk_used_pct: 3, offline: false }],
      "POST /api/projects": { changes: [{ op: "create", kind: "environment", name: "production", detail: "on hel1" }] },
    });
    render(<NewProject />);
    await screen.findByRole("option", { name: "hel1 (1.2.3.4)" });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "acme" } });
    fireEvent.click(screen.getByRole("button", { name: "Create project" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production"));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ name: "acme", server: "hel1" });
  });
  it("creates once on a double click", async () => {
    const calls = fakeApi({
      "GET /api/servers": [{ id: "srv_1", name: "hel1", public_ip: "1.2.3.4", last_heartbeat: 1 }],
      "POST /api/projects": { changes: [] },
    });
    render(<NewProject />);
    await screen.findByRole("option", { name: "hel1 (1.2.3.4)" });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "acme" } });
    const b = screen.getByRole("button", { name: "Create project" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production"));
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
  });
  it("sends the server picked the moment its option appears", async () => {
    const calls = fakeApi({
      "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "1.2.3.4", last_heartbeat: 1 }, { id: "s2", name: "hk1", public_ip: "5.6.7.8", last_heartbeat: 1 }],
      "POST /api/projects": { changes: [] },
    });
    render(<NewProject />);
    await screen.findByRole("option", { name: "hk1 (5.6.7.8)" });
    fireEvent.change(screen.getByLabelText("Server"), { target: { value: "hk1" } });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "beta" } });
    fireEvent.click(screen.getByRole("button", { name: "Create project" }));
    await waitFor(() => expect(location.pathname).toBe("/p/beta/production"));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ name: "beta", server: "hk1" });
  });
  it("defaults to the first online server and disables offline ones with their age", async () => {
    const calls = fakeApi({
      "GET /api/servers": [
        { id: "s1", name: "hel1", public_ip: "1.2.3.4", offline: true, last_heartbeat: Date.now() - 3 * 3600_000 },
        { id: "s2", name: "hk1", public_ip: "5.6.7.8", last_heartbeat: 1, offline: false },
      ],
      "POST /api/projects": { changes: [] },
    });
    render(<NewProject />);
    const offline = await screen.findByRole("option", { name: "hel1 (1.2.3.4) · offline, last seen 3 h ago" });
    expect((offline as HTMLOptionElement).disabled).toBe(true);
    expect((screen.getByLabelText("Server") as HTMLSelectElement).value).toBe("hk1");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "gamma" } });
    fireEvent.click(screen.getByRole("button", { name: "Create project" }));
    await waitFor(() => expect(location.pathname).toBe("/p/gamma/production"));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ name: "gamma", server: "hk1" });
  });
  it("does not default to a server that never connected, and says so", async () => {
    fakeApi({
      "GET /api/servers": [
        { id: "s1", name: "hel1", public_ip: "1.2.3.4", offline: false },
        { id: "s2", name: "hk1", public_ip: "5.6.7.8", last_heartbeat: 1, offline: false },
      ],
    });
    render(<NewProject />);
    const fresh = await screen.findByRole("option", { name: "hel1 (1.2.3.4) · never connected" });
    expect((fresh as HTMLOptionElement).disabled).toBe(true);
    expect((screen.getByLabelText("Server") as HTMLSelectElement).value).toBe("hk1");
  });
  it("offers no server and no submit when every server is offline", async () => {
    fakeApi({ "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "1.2.3.4", offline: true }] });
    render(<NewProject />);
    await screen.findByText("No server is online.");
    expect(screen.queryByLabelText("Server")).toBeNull();
    expect(screen.queryByRole("button", { name: "Create project" })).toBeNull();
  });
});
