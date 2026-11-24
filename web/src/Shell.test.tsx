import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { Shell } from "./Shell";
import { Modal } from "./ui";

describe("Shell", () => {
  it("counts open incidents and flags offline servers in the sidebar", async () => {
    const inc = (id: string, closed_at: number | null) => ({ id, subject: "s", kind: "k", label: id, source: "x", opened_at: 1, closed_at, cause: "", note: "" });
    fakeApi({
      "GET /api/incidents": [inc("a", null), inc("b", null), inc("c", 5)],
      "GET /api/servers": [{ id: "1", offline: true }, { id: "2", offline: true }, { id: "3", offline: false }],
    });
    const { container } = render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    expect((await screen.findAllByText("2"))[0].className).toBe("cnt");
    expect(container.querySelector(".side .sq")?.getAttribute("title")).toBe("2 servers offline");
    expect(container.querySelector(".side .sq")?.textContent).toBe("2 servers offline");
  });

  it("shows no badge when the watcher is off or nothing is wrong", async () => {
    fakeApi({ "GET /api/incidents": { off: true }, "GET /api/servers": [{ id: "1", offline: false }] });
    const { container } = render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    await screen.findByText("Incidents", { selector: ".side a" });
    expect(container.querySelector(".cnt, .sq")).toBeNull();
  });

  it("keeps Search as a button on phones: only its words hide", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const b = screen.getByRole("button", { name: "Search" });
    expect(b.classList.contains("hide")).toBe(false);
    expect(b.querySelector("span.hide")?.textContent).toContain("Search");
  });

  it("ignores Cmd-K while a modal is open", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/"><Modal title="Token" onClose={() => {}}><button>ok</button></Modal></Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByRole("dialog", { name: /search/i })).toBeNull();
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
  });

  it("Cmd-K opens the palette and Cmd-K again closes it", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.getByRole("dialog", { name: "Go to" })).toBeTruthy();
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull();
  });

  it("Cmd-K does nothing while another dialog is open", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/"><Modal title="Other" onClose={() => {}}><button>ok</button></Modal></Shell>);
    fireEvent.keyDown(window, { key: "k", metaKey: true });
    expect(screen.queryByRole("dialog", { name: "Go to" })).toBeNull();
  });

  it("groups the sidebar under Resources, Monitoring and Workspace", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const side = screen.getByRole("navigation", { name: "Main" });
    const groups = within(side).getAllByRole("group");
    expect(groups.map((g) => g.getAttribute("aria-label"))).toEqual(["Resources", "Monitoring", "Workspace"]);
    expect(groups.map((g) => within(g).getAllByRole("link").map((a) => a.textContent))).toEqual([
      ["Projects", "Servers", "Shared variables"],
      ["Incidents", "Status pages"],
      ["Integrations", "Notifications", "Team", "Activity", "Settings"],
    ]);
  });

  it("keeps the incident badge inside the Monitoring group and the offline square inside Resources", async () => {
    fakeApi({
      "GET /api/incidents": [{ id: "a", subject: "s", kind: "k", label: "a", source: "x", opened_at: 1, closed_at: null, cause: "", note: "" }],
      "GET /api/servers": [{ id: "1", offline: true }],
    });
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const side = screen.getByRole("navigation", { name: "Main" });
    await within(within(side).getByRole("group", { name: "Monitoring" })).findByText("1");
    await within(within(side).getByRole("group", { name: "Resources" })).findByText("1 server offline");
  });
});
