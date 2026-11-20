import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "../test-api";
import { Hetzner } from "./Hetzner";

const ready = { connected: true, locations: [{ name: "hel1", city: "Helsinki" }], types: [{ name: "cx22", cores: 2, memory: 4, disk: 40, prices: { hel1: "4.51" } }] };

describe("Hetzner", () => {
  it("two quick clicks on Create send one request, and the button says so", async () => {
    let release!: () => void;
    const calls = fakeApi({
      "GET /api/hetzner": ready,
      "POST /api/hetzner/servers": () => new Promise((r) => { release = () => r({ id: 1, ip: "203.0.113.5" }); }),
    });
    render(<Hetzner />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "fsn2" } });
    const b = screen.getByRole("button", { name: "Create the server" });
    fireEvent.click(b);
    fireEvent.click(b);
    await screen.findByRole("button", { name: "Creating…" });
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    release();
    await screen.findByText(/fsn2 is being made at 203.0.113.5/);
  });

  it("two quick clicks on Connect Hetzner send one request, and the button is disabled meanwhile", async () => {
    let release!: () => void;
    const calls = fakeApi({
      "GET /api/hetzner": { connected: false },
      "PUT /api/settings/hetzner": () => new Promise((r) => { release = () => r(null); }),
    });
    render(<Hetzner />);
    fireEvent.change(await screen.findByLabelText("Hetzner API token"), { target: { value: "tok" } });
    const b = screen.getByRole("button", { name: "Connect Hetzner" }) as HTMLButtonElement;
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(b.disabled).toBe(true));
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(1);
    release();
    await waitFor(() => expect((screen.getByLabelText("Hetzner API token") as HTMLInputElement).value).toBe(""));
    fireEvent.change(screen.getByLabelText("Hetzner API token"), { target: { value: "tok2" } });
    expect(b.disabled).toBe(false);
  });

  it("asks before disconnecting", async () => {
    const calls = fakeApi({ "GET /api/hetzner": ready, "DELETE /api/settings/hetzner": null });
    render(<Hetzner />);
    fireEvent.click(await screen.findByRole("button", { name: "Disconnect Hetzner" }));
    await screen.findByText("Disconnect Hetzner?");
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: "Yes, disconnect" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("shows why it could not load", async () => {
    fakeApi({ "GET /api/hetzner": fail(500, "hetzner is down") });
    render(<Hetzner />);
    await screen.findByText(/hetzner is down/);
  });
});
