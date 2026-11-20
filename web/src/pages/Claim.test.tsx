import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { Claim } from "./Claim";

function fill(label: string, value: string) {
  fireEvent.change(screen.getByLabelText(label), { target: { value } });
}

describe("Claim", () => {
  beforeEach(() => history.replaceState(null, "", "/claim"));

  it("asks for the link when the fragment has no token", () => {
    render(<Claim onDone={() => {}} />);
    expect(screen.getByText("Open the claim link that yawl server printed.")).toBeTruthy();
    expect(screen.queryByLabelText("Email")).toBeNull();
  });

  it("refuses a mismatched confirmation without a request", () => {
    history.replaceState(null, "", "/claim#tok123");
    const f = vi.fn();
    vi.stubGlobal("fetch", f);
    render(<Claim onDone={() => {}} />);
    fill("Email", "me@acme.uz");
    fill("Password", "correct horse battery");
    fill("Password again", "correct horse batter");
    fireEvent.click(screen.getByRole("button", { name: "Claim this panel" }));
    expect(screen.getByText("The two passwords differ.")).toBeTruthy();
    expect(f).not.toHaveBeenCalled();
  });

  it("posts the token from the fragment and shows the server's refusal", async () => {
    history.replaceState(null, "", "/claim#tok123");
    const f = vi.fn(async () => new Response("That claim link is not valid or has expired. Run yawl server claim-link for a new one.", { status: 400 }));
    vi.stubGlobal("fetch", f);
    render(<Claim onDone={() => {}} />);
    fill("Email", "me@acme.uz");
    fill("Password", "correct horse battery");
    fill("Password again", "correct horse battery");
    fireEvent.click(screen.getByRole("button", { name: "Claim this panel" }));
    await waitFor(() => screen.getByText(/claim link is not valid/));
    const [url, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe("/api/claim");
    expect(JSON.parse(init.body as string)).toEqual({ token: "tok123", email: "me@acme.uz", password: "correct horse battery" });
  });
});

describe("Claim then recovery", () => {
  it("shows the recovery step when no key exists", async () => {
    history.replaceState(null, "", "/claim#tok123");
    vi.stubGlobal("fetch", vi.fn(async (url: string) =>
      url === "/api/claim" ? new Response(`{"claimed":true,"email":"me@acme.uz"}`) : new Response(`{"set":false}`)));
    const done = vi.fn();
    render(<Claim onDone={done} />);
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "me@acme.uz" } });
    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "correct horse battery" } });
    fireEvent.change(screen.getByLabelText("Password again"), { target: { value: "correct horse battery" } });
    fireEvent.click(screen.getByRole("button", { name: "Claim this panel" }));
    await screen.findByText("Your recovery key");
    expect(done).not.toHaveBeenCalled();
    expect(location.hash).toBe("");
  });
});
