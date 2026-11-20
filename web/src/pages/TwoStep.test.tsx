import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "../test-api";
import { SignIn } from "./SignIn";
import { Account } from "./settings/Account";
import { Members } from "./settings/Members";

const qr = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 29 29"></svg>';
const codes = ["AAAAAAAAAA", "BBBBBBBBBB", "CCCCCCCCCC", "DDDDDDDDDD", "EEEEEEEEEE", "FFFFFFFFFF", "GGGGGGGGGG", "HHHHHHHHHH", "IIIIIIIIII", "JJJJJJJJJJ"];

function password() {
  fireEvent.change(screen.getByLabelText("Email"), { target: { value: "me@acme.uz" } });
  fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct horse battery" } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("two-step sign in (design record 27)", () => {
  it("asks for the code after the password, and takes a recovery code too", async () => {
    let tries = 0;
    const calls = fakeApi({
      "POST /api/session": { second: "code", ticket: "tk1" },
      "POST /api/session/code": () => (++tries === 1 ? fail(401, "Wrong code.") : { claimed: true, email: "me@acme.uz" }),
    });
    const done = vi.fn();
    render(<SignIn onDone={done} />);
    password();
    expect(await screen.findByText("Enter the code from your authenticator app.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Code"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("Wrong code.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Use a recovery code" }));
    fireEvent.change(screen.getByLabelText("Recovery code"), { target: { value: "AAAAAAAAAA" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await waitFor(() => expect(done).toHaveBeenCalledWith("me@acme.uz"));
    expect(calls.filter((c) => c.url === "/api/session/code").map((c) => c.body)).toEqual([
      { ticket: "tk1", code: "123456" }, { ticket: "tk1", code: "AAAAAAAAAA" }]);
  });

  it("sets two-step up when it is required, and goes on only after the codes are saved", async () => {
    const calls = fakeApi({
      "POST /api/session": { second: "setup", ticket: "tk2" },
      "POST /api/session/setup": { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/x", qr },
      "POST /api/session/setup/confirm": { claimed: true, email: "me@acme.uz", recovery_codes: codes },
    });
    const done = vi.fn();
    render(<SignIn onDone={done} />);
    password();
    expect(await screen.findByText("JBSWY3DPEHPK3PXP")).toBeTruthy();
    expect(screen.getByRole("img", { name: "QR code for your authenticator app" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Code"), { target: { value: "654321" } });
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    expect(await screen.findByText("AAAAAAAAAA")).toBeTruthy();
    expect(done).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "I saved them" }));
    expect(done).toHaveBeenCalledWith("me@acme.uz");
    expect(calls.find((c) => c.url === "/api/session/setup/confirm")?.body).toEqual({ ticket: "tk2", code: "654321" });
  });

  it("confirms once on a double click (plan 25 minor)", async () => {
    let answer!: (v: unknown) => void;
    const calls = fakeApi({
      "POST /api/session": { second: "setup", ticket: "tk2" },
      "POST /api/session/setup": { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/x", qr },
      "POST /api/session/setup/confirm": () => new Promise((r) => (answer = r)),
    });
    render(<SignIn onDone={vi.fn()} />);
    password();
    fireEvent.change(await screen.findByLabelText("Code"), { target: { value: "654321" } });
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    await waitFor(() => expect(answer).toBeTypeOf("function"));
    expect((screen.getByRole("button", { name: "Turn on" }) as HTMLButtonElement).disabled).toBe(true);
    answer({ claimed: true, email: "me@acme.uz", recovery_codes: codes });
    expect(await screen.findByText("AAAAAAAAAA")).toBeTruthy();
    expect(calls.filter((c) => c.url === "/api/session/setup/confirm")).toHaveLength(1);
  });

  it("Account turns it on, then shows the codes left, new codes and turn off", async () => {
    let on = false;
    const calls = fakeApi({
      "GET /api/session": { email: "me@acme.uz" },
      "GET /api/account/two-step": () => ({ on, codes_left: on ? 10 : 0, required: false }),
      "POST /api/account/two-step": { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/x", qr },
      "PUT /api/account/two-step": () => ((on = true), { recovery_codes: codes }),
      "POST /api/account/two-step/codes": { recovery_codes: codes },
      "DELETE /api/account/two-step": null,
    });
    render(<Account />);
    fireEvent.click(await screen.findByRole("button", { name: "Turn on" }));
    fireEvent.change(await screen.findByLabelText("Code"), { target: { value: "111111" } });
    fireEvent.click(screen.getByRole("button", { name: "Turn on" }));
    fireEvent.click(await screen.findByRole("button", { name: "I saved them" }));
    expect(await screen.findByText("On. 10 recovery codes left.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "New recovery codes" }));
    fireEvent.change(screen.getByLabelText("Code"), { target: { value: "222222" } });
    fireEvent.click(screen.getByRole("button", { name: "Make new codes" }));
    expect(await screen.findByText("AAAAAAAAAA")).toBeTruthy();
    expect(calls.find((c) => c.url === "/api/account/two-step/codes")?.body).toEqual({ code: "222222" });
  });

  it("Account moves to a new phone with a code from the old one, also while required (review I3)", async () => {
    const calls = fakeApi({
      "GET /api/session": { email: "me@acme.uz" },
      "GET /api/account/two-step": { on: true, codes_left: 9, required: true },
      "POST /api/account/two-step": { secret: "NEWSECRETNEWSECR", uri: "otpauth://totp/x", qr },
      "PUT /api/account/two-step": { recovery_codes: codes },
    });
    render(<Account />);
    expect(await screen.findByText("On. 9 recovery codes left.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Turn off" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Move to a new phone" }));
    fireEvent.change(screen.getByLabelText("Code"), { target: { value: "AAAAAAAAAA" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(await screen.findByText("NEWSECRETNEWSECR")).toBeTruthy();
    expect(calls.find((c) => c.method === "POST" && c.url === "/api/account/two-step")?.body).toEqual({ code: "AAAAAAAAAA" });
  });

  it("Members shows who has it, turns it off for another, and makes it required", async () => {
    const calls = fakeApi({
      "GET /api/me": { email: "me@acme.uz", role: "admin" },
      "GET /api/account/two-step": { on: true, codes_left: 10, required: false },
      "GET /api/members": [
        { id: "own_1", email: "me@acme.uz", role: "admin", two_step: true },
        { id: "own_2", email: "dev@acme.uz", role: "developer", two_step: false },
        { id: "own_3", email: "ops@acme.uz", role: "admin", two_step: true },
      ],
      "GET /api/invites": [],
      "DELETE /api/members/own_3/two-step": null,
      "PUT /api/settings/two-step": { required: true },
    });
    render(<Members />);
    expect(await screen.findByText("ops@acme.uz")).toBeTruthy();
    expect(screen.getAllByText("On").length).toBe(2);
    expect(screen.getByText("Off")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Turn off two-step sign in for me@acme.uz" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Turn off two-step sign in for ops@acme.uz" }));
    fireEvent.click(screen.getByRole("button", { name: "Yes, turn off" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE" && c.url === "/api/members/own_3/two-step")).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Require two-step sign in for everyone" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, require it" }));    await waitFor(() => expect(calls.find((c) => c.method === "PUT" && c.url === "/api/settings/two-step")?.body).toEqual({ required: true }));
  });
});
