import { inputVariants } from "@cloudflare/kumo";
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { POLL_MS } from "../../ui";
import { LogAlerts } from "./LogAlerts";
import { S } from "./fixtures";

const rules = { rules: [
  { id: "la_1", text: "", lines: 20, minutes: 5, open: true },
  { id: "la_2", text: "timeout", lines: 0, minutes: 10, open: false },
] };

describe("LogAlerts", () => {
  it("lists the rules with their state, adds one and removes one", async () => {
    const calls = fakeApi({ [`GET ${S}/log-alerts`]: rules, [`PUT ${S}/log-alerts`]: (b: unknown) => b });
    render(<LogAlerts project="acme" env="production" service="api" />);
    const first = (await screen.findByText("Stderr lines")).closest("tr")!;
    expect(within(first).getByText("Open")).toBeTruthy();
    expect(within(first).getByText("More than 20 in 5 minutes")).toBeTruthy();
    const second = screen.getByText('Lines with "timeout"').closest("tr")!;
    expect(within(second).getByText("Quiet")).toBeTruthy();
    expect(within(second).getByText("Any in 10 minutes")).toBeTruthy();

    fireEvent.change(screen.getByLabelText("Text"), { target: { value: "  OOM  " } });
    fireEvent.change(screen.getByLabelText("More than"), { target: { value: "2" } });
    fireEvent.change(screen.getByLabelText("Within, minutes"), { target: { value: "15" } });
    fireEvent.click(screen.getByRole("button", { name: "Add alert" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ rules: [
      { id: "la_1", text: "", lines: 20, minutes: 5 },
      { id: "la_2", text: "timeout", lines: 0, minutes: 10 },
      { text: "OOM", lines: 2, minutes: 15 },
    ] });

    fireEvent.click(screen.getByRole("button", { name: "Remove alert 1" }));
    await screen.findByText("Remove this alert?");
    expect(calls.filter((c) => c.method === "PUT").length).toBe(1);
    fireEvent.click(screen.getByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT").length).toBe(2));
    expect(calls.filter((c) => c.method === "PUT")[1].body).toEqual({ rules: [{ id: "la_2", text: "timeout", lines: 0, minutes: 10 }] });
  });

  it("says why a save was refused, and hides the form at five rules", async () => {
    const five = { rules: [1, 2, 3, 4, 5].map((i) => ({ id: "la_" + i, text: "", lines: i, minutes: 5, open: false })) };
    fakeApi({ [`GET ${S}/log-alerts`]: five, [`PUT ${S}/log-alerts`]: () => fail(403, "a Viewer cannot change this") });
    render(<LogAlerts project="acme" env="production" service="api" />);
    await screen.findByText("More than 1 in 5 minutes");
    expect(screen.queryByRole("button", { name: "Add alert" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Remove alert 1" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    const dialog = (await screen.findByText("a Viewer cannot change this")).closest("[role=dialog]")!;
    expect(dialog.textContent).toContain("Remove this alert?");
  });

  it("two quick clicks on Add alert send one PUT, and the button is disabled meanwhile", async () => {
    let release!: () => void;
    const calls = fakeApi({
      [`GET ${S}/log-alerts`]: rules,
      [`PUT ${S}/log-alerts`]: () => new Promise((r) => { release = () => r(null); }),
    });
    render(<LogAlerts project="acme" env="production" service="api" />);
    const b = await screen.findByRole("button", { name: "Add alert" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect((screen.getByRole("button", { name: "Add alert" }) as HTMLButtonElement).disabled).toBe(true));
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
    release();
    await waitFor(() => expect((screen.getByRole("button", { name: "Add alert" }) as HTMLButtonElement).disabled).toBe(false));
  });
});

describe("LogAlerts, live", () => {
  afterEach(() => vi.useRealTimers());

  it("re-reads the list every POLL_MS, and its inputs are styled", async () => {
    vi.useFakeTimers();
    const calls = fakeApi({ [`GET ${S}/log-alerts`]: rules });
    const { unmount } = render(<LogAlerts project="acme" env="production" service="api" />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    for (const l of ["Text", "More than", "Within, minutes"]) expect(screen.getByLabelText(l).className).toBe(inputVariants({ focusIndicator: true }));
    const n = calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS); });
    expect(calls.length).toBe(n + 1);
    unmount();
  });
});
