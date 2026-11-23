import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Audit } from "./Audit";

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

  it("says the year of a time from another year", async () => {
    const y = new Date().getUTCFullYear();
    fakeApi({ "GET /api/audit": { rows: [
      { at: Date.UTC(y, 0, 2, 9, 30), actor: "owner", text: "this year", kind: "audit" },
      { at: Date.UTC(y - 1, 11, 30, 12, 2), actor: "owner", text: "last year", kind: "audit" },
    ], more: false } });
    render(<Audit />);
    expect((await screen.findByText("last year")).closest("tr")!.textContent).toContain(`30 Dec ${y - 1} 12:02 UTC`);
    expect(screen.getByText("this year").closest("tr")!.textContent).toContain("2 Jan 09:30 UTC");
  });

  it("says events in plain words and keeps the raw key as the title", async () => {
    fakeApi({ "GET /api/audit": { rows: [
      { at: 1, actor: "owner", text: "terminal.opened", kind: "terminal.opened" },
      { at: 2, actor: "owner", text: "something.new", kind: "something.new" },
    ], more: false } });
    render(<Audit />);
    const known = await screen.findByText("opened a terminal in a service");
    expect(known.getAttribute("title")).toBe("terminal.opened");
    expect(screen.getByText("something.new").getAttribute("title")).toBe("something.new");
  });
});
