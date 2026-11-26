import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { chooseOption, fakeApi, fail } from "../test-api";
import * as rs from "../reseal";
import { newRecoveryKey } from "../seal";
import { Move } from "./Move";

const servers = [
  { id: "srv_1", name: "hel1", public_ip: "1.2.3.4", agent_version: "", disk_used_pct: 0, offline: false },
  { id: "srv_2", name: "hk1", public_ip: "5.6.7.8", agent_version: "", disk_used_pct: 0, offline: false },
];
const tree = [{ name: "acme", envs: [{ name: "staging", server: "hel1", services: [] }] }];
const plan = { lines: ["move staging from hel1 to hk1", "build and start api on hk1"], to_pubkey: "T", recovery_pubkey: "R", restore: true, vars: [] };
const view = (state: string, cause = "") => ({ id: "mv_1", from: "hel1", to: "hk1", step: "restore", state, cause, cleaned: false, lines: plan.lines });

describe("Move", () => {
  afterEach(() => vi.restoreAllMocks());

  it("shows the plan for the picked server", async () => {
    fakeApi({ "GET /api/servers": servers, "GET /api/projects": tree, "GET /api/environments/acme/staging/moves/latest": fail(404, "no move"),
      "GET /api/environments/acme/staging/move?to=hk1": plan });
    render(<Move project="acme" env="staging" />);
    await screen.findByRole("combobox", { name: "Move to" });
    await chooseOption("Move to", "hk1");
    fireEvent.click(screen.getByRole("combobox", { name: "Move to" }));
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["hk1"]);
    const items = await within(screen.getByRole("main")).findAllByRole("listitem");
    expect(items.map((i) => i.textContent)).toEqual(plan.lines);
  });

  it("shows a refusal", async () => {
    fakeApi({ "GET /api/servers": servers, "GET /api/projects": tree, "GET /api/environments/acme/staging/moves/latest": fail(404, "no move"),
      "GET /api/environments/acme/staging/move?to=hk1": fail(409, "hk1 is offline; a move needs it online.") });
    render(<Move project="acme" env="staging" />);
    await screen.findByRole("combobox", { name: "Move to" });
    await chooseOption("Move to", "hk1");
    await screen.findByText("hk1 is offline; a move needs it online.");
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: "yawl-recovery-X" } });
    expect((screen.getByRole("button", { name: "Move staging to hk1" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("moves, follows progress, forgets the key, and retries", async () => {
    vi.spyOn(rs, "reseal").mockResolvedValue({ boxes: { var_a: { agent_box: "B", updated_at: 1 } }, recovery_box: "RB" });
    await sodium.ready;
    const rec = await newRecoveryKey();
    const target = sodium.crypto_box_keypair();
    let n = 0;
    const calls = fakeApi({
      "GET /api/servers": servers, "GET /api/projects": tree,
      "GET /api/environments/acme/staging/moves/latest": () => [fail(404, "no move"), view("failed", "restore staging-db: disk full"), view("running")][Math.min(n++, 2)],
      "GET /api/environments/acme/staging/move?to=hk1": plan,
      "POST /api/environments/acme/staging/move": { move: "mv_1" },
      "GET /api/environments/acme/staging/keys": { agent_pubkey: sodium.to_base64(target.publicKey, sodium.base64_variants.ORIGINAL), recovery_pubkey: rec.pub, server: "hk1" },
      "POST /api/moves/mv_1/retry": null,
    });
    render(<Move project="acme" env="staging" />);
    await screen.findByRole("combobox", { name: "Move to" });
    await chooseOption("Move to", "hk1");
    await within(screen.getByRole("main")).findAllByRole("listitem");
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: rec.text } });
    fireEvent.click(screen.getByRole("button", { name: "Move staging to hk1" }));
    await screen.findByText("restore staging-db: disk full");
    const post = calls.find((c) => c.method === "POST")!;
    expect(post.body).toEqual({ to: "hk1", boxes: { var_a: { agent_box: "B", updated_at: 1 } }, recovery_box: "RB" });
    expect((screen.getByLabelText("Recovery key") as HTMLTextAreaElement).value).toBe("");
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: rec.text } });
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    await waitFor(() => expect(calls.some((c) => c.url === "/api/moves/mv_1/retry")).toBe(true));
    const retry = calls.find((c) => c.url === "/api/moves/mv_1/retry")!.body as { recovery_box: string };
    const opened = sodium.crypto_box_seal_open(sodium.from_base64(retry.recovery_box, sodium.base64_variants.ORIGINAL), target.publicKey, target.privateKey);
    expect(new TextDecoder().decode(opened)).toBe(rec.text);
    await screen.findByText("Restore the databases (now)");
  });

  it("offers a new move after one that was undone", async () => {
    fakeApi({ "GET /api/servers": servers, "GET /api/projects": tree,
      "GET /api/environments/acme/staging/moves/latest": { ...view("failed", "back up staging-db: bucket key expired"), step: "backup" } });
    render(<Move project="acme" env="staging" />);
    expect(await screen.findByLabelText("Move to")).toBeTruthy();
    expect(screen.getByText("The last move was undone: back up staging-db: bucket key expired.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });
  it("says the old server is not cleaned yet", async () => {
    const moved = [{ name: "acme", envs: [{ name: "staging", server: "hk1", services: [] }] }];
    fakeApi({ "GET /api/servers": servers, "GET /api/projects": moved,
      "GET /api/environments/acme/staging/moves/latest": { ...view("done"), step: "clean" } });
    render(<Move project="acme" env="staging" />);
    await screen.findByText("hel1 is cleaned up once it is back online.");
    expect(screen.getByLabelText("Move to")).toBeTruthy();
  });
  it("says why the old server could not be cleaned", async () => {
    const moved = [{ name: "acme", envs: [{ name: "staging", server: "hk1", services: [] }] }];
    fakeApi({ "GET /api/servers": servers, "GET /api/projects": moved,
      "GET /api/environments/acme/staging/moves/latest": { ...view("done", "could not remove acme-staging-api-1 from hel1: permission denied"), step: "clean" } });
    render(<Move project="acme" env="staging" />);
    await screen.findByText("could not remove acme-staging-api-1 from hel1: permission denied");
  });
  it("says when there is no other server", async () => {
    fakeApi({ "GET /api/servers": [servers[0]], "GET /api/projects": tree, "GET /api/environments/acme/staging/moves/latest": fail(404, "no move") });
    render(<Move project="acme" env="staging" />);
    expect(await screen.findByText("There is no other server to move to.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Add a server" }).getAttribute("href")).toBe("/servers/new");
  });
});
