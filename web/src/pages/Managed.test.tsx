import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { SessionContext } from "../session";
import { Servers } from "./Servers";
import { Group } from "./Group";
import { AddServer } from "./AddServer";

const managed = (ui: React.ReactNode) => render(<SessionContext.Provider value={{ claimed: true, managed: true }}>{ui}</SessionContext.Provider>);

describe("managed cell", () => {
  it("starts with Add your first server and the join command", async () => {
    fakeApi({ "GET /api/servers": [], "POST /api/join-token": { command: "curl -fsSL https://cloud.test/install.sh | YAWL_JOIN='https://acme.cloud.test TOK' sh" } });
    managed(<Servers />);
    expect(await screen.findByRole("heading", { name: "Add your first server" })).toBeTruthy();
    expect(screen.getByText("A server needs Ubuntu 22.04 to 26.04 or Debian 12, ports 80 and 443 open, and 1 GB of memory.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Make the join command" }));
    await screen.findByText("curl -fsSL https://cloud.test/install.sh | YAWL_JOIN='https://acme.cloud.test TOK' sh");
  });
  it("has no self-watch warning with one server", async () => {
    fakeApi({ "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "1.2.3.4", disk_used_pct: 5 }] });
    managed(<Servers />);
    await screen.findByText("hel1");
    expect(screen.queryByText(/This machine watches itself/)).toBeNull();
  });
  it("keeps only the settings the customer owns", () => {
    fakeApi({ "GET /api/settings/storage": { set: false } });
    managed(<Group group="settings" section="" />);
    expect(screen.getAllByRole("link").filter((a) => a.closest(".tabs")).map((a) => a.textContent)).toEqual(["Backup storage", "Recovery key", "Appearance"]);
  });
  it("offers no Hetzner card", async () => {
    const calls = fakeApi({ "GET /api/hetzner": { connected: false } });
    managed(<AddServer />);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByText("Create on Hetzner")).toBeNull();
    expect(calls.some((c) => c.url === "/api/hetzner")).toBe(false);
  });
});
