import { describe, expect, it, vi, beforeEach } from "vitest";
import { act, render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import { chooseOption, fakeApi, fail } from "../test-api";
import * as seal from "../seal";
import { NewService, templatesLine } from "./NewService";
import { emptyForm } from "../serviceForm";

const tree = [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [{ service: "acme-db", kind: "postgres" }] }] }];
const inspect = { commit: "abc", dockerfile: false, language: "node 22", reads_port: true, port: 0, health_path: "/healthz", missing: ["DATABASE_URL", "SECRET_KEY", "SENTRY_DSN"], warnings: ["package.json asks for Node 18; the build uses Node 22."], builder: "", static: false, notes: [] };

async function pick(name: string, typed: string, option: string | RegExp) {
  const input = screen.getByRole("combobox", { name });
  act(() => input.focus());
  fireEvent.keyDown(input, { key: "ArrowDown" });
  fireEvent.change(input, { target: { value: typed } });
  fireEvent.click(await screen.findByRole("option", { name: option }));
}

function readRepo() {
  fireEvent.click(screen.getByRole("button", { name: /Web Service/ }));
  fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/api" } });
  fireEvent.click(screen.getByRole("button", { name: "Read repository" }));
}

async function toSettings() {
  fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
  fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
}

describe("NewService", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("Postgres skips the repository and asks name, version and backup time", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Postgres/ }));
    expect(screen.queryByLabelText("Repository")).toBeNull();
    expect(screen.getByLabelText("Name")).toBeTruthy();
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("17");
    expect((screen.getByLabelText("Daily backup (UTC)") as HTMLInputElement).value).toBe("03:00");
  });

  it("opens with the kind the canvas picked", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" kind="postgres" />);
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("17");
    expect(screen.queryByLabelText("Repository")).toBeNull();
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
    await toSettings();
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("api");
    expect((screen.getByLabelText("Health path") as HTMLInputElement).value).toBe("/healthz");
    for (const bad of ["", "1api", "API"]) {
      fireEvent.change(screen.getByLabelText("Name"), { target: { value: bad } });
      expect((screen.getByRole("button", { name: "Show the plan" }) as HTMLButtonElement).disabled).toBe(true);
    }
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "api" } });

    await chooseOption("DATABASE_URL", "Reference a database: acme-db.url");
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
    await toSettings();
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
    await pick("Time zone", "Tashk", "Asia/Tashkent");
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
    expect(screen.queryByRole("tab", { name: "From an image" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/shop" } });
    fireEvent.click(screen.getByRole("button", { name: "Read repository" }));
    await screen.findByText("Made with Lovable");
    expect(screen.getByText("Vite site: built, then served as static files")).toBeTruthy();
    expect(screen.getByText(note)).toBeTruthy();
    const [vite, stripe] = screen.getAllByRole("checkbox", { name: "Build-time" });
    expect([vite.getAttribute("aria-checked"), stripe.getAttribute("aria-checked")]).toEqual(["true", "false"]);
  });

  it("a Valkey card makes a database with a backup switch", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [{ op: "create", kind: "service", name: "cache" }] } });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Valkey/ }));
    expect(screen.queryByLabelText("Repository")).toBeNull();
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("8");
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "cache" } });
    fireEvent.click(screen.getByRole("switch", { name: "Back up every night" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("create service cache");
    expect(calls.find((c) => c.url.includes("dry=1"))?.body).toMatchObject({ name: "cache", kind: "valkey", version: 8, backup: true });
  });

  it("a MySQL card has no backup switch: it is always backed up", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /MySQL/ }));
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("8");
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("8.4");
    expect(screen.queryByRole("switch", { name: "Back up every night" })).toBeNull();
    expect(screen.getByLabelText("Daily backup (UTC)")).toBeTruthy();
  });

  it("a MongoDB card: version 8, no backup switch", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /MongoDB/ }));
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("8");
    expect(screen.queryByRole("switch", { name: "Back up every night" })).toBeNull();
  });

  it("MariaDB and ClickHouse cards", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /MariaDB/ }));
    expect(screen.getByRole("combobox", { name: "Version" }).textContent).toContain("11");
    fireEvent.click(screen.getByRole("button", { name: /ClickHouse/ }));
    expect(screen.getByRole("switch", { name: "Back up every night" })).toBeTruthy();
  });
});

describe("NewService fields", () => {
  it("shows every field of a web service and sends the same form shape", async () => {
    const calls = fakeApi({
      "GET /api/projects": tree,
      "POST /api/inspect": { ...inspect, missing: [] },
      "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [{ op: "create", kind: "service", name: "api" }] },
    });
    render(<NewService project="acme" env="production" />);
    readRepo();
    await screen.findByText(/No Dockerfile/);
    await toSettings();
    expect(screen.queryByLabelText("Branch")).toBeNull();
    for (const l of ["Name", "Port", "Health path", "Release command", "CPU", "Memory"]) expect(screen.getAllByLabelText(l).length).toBeGreaterThan(0);
    expect(screen.getByRole("group", { name: "Volumes" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Add volume" }));
    fireEvent.change(screen.getByLabelText("Name, volume 1"), { target: { value: "uploads" } });
    fireEvent.change(screen.getByLabelText("Path, volume 1"), { target: { value: "/app/uploads" } });
    fireEvent.click(screen.getByRole("button", { name: "Add volume" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await screen.findByText("create service api");
    const sent = calls.find((c) => c.url.includes("dry=1"))!.body as Record<string, unknown>;
    expect(Object.keys(sent).sort()).toEqual(Object.keys(emptyForm("web")).sort());
    expect(sent).toMatchObject({ name: "api", kind: "web", branch: "main", port: 10000, health_path: "/healthz", volumes: [{ name: "uploads", path: "/app/uploads" }], cpu: 0, memory: "" });
  });
});

describe("NewService repository picker", () => {
  const hour = 3_600_000;
  const repos = [
    { full_name: "acme/api", url: "https://github.com/acme/api", private: true, pushed_at: Date.now() - 2 * hour, default_branch: "trunk" },
    { full_name: "acme/site", url: "https://github.com/acme/site", private: false, pushed_at: Date.now() - 50 * hour, default_branch: "main" },
  ];

  it("lists the App's repositories newest first, finds one and fills the URL and branch", async () => {
    fakeApi({ "GET /api/projects": tree, "GET /api/github/repos": repos });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Web Service/ }));
    const input = await screen.findByRole("combobox", { name: "Your repositories" });
    act(() => input.focus());
    fireEvent.keyDown(input, { key: "ArrowDown" });
    expect((await screen.findAllByRole("option")).map((o) => o.textContent)).toEqual(["acme/apiPrivatepushed 2 h ago", "acme/sitepushed 2 days ago"]);
    fireEvent.change(input, { target: { value: "SITE" } });
    await waitFor(() => expect(screen.getAllByRole("option")).toHaveLength(1));
    fireEvent.change(input, { target: { value: "" } });
    fireEvent.click(await screen.findByRole("option", { name: /acme\/api/ }));
    expect((screen.getByLabelText("Repository") as HTMLInputElement).value).toBe("https://github.com/acme/api");
    expect((screen.getByLabelText("Branch") as HTMLInputElement).value).toBe("trunk");
  });

  it("keeps a branch someone typed", async () => {
    fakeApi({ "GET /api/projects": tree, "GET /api/github/repos": repos });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Web Service/ }));
    fireEvent.change(screen.getByLabelText("Branch"), { target: { value: "release" } });
    await screen.findByRole("combobox", { name: "Your repositories" });
    await pick("Your repositories", "site", /acme\/site/);
    expect((screen.getByLabelText("Repository") as HTMLInputElement).value).toBe("https://github.com/acme/site");
    expect((screen.getByLabelText("Branch") as HTMLInputElement).value).toBe("release");
  });

  it("points to Integrations when the GitHub App is not connected, and the URL still works by hand", async () => {
    fakeApi({ "GET /api/projects": tree, "GET /api/github/repos": [] });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(screen.getByRole("button", { name: /Web Service/ }));
    expect((await screen.findByRole("link", { name: "Connect the GitHub App in Integrations" })).getAttribute("href")).toBe("/integrations");
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/public" } });
    expect((screen.getByLabelText("Repository") as HTMLInputElement).value).toBe("https://github.com/acme/public");
  });
});

describe("NewService build step", () => {
  const next = { ...inspect, missing: [], warnings: [], framework: "nextjs", port: 3000 };

  async function toBuild(d = next) {
    fakeApi({ "GET /api/projects": tree, "POST /api/inspect": d });
    render(<NewService project="acme" env="production" />);
    readRepo();
    expect(screen.getByRole("list", { name: "Steps" }).querySelector("[aria-current=step]")!.textContent).toContain("Source");
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await screen.findByRole("heading", { name: "How to build it" });
  }

  it("marks the detected preset and leaves the commands to the builder", async () => {
    await toBuild();
    expect(screen.getByText("Found next in package.json, so it builds as Next.js. Fill in a command only to change what the build runs.")).toBeTruthy();
    const tile = screen.getByRole("button", { name: /Next\.js/ });
    expect(tile.getAttribute("aria-pressed")).toBe("true");
    expect(within(tile).getByText("Detected")).toBeTruthy();
    for (const [label, hint] of [["Install command", "npm ci"], ["Build command", "npm run build"], ["Start command", "npm start"]]) {
      const input = screen.getByLabelText(label) as HTMLInputElement;
      expect([input.value, input.placeholder]).toEqual(["", hint]);
    }
    expect(screen.queryByLabelText("Port")).toBeNull();
    expect(screen.queryByLabelText("Output folder")).toBeNull();
  });

  it("sends the commands typed and shows Port once, on the last step", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, "POST /api/inspect": next, "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [] } });
    render(<NewService project="acme" env="production" />);
    readRepo();
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await screen.findByRole("heading", { name: "How to build it" });
    fireEvent.change(screen.getByLabelText("Install command"), { target: { value: " pnpm i " } });
    fireEvent.change(screen.getByLabelText("Start command"), { target: { value: "node server.js" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getAllByLabelText("Port")).toHaveLength(1);
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.find((c) => c.url.includes("dry=1"))?.body).toMatchObject({ steps: { install: "pnpm i", start: "node server.js" } }));
  });

  it("Static sends the output folder and hides the start command", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, "POST /api/inspect": next, "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [] } });
    render(<NewService project="acme" env="production" />);
    readRepo();
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
    await screen.findByRole("heading", { name: "How to build it" });
    fireEvent.click(screen.getByRole("radio", { name: /Static/ }));
    expect((screen.getByLabelText("Output folder") as HTMLInputElement).value).toBe("out");
    expect(screen.queryByLabelText("Start command")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /Vite/ }));
    expect((screen.getByLabelText("Output folder") as HTMLInputElement).value).toBe("dist");
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.find((c) => c.url.includes("dry=1"))?.body).toMatchObject({ steps: { start: "", output: "dist" } }));
  });

  it("a repository with a Dockerfile shows no commands to fill in", async () => {
    await toBuild({ ...next, dockerfile: true });
    expect(screen.queryByLabelText("Install command")).toBeNull();
    expect(screen.queryByRole("radio", { name: /Static/ })).toBeNull();
    expect(screen.queryByRole("group", { name: "Framework" })).toBeNull();
    expect(screen.getByText(/Builds with your Dockerfile/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Build with Railpack instead" }));
    expect(screen.getByRole("group", { name: "Framework" })).toBeTruthy();
    expect(screen.getByText("Builds with Railpack.")).toBeTruthy();
  });

  it("Other leaves it to Railpack and sends builder railpack", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, "POST /api/inspect": next, "POST /api/environments/acme/production/services?new=1&dry=1": { changes: [] } });
    render(<NewService project="acme" env="production" />);
    readRepo();
    fireEvent.click(await screen.findByRole("button", { name: "Continue" }));
    fireEvent.click(await screen.findByRole("button", { name: /Other/ }));
    expect(screen.getByText("Builds with Railpack.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.find((c) => c.url.includes("dry=1"))?.body).toMatchObject({ builder: "railpack" }));
  });

  it("Back returns to the earlier step with the choice kept", async () => {
    await toBuild();
    fireEvent.click(screen.getByRole("button", { name: /Astro/ }));
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    expect(screen.getByLabelText("Name")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect((await screen.findByRole("button", { name: /Astro/ })).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByRole("button", { name: "Read repository" })).toBeTruthy();
  });
});

describe("templatesLine", () => {
  const named = (...titles: string[]) => titles.map((title) => ({ title }));
  it("names the templates the panel has", () => {
    expect(templatesLine(named("Directus", "Ghost"))).toBe("Directus, Ghost: ready to install.");
    expect(templatesLine(named("Directus", "Ghost", "Gitea", "n8n", "Umami", "Plausible"))).toBe("Directus, Ghost, Gitea, n8n and 2 more, ready to install.");
    expect(templatesLine(undefined)).toBe("Ready stacks, installed in one step.");
  });
});
