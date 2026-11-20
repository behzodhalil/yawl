import { describe, expect, it, vi } from "vitest";
import { render, waitFor } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Metrics } from "./Metrics";
import { apiView, S } from "./fixtures";

const limits: Record<string, number | undefined> = {};
vi.mock("../../chart", () => ({ LineChart: (p: { title: string; limit?: number }) => ((limits[p.title] = p.limit), null) }));

describe("Metrics with copies", () => {
  it("draws the limits of all copies", async () => {
    const view = { ...apiView, memory_limit: 512e6, form: { ...apiView.form, cpu: 0.5, memory: "512MB", replicas: 3 } };
    fakeApi({ [`GET ${S}`]: view, [`GET ${S}/metrics`]: { range: "1h", step_s: 60, points: [] } });
    render(<Metrics project="acme" env="production" service="api" />);
    await waitFor(() => expect(limits.Memory).toBe(1536e6));
    expect(limits.CPU).toBe(150);
  });
});
