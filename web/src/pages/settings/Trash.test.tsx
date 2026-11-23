import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Trash } from "./Trash";

process.env.TZ = "Asia/Tashkent";

const item = { kind: "service", id: "svc_1", label: "acme/production/api", deleted_by: "API token ci", deleted_at: Date.UTC(2026, 9, 28, 12, 2), purge_at: Date.UTC(2099, 9, 30, 12, 2) };

describe("Trash", () => {
  it("lists what waits, and restores", async () => {
    const calls = fakeApi({ "GET /api/trash": [item, { ...item, id: "svc_2", label: "acme/production/web", waiting: "waiting for hel1 to be online to remove its containers" }], "POST /api/trash/service/svc_1/restore": null });
    render(<Trash />);
    expect(await screen.findByText("acme/production/api")).toBeTruthy();
    expect(screen.getAllByText("Deleted by API token ci on 28 Oct 2026 17:02; purged on 30 Oct 2099 17:02.").length).toBe(2);
    expect(screen.getByText("waiting for hel1 to be online to remove its containers")).toBeTruthy();
    fireEvent.click(screen.getAllByRole("button", { name: "Restore" })[0]);
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/svc_1/restore"))).toBe(true));
  });
  it("says when it is empty", async () => {
    fakeApi({ "GET /api/trash": [] });
    render(<Trash />);
    expect(await screen.findByText("Nothing is in the trash.")).toBeTruthy();
  });
});
