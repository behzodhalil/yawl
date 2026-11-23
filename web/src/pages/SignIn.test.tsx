import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { SignIn } from "./SignIn";
import { fakeApi } from "../test-api";

describe("SignIn", () => {
  it("asks for the two-step code after SSO", async () => {
    window.history.pushState({}, "", "/sign-in?sso=code#t=tick-1");
    fakeApi({ "GET /api/session/sso": { name: "Acme Google" } });
    render(<SignIn onDone={() => {}} />);
    expect(await screen.findByLabelText(/code/i)).toBeTruthy();
    window.history.pushState({}, "", "/");
  });

  it("says the password was changed after a reset", async () => {
    window.history.pushState({}, "", "/sign-in?reset=1");
    fakeApi({ "GET /api/session/sso": {} });
    render(<SignIn onDone={() => {}} />);
    expect(await screen.findByText("Password changed. Sign in with the new one.")).toBeTruthy();
    window.history.pushState({}, "", "/");
  });

  it("sends the code once on a double click", async () => {
    window.history.pushState({}, "", "/sign-in?sso=code#t=tick-1");
    const calls = fakeApi({ "GET /api/session/sso": {}, "POST /api/session/code": { email: "a@acme.uz" } });
    render(<SignIn onDone={() => {}} />);
    fireEvent.change(await screen.findByLabelText("Code"), { target: { value: "123456" } });
    const b = screen.getByRole("button", { name: "Continue" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect((b as HTMLButtonElement).disabled).toBe(false));
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    window.history.pushState({}, "", "/");
  });

  it("goes back to the password from an expired SSO code step, dropping the ticket", async () => {
    window.history.pushState({}, "", "/sign-in?sso=code#t=tick-1");
    fakeApi({ "GET /api/session/sso": { name: "Acme Google" }, "POST /api/session/code": () => new Response("That sign-in expired. Start again.", { status: 401 }) });
    render(<SignIn onDone={() => {}} />);
    fireEvent.change(await screen.findByLabelText("Code"), { target: { value: "123456" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByText("That sign-in expired. Start again.");
    fireEvent.click(screen.getByRole("button", { name: "Start again" }));
    expect(screen.getByLabelText("Password")).toBeTruthy();
    expect(location.search + location.hash).toBe("");
    window.history.pushState({}, "", "/");
  });

  it("offers SSO and says why it did not sign in", async () => {
    window.history.pushState({}, "", "/sign-in?sso=unknown");
    fakeApi({ "GET /api/session/sso": { name: "Acme Google", only: true } });
    render(<SignIn onDone={() => {}} />);
    const link = await screen.findByRole("link", { name: "Sign in with Acme Google" });
    expect(link.getAttribute("href")).toBe("/auth/sso/start");
    expect(screen.getByText("That account is not a member of this team. Ask an Admin for an invite.")).toBeTruthy();
    expect(screen.getByText("Password sign-in is only for the oldest Admin.")).toBeTruthy();
    window.history.pushState({}, "", "/");
  });

  it("shows the server's text on 429", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("Too many attempts. Try again in a few minutes.", { status: 429 })));
    render(<SignIn onDone={() => {}} />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "me@acme.uz" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "x" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => screen.getByText("Too many attempts. Try again in a few minutes."));
  });

  it("calls onDone with the email", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(`{"claimed":true,"email":"me@acme.uz"}`, { status: 200 })));
    const done = vi.fn();
    render(<SignIn onDone={done} />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "me@acme.uz" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct horse battery" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(done).toHaveBeenCalledWith("me@acme.uz"));
  });
});
