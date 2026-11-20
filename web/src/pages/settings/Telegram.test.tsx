import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, act, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { Telegram } from "./Telegram";

afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

const pending = { state: "pending", bot: "acme_bot", code: "yawl-123456" };

describe("Telegram, own bot", () => {
  it("shows the three steps, then the code, polls until linked, then offers a test and disconnect", async () => {
    let n = 0;
    const calls = fakeApi({
      "GET /api/settings/telegram": () => (n++ < 2 ? (n === 1 ? { state: "none" } : pending) : { state: "linked", bot: "acme_bot" }),
      "POST /api/settings/telegram": pending,
    });
    render(<Telegram />);
    expect(await screen.findByText("Alerts from this panel, in a Telegram chat or group.")).toBeTruthy();
    expect(screen.getByText("Create a bot")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open @BotFather" }).getAttribute("href")).toBe("https://t.me/BotFather");
    expect(screen.getByRole("link", { name: "Open @BotFather" }).getAttribute("rel")).toBe("noopener");
    const link = screen.getByRole("button", { name: "Link" }) as HTMLButtonElement;
    expect(link.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Paste its token"), { target: { value: "123:ABC" } });
    vi.useFakeTimers();
    fireEvent.click(link);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ token: "123:ABC" });
    expect(screen.getByText("yawl-123456")).toBeTruthy();
    expect(screen.getByText("Send this code to @acme_bot.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open @acme_bot" }).getAttribute("href")).toBe("https://t.me/acme_bot");
    expect(screen.getByText("This page notices within a few seconds. The code works for 10 minutes.")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByText("Connected")).toBeTruthy();
    expect(screen.getByText("Personal chat")).toBeTruthy();
    expect(screen.getByText("Sent by @acme_bot, your own bot.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Send test" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Disconnect" })).toBeTruthy();
    expect(calls.filter((c) => c.method === "GET").length).toBeGreaterThanOrEqual(3);
  });

  it("copies the code and says so", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    fakeApi({ "GET /api/settings/telegram": pending });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
    expect(writeText).toHaveBeenCalledWith("yawl-123456");
  });

  it("cancels a first link by clearing the pending code", async () => {
    const calls = fakeApi({ "GET /api/settings/telegram": pending, "DELETE /api/settings/telegram": null });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await screen.findByText("Create a bot")).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE")).toBe(true);
  });

  it("changes the token without dropping the current link", async () => {
    const calls = fakeApi({
      "GET /api/settings/telegram": { state: "linked", bot: "old_bot" },
      "POST /api/settings/telegram": { state: "pending", bot: "new_bot", code: "yawl-000111" },
    });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Change token" }));
    fireEvent.change(screen.getByLabelText("Paste its token"), { target: { value: "9:NEW" } });
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    expect(await screen.findByText("Send this code to @new_bot.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await screen.findByText("Sent by @old_bot, your own bot.")).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
  });

  it("shows the API's errors", async () => {
    fakeApi({
      "GET /api/settings/telegram": { state: "none" },
      "POST /api/settings/telegram": fail(400, "Telegram does not know this bot token."),
    });
    render(<Telegram />);
    fireEvent.change(await screen.findByLabelText("Paste its token"), { target: { value: "bad" } });
    fireEvent.click(screen.getByRole("button", { name: "Link" }));
    expect((await screen.findByRole("alert")).textContent).toBe("Telegram does not know this bot token.");
  });

  it("sends a test, and shows when it fails", async () => {
    let ok = true;
    fakeApi({
      "GET /api/settings/telegram": { state: "linked", bot: "acme_bot" },
      "POST /api/settings/telegram/test": () => (ok ? null : fail(502, "Telegram said: chat not found")),
    });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Send test" }));
    expect(await screen.findByText("Sent. Check Telegram.")).toBeTruthy();
    ok = false;
    fireEvent.click(screen.getByRole("button", { name: "Send test" }));
    expect((await screen.findByRole("alert")).textContent).toBe("Telegram said: chat not found");
    expect(screen.queryByText("Sent. Check Telegram.")).toBeNull();
  });

  it("disconnects", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "linked", bot: "acme_bot" }, "DELETE /api/settings/telegram": null });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, disconnect" }));
    expect(await screen.findByText("Create a bot")).toBeTruthy();
  });

  it("does not name a bot it does not know (linked from the CLI)", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "linked" } });
    render(<Telegram />);
    expect(await screen.findByText("Sent by your own bot.")).toBeTruthy();
    expect(screen.queryByText(/@\./)).toBeNull();
  });
  it("names the linked chat: a group by its title, with when it was linked", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "linked", bot: "acme_bot", chat_type: "supergroup", chat_name: "Acme ops", linked_at: String(Date.now() - 3600_000) } });
    render(<Telegram />);
    expect(await screen.findByText("Acme ops")).toBeTruthy();
    expect(screen.getByText(/group · linked/)).toBeTruthy();
  });
});

const relay = (r: object = {}) => ({ available: true, bot: "useyawl_bot", chats: [], ...r });
const pendingRelay = { code: "AbCdEfGh12345678", link: "https://t.me/useyawl_bot?start=AbCdEfGh12345678", expires_at: Date.now() + 600_000, qr: "<svg></svg>", bot: "useyawl_bot" };
const me = { id: "7", type: "private", title: "Behzod Halil", linked_at: Date.UTC(2026, 10, 5, 12), gone_at: null };
const ops = { id: "-100", type: "supergroup", title: "Acme ops", linked_at: Date.UTC(2026, 10, 5, 12), gone_at: null };

describe("Telegram, shared bot", () => {
  it("leads with Connect Telegram, opens the link in a new tab, shows the QR code and waits until a chat is linked", async () => {
    const tab = { opener: {} as unknown, location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    let polls = 0;
    const calls = fakeApi({
      "GET /api/settings/telegram": () => (polls++ === 0 ? { state: "none", relay: relay() } : polls < 3 ? { state: "none", relay: relay({ pending: pendingRelay }) } : { state: "none", relay: relay({ chats: [me] }) }),
      "POST /api/settings/telegram/connect": pendingRelay,
    });
    render(<Telegram />);
    const connect = await screen.findByRole("button", { name: "Connect Telegram" });
    expect(screen.getByRole("button", { name: "Use your own bot" })).toBeTruthy();
    expect(screen.queryByText("Create a bot")).toBeNull();
    vi.useFakeTimers();
    fireEvent.click(connect);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/settings/telegram/connect")).toBe(true);
    expect(tab.location.href).toBe(pendingRelay.link);
    expect(tab.opener).toBeNull();
    expect(screen.getByText("Waiting")).toBeTruthy();
    expect(screen.getByText(/in the chat with @useyawl_bot that just opened/)).toBeTruthy();
    expect(screen.getByRole("link", { name: "Open Telegram again" }).getAttribute("href")).toBe(pendingRelay.link);
    expect(screen.getByAltText("QR code of the Telegram link").getAttribute("src")).toBe("data:image/svg+xml;utf8," + encodeURIComponent("<svg></svg>"));
    expect(screen.getByText("The link works for 10 minutes.")).toBeTruthy();
    expect(screen.queryByText(/For a group/)).toBeNull();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByText("Connected")).toBeTruthy();
    expect(screen.getByText("Behzod Halil")).toBeTruthy();
    expect(screen.getByText("Personal chat · Nov 5")).toBeTruthy();
    expect(screen.getByText("Sent by @useyawl_bot")).toBeTruthy();
  });

  it("closes the tab it opened when the relay refuses", async () => {
    const tab = { opener: {}, location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay() }, "POST /api/settings/telegram/connect": fail(503, "The Telegram relay does not answer. Use your own bot.") });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Connect Telegram" }));
    expect((await screen.findByRole("alert")).textContent).toBe("The Telegram relay does not answer. Use your own bot.");
    expect(tab.close).toHaveBeenCalled();
  });

  it("lists each chat with its test and disconnect; a chat Telegram stopped has disconnect only", async () => {
    const gone = { ...ops, id: "-200", title: "Old group", gone_at: Date.now() };
    const calls = fakeApi({
      "GET /api/settings/telegram": { state: "none", relay: relay({ chats: [me, ops, gone] }) },
      "POST /api/settings/telegram/chats/-100/test": null,
      "DELETE /api/settings/telegram/chats/7": null,
    });
    render(<Telegram />);
    expect(await screen.findByText("Acme ops")).toBeTruthy();
    expect(screen.getByText("Group · Nov 5")).toBeTruthy();
    expect(screen.getByText("Telegram stopped this chat: the bot was blocked or removed.")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Send test" }).length).toBe(2);
    expect(screen.getAllByRole("button", { name: "Disconnect" }).length).toBe(3);
    fireEvent.click(screen.getAllByRole("button", { name: "Send test" })[1]);
    expect(await screen.findByText("Sent. Check Telegram.")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/settings/telegram/chats/-100/test")).toBe(true);
    fireEvent.click(screen.getAllByRole("button", { name: "Disconnect" })[0]);
    fireEvent.click(await screen.findByRole("button", { name: "Yes, disconnect" }));
    await act(async () => {});
    expect(screen.queryByText("Behzod Halil")).toBeNull();
    expect(screen.getByRole("button", { name: "Use your own bot instead" })).toBeTruthy();
  });

  it("adds a group through ?startgroup: the wait says to pick the group and keeps the command to copy", async () => {
    const tab = { opener: {}, location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    const groupLink = { ...pendingRelay, link: "https://t.me/useyawl_bot?startgroup=AbCdEfGh12345678" };
    const calls = fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ chats: [me] }) }, "POST /api/settings/telegram/connect": groupLink });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Add a group" }));
    expect(await screen.findByText("Pick the group in Telegram. @useyawl_bot joins it and links it.")).toBeTruthy();
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ group: true });
    expect(tab.location.href).toBe(groupLink.link);
    expect(screen.getByText("If @useyawl_bot is in the group already, send this there:")).toBeTruthy();
    expect(screen.getByText("/start@useyawl_bot AbCdEfGh12345678")).toBeTruthy();
  });

  it("adds a person's chat through ?start, with no group command", async () => {
    vi.spyOn(window, "open").mockReturnValue(null);
    const calls = fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ chats: [ops] }) }, "POST /api/settings/telegram/connect": pendingRelay });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Add another chat" }));
    expect(await screen.findByText(/in the chat with @useyawl_bot that just opened/)).toBeTruthy();
    expect(calls.find((c) => c.method === "POST")!.body).toBeUndefined();
    expect(screen.queryByText(/send this there/)).toBeNull();
  });

  it("stops waiting when a chat linked before used the code, and says so", async () => {
    let polls = 0;
    fakeApi({ "GET /api/settings/telegram": () => (polls++ === 0 ? { state: "none", relay: relay({ chats: [me], pending: pendingRelay }) }
      : { state: "none", relay: relay({ chats: [me], ended: { why: "already", title: "Behzod Halil" } }) }) });
    vi.useFakeTimers();
    render(<Telegram />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("Waiting")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.getByText("Behzod Halil was already connected.")).toBeTruthy();
    expect(screen.queryByText("Waiting")).toBeNull();
  });

  it("stops waiting when the link expires, and tries again the same way", async () => {
    const groupLink = { ...pendingRelay, link: "https://t.me/useyawl_bot?startgroup=AbCdEfGh12345678" };
    let polls = 0;
    vi.spyOn(window, "open").mockReturnValue(null);
    const calls = fakeApi({
      "GET /api/settings/telegram": () => (polls++ === 0 ? { state: "none", relay: relay({ chats: [me], pending: groupLink }) } : { state: "none", relay: relay({ chats: [me], ended: { why: "expired" } }) }),
      "POST /api/settings/telegram/connect": groupLink,
    });
    vi.useFakeTimers();
    render(<Telegram />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("Waiting")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    vi.useRealTimers();
    expect(screen.getByText("The link expired before a chat used it.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Pick the group in Telegram. @useyawl_bot joins it and links it.")).toBeTruthy();
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ group: true });
  });

  it("says what each chat gets, and chooses servers and projects in its row", async () => {
    const routed = { ...ops, routes: { servers: ["srv_1", "srv_2"], projects: ["acme"] } };
    const calls = fakeApi({
      "GET /api/settings/telegram": { state: "none", relay: relay({ chats: [{ ...me, routes: { servers: [], projects: [] } }, routed] }) },
      "GET /api/servers": [{ id: "srv_1", name: "hel1" }, { id: "srv_2", name: "fsn1" }],
      "GET /api/projects": [{ name: "acme" }, { name: "blog" }],
      "PUT /api/settings/telegram/chats/7/routes": null,
    });
    render(<Telegram />);
    expect(await screen.findByText("hel1, fsn1 · acme")).toBeTruthy();
    expect(screen.getByText("All alerts")).toBeTruthy();
    const choose = screen.getAllByRole("button", { name: "Choose" })[0];
    fireEvent.click(choose);
    expect(choose.getAttribute("aria-expanded")).toBe("true");
    const editor = screen.getByRole("group", { name: "What Behzod Halil gets" });
    expect(screen.getByText(/messages from the watcher, go to every chat/)).toBeTruthy();
    expect(document.activeElement).toBe(screen.getByRole("checkbox", { name: "hel1" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "fsn1" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "blog" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("fsn1 · blog")).toBeTruthy();
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ servers: ["srv_2"], projects: ["blog"] });
    expect(editor.isConnected).toBe(false);
    fireEvent.click(screen.getAllByRole("button", { name: "Choose" })[1]);
    fireEvent.keyDown(screen.getByRole("checkbox", { name: "hel1" }), { key: "Escape" });
    expect(screen.queryByRole("group")).toBeNull();
    expect(calls.filter((c) => c.method === "PUT").length).toBe(1);
  });

  it("shows why a route was refused", async () => {
    fakeApi({
      "GET /api/settings/telegram": { state: "none", relay: relay({ chats: [me] }) },
      "GET /api/servers": [{ id: "srv_1", name: "hel1" }], "GET /api/projects": [],
      "PUT /api/settings/telegram/chats/7/routes": fail(400, 'There is no server "srv_1".'),
    });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Choose" }));
    expect(await screen.findByText("No projects yet.")).toBeTruthy();
    fireEvent.click(await screen.findByRole("checkbox", { name: "hel1" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect((await screen.findByRole("alert")).textContent).toBe('There is no server "srv_1".');
  });

  it("allows no 11th chat", async () => {
    const ten = Array.from({ length: 10 }, (_, i) => ({ ...ops, id: String(-i - 1), title: "g" + i }));
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ chats: ten }) } });
    render(<Telegram />);
    expect(((await screen.findByRole("button", { name: "Add a group" })) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Add another chat" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("cancels the wait", async () => {
    const calls = fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ pending: pendingRelay }) }, "DELETE /api/settings/telegram/connect": null });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }));
    expect(await screen.findByRole("button", { name: "Connect Telegram" })).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/settings/telegram/connect")).toBe(true);
  });

  it("opens the own bot's steps below, and comes back", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay() } });
    render(<Telegram />);
    fireEvent.click(await screen.findByRole("button", { name: "Use your own bot" }));
    expect(screen.getByText("Create a bot")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Use @useyawl_bot instead" }));
    expect(screen.getByRole("button", { name: "Connect Telegram" })).toBeTruthy();
  });

  it("opens on the own bot when it is linked", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "linked", bot: "acme_bot", relay: relay() } });
    render(<Telegram />);
    expect(await screen.findByText("Sent by @acme_bot, your own bot.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Use @useyawl_bot instead" })).toBeTruthy();
  });

  it("is the own bot alone, exactly as before, when the relay does not answer", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ available: false }) } });
    render(<Telegram />);
    expect(await screen.findByText("Create a bot")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Connect Telegram" })).toBeNull();
    expect(screen.queryByText(/instead/)).toBeNull();
  });

  it("still lists linked chats while the relay is away, without Add", async () => {
    fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ available: false, chats: [me] }) } });
    render(<Telegram />);
    expect(await screen.findByText("Behzod Halil")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Add a group" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Add another chat" })).toBeNull();
  });

  it("asks before it disconnects a chat", async () => {
    const calls = fakeApi({ "GET /api/settings/telegram": { state: "none", relay: relay({ chats: [me] }) }, "DELETE /api/settings/telegram/chats/7": null });
    render(<Telegram />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    expect(await screen.findByText("Disconnect Telegram?")).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, disconnect" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/telegram/chats/7", body: undefined }]));
  });

  it("asks before it disconnects the own bot", async () => {
    const calls = fakeApi({ "GET /api/settings/telegram": { state: "linked", bot: "acme_bot" }, "DELETE /api/settings/telegram": null });
    render(<Telegram />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Disconnect" }));
    expect(await screen.findByText("Disconnect Telegram?")).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, disconnect" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/telegram", body: undefined }]));
  });
});
