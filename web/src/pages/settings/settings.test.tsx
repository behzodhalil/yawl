import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { Storage } from "./Storage";
import { Tokens } from "./Tokens";
import { Recovery } from "./Recovery";
import { Account } from "./Account";
import { GitHub } from "./GitHub";

afterEach(() => vi.useRealTimers());

describe("Storage", () => {
  it("never fills the secret", async () => {
    fakeApi({ "GET /api/settings/storage": { set: true, endpoint: "s3.example.com", bucket: "acme", region: "", access_key_end: "1234" } });
    render(<Storage />);
    await screen.findByDisplayValue("acme");
    expect((screen.getByLabelText("Bucket") as HTMLInputElement).value).toBe("acme");
    expect((screen.getByLabelText("Secret key") as HTMLInputElement).value).toBe("");
    expect(screen.getByText("The access key ends in 1234.")).toBeTruthy();
  });
});

describe("Tokens", () => {
  it("shows a new token once", async () => {
    let list = [{ id: "tok_1", name: "laptop", created_at: Date.now() }];
    fakeApi({ "GET /api/tokens": () => list, "POST /api/tokens": () => ((list = [...list, { id: "tok_2", name: "ci", created_at: Date.now() }]), { token: "yawl_SECRETTOKEN" }) });
    render(<Tokens />);
    await screen.findByText("laptop");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "ci" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    expect(await screen.findByText("yawl_SECRETTOKEN")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    await screen.findByText("ci");
    expect(screen.queryByText("yawl_SECRETTOKEN")).toBeNull();
  });
});

describe("Tokens, whose", () => {
  it("names each token's member when the server says it", async () => {
    fakeApi({ "GET /api/tokens": [{ id: "tok_1", name: "laptop", created_at: Date.now(), member: "dev@acme.uz" }, { id: "tok_2", name: "ci", created_at: Date.now(), member: "" }] });
    render(<Tokens />);
    expect((await screen.findByText("laptop")).closest("tr")!.textContent).toContain("dev@acme.uz");
    expect(screen.getByText("ci").closest("td")!.textContent).toBe("ci");
  });
});

describe("Recovery", () => {
  it("set: the fingerprint and no way to make another", async () => {
    fakeApi({ "GET /api/recovery-key": { set: true, fingerprint: "q83vEjRW" } });
    render(<Recovery />);
    await screen.findByText("q83vEjRW");
    expect(screen.queryByRole("button", { name: "Make the recovery key" })).toBeNull();
  });
  it("not set: the one-time flow", async () => {
    fakeApi({ "GET /api/recovery-key": { set: false }, "PUT /api/recovery-key": null });
    render(<Recovery />);
    fireEvent.click(await screen.findByRole("button", { name: "Make the recovery key" }));
    await screen.findByText("I saved the recovery key somewhere other than this server");
  });
});

describe("Account", () => {
  it("refuses a mismatch locally and signs out", async () => {
    const calls = fakeApi({ "GET /api/me": { email: "me@acme.uz", role: "admin" }, "DELETE /api/session": null });
    render(<Account />);
    await screen.findByText("me@acme.uz");
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "correct horse battery" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "another long password" } });
    fireEvent.change(screen.getByLabelText("New password again"), { target: { value: "another long passwor" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(screen.getByText("The two new passwords differ.")).toBeTruthy();
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
    await waitFor(() => expect(location.pathname).toBe("/sign-in"));
    expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/session")).toBe(true);
  });
});

describe("GitHub", () => {
  it("opens the tab in the click, then points it at the returned URL (Safari)", async () => {
    const tab = { opener: {}, location: { href: "" }, close: vi.fn() };
    const open = vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    let answer!: (v: unknown) => void;
    fakeApi({ "POST /api/github/connect": () => new Promise((r) => (answer = r)) });
    render(<GitHub />);
    fireEvent.click(screen.getByRole("button", { name: "Connect GitHub" }));
    expect(open).toHaveBeenCalledWith("about:blank", "_blank");
    expect(tab.opener).toBeNull();
    await waitFor(() => expect(answer).toBeTypeOf("function"));
    answer({ url: "https://panel.acme.uz/github/setup?state=abc" });
    await waitFor(() => expect(tab.location.href).toBe("https://panel.acme.uz/github/setup?state=abc"));
    expect(tab.close).not.toHaveBeenCalled();
  });
  it("shows the server's refusal and closes the blank tab", async () => {
    const tab = { opener: {}, location: { href: "" }, close: vi.fn() };
    vi.spyOn(window, "open").mockReturnValue(tab as unknown as Window);
    fakeApi({ "POST /api/github/connect": fail(409, "Set --public-url to an https address GitHub can reach first.") });
    render(<GitHub />);
    fireEvent.click(screen.getByRole("button", { name: "Connect GitHub" }));
    await screen.findByText("Set --public-url to an https address GitHub can reach first.");
    expect(tab.close).toHaveBeenCalled();
  });
});

describe("GitHub, commit statuses", () => {
  it("says when GitHub refused them", async () => {
    fakeApi({ "GET /api/settings/commit-status": { github: "acme/api: github: 422", gitlab: "" } });
    render(<GitHub />);
    expect(await screen.findByText(/Commit statuses were refused/)).toBeTruthy();
  });
});
