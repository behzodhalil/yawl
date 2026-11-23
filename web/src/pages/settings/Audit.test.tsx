import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Audit, auditQuery } from "./Audit";

process.env.TZ = "Asia/Tashkent";

const rows = [
  { at: Date.UTC(2026, 9, 28, 12, 5), actor: "API token ci", text: "set variable API_KEY on acme/production/api", kind: "audit" },
  { at: Date.UTC(2026, 9, 28, 12, 1), actor: "owner", text: "deployed acme/production/api", kind: "audit" },
];

describe("Audit", () => {
  it("lists newest first, filters by who, and exports what it shows", async () => {
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    const first = await screen.findByText("set variable API_KEY on acme/production/api");
    expect(first.compareDocumentPosition(screen.getByText("deployed acme/production/api")) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Who"), { target: { value: "owner" } });
    await waitFor(() => expect(calls.some((c) => c.url.includes("actor=owner"))).toBe(true));
    expect((screen.getByRole("link", { name: "Export CSV" }) as HTMLAnchorElement).getAttribute("href")).toBe("/api/audit.csv?actor=owner");
  });

  it("writes each time with its year in the browser's zone", async () => {
    fakeApi({ "GET /api/audit": { rows: [{ at: Date.UTC(2025, 11, 30, 12, 2), actor: "owner", text: "last year", kind: "audit" }], more: false } });
    render(<Audit />);
    expect((await screen.findByText("last year")).closest("tr")!.textContent).toContain("30 Dec 2025 17:02");
  });

  it("filters by days written either way, through the end of the last day", async () => {
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    await screen.findByText("deployed acme/production/api");
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "23 Nov 2026" } });
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-11-24" } });
    const from = Date.UTC(2026, 10, 22, 19);
    const to = Date.UTC(2026, 10, 24, 19);
    await waitFor(() => expect(calls.some((c) => c.url.includes(`from=${from}&to=${to}`))).toBe(true));
    expect(screen.queryByText("Use a date like 23 Nov 2026")).toBeNull();
  });

  it("says how to write a day it cannot read and does not filter by it", async () => {
    const calls = fakeApi({ "GET /api/audit": { rows, more: false } });
    render(<Audit />);
    await screen.findByText("deployed acme/production/api");
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "11/23/2026" } });
    expect(screen.getByText("Use a date like 23 Nov 2026")).toBeTruthy();
    expect(screen.getByLabelText("From").getAttribute("aria-invalid")).toBe("true");
    expect(calls.some((c) => c.url.includes("from="))).toBe(false);
    expect((screen.getByRole("link", { name: "Export CSV" }) as HTMLAnchorElement).getAttribute("href")).toBe("/api/audit.csv");
  });

  it("builds the query from who, text and days", () => {
    expect(auditQuery("owner", "", "", "")).toBe("actor=owner");
    expect(auditQuery("", "", "31 Feb 2026", "nope")).toBe("");
    expect(auditQuery("", "x", "1 Mar 2026", "1 Mar 2026")).toBe(`q=x&from=${Date.UTC(2026, 1, 28, 19)}&to=${Date.UTC(2026, 2, 1, 19)}`);
  });

  it("says events in plain words and keeps the raw key as the title", async () => {
    fakeApi({ "GET /api/audit": { rows: [
      { at: 1, actor: "owner", text: "terminal.opened", kind: "terminal.opened" },
      { at: 2, actor: "owner", text: "something.new", kind: "something.new" },
    ], more: false } });
    render(<Audit />);
    const known = await screen.findByText("opened a terminal session");
    expect(known.getAttribute("title")).toBe("terminal.opened");
    expect(screen.getByText("something.new").getAttribute("title")).toBe("something.new");
  });
});
