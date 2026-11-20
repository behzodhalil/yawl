import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../test-api";
import { CliLogin } from "./CliLogin";

describe("CliLogin", () => {
  it("shows the host, and approves", async () => {
    history.replaceState(null, "", "/cli-login#ABCDEFGHJK");
    const calls = fakeApi({
      "GET /api/cli-login/ABCDEFGHJK/info": { host: "laptop", from: "203.0.113.9", at: Date.now(), approved: false },
      "POST /api/cli-login/ABCDEFGHJK/approve": { check: "QRST2345" },
    });
    render(<CliLogin />);
    await screen.findByText(/calling itself laptop/);
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await screen.findByText("QRST2345");
    expect(calls.some((c) => c.method === "POST" && c.url.endsWith("/approve"))).toBe(true);
  });
  it("shows where the code came from", async () => {
    history.replaceState(null, "", "/cli-login#ABCDEFGHJK");
    fakeApi({
      "GET /api/cli-login/ABCDEFGHJK/info": { host: "laptop", from: "203.0.113.9", at: Date.now() - 2 * 60_000, approved: false },
    });
    render(<CliLogin />);
    await screen.findByText("A computer at 203.0.113.9, calling itself laptop, asked 2 min ago.");
  });
  it("reopened after approving, it never takes the CLI's token", async () => {
    history.replaceState(null, "", "/cli-login#ABCDEFGHJK");
    const calls = fakeApi({ "GET /api/cli-login/ABCDEFGHJK/info": { host: "laptop", from: "203.0.113.9", at: Date.now(), approved: true } });
    render(<CliLogin />);
    await screen.findByText("Already approved. If the terminal still waits, run yawl login again.");
    expect(calls.every((c) => c.url.endsWith("/info"))).toBe(true);
  });
  it("approving shows the code to type back, never the link's", async () => {
    history.replaceState(null, "", "/cli-login#ABCDEFGHJK");
    fakeApi({
      "GET /api/cli-login/ABCDEFGHJK/info": { host: "laptop", from: "203.0.113.9", at: Date.now(), approved: false },
      "POST /api/cli-login/ABCDEFGHJK/approve": { check: "QRST2345" },
    });
    render(<CliLogin />);
    await screen.findByText(/calling itself laptop/);
    expect(screen.queryByText("ABCDEFGHJK")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Approve" }));
    await screen.findByText("QRST2345");
    expect(screen.getByText("Type this code into the terminal that printed the link:")).toBeTruthy();
    expect(screen.getByText("Never send it to anyone: whoever types it signs in as you.")).toBeTruthy();
  });
  it("an unknown code", async () => {
    history.replaceState(null, "", "/cli-login#ZZZZZZZZZZ");
    fakeApi({ "GET /api/cli-login/ZZZZZZZZZZ/info": new Response("This code has expired or was already used.", { status: 410 }) });
    render(<CliLogin />);
    await screen.findByText("This code has expired or was already used.");
  });
});
