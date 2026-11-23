import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { OSUpdates } from "./OSUpdates";

describe("OS updates", () => {
  it("says what waits, whether a restart is due, and installs on request", async () => {
    const calls = fakeApi({
      "GET /api/servers/hel1/os": { Updates: 12, Security: 5, Reboot: true, RebootPkgs: ["linux-image-6.8.0-48-generic"], Auto: true, Installer: true, Running: false, Log: "INFO All upgrades installed" },
      "POST /api/servers/hel1/os/upgrade": null,
    });
    render(<OSUpdates name="hel1" />);
    await screen.findByText("12 updates wait, 5 of them security updates.");
    expect(screen.getByText("Restart this server at a quiet time to finish an update (linux-image-6.8.0-48-generic): sudo reboot")).toBeTruthy();
    expect(screen.getByText("Security updates install by themselves each day.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Install updates now" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    await screen.findByText("Installing. It takes a few minutes; the log below shows when it is done.");
  });
  it("says so when the server cannot tell", async () => {
    fakeApi({ "GET /api/servers/hel1/os": { Updates: -1, Security: -1, Reboot: false, Auto: false, Installer: false, Running: false } });
    render(<OSUpdates name="hel1" />);
    await screen.findByText("This server does not say how many updates wait (no update-notifier).");
    expect(screen.getByText("unattended-upgrades is not installed: sudo apt-get install unattended-upgrades")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Install updates now" })).toBeNull();
  });
  it("encodes the server's name in the path", async () => {
    const calls = fakeApi({ "GET /api/servers/a%2Fb%3Fx/os": { Updates: 0, Security: 0, Reboot: false, Auto: true, Installer: true, Running: false } });
    render(<OSUpdates name="a/b?x" />);
    await waitFor(() => expect(calls[0]?.url).toBe("/api/servers/a%2Fb%3Fx/os"));
  });
  describe("polling", () => {
    afterEach(() => { cleanup(); vi.useRealTimers(); });
    const run = (Running: boolean) => ({ Updates: 1, Security: 0, Reboot: false, Auto: true, Installer: true, Running });
    const gets = async (Running: boolean) => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
      const calls = fakeApi({ "GET /api/servers/hel1/os": run(Running) });
      render(<OSUpdates name="hel1" />);
      await screen.findByText("1 updates wait, 0 of them security updates.");
      const before = calls.length;
      await act(async () => { vi.advanceTimersByTime(5000); });
      return calls.length - before;
    };
    it("asks again after POLL_MS while an update runs", async () => expect(await gets(true)).toBe(1));
    it("does not poll when idle", async () => expect(await gets(false)).toBe(0));
    it("stops polling once an install it started is done", async () => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
      let Running = false;
      const calls = fakeApi({ "GET /api/servers/hel1/os": () => run(Running), "POST /api/servers/hel1/os/upgrade": () => { Running = true; return null; } });
      render(<OSUpdates name="hel1" />);
      fireEvent.click(await screen.findByRole("button", { name: "Install updates now" }));
      await screen.findByText(/Installing\./);
      const tick = () => act(async () => { vi.advanceTimersByTime(5000); });
      const before = calls.length;
      await tick();
      expect(calls.length).toBeGreaterThan(before);
      Running = false;
      await tick();
      await waitFor(() => expect(screen.queryByText(/Installing\./)).toBeNull());
      const after = calls.length;
      await tick();
      await tick();
      expect(calls.length).toBe(after);
    });
  });
});
