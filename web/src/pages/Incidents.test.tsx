import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Incidents } from "./Incidents";

const now = Date.now();
const list = [
  { id: "inc_open", subject: "service:svc_1", kind: "url", label: "acme/production/api", source: "watch", opened_at: now - 600_000, closed_at: null, cause: "acme/production/api is down: GET https://api.acme.uz answered 502 twice. Since 14:02.", note: "" },
  { id: "inc_old", subject: "server:srv_1", kind: "server", label: "hel1", source: "watch", opened_at: now - 86_400_000, closed_at: now - 86_400_000 + 420_000, cause: "hel1 is not answering.", note: "" },
];

describe("Incidents", () => {
  it("puts open ones first and saves a note", async () => {
    const calls = fakeApi({ "GET /api/incidents": list, "POST /api/incidents/inc_open/note": null });
    render(<Incidents />);
    expect(await screen.findByText("Opened by the watcher when a check fails. Status pages have their own public incidents.")).toBeTruthy();
    const open = await screen.findByText("acme/production/api is down: GET https://api.acme.uz answered 502 twice. Since 14:02.");
    const old = screen.getByText("hel1 is not answering.");
    expect(open.compareDocumentPosition(old) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.getByText("lasted 7 minutes")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Note for acme/production/api"), { target: { value: "Rolling back" } });
    fireEvent.click(screen.getByRole("button", { name: "Save note" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ text: "Rolling back" });
  });

  it("points to Settings without a watcher", async () => {
    fakeApi({ "GET /api/incidents": { off: true } });
    render(<Incidents />);
    expect(await screen.findByText("Incidents come from the watcher, and it is off.")).toBeTruthy();
    expect((screen.getByRole("link", { name: "Set up the watcher" }) as HTMLAnchorElement).getAttribute("href")).toBe("/notifications/watcher");
  });

  it("searches and pages past incidents, open ones stay", async () => {
    const past = Array.from({ length: 25 }, (_, i) => ({ id: `inc_${i}`, subject: "server:s", kind: "server", label: `srv${String(i).padStart(2, "0")}`, source: "watch", opened_at: now - 86_400_000, closed_at: now - 86_400_000 + 60_000, cause: i === 4 ? "disk full" : "not answering", note: i === 9 ? "rebooted" : "" }));
    fakeApi({ "GET /api/incidents": [list[0], ...past] });
    render(<Incidents />);
    await screen.findByText("25 incidents");
    expect(screen.getAllByText("not answering").length + screen.getAllByText("disk full").length + screen.getAllByText("rebooted").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/^srv\d\d$/).length).toBe(20);
    fireEvent.click(screen.getByRole("button", { name: /Next/ }));
    expect(screen.getAllByText(/^srv\d\d$/).length).toBe(5);
    fireEvent.change(screen.getByLabelText("Search incidents"), { target: { value: "disk" } });
    expect(screen.getByText("srv04")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Search incidents"), { target: { value: "reboot" } });
    expect(screen.getByText("srv09")).toBeTruthy();
    expect(screen.getByText("acme/production/api")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Search incidents"), { target: { value: "zzz" } });
    expect(screen.getByText("Nothing matches the search.")).toBeTruthy();
    expect(screen.getByText("acme/production/api")).toBeTruthy();
  });
});
