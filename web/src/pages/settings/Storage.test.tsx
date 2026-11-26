import { afterEach, describe, expect, it } from "vitest";
import { Toasty } from "@cloudflare/kumo";
import { toasts } from "../../ui";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { Storage } from "./Storage";

afterEach(() => toasts.close());

const set = { set: true, endpoint: "s3.a.com", bucket: "acme", region: "", access_key_end: "1234", copies: [] };

describe("Storage hint", () => {
  it("does not promise saved keys before any are saved", async () => {
    fakeApi({ "GET /api/settings/storage": { set: false, copies: [] } });
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    await screen.findByLabelText("Secret key");
    expect(screen.queryByText("Leave both empty to keep the saved keys.")).toBeNull();
  });
});

describe("Storage", () => {
  it("saves with both keys empty when the endpoint is unchanged", async () => {
    const calls = fakeApi({ "GET /api/settings/storage": set, "PUT /api/settings/storage": null });
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    expect(await screen.findByText("Leave both empty to keep the saved keys.")).toBeTruthy();
    fireEvent.change(await screen.findByLabelText("Region"), { target: { value: "eu" } });
    fireEvent.click(screen.getByRole("button", { name: "Save storage" }));
    await screen.findByText("Saved. The next backup goes there.");
    expect(screen.queryByText("Enter the secret key again to change storage.")).toBeNull();
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ endpoint: "s3.a.com", bucket: "acme", region: "eu", access_key: "", secret_key: "" });
  });

  it("sends one request for two clicks", async () => {
    const calls = fakeApi({ "GET /api/settings/storage": set, "PUT /api/settings/storage": new Promise(() => {}) });
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    const b = await screen.findByRole("button", { name: "Save storage" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
  });
});

describe("Storage test connection", () => {
  const routes = (r: unknown) => ({ "GET /api/settings/storage": set, "POST /api/settings/storage/test": r });
  const posts = (c: { method: string }[]) => c.filter((x) => x.method === "POST");
  it("tests what is saved and says Connected.", async () => {
    const calls = fakeApi(routes(null));
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    fireEvent.click(await screen.findByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("Connected.")).toBeTruthy();
    expect(posts(calls)).toEqual([{ method: "POST", url: "/api/settings/storage/test", body: undefined }]);
  });
  it("shows the far end's error", async () => {
    fakeApi(routes(fail(502, "Bucket not reachable.")));
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    fireEvent.click(await screen.findByRole("button", { name: "Test connection" }));
    expect(await screen.findByText("Bucket not reachable.")).toBeTruthy();
  });
  it("sends one POST for two same-tick clicks", async () => {
    const calls = fakeApi(routes(new Promise(() => {})));
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    const b = await screen.findByRole("button", { name: "Test connection" });
    fireEvent.click(b);
    fireEvent.click(b);
    expect(posts(calls)).toHaveLength(1);
    expect(await screen.findByText("Testing…")).toBeTruthy();
  });
  it("drops an earlier Connected. once new settings are saved", async () => {
    fakeApi({ ...routes(null), "PUT /api/settings/storage": null });
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    fireEvent.click(await screen.findByRole("button", { name: "Test connection" }));
    await screen.findByText("Connected.");
    fireEvent.click(screen.getByRole("button", { name: "Save storage" }));
    await screen.findByText("Saved. The next backup goes there.");
    await waitFor(() => expect(screen.queryByText("Connected.")).toBeNull());
  });
  it("has no Test button before storage is saved, and hints when the form differs", async () => {
    fakeApi({ "GET /api/settings/storage": { set: false, copies: [] } });
    const { unmount } = render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    await screen.findByLabelText("Secret key");
    expect(screen.queryByRole("button", { name: "Test connection" })).toBeNull();
    unmount();
    fakeApi({ "GET /api/settings/storage": set });
    render(<Toasty toastManager={toasts}><Storage /></Toasty>);
    await screen.findByRole("button", { name: "Test connection" });
    await waitFor(() => expect(screen.queryByText("Tests the saved settings.")).toBeNull());
    fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "other" } });
    expect(screen.getByText("Tests the saved settings.")).toBeTruthy();
  });
});
