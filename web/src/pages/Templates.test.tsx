import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../test-api";
import * as seal from "../seal";
import { Templates } from "./Templates";

const E = "/api/environments/acme/production";
const svg = '<svg viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>';
const cards = [{ id: "umami", title: "Umami", line: "Website analytics without cookies.", memory: "1GB", icons: [svg] },
  { id: "grafana-loki", title: "Grafana and Loki", line: "Dashboards.", memory: "1GB", icons: [svg, svg] }];
const umami = { ...cards[0], note: "Log in as admin.", services: [{ name: "umami-db", kind: "postgres" }, { name: "umami", kind: "web" }],
  vars: [{ service: "umami", key: "DATABASE_URL", kind: "ref", value: "umami-db.url" }, { service: "umami", key: "APP_SECRET", kind: "generate" },
    { service: "umami", key: "ADMIN_EMAIL", kind: "ask", label: "Admin email" }] };
const keys = { agent_pubkey: "a", recovery_pubkey: "r", server: "hel1" };
const plan = { changes: [{ op: "create", kind: "service", name: "umami-db" }, { op: "create", kind: "service", name: "umami" }] };

describe("Templates", () => {
  afterEach(() => vi.restoreAllMocks());

  it("installs Umami: plan, variables, the database live before the app", async () => {
    const vars = vi.spyOn(seal, "setVar").mockResolvedValue();
    const refs = vi.spyOn(seal, "setRef").mockResolvedValue();
    let polled = 0;
    const calls = fakeApi({
      "GET /api/templates": cards,
      "GET /api/templates/umami": umami,
      [`POST ${E}/templates/umami`]: (_b: unknown, url: string) => (url.includes("dry=1") ? plan : plan),
      [`GET ${E}/keys`]: keys,
      "POST /api/services/acme/production/umami-db/deploys": { deploy_id: "dep_db" },
      "GET /api/deploys/dep_db": () => ({ state: ++polled > 1 ? "live" : "starting" }),
      "POST /api/services/acme/production/umami/deploys": { deploy_id: "dep_app" },
      "GET /api/deploys/dep_app": { state: "live" },
    });
    render(<Templates project="acme" env="production" poll={1} />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    expect(await screen.findByText("create service umami-db")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Admin email"), { target: { value: "me@acme.uz" } });
    fireEvent.click(screen.getByRole("button", { name: "Install" }));
    expect(await screen.findByText("Log in as admin.")).toBeTruthy();
    const svc = "/api/services/acme/production/umami";
    expect(refs).toHaveBeenCalledWith(svc, "DATABASE_URL", "umami-db.url");
    const secret = vars.mock.calls.find((c) => c[1] === "APP_SECRET")!;
    expect(secret[0]).toBe(svc);
    expect(secret[2]).toMatch(/^[A-Za-z0-9]{32}$/);
    expect(screen.getByText(secret[2] as string)).toBeTruthy();
    expect(screen.getByText("APP_SECRET on umami")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy" })).toBeTruthy();
    expect(vars).toHaveBeenCalledWith(svc, "ADMIN_EMAIL", "me@acme.uz", false);
    const order = calls.map((c) => c.method + " " + c.url).filter((c) => c.includes("/deploys"));
    expect(order.indexOf("POST /api/services/acme/production/umami/deploys")).toBeGreaterThan(order.lastIndexOf("GET /api/deploys/dep_db"));
    expect(polled).toBeGreaterThan(1);
  });

  it("stops before anything when a name is taken", async () => {
    const calls = fakeApi({
      "GET /api/templates": cards,
      "GET /api/templates/umami": umami,
      [`POST ${E}/templates/umami`]: () => fail(409, "umami-db already exists in acme/production."),
    });
    render(<Templates project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    expect(await screen.findByText("umami-db already exists in acme/production.")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Install" })).toBeNull();
    expect(calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1"))).toHaveLength(0);
  });

  it("says which step failed and what was created", async () => {
    vi.spyOn(seal, "setVar").mockRejectedValue(new Error("Make the recovery key first, in Settings."));
    vi.spyOn(seal, "setRef").mockResolvedValue();
    fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": { ...umami, vars: umami.vars.slice(0, 2) }, [`POST ${E}/templates/umami`]: plan, [`GET ${E}/keys`]: keys });
    render(<Templates project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Install" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Setting APP_SECRET on umami failed: Make the recovery key first, in Settings."));
    expect(screen.getByRole("alert").textContent).toContain("Created: umami-db, umami.");
  });

  it("draws each template's icons", async () => {
    fakeApi({ "GET /api/templates": cards });
    render(<Templates project="acme" env="production" />);
    const grafana = await screen.findByRole("button", { name: /Grafana and Loki/ });
    expect(grafana.querySelectorAll("svg")).toHaveLength(2);
    expect(screen.getByRole("button", { name: /Umami/ }).querySelectorAll("svg")).toHaveLength(1);
  });

  it("checks the keys before creating anything", async () => {
    const calls = fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": umami, [`POST ${E}/templates/umami`]: plan,
      [`GET ${E}/keys`]: { ...keys, recovery_pubkey: "" } });
    render(<Templates project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Install" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Make the recovery key first, in Settings."));
    expect(calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1"))).toHaveLength(0);
  });

  it("follows the app's deploy too and says when it failed (review M1, plan 23)", async () => {
    vi.spyOn(seal, "setVar").mockResolvedValue();
    vi.spyOn(seal, "setRef").mockResolvedValue();
    fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": umami, [`POST ${E}/templates/umami`]: plan, [`GET ${E}/keys`]: keys,
      "POST /api/services/acme/production/umami-db/deploys": { deploy_id: "dep_db" }, "GET /api/deploys/dep_db": { state: "live" },
      "POST /api/services/acme/production/umami/deploys": { deploy_id: "dep_app" }, "GET /api/deploys/dep_app": { state: "failed" } });
    render(<Templates project="acme" env="production" poll={1} />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Install" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Deploying umami failed: umami did not start."));
    expect(screen.queryByText("Log in as admin.")).toBeNull();
  });

  it("stops waiting for a queued database and says what to do", async () => {
    vi.spyOn(seal, "setVar").mockResolvedValue();
    vi.spyOn(seal, "setRef").mockResolvedValue();
    fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": umami, [`POST ${E}/templates/umami`]: plan, [`GET ${E}/keys`]: keys,
      "POST /api/services/acme/production/umami-db/deploys": { deploy_id: "dep_db" }, "GET /api/deploys/dep_db": { state: "queued" } });
    render(<Templates project="acme" env="production" poll={1} deadline={30} />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Install" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("umami-db is still queued; deploy umami from its page once umami-db is live."));
  });
});
