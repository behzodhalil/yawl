import { describe, expect, it } from "vitest";
import { act } from "@testing-library/react";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Metrics } from "./Metrics";
import { apiView, S } from "./fixtures";

const now = Date.now();
const points = [0, 1, 2].map((i) => ({ at: now - (3 - i) * 60_000, cpu: 10 + i, mem: 300e6, req: 100, err5: 1, p95_ms: 120 }));

describe("Metrics", () => {
  it("draws four graphs for a web service and switches range", async () => {
    const calls = fakeApi({ [`GET ${S}`]: apiView, [`GET ${S}/metrics`]: { range: "1h", step_s: 60, points } });
    render(<Metrics project="acme" env="production" service="api" />);
    expect(await screen.findByRole("img", { name: /^CPU now 12%/ })).toBeTruthy();
    expect(screen.getByText("% of one core")).toBeTruthy();
    expect(screen.getByRole("img", { name: /^Memory now 300 MB/ })).toBeTruthy();
    expect(screen.getByRole("img", { name: /^Requests now 100/ })).toBeTruthy();
    expect(screen.getByRole("img", { name: /^Response time p95 now 120 ms/ })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "24 hours" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("range=24h"))).toBe(true));
  });

  it("has no requests or p95 for a worker", async () => {
    fakeApi({ [`GET ${S}`]: { ...apiView, form: { ...apiView.form, kind: "worker" } }, [`GET ${S}/metrics`]: { range: "1h", step_s: 60, points } });
    render(<Metrics project="acme" env="production" service="api" />);
    await screen.findByRole("img", { name: /^CPU now/ });
    expect(screen.queryByRole("img", { name: /^Requests now/ })).toBeNull();
    expect(screen.queryByRole("img", { name: /^Response time p95/ })).toBeNull();
  });

  it("draws the service's limits", async () => {
    const view = { ...apiView, memory_limit: 512e6, form: { ...apiView.form, cpu: 0.5, memory: "512MB" } };
    fakeApi({ [`GET ${S}`]: view, [`GET ${S}/metrics`]: { range: "1h", step_s: 60, points } });
    const { container } = render(<Metrics project="acme" env="production" service="api" />);
    await screen.findByRole("img", { name: /^CPU now/ });
    expect(container.querySelectorAll(".viz-limit")).toHaveLength(2);
  });
});

describe("Metrics, a new range", () => {
  it("never shows the previous range's points", async () => {
    let release!: (v: unknown) => void;
    fakeApi({ [`GET ${S}`]: apiView, [`GET ${S}/metrics`]: (_b: unknown, url: string) => url.endsWith("range=24h") ? new Promise((r) => (release = r)) : { range: "1h", step_s: 60, points } });
    render(<Metrics project="acme" env="production" service="api" />);
    await screen.findByRole("img", { name: /^CPU now/ });
    fireEvent.click(screen.getByRole("button", { name: "24 hours" }));
    await waitFor(() => expect(release).toBeTruthy());
    expect(screen.queryByRole("img", { name: /^CPU now/ })).toBeNull();
    expect(screen.getByRole("button", { name: "24 hours" })).toBeTruthy();
    await act(async () => release({ range: "24h", step_s: 60, points }));
    expect(await screen.findByRole("img", { name: /^CPU now/ })).toBeTruthy();
  });
});
