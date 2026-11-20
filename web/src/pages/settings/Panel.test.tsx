import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Panel } from "./Panel";

describe("Panel", () => {
  it("shows the hosts and saves a domain, then its DNS advice", async () => {
    let saved = "";
    const calls = fakeApi({
      "GET /api/settings/panel": () => ({
        domain: saved,
        hosts: [
          { host: "panel.5-6-7-8.sslip.io", dns_state: "ok", cert_state: "issued" },
          ...(saved ? [{ host: saved, dns_state: "wrong", dns_detail: "panel.acme.uz has no A record yet. Set one: panel.acme.uz → 5.6.7.8.", cert_state: "pending" }] : []),
        ],
      }),
      "PUT /api/settings/panel": (b: unknown) => ((saved = (b as { domain: string }).domain), null),
    });
    render(<Panel />);
    await screen.findByText("panel.5-6-7-8.sslip.io");
    fireEvent.change(screen.getByLabelText("Your domain for the panel"), { target: { value: "panel.acme.uz" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await screen.findByText("panel.acme.uz has no A record yet. Set one: panel.acme.uz → 5.6.7.8.");
    await waitFor(() => expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ domain: "panel.acme.uz" }));
  });
});

describe("Panel saved", () => {
  it("says Saved.", async () => {
    fakeApi({ "GET /api/settings/panel": { domain: "", hosts: [] }, "PUT /api/settings/panel": null });
    render(<Panel />);
    fireEvent.click(await screen.findByRole("button", { name: "Save" }));
    expect(await screen.findByText("Saved.")).toBeTruthy();
  });
});
