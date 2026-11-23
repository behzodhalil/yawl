import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { App } from "./App";
import { nearestPage } from "./NotFound";

const visit = (path: string) => {
  window.history.pushState({}, "", path);
  fakeApi({ "GET /api/session": { claimed: true, email: "me@acme.uz" }, "GET /api/incidents": [], "GET /api/servers": [] });
  render(<App />);
};

describe("NotFound", () => {
  it("offers a way home and opens search", async () => {
    visit("/nowhere");
    await screen.findByText("Page not found");
    expect(screen.getByRole("link", { name: "Go to Projects" }).getAttribute("href")).toBe("/");
    expect(screen.queryByText(/Did you mean/)).toBeNull();
    fireEvent.click(screen.getAllByRole("button", { name: "Search" }).find((b) => b.classList.contains("btn"))!);
    expect(await screen.findByRole("combobox", { name: "Go to" })).toBeTruthy();
    window.history.pushState({}, "", "/");
  });

  it("links the server overview for an unknown server sub-path", async () => {
    visit("/servers/hel1/updates");
    expect((await screen.findByRole("link", { name: "Server overview" })).getAttribute("href")).toBe("/servers/hel1");
    window.history.pushState({}, "", "/");
  });

  it("finds the closest real page", () => {
    expect(nearestPage("/p/acme/staging/zzz/yyy")).toEqual(["/p/acme/staging", "acme / staging"]);
    expect(nearestPage("/p/acme")).toBeNull();
    expect(nearestPage("/other/a")).toBeNull();
  });
});
