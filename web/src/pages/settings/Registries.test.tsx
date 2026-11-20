import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../../test-api";
import { Registries } from "./Registries";

describe("Registries", () => {
  it("lists, adds and removes logins", async () => {
    let list = [{ host: "ghcr.io", username: "acme" }];
    const calls = fakeApi({
      "GET /api/registries": () => list,
      "PUT /api/registries/docker.io": () => ((list = [...list, { host: "docker.io", username: "me" }]), null),
      "DELETE /api/registries/ghcr.io": () => ((list = list.filter((r) => r.host !== "ghcr.io")), null),
    });
    render(<Registries />);
    expect(await screen.findByText("ghcr.io")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Host"), { target: { value: "docker.io" } });
    fireEvent.change(screen.getByLabelText("User"), { target: { value: "me" } });
    const token = screen.getByLabelText("Token") as HTMLInputElement;
    expect(token.type).toBe("password");
    fireEvent.change(token, { target: { value: "dckr_secret" } });
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    expect(await screen.findByText("docker.io")).toBeTruthy();
    expect(calls.find((c) => c.method === "PUT")!.body).toEqual({ username: "me", token: "dckr_secret" });
    await waitFor(() => expect((screen.getByLabelText("Token") as HTMLInputElement).value).toBe(""));
    fireEvent.click(screen.getByRole("button", { name: "Remove ghcr.io" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(screen.queryByText("ghcr.io")).toBeNull());
  });

  it("asks before it removes a registry", async () => {
    const calls = fakeApi({ "GET /api/registries": [{ host: "ghcr.io", username: "me" }], "DELETE /api/registries/ghcr.io": null });
    render(<Registries />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Remove ghcr.io" }));
    expect(await screen.findByText("Remove ghcr.io?")).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Remove ghcr.io" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/registries/ghcr.io", body: undefined }]));
  });
});

describe("Registries test connection", () => {
  const routes = (r: unknown) => ({ "GET /api/registries": [{ host: "ghcr.io", username: "me" }], "POST /api/registries/ghcr.io/test": r });
  const posts = (c: { method: string }[]) => c.filter((x) => x.method === "POST");
  it("tests what is saved and says Connected.", async () => {
    const calls = fakeApi(routes(null));
    render(<Registries />);
    fireEvent.click(await screen.findByRole("button", { name: "Test ghcr.io" }));
    expect(await screen.findByText("Connected.")).toBeTruthy();
    expect(posts(calls)).toEqual([{ method: "POST", url: "/api/registries/ghcr.io/test", body: undefined }]);
  });
  it("shows the far end's error", async () => {
    fakeApi(routes(fail(502, "The registry refused this login.")));
    render(<Registries />);
    fireEvent.click(await screen.findByRole("button", { name: "Test ghcr.io" }));
    expect(await screen.findByText("The registry refused this login.")).toBeTruthy();
  });
  it("sends one POST for two same-tick clicks", async () => {
    const calls = fakeApi(routes(new Promise(() => {})));
    render(<Registries />);
    const b = await screen.findByRole("button", { name: "Test ghcr.io" });
    fireEvent.click(b);
    fireEvent.click(b);
    expect(posts(calls)).toHaveLength(1);
    expect(await screen.findByText("Testing…")).toBeTruthy();
  });
});
