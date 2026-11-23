import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { fail, fakeApi } from "../../test-api";
import * as seal from "../../seal";
import { Env } from "./Env";
import { Deploys } from "./Deploy";
import { ServiceFrame } from "./Service";
import { POLL_MS } from "../../ui";
import type { DeployRow } from "../../types";
import { S, apiView } from "./fixtures";

const tree = [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }, { name: "staging", server: "hel1", services: [] }] }];

const withDeploys = (data: unknown[]) => render(
  <Deploys.Provider value={{ data: data as DeployRow[], reload: () => {} }}><Env project="acme" env="production" service="api" /></Deploys.Provider>);

const envs = { "GET /api/environments/acme/production/env": [], "GET /api/environments/acme/staging/env": [] };

describe("EnvPage", () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  it("sends only boxes", async () => {
    await sodium.ready;
    const k = sodium.crypto_box_keypair();
    const pub = sodium.to_base64(k.publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({
      "GET /api/projects": tree,
      [`GET ${S}/env`]: [{ key: "SHARED_ONE", scope: "shared", updated_at: 1 }],
      [`GET ${S}/keys`]: { agent_pubkey: pub, recovery_pubkey: pub, server: "hel1" },
      [`PUT ${S}/env/STRIPE_KEY`]: null,
      "GET /api/environments/acme/production/env": [],
      "GET /api/environments/acme/staging/env": [],
    });
    render(<Env project="acme" env="production" service="api" />);
    expect((await screen.findByText("SHARED_ONE")).closest("tr")!.textContent).toContain("shared");
    fireEvent.click(screen.getByRole("button", { name: "Add a variable" }));
    fireEvent.change(screen.getByLabelText("Key"), { target: { value: "STRIPE_KEY" } });
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "sk_live_abcdef123" } });
    fireEvent.click(screen.getByRole("button", { name: "Add variable" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    for (const c of calls) {
      expect(JSON.stringify(c.body ?? "")).not.toContain("sk_live_abcdef123");
      expect(JSON.stringify(c.body ?? "")).not.toContain(btoa("sk_live_abcdef123"));
    }
  });

  it("shows team and server variables, without Reveal or Delete", async () => {
    fakeApi({
      "GET /api/projects": tree,
      [`GET ${S}/env`]: [{ key: "SENTRY_DSN", scope: "team", updated_at: 1 }, { key: "SMTP_HOST", scope: "server", updated_at: 1 }],
      "GET /api/environments/acme/production/env": [],
      "GET /api/environments/acme/staging/env": [],
    });
    render(<Env project="acme" env="production" service="api" />);
    const row = (await screen.findByText("SENTRY_DSN")).closest("tr")!;
    expect(row.textContent).toContain("team");
    expect(screen.getByText("SMTP_HOST").closest("tr")!.textContent).toContain("server");
    expect(screen.queryByRole("button", { name: "Actions for SENTRY_DSN" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Actions for SMTP_HOST" })).toBeNull();
  });

  it("reveals for 30 seconds, confirms a delete, and diffs against staging", async () => {
    vi.spyOn(seal, "reveal").mockResolvedValue("the-value");
    const calls = fakeApi({
      "GET /api/projects": tree,
      [`GET ${S}/env`]: [{ key: "API_KEY", scope: "service", updated_at: 1 }],
      [`DELETE ${S}/env/API_KEY`]: null,
      "GET /api/environments/acme/production/env": [{ key: "API_KEY", scope: "service", service: "api" }],
      "GET /api/environments/acme/staging/env": [{ key: "API_KEY", scope: "service", service: "api" }, { key: "X", scope: "shared" }],
    });
    render(<Env project="acme" env="production" service="api" />);
    await screen.findByText("only in staging: X");
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Show API_KEY" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("the-value")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Hide API_KEY" })).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(screen.queryByText("the-value")).toBeNull();
    expect(screen.getByRole("button", { name: "Show API_KEY" })).toBeTruthy();
    vi.useRealTimers();
    fireEvent.click(screen.getByRole("button", { name: "Actions for API_KEY" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(await screen.findByRole("button", { name: "Yes, delete" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });
  it("hides a value 30 seconds after its latest Show", async () => {
    vi.spyOn(seal, "reveal").mockResolvedValue("the-value");
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "API_KEY", scope: "service", updated_at: 1 }], ...envs });
    render(<Env project="acme" env="production" service="api" />);
    await screen.findByText("API_KEY");
    vi.useFakeTimers();
    fireEvent.click(screen.getByRole("button", { name: "Show API_KEY" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
    fireEvent.click(screen.getByRole("button", { name: "Hide API_KEY" }));
    fireEvent.click(screen.getByRole("button", { name: "Show API_KEY" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(20_000); });
    expect(screen.getByText("the-value")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(10_000); });
    expect(screen.queryByText("the-value")).toBeNull();
  });

  it("compares values on request", async () => {
    await sodium.ready;
    const V = sodium.base64_variants.ORIGINAL;
    const kp = sodium.crypto_box_keypair(), ks = sodium.crypto_box_keypair();
    const calls = fakeApi({
      "GET /api/projects": tree,
      [`GET ${S}/env`]: [],
      "GET /api/environments/acme/production/env": [{ key: "A", scope: "shared" }],
      "GET /api/environments/acme/staging/env": [],
      "GET /api/environments/acme/production/keys": { agent_pubkey: sodium.to_base64(kp.publicKey, V), recovery_pubkey: "", server: "hel1" },
      "GET /api/environments/acme/staging/keys": { agent_pubkey: sodium.to_base64(ks.publicKey, V), recovery_pubkey: "", server: "hk1" },
      "POST /api/projects/acme/compare": [
        { service: "api", key: "DATABASE_URL", state: "same" }, { service: "api", key: "SECRET_KEY", state: "different" },
        { service: "", key: "SENTRY_DSN", state: "only_right" }, { service: "web", key: "OTHER", state: "different" }],
    });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Compare values" }));
    await screen.findByText("DATABASE_URL: same");
    expect(screen.getByText("SECRET_KEY: different")).toBeTruthy();
    expect(screen.getByText("SENTRY_DSN: only in staging")).toBeTruthy();
    expect(screen.queryByText("only in production: A")).toBeNull();
    expect(screen.queryByText("OTHER: different")).toBeNull();
    const post = calls.find((c) => c.method === "POST")!.body as { left: string; right: string; keys: Record<string, string> };
    expect([post.left, post.right]).toEqual(["production", "staging"]);
    const a = sodium.crypto_box_seal_open(sodium.from_base64(post.keys.production, V), kp.publicKey, kp.privateKey);
    const b = sodium.crypto_box_seal_open(sodium.from_base64(post.keys.staging, V), ks.publicKey, ks.privateKey);
    expect(a.length).toBe(32);
    expect(Array.from(a)).toEqual(Array.from(b));
  });

  it("shows the offline error", async () => {
    await sodium.ready;
    const k = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    fakeApi({
      "GET /api/projects": tree,
      [`GET ${S}/env`]: [],
      "GET /api/environments/acme/production/env": [],
      "GET /api/environments/acme/staging/env": [{ key: "X", scope: "shared" }],
      "GET /api/environments/acme/production/keys": { agent_pubkey: k, recovery_pubkey: "", server: "hel1" },
      "GET /api/environments/acme/staging/keys": { agent_pubkey: k, recovery_pubkey: "", server: "hel1" },
      "POST /api/projects/acme/compare": fail(409, "hel1 is offline; values cannot be compared now."),
    });
    render(<Env project="acme" env="production" service="api" />);
    expect(await screen.findByText("only in staging: X")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Compare values" }));
    expect(await screen.findByText("hel1 is offline; values cannot be compared now.")).toBeTruthy();
  });

  it("has no build-time switch for an image service", async () => {
    fakeApi({
      "GET /api/projects": tree,
      [`GET ${S}`]: { ...apiView, form: { ...apiView.form, repo: "", image: "grafana/grafana:11" } },
      [`GET ${S}/env`]: [],
      "GET /api/environments/acme/production/env": [],
      "GET /api/environments/acme/staging/env": [],
    });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add a variable" }));
    await screen.findByLabelText("Key");
    await waitFor(() => expect(screen.queryByText("Also at build time")).toBeNull());
  });

  it("shows a failed delete inside its dialog", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "API_KEY", scope: "service", service: "api", updated_at: 1 }],
      [`DELETE ${S}/env/API_KEY`]: () => fail(500, "nope"),
      "GET /api/environments/acme/production/env": [], "GET /api/environments/acme/staging/env": [] });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Actions for API_KEY" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, delete" }));
    expect(await within(screen.getByRole("dialog")).findByText(/nope/)).toBeTruthy();
  });

  it("warns that an existing key is replaced, and edits a value with the key fixed", async () => {
    await sodium.ready;
    const pub = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "API_KEY", scope: "service", service: "api", updated_at: 1 }],
      [`GET ${S}/keys`]: { agent_pubkey: pub, recovery_pubkey: pub, server: "hel1" }, [`PUT ${S}/env/API_KEY`]: null,
      "GET /api/environments/acme/production/env": [], "GET /api/environments/acme/staging/env": [] });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add a variable" }));
    fireEvent.change(screen.getByLabelText("Key"), { target: { value: "api_key" } });
    expect(screen.getByText("API_KEY already exists. Adding replaces its value.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Replace variable" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Actions for API_KEY" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit value" }));
    expect((screen.getByLabelText("Key") as HTMLInputElement).value).toBe("API_KEY");
    expect((screen.getByLabelText("Key") as HTMLInputElement).readOnly).toBe(true);
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "new" } });
    const b = screen.getByRole("button", { name: "Replace variable" });
    fireEvent.click(b); fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
  });

  it("edits a shared variable in its own scope, with the Shared box locked", async () => {
    await sodium.ready;
    const pub = sodium.to_base64(sodium.crypto_box_keypair().publicKey, sodium.base64_variants.ORIGINAL);
    const calls = fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "SH", scope: "shared", updated_at: 1 }],
      "GET /api/environments/acme/production/keys": { agent_pubkey: pub, recovery_pubkey: pub, server: "hel1" },
      "PUT /api/environments/acme/production/env/SH": null, ...envs });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Actions for SH" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit value" }));
    const box = screen.getByLabelText(/Shared by every service/) as HTMLInputElement;
    expect(box.checked).toBe(true);
    expect(box.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Value"), { target: { value: "v" } });
    fireEvent.click(screen.getByRole("button", { name: "Replace variable" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT" && c.url === "/api/environments/acme/production/env/SH")).toBe(true));
  });

  it("does not warn about a service variable when the new one is shared", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "KEY", scope: "service", service: "api", updated_at: 1 }], ...envs });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Add a variable" }));
    fireEvent.change(screen.getByLabelText("Key"), { target: { value: "key" } });
    expect(screen.getByText(/already exists/)).toBeTruthy();
    fireEvent.click(screen.getByLabelText(/Shared by every service/));
    expect(screen.queryByText(/already exists/)).toBeNull();
  });

  it("filters keys without case", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [
      { key: "DATABASE_URL", scope: "service", updated_at: 1 }, { key: "API_KEY", scope: "service", updated_at: 1 }, { key: "DB_POOL", scope: "shared", updated_at: 1 }], ...envs });
    render(<Env project="acme" env="production" service="api" />);
    const box = await screen.findByPlaceholderText("Filter 3 variables");
    fireEvent.change(box, { target: { value: "db" } });
    expect(screen.getByText("DB_POOL")).toBeTruthy();
    expect(screen.queryByText("DATABASE_URL")).toBeNull();
    expect(screen.queryByText("API_KEY")).toBeNull();
    fireEvent.change(box, { target: { value: "nothing" } });
    expect(screen.getByText("No variables match.")).toBeTruthy();
  });

  it("copies a value without showing it, and shows an empty value as empty", async () => {
    const write = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText: write } });
    const reveal = vi.spyOn(seal, "reveal").mockImplementation(async (_, k) => (k === "API_KEY" ? "sk_live_1" : ""));
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [
      { key: "API_KEY", scope: "service", updated_at: 1 }, { key: "BLANK", scope: "service", updated_at: 1 },
      { key: "DB", scope: "service", ref: "db.url", updated_at: 1 }, { key: "SENTRY_DSN", scope: "team", updated_at: 1 }], ...envs });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy API_KEY" }));
    await waitFor(() => expect(write).toHaveBeenCalledWith("sk_live_1"));
    expect(reveal).toHaveBeenCalledWith(S, "API_KEY");
    expect(await screen.findByText("Copied")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copied API_KEY" })).toBeTruthy();
    expect(screen.queryByText("sk_live_1")).toBeNull();
    expect(screen.queryByRole("button", { name: "Show DB" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy DB" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Show SENTRY_DSN" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy SENTRY_DSN" })).toBeNull();
    const row = screen.getByText("BLANK").closest("tr")!;
    expect(row.textContent).toContain("••••••••");
    fireEvent.click(screen.getByRole("button", { name: "Show BLANK" }));
    expect(await within(row).findByText("empty")).toBeTruthy();
    expect(row.textContent).not.toContain("••••••••");
  });

  it("counts changes made after the live deploy and deploys them", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [
      { key: "OLD", scope: "service", updated_at: 100 }, { key: "NEW", scope: "service", updated_at: 300 }, { key: "SH", scope: "shared", updated_at: 400 }],
      [`POST ${S}/deploys`]: { deploy_id: "dep_4" }, ...envs });
    withDeploys([{ id: "dep_3", state: "failed", rollback: false, created_at: 350, live: false, can_roll_back: false },
      { id: "dep_2", state: "succeeded", rollback: false, created_at: 200, live: true, can_roll_back: false }]);
    expect(await screen.findByText("2 changes not deployed yet")).toBeTruthy();
    expect(screen.getByText("NEW").closest("tr")!.textContent).toContain("changed");
    expect(screen.getByText("SH").closest("tr")!.textContent).toContain("changed");
    expect(screen.getByText("OLD").closest("tr")!.textContent).not.toContain("changed");
    fireEvent.click(screen.getByRole("button", { name: "Deploy" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_4"));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ commit: "" });
  });

  it("points to the running deploy instead of offering another", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "NEW", scope: "service", updated_at: 300 }], ...envs });
    withDeploys([{ id: "dep_3", state: "building", rollback: false, created_at: 350, live: false, can_roll_back: false },
      { id: "dep_2", state: "live", rollback: false, created_at: 200, live: true, can_roll_back: false }]);
    expect(await screen.findByText("1 change not deployed yet")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Deploying" }).getAttribute("href")).toBe("/p/acme/production/s/api/deploys/dep_3");
    expect(screen.queryByRole("button", { name: "Deploy" })).toBeNull();
  });

  it("says 1 change for a single change", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "NEW", scope: "service", updated_at: 300 }], ...envs });
    withDeploys([{ id: "dep_2", state: "succeeded", rollback: false, created_at: 200, live: true, can_roll_back: false }]);
    expect(await screen.findByText("1 change not deployed yet")).toBeTruthy();
    expect(screen.getByPlaceholderText("Filter 1 variable")).toBeTruthy();
  });

  it("shows no changes without a live deploy", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "NEW", scope: "service", updated_at: 300 }], ...envs });
    withDeploys([{ id: "dep_2", state: "failed", rollback: false, created_at: 200, live: false, can_roll_back: false }]);
    const row = (await screen.findByText("NEW")).closest("tr")!;
    expect(screen.queryByText(/not deployed yet/)).toBeNull();
    expect(row.textContent).not.toContain("changed");
    expect(screen.queryByRole("button", { name: "Deploy" })).toBeNull();
  });

  it("starts a fresh form after Edit then Cancel then Add", async () => {
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "SH", scope: "shared", ref: "db.url", updated_at: 1 }], ...envs });
    render(<Env project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Actions for SH" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Edit value" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Add a variable" }));
    expect((screen.getByLabelText(/Shared by every service/) as HTMLInputElement).checked).toBe(false);
    expect((screen.getByLabelText(/Shared by every service/) as HTMLInputElement).disabled).toBe(false);
    expect((screen.getByLabelText("Key") as HTMLInputElement).value).toBe("");
    expect(screen.getByLabelText("Value")).toBeTruthy();
  });
});

describe("EnvPage copy with ClipboardItem", () => {
  afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

  const setup = (write: (items: { parts: Record<string, Promise<Blob>> }[]) => Promise<void>) => {
    vi.stubGlobal("ClipboardItem", class { constructor(public parts: Record<string, Promise<Blob>>) {} });
    const spy = vi.fn(write);
    vi.stubGlobal("navigator", { ...navigator, clipboard: { write: spy, writeText: vi.fn() } });
    fakeApi({ "GET /api/projects": tree, [`GET ${S}/env`]: [{ key: "API_KEY", scope: "service", updated_at: 1 }], ...envs });
    render(<Env project="acme" env="production" service="api" />);
    return spy;
  };

  it("writes the clipboard within the click and fills it once the value is revealed", async () => {
    let release!: (v: string) => void;
    vi.spyOn(seal, "reveal").mockReturnValue(new Promise((ok) => { release = ok; }));
    let text = "";
    const write = setup(async ([item]) => { text = await (await item.parts["text/plain"]).text(); });
    fireEvent.click(await screen.findByRole("button", { name: "Copy API_KEY" }));
    expect(write).toHaveBeenCalledTimes(1);
    release("sk_live_1");
    expect(await screen.findByRole("button", { name: "Copied API_KEY" })).toBeTruthy();
    expect(text).toBe("sk_live_1");
    expect(navigator.clipboard.writeText).not.toHaveBeenCalled();
  });

  it("says the browser refused when the clipboard write fails", async () => {
    vi.spyOn(seal, "reveal").mockResolvedValue("sk_live_1");
    setup(async () => { throw new DOMException("denied", "NotAllowedError"); });
    fireEvent.click(await screen.findByRole("button", { name: "Copy API_KEY" }));
    expect(await screen.findByText("The browser did not allow copying. Use Show and select the value.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Copied API_KEY" })).toBeNull();
  });

  it("shows the reveal error when the value cannot be revealed", async () => {
    vi.spyOn(seal, "reveal").mockRejectedValue(new Error("hel1 is offline"));
    setup(async ([item]) => { await item.parts["text/plain"]; });
    fireEvent.click(await screen.findByRole("button", { name: "Copy API_KEY" }));
    expect(await screen.findByText("hel1 is offline")).toBeTruthy();
    expect(screen.queryByText(/did not allow copying/)).toBeNull();
  });
});

describe("EnvPage in the service frame", () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  const live = { id: "dep_2", state: "live", rollback: false, created_at: 200, live: true, can_roll_back: false };
  const running = { id: "dep_3", state: "building", rollback: false, created_at: 350, live: false, can_roll_back: false };
  const frame = (deploys: () => unknown) => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, "GET /api/checks": { off: true }, "GET /api/projects": tree,
      [`GET ${S}/env`]: [{ key: "NEW", scope: "service", updated_at: 300 }], [`GET ${S}/deploys`]: deploys, ...envs });
    render(<ServiceFrame p={{ project: "acme", env: "production", service: "api" }} tab="env" />);
    return calls;
  };

  it("clears the pending line once the running deploy goes live", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let rows: unknown[] = [running, live];
    frame(() => rows);
    expect(await screen.findByRole("link", { name: "Deploying" })).toBeTruthy();
    rows = [{ ...running, state: "live", live: true }, { ...live, live: false }];
    await act(async () => { vi.advanceTimersByTime(POLL_MS); });
    await waitFor(() => expect(screen.queryByText(/not deployed yet/)).toBeNull());
    expect(screen.queryByRole("link", { name: "Deploying" })).toBeNull();
  });

  it("offers Deploy again once the running deploy fails, and stops asking", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    let rows: unknown[] = [running, live];
    const calls = frame(() => rows);
    expect(await screen.findByRole("link", { name: "Deploying" })).toBeTruthy();
    rows = [{ ...running, state: "failed" }, live];
    await act(async () => { vi.advanceTimersByTime(POLL_MS); });
    await waitFor(() => expect(screen.queryByRole("link", { name: "Deploying" })).toBeNull());
    expect(within(screen.getByText("1 change not deployed yet")).getByRole("button", { name: "Deploy" })).toBeTruthy();
    const asked = calls.filter((c) => c.url === `${S}/deploys`).length;
    await act(async () => { vi.advanceTimersByTime(POLL_MS * 3); });
    expect(calls.filter((c) => c.url === `${S}/deploys`).length).toBe(asked);
  });

  it("shows no pending line when the deploys cannot be loaded", async () => {
    const calls = frame(() => fail(500, "down"));
    await screen.findByText("NEW");
    await waitFor(() => expect(calls.some((c) => c.url === `${S}/deploys`)).toBe(true));
    await act(async () => {});
    expect(screen.queryByText(/not deployed yet/)).toBeNull();
  });
});
