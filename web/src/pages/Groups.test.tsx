import { describe, expect, it } from "vitest";
import { render, screen, within } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Shell } from "../Shell";
import { Group } from "./Group";
import { ServerTabs } from "./ServerTabs";

describe("menus", () => {
  it("the side nav names each part, and Account is in the top bar", () => {
    render(<Shell crumb={["x"]} active="/team"><p>body</p></Shell>);
    const side = within(screen.getByRole("navigation", { name: "Main" }));
    expect(side.getAllByRole("link").map((a) => a.textContent)).toEqual(
      ["Projects", "Servers", "Shared variables", "Incidents", "Status pages", "Integrations", "Notifications", "Team", "Activity", "Settings"]);
    expect(side.getByRole("link", { name: "Team" }).className).toBe("on");
    expect(screen.getByRole("link", { name: "Account" }).getAttribute("href")).toBe("/account");
  });
  it("a group shows only its own tabs", () => {
    fakeApi({ "GET /api/members": [], "GET /api/settings/sso": { name: "", issuer: "", client_id: "", only: false } });
    render(<Group group="team" section="sso" />);
    expect(screen.getByRole("heading", { name: "Team" })).toBeTruthy();
    const tabs = screen.getAllByRole("link").filter((a) => a.closest(".tabs"));
    expect(tabs.map((a) => a.textContent)).toEqual(["Members", "Sign-in", "API tokens"]);
    expect(tabs[1].className).toBe("on");
  });
  it("Settings keeps only the panel's own", () => {
    fakeApi({ "GET /api/settings/panel": { domain: "", hosts: [] } });
    render(<Group group="settings" section="" />);
    const tabs = screen.getAllByRole("link").filter((a) => a.closest(".tabs"));
    expect(tabs.map((a) => a.textContent)).toEqual(["Panel", "Backup storage", "Updates", "Recovery key", "Appearance"]);
  });
  it("Integrations is one list that says where each stands, and opens each", async () => {
    fakeApi({ "GET /api/github": { connected: true, slug: "yawl-ab12" }, "GET /api/settings/gitlab": [{ host: "gitlab.com", webhook_url: "" }], "GET /api/settings/gitea": [],
      "GET /api/registries": [{}, {}], "GET /api/settings/builds": { server: "", repository: "" } });
    render(<Group group="integrations" section="" />);
    expect(await screen.findByText("Connected: gitlab.com")).toBeTruthy();
    expect(await screen.findByText("Connected: yawl-ab12")).toBeTruthy();
    expect(screen.getByText("Not connected")).toBeTruthy();
    expect(screen.getByText("2 logins")).toBeTruthy();
    expect(screen.getByText("Each server builds its own")).toBeTruthy();
    expect(screen.queryByRole("link", { name: "GitHub" })).toBeNull();
    expect(screen.getAllByRole("link").filter((a) => a.closest(".ilist")).map((a) => a.getAttribute("href"))).toEqual(
      ["/integrations/github", "/integrations/gitlab", "/integrations/gitea", "/integrations/registries", "/integrations/builds"]);
  });
  it("Notifications is one list of channels, each saying if it is set up", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "linked", bot: "acme_alerts_bot" }, "GET /api/settings/watch": { state: "off", checks: 0, servers: [] },
      "GET /api/settings/notifications": { slack: { set: true, host: "hooks.slack.com" }, discord: { set: false, host: "" }, email: { set: false, to: "" } } });
    render(<Group group="notifications" section="" />);
    expect(await screen.findByText("Connected")).toBeTruthy();
    expect(screen.getByText("Alerts in a Telegram chat or group.")).toBeTruthy();
    expect(screen.getByText("Posts to hooks.slack.com")).toBeTruthy();
    expect(screen.getAllByText("Not set up").length).toBe(4);
    expect(screen.getByText("Off")).toBeTruthy();
    expect(screen.getAllByRole("link").filter((a) => a.closest(".ilist")).map((a) => a.getAttribute("href"))).toEqual(
      ["telegram", "slack", "discord", "email", "webhook", "ntfy", "watcher"].map((k) => "/notifications/" + k));
  });
  it("Telegram's line counts the shared bot's chats, and says when one stopped", async () => {
    const chat = (gone_at: number | null) => ({ id: "7", type: "private", title: "B", linked_at: 1, gone_at, routes: { servers: [], projects: [] } });
    let chats = [chat(null), chat(null)];
    fakeApi({ "GET /api/settings/telegram": () => ({ state: "none", relay: { available: true, chats } }), "GET /api/settings/watch": { state: "off" },
      "GET /api/settings/notifications": { slack: { set: false, host: "" }, discord: { set: false, host: "" }, email: { set: false, to: "" } } });
    const { unmount } = render(<Group group="notifications" section="" />);
    expect(await screen.findByText("Connected, 2 chats")).toBeTruthy();
    expect(screen.getAllByRole("link", { name: "Manage" }).map((a) => a.getAttribute("href"))).toEqual(["/notifications/telegram"]);
    unmount();
    chats = [chat(null), chat(5)];
    render(<Group group="notifications" section="" />);
    expect(await screen.findByText("1 chat stopped")).toBeTruthy();
    chats = [];
  });
  it("Telegram is not set up with neither way", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: { available: true, chats: [] } }, "GET /api/settings/watch": { state: "off" },
      "GET /api/settings/notifications": { slack: { set: false, host: "" }, discord: { set: false, host: "" }, email: { set: false, to: "" } } });
    render(<Group group="notifications" section="" />);
    await screen.findAllByText("Not set up");
    expect(screen.getAllByText("Not set up").length).toBe(6);
    expect(screen.getAllByRole("link", { name: "Set up" })[0].getAttribute("href")).toBe("/notifications/telegram");
  });
  it("a channel's page shows only that channel", async () => {
    fakeApi({ "GET /api/settings/notifications": { slack: { set: false, host: "" }, discord: { set: false, host: "" }, email: { set: false, to: "" } } });
    render(<Group group="notifications" section="discord" />);
    expect(await screen.findByLabelText("Discord webhook URL")).toBeTruthy();
    expect(screen.queryByLabelText("Slack webhook URL")).toBeNull();
  });
  it("a server's pages are its tabs", () => {
    render(<ServerTabs name="hel1" active="os" />);
    const tabs = screen.getAllByRole("link");
    expect(tabs.map((a) => [a.textContent, a.getAttribute("href")])).toEqual([
      ["Overview", "/servers/hel1"], ["Firewall", "/servers/hel1/firewall"], ["Disk", "/servers/hel1/disk"],
      ["OS updates", "/servers/hel1/os"], ["Log drain", "/servers/hel1/drain"], ["Tailscale", "/servers/hel1/tailscale"]]);
    expect(tabs[3].className).toBe("on");
  });
});
