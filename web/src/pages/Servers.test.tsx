import { describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Servers, attention } from "./Servers";
import type { ServerView } from "../types";
import { ServerOverview } from "./ServerOverview";

const gap = "This machine watches itself. If it goes down, no message can be sent. A second machine fixes this in a later version.";
const s = (name: string, extra = {}) => ({ id: name, name, public_ip: "1.2.3.4", agent_version: "dev", last_heartbeat: Date.now(), disk_used_pct: 40, offline: false, ...extra });

describe("Servers", () => {
  it("one server: the known gap, offline badge, disk bar", async () => {
    fakeApi({ "GET /api/servers": [s("hel1", { offline: true, disk_used_pct: 86 })] });
    const { container } = render(<Servers />);
    await screen.findByText("hel1");
    expect(screen.getByText(gap)).toBeTruthy();
    expect([...screen.getByText("Offline").classList]).toEqual(["badge", "off"]);
    expect(container.querySelector(".bar.warn")).toBeTruthy();
  });
  it("lists what needs attention per server", async () => {
    fakeApi({ "GET /api/servers": [s("hel1", { offline: true, firewall_off: true, reboot_required: true, forecast: { days: 12, gb_per_day: 1, growing: true } }), s("hk1", { forecast: { days: 90, gb_per_day: 1, growing: true } })] });
    const { container } = render(<Servers />);
    await screen.findByText("hel1");
    const col = [...container.querySelectorAll("th")].findIndex((th) => th.textContent === "Needs attention");
    const cell = container.querySelectorAll("tbody tr")[0].querySelectorAll("td")[col];
    expect(cell.textContent).toContain("Firewall off");
    expect(screen.getByText(/^Offline, last seen/)).toBeTruthy();
    expect(screen.getByText("Firewall off")).toBeTruthy();
    expect(screen.getByText("Disk full in 12 days")).toBeTruthy();
    expect(screen.getByText("Restart needed")).toBeTruthy();
    expect(screen.queryByText("Disk full in 90 days")).toBeNull();
  });
  it("an offline server that never sent a heartbeat gets a bare Offline chip", () => {
    expect(attention(s("a", { offline: true, last_heartbeat: 0 }) as ServerView).map((c) => c.text)).toEqual(["Offline"]);
  });
  it("says 1 day, not 1 days", () => {
    expect(attention(s("a", { forecast: { days: 1, gb_per_day: 1, growing: true } }) as ServerView).map((c) => c.text)).toEqual(["Disk full in 1 day"]);
  });
  it("two servers: no gap", async () => {
    fakeApi({ "GET /api/servers": [s("hel1"), s("hk1")] });
    render(<Servers />);
    await screen.findByText("hk1");
    expect(screen.queryByText(gap)).toBeNull();
    expect(screen.getAllByText("Online")).toHaveLength(2);
  });
  it("a joined server that never connected is not called online", async () => {
    fakeApi({ "GET /api/servers": [s("hel1"), s("two", { last_heartbeat: 0 })] });
    render(<Servers />);
    await screen.findByText("two");
    expect(screen.getAllByText("Online")).toHaveLength(1);
    expect([...screen.getByText("Never connected").classList]).toEqual(["badge", "off"]);
  });
  it("says the disk's course, in red within a week", async () => {
    fakeApi({ "GET /api/servers": [s("hel1", { disk_used_pct: 92, forecast: { days: 5, gb_per_day: 2.1, growing: true } }), s("hk1", { forecast: { days: null, gb_per_day: 0, growing: false } })] });
    render(<Servers />);
    const red = await screen.findByText("Disk 92% full, growing 2.1 GB a day: full in about 5 days.");
    expect(red.className).toContain("bad");
    expect(screen.queryByText("Disk 40% full, not growing.")).toBeNull();
  });
  it("shows the private address and each peer's last handshake", async () => {
    fakeApi({ "GET /api/servers": [
      s("hel1", { private_ip: "10.73.0.1", peers: [{ name: "fsn1", last_handshake: Date.now() - 30_000 }, { name: "nbg1", last_handshake: 0 }] }),
      s("old1", { network: "rejoin to use the private network" }),
    ] });
    render(<ServerOverview name="hel1" />);
    expect(await screen.findByText("10.73.0.1")).toBeTruthy();
    expect(screen.getByText(/fsn1: handshake 30 s ago/)).toBeTruthy();
    expect(screen.getByText(/nbg1: no handshake yet/)).toBeTruthy();
    cleanup();
    render(<ServerOverview name="old1" />);
    expect(await screen.findByText("rejoin to use the private network")).toBeTruthy();
  });
  it("asks again after POLL_MS", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      const calls = fakeApi({ "GET /api/servers": [s("hel1")] });
      render(<Servers />);
      await screen.findByText("hel1");
      const n = calls.filter((c) => c.url.startsWith("/api/servers")).length;
      await act(async () => { vi.advanceTimersByTime(5000); });
      expect(calls.filter((c) => c.url.startsWith("/api/servers")).length).toBeGreaterThan(n);
    } finally { cleanup(); vi.useRealTimers(); }
  });
});
