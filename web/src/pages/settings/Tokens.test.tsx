import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, pressBackdrop } from "../../test-api";
import { Tokens } from "./Tokens";

describe("Tokens", () => {
  it("the new token stays until Done", async () => {
    fakeApi({ "GET /api/tokens": [], "POST /api/tokens": { token: "yawl_tok_abc" } });
    render(<Tokens />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "laptop" } });
    fireEvent.click(screen.getByRole("button", { name: "Create token" }));
    expect(await screen.findByText("yawl_tok_abc")).toBeTruthy();
    fireEvent.keyDown(document.activeElement!, { key: "Escape" });
    pressBackdrop();
    expect(screen.getByText("yawl_tok_abc")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Done" }));
    expect(screen.queryByText("yawl_tok_abc")).toBeNull();
  });

  it("asks before it deletes a token", async () => {
    const calls = fakeApi({ "GET /api/tokens": [{ id: "t1", name: "laptop", created_at: 1 }], "DELETE /api/tokens/t1": null });
    render(<Tokens />);
    const sent = () => calls.filter((c) => c.method !== "GET");
    fireEvent.click(await screen.findByRole("button", { name: "Delete laptop" }));
    expect(await screen.findByText('Delete the token "laptop"?')).toBeTruthy();
    expect(sent()).toHaveLength(0);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(sent()).toHaveLength(0);
    fireEvent.click(await screen.findByRole("button", { name: "Delete laptop" }));
    fireEvent.click(await screen.findByRole("button", { name: "Yes, delete" }));
    await waitFor(() => expect(sent()).toEqual([{ method: "DELETE", url: "/api/tokens/t1", body: undefined }]));
  });
});

describe("Tokens single submit", () => {
  it("sends one request for two clicks", async () => {
    const calls = fakeApi({ "GET /api/tokens": [], "POST /api/tokens": new Promise(() => {}) });
    render(<Tokens />);
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "laptop" } });
    const b = screen.getByRole("button", { name: "Create token" });
    fireEvent.click(b);
    fireEvent.click(b);
    await waitFor(() => expect(calls.filter((c) => c.method === "POST")).toHaveLength(1));
  });
});
