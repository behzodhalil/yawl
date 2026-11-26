import { afterEach, describe, expect, it } from "vitest";
import { Toasty } from "@cloudflare/kumo";
import { toasts } from "../../ui";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { SSO } from "./SSO";

afterEach(() => toasts.close());

describe("SSO settings", () => {
  it("shows the provider, keeps the secret unless typed, and turns SSO only on", async () => {
    const calls = fakeApi({
      "GET /api/settings/sso": { name: "Acme Google", issuer: "https://accounts.google.com", client_id: "cid", only: false, secret_set: true, works: true, callback: "https://panel.acme.uz/auth/sso/callback" },
      "PUT /api/settings/sso": null,
    });
    render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    expect(((await screen.findByLabelText("Issuer")) as HTMLInputElement).value).toBe("https://accounts.google.com");
    expect(screen.getByText(/says whether an email is verified/).textContent).toContain("Microsoft Entra does not, so it cannot sign members in");
    expect(screen.getByText("The secret is set; leave it empty to keep it.")).toBeTruthy();
    expect((screen.getByLabelText("Callback URL") as HTMLInputElement).value).toBe("https://panel.acme.uz/auth/sso/callback");
    fireEvent.click(screen.getByLabelText("Only SSO: password sign-in is refused, except for the oldest Admin"));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PUT")).toBe(true));
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ name: "Acme Google", issuer: "https://accounts.google.com", client_id: "cid", client_secret: "", only: true });
  });
});

describe("SSO single save", () => {
  it("sends one request for two clicks", async () => {
    const calls = fakeApi({ "GET /api/settings/sso": { name: "", issuer: "", client_id: "", only: false }, "PUT /api/settings/sso": new Promise(() => {}) });
    render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    const b = await screen.findByRole("button", { name: "Save" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
  });
});

describe("SSO callback, Only SSO and turning off", () => {
  const data = { name: "Acme", issuer: "https://idp.test", client_id: "cid", only: false, secret_set: true, works: true };

  it("shows the callback in its own field with Copy", async () => {
    fakeApi({ "GET /api/settings/sso": { ...data, callback: "https://p.test/auth/sso/callback" } });
    render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    const f = (await screen.findByLabelText("Callback URL")) as HTMLInputElement;
    expect(f.value).toBe("https://p.test/auth/sso/callback");
    expect(f.readOnly).toBe(true);
    expect(f.parentElement!.querySelector("button")!.textContent).toBe("Copy");
  });

  it("says how to get a callback when there is none", async () => {
    fakeApi({ "GET /api/settings/sso": data });
    const { container } = render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    await screen.findByLabelText("Issuer");
    expect(container.textContent).not.toContain("undefined");
    expect(screen.getByText(/Start the panel with --public-url/)).toBeTruthy();
    expect(screen.queryByLabelText("Callback URL")).toBeNull();
  });

  it("Only SSO is disabled until an SSO sign-in worked", async () => {
    fakeApi({ "GET /api/settings/sso": { ...data, works: false } });
    const { unmount } = render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    expect(((await screen.findByLabelText(/^Only SSO/)) as HTMLInputElement).disabled).toBe(true);
    expect(screen.getByText("Sign in with SSO once, in a private window, to turn this on.")).toBeTruthy();
    unmount();
    fakeApi({ "GET /api/settings/sso": data });
    render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    expect(((await screen.findByLabelText(/^Only SSO/)) as HTMLInputElement).disabled).toBe(false);
  });

  it("clearing the issuer asks before turning SSO off", async () => {
    const calls = fakeApi({ "GET /api/settings/sso": data, "PUT /api/settings/sso": null });
    render(<Toasty toastManager={toasts}><SSO /></Toasty>);
    fireEvent.change(await screen.findByLabelText("Issuer"), { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByText("Turn SSO off?")).toBeTruthy();
    expect(screen.getByText("Members sign in with their passwords again.")).toBeTruthy();
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Yes, turn off" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
    expect((calls.find((c) => c.method === "PUT")!.body as { issuer: string }).issuer).toBe("");
  });
});
