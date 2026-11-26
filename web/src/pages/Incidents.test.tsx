import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Incidents, groupOffline, summarize } from "./Incidents";

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
    const open = await screen.findByText("acme/production/api is down: GET https://api.acme.uz answered 502 twice.");
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

  it("shows the summary with the full text behind a disclosure", async () => {
    fakeApi({ "GET /api/incidents": [list[0]] });
    render(<Incidents />);
    fireEvent.click(await screen.findByRole("button", { name: /Show the full log/ }));
    expect(await screen.findByText("acme/production/api is down: GET https://api.acme.uz answered 502 twice. Since 14:02.")).toBeTruthy();
  });

  it("links a missed backup without storage to the storage settings", async () => {
    const cause = "acme-db: no backup has succeeded in 26 hours (last: never). Backup storage is not set: run yawl server storage set.";
    fakeApi({ "GET /api/incidents": [{ ...list[0], id: "inc_bak", subject: "backup:svc_2", kind: "backup.missed", label: "acme/production/acme-db", cause }] });
    render(<Incidents />);
    const link = await screen.findByRole("link", { name: "Set up storage" });
    expect(link.getAttribute("href")).toBe("/settings/storage");
    expect(link.parentElement!.textContent).toBe("acme-db: no backup has succeeded in 26 hours (last: never). Backup storage is not set. Set up storage");
    expect(screen.queryByText(/yawl server storage set/)).toBeNull();
    expect(screen.queryByText("Show the full log")).toBeNull();
  });

  it("groups open incidents on an offline server under one card", async () => {
    const svc = (id: string, label: string) => ({ ...list[0], id, subject: `service:${id}`, label });
    fakeApi({
      "GET /api/incidents": [svc("a", "apayment/dev/frontend"), svc("b", "apayment/dev/api"), svc("c", "acme/production/api")],
      "GET /api/projects": [
        { name: "apayment", envs: [{ name: "dev", server: "azure-1", server_offline: true, services: [] }] },
        { name: "acme", envs: [{ name: "production", server: "hel1", services: [] }] },
      ],
    });
    render(<Incidents />);
    expect(await screen.findByText("azure-1 is offline")).toBeTruthy();
    expect(screen.getAllByText(/^apayment\/dev\/(frontend|api)/).length).toBe(2);
    expect((screen.getByRole("link", { name: "Open azure-1" }) as HTMLAnchorElement).getAttribute("href")).toBe("/servers/azure-1");
    expect(screen.getByText("acme/production/api")).toBeTruthy();
  });
});

describe("summarize", () => {
  it("takes the failing step and the Error line of a build failure", () => {
    const cause = "stockplus-web deploy failed: build failed: failed to solve: process \"/bin/sh -c npm run build\" did not complete successfully: exit code: 1\nError: Failed to collect configuration for /_not-found\nNothing was live before this deploy.";
    expect(summarize(cause)).toBe("stockplus-web deploy failed at build: Error: Failed to collect configuration for /_not-found");
  });

  it("takes the Caused by line and drops exit code 0", () => {
    const cause = "tafakkur-api deploy failed: Caused by: java.lang.IllegalStateException: Required environment variable JWT_SECRET is not set (exit code 0). Nothing was live before this deploy.";
    const out = summarize(cause);
    expect(out.startsWith("tafakkur-api deploy failed: Caused by: java.lang.IllegalStateException")).toBe(true);
    expect(out).not.toContain("exit code 0");
  });

  it("falls back to the first sentence", () => {
    expect(summarize("apayment/dev/frontend is down: GET https://frontend-dev.20-2-211-187.sslip.io/login did not answer within 10 s twice. Since 04:54.")).toBe("apayment/dev/frontend is down: GET https://frontend-dev.20-2-211-187.sslip.io/login did not answer within 10 s twice.");
  });
});

describe("groupOffline", () => {
  it("leaves server incidents and online services alone", () => {
    const inc = (subject: string, label: string) => ({ ...list[0], subject, label });
    const open = [inc("server:s", "hel1"), inc("service:x", "acme/production/api")];
    const { groups, rest } = groupOffline(open, [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }] }]);
    expect(groups).toEqual([]);
    expect(rest).toEqual(open);
  });
});
