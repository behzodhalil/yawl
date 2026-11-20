import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { App } from "./App";

describe("App", () => {
  it("sends a reset to sign in saying the password changed", async () => {
    window.history.pushState({}, "", "/reset#tok123");
    fakeApi({ "GET /api/session": { claimed: true }, "GET /api/session/sso": {}, "POST /api/password-reset": { email: "me@acme.uz" } });
    render(<App />);
    fireEvent.change(await screen.findByLabelText("New password"), { target: { value: "a new long password" } });
    fireEvent.change(screen.getByLabelText("New password again"), { target: { value: "a new long password" } });
    fireEvent.click(screen.getByRole("button", { name: "Set password" }));
    await waitFor(() => expect(location.pathname + location.search).toBe("/sign-in?reset=1"));
    expect(await screen.findByText("Password changed. Sign in with the new one.")).toBeTruthy();
    window.history.pushState({}, "", "/");
  });
});
