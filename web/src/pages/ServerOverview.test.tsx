import { describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { fail, fakeApi } from "../test-api";
import { ServerOverview, rate } from "./ServerOverview";
import { Servers } from "./Servers";

const now = Date.now();
const base = { id: "srv_1", name: "hel1", public_ip: "198.51.100.10", agent_version: "1.0.0", last_heartbeat: now, disk_used_pct: 41, offline: false };
const facts = { os_name: "Ubuntu 24.04.1 LTS", os_id: "ubuntu", kernel: "6.8.0-48-generic", cores: 4, booted_at: now - 12 * 86_400_000, mem_total: 8e9, disk_total: 80e9 };
const point = (i: number, net: boolean) => ({ at: now - (3 - i) * 60_000, cpu: 20 + i, mem: 4e9 + i * 1e8, mem_total: 8e9, disk: 32e9, disk_total: 80e9, load: 0.5,
  ...(net ? { net_rx: 1.25e6, net_tx: 2.5e5 } : {}) });
const svc = (name: string, mem: number, mem_limit: number) => ({ project: "acme", env: "production", name, cpu: 12, mem, cpu_limit: 0, mem_limit });

describe("ServerOverview", () => {
  it("shows the machine's facts, its measures now and its services", async () => {
    fakeApi({
      "GET /api/servers": [{ ...base, ...facts }],
      "GET /api/servers/hel1/metrics": { range: "24h", step_s: 60, points: [0, 1, 2].map((i) => point(i, true)),
        services: [svc("api", 470e6, 512e6), svc("web", 200e6, 0)] },
    });
    const { container } = render(<ServerOverview name="hel1" />);
    expect(await screen.findByText("Ubuntu 24.04.1 LTS")).toBeTruthy();
    expect(screen.getByText("6.8.0-48-generic")).toBeTruthy();
    expect(screen.getByText("4 cores")).toBeTruthy();
    expect(screen.getByText("8 GB")).toBeTruthy();
    expect(screen.getByText("12 days ago")).toBeTruthy();
    expect(await screen.findByRole("img", { name: /^CPU now 22%/ })).toBeTruthy();
    expect(screen.getByText("4.2 GB of 8 GB")).toBeTruthy();
    expect(screen.getAllByText("In 10.0 Mbps").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Out 2.0 Mbps").length).toBeGreaterThan(0);
    expect(screen.getByRole("img", { name: /^Network now 10 Mbps/ })).toBeTruthy();
    expect(screen.getByText("470 MB of 512 MB")).toBeTruthy();
    expect(container.querySelectorAll(".sv-bar.warn")).toHaveLength(1);
    expect(screen.queryByText("Not reported")).toBeNull();
  });

  it("lists running services by CPU as links with one decimal and folds stopped containers away", async () => {
    const withCpu = (name: string, cpu: number, mem: number) => ({ ...svc(name, mem, 0), cpu });
    fakeApi({
      "GET /api/servers": [{ ...base, ...facts }],
      "GET /api/servers/hel1/metrics": { range: "24h", step_s: 60, points: [0, 1, 2].map((i) => point(i, true)),
        services: [withCpu("idle", 0.4, 1e6), withCpu("gone", 0, 0), withCpu("busy", 35.26, 2e6), withCpu("old", 0, 0)] },
    });
    const { container } = render(<ServerOverview name="hel1" />);
    expect(await screen.findByText("35.3%")).toBeTruthy();
    expect(screen.getByText("0.4%")).toBeTruthy();
    const rows = Array.from(container.querySelectorAll<HTMLAnchorElement>(".sv-list > a.sv-svc"));
    expect(rows.map((r) => r.querySelector("b")!.textContent)).toEqual(["busy", "idle", "gone", "old"]);
    expect(rows[0].getAttribute("href")).toBe("/p/acme/production/s/busy");
    const folded = container.querySelector("details")!;
    expect(folded.open).toBe(false);
    expect(folded.querySelector("summary")!.textContent).toBe("2 stopped containers");
    expect(folded.querySelectorAll("a.sv-svc")).toHaveLength(2);
  });

  it("says one stopped container in the singular and keeps the empty line above it", async () => {
    fakeApi({
      "GET /api/servers": [{ ...base, ...facts }],
      "GET /api/servers/hel1/metrics": { range: "24h", step_s: 60, points: [0, 1, 2].map((i) => point(i, true)),
        services: [svc("gone", 0, 0)] },
    });
    const { container } = render(<ServerOverview name="hel1" />);
    expect(await screen.findByText("No service is running here.")).toBeTruthy();
    expect(container.querySelector("summary")!.textContent).toBe("1 stopped container");
  });

  it("an older agent's server says what it did not report and never breaks", async () => {
    fakeApi({
      "GET /api/servers": [base],
      "GET /api/servers/hel1/metrics": { range: "24h", step_s: 60, points: [0, 1, 2].map((i) => point(i, false)) },
    });
    render(<ServerOverview name="hel1" />);
    await screen.findByRole("img", { name: /^CPU now/ });
    expect(screen.getAllByText("Not reported")).toHaveLength(6);
    expect(screen.getByText("No service is running here.")).toBeTruthy();
  });

  it("the list says each server's size when reported", async () => {
    fakeApi({ "GET /api/servers": [{ ...base, ...facts }, { ...base, id: "srv_2", name: "old1" }] });
    render(<Servers />);
    expect(await screen.findByText("4 cores, 8 GB")).toBeTruthy();
    expect(screen.getAllByText(/cores/)).toHaveLength(1);
  });

  it("says network speed in bits", () => {
    expect(rate(1.25e6)).toBe("10.0 Mbps");
    expect(rate(5000)).toBe("40 kbps");
    expect(rate(2.5e8)).toBe("2.0 Gbps");
  });
  it("asks again after POLL_MS", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      const calls = fakeApi({ "GET /api/servers": [{ ...base, ...facts }], "GET /api/servers/hel1/metrics": { range: "24h", step_s: 60, points: [point(0, true)] } });
      render(<ServerOverview name="hel1" />);
      await screen.findByText("Ubuntu 24.04.1 LTS");
      const n = calls.filter((c) => c.url.startsWith("/api/servers")).length;
      await act(async () => { vi.advanceTimersByTime(5000); });
      expect(calls.filter((c) => c.url.startsWith("/api/servers")).length).toBeGreaterThan(n);
    } finally { cleanup(); vi.useRealTimers(); }
  });

  describe("needs attention", () => {
    const metrics = { range: "24h", step_s: 60, points: [0, 1, 2].map((i) => point(i, true)) };
    const fw = (n: number) => ({ status: { ports: [...Array(n)].map(() => ({ proto: "tcp", port: 1, address: "0.0.0.0", exposure: "everyone" })).concat([{ proto: "tcp", port: 2, address: "127.0.0.1", exposure: "closed" }]) } });
    const show = (s: object, extra: Record<string, unknown> = {}) => {
      const calls = fakeApi({ "GET /api/servers": [{ ...base, ...s }], "GET /api/servers/hel1/metrics": metrics, "GET /api/servers/hel1/os": { Updates: 0, Security: 0 }, ...extra });
      render(<ServerOverview name="hel1" />);
      return calls;
    };

    it("is absent when nothing needs attention", async () => {
      const calls = show({});
      await screen.findByRole("img", { name: /^CPU now/ });
      expect(screen.queryByText("Needs attention")).toBeNull();
      expect(calls.some((c) => c.url.endsWith("/firewall"))).toBe(false);
    });

    it("links the firewall, disk and updates lines to their tabs and counts public ports", async () => {
      show({ firewall_off: true, forecast: { days: 17, gb_per_day: 1.25, growing: true } },
        { "GET /api/servers/hel1/firewall": fw(3), "GET /api/servers/hel1/os": { Updates: 12, Security: 4 } });
      const link = async (text: string) => (await screen.findByText(text)).closest("a")?.getAttribute("href");
      expect(await link("Firewall off, 3 public ports open")).toBe("/servers/hel1/firewall");
      expect(await link("Disk full in about 17 days, growing 1.3 GB a day")).toBe("/servers/hel1/disk");
      expect(await link("12 updates waiting, 4 security")).toBe("/servers/hel1/os");
    });

    it("drops the details it cannot load", async () => {
      show({ firewall_off: true }, { "GET /api/servers/hel1/firewall": fail(500, "agent offline"), "GET /api/servers/hel1/os": fail(500, "agent offline") });
      expect(await screen.findByText("Firewall off")).toBeTruthy();
      expect(screen.queryByText(/waiting/)).toBeNull();
    });

    it("says only offline for an offline server and asks the agent nothing", async () => {
      const calls = show({ offline: true, firewall_off: true });
      expect(await screen.findByText(/^Offline, last seen/)).toBeTruthy();
      expect(calls.some((c) => c.url.endsWith("/firewall") || c.url.endsWith("/os"))).toBe(false);
    });

    it("leaves the server list chips as they were", async () => {
      fakeApi({ "GET /api/servers": [{ ...base, firewall_off: true, forecast: { days: 17, gb_per_day: 1.25, growing: true } }] });
      render(<Servers />);
      expect(await screen.findByText("Firewall off")).toBeTruthy();
      expect(screen.getByText("Disk full in 17 days")).toBeTruthy();
    });
  });
});
