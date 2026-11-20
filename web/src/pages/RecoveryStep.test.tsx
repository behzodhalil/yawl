import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { RecoveryStep } from "./RecoveryStep";
import { backupKey } from "../seal";

describe("RecoveryStep", () => {
  it("shows the key once, waits for the box, and sends only the public key and the backup signing key", async () => {
    const f = vi.fn(async (_u: string, _i?: RequestInit) => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", f);
    const done = vi.fn();
    render(<RecoveryStep onDone={done} />);
    const key = await screen.findByText(/^yawl-recovery-[A-Z2-7]{52}$/);
    const cont = screen.getByRole("button", { name: "Continue" }) as HTMLButtonElement;
    expect(cont.disabled).toBe(true);
    fireEvent.click(screen.getByLabelText("I saved the recovery key somewhere other than this server"));
    expect(cont.disabled).toBe(false);
    fireEvent.click(cont);
    await waitFor(() => expect(done).toHaveBeenCalled());
    const [url, init] = f.mock.calls[0];
    expect(url).toBe("/api/recovery-key");
    const body = JSON.parse(String(init!.body));
    expect(Object.keys(body)).toEqual(["pubkey", "backup_key"]);
    expect(body.backup_key).toBe(await backupKey(key.textContent!));
    expect(String(init!.body)).not.toContain(key.textContent!.slice(14, 30));
  });
});
