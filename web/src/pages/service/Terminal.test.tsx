import { describe, expect, it, vi } from "vitest";
import { render, screen, act, waitFor, fireEvent } from "@testing-library/react";

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 100; rows = 30; written: Uint8Array[] = []; data?: (d: string) => void;
    loadAddon() {} open() {} dispose() {}
    write(b: Uint8Array) { this.written.push(b); }
    onData(f: (d: string) => void) { this.data = f; }
  },
}));
vi.mock("@xterm/addon-fit", () => ({ FitAddon: class { fit() {} } }));
vi.mock("@xterm/xterm/css/xterm.css", () => ({}));

class FakeWS {
  static last: FakeWS;
  readyState = 1; binaryType = ""; sent: unknown[] = [];
  onopen?: () => void; onmessage?: (e: { data: unknown }) => void; onclose?: () => void;
  constructor(public url: string) { FakeWS.last = this; }
  send(b: unknown) { this.sent.push(b); }
  close() {}
}

import { Terminal } from "./Terminal";
import { fakeApi } from "../../test-api";
import { apiView, S } from "./fixtures";

function openShell() {
  render(<Terminal project="acme" env="production" service="api" />);
  fireEvent.click(screen.getByRole("button", { name: "Open terminal" }));
}

describe("Terminal", () => {
  it("starts no session until Open terminal is clicked, and names the service and environment", () => {
    const stub = vi.fn(function (this: unknown, url: string) { return new FakeWS(url); });
    vi.stubGlobal("WebSocket", stub);
    render(<Terminal project="acme" env="production" service="api" />);
    expect(screen.getByText("Open a shell in api (production)")).toBeTruthy();
    expect(stub).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Open terminal" }));
    expect(stub).toHaveBeenCalledTimes(1);
  });

  it("shows the environment in a strip, and End session returns to the card", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    openShell();
    expect(document.querySelector(".term-strip.prod")?.textContent).toContain("production");
    fireEvent.click(screen.getByRole("button", { name: "End session" }));
    expect(screen.getByRole("button", { name: "Open terminal" })).toBeTruthy();
    expect(document.querySelector(".term-strip")).toBeNull();
  });

  it("opens the service's shell sized to the window, and says when it ends", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    openShell();
    expect(FakeWS.last.url).toMatch(/\/api\/services\/acme\/production\/api\/terminal\?cols=100&rows=30$/);
    act(() => FakeWS.last.onopen?.());
    act(() => FakeWS.last.onmessage?.({ data: JSON.stringify({ end: "the shell ended" }) }));
    expect(screen.getByText("Session ended: the shell ended.")).toBeTruthy();
  });

  it("shows why the panel refused the terminal", async () => {
    vi.stubGlobal("WebSocket", FakeWS);
    vi.stubGlobal("fetch", vi.fn(async () => new Response("open a terminal is for Admins.", { status: 403 })));
    openShell();
    act(() => FakeWS.last.onclose?.());
    await waitFor(() => expect(screen.getByText("Session ended: open a terminal is for Admins.")).toBeTruthy());
  });

  it("Reconnect opens a new session after it ends", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    openShell();
    const first = FakeWS.last;
    act(() => first.onopen?.());
    act(() => first.onclose?.());
    fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(FakeWS.last).not.toBe(first);
    expect(FakeWS.last.url).toBe(first.url);
    expect(screen.queryByText(/Session ended/)).toBeNull();
  });

  describe("recent sessions", () => {
    const opened = (at: number, actor: string, container: string) => ({ at, actor, kind: "terminal.opened", text: "terminal.opened", container });
    const audit = (rows: unknown[]) => ({ [`GET ${S}`]: apiView, [`GET /api/audit?from=0&q=${encodeURIComponent(`"service":"${apiView.id}"`)}`]: { rows, more: false } });

    it("lists who opened a shell, newest first, and leaves out other events", async () => {
      fakeApi(audit([opened(1_000, "dilnoza", "yawl-api-1"), { at: 3_000, actor: "owner", kind: "audit", text: "stopped a service" }, opened(2_000, "owner", "yawl-api-2")]));
      render(<Terminal project="acme" env="production" service="api" />);
      const list = await screen.findByRole("list", { name: "Recent sessions" });
      expect([...list.querySelectorAll("li")].map((li) => li.firstChild?.textContent)).toEqual(["owner", "dilnoza"]);
      expect(list.textContent).toContain("yawl-api-2");
      expect(list.textContent).not.toContain("stopped a service");
    });

    it("says when nobody opened one yet", async () => {
      fakeApi(audit([]));
      render(<Terminal project="acme" env="production" service="api" />);
      expect(await screen.findByText("No sessions yet.")).toBeTruthy();
    });
  });
});
