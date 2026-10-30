import { LineChart as EChartsLineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { init, use } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useMemo, useRef } from "react";
import type { EChartsCoreOption, EChartsType } from "echarts/core";
import { readTheme, useTheme } from "../lib/theme";

use([EChartsLineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer]);

export const CHART_PALETTE = ["#38e8ff", "#8b7bff", "#3ddc97", "#ffb454", "#ff7ab6", "#7aa2ff"];
export const CHART_MUTED = "#8d99ad";
export const CHART_GRID = "rgba(148, 163, 184, 0.15)";
export const CHART_TOOLTIP = {
  backgroundColor: "rgba(13, 19, 28, 0.96)",
  borderWidth: 1,
  borderColor: "rgba(148, 163, 184, 0.26)",
  textStyle: { color: "#e8eef8", fontSize: 12 },
};

export interface ChartTheme {
  palette: string[];
  muted: string;
  grid: string;
  text: string;
  surface: string;
  border: string;
  fontFamily: string;
}

function css(name: string, fallback: string): string {
  if (typeof document === "undefined") return fallback;
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim() || fallback;
}

/** useChartTheme reads the CSS tokens so canvas colors follow the theme. */
export function useChartTheme(): ChartTheme {
  const theme = useTheme();
  return useMemo(() => {
    void theme;
    return {
      palette: [1, 2, 3, 4, 5, 6].map((n) => css(`--c${n}`, CHART_PALETTE[n - 1])),
      muted: css("--muted", CHART_MUTED),
      grid: css("--line", CHART_GRID),
      text: css("--fg", "#e8eef8"),
      surface: css("--panel-solid", "#0d131c"),
      border: css("--line-strong", "rgba(148,163,184,.26)"),
      fontFamily: css("--font-ui", "sans-serif"),
    };
  }, [theme]);
}

/** chartTooltip is the shared ECharts tooltip style. */
export function chartTooltip(theme: ChartTheme): EChartsCoreOption["tooltip"] {
  return {
    trigger: "axis",
    backgroundColor: theme.surface,
    borderColor: theme.border,
    borderWidth: 1,
    padding: [8, 10],
    textStyle: { color: theme.text, fontSize: 11, fontFamily: theme.fontFamily },
    axisPointer: { lineStyle: { color: theme.muted, opacity: 0.4 } },
  };
}

export interface LineChartProps {
  option: EChartsCoreOption;
  ariaLabel: string;
  height?: number;
  empty?: boolean;
  emptyLabel?: string;
  className?: string;
  onEvents?: ChartEventHandlers;
}

/** ChartEventParams is the subset of ECharts event payloads the UI reads. */
export interface ChartEventParams {
  componentType?: string;
  seriesName?: string;
  name?: string;
  value?: unknown;
  seriesIndex?: number;
  dataIndex?: number;
}

export type ChartEventHandlers = Record<string, (params: ChartEventParams) => void>;

/** LineChart owns one ECharts instance and keeps it sized to its container. */
export function LineChart({
  option,
  ariaLabel,
  height = 300,
  empty = false,
  emptyLabel = "暂无数据",
  className = "",
  onEvents,
}: LineChartProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const chartRef = useRef<EChartsType | null>(null);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    const chart = init(container);
    chartRef.current = chart;

    if (typeof ResizeObserver === "undefined") {
      const onResize = () => chart.resize();
      window.addEventListener("resize", onResize);
      return () => {
        window.removeEventListener("resize", onResize);
        chart.dispose();
        chartRef.current = null;
      };
    }

    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(container);
    return () => {
      observer.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, []);

  useEffect(() => {
    chartRef.current?.setOption(option, { notMerge: true });
  }, [option]);

  useEffect(() => {
    const chart = chartRef.current;
    if (!chart || !onEvents) return;
    const entries = Object.entries(onEvents);
    for (const [name, handler] of entries) {
      chart.on(name, handler as never);
    }
    return () => {
      for (const [name, handler] of entries) {
        chart.off(name, handler as never);
      }
    };
  }, [onEvents]);

  return (
    <div
      className={`chart-canvas ${className}`.trim()}
      style={{ height }}
      role="img"
      aria-label={ariaLabel}
      data-theme={readTheme()}
    >
      <div ref={containerRef} className="chart-canvas-inner" aria-hidden="true" />
      {empty && <div className="chart-empty">{emptyLabel}</div>}
    </div>
  );
}
