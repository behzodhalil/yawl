import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { Gitea } from "./Gitea";

describe("Gitea (design record 34)", () => {
  it("connects a host, shows its webhook and secret once, and forgets it", async () => {
    let hosts: unknown[] = [];
    const calls = fakeApi({
      "GET /api/settings/gitea": () => hosts,
      "PUT /api/settings/gitea/git.acme.uz": () => ((hosts = [{ host: "git.acme.uz", webhook_url: "https://panel/gitea/webhook" }]),
        { host: "git.acme.uz", webhook_url: "https://panel/gitea/webhook", secret: "s3cr3t" }),
      "DELETE /api/settings/gitea/git.acme.uz": () => ((hosts = []), null),
    });
    render(<Gitea />);
    fireEvent.change(await screen.findByLabelText("Host"), { target: { value: "git.acme.uz" } });
    fireEvent.change(screen.getByLabelText("Access token"), { target: { value: "glpat-x" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect((await screen.findByText("s3cr3t")).tagName).toBe("CODE");
    expect(screen.getByText("Webhook URL")).toBeTruthy();
    expect(screen.getByText("Secret")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Copy" }).length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("https://panel/gitea/webhook").length).toBeGreaterThan(0);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ token: "glpat-x" });
    fireEvent.click(await screen.findByRole("button", { name: "Forget git.acme.uz" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, forget" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });


  it("asks before it forgets a host", async () => {
    const calls = fakeApi({ "GET /api/settings/gitea": [{ host: "git.acme.uz", webhook_url: "https://panel/gitea/webhook" }], "DELETE /api/settings/gitea/git.acme.uz": null });
    render(<Gitea />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Forget git.acme.uz" }));
    expect(await screen.findByText("Forget git.acme.uz?")).toBeTruthy();
    expect(screen.getByText(/stop deploying on push/)).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Forget git.acme.uz" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, forget" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/gitea/git.acme.uz", body: undefined }]));
  });
});

describe("Gitea test connection", () => {
  const routes = (r: unknown) => ({ "GET /api/settings/gitea": [{ host: "git.acme.uz", webhook_url: "https://panel/gitea/webhook" }], "POST /api/settings/gitea/git.acme.uz/test": r });
  const posts = (c: { method: string }[]) => c.filter((x) => x.method === "POST");
  it("tests what is saved and says Connected.", async () => {
    const calls = fakeApi(routes(null));
    render(<Gitea />);
    fireEvent.click(await screen.findByRole("button", { name: "Test git.acme.uz" }));
    expect(await screen.findByText("Connected.")).toBeTruthy();
    expect(posts(calls)).toEqual([{ method: "POST", url: "/api/settings/gitea/git.acme.uz/test", body: undefined }]);
  });
  it("shows the far end's error", async () => {
    fakeApi(routes(fail(502, "Gitea refused the token: 401")));
    render(<Gitea />);
    fireEvent.click(await screen.findByRole("button", { name: "Test git.acme.uz" }));
    expect(await screen.findByText("Gitea refused the token: 401")).toBeTruthy();
  });
  it("sends one POST for two same-tick clicks", async () => {
    const calls = fakeApi(routes(new Promise(() => {})));
    render(<Gitea />);
    const b = await screen.findByRole("button", { name: "Test git.acme.uz" });
    fireEvent.click(b);
    fireEvent.click(b);
    expect(posts(calls)).toHaveLength(1);
    expect(await screen.findByText("Testing…")).toBeTruthy();
  });
});
