import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RecoveryCodes } from "./TwoStepSetup";

describe("RecoveryCodes", () => {
  it("Copy is the shared button and says Copied", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    render(<RecoveryCodes codes={["aaa", "bbb"]} onDone={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(await screen.findByRole("button", { name: "Copied" })).toBeTruthy();
    expect(writeText).toHaveBeenCalledWith("aaa\nbbb");
  });
});
