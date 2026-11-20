import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { NewService, nameFromImage } from "./NewService";

const tree = [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }] }];
const path = "/api/environments/acme/production/services";

describe("NewService from an image", () => {
  it("asks the image, names the service after it and plans it without a repository", async () => {
    const calls = fakeApi({ "GET /api/projects": tree, [`POST ${path}`]: { changes: [{ op: "create", kind: "service", name: "grafana" }] } });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Web Service/ }));
    fireEvent.click(screen.getByRole("button", { name: "From an image" }));
    expect(screen.queryByRole("button", { name: "Read repository" })).toBeNull();
    fireEvent.change(screen.getByLabelText("Image"), { target: { value: "grafana/grafana:11" } });
    expect((screen.getByLabelText("Name") as HTMLInputElement).value).toBe("grafana");
    fireEvent.click(screen.getByRole("button", { name: "Show the plan" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const body = calls.find((c) => c.method === "POST")!.body as { image: string; repo: string; branch: string };
    expect(body.image).toBe("grafana/grafana:11");
    expect(body.repo).toBe("");
    expect(body.branch).toBe("");
  });

  it("drops the missing keys and the branch when the source becomes an image (review M4, plan 22)", async () => {
    fakeApi({ "GET /api/projects": tree, "POST /api/inspect": { commit: "abc", dockerfile: true, language: "", reads_port: true, port: 0, health_path: "/", missing: ["SECRET_KEY"], warnings: [], builder: "", static: false, notes: [] } });
    render(<NewService project="acme" env="production" />);
    fireEvent.click(await screen.findByRole("button", { name: /Web Service/ }));
    fireEvent.change(screen.getByLabelText("Repository"), { target: { value: "https://github.com/acme/api" } });
    fireEvent.click(screen.getByRole("button", { name: "Read repository" }));
    await screen.findByText("SECRET_KEY");
    fireEvent.click(screen.getByRole("button", { name: "From an image" }));
    expect(screen.queryByText("SECRET_KEY")).toBeNull();
    expect(screen.queryByLabelText("Branch")).toBeNull();
  });

  it("names a service only as the spec takes it (review M5, plan 22)", () => {
    expect(nameFromImage("ghcr.io/acme/api:1")).toBe("api");
    expect(nameFromImage("registry:5000/acme/9lives@sha256:abc")).toBe("lives");
    expect(nameFromImage("acme/" + "a".repeat(30) + "-b:1")).toMatch(/^[a-z][a-z0-9-]{0,30}$/);
    expect(nameFromImage("acme/" + "a".repeat(30) + "-b:1").endsWith("-")).toBe(false);
  });
});
