import { describe, expect, it } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { LineChart, footer } from "./chart";

const t0 = new Date(2026, 9, 27, 14, 0).getTime();
const M = 60_000;

describe("LineChart", () => {
  it("breaks the line where a minute is missing", () => {
    const points = [0, 1, 2, 4, 5].map((i) => ({ at: t0 + i * M, values: [i * 10] }));
    const { container } = render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" />);
    const d = container.querySelector("path[data-series='0']")!.getAttribute("d")!;
    expect(d.match(/M/g)?.length).toBe(2);
  });

  it("says the latest value and the peak", () => {
    const points = [{ at: t0, values: [40] }, { at: t0 + 2 * M, values: [180] }, { at: t0 + 3 * M, values: [12] }];
    expect(footer("CPU", points, (v) => `${v}%`, "1h", M, t0 + 3 * M)).toBe("CPU now 12%, peak 180% at 14:02");
    render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0 - 57 * M} to={t0 + 3 * M} />);
    expect(screen.getByRole("img").getAttribute("aria-label")).toBe("CPU now 12%, peak 180% at 14:02");
  });

  it("draws round ticks and says the unit and the newest value", () => {
    const points = [{ at: t0, values: [40] }, { at: t0 + 2 * M, values: [180] }, { at: t0 + 3 * M, values: [12] }];
    const { container } = render(<LineChart title="CPU" unit="% of one core" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 3 * M} />);
    const ticks = [...container.querySelectorAll("text.viz-axis")].map((t) => t.textContent).filter((t) => t!.endsWith("%"));
    expect(ticks).toEqual(["0%", "50%", "100%", "150%", "200%"]);
    expect(screen.getByText("% of one core")).toBeTruthy();
    expect(container.querySelector(".viz-now")!.textContent).toBe("12%");
    fireEvent.pointerMove(container.querySelector("svg")!, { clientX: 0, clientY: 10 });
    expect(container.querySelector(".viz-tip")!.textContent).toBe("14:00 40%");
  });

  it("says last and when at the top right when the newest point is old", () => {
    const points = [{ at: t0 + 30 * M, values: [10] }];
    const { container } = render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 60 * M} />);
    expect(container.querySelector(".viz-now")!.textContent).toBe("last 10% at 14:30");
  });

  it("names both series of a two-line chart", () => {
    const points = [{ at: t0, values: [100, 2] }];
    const { container } = render(<LineChart title="Requests" points={points} series={[{ name: "Requests" }, { name: "5xx" }]} stepMs={M} format={(v) => `${v}`} range="1h" to={t0} />);
    expect(container.querySelector(".viz-now")!.textContent).toBe("Requests 1005xx 2");
  });

  it("says when there is nothing to draw", () => {
    render(<LineChart title="CPU" points={[]} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}`} range="1h" />);
    expect(screen.getByText("No samples in this range yet.")).toBeTruthy();
  });

  it("spans the whole range and says when the newest point is old", () => {
    const points = [{ at: t0 + 30 * M, values: [10] }];
    const { container } = render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 60 * M} />);
    const d = container.querySelector("path[data-series='0']")!.getAttribute("d")!;
    const x = Number(d.slice(1).split(",")[0]);
    expect(x).toBeGreaterThan(320);
    expect(x).toBeLessThan(360);
    expect(footer("CPU", points, (v) => `${v}%`, "1h", M, t0 + 60 * M)).toBe("CPU last 10% at 14:30, peak 10% at 14:30");
  });

  it("draws a readable sparkline on a fixed scale", () => {
    const points = [{ at: t0, values: [61] }, { at: t0 + M, values: [61] }];
    const { container } = render(<LineChart compact title="Disk" points={points} series={[{ name: "Disk" }]} stepMs={M} format={(v) => `${v}%`} range="24h" max={100} from={t0} to={t0 + M} />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("viewBox")).toBe("0 0 120 32");
    const d = container.querySelector("path[data-series='0']")!.getAttribute("d")!;
    const y = Number(d.split(",")[1].split(/[ML]/)[0]);
    expect(y).toBeGreaterThan(10);
  });

  it("puts the readout beside the point", () => {
    const points = [0, 1, 2, 3].map((i) => ({ at: t0 + i * M, values: [i === 3 ? 94 : 10] }));
    const { container } = render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 3 * M} />);
    const svg = container.querySelector("svg")!;
    svg.getBoundingClientRect = () => ({ left: 0, width: 640, top: 0, height: 140, right: 640, bottom: 140, x: 0, y: 0, toJSON: () => ({}) });
    fireEvent.pointerMove(svg, { clientX: 639, clientY: 10 });
    const tip = container.querySelector(".viz-tip") as HTMLElement;
    expect(tip.textContent).toBe("14:03 94%");
    expect(tip.style.right).not.toBe("");
    expect(tip.style.left).toBe("");
  });

  it("draws the limit and reaches it", () => {
    const points = [{ at: t0, values: [150e6] }, { at: t0 + M, values: [200e6] }];
    const fmt = (v: number) => `${Math.round(v / 1e6)} MB`;
    const { container, rerender } = render(<LineChart title="Memory" points={points} series={[{ name: "Memory" }]} stepMs={M} format={fmt} range="1h" to={t0 + M} limit={512e6} />);
    expect(container.querySelector(".viz-limit")).not.toBeNull();
    const ticks = [...container.querySelectorAll("text.viz-axis")].map((t) => t.textContent).filter((t) => t!.endsWith("MB"));
    expect(ticks[ticks.length - 1]).toBe("600 MB");
    rerender(<LineChart title="Memory" points={points} series={[{ name: "Memory" }]} stepMs={M} format={fmt} range="1h" to={t0 + M} />);
    expect(container.querySelector(".viz-limit")).toBeNull();
  });
});
