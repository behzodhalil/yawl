import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Audit, auditQuery } from "./Audit";

process.env.TZ = "Asia/Tashkent";

const rows = [
  { at: Date.UTC(2026, 9, 28, 12, 5), actor: "API token ci", text: "set variable API_KEY on acme/production/api", kind: "audit" },
  { at: Date.UTC(2026, 9, 28, 12, 1), actor: "owner", text: "deployed acme/production/api", kind: "audit" },
];

const at = (d: number, h: number, m: number) => new Date(2026, 10, d, h, m).getTime();

describe("Audit", () => {
  afterEach(() => vi.useRealTimers());

  it("lists newest first, filters by who, and exports what it shows", async () => {
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    const first = await screen.findByText("set variable API_KEY on acme/production/api");
    expect(first.compareDocumentPosition(screen.getByText("deployed acme/production/api")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getAllByRole("option").map((o) => o.textContent)).toContain("API token ci");
    fireEvent.change(screen.getByLabelText("Range"), { target: { value: "custom" } });
    fireEvent.change(screen.getByLabelText("Who"), { target: { value: "owner" } });
    await waitFor(() => expect(calls.some((c) => c.url.includes("actor=owner"))).toBe(true));
    expect((screen.getByRole("link", { name: "Export CSV" }) as HTMLAnchorElement).getAttribute("href")).toBe("/api/audit.csv?actor=owner");
  });

  it("heads each day with its date and year in the browser's zone and shows only the time on the row", async () => {
    fakeApi({ "GET /api/audit": { rows: [{ at: Date.UTC(2025, 11, 30, 12, 2), actor: "owner", text: "last year", kind: "audit" }], more: false } });
    render(<Audit />);
    const row = (await screen.findByText("last year")).closest("li")!;
    expect(row.querySelector("time")!.textContent).toBe("17:02");
    expect(row.closest("section")!.querySelector("h3")!.textContent).toBe("30 Dec 2025");
  });

  it("groups rows under Today, Yesterday and older dates", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(at(24, 23, 30));
    fakeApi({ "GET /api/audit": { rows: [
      { at: at(24, 23, 18), actor: "owner@acme.uz", text: "deployed acme/production/web", kind: "audit" },
      { at: at(24, 0, 1), actor: "system", text: "deploy.live", kind: "deploy.live" },
      { at: at(23, 18, 2), actor: "owner@acme.uz", text: "env.revealed", kind: "env.revealed" },
      { at: at(21, 9, 0), actor: "owner@acme.uz", text: "backup.ok", kind: "backup.ok" },
    ], more: false } });
    render(<Audit />);
    await screen.findByText("deployed acme/production/web");
    const days = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(days).toEqual(["Today", "Yesterday", "21 Nov 2026"]);
    const today = screen.getByRole("heading", { name: "Today" }).closest("section")!;
    expect([...today.querySelectorAll("li")].map((li) => li.textContent)).toEqual([
      "23:18owner@acme.uz deployed acme/production/web",
      "00:01System completed a deployment",
    ]);
    expect(screen.getByText("System", { selector: "li span" }).className).toBe("who dim");
  });

  it("maps the range to days back from the start of today and shows From and To only for Custom", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(at(24, 15, 0));
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    await screen.findByText("deployed acme/production/api");
    expect(calls[0].url).toBe(`/api/audit?from=${at(18, 0, 0)}&page=0`);
    expect(screen.queryByLabelText("From")).toBeNull();
    fireEvent.change(screen.getByLabelText("Range"), { target: { value: "today" } });
    await waitFor(() => expect(calls.at(-1)!.url).toBe(`/api/audit?from=${at(24, 0, 0)}&page=0`));
    fireEvent.change(screen.getByLabelText("Range"), { target: { value: "30d" } });
    await waitFor(() => expect(calls.at(-1)!.url).toBe(`/api/audit?from=${new Date(2026, 9, 26).getTime()}&page=0`));
    fireEvent.change(screen.getByLabelText("Range"), { target: { value: "custom" } });
    await waitFor(() => expect(calls.at(-1)!.url).toBe("/api/audit?page=0"));
    expect(screen.getByLabelText("From")).toBeTruthy();
    expect(screen.getByLabelText("To")).toBeTruthy();
  });

  it("filters by days written either way, through the end of the last day", async () => {
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    await screen.findByText("deployed acme/production/api");
    fireEvent.change(screen.getByLabelText("Range"), { target: { value: "custom" } });
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "23 Nov 2026" } });
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-11-24" } });
    const from = Date.UTC(2026, 10, 22, 19);
    const to = Date.UTC(2026, 10, 24, 19);
    await waitFor(() => expect(calls.some((c) => c.url.includes(`from=${from}&to=${to}`))).toBe(true));
    expect(screen.queryByText("Use a date like 23 Nov 2026")).toBeNull();
  });

  it("says how to write a day it cannot read only after the field is left, and does not filter by it", async () => {
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    await screen.findByText("deployed acme/production/api");
    fireEvent.change(screen.getByLabelText("Range"), { target: { value: "custom" } });
    const from = screen.getByLabelText("From");
    fireEvent.change(from, { target: { value: "11/23/2026" } });
    expect(screen.queryByText("Use a date like 23 Nov 2026")).toBeNull();
    expect(from.getAttribute("aria-invalid")).toBe("false");
    fireEvent.blur(from);
    const message = screen.getByText("Use a date like 23 Nov 2026");
    expect(from.getAttribute("aria-invalid")).toBe("true");
    expect(from.getAttribute("aria-describedby")).toBe(message.id);
    fireEvent.change(from, { target: { value: "23 Nov 2026" } });
    expect(screen.queryByText("Use a date like 23 Nov 2026")).toBeNull();
    expect(from.getAttribute("aria-describedby")).toBeNull();
    fireEvent.change(from, { target: { value: "11/23/2026" } });
    fireEvent.blur(from);
    expect(calls.some((c) => c.url.includes("from=") && c.url.includes("11"))).toBe(false);
    expect((screen.getByRole("link", { name: "Export CSV" }) as HTMLAnchorElement).getAttribute("href")).toBe("/api/audit.csv");
  });

  it("builds the query from who, text and days", () => {
    expect(auditQuery("owner", "", "", "")).toBe("actor=owner");
    expect(auditQuery("", "", "31 Feb 2026", "nope")).toBe("");
    expect(auditQuery("", "x", "1 Mar 2026", "1 Mar 2026")).toBe(`q=x&from=${Date.UTC(2026, 1, 28, 19)}&to=${Date.UTC(2026, 2, 1, 19)}`);
  });

  it("says events in plain words, makes unknown keys readable and keeps the raw key as the title", async () => {
    fakeApi({ "GET /api/audit": { rows: [
      { at: 1, actor: "owner", text: "terminal.opened", kind: "terminal.opened" },
      { at: 2, actor: "owner", text: "domain.state_changed", kind: "domain.state_changed" },
      { at: 3, actor: "owner", text: "set variable API_KEY on acme/production/api", kind: "audit" },
    ], more: false } });
    render(<Audit />);
    const known = await screen.findByText("opened a terminal session");
    expect(known.getAttribute("title")).toBe("terminal.opened");
    expect(screen.getByText("domain state changed").getAttribute("title")).toBe("domain.state_changed");
    expect(screen.queryByText("domain.state_changed")).toBeNull();
    expect(screen.getByText("set variable API_KEY on acme/production/api")).toBeTruthy();
  });
});
