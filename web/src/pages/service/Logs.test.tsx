import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Logs } from "./Logs";
import { S } from "./fixtures";

const line = (t: string, text: string) => ({ Time: t, Deploy: "dep_1", Text: text });
const tail = () => screen.getByRole("button", { name: "Live tail" });

describe("Logs", () => {
  const tz = process.env.TZ;
  beforeEach(() => { vi.useFakeTimers(); localStorage.clear(); });
  afterEach(() => { vi.useRealTimers(); process.env.TZ = tz; localStorage.clear(); });
  const flush = () => act(async () => { await vi.advanceTimersByTimeAsync(0); });

  it("live tails from the last line, searches after a pause, and never renders HTML", async () => {
    const calls = fakeApi({ [`GET ${S}/logs`]: (_b: unknown, url: string) => ({ Lines: url.includes("since") ? [line("2026-10-24T10:00:03Z", "later")] : [line("2026-10-24T10:00:01Z", "<b>x</b>")] }) });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    expect(screen.getByText("<b>x</b>")).toBeTruthy();
    expect(document.querySelector(".logs b")).toBeNull();
    expect(screen.getByText("Secrets are masked before lines leave the server.")).toBeTruthy();
    fireEvent.click(tail());
    expect(tail().getAttribute("aria-pressed")).toBe("true");
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    const follow = calls.filter((c) => c.url.includes("since="));
    expect(follow.length).toBe(1);
    expect(decodeURIComponent(follow[0].url)).toContain("since=2026-10-24T10:00:01Z");
    expect(screen.getByText("later")).toBeTruthy();
    fireEvent.click(tail());
    const logCalls = () => calls.filter((c) => c.url.includes("/logs?")).length;
    const n = logCalls();
    await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
    expect(logCalls()).toBe(n);
    fireEvent.change(screen.getByLabelText("Search"), { target: { value: "error" } });
    await act(async () => { await vi.advanceTimersByTimeAsync(299); });
    expect(calls.some((c) => c.url.includes("q=error"))).toBe(false);
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(calls.some((c) => c.url.includes("q=error"))).toBe(true);
  });

  it("reads older lines from the server's Loki when it has a drain", async () => {
    const calls = fakeApi({ [`GET ${S}/logs`]: (_b: unknown, url: string) => url.includes("from=drain")
      ? { Lines: [line("2026-10-20T08:00:00Z", "from loki")], Drain: true }
      : { Lines: [line("2026-10-24T10:00:01Z", "here")], Drain: true } });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "168" } });
    await flush();
    const q = calls.find((c) => c.url.includes("from=drain"));
    expect(q && decodeURIComponent(q.url)).toContain("since=");
    expect(screen.getByText("from loki")).toBeTruthy();
    expect((tail() as HTMLButtonElement).disabled).toBe(true);
  });

  it("marks the lines the app wrote to stderr (ROADMAP 35)", async () => {
    fakeApi({ [`GET ${S}/logs`]: { Lines: [line("2026-10-24T10:00:01Z", "out"), { ...line("2026-10-24T10:00:02Z", "err"), Stream: "stderr" }] } });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    expect(screen.getByText("out").closest(".ln")!.className).toBe("ln");
    expect(screen.getByText("err").closest(".ln")!.className).toBe("ln se");
  });

  it("shows errors only: stderr lines, parsed 5xx and error-level JSON, without a server filter", async () => {
    const json = (o: object) => JSON.stringify({ message: "m", ...o });
    const calls = fakeApi({ [`GET ${S}/logs`]: { Lines: [
      line("2026-10-24T10:00:01Z", "plain out"),
      { ...line("2026-10-24T10:00:02Z", "plain err"), Stream: "stderr" },
      line("2026-10-24T10:00:03Z", json({ message: "boom", statusCode: "503" })),
      line("2026-10-24T10:00:04Z", json({ message: "denied", statusCode: "401", level: "INFO" })),
      line("2026-10-24T10:00:05Z", json({ message: "fell over", level: "ERROR" })),
    ] } });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    const errs = screen.getByRole("button", { name: "Errors only" });
    expect(errs.getAttribute("aria-pressed")).toBe("false");
    expect(screen.getAllByText(/./, { selector: ".tx" })).toHaveLength(5);
    fireEvent.click(errs);
    await flush();
    expect(errs.getAttribute("aria-pressed")).toBe("true");
    expect(calls.some((c) => c.url.includes("stream="))).toBe(false);
    expect(calls[calls.length - 1].url).toContain("n=1000");
    const shown = [...document.querySelectorAll(".tx")].map((x) => x.textContent);
    expect(shown).toEqual(["plain err", "503boom", "ERRORfell over"]);
  });

  it("offers Loki only when the server has a drain", async () => {
    fakeApi({ [`GET ${S}/logs`]: { Lines: [] } });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    expect([...(screen.getByLabelText("From") as HTMLSelectElement).options].map((o) => o.text)).toEqual(["This server"]);
  });

  it("shows times in the local zone, or in UTC, and remembers the choice", async () => {
    process.env.TZ = "America/Los_Angeles";
    fakeApi({ [`GET ${S}/logs`]: { Lines: [line("2026-10-24T22:00:01Z", "hi")] } });
    const { unmount } = render(<Logs project="acme" env="production" service="api" />);
    await flush();
    expect(screen.getByText("24 Oct 15:00:01")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Times in P[SD]T$/ }));
    expect(screen.getByText("24 Oct 22:00:01")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Times in UTC" }).textContent).toBe("UTC");
    expect(localStorage.getItem("yawl.logs.utc")).toBe("1");
    unmount();
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    expect(screen.getByText("24 Oct 22:00:01")).toBeTruthy();
  });

  it("fills the screen until the button or Escape", async () => {
    fakeApi({ [`GET ${S}/logs`]: { Lines: [] } });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    const panel = document.querySelector(".logview")!;
    const btn = screen.getByRole("button", { name: "Full screen" });
    fireEvent.click(btn);
    expect(panel.className).toContain("full");
    expect(btn.getAttribute("aria-pressed")).toBe("true");
    fireEvent.keyDown(document, { key: "Escape" });
    expect(panel.className).not.toContain("full");
    fireEvent.click(btn);
    fireEvent.click(btn);
    expect(panel.className).not.toContain("full");
  });

  it("keeps a live tail at the bottom unless the reader scrolled up, and jumps to the top", async () => {
    let n = 0;
    fakeApi({ [`GET ${S}/logs`]: () => ({ Lines: [line(`2026-10-24T10:00:${String(++n).padStart(2, "0")}Z`, `line ${n}`)] }) });
    render(<Logs project="acme" env="production" service="api" />);
    await flush();
    const log = screen.getByRole("log");
    let top = 0;
    Object.defineProperty(log, "scrollHeight", { configurable: true, get: () => 1000 });
    Object.defineProperty(log, "clientHeight", { configurable: true, get: () => 100 });
    Object.defineProperty(log, "scrollTop", { configurable: true, get: () => top, set: (v: number) => { top = v; } });
    fireEvent.click(tail());
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(top).toBe(1000);
    top = 200;
    fireEvent.scroll(log);
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(top).toBe(200);
    fireEvent.click(screen.getByRole("button", { name: "Jump to top" }));
    expect(top).toBe(0);
  });
});

describe("Logs, loading and errors", () => {
  beforeEach(() => { vi.useFakeTimers(); localStorage.clear(); });
  afterEach(() => { vi.useRealTimers(); localStorage.clear(); });

  it("shows the skeleton, not No lines., until the first answer", async () => {
    let answer!: (v: unknown) => void;
    fakeApi({ [`GET ${S}/logs`]: new Promise((r) => (answer = r)) });
    render(<Logs project="acme" env="production" service="api" />);
    expect(screen.getAllByRole("status").length).toBeGreaterThan(0);
    expect(screen.queryByText("No lines.")).toBeNull();
    await act(async () => { answer({ Lines: [] }); await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("No lines.")).toBeTruthy();
  });

  it("clears the error banner after the next good poll", async () => {
    let n = 0;
    fakeApi({ [`GET ${S}/logs`]: (_b: unknown, url: string) => url.includes("since") && n++ === 0 ? fail(500, "agent down") : { Lines: [line("2026-10-24T10:00:01Z", "x")] } });
    render(<Logs project="acme" env="production" service="api" />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    fireEvent.click(tail());
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByText("agent down")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.queryByText("agent down")).toBeNull();
  });
});
