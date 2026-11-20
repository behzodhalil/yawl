import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Shell } from "../../Shell";
import { Appearance } from "./Appearance";

const root = document.documentElement;
afterEach(() => {
  localStorage.clear();
  delete root.dataset.theme;
  delete root.dataset.accent;
});

describe("Appearance", () => {
  it("keeps the accent in this browser and marks the picked swatch", () => {
    render(<Appearance />);
    expect((screen.getByRole("radio", { name: "Lime" }) as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByRole("radio", { name: "Blue" }));
    expect(root.dataset.accent).toBe("blue");
    expect(localStorage.getItem("yawl.accent")).toBe("blue");
    expect((screen.getByRole("radio", { name: "Blue" }) as HTMLInputElement).checked).toBe(true);
  });

  it("System forgets the kept theme, and the top bar and the page agree", () => {
    render(<Shell crumb={["x"]} active="/settings"><Appearance /></Shell>);
    fireEvent.click(screen.getByRole("button", { name: "Dark" }));
    expect(root.dataset.theme).toBe("dark");
    fireEvent.click(screen.getByRole("button", { name: "Light theme" }));
    expect(screen.getByRole("button", { name: "Light" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "System" }));
    expect(root.dataset.theme).toBeUndefined();
    expect(localStorage.getItem("yawl.theme")).toBeNull();
  });

  it("applies a kept accent on load and ignores one it does not know", async () => {
    localStorage.setItem("yawl.accent", "violet");
    vi.resetModules();
    await import("../../Shell");
    expect(root.dataset.accent).toBe("violet");
    delete root.dataset.accent;
    localStorage.setItem("yawl.accent", "green");
    vi.resetModules();
    await import("../../Shell");
    expect(root.dataset.accent).toBeUndefined();
  });
});
