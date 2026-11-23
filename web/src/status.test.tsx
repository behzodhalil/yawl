import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { StatusBadge, type BadgeKind } from "./ui";

describe("StatusBadge", () => {
  it("draws its icon before the word, in a chip of its kind", () => {
    const { container } = render(<StatusBadge kind="ok">live</StatusBadge>);
    const chip = container.querySelector(".badge.ok")!;
    expect(chip.textContent).toBe("Live");
    expect(chip.firstElementChild!.tagName.toLowerCase()).toBe("svg");
    expect(container.querySelector(".pill, .dot")).toBeNull();
  });
  it("each kind has its own icon", () => {
    const kinds: BadgeKind[] = ["ok", "busy", "bad", "warn", "none", "off"];
    const svgs = kinds.map((k) => render(<StatusBadge kind={k}>x</StatusBadge>).container.querySelector(`.badge.${k} svg`)!.innerHTML);
    expect(new Set(svgs).size).toBe(kinds.length);
  });
  it("says a state in sentence case, without underscores", () => {
    expect(render(<StatusBadge kind="bad">rolled_back</StatusBadge>).container.textContent).toBe("Rolled back");
  });
});
