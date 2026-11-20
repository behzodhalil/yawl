import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { fakeApi, fail } from "./test-api";
import { SessionContext } from "./session";
import { Shell } from "./Shell";

const shell = (managed: boolean) =>
  render(<SessionContext.Provider value={{ claimed: true, managed }}><Shell crumb={["Projects"]} active="/"><p>page</p></Shell></SessionContext.Provider>);

describe("billing line", () => {
  it("shows the trial's days left and Add a card", async () => {
    fakeApi({ "GET /api/billing": { state: "trialing", trial_ends: Date.now() + 9 * 864e5 - 1000, deploys: true, card_url: "https://cloud.test/billing" } });
    shell(true);
    expect(await screen.findByText("Trial: 9 days left.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Add a card" }).getAttribute("href")).toBe("https://cloud.test/billing");
  });
  it("says new deploys are paused when unpaid", async () => {
    fakeApi({ "GET /api/billing": { state: "ended", deploys: false, card_url: "https://cloud.test/billing" } });
    shell(true);
    expect(await screen.findByText("New deploys are paused until this panel's subscription is paid; running apps keep running.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Add a card" })).toBeTruthy();
  });
  it("asks to update the card when a payment failed", async () => {
    fakeApi({ "GET /api/billing": { state: "past_due", deploys: true, card_url: "https://cloud.test/billing" } });
    shell(true);
    expect(await screen.findByRole("link", { name: "Update the card" })).toBeTruthy();
  });
  it("shows nothing when paid, to non-admins, or when self-hosted", async () => {
    let calls = fakeApi({ "GET /api/billing": { state: "active", deploys: true, card_url: "https://cloud.test/billing" } });
    shell(true);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(/card/)).toBeNull();
    calls = fakeApi({ "GET /api/billing": () => fail(403, "Developers cannot see billing.") });
    shell(true);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText(/card|cannot/)).toBeNull();
    calls = fakeApi({});
    shell(false);
    await new Promise((r) => setTimeout(r, 50));
    expect(calls.some((c) => c.url === "/api/billing")).toBe(false);
  });
});
