import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { fakeApi } from "../../test-api";
import { SharedVars } from "./SharedVars";

describe("SharedVars", () => {
  it("lists them, says where a team one is missing, and sets one sealed to every server", async () => {
    await sodium.ready;
    const pk = () => sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({
      "GET /api/shared-env": [{ scope: "team", key: "SENTRY_DSN", servers: ["hel1", "fra1"], missing: ["hk1"], stale: ["fra1"], updated_at: Date.now() }],
      "GET /api/shared-env/keys": { servers: [{ name: "hel1", agent_pubkey: pk() }, { name: "hk1", agent_pubkey: pk() }], recovery_pubkey: pk() },
      "PUT /api/shared-env/team/SMTP_PASSWORD": { ok: true },
    });
    render(<SharedVars />);
    await screen.findByText("SENTRY_DSN");
    expect(screen.getByText("Not on hk1: set it again to reach it.")).toBeTruthy();
    expect(screen.getByText("fra1 joined again since: set it again, or its deploys stop.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "SMTP_PASSWORD" } });
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "hunter2-secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Set" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const put = calls.find((c) => c.method === "PUT")!;
    const body = put.body as { boxes: Record<string, string>; recovery_box: string };
    expect(Object.keys(body.boxes).sort()).toEqual(["hel1", "hk1"]);
    expect(body.recovery_box).toBeTruthy();
    expect(Object.keys((put.body as { keys: Record<string, string> }).keys).sort()).toEqual(["hel1", "hk1"]);
    expect(JSON.stringify(body)).not.toContain("hunter2-secret");
  });

  it("sets a server one sealed to that server only", async () => {
    await sodium.ready;
    const pk = () => sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({
      "GET /api/shared-env": [],
      "GET /api/shared-env/keys": { servers: [{ name: "hel1", agent_pubkey: pk() }, { name: "hk1", agent_pubkey: pk() }], recovery_pubkey: pk() },
      "PUT /api/shared-env/servers/hk1/SMTP_PASSWORD": { ok: true },
    });
    render(<SharedVars />);
    await screen.findByRole("option", { name: "Server hk1" });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "SMTP_PASSWORD" } });
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "hunter2-secret" } });
    fireEvent.change(screen.getByLabelText("Where"), { target: { value: "hk1" } });
    fireEvent.click(screen.getByRole("button", { name: "Set" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    const put = calls.find((c) => c.method === "PUT")!;
    expect(put.url).toBe("/api/shared-env/servers/hk1/SMTP_PASSWORD");
    expect(Object.keys((put.body as { keys: Record<string, string> }).keys)).toEqual(["hk1"]);
  });

  it("asks before it deletes one", async () => {
    const calls = fakeApi({
      "GET /api/shared-env": [{ scope: "team", key: "SENTRY_DSN", servers: ["hel1"], updated_at: Date.now() }],
      "GET /api/shared-env/keys": { servers: [], recovery_pubkey: "" },
      "DELETE /api/shared-env/team/SENTRY_DSN": { ok: true },
    });
    render(<SharedVars />);
    await screen.findByText("SENTRY_DSN");
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Delete SENTRY_DSN" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });
});
