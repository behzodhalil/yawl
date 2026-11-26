import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Account } from "./Account";

const base = { "GET /api/account/two-step": { on: false, codes_left: 0, required: false } };

describe("Account", () => {
  it.each([["developer", "a developer"], ["admin", "an admin"], ["viewer", "a viewer"]])("names the %s role", async (role, text) => {
    fakeApi({ ...base, "GET /api/me": { email: "a@b", role } });
    render(<Account />);
    await screen.findByText("a@b");
    expect(screen.getByText(/Signed in as/).textContent).toBe(`Signed in as a@b, ${text}.`);
  });

  it("keeps Change password off until all three fields are filled", async () => {
    fakeApi({ ...base, "GET /api/me": { email: "a@b", role: "admin" } });
    render(<Account />);
    const b = await screen.findByRole("button", { name: "Change password" });
    expect((b as HTMLButtonElement).disabled).toBe(true);
    for (const l of ["Current password", "New password"]) fireEvent.change(screen.getByLabelText(l), { target: { value: "correct horse battery" } });
    expect((b as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("New password again"), { target: { value: "correct horse battery" } });
    expect((b as HTMLButtonElement).disabled).toBe(false);
  });

  it("sends one password change for two clicks", async () => {
    const calls = fakeApi({ ...base, "GET /api/me": { email: "a@b", role: "admin" }, "PUT /api/account/password": new Promise(() => {}) });
    render(<Account />);
    for (const l of ["Current password", "New password", "New password again"]) fireEvent.change(await screen.findByLabelText(l), { target: { value: "correct horse battery" } });
    const b = screen.getByRole("button", { name: "Change password" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1));
  });
});
