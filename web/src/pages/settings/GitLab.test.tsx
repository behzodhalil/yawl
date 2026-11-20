import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { GitLab } from "./GitLab";

describe("GitLab (design record 34)", () => {
  it("connects a host, shows its webhook and secret once, and forgets it", async () => {
    let hosts: unknown[] = [];
    const calls = fakeApi({
      "GET /api/settings/gitlab": () => hosts,
      "PUT /api/settings/gitlab/gitlab.acme.uz": () => ((hosts = [{ host: "gitlab.acme.uz", webhook_url: "https://panel/gitlab/webhook" }]),
        { host: "gitlab.acme.uz", webhook_url: "https://panel/gitlab/webhook", secret: "s3cr3t" }),
      "DELETE /api/settings/gitlab/gitlab.acme.uz": () => ((hosts = []), null),
    });
    render(<GitLab />);
    fireEvent.change(await screen.findByLabelText("Host"), { target: { value: "gitlab.acme.uz" } });
    fireEvent.change(screen.getByLabelText("Access token"), { target: { value: "glpat-x" } });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));
    expect((await screen.findByText("s3cr3t")).tagName).toBe("CODE");
    expect(screen.getByText("Webhook URL")).toBeTruthy();
    expect(screen.getByText("Secret")).toBeTruthy();
    expect(screen.getAllByRole("button", { name: "Copy" }).length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText("https://panel/gitlab/webhook").length).toBeGreaterThan(0);
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ token: "glpat-x" });
    fireEvent.click(await screen.findByRole("button", { name: "Forget gitlab.acme.uz" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, forget" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("says when GitLab refused commit statuses (design record 37)", async () => {
    fakeApi({ "GET /api/settings/gitlab": [], "GET /api/settings/commit-status": { github: "", gitlab: "gitlab.com/a/b: GitLab refused the token: 403" } });
    render(<GitLab />);
    expect(await screen.findByText(/give the token the api scope/)).toBeTruthy();
  });

  it("says when the panel has no address GitLab can call (record 34 review M2)", async () => {
    fakeApi({ "GET /api/settings/gitlab": [{ host: "gitlab.com", webhook_url: "" }], "GET /api/settings/commit-status": { github: "", gitlab: "" } });
    render(<GitLab />);
    expect(await screen.findByText(/GitLab cannot reach this panel's address/)).toBeTruthy();
  });

  it("says which services still build from a forgotten host (record 34 review M5)", async () => {
    fakeApi({
      "GET /api/settings/gitlab": [{ host: "gitlab.com", webhook_url: "https://panel/gitlab/webhook" }],
      "GET /api/settings/commit-status": { github: "", gitlab: "" },
      "DELETE /api/settings/gitlab/gitlab.com": { note: "Forgotten. acme/production/shop still build from gitlab.com; a private repository there no longer clones." },
    });
    render(<GitLab />);
    fireEvent.click(await screen.findByRole("button", { name: "Forget gitlab.com" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, forget" }));
    expect(await screen.findByText(/acme\/production\/shop still build from gitlab.com/)).toBeTruthy();
  });

  it("asks before it forgets a host", async () => {
    const calls = fakeApi({ "GET /api/settings/gitlab": [{ host: "gitlab.acme.uz", webhook_url: "https://panel/gitlab/webhook" }], "GET /api/settings/commit-status": { github: "", gitlab: "" }, "DELETE /api/settings/gitlab/gitlab.acme.uz": null });
    render(<GitLab />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Forget gitlab.acme.uz" }));
    expect(await screen.findByText("Forget gitlab.acme.uz?")).toBeTruthy();
    expect(screen.getByText(/stop deploying on push/)).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Forget gitlab.acme.uz" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, forget" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/gitlab/gitlab.acme.uz", body: undefined }]));
  });
});

describe("GitLab test connection", () => {
  const routes = (r: unknown) => ({ "GET /api/settings/gitlab": [{ host: "gitlab.com", webhook_url: "" }], "GET /api/settings/commit-status": { github: "", gitlab: "" }, "POST /api/settings/gitlab/gitlab.com/test": r });
  const posts = (c: { method: string }[]) => c.filter((x) => x.method === "POST");
  it("tests what is saved and says Connected.", async () => {
    const calls = fakeApi(routes(null));
    render(<GitLab />);
    fireEvent.click(await screen.findByRole("button", { name: "Test gitlab.com" }));
    expect(await screen.findByText("Connected.")).toBeTruthy();
    expect(posts(calls)).toEqual([{ method: "POST", url: "/api/settings/gitlab/gitlab.com/test", body: undefined }]);
  });
  it("shows the far end's error", async () => {
    fakeApi(routes(fail(502, "GitLab refused the token: 401")));
    render(<GitLab />);
    fireEvent.click(await screen.findByRole("button", { name: "Test gitlab.com" }));
    expect(await screen.findByText("GitLab refused the token: 401")).toBeTruthy();
  });
  it("sends one POST for two same-tick clicks", async () => {
    const calls = fakeApi(routes(new Promise(() => {})));
    render(<GitLab />);
    const b = await screen.findByRole("button", { name: "Test gitlab.com" });
    fireEvent.click(b);
    fireEvent.click(b);
    expect(posts(calls)).toHaveLength(1);
    expect(await screen.findByText("Testing…")).toBeTruthy();
  });
});
