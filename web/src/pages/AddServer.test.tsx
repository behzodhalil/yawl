import { describe, expect, it } from "vitest";
import { waitFor } from "@testing-library/react";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { AddServer } from "./AddServer";

describe("AddServer", () => {
  it("makes the command on request and shows it with copy", async () => {
    const calls = fakeApi({ "POST /api/join-token": { command: "yawl agent join https://panel.acme.uz TOKEN" }, "GET /api/hetzner": { connected: false } });
    render(<AddServer />);
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Make the join command" }));
    await screen.findByText("yawl agent join https://panel.acme.uz TOKEN");
    expect(screen.getByText("Good for one hour and one use.")).toBeTruthy();
    expect(screen.getByRole("button", { name: /Copy/ })).toBeTruthy();
  });

  it("connects Hetzner, then makes a server there (design record 58)", async () => {
    let connected = false;
    const calls = fakeApi({
      "GET /api/hetzner": () => connected
        ? { connected: true, locations: [{ name: "hel1", city: "Helsinki" }], types: [{ name: "cx22", cores: 2, memory: 4, disk: 40, prices: { hel1: "4.51" } }],
            made: [{ name: "old1", ip: "203.0.113.9", at: 1, late: true }] }
        : { connected: false },
      "PUT /api/settings/hetzner": () => { connected = true; return null; },
      "POST /api/hetzner/servers": { id: 42, ip: "203.0.113.5" },
    });
    render(<AddServer />);
    fireEvent.change(await screen.findByLabelText("Hetzner API token"), { target: { value: "hz-tok" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect Hetzner" }));
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "fsn2" } });
    expect(screen.getByRole("option", { name: "cx22: 2 cores, 4 GB, 40 GB disk, 4.51 € a month" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Create the server" }));
    await screen.findByText("fsn2 is being made at 203.0.113.5. It installs Yawl and joins in a few minutes, then appears in Servers.");
    expect(screen.getByText("old1 (203.0.113.9) did not join in 15 minutes. Read /var/log/cloud-init-output.log on it, or delete it in the Hetzner console.")).toBeTruthy();
    expect(calls.find((c) => c.method === "POST" && c.url === "/api/hetzner/servers")?.body).toEqual({ name: "fsn2", type: "cx22", location: "hel1" });
  });

  it("two quick clicks on Make the join command send one request", async () => {
    const calls = fakeApi({ "POST /api/join-token": { command: "yawl agent join X" }, "GET /api/hetzner": { connected: false } });
    render(<AddServer />);
    const b = screen.getByRole("button", { name: "Make the join command" });
    fireEvent.click(b);
    fireEvent.click(b);
    await screen.findByText("yawl agent join X");
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });
});
