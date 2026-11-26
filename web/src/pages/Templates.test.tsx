import { afterEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../test-api";
import * as seal from "../seal";
import { Templates } from "./Templates";

const E = "/api/environments/acme/production";
const svg = '<svg viewBox="0 0 24 24"><path d="M0 0h24v24H0z"/></svg>';
const cards = [{ id: "umami", title: "Umami", line: "Website analytics without cookies.", category: "analytics", memory: "1GB", icons: [svg],
  services: [{ name: "umami-db", kind: "postgres", volumes: [] }, { name: "umami", kind: "web", volumes: [] }] },
  { id: "grafana-loki", title: "Grafana and Loki", line: "Dashboards.", category: "analytics", memory: "1GB", icons: [svg, svg], services: [] },
  { id: "pocketbase", title: "PocketBase", line: "Auth and a realtime database.", category: "backend", new: true, memory: "256MB", icons: [svg],
    services: [{ name: "pocketbase", kind: "web", volumes: [{ name: "data", path: "/pb_data" }] }] }];
const umami = { ...cards[0], note: "Log in as admin.", services: [{ name: "umami-db", kind: "postgres", memory: "512MB" }, { name: "umami", kind: "web", port: 3000, memory: "512MB" }],
  vars: [{ service: "umami", key: "DATABASE_URL", kind: "ref", value: "umami-db.url" }, { service: "umami", key: "APP_SECRET", kind: "generate" },
    { service: "umami", key: "ADMIN_EMAIL", kind: "ask", label: "Admin email" }] };
const keys = { agent_pubkey: "a", recovery_pubkey: "r", server: "hel1" };
const plan = { changes: [{ op: "create", kind: "service", name: "umami-db" }, { op: "create", kind: "service", name: "umami" }] };
const fill = () => {
  const ask = screen.queryByLabelText("Admin email");
  if (ask) fireEvent.change(ask, { target: { value: "me@acme.uz" } });
};

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
    expect((await screen.findByRole("list", { name: "Will be created" })).textContent).toContain("umami-dbpostgres · 512 MBdatabase");
    fireEvent.change(screen.getByLabelText("Admin email"), { target: { value: "me@acme.uz" } });
    fireEvent.click(screen.getByRole("button", { name: "Deploy template" }));
    expect(await screen.findByText("Log in as admin.")).toBeTruthy();
    const svc = "/api/services/acme/production/umami";
    expect(refs).toHaveBeenCalledWith(svc, "DATABASE_URL", "umami-db.url");
    const secret = vars.mock.calls.find((c) => c[1] === "APP_SECRET")!;
    expect(secret[0]).toBe(svc);
    expect(secret[2]).toMatch(/^[A-Za-z0-9]{32}$/);
    expect(screen.getByText(secret[2] as string)).toBeTruthy();
    expect(screen.getByText("APP_SECRET on umami")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy APP_SECRET" })).toBeTruthy();
    expect(vars).toHaveBeenCalledWith(svc, "ADMIN_EMAIL", "me@acme.uz", false);
    const order = calls.map((c) => c.method + " " + c.url).filter((c) => c.includes("/deploys"));
    expect(order.indexOf("POST /api/services/acme/production/umami/deploys")).toBeGreaterThan(order.lastIndexOf("GET /api/deploys/dep_db"));
    expect(polled).toBeGreaterThan(1);
  });

  it("keeps Deploy template off until every asked value is filled", async () => {
    fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": umami, [`POST ${E}/templates/umami`]: plan });
    render(<Templates project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    const deploy = await screen.findByRole("button", { name: "Deploy template" }) as HTMLButtonElement;
    expect(deploy.disabled).toBe(true);
    fireEvent.change(screen.getByLabelText("Admin email"), { target: { value: "   " } });
    expect(deploy.disabled).toBe(true);
    fill();
    expect(deploy.disabled).toBe(false);
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
    expect(screen.queryByRole("button", { name: "Deploy template" })).toBeNull();
    expect(calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1"))).toHaveLength(0);
  });

  it("says which step failed and what was created", async () => {
    vi.spyOn(seal, "setVar").mockRejectedValue(new Error("Make the recovery key first, in Settings."));
    vi.spyOn(seal, "setRef").mockResolvedValue();
    fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": { ...umami, vars: umami.vars.slice(0, 2) }, [`POST ${E}/templates/umami`]: plan, [`GET ${E}/keys`]: keys });
    render(<Templates project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    await screen.findByRole("button", { name: "Deploy template" });
    fill();
    fireEvent.click(screen.getByRole("button", { name: "Deploy template" }));
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
    await screen.findByRole("button", { name: "Deploy template" });
    fill();
    fireEvent.click(screen.getByRole("button", { name: "Deploy template" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("Make the recovery key first, in Settings."));
    expect(calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1"))).toHaveLength(0);
  });

  it("follows the app's deploy too and says when it failed", async () => {
    vi.spyOn(seal, "setVar").mockResolvedValue();
    vi.spyOn(seal, "setRef").mockResolvedValue();
    fakeApi({ "GET /api/templates": cards, "GET /api/templates/umami": umami, [`POST ${E}/templates/umami`]: plan, [`GET ${E}/keys`]: keys,
      "POST /api/services/acme/production/umami-db/deploys": { deploy_id: "dep_db" }, "GET /api/deploys/dep_db": { state: "live" },
      "POST /api/services/acme/production/umami/deploys": { deploy_id: "dep_app" }, "GET /api/deploys/dep_app": { state: "failed" } });
    render(<Templates project="acme" env="production" poll={1} />);
    fireEvent.click(await screen.findByRole("button", { name: /Umami/ }));
    await screen.findByRole("button", { name: "Deploy template" });
    fill();
    fireEvent.click(screen.getByRole("button", { name: "Deploy template" }));
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
    await screen.findByRole("button", { name: "Deploy template" });
    fill();
    fireEvent.click(screen.getByRole("button", { name: "Deploy template" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("umami-db is still queued; deploy umami from its page once umami-db is live."));
  });

  it("filters by kind and counts each kind", async () => {
    fakeApi({ "GET /api/templates": cards });
    render(<Templates project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("tab", { name: "Backend · 1" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: /Umami/ })).toBeNull());
    expect(screen.getByRole("tab", { name: "All · 3" })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Analytics · 2" })).toBeTruthy();
    expect(screen.queryByRole("tab", { name: /Content/ })).toBeNull();
    const pb = screen.getByRole("button", { name: /PocketBase/ });
    expect(pb.textContent).toContain("New");
    expect(pb.textContent).toContain("pocketbase (web, data volume)");
  });

  it("searches titles, lines and service names within the chosen kind", async () => {
    fakeApi({ "GET /api/templates": cards });
    render(<Templates project="acme" env="production" />);
    const box = await screen.findByRole("searchbox", { name: "Search templates" });
    fireEvent.change(box, { target: { value: "UMAMI-DB" } });
    expect(screen.getByRole("button", { name: /Umami/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Grafana/ })).toBeNull();
    fireEvent.change(box, { target: { value: "realtime" } });
    expect(screen.getByRole("button", { name: /PocketBase/ })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "All · 1" })).toBeTruthy();
    expect(screen.getByRole("tab", { name: "Backend · 1" })).toBeTruthy();
    fireEvent.click(screen.getByRole("tab", { name: "Analytics · 0" }));
    await screen.findByText("No template matches the search.");
  });

  it("lists the services and volumes it creates and sends the domain", async () => {
    vi.spyOn(seal, "setVar").mockResolvedValue();
    const pocketbase = { ...cards[2], note: "Open /_/.", services: [{ name: "pocketbase", kind: "web", port: 8090, memory: "256MB", volumes: [{ name: "data", path: "/pb_data" }] }],
      vars: [{ service: "pocketbase", key: "PB_ADMIN_EMAIL", kind: "ask", label: "Admin email" }, { service: "pocketbase", key: "PB_ADMIN_PASSWORD", kind: "generate" }] };
    const calls = fakeApi({ "GET /api/templates": cards, "GET /api/templates/pocketbase": pocketbase,
      "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "fra-1", services: [] }] }],
      [`POST ${E}/templates/pocketbase`]: { changes: [] }, [`GET ${E}/keys`]: keys,
      "POST /api/services/acme/production/pocketbase/deploys": { deploy_id: "d" }, "GET /api/deploys/d": { state: "live" } });
    render(<Templates project="acme" env="production" poll={1} />);
    fireEvent.click(await screen.findByRole("button", { name: /PocketBase/ }));
    const list = await screen.findByRole("list", { name: "Will be created" });
    expect(list.textContent).toContain("web · port 8090 · 256 MB");
    expect(list.textContent).toContain("pocketbase/datavolume at /pb_data");
    expect(screen.getByText("Databases and volumes are backed up encrypted every night.")).toBeTruthy();
    expect(await screen.findByText("fra-1")).toBeTruthy();
    expect(screen.getByText("PB_ADMIN_PASSWORD is generated and shown once, then kept as a secret variable.")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Domain"), { target: { value: " pb.acme.dev " } });
    fill();
    fireEvent.click(screen.getByRole("button", { name: "Deploy template" }));
    expect(await screen.findByText("Open /_/.")).toBeTruthy();
    expect(calls.find((c) => c.method === "POST" && c.url === `${E}/templates/pocketbase`)!.body).toEqual({ domain: "pb.acme.dev" });
  });
});
