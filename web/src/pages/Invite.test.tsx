import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Invite } from "./Invite";

describe("Invite (design record 22, T4)", () => {
  it("signs the invitee up with the link's token", async () => {
    location.hash = "#tok123";
    const calls = fakeApi({ "POST /api/invites/accept": { joined: true } });
    let done = "";
    render(<Invite onDone={(e) => (done = e)} />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "dev@acme.uz" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct horse battery" } });
    fireEvent.change(screen.getByLabelText("Password again"), { target: { value: "correct horse battery" } });
    fireEvent.click(screen.getByRole("button", { name: "Join" }));
    await waitFor(() => expect(done).toBe("dev@acme.uz"));
    expect(calls[0].body).toEqual({ token: "tok123", email: "dev@acme.uz", password: "correct horse battery" });
  });

  it("sets two-step up before signing in when the panel requires it (design record 27)", async () => {
    location.hash = "#tok456";
    fakeApi({
      "POST /api/invites/accept": { second: "setup", ticket: "tk9" },
      "POST /api/session/setup": { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/x", qr: "<svg></svg>" },
    });
    let done = "";
    render(<Invite onDone={(e) => (done = e)} />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "dev@acme.uz" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct horse battery" } });
    fireEvent.change(screen.getByLabelText("Password again"), { target: { value: "correct horse battery" } });
    fireEvent.click(screen.getByRole("button", { name: "Join" }));
    expect(await screen.findByText("JBSWY3DPEHPK3PXP")).toBeTruthy();
    expect(done).toBe("");
  });

  it("is the styled centered card, asks 12 characters and sends once on a double click", async () => {
    location.hash = "#tok123";
    const calls = fakeApi({ "POST /api/invites/accept": { joined: true } });
    render(<Invite onDone={() => {}} />);
    expect(screen.getByLabelText("Email").closest(".centered")).toBeTruthy();
    expect((screen.getByLabelText("Password") as HTMLInputElement).minLength).toBe(12);
    expect((screen.getByLabelText("Password again") as HTMLInputElement).minLength).toBe(12);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "dev@acme.uz" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct horse battery" } });
    fireEvent.change(screen.getByLabelText("Password again"), { target: { value: "correct horse battery" } });
    const b = screen.getByRole("button", { name: "Join" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });

  it("says a join signs out whoever is signed in here (review M6, plan 19)", () => {
    location.hash = "#tok789";
    render(<Invite signedIn="owner@acme.uz" onDone={() => {}} />);
    expect(screen.getByText("You are signed in as owner@acme.uz; joining signs you out.")).toBeTruthy();
  });
});
