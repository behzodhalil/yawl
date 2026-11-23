import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { fail, fakeApi } from "../../test-api";
import { Sources } from "./Sources";

const E = "/api/environments/acme/production";

describe("Sources", () => {
  it("lists the managers and adds Doppler with its token sealed here", async () => {
    await sodium.ready;
    const pk = () => sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({
      [`GET ${E}/sources`]: [{ provider: "vault", url: "https://vault.acme", path: "secret/data/shop", updated_at: 1 }],
      [`GET ${E}/keys`]: { agent_pubkey: pk(), recovery_pubkey: pk(), server: "hel1" },
      [`PUT ${E}/sources/doppler`]: null,
    });
    render(<Sources project="acme" env="production" />);
    await screen.findByText("secret/data/shop");
    fireEvent.change(screen.getByLabelText("Manager"), { target: { value: "doppler" } });
    fireEvent.change(screen.getByLabelText("Token"), { target: { value: "dp.st.prod.secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Use it" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const body = calls.find((c) => c.method === "PUT")!.body as Record<string, string>;
    expect(body.agent_box && body.recovery_box).toBeTruthy();
    expect(JSON.stringify(body)).not.toContain("dp.st.prod.secret");
  });

  it("sends Infisical's folder path", async () => {
    await sodium.ready;
    const pk = () => sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({
      [`GET ${E}/sources`]: [],
      [`GET ${E}/keys`]: { agent_pubkey: pk(), recovery_pubkey: pk(), server: "hel1" },
      [`PUT ${E}/sources/infisical`]: null,
    });
    render(<Sources project="acme" env="production" />);
    fireEvent.change(await screen.findByLabelText("Manager"), { target: { value: "infisical" } });
    fireEvent.change(screen.getByLabelText("Path"), { target: { value: "/api" } });
    fireEvent.change(screen.getByLabelText("Token"), { target: { value: "cid:csecret" } });
    fireEvent.click(screen.getByRole("button", { name: "Use it" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect((calls.find((c) => c.method === "PUT")!.body as Record<string, string>).path).toBe("/api");
  });

  it("asks before removing a manager, and shows a failure inside", async () => {
    const calls = fakeApi({ [`GET ${E}/sources`]: [{ provider: "vault", path: "p", updated_at: 1 }], [`DELETE ${E}/sources/vault`]: () => fail(500, "nope") });
    render(<Sources project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: "Remove" }));
    expect(screen.getByRole("dialog").textContent).toContain("Every service in this environment loses its variables at the next start.");
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, stop using it" }));
    expect(await within(screen.getByRole("dialog")).findByText(/nope/)).toBeTruthy();
  });
});
