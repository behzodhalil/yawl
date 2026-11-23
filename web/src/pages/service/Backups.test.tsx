import { afterEach, describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import sodium from "libsodium-wrappers";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fakeApi } from "../../test-api";
import { POLL_MS } from "../../ui";
import { Backups, VolumeBackups } from "./Backups";

process.env.TZ = "Asia/Tashkent";

const D = "/api/services/acme/production/acme-db";
const key = JSON.parse(readFileSync(join(process.cwd(), "../internal/seal/testdata/vectors.json"), "utf8")).recovery as string;

describe("Backups", () => {
  it("names a backup by its date and keeps the id in the title", async () => {
    const at = new Date(2025, 10, 21, 8, 40).getTime();
    fakeApi({
      [`GET ${D}/backups`]: [{ id: "bak_old", state: "ok", size: 1, created_at: at }],
      [`GET ${D}`]: { form: { kind: "postgres" } },
      [`GET ${D}/pitr`]: { pitr: false, from: 0, to: 0, shipped: 0 },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    const cell = await screen.findByText("21 Nov 2025, 08:40");
    expect(cell.getAttribute("title")).toMatch(/ ago · bak_old$/);
    expect(screen.queryByText("bak_old")).toBeNull();
    expect(screen.getByRole("button", { name: "Restore the backup of 21 Nov 2025, 08:40" })).toBeTruthy();
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Taken", "State", "Size", ""]);
  });

  it("links to the storage settings instead of showing the command", async () => {
    fakeApi({
      [`GET ${D}/backups`]: [{ id: "bak_1", state: "failed", created_at: Date.now(), cause: "backup storage is not set: run yawl server storage set" }],
      [`GET ${D}`]: { form: { kind: "postgres" } },
      [`GET ${D}/pitr`]: { pitr: false, from: 0, to: 0, shipped: 0 },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    const link = await screen.findByRole("link", { name: "Set up storage" });
    expect(link.getAttribute("href")).toMatch(/\/settings\/storage$/);
    expect(screen.getByText(/Backup storage is not set\./)).toBeTruthy();
    expect(screen.queryByText(/yawl server storage set/)).toBeNull();
  });

  it("says how many backups are kept", async () => {
    fakeApi({
      [`GET ${D}/backups`]: [],
      [`GET ${D}`]: { form: { kind: "postgres", keep: { daily: 14, weekly: 0, monthly: 6 } } },
      [`GET ${D}/pitr`]: { pitr: false, from: 0, to: 0, shipped: 0 },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    await screen.findByText(/kept off this server: 14 daily and 6 monthly\./);
  });

  it("says what a ClickHouse restore leaves out", async () => {
    fakeApi({
      [`GET ${D}/backups`]: [],
      [`GET ${D}`]: { form: { kind: "clickhouse", backup: true } },
      [`GET ${D}/pitr`]: { pitr: false, from: 0, to: 0, shipped: 0 },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    await screen.findByText(/A materialized view without a TO table comes back/);
    expect(screen.getByText(/^Daily, encrypted to your recovery key/)).toBeTruthy();
    expect(screen.queryByText(/Import/)).toBeNull();
  });

  it("lists, backs up now, and restores with the key sealed", async () => {
    await sodium.ready;
    const agent = sodium.crypto_box_keypair();
    const calls = fakeApi({
      [`GET ${D}/backups`]: [{ id: "bak_1", state: "ok", size: 2_500_000, created_at: Date.now(),
        copies: [{ copy: "eu", state: "ok" }, { copy: "us", state: "failed", cause: "no such host" }] }],
      [`POST ${D}/backups`]: { backup: "bak_2" },
      [`GET ${D}/keys`]: { agent_pubkey: sodium.to_base64(agent.publicKey, sodium.base64_variants.ORIGINAL), recovery_pubkey: "", server: "hel1" },
      [`POST ${D}/restore`]: { deploy: "dep_5" },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    await screen.findByText("2.4 MB");
    fireEvent.click(screen.getByRole("button", { name: "Back up now" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/backups"))).toBe(true));

    fireEvent.click(screen.getByRole("button", { name: /^Restore the backup of / }));
    fireEvent.change(screen.getByLabelText("New database name"), { target: { value: "acme-db-2" } });
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: "yawl-recovery-NOTAKEY" } });
    fireEvent.click(screen.getByRole("button", { name: "Restore into a new database" }));
    await screen.findByText("That is not a Yawl recovery key.");
    expect(calls.some((c) => c.url.endsWith("/restore"))).toBe(false);

    expect(screen.getByText("Copy to us failed: no such host")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Read from"), { target: { value: "eu" } });
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: key } });
    fireEvent.click(screen.getByRole("button", { name: "Restore into a new database" }));
    await screen.findByText(/Restoring into acme-db-2\./);
    expect(screen.getByText(/set DATABASE_URL to the reference acme-db-2\.url, then deploy\./)).toBeTruthy();
    const r = calls.find((c) => c.url.endsWith("/restore"))!;
    expect(r.body).toMatchObject({ backup: "bak_1", into: "acme-db-2", from: "eu" });
    for (const c of calls) expect(JSON.stringify(c.body ?? "")).not.toContain(key.slice(14));
    const opened = sodium.crypto_box_seal_open(sodium.from_base64((r.body as { recovery_box: string }).recovery_box, sodium.base64_variants.ORIGINAL), agent.publicKey, agent.privateKey);
    expect(new TextDecoder().decode(opened)).toBe(key);
  });
});

describe("Backups, restore to a time", () => {
  it("shows the window and posts the time with the key sealed", async () => {
    await sodium.ready;
    const agent = sodium.crypto_box_keypair();
    const calls = fakeApi({
      [`GET ${D}/backups`]: [],
      [`GET ${D}/pitr`]: { pitr: true, from: Date.UTC(2026, 9, 23, 3, 5), to: Date.UTC(2026, 9, 30, 17, 38), shipped: Date.now() },
      [`GET ${D}/keys`]: { agent_pubkey: sodium.to_base64(agent.publicKey, sodium.base64_variants.ORIGINAL), recovery_pubkey: "", server: "hel1" },
      [`POST ${D}/restore`]: { deploy: "dep_9" },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    expect(await screen.findByText("Any second from 23 Oct 2026 08:05 to 30 Oct 2026 22:38.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Restore to a time" }));
    fireEvent.change(screen.getByLabelText("Time"), { target: { value: "2026-10-30T22:40:05" } });
    fireEvent.change(screen.getByLabelText("New database name"), { target: { value: "acme-db-1740" } });
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: key } });
    fireEvent.click(screen.getByRole("button", { name: "Restore into a new database" }));
    await screen.findByText(/Restoring into acme-db-1740\./);
    expect(calls.find((c) => c.url.endsWith("/restore"))!.body).toMatchObject({ at: "2026-10-30T17:40:05Z", into: "acme-db-1740" });
  });

  it("warns when the WAL has not been uploaded for 10 minutes", async () => {
    fakeApi({
      [`GET ${D}/backups`]: [],
      [`GET ${D}/pitr`]: { pitr: true, from: Date.UTC(2026, 9, 23, 3, 5), to: Date.now() - 23 * 60_000, shipped: Date.now() - 20 * 60_000 },
    });
    render(<Backups project="acme" env="production" service="acme-db" />);
    expect(await screen.findByText(/^Changes were last uploaded 20 min ago/)).toBeTruthy();
  });
});

const A = "/api/services/acme/production/api";

describe("VolumeBackups", () => {
  it("lists each volume, backs one up, and restores it in place with the name typed", async () => {
    await sodium.ready;
    const agent = sodium.crypto_box_keypair();
    const at = Date.UTC(2026, 9, 28, 3, 30);
    const calls = fakeApi({
      [`GET ${A}/backups`]: [{ id: "bak_u", state: "ok", size: 2_500_000, created_at: at, volume: "uploads" }],
      [`POST ${A}/backups`]: { id: "bak_2" },
      [`GET ${A}/keys`]: { agent_pubkey: sodium.to_base64(agent.publicKey, sodium.base64_variants.ORIGINAL), recovery_pubkey: "", server: "hel1" },
      [`POST ${A}/restore`]: { deploy: "dep_7" },
    });
    render(<VolumeBackups project="acme" env="production" service="api" volumes={[{ name: "uploads", path: "/app/uploads" }, { name: "index", path: "/i" }]} />);
    expect(await screen.findByText("A volume is read while the app runs: a file being written at that moment may be cut. Keep what must be exact in Postgres.")).toBeTruthy();
    await screen.findByText("2.4 MB");
    expect(screen.getByText("No backups of index yet.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back up uploads now" }));
    await waitFor(() => expect(calls.find((c) => c.method === "POST" && c.url.endsWith("/backups"))?.body).toEqual({ volume: "uploads" }));

    fireEvent.click(screen.getByRole("button", { name: /^Restore the backup of / }));
    expect(screen.getByText("api stops, uploads is emptied and filled from the backup of 28 Oct 2026 08:30, then api starts.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: key } });
    const go = screen.getByRole("button", { name: "Restore uploads" }) as HTMLButtonElement;
    expect(go.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Type api to confirm"), { target: { value: "api" } });
    fireEvent.click(go);
    await screen.findByText(/Restoring uploads\./);
    const r = calls.find((c) => c.url.endsWith("/restore"))!;
    expect(r.body).toMatchObject({ backup: "bak_u" });
    expect(r.body).not.toHaveProperty("into");
  });

  it("backs up one volume at a time per volume: a's button is held, b's stays usable", async () => {
    let release!: () => void;
    const calls = fakeApi({
      [`GET ${A}/backups`]: [],
      [`POST ${A}/backups`]: (b: unknown) => (b as { volume: string }).volume === "a" ? new Promise((r) => { release = () => r({ id: "bak_a" }); }) : { id: "bak_b" },
    });
    render(<VolumeBackups project="acme" env="production" service="api" volumes={[{ name: "a", path: "/a" }, { name: "b", path: "/b" }]} />);
    const a = await screen.findByRole("button", { name: "Back up a now" }) as HTMLButtonElement;
    fireEvent.click(a);
    fireEvent.click(a);
    await waitFor(() => expect(a.disabled).toBe(true));
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    const b = screen.getByRole("button", { name: "Back up b now" }) as HTMLButtonElement;
    expect(b.disabled).toBe(false);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "POST").map((c) => c.body)).toEqual([{ volume: "a" }, { volume: "b" }]));
    release();
    await waitFor(() => expect(a.disabled).toBe(false));
  });

  it("restores a volume from a copy", async () => {
    await sodium.ready;
    const agent = sodium.crypto_box_keypair();
    const calls = fakeApi({
      [`GET ${A}/backups`]: [{ id: "bak_u", state: "ok", created_at: Date.now(), volume: "uploads", copies: [{ copy: "eu", state: "ok" }] }],
      [`GET ${A}/keys`]: { agent_pubkey: sodium.to_base64(agent.publicKey, sodium.base64_variants.ORIGINAL), recovery_pubkey: "", server: "hel1" },
      [`POST ${A}/restore`]: { deploy: "dep_7" },
    });
    render(<VolumeBackups project="acme" env="production" service="api" volumes={[{ name: "uploads", path: "/app/uploads" }]} />);
    fireEvent.click(await screen.findByRole("button", { name: /^Restore the backup of / }));
    fireEvent.change(screen.getByLabelText("Read from"), { target: { value: "eu" } });
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: key } });
    fireEvent.change(screen.getByLabelText("Type api to confirm"), { target: { value: "api" } });
    fireEvent.click(screen.getByRole("button", { name: "Restore uploads" }));
    await screen.findByText(/Restoring uploads\./);
    expect(calls.find((c) => c.url.endsWith("/restore"))!.body).toMatchObject({ backup: "bak_u", from: "eu" });
  });

  it("a Valkey restore names REDIS_URL", async () => {
    await sodium.ready;
    const C = "/api/services/acme/production/cache";
    const agent = sodium.crypto_box_keypair();
    fakeApi({
      [`GET ${C}/backups`]: [{ id: "bak_1", state: "ok", size: 1, created_at: Date.now() }],
      [`GET ${C}`]: { form: { kind: "valkey", backup: true } },
      [`GET ${C}/keys`]: { agent_pubkey: sodium.to_base64(agent.publicKey, sodium.base64_variants.ORIGINAL), recovery_pubkey: "", server: "hel1" },
      [`POST ${C}/restore`]: { deploy: "dep_5" },
    });
    render(<Backups project="acme" env="production" service="cache" />);
    fireEvent.click(await screen.findByRole("button", { name: /^Restore the backup of / }));
    fireEvent.change(screen.getByLabelText("Recovery key"), { target: { value: key } });
    fireEvent.click(screen.getByRole("button", { name: "Restore into a new database" }));
    expect(await screen.findByText(/set REDIS_URL to the reference cache-restored\.url, then deploy\./)).toBeTruthy();
  });

  it("a Valkey without backup says so, not 'Daily'", async () => {
    const C = "/api/services/acme/production/cache";
    fakeApi({ [`GET ${C}/backups`]: [], [`GET ${C}`]: { form: { kind: "valkey", backup: false } } });
    render(<Backups project="acme" env="production" service="cache" />);
    expect(await screen.findByText("Not backed up every night. Turn it on with backup: true in yawl.yaml; Back up now still works.")).toBeTruthy();
    expect(screen.queryByText(/^Daily, encrypted/)).toBeNull();
  });
});

describe("Backups, live", () => {
  afterEach(() => vi.useRealTimers());
  const view = { form: { kind: "postgres" } };
  const pitr = { pitr: false, from: 0, to: 0, shipped: 0 };

  it("backs up once for two clicks in one tick, and re-reads the list", async () => {
    const calls = fakeApi({ [`GET ${D}/backups`]: [], [`GET ${D}`]: view, [`GET ${D}/pitr`]: pitr, [`POST ${D}/backups`]: new Promise((r) => setTimeout(() => r({ backup: "b" }), 20)) });
    render(<Backups project="acme" env="production" service="acme-db" />);
    const b = await screen.findByRole("button", { name: "Back up now" });
    const before = calls.filter((c) => c.url.endsWith("/backups") && c.method === "GET").length;
    act(() => { b.click(); b.click(); });
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
    await waitFor(() => expect(calls.filter((c) => c.url.endsWith("/backups") && c.method === "GET").length).toBeGreaterThan(before));
  });

  it("re-reads the list while a backup is running", async () => {
    vi.useFakeTimers();
    const calls = fakeApi({ [`GET ${D}/backups`]: [{ id: "bak_1", state: "running", created_at: Date.now() }], [`GET ${D}`]: view, [`GET ${D}/pitr`]: pitr });
    const { unmount } = render(<Backups project="acme" env="production" service="acme-db" />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const n = calls.filter((c) => c.url.endsWith("/backups")).length;
    await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS); });
    expect(calls.filter((c) => c.url.endsWith("/backups")).length).toBeGreaterThan(n);
    unmount();
  });

  it("does not poll when no backup is running", async () => {
    vi.useFakeTimers();
    const calls = fakeApi({ [`GET ${D}/backups`]: [{ id: "bak_1", state: "ok", created_at: Date.now() }], [`GET ${D}`]: view, [`GET ${D}/pitr`]: pitr });
    const { unmount } = render(<Backups project="acme" env="production" service="acme-db" />);
    await act(async () => { await vi.advanceTimersByTimeAsync(0); });
    const n = calls.length;
    await act(async () => { await vi.advanceTimersByTimeAsync(POLL_MS * 3); });
    expect(calls.length).toBe(n);
    unmount();
  });
});
