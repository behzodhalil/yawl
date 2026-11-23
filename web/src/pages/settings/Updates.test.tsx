import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Updates } from "./Updates";

afterEach(() => vi.useRealTimers());

describe("Updates", () => {
  it("updates, says the panel restarts, and polls until the new version answers", async () => {
    let n = 0;
    fakeApi({
      "GET /api/update": () => (n++ < 2 ? { current: "1.1.0", latest: "1.2.0" } : { current: "1.2.0", latest: "1.2.0" }),
      "POST /api/update": { version: "1.2.0" },
    });
    render(<Updates />);
    await screen.findByText("v1.1.0");
    expect(screen.getByText("v1.2.0 is out.")).toBeTruthy();
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Update to v1.2.0" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("The panel is restarting…")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(4000); });
    expect(screen.getByText("The panel runs v1.2.0.")).toBeTruthy();
  });
  it("shows why a build cannot update", async () => {
    fakeApi({ "GET /api/update": { current: "dev", error: "This is a development build; it does not update itself." } });
    render(<Updates />);
    await screen.findByText("This is a development build; it does not update itself.");
    expect(screen.queryByRole("button", { name: /Update/ })).toBeNull();
  });

  it("stops when the backup before it fails, and goes on only when asked", async () => {
    let tries = 0;
    const calls = fakeApi({
      "GET /api/update": { current: "1.1.0", latest: "1.2.0" },
      "POST /api/update": () => (tries++ === 0 ? fail(409, "The backup before the update failed: upload to b: 403. Fix it, or update without a backup.") : { version: "1.2.0", backup: "skipped" }),
    });
    render(<Updates />);
    fireEvent.click(await screen.findByRole("button", { name: "Update to v1.2.0" }));
    expect(await screen.findByText(/The backup before the update failed/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Update without a backup" }));
    expect(calls.filter((c) => c.method === "POST").length).toBe(1);
    fireEvent.click(screen.getByRole("button", { name: "Update with no backup" }));
    expect(await screen.findByText("The panel is restarting…")).toBeTruthy();
    expect(calls.filter((c) => c.method === "POST")[1].body).toEqual({ version: "1.2.0", skip_backup: true });
  });

  it("says when no backup was taken, and holds the button while the backup runs", async () => {
    let answer: (v: unknown) => void = () => {};
    fakeApi({
      "GET /api/update": { current: "1.1.0", latest: "1.2.0" },
      "POST /api/update": () => new Promise((r) => (answer = r)),
    });
    render(<Updates />);
    const btn = await screen.findByRole("button", { name: "Update to v1.2.0" });
    fireEvent.click(btn);
    await screen.findByRole("button", { name: "Backing up and updating…" });
    expect((screen.getByRole("button", { name: "Backing up and updating…" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => answer({ version: "1.2.0", backup: "none: no backup storage is set" }));
    expect(await screen.findByText(/No backup was taken: no backup storage is set/)).toBeTruthy();
  });
});

describe("Updates timeout", () => {
  it("gives up after three minutes of restarting", async () => {
    fakeApi({ "GET /api/update": { current: "1.1.0", latest: "1.2.0" }, "POST /api/update": { version: "1.2.0" } });
    render(<Updates />);
    await screen.findByText("v1.1.0");
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Update to v1.2.0" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("The panel is restarting…")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(180_000); });
    expect(screen.getByText("The panel did not come back on 1.2.0. Check the server with yawl status.")).toBeTruthy();
    expect(screen.queryByText("The panel is restarting…")).toBeNull();
  });
});
