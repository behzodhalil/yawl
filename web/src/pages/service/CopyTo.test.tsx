import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { chooseOption, fakeApi } from "../../test-api";
import { CopyTo } from "./CopyTo";

const S = "/api/services/acme/production/api";

describe("CopyTo", () => {
  it("shows the plan, then copies to the environment chosen", async () => {
    const calls = fakeApi({
      "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }, { name: "staging", server: "hel1", services: [] }] }],
      [`POST ${S}/copy`]: (body: unknown, url: string) => url.includes("dry=1")
        ? { lines: ["copy api to acme/staging, not deployed until you deploy it"], vars: [] }
        : { service: "svc_9", path: "/p/acme/staging/s/api" },
    });
    render(<CopyTo project="acme" env="production" service="api" />);
    await screen.findByRole("combobox", { name: "To" });
    await chooseOption("To", "acme/staging");
    await screen.findByText("copy api to acme/staging, not deployed until you deploy it");
    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1")).length).toBe(1));
    expect(calls.find((c) => c.method === "POST" && !c.url.includes("dry=1"))!.body).toEqual({ project: "acme", env: "staging", move: false, boxes: {} });
    expect(await screen.findByRole("link", { name: "Open the copy" })).toBeTruthy();
  });

  const routes = (calls?: unknown) => ({
    "GET /api/projects": [{ name: "acme", envs: [{ name: "production", server: "hel1", services: [] }, { name: "staging", server: "hel1", services: [] }] }],
    [`POST ${S}/copy`]: (_b: unknown, url: string) => url.includes("dry=1")
      ? { lines: ["copy api"], vars: [] }
      : { service: "svc_9", path: "/p/acme/staging/s/api" },
  });

  it("has no inline max width", async () => {
    fakeApi(routes());
    const { container } = render(<CopyTo project="acme" env="production" service="api" />);
    await screen.findByLabelText("To");
    expect((container.firstChild as HTMLElement).style.maxWidth).toBe("");
  });

  it("asks before a move and sends it once", async () => {
    const calls = fakeApi(routes());
    render(<CopyTo project="acme" env="production" service="api" />);
    await screen.findByRole("combobox", { name: "To" });
    await chooseOption("To", "acme/staging");
    await screen.findByText("copy api");
    fireEvent.click(screen.getByRole("button", { name: /^Move/ }));
    expect(screen.getByRole("dialog", { name: "Move api to acme/staging?" })).toBeTruthy();
    expect(screen.getByText("The original goes to the trash.")).toBeTruthy();
    expect(calls.some((c) => c.method === "POST" && !c.url.includes("dry=1"))).toBe(false);
    const yes = screen.getByRole("button", { name: "Yes, move" });
    fireEvent.click(yes);
    fireEvent.click(yes);
    await screen.findByRole("link", { name: "Open the copy" });
    const posts = calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1"));
    expect(posts).toHaveLength(1);
    expect(posts[0].body).toMatchObject({ move: true });
  });

  it("sends a double-clicked Copy once", async () => {
    const calls = fakeApi(routes());
    render(<CopyTo project="acme" env="production" service="api" />);
    await screen.findByRole("combobox", { name: "To" });
    await chooseOption("To", "acme/staging");
    await screen.findByText("copy api");
    const b = screen.getByRole("button", { name: "Copy" });
    fireEvent.click(b);
    fireEvent.click(b);
    await screen.findByRole("link", { name: "Open the copy" });
    expect(calls.filter((c) => c.method === "POST" && !c.url.includes("dry=1"))).toHaveLength(1);
  });
});
