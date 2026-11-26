import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { ServerTabs } from "./ServerTabs";

describe("ServerTabs", () => {
  it("renders the shared tabs row with the active tab marked current", () => {
    render(<ServerTabs name="hel1" active="disk" />);
    expect(screen.getByRole("navigation", { name: "Server" })).toBeTruthy();
    expect(screen.getByRole("link", { name: "Disk" }).getAttribute("aria-current")).toBe("page");
    expect(screen.getByRole("link", { name: "Overview" }).hasAttribute("aria-current")).toBe(false);
  });
});
