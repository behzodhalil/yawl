import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { POLL_MS } from "../../ui";
import { Runs } from "./Runs";

const R = "/api/services/acme/production/report/runs";
const at = Date.UTC(2026, 9, 27, 22, 0);
const data = {
  next: at + 86_400_000,
  timezone: "Asia/Tashkent",
  running: false,
  runs: [
    { ID: "r2", ScheduledAt: at + 60_000, StartedAt: 0, FinishedAt: at + 60_000, Exit: 0, Cause: "Skipped: the run before was still going.", Output: "", Skipped: true, TimedOut: false },
    { ID: "r1", ScheduledAt: at, StartedAt: at, FinishedAt: at + 41_000, Exit: 1, Cause: "", Output: "line 1\nboom", Skipped: false, TimedOut: false },
  ],
};

describe("Runs", () => {
  it("lists runs newest first with the next run time", async () => {
    fakeApi({ [`GET ${R}`]: data });
    render(<Runs project="acme" env="production" service="report" />);
    expect(await screen.findByText("Next run: 29 Oct 03:00 (Asia/Tashkent)")).toBeTruthy();
    const skip = screen.getByText("28 Oct 03:01, Skipped: the run before was still going.");
    const failed = screen.getByText("28 Oct 03:00, 41 s, exit 1");
    expect(skip.compareDocumentPosition(failed) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByText(/boom/)).toBeTruthy();
  });

  it("runs now, and says when a run is still going", async () => {
    let n = 0;
    const calls = fakeApi({ [`GET ${R}`]: data, [`POST ${R}`]: () => (n++ ? fail(409, "A run of report is still going.") : new Response(null, { status: 202 })) });
    render(<Runs project="acme" env="production" service="report" />);
    fireEvent.click(await screen.findByRole("button", { name: "Run now" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Run now" }));
    expect(await screen.findByText("A run of report is still going.")).toBeTruthy();
  });

  it("shows the server's words when the agent is away", async () => {
    fakeApi({ [`GET ${R}`]: fail(502, "hel1 is offline; the runs of report cannot be read now.") });
    render(<Runs project="acme" env="production" service="report" />);
    expect(await screen.findByText("hel1 is offline; the runs of report cannot be read now.")).toBeTruthy();
  });
});

describe("Runs, live", () => {
  afterEach(() => vi.useRealTimers());

  it("runs once for two clicks in one tick", async () => {
    const calls = fakeApi({ [`GET ${R}`]: data, [`POST ${R}`]: new Promise((r) => setTimeout(() => r(null), 20)) });
    render(<Runs project="acme" env="production" service="report" />);
    const b = await screen.findByRole("button", { name: "Run now" });
    act(() => { b.click(); b.click(); });
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });

  it("re-reads while a run is going, and stops when none is", async () => {
    vi.useFakeTimers();
    const going = { ...data, running: true, runs: [{ ...data.runs[1], FinishedAt: 0, Exit: 0 }] };
    const calls = fakeApi({ [`GET ${R}`]: going });
    const { unmount } = render(<Runs project="acme" env="production" service="report" />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const n = calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS); });
    expect(calls.length).toBeGreaterThan(n);
    unmount();
  });

  it("does not poll when nothing runs", async () => {
    vi.useFakeTimers();
    const calls = fakeApi({ [`GET ${R}`]: data });
    const { unmount } = render(<Runs project="acme" env="production" service="report" />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const n = calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS * 3); });
    expect(calls.length).toBe(n);
    unmount();
  });
});
