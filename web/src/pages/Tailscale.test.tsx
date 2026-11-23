import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { fail, fakeApi } from "../test-api";
import { POLL_MS } from "../ui";
import { JOIN_LIMIT, Tailscale } from "./Tailscale";

const S = "/api/servers/hel1/tailscale";

describe("Tailscale", () => {
  it("shows the address, joins with the key sealed here, and leaves", async () => {
    await sodium.ready;
    const pk = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    let put = false;
    const calls = fakeApi({
      [`GET ${S}`]: () => (put ? { running: true, address: "100.64.1.2", agent_pubkey: pk } : { running: false, agent_pubkey: pk }),
      [`PUT ${S}`]: () => { put = true; return null; },
      [`DELETE ${S}`]: null,
    });
    render(<Tailscale name="hel1" />);
    fireEvent.change(await screen.findByLabelText("Auth key"), { target: { value: "tskey-auth-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Join the tailnet" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const body = calls.find((c) => c.method === "PUT")!.body as { auth_key_box: string };
    expect(body.auth_key_box).toBeTruthy();
    expect(JSON.stringify(body)).not.toContain("tskey-auth-secret");
    await screen.findByText("On the tailnet at 100.64.1.2.");
    fireEvent.click(screen.getByRole("button", { name: "Leave the tailnet" }));
    await screen.findByText("Leave the tailnet?");
    expect(screen.getByText("The panel and SSH may stop being reachable over Tailscale.")).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, leave" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("keeps the typed auth key when the join fails, and disables Join while it runs", async () => {
    await sodium.ready;
    const pk = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    let release!: () => void;
    fakeApi({
      [`GET ${S}`]: { running: false, agent_pubkey: pk },
      [`PUT ${S}`]: () => new Promise<Response>((r) => { release = () => r(fail(500, "no route")); }),
    });
    render(<Tailscale name="hel1" />);
    const key = (await screen.findByLabelText("Auth key")) as HTMLInputElement;
    fireEvent.change(key, { target: { value: "tskey-auth-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Join the tailnet" }));
    await waitFor(() => expect((screen.getByRole("button", { name: /Joining|Join the tailnet/ }) as HTMLButtonElement).disabled).toBe(true));
    expect(key.value).toBe("tskey-auth-secret");
    release();
    await screen.findByText(/no route/);
    expect(key.value).toBe("tskey-auth-secret");
  });

  it("no auth key form while on the tailnet", async () => {
    fakeApi({ [`GET ${S}`]: { running: true, address: "100.64.0.2", agent_pubkey: "x" } });
    const { unmount } = render(<Tailscale name="hel1" />);
    await screen.findByText("On the tailnet at 100.64.0.2.");
    expect(screen.queryByLabelText("Auth key")).toBeNull();
    expect(screen.queryByRole("button", { name: "Join the tailnet" })).toBeNull();
    unmount();
    fakeApi({ [`GET ${S}`]: { running: false, outside: true, address: "100.64.0.3", agent_pubkey: "x" } });
    render(<Tailscale name="hel1" />);
    await screen.findByText(/installed outside Yawl/);
    expect(screen.queryByLabelText("Auth key")).toBeNull();
    expect(screen.queryByRole("button", { name: "Join the tailnet" })).toBeNull();
  });

  it("no auth key form while the status loads or when it cannot load", async () => {
    fakeApi({ [`GET ${S}`]: () => new Promise<Response>(() => {}) });
    const { unmount } = render(<Tailscale name="hel1" />);
    await screen.findByRole("status", { name: "Loading" });
    expect(screen.queryByLabelText("Auth key")).toBeNull();
    unmount();
    fakeApi({ [`GET ${S}`]: () => fail(500, "x") });
    render(<Tailscale name="hel1" />);
    await screen.findByText("x");
    expect(screen.queryByLabelText("Auth key")).toBeNull();
  });

  describe("while joining", () => {
    afterEach(() => vi.useRealTimers());
    it("a join that takes too long stops polling and says so", async () => {
      await sodium.ready;
      const pk = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval", "setTimeout", "clearTimeout"] });
      const calls = fakeApi({ [`GET ${S}`]: { running: false, agent_pubkey: pk }, [`PUT ${S}`]: null });
      const gets = () => calls.filter((c) => c.method === "GET").length;
      render(<Tailscale name="hel1" />);
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      fireEvent.change(screen.getByLabelText("Auth key"), { target: { value: "k" } });
      fireEvent.click(screen.getByRole("button", { name: "Join the tailnet" }));
      await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS); });
      const n = gets();
      await act(async () => { await vi.advanceTimersByTimeAsync(JOIN_LIMIT); });
      expect(gets()).toBeGreaterThan(n);
      screen.getByText("Joining is taking longer than expected.");
      const stopped = gets();
      await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS * 3); });
      expect(gets()).toBe(stopped);
      fireEvent.click(screen.getByRole("button", { name: "Check again" }));
      await act(async () => { await vi.advanceTimersByTimeAsync(0); });
      expect(gets()).toBe(stopped + 1);
      expect(screen.queryByText("Joining is taking longer than expected.")).toBeNull();
      await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS); });
      expect(gets()).toBe(stopped + 2);
    });
    it("asks again after POLL_MS", async () => {
      vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
      const calls = fakeApi({ [`GET ${S}`]: { running: true, agent_pubkey: "x" } });
      render(<Tailscale name="hel1" />);
      await screen.findByText("Joining the tailnet.");
      const n = calls.filter((c) => c.method === "GET").length;
      await act(async () => { vi.advanceTimersByTime(5000); });
      expect(calls.filter((c) => c.method === "GET").length).toBe(n + 1);
    });
  });
});
