import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { DeleteDialog, stamp } from "./DeleteDialog";

const purge = Date.UTC(2026, 9, 30, 12, 2);

describe("DeleteDialog", () => {
  it("asks for the name, names the users, and sends it", async () => {
    const calls = fakeApi({ "DELETE /api/services/acme/production/acme-db": null });
    const done = vi.fn();
    render(<DeleteDialog what="acme-db" path="/api/services/acme/production/acme-db" purgeAt={purge} users={["api", "web"]} usesRef="acme-db.url" onDone={done} onClose={() => {}} />);
    expect(screen.getByText("It stops now and is purged on 30 Oct 12:02 UTC unless restored.")).toBeTruthy();
    expect(screen.getByText("api, web use it through acme-db.url.")).toBeTruthy();
    const button = screen.getByRole("button", { name: "Delete acme-db" }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Type acme-db to delete it."), { target: { value: "acme-d" } });
    expect(button.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Type acme-db to delete it."), { target: { value: "acme-db" } });
    fireEvent.click(button);
    await waitFor(() => expect(done).toHaveBeenCalled());
    expect(calls[0].body).toEqual({ confirm: "acme-db" });
  });
  it("formats the purge time in UTC", () => {
    expect(stamp(purge)).toBe("30 Oct 12:02 UTC");
  });
});
