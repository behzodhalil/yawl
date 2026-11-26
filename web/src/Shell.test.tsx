import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { Shell } from "./Shell";
import { SessionContext } from "./session";
import { Icon, Modal } from "./ui";

describe("Shell", () => {
  it("counts open incidents and flags offline servers in the sidebar", async () => {
    const inc = (id: string, closed_at: number | null) => ({ id, subject: "s", kind: "k", label: id, source: "x", opened_at: 1, closed_at, cause: "", note: "" });
    fakeApi({
      "GET /api/incidents": [inc("a", null), inc("b", null), inc("c", 5)],
      "GET /api/servers": [{ id: "1", offline: true }, { id: "2", offline: true }, { id: "3", offline: false }],
    });
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const side = screen.getByRole("navigation", { name: "Main" });
    expect(await within(within(side).getByRole("link", { name: /Incidents/ })).findByText("2")).toBeTruthy();
    const offline = await within(side).findByTitle("2 servers offline");
    expect(offline.textContent).toBe("2 servers offline");
  });

  it("shows no badge when the watcher is off or nothing is wrong", async () => {
    fakeApi({ "GET /api/incidents": { off: true }, "GET /api/servers": [{ id: "1", offline: false }] });
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const side = screen.getByRole("navigation", { name: "Main" });
    await waitFor(() => expect(within(side).getByRole("link", { name: "Incidents" })).toBeTruthy());
    expect(within(side).queryByTitle(/offline/)).toBeNull();
  });

  it("opens search from the sidebar, and from the top bar on phones", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const [side, top] = screen.getAllByRole("button", { name: "Search" });
    expect(side.textContent).toContain("Search…");
    expect(top.className).toContain("md:hidden");
    fireEvent.click(side);
    expect(screen.getByRole("dialog", { name: "Go to" })).toBeTruthy();
  });

  it("draws sidebar icons light, and the current page's filled", () => {
    fakeApi({});
    render(<Shell crumb={["Servers"]} active="/servers">x</Shell>);
    const side = within(screen.getByRole("navigation", { name: "Main" }));
    const drawn = (name: string, weight: "light" | "fill") => render(<Icon name={name} weight={weight} />).container.querySelector("svg")!.innerHTML;
    expect(side.getByRole("link", { name: /Servers/ }).querySelector("svg")!.innerHTML).toBe(drawn("server", "fill"));
    expect(side.getByRole("link", { name: "Projects" }).querySelector("svg")!.innerHTML).toBe(drawn("folders", "light"));
  });

  it("keeps the sidebar collapsed across visits", () => {
    fakeApi({});
    const { unmount } = render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    fireEvent.click(within(screen.getByRole("complementary")).getByRole("button", { name: "Collapse sidebar" }));
    expect(localStorage.getItem("yawl.sidebar")).toBe("closed");
    unmount();
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    expect(within(screen.getByRole("complementary")).getByRole("button", { name: "Expand sidebar" })).toBeTruthy();
    localStorage.clear();
  });

  it("shows who is signed in and the panel version", async () => {
    fakeApi({ "GET /api/update": { current: "0.1.18" } });
    render(<SessionContext.Provider value={{ claimed: true, email: "me@acme.uz" }}><Shell crumb={["Projects"]} active="/account">x</Shell></SessionContext.Provider>);
    expect(await screen.findByText("v0.1.18")).toBeTruthy();
    const me = screen.getByRole("link", { name: "Account" });
    expect(me.textContent).toContain("me@acme.uz");
    expect(me.getAttribute("aria-current")).toBe("page");
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

  it("groups the sidebar under Hosting, Monitoring and Panel", () => {
    fakeApi({});
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const side = screen.getByRole("navigation", { name: "Main" });
    const groups = within(side).getAllByRole("group");
    expect(groups.map((g) => g.getAttribute("aria-label"))).toEqual(["Hosting", "Monitoring", "Panel"]);
    expect(groups.map((g) => within(g).getAllByRole("link").map((a) => a.textContent))).toEqual([
      ["Projects", "Servers", "Shared variables"],
      ["Incidents", "Status pages"],
      ["Integrations", "Notifications", "Team", "Activity", "Settings"],
    ]);
  });

  it("keeps the incident badge inside the Monitoring group and the offline square inside Hosting", async () => {
    fakeApi({
      "GET /api/incidents": [{ id: "a", subject: "s", kind: "k", label: "a", source: "x", opened_at: 1, closed_at: null, cause: "", note: "" }],
      "GET /api/servers": [{ id: "1", offline: true }],
    });
    render(<Shell crumb={["Projects"]} active="/">x</Shell>);
    const side = screen.getByRole("navigation", { name: "Main" });
    await within(within(side).getByRole("group", { name: "Monitoring" })).findByText("1");
    await within(within(side).getByRole("group", { name: "Hosting" })).findByText("1 server offline");
  });
});
