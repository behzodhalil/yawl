import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { Watcher } from "./Watcher";

const linked = { "GET /api/settings/telegram": { state: "linked", bot: "acme_bot" } };

describe("Watcher", () => {
  it("asks for Telegram first", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "none" }, "GET /api/settings/watch": { state: "off", servers: [], checks: 0 } });
    render(<Watcher />);
    expect(await screen.findByText("Link Telegram first: the watcher sends its alerts there.")).toBeTruthy();
    expect(screen.queryByLabelText("Cloudflare API token")).toBeNull();
  });

  it("takes a shared bot chat in place of the own bot, not one Telegram stopped", async () => {
    const chat = (gone_at: number | null) => ({ id: "7", type: "private", title: "B", linked_at: 1, gone_at, routes: { servers: [], projects: [] } });
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: { available: true, chats: [chat(5)] } }, "GET /api/settings/watch": { state: "off", servers: [], checks: 0 } });
    const { unmount } = render(<Watcher />);
    expect(await screen.findByText("Link Telegram first: the watcher sends its alerts there.")).toBeTruthy();
    unmount();
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: { available: true, chats: [chat(null)] } }, "GET /api/settings/watch": { state: "off", servers: [], checks: 0 } });
    render(<Watcher />);
    expect(await screen.findByLabelText("Cloudflare API token")).toBeTruthy();
  });

  it("names both permissions and shows a failed step", async () => {
    const calls = fakeApi({
      ...linked,
      "GET /api/settings/watch": { state: "off", servers: [], checks: 0 },
      "PUT /api/settings/watch": fail(400, "Could not upload the watcher: Authentication error"),
    });
    render(<Watcher />);
    await screen.findByLabelText("Cloudflare API token");
    expect(screen.getByText(/Workers Scripts: Edit/)).toBeTruthy();
    expect(screen.getByText(/D1: Edit/)).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Cloudflare API token"), { target: { value: "tok" } });
    fireEvent.change(screen.getByLabelText("Account ID"), { target: { value: "acc1" } });
    fireEvent.click(screen.getByRole("button", { name: "Deploy the watcher" }));
    expect(await screen.findByText("Could not upload the watcher: Authentication error")).toBeTruthy();
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ token: "tok", account: "acc1" });
  });

  it("shows what it watches, the status page and heartbeats", async () => {
    const url = "https://yawl-watch-abcdefgh.acme.workers.dev";
    let hbs = [{ id: "hb_1", name: "laptop backup", period_s: 86400, grace_s: 3600, url: url + "/hb/aa" }];
    const calls = fakeApi({
      ...linked,
      "GET /api/settings/watch": { state: "on", url, checks: 6, last_run: Date.now() - 12_000, servers: [{ name: "hel1", state: "watched" }, { name: "hk1", state: "not watched" }] },
      "GET /api/heartbeats": () => hbs,
      "POST /api/heartbeats": (b: unknown) => ((hbs = [...hbs, { id: "hb_2", name: (b as { name: string }).name, period_s: 3600, grace_s: 600, url: url + "/hb/bb" }]), hbs[1]),
    });
    render(<Watcher />);
    expect(await screen.findByText("Watching from Cloudflare: 6 checks, last run 12 s ago")).toBeTruthy();
    expect((screen.getByRole("link", { name: url + "/status" }) as HTMLAnchorElement).href).toBe(url + "/status");
    expect(screen.getByText("hk1 is not watched yet: its agent has not taken the watcher's key.")).toBeTruthy();
    await screen.findByText("laptop backup");
    expect(screen.getByText(url + "/hb/aa")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Heartbeat name"), { target: { value: "nightly export" } });
    fireEvent.change(screen.getByLabelText("Every (minutes)"), { target: { value: "60" } });
    fireEvent.change(screen.getByLabelText("Grace (minutes)"), { target: { value: "10" } });
    fireEvent.click(screen.getByRole("button", { name: "Add heartbeat" }));
    await screen.findByText("nightly export");
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ name: "nightly export", period_s: 3600, grace_s: 600 });
  });

  it("shows a deploy error from the state in red", async () => {
    fakeApi({ ...linked, "GET /api/settings/watch": { state: "error", error: "Could not check the token: Invalid API Token", account: "acc1", servers: [], checks: 0 } });
    render(<Watcher />);
    const el = await screen.findByText("Could not check the token: Invalid API Token");
    expect(el.closest(".callout")?.className).toContain("bad");
  });

  it("asks before it stops using the watcher", async () => {
    const calls = fakeApi({ ...linked, "GET /api/settings/watch": { state: "on", url: "https://w.acme.workers.dev", checks: 1, servers: [] }, "GET /api/heartbeats": [], "DELETE /api/settings/watch": {} });
    render(<Watcher />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Stop using the watcher" }));
    expect(await screen.findByRole("heading", { name: "Stop using the watcher?" })).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Stop using the watcher" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, stop" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/watch", body: undefined }]));
  });

  it("asks before it deletes a heartbeat", async () => {
    const calls = fakeApi({ ...linked, "GET /api/settings/watch": { state: "on", url: "https://w.acme.workers.dev", checks: 1, servers: [] }, "GET /api/heartbeats": [{ id: "hb_1", name: "laptop backup", period_s: 86400, grace_s: 3600, url: "https://w/hb/aa" }], "DELETE /api/heartbeats/hb_1": null });
    render(<Watcher />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Delete laptop backup" }));
    expect(await screen.findByText('Delete the heartbeat "laptop backup"?')).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Delete laptop backup" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, delete" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/heartbeats/hb_1", body: undefined }]));
  });
});
