import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fail, fakeApi } from "../../test-api";
import { Notifications } from "./Notifications";

const none = { slack: { set: false, host: "" }, discord: { set: false, host: "" }, webhook: { set: false, host: "" }, ntfy: { set: false, host: "" }, email: { set: false, host: "", port: "", username: "", from: "", to: "" } };

describe("Notifications", () => {
  it("sets Slack, tests it and removes it", async () => {
    let view: unknown = none;
    const calls = fakeApi({
      "GET /api/settings/notifications": () => view,
      "PUT /api/settings/notifications/slack": () => ((view = { ...none, slack: { set: true, host: "hooks.slack.com" } }), { set: true }),
      "POST /api/settings/notifications/slack/test": null,
      "DELETE /api/settings/notifications/slack": () => ((view = none), null),
    });
    render(<Notifications />);
    expect((await screen.findByLabelText("Slack webhook URL")).getAttribute("type")).toBe("url");
    fireEvent.change(await screen.findByLabelText("Slack webhook URL"), { target: { value: "https://hooks.slack.com/services/T/B/x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Slack" }));
    expect(await screen.findByText("Posts to hooks.slack.com.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Send a test to Slack" }));
    expect(await screen.findByText("Sent. Look in Slack.")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Remove Slack" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(calls.some((c) => c.method === "DELETE")).toBe(true));
  });

  it("saves the email settings and shows the server's error", async () => {
    const calls = fakeApi({
      "GET /api/settings/notifications": none,
      "PUT /api/settings/notifications/email": fail(400, "me@ is not an email address."),
    });
    render(<Notifications />);
    for (const [label, v] of [["SMTP host", "smtp.acme.uz"], ["Port", "587"], ["Username", "yawl"], ["Password", "pw"], ["From", "yawl@acme.uz"], ["To", "me@"]]) {
      fireEvent.change(await screen.findByLabelText(label), { target: { value: v } });
    }
    fireEvent.click(screen.getByRole("button", { name: "Save email" }));
    expect(await screen.findByText("me@ is not an email address.")).toBeTruthy();
    expect(calls.find((c) => c.method === "PUT")?.body).toEqual({ host: "smtp.acme.uz", port: "587", username: "yawl", password: "pw", from: "yawl@acme.uz", to: "me@" });
  });

  it("sets a webhook and an ntfy topic", async () => {
    const calls = fakeApi({
      "GET /api/settings/notifications": none,
      "PUT /api/settings/notifications/webhook": { set: true },
      "PUT /api/settings/notifications/ntfy": { set: true },
    });
    render(<Notifications />);
    fireEvent.change(await screen.findByLabelText("Webhook URL"), { target: { value: "https://n8n.acme.uz/webhook/x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Webhook" }));
    fireEvent.change(screen.getByLabelText("ntfy topic URL"), { target: { value: "https://ntfy.sh/acme" } });
    fireEvent.click(screen.getByRole("button", { name: "Save ntfy" }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PUT").map((c) => c.url)).toEqual(["/api/settings/notifications/webhook", "/api/settings/notifications/ntfy"]));
  });

  it("empties the email form after Remove", async () => {
    const mail = { set: true, host: "smtp.acme.uz", port: "587", username: "yawl", from: "yawl@acme.uz", to: "me@acme.uz" };
    let view: unknown = { ...none, email: mail };
    fakeApi({
      "GET /api/settings/notifications": () => view,
      "DELETE /api/settings/notifications/email": () => ((view = none), null),
    });
    render(<Notifications />);
    expect(((await screen.findByLabelText("SMTP host")) as HTMLInputElement).value).toBe("smtp.acme.uz");
    fireEvent.click(screen.getByRole("button", { name: "Remove email" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect((screen.getByLabelText("SMTP host") as HTMLInputElement).value).toBe(""));
    expect((screen.getByLabelText("To") as HTMLInputElement).value).toBe("");
  });

  it("asks before it removes a channel", async () => {
    const calls = fakeApi({ "GET /api/settings/notifications": { ...none, slack: { set: true, host: "hooks.slack.com" } }, "DELETE /api/settings/notifications/slack": null });
    render(<Notifications />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Remove Slack" }));
    expect(await screen.findByText("Remove Slack?")).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Remove Slack" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/notifications/slack", body: undefined }]));
  });

  it("asks before it removes email", async () => {
    const calls = fakeApi({ "GET /api/settings/notifications": { ...none, email: { set: true, host: "smtp.acme.uz", port: "587", username: "y", from: "y@acme.uz", to: "me@acme.uz" } }, "DELETE /api/settings/notifications/email": null });
    render(<Notifications />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Remove email" }));
    expect(await screen.findByText("Remove email?")).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Remove email" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, remove" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/settings/notifications/email", body: undefined }]));
  });
});

describe("Notifications email saved", () => {
  it("says Saved.", async () => {
    fakeApi({ "GET /api/settings/notifications": none, "PUT /api/settings/notifications/email": null });
    render(<Notifications />);
    fireEvent.click(await screen.findByRole("button", { name: "Save email" }));
    expect(await screen.findByText("Saved.")).toBeTruthy();
  });
  it("says Saved. after a Slack save too", async () => {
    fakeApi({ "GET /api/settings/notifications": none, "PUT /api/settings/notifications/slack": null });
    render(<Notifications />);
    fireEvent.change(await screen.findByLabelText("Slack webhook URL"), { target: { value: "https://hooks.slack.com/x" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Slack" }));
    expect(await screen.findByText("Saved.")).toBeTruthy();
  });
});
