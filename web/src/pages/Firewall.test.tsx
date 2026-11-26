import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { chooseOption, fail, fakeApi } from "../test-api";
import { Firewall, portError } from "./Firewall";

const status = {
  on: true, available: true, ufw: false, version: "v1",
  bans: [{ ip: "192.0.2.9", until: Date.now() + 3_600_000 }],
  ports: [
    { proto: "tcp", port: 22, address: "0.0.0.0", process: "sshd", exposure: "everyone" },
    { proto: "tcp", port: 6379, address: "0.0.0.0", process: "redis-server", exposure: "closed" },
    { proto: "tcp", port: 8080, address: "::", process: "docker-proxy", exposure: "some" },
  ],
};
const yawl = [{ proto: "tcp", port: 22, why: "SSH (the port sshd listens on)" }, { proto: "tcp", port: 80, why: "Web traffic" }];
const off = { rules: { on: false, allow: [], block: [], ssh_from: [] }, yawl, your_address: "203.0.113.7", status: { ...status, on: false, bans: [] }, telegram: false };
const on = { ...off, rules: { on: true, allow: [{ proto: "tcp", port: 8080, from: ["203.0.113.0/24"] }], block: [], ssh_from: [] }, status };

describe("Firewall", () => {
  it("turns on, then asks to keep with a countdown", async () => {
    let view: unknown = off;
    const calls = fakeApi({
      "GET /api/servers/hel1/firewall": () => view,
      "PUT /api/servers/hel1/firewall": () => ((view = { ...off, trial_until: Date.now() + 120_000 }), { trial_until: Date.now() + 120_000 }),
      "POST /api/servers/hel1/firewall/keep": () => ((view = on), { kept: true }),
    });
    render(<Firewall name="hel1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Turn on" }));
    expect(await screen.findByText("Check that you can still reach this server by SSH. Keep these rules?")).toBeTruthy();
    expect(screen.getByText(/\d+ s left/)).toBeTruthy();
    expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ on: true, allow: [], block: [], ssh_from: [] });
    fireEvent.click(screen.getByRole("button", { name: "Keep" }));
    expect(await screen.findByText("SSH (the port sshd listens on)")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/keep"))).toBe(true);
  });

  it("adds an allow rule and saves; a 409 offers Save anyway", async () => {
    let n = 0;
    const calls = fakeApi({
      "GET /api/servers/hel1/firewall": on,
      "PUT /api/servers/hel1/firewall": () => (++n === 1 ? fail(409, "Your address 203.0.113.7 is not in the list.") : { trial_until: Date.now() + 120_000 }),
    });
    render(<Firewall name="hel1" />);
    const sshFrom = await screen.findByLabelText("SSH from");
    fireEvent.change(sshFrom, { target: { value: "198.51.100.0/24" } });
    fireEvent.keyDown(sshFrom, { key: "Enter" });
    fireEvent.change(screen.getByLabelText("Port"), { target: { value: "9000" } });
    await chooseOption("Protocol", "UDP");
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Your address 203.0.113.7 is not in the list.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Save anyway" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT").length).toBe(2));
    expect(calls.filter((c) => c.method === "PUT")[1].body).toEqual({
      on: true, allow: [{ proto: "tcp", port: 8080, from: ["203.0.113.0/24"] }, { proto: "udp", port: 9000, from: [] }],
      block: [], ssh_from: ["198.51.100.0/24"], confirm_address: true,
    });
  });

  it("lists bans with Unban and the open ports in words", async () => {
    const calls = fakeApi({ "GET /api/servers/hel1/firewall": on, "POST /api/servers/hel1/firewall/unban": null });
    render(<Firewall name="hel1" />);
    expect(await screen.findByText("192.0.2.9")).toBeTruthy();
    expect(screen.getByText("open to everyone")).toBeTruthy();
    expect(screen.getByText("closed by the firewall")).toBeTruthy();
    expect(screen.getByText("open to some")).toBeTruthy();
    expect(screen.getByText("redis-server")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Unban 192.0.2.9" }));
    await waitFor(() => expect(calls.find((c) => c.url.endsWith("/unban"))?.body).toEqual({ ip: "192.0.2.9" }));
  });

  it("says when nftables is missing, and when ufw is on too", async () => {
    fakeApi({ "GET /api/servers/hel1/firewall": { ...off, status: { ...status, available: false, ufw: true } } });
    render(<Firewall name="hel1" />);
    expect(await screen.findByText("The firewall needs nftables (Debian 12, Ubuntu 22.04 or later).")).toBeTruthy();
    expect(screen.getByText("ufw is on too; both apply.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Turn on" })).toBeNull();
  });

  it("writes protocols in capitals everywhere", async () => {
    fakeApi({ "GET /api/servers/hel1/firewall": on });
    render(<Firewall name="hel1" />);
    expect((await screen.findAllByText("TCP 8080")).length).toBeGreaterThan(0);
    expect(screen.getByText("TCP 6379")).toBeTruthy();
    expect(screen.queryByText(/\btcp \d/)).toBeNull();
  });

  it("portError accepts a whole port from 1 to 65535 and refuses the rest", () => {
    const msg = "A port is a whole number from 1 to 65535.";
    for (const ok of ["22", " 22 ", "1", "65535"]) expect(portError(ok)).toBe("");
    for (const bad of ["0", "65536", "22.5", "ssh", ""]) expect(portError(bad)).toBe(msg);
  });

  it("refuses a port that is not a port, inline", async () => {
    fakeApi({ "GET /api/servers/hel1/firewall": on });
    render(<Firewall name="hel1" />);
    fireEvent.change(await screen.findByLabelText("Port"), { target: { value: "ssh" } });
    const rows = screen.getAllByRole("row").length;
    fireEvent.click(screen.getByRole("button", { name: "Add rule" }));
    expect(screen.getByText("A port is a whole number from 1 to 65535.")).toBeTruthy();
    expect(screen.queryByText(/NaN/)).toBeNull();
    expect(screen.getAllByRole("row")).toHaveLength(rows);
  });

  it("asks before turning off", async () => {
    const calls = fakeApi({ "GET /api/servers/hel1/firewall": on, "PUT /api/servers/hel1/firewall": { trial_until: 0 } });
    render(<Firewall name="hel1" />);
    fireEvent.click(await screen.findByRole("button", { name: "Turn off" }));
    await screen.findByText("Turn off the firewall?");
    expect(screen.getByText("Every port opens to everyone.")).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, turn it off" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toMatchObject({ on: false }));
  });
});
