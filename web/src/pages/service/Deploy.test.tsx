import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, act, fireEvent, within, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Deploy, DeployEnded } from "./Deploy";

class FakeES {
  static last: FakeES;
  onmessage: ((e: MessageEvent) => void) | null = null;
  onerror: (() => void) | null = null;
  closed = false;
  constructor(public url: string) { FakeES.last = this; }
  close() { this.closed = true; }
  push(v: unknown) { act(() => this.onmessage?.(new MessageEvent("message", { data: JSON.stringify(v) }))); }
}

const base = { id: "dep_9", service: "api", commit: "4be81c0aa", subject: "Add PDF export", by: "owner", live: false, can_roll_back: false,
  rollback: false, created_at: 1, warnings: [] as string[] };
const step = (name: string, state: string, from: number, to?: number, output?: string) => ({ step: name, state, started_at: from, finished_at: to, output });

describe("Deploy", () => {
  it("streams the steps with their times and, on failure, what the failed step printed", () => {
    vi.stubGlobal("EventSource", FakeES);
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    expect(FakeES.last.url).toBe("/api/deploys/dep_9/stream");
    FakeES.last.push({ ...base, state: "building", steps: [step("build", "running", 1000)], warnings: ["The code reads SENTRY_DSN, which is not set in production."] });
    expect(screen.getByRole("heading").textContent).toBe("Add PDF export");
    expect(screen.getByText("Build").closest("tr")!.textContent).toContain("running");
    expect(screen.getByText("The code reads SENTRY_DSN, which is not set in production.").closest(".callout")!.className).toContain("warn");
    FakeES.last.push({ ...base, state: "failed", cause: "health check failed: GET /health answered 500 for 60 s",
      steps: [step("build", "ok", 0, 28_000), step("start", "ok", 28_000, 31_000), step("check", "failed", 31_000, 92_000, "GET /health 500\nError: Cannot find module 'pdfkit'\n    at /app/src/pdf.js:3:13")] });
    expect([...screen.getByText("Failed").classList]).toEqual(["badge", "bad"]);
    const rows = screen.getAllByRole("row").map((r) => r.textContent);
    expect(rows).toEqual(["Build28s", "Start3s", "Health check failed1m 01s"]);
    expect(screen.getByText("Health check failed").closest("tr")!.className).toContain("bad");
    const log = screen.getByTestId("log");
    const row = (t: string) => within(log).getByText(t).closest(".ln")!.className;
    expect(row("health check failed: GET /health answered 500 for 60 s")).toBe("ln er");
    expect(row("Error: Cannot find module 'pdfkit'")).toBe("ln er");
    expect(row("GET /health 500")).toBe("ln");
    expect(log.querySelector(".ts")).toBeNull();
    expect(FakeES.last.closed).toBe(true);
  });

  it("keeps a live deploy's output behind View log", () => {
    vi.stubGlobal("EventSource", FakeES);
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    FakeES.last.push({ ...base, state: "live", live: true, steps: [step("build", "ok", 0, 112_000, "#5 DONE 1.2s")] });
    expect(screen.getByText("Deployed").className).toContain("ok");
    expect(screen.queryByTestId("log")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "View log" }));
    expect(screen.getByTestId("log").textContent).toContain("#5 DONE 1.2s");
    const head = screen.getByTestId("log").querySelector(".ln.hd")!;
    expect(head.textContent).toBe("==> Build");
  });

  it("shows a build failure's output once", () => {
    vi.stubGlobal("EventSource", FakeES);
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    const out = "build failed: exit code 1\n#7 RUN npm ci\nnpm ERR! missing script";
    FakeES.last.push({ ...base, state: "failed", cause: out, steps: [step("build", "failed", 0, 40_000, out)] });
    const lines = [...screen.getByTestId("log").children].map((l) => l.textContent);
    expect(lines).toEqual(["build failed: exit code 1", "#7 RUN npm ci", "npm ERR! missing script"]);
    expect(within(screen.getByTestId("log")).getByText("build failed: exit code 1").closest(".ln")!.className).toBe("ln er");
  });

  it("says why for rolled back and replaced deploys", () => {
    vi.stubGlobal("EventSource", FakeES);
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    FakeES.last.push({ ...base, state: "rolled_back", cause: "health check failed after the switch",
      steps: [step("build", "ok", 0, 1000), step("check", "failed", 1000, 2000, "GET /health 500")] });
    expect(screen.getByTestId("log").textContent).toContain("health check failed after the switch");
    expect(screen.getByTestId("log").textContent).toContain("GET /health 500");
    FakeES.last.push({ ...base, state: "superseded", cause: "The panel was restored from a backup.", steps: [] });
    expect(screen.getByTestId("log").textContent).toBe("The panel was restored from a backup.");
  });
});

describe("Deploy, a restart (design record 36)", () => {
  it("says restart, not rollback", () => {
    vi.stubGlobal("EventSource", FakeES);
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    FakeES.last.push({ ...base, rollback: true, restart: true, state: "live", live: true, steps: [] });
    expect(screen.getByText(/· restart$/)).toBeTruthy();
    expect(screen.queryByText(/rollback/)).toBeNull();
  });
});

describe("Deploy, cancel (design record 40)", () => {
  it("cancels while it builds, and says Canceled", async () => {
    vi.stubGlobal("EventSource", FakeES);
    const calls = fakeApi({ "POST /api/deploys/dep_9/cancel": null });
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    FakeES.last.push({ ...base, state: "building", steps: [] });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(calls.some((c) => c.url === "/api/deploys/dep_9/cancel")).toBe(true));
    FakeES.last.push({ ...base, state: "superseded", cause: "Canceled.", steps: [] });
    expect(screen.getByText("Canceled")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });

});

describe("Deploy, a refused cancel (record 40, review I3)", () => {
  it("shows why", async () => {
    vi.stubGlobal("EventSource", FakeES);
    fakeApi({ "POST /api/deploys/dep_9/cancel": fail(409, "This deploy is past its build; it cannot be canceled now. Roll back once it ends.") });
    render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    FakeES.last.push({ ...base, state: "building", steps: [] });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await screen.findByText(/past its build/)).toBeTruthy();
  });
});

describe("Deploy, the service page around it", () => {
  it("tells the service page once when the deploy ends, so its Live line moves to the new version", () => {
    vi.stubGlobal("EventSource", FakeES);
    const ended = vi.fn();
    render(<DeployEnded.Provider value={ended}><Deploy project="acme" env="production" service="api" id="dep_9" /></DeployEnded.Provider>);
    FakeES.last.push({ ...base, state: "building", steps: [step("build", "running", 1000)] });
    expect(ended).not.toHaveBeenCalled();
    FakeES.last.push({ ...base, state: "live", live: true, steps: [step("build", "ok", 0, 28_000)] });
    expect(ended).toHaveBeenCalledTimes(1);
  });
});

describe("Deploy, a dropped stream", () => {
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
  class ES2 extends FakeES { static all: ES2[] = []; constructor(u: string) { super(u); ES2.all.push(this); } }

  it("says it lost the connection, reopens after 3 s, and clears the note on the next message", () => {
    vi.useFakeTimers();
    ES2.all = [];
    vi.stubGlobal("EventSource", ES2);
    const { unmount } = render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    ES2.all[0].push({ ...base, state: "building", steps: [] });
    act(() => ES2.all[0].onerror?.());
    expect(screen.getByText("Lost the live connection. Reconnecting…")).toBeTruthy();
    act(() => { vi.advanceTimersByTime(2999); });
    expect(ES2.all).toHaveLength(1);
    act(() => { vi.advanceTimersByTime(1); });
    expect(ES2.all).toHaveLength(2);
    ES2.all[1].push({ ...base, state: "building", steps: [] });
    expect(screen.queryByText("Lost the live connection. Reconnecting…")).toBeNull();
    unmount();
    act(() => ES2.all[1].onerror?.());
    act(() => { vi.advanceTimersByTime(10_000); });
    expect(ES2.all).toHaveLength(2);
  });

  it("does not reconnect once the deploy has ended", () => {
    vi.useFakeTimers();
    ES2.all = [];
    vi.stubGlobal("EventSource", ES2);
    const { unmount } = render(<Deploy project="acme" env="production" service="api" id="dep_9" />);
    ES2.all[0].push({ ...base, state: "live", live: true, steps: [] });
    act(() => ES2.all[0].onerror?.());
    act(() => { vi.advanceTimersByTime(10_000); });
    expect(ES2.all).toHaveLength(1);
    unmount();
  });
});
