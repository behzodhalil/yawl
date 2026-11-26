import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Shell, pickTheme } from "../../Shell";
import { Appearance } from "./Appearance";

const root = document.documentElement;
afterEach(() => {
  pickTheme("system");
  localStorage.clear();
});

describe("Appearance", () => {
  it("System forgets the kept theme, and the top bar and the page agree", () => {
    render(<Shell crumb={["x"]} active="/settings"><Appearance /></Shell>);
    fireEvent.click(screen.getByRole("tab", { name: "Dark" }));
    expect(root.dataset.mode).toBe("dark");
    fireEvent.click(screen.getByRole("button", { name: "Light theme" }));
    expect(screen.getByRole("tab", { name: "Light" }).getAttribute("aria-selected")).toBe("true");
    fireEvent.click(screen.getByRole("tab", { name: "System" }));
    expect(localStorage.getItem("yawl.theme")).toBeNull();
    expect(root.dataset.mode).toBe("dark");
  });

  it("applies a kept theme on load", async () => {
    localStorage.setItem("yawl.theme", "light");
    vi.resetModules();
    await import("../../Shell");
    expect(root.dataset.mode).toBe("light");
  });
});
