import { LineChart as EChartsLineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import { init, use } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";
import type { EChartsCoreOption, EChartsType } from "echarts/core";

use([EChartsLineChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer]);

export interface LineChartProps {
  option: EChartsCoreOption;
  ariaLabel: string;
  height?: number;
  empty?: boolean;
  emptyLabel?: string;
  className?: string;
}

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
