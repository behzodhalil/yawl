import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Group } from "./Group";

const now = Date.now();

describe("AI agents", () => {
  it("states the MCP tools yawl mcp really serves", async () => {
    fakeApi({ "GET /api/audit": { rows: [], more: false }, "GET /api/members": [] });
    render(<Group group="integrations" section="agents" />);
    expect(screen.getByRole("heading", { name: "AI agents", level: 1 })).toBeTruthy();
    expect(within(screen.getByRole("list", { name: "Reads" })).getAllByRole("listitem").map((li) => li.textContent))
      .toEqual(["list_services", "service", "deploys", "deploy_log", "logs", "incidents"]);
    expect(within(screen.getByRole("list", { name: "Changes" })).getAllByRole("listitem").map((li) => li.textContent)).toEqual(["deploy", "rollback"]);
    expect(await screen.findByText("No API token has changed anything yet.")).toBeTruthy();
    expect(screen.getByText(`${window.location.origin}/llms.txt`)).toBeTruthy();
  });
  it("names where each agent's skill is written", async () => {
    fakeApi({ "GET /api/audit": { rows: [], more: false }, "GET /api/members": [] });
    render(<Group group="integrations" section="agents" />);
    expect(screen.getByText(".claude/skills/yawl/SKILL.md")).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "Cursor" }));
    expect(await screen.findByText(".cursor/rules/yawl.mdc")).toBeTruthy();
    expect((await screen.findAllByText((_, el) => el?.textContent === "yawl skills add cursor")).length).toBeGreaterThan(0);
  });
  it("lists only what API tokens did, with the token's member and role", async () => {
    const calls = fakeApi({
      "GET /api/audit": { more: false, rows: [
        { at: now - 60_000, actor: "API token laptop (dev@acme.uz)", text: "deploy.queued", kind: "deploy.queued" },
        { at: now - 120_000, actor: "owner@acme.uz", text: "spec.applied", kind: "spec.applied" },
        { at: now - 180_000, actor: "API token ci", text: "rolled back web", kind: "deploy.rollback" },
      ] },
      "GET /api/members": [{ email: "dev@acme.uz", role: "developer" }],
    });
    render(<Group group="integrations" section="agents" />);
    const table = (await screen.findByText("started a deployment")).closest("table")!;
    const rows = within(table).getAllByRole("row").slice(1).map((r) => [...r.querySelectorAll("td")].slice(1).map((td) => td.textContent));
    expect(rows).toEqual([["laptop · dev@acme.uz · Developer", "started a deployment"], ["ci", "rolled back web"]]);
    expect(screen.getByRole("link", { name: "All in Activity" }).getAttribute("href")).toBe("/activity/audit");
    expect(calls.some((c) => c.url === "/api/audit?page=0&tokens=1")).toBe(true);
  });
});
