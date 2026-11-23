import { describe, expect, it, vi } from "vitest";
import { act, render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../test-api";
import { Project } from "./Project";

const tree = [{
  name: "acme",
  envs: [{
    name: "production", server: "hel1", services: [
      { project: "acme", env: "production", service: "api", kind: "web", url: "https://api-production.1-2-3-4.sslip.io", state: "live", commit: "a1f9c2e88", health: "ok" },
      { project: "acme", env: "production", service: "worker", kind: "worker", state: "failed", commit: "7be0d41aa", cause: "ModuleNotFoundError: jinja2", health: "ok" },
    ],
  }],
}];

describe("Project", () => {
  it("shows the services with state, URL, commit and cause", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<Project project="acme" env="production" />);
    await screen.findByText("api");
    const link = screen.getByText("api-production.1-2-3-4.sslip.io");
    expect(link.getAttribute("href")).toBe("https://api-production.1-2-3-4.sslip.io");
    expect(screen.getByText("a1f9c2e")).toBeTruthy();
    expect(screen.getByText("ModuleNotFoundError: jinja2")).toBeTruthy();
    expect([...screen.getByText("Deployed").classList]).toEqual(["badge", "ok"]);
    expect([...screen.getByText("Failed").classList]).toEqual(["badge", "bad"]);
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    const move = screen.getByRole("menuitem", { name: "Move production" });
    expect(move.getAttribute("href")).toBe("/p/acme/production/move");
  });
  it("says No signal and links the server when the environment's server is offline", async () => {
    const lastSeen = Date.now() - 5 * 60_000;
    fakeApi({ "GET /api/projects": [{ ...tree[0], envs: [{ ...tree[0].envs[0], server_offline: true, server_last_seen: lastSeen }] }] });
    render(<Project project="acme" env="production" />);
    const badges = await screen.findAllByText("No signal · 5 min ago");
    expect(badges).toHaveLength(2);
    expect([...badges[0].classList]).toEqual(["badge", "none"]);
    expect(screen.queryByText("Deployed")).toBeNull();
    expect(screen.getByRole("link", { name: "hel1" }).getAttribute("href")).toBe("/servers/hel1");
    expect(document.querySelector(".sub")!.textContent).toBe("production runs on hel1, offline, last seen 5 min ago.");
  });
  it("keeps the plain subtitle and green badge when the server is online", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<Project project="acme" env="production" />);
    await screen.findByText("Deployed");
    expect(document.querySelector(".sub")!.textContent).toBe("production runs on hel1.");
    expect(screen.queryByText(/No signal/)).toBeNull();
  });
  it("lands on the first environment when the one asked for does not exist", async () => {
    fakeApi({ "GET /api/projects": [{ name: "x", envs: [{ name: "dev", server: "hel1", services: [] }] }] });
    history.replaceState(null, "", "/p/x/production");
    render(<Project project="x" env="production" />);
    await waitFor(() => expect(location.pathname).toBe("/p/x/dev"));
  });
  it("switches environment from the title and marks the current one", async () => {
    const t = structuredClone(tree);
    t[0].envs.push({ name: "staging", server: "hk1", services: [] });
    fakeApi({ "GET /api/projects": t });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /^Switch environment/ }));
    expect(screen.getByRole("menuitem", { name: /production/ }).getAttribute("aria-current")).toBe("true");
    expect(screen.getByRole("menu", { name: "Switch environment, production" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Add staging" })).toBeNull();
    const staging = screen.getByRole("menuitem", { name: /staging/ });
    expect(staging.textContent).toContain("hk1");
    fireEvent.click(staging);
    expect(location.pathname).toBe("/p/acme/staging");
  });
  it("adds staging on a chosen server", async () => {
    const calls = fakeApi({
      "GET /api/projects": tree,
      "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "1.2.3.4", last_heartbeat: 1 }, { id: "s2", name: "hk1", public_ip: "5.6.7.8", last_heartbeat: 1 }],
      "POST /api/environments": { changes: [] },
    });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /^Switch environment/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Add staging" }));
    await screen.findByRole("option", { name: "hk1 (5.6.7.8)" });
    fireEvent.change(screen.getByLabelText("Server"), { target: { value: "hk1" } });
    fireEvent.click(screen.getByRole("button", { name: "Create staging" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/staging"));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ project: "acme", env: "staging", server: "hk1" });
  });
  it("adds staging on the first online server, never an offline one", async () => {
    const calls = fakeApi({
      "GET /api/projects": tree,
      "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "1.2.3.4", offline: true }, { id: "s2", name: "hk1", public_ip: "5.6.7.8", last_heartbeat: 1, offline: false }],
      "POST /api/environments": { changes: [] },
    });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /^Switch environment/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Add staging" }));
    await screen.findByRole("option", { name: "hk1 (5.6.7.8)" });
    fireEvent.click(screen.getByRole("button", { name: "Create staging" }));
    await waitFor(() => expect(location.pathname).toBe("/p/acme/staging"));
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ project: "acme", env: "staging", server: "hk1" });
  });
  it("adds staging once on a double click, with the server picked at once", async () => {
    const calls = fakeApi({
      "GET /api/projects": tree,
      "GET /api/servers": [{ id: "s1", name: "hel1", public_ip: "1.2.3.4", last_heartbeat: 1 }, { id: "s2", name: "hk1", public_ip: "5.6.7.8", last_heartbeat: 1 }],
      "POST /api/environments": { changes: [] },
    });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /^Switch environment/ }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Add staging" }));
    await screen.findByRole("option", { name: "hk1 (5.6.7.8)" });
    fireEvent.change(screen.getByLabelText("Server"), { target: { value: "hk1" } });
    const b = screen.getByRole("button", { name: "Create staging" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(location.pathname).toBe("/p/acme/staging"));
    const posts = calls.filter((c) => c.method === "POST");
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toEqual({ project: "acme", env: "staging", server: "hk1" });
  });
  it("asks for the services again after POLL_MS", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    try {
      const calls = fakeApi({ "GET /api/projects": tree });
      render(<Project project="acme" env="production" />);
      await screen.findByText("api");
      const n = calls.filter((c) => c.url === "/api/projects").length;
      await act(async () => { vi.advanceTimersByTime(5000); });
      expect(calls.filter((c) => c.url === "/api/projects").length).toBeGreaterThan(n);
    } finally {
      vi.useRealTimers();
    }
  });
  it("links a web service's URL to its Domains, for those who may change them", async () => {
    fakeApi({ "GET /api/projects": tree, "GET /api/me": { email: "a@acme.uz", role: "developer" } });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: "Edit the domains of api" }));
    expect(location.pathname + location.hash).toBe("/p/acme/production/s/api/settings#domains");
    expect(screen.queryByRole("button", { name: "Edit the domains of worker" })).toBeNull();
  });
  it("shows viewers no edit control", async () => {
    fakeApi({ "GET /api/projects": tree, "GET /api/me": { email: "v@acme.uz", role: "viewer" } });
    render(<Project project="acme" env="production" />);
    await screen.findByText("api-production.1-2-3-4.sslip.io");
    expect(screen.queryByRole("button", { name: "Edit the domains of api" })).toBeNull();
  });
  it("says when the project does not exist", async () => {
    fakeApi({ "GET /api/projects": tree });
    render(<Project project="nope" env="production" />);
    await screen.findByText("There is no project nope.");
  });
});

describe("delete environment and project", () => {
  it("offers both, each behind the typed name", async () => {
    fakeApi({ "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }] }] });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete production" }));
    expect(screen.getByLabelText("Type production to delete it.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete project" }));
    expect(screen.getByLabelText("Type acme to delete it.")).toBeTruthy();
  });
  it("hides preview environments from the tabs and streams pull requests apart (v0.1.15)", async () => {
    const t = structuredClone(tree) as unknown as { name: string; envs: Record<string, unknown>[] }[];
    t[0].envs[0].preview = true;
    t[0].envs.push({ name: "pr-12", server: "hel1", services: [], preview_of: "env_1" });
    const calls = fakeApi({
      "GET /api/projects": t,
      "GET /api/projects/acme/previews": [
        { id: "pre_1", number: 12, title: "Faster search", branch: "search", base: "development", author: "octocat", state: "live", source: "production", env: "pr-12", at: Date.now() - 120_000,
          service: "api", deploy_id: "dep_9", url: "https://api-pr-12.1-2-3-4.sslip.io", commit: "abc1234ff", steps: [{ name: "build", state: "done", ms: 48_000 }, { name: "deploy", state: "done" }, { name: "live", state: "done" }] },
        { id: "pre_2", number: 31, title: "New invoice", branch: "invoice", state: "waiting", source: "production", at: 1, comment: "pr-31 waits: 5 previews are running. It starts when one closes." },
        { id: "pre_3", number: 7, title: "Other", branch: "x", state: "live", source: "staging", env: "pr-7", at: 1 },
        { id: "pre_4", number: 9, title: "Shipped", branch: "ship", state: "closed", source: "production", merged: true, at: 1, landed: [["production · api", "✅ api.acme.uz · e51f0d2"], ["Preview", "Removed"]] },
      ],
      "POST /api/previews/pre_1/close": null,
    });
    render(<Project project="acme" env="production" pulls />);
    expect(await screen.findByText("Faster search")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: /^Switch environment/ }));
    expect(screen.queryByRole("menuitem", { name: /pr-12/ })).toBeNull();
    expect(screen.getByRole("button", { name: "Switch environment, Pull requests" }).textContent).toBe("Pull requests");
    const pullRequests = screen.getByRole("menuitem", { name: "Pull requests2" });
    expect(pullRequests.getAttribute("href")).toBe("/p/acme/production/pulls");
    expect(pullRequests.getAttribute("aria-current")).toBe("true");
    expect(screen.getByRole("menuitem", { name: /production/ }).getAttribute("aria-current")).toBeNull();
    expect(screen.getByRole("link", { name: "Open preview ↗" }).getAttribute("href")).toBe("https://api-pr-12.1-2-3-4.sslip.io");
    expect(screen.getByRole("link", { name: "Logs" }).getAttribute("href")).toBe("/p/acme/pr-12/s/api/deploys/dep_9");
    expect(screen.getByText("Build 48s")).toBeTruthy();
    expect(screen.getByText("octocat", { exact: false })).toBeTruthy();
    expect(screen.getByText("pr-31 waits: 5 previews are running. It starts when one closes.")).toBeTruthy();
    expect(screen.queryByText("Other")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Close preview 12" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/previews/pre_1/close"))).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: "Merged, last day · 1" }));
    expect(await screen.findByText("✅ api.acme.uz · e51f0d2")).toBeTruthy();
  });
  it("offers Close only where the server closes, says why a deploy failed, and shows a refusal (review M7, plan 18)", async () => {
    const t = structuredClone(tree);
    (t[0].envs[0] as { preview?: boolean }).preview = true;
    fakeApi({
      "GET /api/projects": t,
      "GET /api/projects/acme/previews": [
        { id: "pre_1", number: 12, title: "Faster search", branch: "search", state: "live", source: "production", env: "pr-12", at: 1, service: "api", deploy_id: "dep_8", cause: "npm ci failed",
          steps: [{ name: "build", state: "fail" }, { name: "deploy", state: "todo" }, { name: "live", state: "todo" }] },
        { id: "pre_4", number: 40, title: "From a fork", branch: "fork:main", state: "fork", source: "production", at: 1, comment: "A fork's pull request gets no preview." },
      ],
      "POST /api/previews/pre_1/close": fail(404, "no such preview"),
    });
    render(<Project project="acme" env="production" pulls />);
    expect(await screen.findByText("From a fork")).toBeTruthy();
    expect(screen.getByText("npm ci failed")).toBeTruthy();
    expect(screen.getByRole("link", { name: "See why" }).getAttribute("href")).toBe("/p/acme/pr-12/s/api/deploys/dep_8");
    expect(screen.queryByRole("button", { name: "Close preview 40" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Close preview 12" }));
    expect(await screen.findByText("no such preview")).toBeTruthy();
  });
  it("lets an Admin set what previews copy and who may build them", async () => {
    const previewTree = structuredClone(tree);
    (previewTree[0].envs[0] as { preview?: boolean }).preview = true;
    (previewTree[0].envs[0].services[1] as { no_preview?: boolean }).no_preview = true;
    const calls = fakeApi({
      "GET /api/projects": previewTree,
      "GET /api/me": { role: "admin" },
      "GET /api/projects/acme/previews": [],
      "GET /api/projects/acme/preview-senders": { senders: ["owner"] },
      "PUT /api/projects/acme/preview-senders": null,
      "PUT /api/services/acme/production/api/preview": null,
    });
    render(<Project project="acme" env="production" />);
    expect(await screen.findByText("Each pull request gets a copy of")).toBeTruthy();
    expect([...screen.getByText("worker", { selector: ".pill" }).classList]).toContain("off");
    fireEvent.click(screen.getByRole("button", { name: "Preview settings" }));
    fireEvent.click(screen.getByRole("checkbox", { name: /api/ }));
    await waitFor(() => expect(calls.find((c) => c.url === "/api/services/acme/production/api/preview")?.body).toEqual({ on: false }));
    const input = await screen.findByLabelText("Who may build previews");
    await waitFor(() => expect((input as HTMLInputElement).value).toBe("owner"));
    fireEvent.change(input, { target: { value: "owner, dependabot[bot]" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(calls.find((c) => c.method === "PUT" && c.url === "/api/projects/acme/preview-senders")?.body).toEqual({ senders: ["owner", "dependabot[bot]"] }));
  });
  it("offers a Developer no preview settings", async () => {
    const previewTree = structuredClone(tree);
    (previewTree[0].envs[0] as { preview?: boolean }).preview = true;
    fakeApi({ "GET /api/projects": previewTree, "GET /api/me": { role: "developer" }, "GET /api/projects/acme/previews": [] });
    render(<Project project="acme" env="production" />);
    await screen.findByText("Each pull request gets a copy of");
    expect(screen.queryByRole("button", { name: "Preview settings" })).toBeNull();
  });
  it("links the last deploy, says its commit, and how a service without an address is reached", async () => {
    const t = structuredClone(tree);
    Object.assign(t[0].envs[0].services[0], { deploy_id: "dep_1", subject: "Merge #379", at: Date.now() - 7_200_000 });
    t[0].envs[0].services.push({ project: "acme", env: "production", service: "db", kind: "postgres", state: "live", health: "ok" } as never);
    fakeApi({ "GET /api/projects": t });
    render(<Project project="acme" env="production" />);
    expect((await screen.findByRole("link", { name: "a1f9c2e" })).getAttribute("href")).toBe("/p/acme/production/s/api/deploys/dep_1");
    expect(screen.getByText("Merge #379 · 2 h ago")).toBeTruthy();
    expect(screen.getByText("No address: it calls out")).toBeTruthy();
    expect(screen.getByText("db · private network")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Copy the address of api" })).toBeTruthy();
  });
  it("filters by tag", async () => {
    const tagged = structuredClone(tree);
    tagged[0].envs[0].services[0] = { ...tagged[0].envs[0].services[0], tags: ["frontend"] } as never;
    fakeApi({ "GET /api/projects": tagged });
    render(<Project project="acme" env="production" />);
    await screen.findByText("worker");
    expect(screen.getByText("frontend", { selector: "span" })).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Tag"), { target: { value: "frontend" } });
    expect(screen.queryByText("worker")).toBeNull();
    expect(screen.getByText("api")).toBeTruthy();
  });
  it("downloads the export, or says why not", async () => {
    const saved: string[] = [];
    URL.createObjectURL = (b: Blob) => (b.text().then((t) => saved.push(t)), "blob:x");
    URL.revokeObjectURL = () => {};
    let refuse = false;
    fakeApi({ "GET /api/projects": tree,
      "GET /api/environments/acme/production/export": () => refuse ? fail(403, "Viewers cannot export.") : new Response("project: acme\n", { status: 200 }) });
    render(<Project project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Export yawl.yaml" }));
    await waitFor(() => expect(saved).toEqual(["project: acme\n"]));
    refuse = true;
    fireEvent.click(screen.getByRole("button", { name: "More actions" }));
    fireEvent.click(screen.getByRole("menuitem", { name: "Export yawl.yaml" }));
    await screen.findByText("Viewers cannot export.");
  });
});
