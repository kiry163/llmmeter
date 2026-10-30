import { LineChart as EChartsLineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { init, use } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";
import type { EChartsCoreOption, EChartsType } from "echarts/core";

use([EChartsLineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer]);

/** Shared chart theme: sequential palette, muted axis ink and tooltip surface. */
export const CHART_PALETTE = ["#4f7cf7", "#f59e0b", "#10b981", "#ef4444"];
export const CHART_MUTED = "#8b95a5";
export const CHART_GRID = "rgba(139, 149, 165, 0.22)";
export const CHART_TOOLTIP = {
  backgroundColor: "rgba(17, 24, 39, 0.92)",
  borderWidth: 0,
  textStyle: { color: "#f8fafc", fontSize: 12 },
};

export interface LineChartProps {
  option: EChartsCoreOption;
  ariaLabel: string;
  height?: number;
  empty?: boolean;
  emptyLabel?: string;
  className?: string;
  /**
   * onEvents binds ECharts event handlers by event name (for example
   * "click"). Memoize it: the handlers are rebound whenever it changes.
   */
  onEvents?: ChartEventHandlers;
}

/** ChartEventParams is the subset of ECharts event payloads the UI reads. */
export interface ChartEventParams {
  /** componentType is "series" for curve clicks and "legend" for legend clicks. */
  componentType?: string;
  seriesName?: string;
  name?: string;
  /** value carries the legend entry name on legend clicks. */
  value?: unknown;
  seriesIndex?: number;
}

export type ChartEventHandlers = Record<string, (params: ChartEventParams) => void>;

/**
 * LineChart owns a single ECharts instance and keeps it sized to its
 * container. Options are replaced (not merged) so metric switches cannot leave
 * stale series behind.
 */
export function LineChart({
  option,
  ariaLabel,
  height = 260,
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
      className={`relative w-full ${className}`}
      style={{ height }}
      role="img"
      aria-label={ariaLabel}
    >
      <div ref={containerRef} className="absolute inset-0" aria-hidden="true" />
      {empty && (
        <div className="absolute inset-0 flex items-center justify-center text-sm text-muted">
          {emptyLabel}
        </div>
      )}
    </div>
  );
}
