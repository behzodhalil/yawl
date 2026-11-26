import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "./test-api";
import { Palette, readRecent, recordRecent } from "./palette";

const tree = (stopped = false) => [{
  name: "shop",
  envs: [{ name: "production", server: "hel1", services: [
    { project: "shop", env: "production", service: "api", kind: "web", state: "live", health: "ok", stopped },
    { project: "shop", env: "production", service: "db", kind: "postgres", state: "live", health: "ok", stopped },
  ] }],
}];
const base = "/p/shop/production/s/api";
const visit = (service: string) => ({ project: "shop", env: "production", service, kind: "web" });
const pages = [{ to: "/", label: "Projects", icon: "layers" }];
const open = (box = true) => {
  render(<Palette pages={pages} onClose={() => {}} />);
  return box ? screen.getByRole("combobox") : null;
};
const labels = () => screen.getAllByRole("option").map((o) => o.textContent);

beforeEach(() => {
  localStorage.clear();
  history.replaceState(null, "", "/");
});

describe("recent services", () => {
  it("keeps the newest five without repeats", () => {
    for (const s of ["a", "b", "c", "d", "e", "f", "c"]) recordRecent(visit(s));
    expect(readRecent().map((v) => v.service)).toEqual(["c", "f", "e", "d", "b"]);
  });

  it("works when storage throws", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => { throw new Error("off"); });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("off"); });
    expect(() => recordRecent(visit("a"))).not.toThrow();
    expect(readRecent()).toEqual([]);
    vi.restoreAllMocks();
  });

  it("ignores garbage in storage", () => {
    localStorage.setItem("yawl.recent", JSON.stringify([{ service: 1 }, "x", visit("ok")]));
    expect(readRecent().map((v) => v.service)).toEqual(["ok"]);
  });
});

describe("Palette", () => {
  it("shows Recent then Pages when empty, with headings that are not options", async () => {
    fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [] });
    recordRecent(visit("api"));
    open();
    expect(labels()).toEqual(["apishop / production", "Projects"]);
    expect(screen.getByText("Recent").closest("[role=option]")).toBeNull();
    expect(screen.getByText("Pages").closest("[role=option]")).toBeNull();
    expect(screen.queryByText("Deploy api")).toBeNull();
  });

  it("matches an action by its label", async () => {
    fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [] });
    const box = open();
    fireEvent.change(box!, { target: { value: "deploy api" } });
    expect(await screen.findByText("Deploy api")).toBeTruthy();
    expect(screen.queryByText("Restart api")).toBeNull();
    expect(screen.getByText("Actions").closest("[role=option]")).toBeNull();
  });

  it("deploys and opens the deploy", async () => {
    const calls = fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [], "POST /api/services/shop/production/api/deploys": { deploy_id: "d1" } });
    const box = open();
    fireEvent.change(box!, { target: { value: "deploy api" } });
    fireEvent.click(await screen.findByText("Deploy api"));
    await waitFor(() => expect(location.pathname).toBe(base + "/deploys/d1"));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ commit: "" });
  });

  it("restarts and opens the service", async () => {
    const calls = fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [], "POST /api/services/shop/production/api/restart": null });
    const box = open();
    fireEvent.change(box!, { target: { value: "restart api" } });
    fireEvent.click(await screen.findByText("Restart api"));
    await waitFor(() => expect(location.pathname).toBe(base));
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/restart"))).toBe(true);
  });

  it("opens the logs tab", async () => {
    fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [] });
    const box = open();
    fireEvent.change(box!, { target: { value: "logs" } });
    fireEvent.click(await screen.findByText("View logs for api"));
    await waitFor(() => expect(location.pathname).toBe(base + "/logs"));
  });

  it("gives a database no deploy and no logs", async () => {
    fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [] });
    const box = open();
    fireEvent.change(box!, { target: { value: "db" } });
    expect(await screen.findByText("Restart db")).toBeTruthy();
    expect(screen.queryByText("Deploy db")).toBeNull();
    expect(screen.queryByText("View logs for db")).toBeNull();
  });

  it("gives a stopped service no deploy and no restart, but its logs", async () => {
    fakeApi({ "GET /api/projects": tree(true), "GET /api/servers": [] });
    const box = open();
    fireEvent.change(box!, { target: { value: "api" } });
    expect(await screen.findByText("View logs for api")).toBeTruthy();
    expect(screen.queryByText("Deploy api")).toBeNull();
    expect(screen.queryByText("Restart api")).toBeNull();
  });

  it("Enter on a service name opens the service and sends nothing", async () => {
    const calls = fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [] });
    const box = open();
    fireEvent.change(box!, { target: { value: "api" } });
    await screen.findByText("Deploy api");
    expect(labels()[0]).toBe("apishop / production");
    fireEvent.keyDown(box!, { key: "Enter" });
    await waitFor(() => expect(location.pathname).toBe(base));
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("Enter on deploy and a name deploys", async () => {
    const calls = fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [], "POST /api/services/shop/production/api/deploys": { deploy_id: "d1" } });
    const box = open();
    fireEvent.change(box!, { target: { value: "deploy api" } });
    await screen.findByText("Deploy api");
    fireEvent.keyDown(box!, { key: "Enter" });
    await waitFor(() => expect(location.pathname).toBe(base + "/deploys/d1"));
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/deploys"))).toBe(true);
  });

  it("does not navigate when closed while the action runs", async () => {
    let finish!: () => void;
    fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [], "POST /api/services/shop/production/api/restart": () => new Promise((r) => { finish = () => r(null); }) });
    const { unmount } = render(<Palette pages={pages} onClose={() => {}} />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "restart api" } });
    fireEvent.click(await screen.findByText("Restart api"));
    await waitFor(() => expect(finish).toBeTypeOf("function"));
    unmount();
    finish();
    await new Promise((r) => setTimeout(r, 20));
    expect(location.pathname).toBe("/");
  });

  it("shows the error and stays open when an action fails", async () => {
    fakeApi({ "GET /api/projects": tree(), "GET /api/servers": [], "POST /api/services/shop/production/api/deploys": () => fail(500, "no builder") });
    const onClose = vi.fn();
    render(<Palette pages={pages} onClose={onClose} />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "deploy api" } });
    fireEvent.click(await screen.findByText("Deploy api"));
    expect((await screen.findByRole("alert")).textContent).toContain("no builder");
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByText("Deploy api")).toBeTruthy();
  });

  it("says why a list did not load instead of looking forever", async () => {
    fakeApi({ "GET /api/projects": [], "GET /api/servers": () => fail(500, "servers are down") });
    render(<Palette pages={[]} onClose={() => {}} />);
    fireEvent.change(screen.getByRole("combobox"), { target: { value: "zzz" } });
    expect(await screen.findByText("servers are down")).toBeTruthy();
    expect(screen.queryByText("Looking…")).toBeNull();
  });

  it("is a list autocomplete that announces how many places match", async () => {
    fakeApi({ "GET /api/projects": [], "GET /api/servers": [{ name: "hel1" }, { name: "fsn1" }] });
    render(<Palette pages={[{ to: "/", label: "Projects", icon: "layers" }]} onClose={() => {}} />);
    const box = screen.getByRole("combobox");
    expect(box.getAttribute("aria-autocomplete")).toBe("list");
    fireEvent.change(box, { target: { value: "1" } });
    expect(await screen.findByText("2 results")).toBeTruthy();
    fireEvent.change(box, { target: { value: "hel" } });
    expect(screen.getByText("1 result").getAttribute("aria-live")).toBe("polite");
  });
});
