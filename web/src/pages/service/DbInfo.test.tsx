import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { DbInfo } from "./DbInfo";

const D = "/api/services/acme/production/acme-db";
const view = { form: { name: "acme-db", kind: "postgres", repo: "", branch: "", port: 0, health_path: "", health_timeout: 0, drain_timeout: 0, auto_deploy: true,
  release: [], release_timeout: 0, domains: [], version: 17, backup_at: "03:00", outside_port: 0, allow: [], schedule: "", timezone: "", command: [], timeout: 0 },
  health: "ok", server: "hel1", domains: [] };

describe("DbInfo", () => {
  it("shows the reference and asks for a port and an address before planning outside access", async () => {
    const calls = fakeApi({
      [`GET ${D}`]: view,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "acme-db", detail: "outside_port, allow; acme-db is open to every address." }] },
    });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    await screen.findByText("acme-db.url");
    expect(screen.getByText("Use it as a reference: DATABASE_URL → acme-db.url")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Allow connections from outside"));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    expect(screen.getByText("Give a port and at least one address.")).toBeTruthy();
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(0);
    fireEvent.change(screen.getByLabelText("Outside port"), { target: { value: "15432" } });
    fireEvent.change(screen.getByLabelText("Allowed addresses"), { target: { value: "0.0.0.0/0\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText(/is open to every address\./);
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ outside_port: 15432, allow: ["0.0.0.0/0"] });
  });

  it("a Valkey shows its reference for REDIS_URL and no outside access (design record 29, B8)", async () => {
    fakeApi({ [`GET /api/services/acme/production/cache`]: { ...view, form: { ...view.form, name: "cache", kind: "valkey", version: 8 } } });
    render(<DbInfo project="acme" env="production" service="cache" />);
    await screen.findByText("cache.url");
    expect(screen.getByText("Use it as a reference: REDIS_URL → cache.url")).toBeTruthy();
    expect(screen.queryByLabelText("Allow connections from outside")).toBeNull();
  });

  it("lists the apps that use it, other environments too, each a link (design record 20)", async () => {
    fakeApi({ [`GET ${D}`]: { ...view, used_by: [{ env: "production", service: "api" }, { env: "staging", service: "web" }] } });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect((await screen.findByRole("link", { name: "api" })).getAttribute("href")).toBe("/p/acme/production/s/api");
    expect(screen.getByRole("link", { name: "staging/web" }).getAttribute("href")).toBe("/p/acme/staging/s/web");
  });

  it("says when nothing uses it", async () => {
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    expect(await screen.findByText("No service references acme-db.url yet.")).toBeTruthy();
  });

  it("keeps 7 days of changes through the plan, with a button (design record 31)", async () => {
    const calls = fakeApi({
      [`GET ${D}`]: view,
      "POST /api/environments/acme/production/services?dry=1": { changes: [{ op: "update", kind: "service", name: "acme-db", detail: "pitr" }] },
      "POST /api/environments/acme/production/services": { changes: [] },
      [`POST ${D}/deploys`]: { deploy_id: "dep_3" },
    });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    fireEvent.click(await screen.findByRole("button", { name: "Keep 7 days of changes (restarts the database)" }));
    await screen.findByText(/acme-db.*pitr/);
    expect(calls.find((c) => c.method === "POST")!.body).toMatchObject({ pitr: true });
    fireEvent.click(screen.getByRole("button", { name: "Apply (restarts the database)" }));
    await waitFor(() => expect(calls.some((c) => c.url === `${D}/deploys`)).toBe(true));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/acme-db/deploys/dep_3"));
  });

  it("says Unsaved changes after editing outside access", async () => {
    fakeApi({ [`GET ${D}`]: view });
    render(<DbInfo project="acme" env="production" service="acme-db" />);
    fireEvent.click(await screen.findByLabelText("Allow connections from outside"));
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    fireEvent.click(screen.getByLabelText("Allow connections from outside"));
    expect(screen.queryByText("Unsaved changes")).toBeNull();
  });
});
