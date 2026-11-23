import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { fakeApi, fail } from "../test-api";
import * as seal from "../seal";
import { NewService } from "./NewService";
import { emptyForm } from "../serviceForm";

const tree = [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [{ service: "acme-db", kind: "postgres" }] }] }];
const inspect = { commit: "abc", dockerfile: false, language: "node 22", reads_port: true, port: 0, health_path: "/healthz", missing: ["DATABASE_URL", "SECRET_KEY", "SENTRY_DSN"], warnings: ["package.json asks for Node 18; the build uses Node 22."], builder: "", static: false, notes: [] };

function readRepo() {
  fireEvent.click(screen.getByRole("button", { name: /Web Service/ }));
  fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/api" } });
  fireEvent.click(screen.getByRole("button", { name: "Read repository" }));
}

describe("NewService", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("Postgres skips the repository and asks name, version and backup time", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Postgres/ }));
    expect(screen.queryByLabelText("Repository")).toBeNull();
    expect(screen.getByLabelText("Name")).toBeTruthy();
    expect((screen.getByLabelText("Version") as HTMLSelectElement).value).toBe("17");
    expect((screen.getByLabelText("Daily backup (UTC)") as HTMLInputElement).value).toBe("03:00");
  });

  it("shows the server's text when the repository cannot be read", async () => {
    fakeApi({ "GET /api/projects": tree, "POST /api/inspect": fail(422, "clone https://github.com/acme/api: repository not found") });
    render(<NewService project="acme" env="production" />);
    readRepo();
    expect((await screen.findByRole("alert")).textContent).toContain("repository not found");
  });

  it("reads the repository, prefills, plans, applies, sets keys and deploys", async () => {
    const setVar = vi.spyOn(seal, "setVar").mockResolvedValue();
    const setRef = vi.spyOn(seal, "setRef").mockResolvedValue();
    const calls = fakeApi({
      "GET /api/projects": tree,
      "POST /api/inspect": inspect,
      "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [{ op: "create", kind: "service", name: "api" }, { op: "unchanged", kind: "service", name: "acme-db" }] },
      "POST /api/environments/acme/production/services?new=1": { changes: [] },
      "POST /api/services/acme/production/api/deploys": { deploy_id: "dep_9" },
    });
    render(<NewService project="acme" env="production" />);
    readRepo();
    await screen.findByText("No Dockerfile: Yawl will build it as node 22");
    expect(screen.getByText("3 keys missing: DATABASE_URL, SECRET_KEY, SENTRY_DSN")).toBeTruthy();
    expect(screen.getByText("package.json asks for Node 18; the build uses Node 22.")).toBeTruthy();
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("api");
    expect((screen.getByLabelText("Health path") as HTMLInputElement).value).toBe("/healthz");

    fireEvent.change(screen.getByLabelText("DATABASE_URL"), { target: { value: "ref:acme-db.url" } });
    fireEvent.change(screen.getByLabelText("SECRET_KEY value"), { target: { value: "s3cret-value" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("create service api");
    fireEvent.click(screen.getByRole("button", { name: "Apply and deploy" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_9"));

    const applied = calls.filter((c) => c.url.startsWith("/api/environments/acme/production/services"));
    expect(applied.map((c) => c.url)).toEqual(["/api/environments/acme/production/services?new=1&dry=1", "/api/environments/acme/production/services?new=1"]);
    expect(applied[1].body).toMatchObject({ name: "api", kind: "web", repo: "https://github.com/acme/api", branch: "main", health_path: "/healthz", port: 10000 });
    expect(setRef).toHaveBeenCalledWith("/api/services/acme/production/api", "DATABASE_URL", "acme-db.url");
    expect(setVar).toHaveBeenCalledWith("/api/services/acme/production/api", "SECRET_KEY", "s3cret-value", false);
    expect(setVar).toHaveBeenCalledTimes(1);
    for (const c of calls) expect(JSON.stringify(c.body ?? "")).not.toContain("s3cret-value");
  });
});

describe("NewService after a partial failure", () => {
  it("offers the service that was made instead of a second create", async () => {
    vi.spyOn(seal, "setVar").mockResolvedValue();
    vi.spyOn(seal, "setRef").mockResolvedValue();
    const calls = fakeApi({
      "GET /api/projects": tree,
      "POST /api/inspect": { ...inspect, missing: [] },
      "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [{ op: "create", kind: "service", name: "api" }] },
      "POST /api/environments/acme/production/services?new=1": { changes: [] },
      "POST /api/services/acme/production/api/deploys": () => fail(500, "no room on hel1"),
    });
    render(<NewService project="acme" env="production" />);
    readRepo();
    await screen.findByText(/No Dockerfile/);
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("create service api");
    const b = screen.getByRole("button", { name: "Apply and deploy" });
    fireEvent.click(b);
    fireEvent.click(b);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("no room on hel1");
    expect(within(alert).getByRole("link", { name: "Open api" }).getAttribute("href")).toBe("/p/acme/production/s/api");
    expect(screen.queryByRole("button", { name: "Apply and deploy" })).toBeNull();
    expect(calls.filter((c) => c.url === "/api/environments/acme/production/services?new=1")).toHaveLength(1);
  });
});

describe("NewService cron", () => {
  it("asks schedule, zone, command and limit, and shows the server's refusal", async () => {
    const calls = fakeApi({
      "GET /api/projects": tree,
      "POST /api/inspect": { ...inspect, missing: [] },
      "POST /api/environments/acme/production/services?new=1&dry=1": fail(400, 'report: "schedule" must be five fields (minute hour day month weekday), got "0 3 * *"'),
    });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Cron Job/ }));
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/report" } });
    fireEvent.click(screen.getByRole("button", { name: "Read repository" }));
    await screen.findByLabelText("Schedule");
    expect(screen.queryByLabelText("Port")).toBeNull();
    expect(screen.queryByLabelText("Release command")).toBeNull();
    expect((screen.getByLabelText("Time limit (minutes)") as HTMLInputElement).value).toBe("60");
    fireEvent.change(screen.getByLabelText("Schedule"), { target: { value: "0 3 * *" } });
    fireEvent.change(screen.getByLabelText("Time zone"), { target: { value: "Asia/Tashkent" } });
    fireEvent.change(screen.getByLabelText("Command"), { target: { value: "node\n\njobs/report.js\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    expect((await screen.findByRole("alert")).textContent).toContain('"schedule" must be five fields');
    const sent = calls.find((c) => c.method === "POST" && c.url.includes("dry=1"))!.body as Record<string, unknown>;
    expect([sent.kind, sent.schedule, sent.timezone, sent.command, sent.timeout, sent.health_path]).toEqual(["cron", "0 3 * *", "Asia/Tashkent", ["node", "jobs/report.js"], 3600, ""]);
  });

  it("Import an app reads a repository and ticks the keys the build bakes in", async () => {
    const note = "Supabase stays where it is. Add this service's address in Supabase, Authentication, URL Configuration, or sign in sends people back to the old address.";
    fakeApi({ "GET /api/projects": tree, "POST /api/inspect": { ...inspect, builder: "Lovable", static: true, health_path: "/", missing: ["VITE_API", "STRIPE_KEY"], warnings: [], notes: [note] } });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Import an app/ }));
    expect(screen.getByRole("heading", { level: 1 }).textContent).toBe("Import an app");
    expect(screen.queryByRole("group", { name: "Source" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/shop" } });
    fireEvent.click(screen.getByRole("button", { name: "Read repository" }));
    await screen.findByText("Made with Lovable");
    expect(screen.getByText("Vite site: built, then served as static files")).toBeTruthy();
    expect(screen.getByText(note)).toBeTruthy();
    const [vite, stripe] = screen.getAllByRole("checkbox", { name: "Build-time" }) as HTMLInputElement[];
    expect([vite.checked, stripe.checked]).toEqual([true, false]);
  });

  it("a Valkey card makes a database with a backup switch", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [{ op: "create", kind: "service", name: "cache" }] } });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Valkey/ }));
    expect(screen.queryByLabelText("Repository")).toBeNull();
    expect((screen.getByLabelText("Version") as HTMLSelectElement).value).toBe("8");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "cache" } });
    fireEvent.click(screen.getByLabelText("Back up every night"));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("create service cache");
    expect(calls.find((c) => c.url.includes("dry=1"))?.body).toMatchObject({ name: "cache", kind: "valkey", version: 8, backup: true });
  });

  it("a MySQL card has no backup switch: it is always backed up", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /MySQL/ }));
    expect((screen.getByLabelText("Version") as HTMLSelectElement).value).toBe("8");
    expect((screen.getByLabelText("Version") as HTMLSelectElement).selectedOptions[0].textContent).toBe("8.4");
    expect(screen.queryByLabelText("Back up every night")).toBeNull();
    expect(screen.getByLabelText("Daily backup (UTC)")).toBeTruthy();
  });

  it("a MongoDB card: version 8, no backup switch", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /MongoDB/ }));
    expect((screen.getByLabelText("Version") as HTMLSelectElement).value).toBe("8");
    expect(screen.queryByLabelText("Back up every night")).toBeNull();
  });

  it("MariaDB and ClickHouse cards", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /MariaDB/ }));
    expect((screen.getByLabelText("Version") as HTMLSelectElement).value).toBe("11");
    fireEvent.click(screen.getByRole("button", { name: /ClickHouse/ }));
    expect(screen.getByLabelText("Back up every night")).toBeTruthy();
  });
});

describe("NewService fields", () => {
  it("shows every field of a web service in one card and sends the same form shape", async () => {
    const calls = fakeApi({
      "GET /api/projects": tree,
      "POST /api/inspect": { ...inspect, missing: [] },
      "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [{ op: "create", kind: "service", name: "api" }] },
    });
    render(<NewService project="acme" env="production" />);
    readRepo();
    await screen.findByText(/No Dockerfile/);
    for (const l of ["Name", "Branch", "Port", "Health path", "Release command", "CPU", "Memory"]) expect(screen.getAllByLabelText(l).length).toBeGreaterThan(0);
    expect(screen.getByRole("group", { name: "Volumes" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "+ Add volume" }));
    fireEvent.change(screen.getByLabelText("Name, volume 1"), { target: { value: "uploads" } });
    fireEvent.change(screen.getByLabelText("Path, volume 1"), { target: { value: "/app/uploads" } });
    fireEvent.click(screen.getByRole("button", { name: "+ Add volume" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("create service api");
    const sent = calls.find((c) => c.url.includes("dry=1"))!.body as Record<string, unknown>;
    expect(Object.keys(sent).sort()).toEqual(Object.keys(emptyForm("web")).sort());
    expect(sent).toMatchObject({ name: "api", kind: "web", branch: "main", port: 10000, health_path: "/healthz", volumes: [{ name: "uploads", path: "/app/uploads" }], cpu: 0, memory: "" });
  });
});
