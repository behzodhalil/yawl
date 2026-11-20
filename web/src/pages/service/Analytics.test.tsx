import { describe, expect, it } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Analytics } from "./Analytics";

const B = "/api/services/acme/production/shop/analytics";
const day = (r: string, views: number) => ({
  range: r, visitors: 42, views, requests: 900, errors: 9, bytes: 5_000_000, s2: 800, s3: 50, s4: 41, s5: 9,
  series: [{ at: Date.now() - 3_600_000, views, requests: 900, errors: 9, bytes: 1 }],
  paths: [{ value: "/pricing", n: 30 }], refs: [{ value: "news.example", n: 7 }],
});

describe("Analytics (design record 47)", () => {
  it("shows visitors, views, bandwidth, status codes and the tops, by range", async () => {
    const calls = fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => day(url.split("range=")[1], url.endsWith("7d") ? 700 : 120) });
    render(<Analytics project="acme" env="production" service="shop" />);
    await screen.findByText("/pricing");
    expect(screen.getByText("42")).toBeTruthy();
    expect(screen.getAllByText("120").length).toBeGreaterThan(0);
    expect(screen.getByText("5 MB")).toBeTruthy();
    expect(screen.getByText("news.example")).toBeTruthy();
    expect(screen.getByText("1%")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "7 days" }));
    await screen.findAllByText("700");
    expect(calls.some((c) => c.url.endsWith("range=7d"))).toBe(true);
  });
});
