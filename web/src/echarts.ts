import * as echarts from "echarts/core";
import { BarChart, LineChart } from "echarts/charts";
import { AriaComponent, BrushComponent, GridComponent, MarkLineComponent, ToolboxComponent, TooltipComponent } from "echarts/components";
import { CanvasRenderer } from "echarts/renderers";

echarts.use([BarChart, LineChart, AriaComponent, BrushComponent, GridComponent, MarkLineComponent, ToolboxComponent, TooltipComponent, CanvasRenderer]);

export { echarts };
