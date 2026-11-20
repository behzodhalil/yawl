import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor, act, within } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { fail, fakeApi } from "../../test-api";
import * as seal from "../../seal";
import { Env } from "./Env";
import { S, apiView } from "./fixtures";

const tree = [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }, { name: "staging", server: "hel1", services: [] }] }];

describe("EnvPage", () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

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
    fireEvent.click(screen.getByRole("button", { name: "Actions for API_KEY" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Reveal for 30 seconds" }));
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    expect(screen.getByText("the-value")).toBeTruthy();
    await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
    expect(screen.queryByText("the-value")).toBeNull();
    vi.useRealTimers();
    fireEvent.click(screen.getByRole("button", { name: "Actions for API_KEY" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(await screen.findByRole("button", { name: "Yes, delete" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
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

  const envs = { "GET /api/environments/acme/production/env": [], "GET /api/environments/acme/staging/env": [] };

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
