import { describe, expect, it } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { LineChart, footer, gaps } from "./chart";
import { drawn, zrEvents } from "./test-echarts";

const t0 = new Date(2026, 9, 27, 14, 0).getTime();
const M = 60_000;

type Drawn = { xAxis: { axisLabel: { formatter: (v: number) => string } }; series: { name: string; data: [number, number][]; markLine?: { data: { yAxis: number; name?: string }[] } }[] };
const chart = () => drawn[drawn.length - 1].options as unknown as Drawn;
const painted = () => waitFor(() => expect(drawn.length).toBeGreaterThan(0));
const lines = () => chart().series.filter((s) => s.data.length > 0);
const limits = () => chart().series.flatMap((s) => s.markLine?.data ?? []).filter((m) => m.yAxis !== 0);

describe("LineChart", () => {
  it("breaks the line where a minute is missing", async () => {
    const points = [0, 1, 2, 4, 5].map((i) => ({ at: t0 + i * M, values: [i * 10] }));
    render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" />);
    await painted();
    const ys = lines()[0].data.map(([, y]) => y);
    expect(ys.filter(Number.isNaN)).toHaveLength(1);
    expect(ys.findIndex(Number.isNaN)).toBe(3);
  });

  it("labels each gap with when the samples stopped and came back", () => {
    const points = [0, 1, 2, 7, 8, 10].map((i) => ({ at: t0 + i * M, values: [i, i === 8 ? null : i] }));
    expect(gaps(points, M, "1h").map((g) => [g.timestamp, g.label, g.description])).toEqual([
      [t0 + 2 * M, "No data", "No samples from 14:02 to 14:07"],
      [t0 + 8 * M, "No data", "No samples from 14:08 to 14:10"],
    ]);
    expect(gaps(points.slice(0, 3), M, "1h")).toEqual([]);
  });

  it("says the latest value and the peak", async () => {
    const points = [{ at: t0, values: [40] }, { at: t0 + 2 * M, values: [180] }, { at: t0 + 3 * M, values: [12] }];
    expect(footer("CPU", points, (v) => `${v}%`, "1h", M, t0 + 3 * M)).toBe("CPU now 12%, peak 180% at 14:02");
    render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0 - 57 * M} to={t0 + 3 * M} />);
    await painted();
    expect(screen.getByRole("img").getAttribute("aria-label")).toBe("CPU now 12%, peak 180% at 14:02");
  });

  it("says the unit and the newest value", () => {
    const points = [{ at: t0, values: [40] }, { at: t0 + 2 * M, values: [180] }, { at: t0 + 3 * M, values: [12] }];
    const { container } = render(<LineChart title="CPU" unit="% of one core" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 3 * M} />);
    expect(screen.getByText("% of one core")).toBeTruthy();
    expect(container.querySelector(".viz-now")!.textContent).toBe("12%");
  });

  it("says last and when at the top right when the newest point is old", () => {
    const points = [{ at: t0 + 30 * M, values: [10] }];
    const { container } = render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 60 * M} />);
    expect(container.querySelector(".viz-now")!.textContent).toBe("last 10% at 14:30");
  });

  it("names both series of a two-line chart", async () => {
    const points = [{ at: t0, values: [100, 2] }];
    const { container } = render(<LineChart title="Requests" points={points} series={[{ name: "Requests" }, { name: "5xx" }]} stepMs={M} format={(v) => `${v}`} range="1h" to={t0} />);
    await painted();
    expect(container.querySelector(".viz-now")!.textContent).toBe("Requests 1005xx 2");
    expect(lines().map((s) => s.name)).toEqual(["Requests", "5xx"]);
  });

  it("says when there is nothing to draw", () => {
    render(<LineChart title="CPU" points={[]} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}`} range="1h" />);
    expect(screen.getByText("No samples in this range yet.")).toBeTruthy();
  });

  it("spans the whole range and says when the newest point is old", async () => {
    const points = [{ at: t0 + 30 * M, values: [10] }];
    render(<LineChart title="CPU" points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" from={t0} to={t0 + 60 * M} />);
    await painted();
    const xs = lines()[0].data.map(([x]) => x);
    expect([xs[0], xs[xs.length - 1]]).toEqual([t0, t0 + 60 * M]);
    expect(footer("CPU", points, (v) => `${v}%`, "1h", M, t0 + 60 * M)).toBe("CPU last 10% at 14:30, peak 10% at 14:30");
  });

  it("starts at zero", async () => {
    render(<LineChart title="CPU" points={[{ at: t0, values: [40] }]} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" />);
    await painted();
    expect(chart().series.flatMap((s) => s.markLine?.data ?? [])[0]).toEqual(expect.objectContaining({ yAxis: 0 }));
  });

  it("holds a fixed scale without drawing it", async () => {
    const points = [{ at: t0, values: [61] }, { at: t0 + M, values: [61] }];
    render(<LineChart title="Disk" points={points} series={[{ name: "Disk" }]} stepMs={M} format={(v) => `${v}%`} range="24h" max={100} from={t0} to={t0 + M} />);
    await painted();
    expect(limits()).toEqual([expect.objectContaining({ yAxis: 100 })]);
  });

  it("ends the scale on a round tick above the peak", async () => {
    const points = [{ at: t0, values: [23] }];
    render(<LineChart title="CPU" whole points={points} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" to={t0} />);
    await painted();
    expect(limits()).toEqual([expect.objectContaining({ yAxis: 30 })]);
  });

  it("writes only the day under a 30 or 90 day axis", async () => {
    render(<LineChart title="CPU" points={[{ at: t0, values: [1] }]} series={[{ name: "CPU" }]} stepMs={60 * M} format={(v) => `${v}%`} range="30d" to={t0} />);
    await painted();
    expect(chart().xAxis.axisLabel.formatter(new Date(2026, 10, 24).getTime())).toBe("24 Nov");
  });

  it("lets go of the tooltip when the page scrolls", async () => {
    render(<LineChart title="CPU" points={[{ at: t0, values: [1] }]} series={[{ name: "CPU" }]} stepMs={M} format={(v) => `${v}%`} range="1h" to={t0} />);
    await painted();
    zrEvents.length = 0;
    fireEvent.scroll(window);
    expect(zrEvents).toEqual(["globalout"]);
  });

  it("draws the limit and names it", async () => {
    const points = [{ at: t0, values: [150e6] }, { at: t0 + M, values: [200e6] }];
    const fmt = (v: number) => `${Math.round(v / 1e6)} MB`;
    const { rerender } = render(<LineChart title="Memory" points={points} series={[{ name: "Memory" }]} stepMs={M} format={fmt} range="1h" to={t0 + M} limit={512e6} />);
    await painted();
    expect(limits()).toEqual([expect.objectContaining({ yAxis: 512e6, name: "Limit 512 MB" }), expect.objectContaining({ yAxis: 600e6 })]);
    rerender(<LineChart title="Memory" points={points} series={[{ name: "Memory" }]} stepMs={M} format={fmt} range="1h" to={t0 + M} />);
    await waitFor(() => expect(limits()).toEqual([expect.objectContaining({ yAxis: 200e6 })]));
  });
});
