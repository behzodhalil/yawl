import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "../test-api";
import { Reset } from "./Reset";

function fill(a: string, b: string) {
  fireEvent.change(screen.getByLabelText("New password"), { target: { value: a } });
  fireEvent.change(screen.getByLabelText("New password again"), { target: { value: b } });
  fireEvent.click(screen.getByRole("button", { name: "Set password" }));
}

describe("Reset (yawl server reset-password)", () => {
  it("sets the new password with the link's token, then goes to sign in", async () => {
    location.hash = "#tok123";
    const calls = fakeApi({ "POST /api/password-reset": { email: "me@acme.uz" } });
    let done = false;
    render(<Reset onDone={() => (done = true)} />);
    fill("a new long password", "a new long password");
    await waitFor(() => expect(done).toBe(true));
    expect(calls[0].body).toEqual({ token: "tok123", password: "a new long password" });
    expect(location.hash).toBe("");
  });

  it("is the styled centered card, asks 12 characters and sends once on a double click", async () => {
    location.hash = "#tok123";
    const calls = fakeApi({ "POST /api/password-reset": { email: "me@acme.uz" } });
    render(<Reset onDone={() => {}} />);
    expect(screen.getByLabelText("New password").closest(".centered")).toBeTruthy();
    expect((screen.getByLabelText("New password") as HTMLInputElement).minLength).toBe(12);
    expect((screen.getByLabelText("New password again") as HTMLInputElement).minLength).toBe(12);
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "a new long password" } });
    fireEvent.change(screen.getByLabelText("New password again"), { target: { value: "a new long password" } });
    const b = screen.getByRole("button", { name: "Set password" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });

  it("asks for the same password twice", () => {
    location.hash = "#tok123";
    const calls = fakeApi({});
    render(<Reset onDone={() => {}} />);
    fill("a new long password", "another password");
    expect(screen.getByText("The two passwords differ.")).toBeTruthy();
    expect(calls).toHaveLength(0);
  });

  it("says a spent link is spent", async () => {
    location.hash = "#old";
    fakeApi({ "POST /api/password-reset": () => fail(400, "This reset link has expired or was already used.") });
    render(<Reset onDone={() => {}} />);
    fill("a new long password", "a new long password");
    expect(await screen.findByText("This reset link has expired or was already used.")).toBeTruthy();
  });
});
