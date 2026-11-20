import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { fail, fakeApi } from "../test-api";
import { Drain } from "./Drain";

const P = "/api/servers/hel1/drain";

describe("Log drain (design record 60)", () => {
  it("sends the logs to Loki with the token sealed here, and stops", async () => {
    await sodium.ready;
    const pk = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    let set = false;
    const calls = fakeApi({
      [`GET ${P}`]: () => ({ set, url: set ? "https://logs.example.com" : "", user: set ? "1234" : "", agent_pubkey: pk }),
      [`PUT ${P}`]: () => { set = true; return null; },
      [`DELETE ${P}`]: () => { set = false; return null; },
    });
    render(<Drain name="hel1" />);
    fireEvent.change(await screen.findByLabelText("Loki address"), { target: { value: "https://logs.example.com" } });
    fireEvent.change(screen.getByLabelText("User"), { target: { value: "1234" } });
    fireEvent.change(screen.getByLabelText("Token"), { target: { value: "glc_secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Send logs to Loki" }));
    await screen.findByText("Every app's logs on hel1 go to https://logs.example.com.");
    const body = calls.find((c) => c.method === "PUT")!.body as Record<string, string>;
    expect(body.url).toBe("https://logs.example.com");
    expect(body.token_box).toBeTruthy();
    expect(JSON.stringify(body)).not.toContain("glc_secret");
    fireEvent.click(screen.getByRole("button", { name: "Stop sending" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("keeps the token in the field when the save fails", async () => {
    await sodium.ready;
    const pk = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    fakeApi({ [`GET ${P}`]: { set: false, url: "", user: "", agent_pubkey: pk }, [`PUT ${P}`]: fail(500, "loki said no") });
    render(<Drain name="hel1" />);
    fireEvent.change(await screen.findByLabelText("Loki address"), { target: { value: "https://logs.example.com" } });
    const tok = screen.getByLabelText("Token") as HTMLInputElement;
    fireEvent.change(tok, { target: { value: "glc_secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Send logs to Loki" }));
    await screen.findByText(/loki said no/);
    expect(tok.value).toBe("glc_secret");
  });

  it("two quick clicks on Send logs and on Stop sending each make one call, disabled meanwhile", async () => {
    await sodium.ready;
    const pk = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    let set = false;
    let release!: () => void;
    const hold = () => new Promise((r) => { release = () => r(null); });
    const calls = fakeApi({
      [`GET ${P}`]: () => ({ set, url: "https://logs.example.com", user: "", agent_pubkey: pk }),
      [`PUT ${P}`]: async () => { await hold(); set = true; return null; },
      [`DELETE ${P}`]: async () => { await hold(); set = false; return null; },
    });
    render(<Drain name="hel1" />);
    fireEvent.change(await screen.findByLabelText("Loki address"), { target: { value: "https://logs.example.com" } });
    const send = screen.getByRole("button", { name: "Send logs to Loki" }) as HTMLButtonElement;
    fireEvent.click(send);
    fireEvent.click(send);
    await waitFor(() => expect(send.disabled).toBe(true));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
    release();
    const stop = (await screen.findByRole("button", { name: "Stop sending" })) as HTMLButtonElement;
    fireEvent.click(stop);
    fireEvent.click(stop);
    await waitFor(() => expect(stop.disabled).toBe(true));
    await waitFor(() => expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1));
    release();
    await screen.findByRole("button", { name: "Send logs to Loki" });
  });
});
