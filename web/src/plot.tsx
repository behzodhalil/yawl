import { useEffect, useRef, type ComponentProps } from "react";
import { TimeseriesChart } from "@cloudflare/kumo";
import type { ECharts } from "echarts/core";
import { echarts } from "./echarts";

export default function Plot(props: Omit<ComponentProps<typeof TimeseriesChart>, "echarts">) {
  const chart = useRef<ECharts>(null);
  useEffect(() => {
    // Kumo closes its tooltip and echarts drops the axis pointer only on zrender's globalout, which a scroll never fires.
    const leave = () => chart.current?.getZr().handler.trigger("globalout", { type: "globalout" });
    window.addEventListener("scroll", leave, { capture: true, passive: true });
    return () => window.removeEventListener("scroll", leave, { capture: true });
  }, []);
  return <TimeseriesChart ref={chart} echarts={echarts} {...props} />;
}
