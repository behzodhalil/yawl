import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { Projects } from "./Projects";

describe("Projects", () => {
  it("empty", async () => {
    fakeApi({ "GET /api/projects": [] });
    render(<Projects />);
    await screen.findByText("No projects yet.");
    expect(screen.getAllByText("New project").length).toBeGreaterThan(0);
  });
  it("lists projects with their service counts", async () => {
    fakeApi({
      "GET /api/projects": [
        { name: "acme", envs: [{ name: "production", server: "hel1", services: [{ service: "api" }, { service: "db" }] }, { name: "staging", server: "hk1", services: [{ service: "api" }] }] },
        { name: "shop", envs: [{ name: "production", server: "hel1", services: [] }] },
      ],
    });
    render(<Projects />);
    await screen.findByText("acme");
    expect(screen.getByText("3 services, production and staging")).toBeTruthy();
    expect(screen.getByText("No services yet, production")).toBeTruthy();
  });
  it("pages and searches many projects", async () => {
    const many = Array.from({ length: 25 }, (_, i) => ({ name: `p${String(i).padStart(2, "0")}`, envs: [{ name: "production", server: "hel1", services: i === 7 ? [{ service: "billing" }] : [] }] }));
    many[3].name = "api-core";
    fakeApi({ "GET /api/projects": many });
    render(<Projects />);
    await screen.findByText("25 projects");
    expect(screen.getAllByText(/^(p\d\d|api-core)$/).length).toBe(20);
    fireEvent.click(screen.getByRole("button", { name: /Next/ }));
    expect(screen.getAllByText(/^(p\d\d|api-core)$/).length).toBe(5);
    fireEvent.change(screen.getByLabelText("Search projects"), { target: { value: "api" } });
    expect(screen.getByText("1 projects")).toBeTruthy();
    expect(screen.getByText("api-core")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Search projects"), { target: { value: "billing" } });
    expect(screen.getByText("p07")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Search projects"), { target: { value: "zzz" } });
    expect(screen.getByText("Nothing matches the search.")).toBeTruthy();
  });
});
