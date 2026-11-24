import { describe, expect, it } from "vitest";
import { render, screen, fireEvent, within } from "@testing-library/react";
import { fakeApi } from "../../test-api";
import { Analytics, servesPages, share } from "./Analytics";

const B = "/api/services/acme/production/shop/analytics";
const day = (r: string, views: number) => ({
  range: r, visitors: 42, views, requests: 900, errors: 9, bytes: 5_000_000, s2: 800, s3: 50, s4: 41, s5: 9,
  series: [{ at: Date.now() - 3_600_000, views, requests: 900, errors: 9, bytes: 1 }],
  paths: [{ value: "/pricing", n: 30 }], refs: [{ value: "news.example", n: 7 }],
});

describe("Analytics", () => {
  it("shows visitors, views, bandwidth, status codes and the tops, by range", async () => {
    const calls = fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => day(url.split("range=")[1], url.endsWith("7d") ? 700 : 120) });
    render(<Analytics project="acme" env="production" service="shop" />);
    await screen.findByText("/pricing");
    expect(screen.getByText("42")).toBeTruthy();
    expect(screen.getAllByText("120").length).toBeGreaterThan(0);
    expect(screen.getByText("5 MB")).toBeTruthy();
    expect(screen.getByText("news.example")).toBeTruthy();
    expect(screen.getByText("4.6%")).toBeTruthy();
    expect(screen.getByText("Page views per hour")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "7d" }));
    await screen.findAllByText("700");
    expect(calls.some((c) => c.url.endsWith("range=7d"))).toBe(true);
  });

  it("offers only the ranges analytics keeps", async () => {
    fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => day(url.split("range=")[1], 120) });
    render(<Analytics project="acme" env="production" service="shop" />);
    await screen.findByText("/pricing");
    expect(within(screen.getByRole("group", { name: "Range" })).getAllByRole("button").map((b) => b.textContent)).toEqual(["24h", "7d", "30d"]);
  });

  it("is not capped below the page width", async () => {
    fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => day(url.split("range=")[1], 120) });
    const { container } = render(<Analytics project="acme" env="production" service="shop" />);
    await screen.findByText("/pricing");
    const root = container.firstChild as HTMLElement;
    expect(root.style.maxWidth).toBe("");
    expect(root.classList.contains("w-form")).toBe(false);
  });

  it("lays out an API service with requests, a 5xx series and no page lists", async () => {
    fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => day(url.split("range=")[1], 5) });
    render(<Analytics project="acme" env="production" service="shop" />);
    await screen.findByText("Requests per hour");
    expect(screen.queryByText("Visitors")).toBeNull();
    expect(screen.queryByText("Page views")).toBeNull();
    expect(screen.queryByText("Pages")).toBeNull();
    expect(screen.queryByText("Referrers")).toBeNull();
    expect(screen.getByText("Bandwidth")).toBeTruthy();
    expect(screen.getByText("Last 24 hours")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "30d" }));
    await screen.findByText("Requests per day");
  });

  it("shows the 4xx share in a tile and drops the visitors note for API services", async () => {
    fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => ({ ...day(url.split("range=")[1], 5), requests: 1000, s4: 4 }) });
    render(<Analytics project="acme" env="production" service="shop" />);
    const label = await screen.findByText("4xx", { selector: ".tile *" });
    expect(label.nextElementSibling?.textContent).toBe("0.4%");
    expect(screen.queryByText(/distinct visitors/)).toBeNull();
  });

  it("titles the page chart per day on longer ranges", async () => {
    fakeApi({ [`GET ${B}`]: (_b: unknown, url: string) => day(url.split("range=")[1], 120) });
    render(<Analytics project="acme" env="production" service="shop" />);
    await screen.findByText("Page views per hour");
    expect(screen.getByText("Pages")).toBeTruthy();
    expect(screen.getByText("Referrers")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "7d" }));
    await screen.findByText("Page views per day");
  });
});

describe("servesPages", () => {
  it("treats html at 2% of requests as a pages service", () => {
    expect(servesPages({ views: 20, requests: 1000 })).toBe(true);
    expect(servesPages({ views: 19, requests: 1000 })).toBe(false);
  });
});

describe("share", () => {
  it("uses one decimal under 10% and whole numbers above", () => {
    expect(share(0, 100)).toBe("0%");
    expect(share(5, 0)).toBe("0%");
    expect(share(4, 1000)).toBe("0.4%");
    expect(share(31, 1000)).toBe("3.1%");
    expect(share(150, 1000)).toBe("15%");
    expect(share(996, 10000)).toBe("10%");
  });
});
