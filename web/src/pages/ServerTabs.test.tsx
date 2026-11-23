import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { ServerTabs } from "./ServerTabs";

describe("ServerTabs", () => {
  it("renders the shared tabs row with the active tab marked on", () => {
    const { container } = render(<ServerTabs name="hel1" active="disk" />);
    expect(container.firstElementChild?.classList.contains("tabs")).toBe(true);
    expect(screen.getByRole("link", { name: "Disk" }).className).toBe("on");
    expect(screen.getByRole("link", { name: "Overview" }).className).toBe("");
  });
});
