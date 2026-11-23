import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Disk } from "./Disk";

const S = "/api/servers/hel1";

describe("Disk", () => {
  it("shows the last cleanup, cleans up now, and removes an unused volume after a confirm", async () => {
    const calls = fakeApi({
      [`GET ${S}/cleanup`]: { at: Date.now() - 3_600_000, why: "nightly", containers: 3, images: 2, freed: 5_000_000_000 },
      [`POST ${S}/cleanup`]: { at: Date.now(), why: "by hand", freed: 1_000_000 },
      [`GET ${S}/volumes/unused`]: ["yawl-old-gone-data"],
      [`DELETE ${S}/volumes/yawl-old-gone-data`]: null,
    });
    render(<Disk name="hel1" />);
    await screen.findByText(/Freed 5 GB: 3 containers, 2 images/);
    fireEvent.click(screen.getByRole("button", { name: "Clean up now" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    fireEvent.click(await screen.findByRole("button", { name: "Remove yawl-old-gone-data" }));
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Remove the volume" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });
});
