import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Members } from "./Members";

process.env.TZ = "Asia/Tashkent";

describe("Members", () => {
  it("lists members, invites with a link shown once, changes a role and removes", async () => {
    const calls = fakeApi({
      "GET /api/members": [
        { id: "own_1", email: "me@acme.uz", role: "admin", last_sign_in_at: Date.now() - 60_000 },
        { id: "own_2", email: "dev@acme.uz", role: "developer" },
      ],
      "GET /api/invites": [{ id: "inv_1", role: "viewer", expires_at: Date.UTC(2026, 10, 4, 12, 0), created_by: "me@acme.uz" }],
      "POST /api/invites": { token: "tok123", id: "inv_2" },
      "PUT /api/members/own_2": null,
      "DELETE /api/members/own_2": null,
      "DELETE /api/invites/inv_1": null,
    });
    render(<Members />);
    expect(await screen.findByText("dev@acme.uz")).toBeTruthy();
    expect(screen.getByText("viewer, valid until 4 Nov 2026 17:00, by me@acme.uz")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Invite a member" }));
    fireEvent.change(screen.getByLabelText("Role of the new member"), { target: { value: "developer" } });
    fireEvent.click(screen.getByRole("button", { name: "Invite" }));
    expect(await screen.findByText(`${location.origin}/invite#tok123`)).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
    expect(calls.find((c) => c.method === "POST" && c.url.endsWith("/api/invites"))?.body).toEqual({ role: "developer" });
    fireEvent.change(screen.getByLabelText("Role of dev@acme.uz"), { target: { value: "viewer" } });
    fireEvent.click(await screen.findByRole("button", { name: "Yes, make viewer" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ role: "viewer" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove dev@acme.uz" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/api/members/own_2"))).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Revoke" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, revoke" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url.endsWith("/api/invites/inv_1"))).toBe(true));
  });

  it("asks once more before Remove, Turn off and a change of your own role", async () => {
    const calls = fakeApi({
      "GET /api/me": { email: "me@acme.uz", role: "admin" },
      "GET /api/account/two-step": { on: true, required: false },
      "GET /api/members": [
        { id: "own_1", email: "me@acme.uz", role: "admin", two_step: true },
        { id: "own_3", email: "ops@acme.uz", role: "admin", two_step: true },
      ],
      "GET /api/invites": [],
      "PUT /api/members/own_1": null,
      "DELETE /api/members/own_1": null,
      "DELETE /api/members/own_3/two-step": null,
    });
    render(<Members />);
    await screen.findByRole("button", { name: "Turn off two-step sign in for ops@acme.uz" });
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.change(screen.getByLabelText("Role of me@acme.uz"), { target: { value: "viewer" } });
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove me@acme.uz" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Turn off two-step sign in for ops@acme.uz" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.change(screen.getByLabelText("Role of me@acme.uz"), { target: { value: "viewer" } });
    fireEvent.click(screen.getByRole("button", { name: "Yes, make me viewer" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "PUT", url: "/api/members/own_1", body: { role: "viewer" } }]));
  });

  it("says why when the API refuses (a Developer)", async () => {
    fakeApi({ "GET /api/members": () => fail(403, "Developers cannot manage members."), "GET /api/invites": () => fail(403, "Developers cannot manage members.") });
    render(<Members />);
    expect(await screen.findByText(/Developers cannot manage members\./)).toBeTruthy();
  });

  it("asks before it changes another member's role, revokes an invite or requires two-step for everyone", async () => {
    const calls = fakeApi({
      "GET /api/me": { email: "me@acme.uz", role: "admin" },
      "GET /api/account/two-step": { on: true, required: false },
      "GET /api/members": [{ id: "own_1", email: "me@acme.uz", role: "admin", two_step: true }, { id: "own_2", email: "dev@acme.uz", role: "developer", two_step: false }],
      "GET /api/invites": [{ id: "inv_1", role: "viewer", expires_at: Date.UTC(2026, 10, 4, 12, 0) }],
      "PUT /api/members/own_2": null,
      "DELETE /api/invites/inv_1": null,
      "PUT /api/settings/two-step": null,
    });
    render(<Members />);
    await screen.findByText("dev@acme.uz");
    const sent = () => calls.filter((c) => c.method !== "GET");
    const cases: [() => void, string, string, { method: string; url: string; body: unknown }][] = [
      [() => fireEvent.change(screen.getByLabelText("Role of dev@acme.uz"), { target: { value: "viewer" } }), "Make dev@acme.uz viewer?", "Yes, make viewer", { method: "PUT", url: "/api/members/own_2", body: { role: "viewer" } }],
      [() => fireEvent.click(screen.getByRole("button", { name: "Revoke" })), "Revoke this invite?", "Yes, revoke", { method: "DELETE", url: "/api/invites/inv_1", body: undefined }],
      [() => fireEvent.click(screen.getByRole("button", { name: "Require two-step sign in for everyone" })), "Require two-step sign in for everyone?", "Yes, require it", { method: "PUT", url: "/api/settings/two-step", body: { required: true } }],
    ];
    for (const [open, title, yes, req] of cases) {
      open();
      expect(await screen.findByRole("heading", { name: title })).toBeTruthy();
      expect(sent()).toHaveLength(0);
      fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
      await waitFor(() => expect(screen.queryByRole("heading", { name: title })).toBeNull());
      expect(sent()).toHaveLength(0);
      open();
      fireEvent.click(await screen.findByRole("button", { name: yes }));
      await waitFor(() => expect(sent()).toEqual([req]));
      calls.length = 0;
      await waitFor(() => expect(screen.queryByRole("heading", { name: title })).toBeNull());
    }
  });
});

describe("Members single invite", () => {
  it("sends one request for two clicks on Invite", async () => {
    const calls = fakeApi({ "GET /api/members": [], "GET /api/invites": [], "GET /api/me": { email: "me@acme.uz" }, "GET /api/account/two-step": { on: false, required: false }, "POST /api/invites": new Promise(() => {}) });
    render(<Members />);
    fireEvent.click(await screen.findByRole("button", { name: "Invite a member" }));
    const b = screen.getByRole("button", { name: "Invite" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });
});

describe("Members, the only admin", () => {
  it("shows the role as text, offers no Remove and says why", async () => {
    fakeApi({
      "GET /api/me": { email: "me@acme.uz", role: "admin" },
      "GET /api/members": [
        { id: "own_1", email: "me@acme.uz", role: "admin", two_step: false },
        { id: "own_2", email: "dev@acme.uz", role: "developer", two_step: false },
      ],
      "GET /api/invites": [],
    });
    render(<Members />);
    await screen.findByText("dev@acme.uz");
    expect(screen.queryByLabelText("Role of me@acme.uz")).toBeNull();
    expect(screen.queryByRole("button", { name: "Remove me@acme.uz" })).toBeNull();
    expect(screen.getByRole("button", { name: "Remove dev@acme.uz" })).toBeTruthy();
    expect(screen.getByText("You are the only admin. Invite another admin before you change your role or leave the team.")).toBeTruthy();
  });
});
