import { describe, expect, it, vi } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi, fail } from "../test-api";
import { Adopt } from "./Adopt";
import { Shell } from "../Shell";
import { SessionContext } from "../session";

describe("Adopt", () => {
  it("lists the lines and accepts", async () => {
    let n = 0;
    const calls = fakeApi({
      "GET /api/adopt": () => (n++ === 0 ? { adopting: true, lines: ["acme-api runs deploy dep_81, the server last knew dep_79."] } : { adopting: false, lines: [] }),
      "POST /api/adopt": null,
    });
    render(<Adopt />);
    await screen.findByText("acme-api runs deploy dep_81, the server last knew dep_79.");
    fireEvent.click(screen.getByRole("button", { name: "Accept what the servers run" }));
    await screen.findByText("Adopted. The panel acts on the servers again.");
    expect(calls.some((c) => c.method === "POST")).toBe(true);
  });
  it("sends what it showed", async () => {
    const calls = fakeApi({
      "GET /api/adopt": { adopting: true, lines: ["acme-api runs deploy dep_81, the server last knew dep_79."], seen: "abc123" },
      "POST /api/adopt": null,
    });
    render(<Adopt />);
    fireEvent.click(await screen.findByRole("button", { name: "Accept what the servers run" }));
    await screen.findByText("Adopted. The panel acts on the servers again.");
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ without: [], seen: "abc123" });
  });
  it("shows a refusal", async () => {
    fakeApi({ "GET /api/adopt": { adopting: true, lines: ["hk1 has not connected yet."] }, "POST /api/adopt": fail(409, "Some servers have not reported yet: hk1.") });
    render(<Adopt />);
    fireEvent.click(await screen.findByRole("button", { name: "Accept what the servers run" }));
    await screen.findByText("Some servers have not reported yet: hk1.");
  });
  it("the banner shows on every page while adopting", () => {
    render(<SessionContext.Provider value={{ claimed: true, adopting: true }}><Shell crumb={["x"]} active="/">body</Shell></SessionContext.Provider>);
    expect(screen.getByText("This panel was restored from a backup. It changes nothing until you review what the servers run.")).toBeTruthy();
    expect(screen.getByRole("link", { name: "Review" }).getAttribute("href")).toBe("/adopt");
  });
});

describe("Adopt without a dead machine", () => {
  it("offers to accept without servers that never connected, and says so", async () => {
    const calls = fakeApi({
      "GET /api/adopt": { adopting: true, lines: ["hel1 has not connected yet."] },
      "POST /api/adopt": null,
    });
    render(<Adopt />);
    fireEvent.click(await screen.findByRole("button", { name: "Accept without hel1" }));
    await screen.findByText("Adopted. The panel acts on the servers again.");
    expect(calls.find((c) => c.method === "POST")!.body).toEqual({ without: ["hel1"] });
  });
});

describe("Adopt accept", () => {
  it("accepting refreshes the session", async () => {
    const reload = vi.fn();
    fakeApi({ "GET /api/adopt": { adopting: true, lines: ["x"] }, "POST /api/adopt": null });
    render(<SessionContext.Provider value={{ claimed: true, adopting: true, reload }}><Adopt /></SessionContext.Provider>);
    fireEvent.click(await screen.findByRole("button", { name: "Accept what the servers run" }));
    await screen.findByText("Adopted. The panel acts on the servers again.");
    expect(reload).toHaveBeenCalledTimes(1);
  });
  it("Accept runs once and is disabled while it runs", async () => {
    let release!: () => void;
    const calls = fakeApi({
      "GET /api/adopt": { adopting: true, lines: ["hel1 has not connected yet."] },
      "POST /api/adopt": () => new Promise((r) => (release = () => r(null))),
    });
    render(<Adopt />);
    const a = await screen.findByRole("button", { name: "Accept what the servers run" });
    fireEvent.click(a);
    fireEvent.click(a);
    await waitFor(() => expect((a as HTMLButtonElement).disabled).toBe(true));
    expect((screen.getByRole("button", { name: "Accept without hel1" }) as HTMLButtonElement).disabled).toBe(true);
    expect(calls.filter((c) => c.method === "POST")).toHaveLength(1);
    release();
    await screen.findByText("Adopted. The panel acts on the servers again.");
  });
});
