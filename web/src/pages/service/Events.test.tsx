import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Events } from "./Events";
import { S } from "./fixtures";

const now = Date.now();
const rows = [
  { id: "dep_4", state: "live", live: true, can_roll_back: false, commit: "c03d9e5aa", subject: "Fix invoice rounding", by: "dilnoza", took_ms: 134_000, rollback: false, created_at: now },
  { id: "dep_3", state: "failed", live: false, can_roll_back: false, commit: "4be81c0bb", subject: "Add PDF export", by: "owner", took_ms: 92_000, cause: "health check failed", rollback: false, created_at: now },
  { id: "dep_2", state: "superseded", live: false, can_roll_back: false, commit: "9f2c41ecc", by: "dilnoza", rollback: false, created_at: now },
  { id: "dep_1", state: "live", live: false, can_roll_back: true, commit: "7a1d2e0dd", subject: "Show totals", by: "dilnoza", took_ms: 122_000, rollback: false, created_at: now },
];

describe("Events", () => {
  it("leads each deploy with its commit and says who, how long and its state", async () => {
    fakeApi({ [`GET ${S}/deploys`]: rows });
    render(<Events project="acme" env="production" service="api" />);
    const live = (await screen.findByText("Fix invoice rounding")).closest("tr")!;
    expect(live.textContent).toContain("c03d9e5");
    expect(live.textContent).toContain("dilnoza");
    expect(live.textContent).toContain("2m 14s");
    expect(within(live).getByText("Deployed").className).toContain("ok");
    expect(within(screen.getByText("Add PDF export").closest("tr")!).getByText("Failed").className).toContain("bad");
    expect(screen.queryByText("health check failed")).toBeNull();
    expect(screen.getByText("9f2c41e").closest("tr")!.textContent).toContain("Skipped");
    expect(screen.queryByText("dep_4")).toBeNull();
    expect(screen.getAllByRole("button", { name: "Roll back" })).toHaveLength(1);
  });

  it("rolls back to an earlier version after asking", async () => {
    fakeApi({ [`GET ${S}/deploys`]: rows, [`POST ${S}/rollback`]: { deploy_id: "dep_5" } });
    render(<Events project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Roll back" }));
    expect(screen.getByText("The database is not touched. Migrations that ran stay in place.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Roll back to 7a1d2e0" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_5"));
  });

  it("sends a double-clicked rollback once", async () => {
    const calls = fakeApi({ [`GET ${S}/deploys`]: rows, [`POST ${S}/rollback`]: { deploy_id: "dep_5" } });
    render(<Events project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Roll back" }));
    const b = screen.getByRole("button", { name: "Roll back to 7a1d2e0" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_5"));
    expect(calls.filter((c) => c.url.endsWith("/rollback"))).toHaveLength(1);
  });

  it("shows the rollback's note before following it (yawl-5)", async () => {
    const note = "The database is unchanged: dep_4 ran a release command. Its snapshot is kept on hel1 at /var/lib/yawl/snap.sql.";
    fakeApi({ [`GET ${S}/deploys`]: rows, [`POST ${S}/rollback`]: { deploy_id: "dep_6", to: "dep_1", note } });
    history.pushState(null, "", "/p/acme/production/s/api/events");
    render(<Events project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Roll back" }));
    fireEvent.click(screen.getByRole("button", { name: "Roll back to 7a1d2e0" }));
    expect(await screen.findByText(note)).toBeTruthy();
    expect(location.pathname).toBe("/p/acme/production/s/api/events");
    fireEvent.click(screen.getByRole("button", { name: "Follow the rollback" }));
    expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_6");
  });

  it("drops a rollback's error with the dialog", async () => {
    fakeApi({ [`GET ${S}/deploys`]: rows, [`POST ${S}/rollback`]: fail(409, "a deploy is running") });
    render(<Events project="acme" env="production" service="api" />);
    fireEvent.click(await screen.findByRole("button", { name: "Roll back" }));
    fireEvent.click(screen.getByRole("button", { name: "Roll back to 7a1d2e0" }));
    await screen.findByText("a deploy is running");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(screen.queryByText("a deploy is running")).toBeNull();
  });

  it("names a deploy with no commit by its digest, then subject, then id", async () => {
    fakeApi({ [`GET ${S}/deploys`]: [
      { id: "dep_9", state: "live", live: true, can_roll_back: false, rollback: false, created_at: now },
      { id: "dep_8", state: "live", live: false, can_roll_back: true, subject: "grafana/grafana:11", digest: "sha256:0123456789abcdef", rollback: false, created_at: now },
      { id: "dep_7", state: "live", live: false, can_roll_back: true, subject: "Adopted", rollback: false, created_at: now },
      { id: "dep_6", state: "live", live: false, can_roll_back: true, rollback: false, created_at: now },
    ] });
    render(<Events project="acme" env="production" service="api" />);
    const backs = await screen.findAllByRole("button", { name: "Roll back" });
    fireEvent.click(backs[0]);
    expect(screen.getByRole("button", { name: "Roll back to 0123456789ab" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(backs[1]);
    expect(screen.getByRole("dialog", { name: "Roll back api to Adopted?" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(backs[2]);
    expect(screen.getByRole("button", { name: "Roll back to dep_6" })).toBeTruthy();
  });

  it("shows an image deploy's digest", async () => {
    fakeApi({ [`GET ${S}/deploys`]: [{ id: "dep_9", state: "live", live: true, can_roll_back: false, subject: "grafana/grafana:11",
      digest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", rollback: false, created_at: now }] });
    render(<Events project="acme" env="production" service="api" />);
    const row = (await screen.findByText("grafana/grafana:11")).closest("tr")!;
    expect(row.textContent).toContain("0123456789ab");
    expect(row.textContent).not.toContain("sha256");
  });
});
