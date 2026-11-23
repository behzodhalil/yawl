import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fail, fakeApi } from "./test-api";
import { Palette } from "./palette";

describe("Palette", () => {
  it("says why a list did not load instead of looking forever", async () => {
    fakeApi({ "GET /api/projects": [], "GET /api/servers": () => fail(500, "servers are down") });
    render(<Palette pages={[]} onClose={() => {}} />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "zzz" } });
    expect(await screen.findByText("servers are down")).toBeTruthy();
    expect(screen.queryByText("Looking…")).toBeNull();
  });

  it("is a list autocomplete that announces how many places match", async () => {
    fakeApi({ "GET /api/projects": [], "GET /api/servers": [{ name: "hel1" }, { name: "fsn1" }] });
    render(<Palette pages={[{ to: "/", label: "Projects", icon: "layers" }]} onClose={() => {}} />);
    const box = screen.getByRole("combobox");
    expect(box.getAttribute("aria-autocomplete")).toBe("list");
    expect(await screen.findByText("3 results")).toBeTruthy();
    fireEvent.change(box, { target: { value: "hel" } });
    expect(screen.getByText("1 result").getAttribute("aria-live")).toBe("polite");
  });
});
