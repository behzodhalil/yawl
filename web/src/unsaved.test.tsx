import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { fakeApi } from "./test-api";
import { ServiceSettings } from "./pages/service/Settings";
import { apiView, S } from "./pages/service/fixtures";
import { toasts } from "./ui";

const SAVE = "/api/environments/acme/production/services";
const routes = () => ({
  [`GET ${S}`]: apiView,
  [`POST ${SAVE}?dry=1`]: { changes: [{ op: "update", kind: "service", name: "api", detail: "health_path" }] },
  [`POST ${SAVE}`]: { changes: [] },
  [`POST ${S}/deploys`]: { deploy_id: "dep_9" },
});
const said = vi.spyOn(toasts, "add");
afterEach(() => said.mockClear());
const bar = () => screen.queryByRole("region", { name: "Unsaved changes" });
const posts = (calls: { method: string; url: string }[], url: string) => calls.filter((c) => c.method === "POST" && c.url === url);
const edit = async () => fireEvent.change(await screen.findByLabelText("Health path"), { target: { value: "/up" } });

describe("UnsavedBar on service settings", () => {
  it("shows only while the form differs from what is saved, and Reset puts it back", async () => {
    fakeApi(routes());
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await screen.findByLabelText("Health path");
    expect(bar()).toBeNull();
    await edit();
    expect(within(bar()!).getByText("1 section changed · Runtime")).toBeTruthy();
    fireEvent.click(within(bar()!).getByRole("button", { name: "Reset" }));
    expect((screen.getByLabelText("Health path") as HTMLInputElement).value).toBe("/health");
    expect(bar()).toBeNull();
  });

  it("Save only saves after the plan and starts no deploy", async () => {
    const calls = fakeApi(routes());
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await edit();
    fireEvent.click(within(bar()!).getByRole("button", { name: "Save options" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Save only" }));
    await screen.findByText("update service api health_path");
    fireEvent.click(within(bar()!).getByRole("button", { name: "Apply only" }));
    await waitFor(() => expect(said).toHaveBeenCalledWith(expect.objectContaining({ title: "Saved", description: "It takes effect on the next deploy." })));
    expect(posts(calls, SAVE)).toHaveLength(1);
    expect(posts(calls, `${S}/deploys`)).toHaveLength(0);
  });

  it("Save and deploy saves, then starts exactly one deploy and opens it", async () => {
    const calls = fakeApi(routes());
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await edit();
    fireEvent.click(within(bar()!).getByRole("button", { name: "Save options" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Save and deploy" }));
    await screen.findByText("update service api health_path");
    const apply = within(bar()!).getByRole("button", { name: "Apply and deploy" });
    fireEvent.click(apply);
    await waitFor(() => expect(location.pathname).toBe("/p/acme/production/s/api/deploys/dep_9"));
    expect(posts(calls, SAVE)).toHaveLength(1);
    expect(posts(calls, `${S}/deploys`)).toHaveLength(1);
  });

  it("Cmd+S does the default action, a plain save", async () => {
    const calls = fakeApi(routes());
    render(<ServiceSettings project="acme" env="production" service="api" />);
    await edit();
    fireEvent.keyDown(window, { key: "s", metaKey: true });
    await screen.findByText("update service api health_path");
    fireEvent.keyDown(window, { key: "s", ctrlKey: true });
    await waitFor(() => expect(said).toHaveBeenCalledWith(expect.objectContaining({ title: "Saved", description: "It takes effect on the next deploy." })));
    expect(posts(calls, `${S}/deploys`)).toHaveLength(0);
  });

  it("offers no deploy for a database", async () => {
    fakeApi({ ...routes(), [`GET ${S}`]: { ...apiView, form: { ...apiView.form, kind: "postgres", version: "17" } } });
    render(<ServiceSettings project="acme" env="production" service="api" />);
    fireEvent.change(await screen.findByLabelText("CPU"), { target: { value: "2" } });
    expect(within(bar()!).queryByRole("button", { name: "Save options" })).toBeNull();
  });
});
