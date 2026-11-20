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

describe("Terminal (design record 42)", () => {
  it("opens the service's shell sized to the window, and says when it ends", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    render(<Terminal project="acme" env="production" service="api" />);
    expect(FakeWS.last.url).toMatch(/\/api\/services\/acme\/production\/api\/terminal\?cols=100&rows=30$/);
    act(() => FakeWS.last.onopen?.());
    act(() => FakeWS.last.onmessage?.({ data: JSON.stringify({ end: "the shell ended" }) }));
    expect(screen.getByText("Session ended: the shell ended.")).toBeTruthy();
  });

  it("shows why the panel refused the terminal", async () => {
    vi.stubGlobal("WebSocket", FakeWS);
    vi.stubGlobal("fetch", vi.fn(async () => new Response("open a terminal is for Admins.", { status: 403 })));
    render(<Terminal project="acme" env="production" service="api" />);
    act(() => FakeWS.last.onclose?.());
    await waitFor(() => expect(screen.getByText("Session ended: open a terminal is for Admins.")).toBeTruthy());
  });

  it("Reconnect opens a new session after it ends", () => {
    vi.stubGlobal("WebSocket", FakeWS);
    render(<Terminal project="acme" env="production" service="api" />);
    const first = FakeWS.last;
    act(() => first.onopen?.());
    act(() => first.onclose?.());
    fireEvent.click(screen.getByRole("button", { name: "Reconnect" }));
    expect(FakeWS.last).not.toBe(first);
    expect(FakeWS.last.url).toBe(first.url);
    expect(screen.queryByText(/Session ended/)).toBeNull();
  });
});
