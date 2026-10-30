import type { EChartsCoreOption } from "echarts/core";
import { useMemo } from "react";
import type { ReactElement } from "react";
import type { LiveSparkPoint } from "../api/types";
import { useLive } from "../hooks/useLive";
import type { LiveMode } from "../hooks/useLive";
import {
  formatBytes,
  formatDuration,
  formatOptionalDuration,
  formatTokens,
} from "../lib/format";
import {
  CHART_GRID,
  CHART_MUTED,
  CHART_PALETTE,
  CHART_TOOLTIP,
  LineChart,
} from "./charts";
import { StatCard } from "./StatCard";
import { Badge, Card } from "./ui";

const MODE_LABELS: Record<LiveMode, string> = {
  sse: "实时",
  poll: "轮询",
  down: "未连接",
};

const MODE_TONES = {
  sse: "success",
  poll: "warning",
  down: "danger",
} as const;

const MODE_HINTS: Record<LiveMode, string> = {
  sse: "每 1 秒推送",
  poll: "每 5 秒轮询",
  down: "等待连接",
};

function formatQps(qps: number): string {
  if (!Number.isFinite(qps) || qps <= 0) return "0";
  if (qps >= 100) return String(Math.round(qps));
  return qps.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
}

function buildSparkOption(points: LiveSparkPoint[]): EChartsCoreOption {
  return {
    animation: false,
    color: CHART_PALETTE,
    grid: { left: 8, right: 8, top: 8, bottom: 4, containLabel: true },
    tooltip: { trigger: "axis", axisPointer: { type: "line" }, ...CHART_TOOLTIP },
    xAxis: {
      type: "value",
      min: "dataMin",
      max: "dataMax",
      axisLabel: {
        color: CHART_MUTED,
        formatter: (value: number) => `${Math.round(value)}s`,
      },
      axisLine: { lineStyle: { color: CHART_GRID } },
      axisTick: { show: false },
      splitLine: { show: false },
    },
    yAxis: [
      { type: "value", show: false },
      { type: "value", show: false },
    ],
    series: [
      {
        type: "line",
        name: "请求数",
        yAxisIndex: 0,
        data: points.map((point) => [point.offsetS, point.requests]),
        showSymbol: false,
        lineStyle: { width: 1.5 },
        areaStyle: { opacity: 0.1 },
        tooltip: { valueFormatter: (value: number) => formatTokens(Number(value)) },
      },
      {
        type: "line",
        name: "下行流量",
        yAxisIndex: 1,
        data: points.map((point) => [point.offsetS, point.downBytes]),
        showSymbol: false,
        lineStyle: { width: 1.5 },
        tooltip: { valueFormatter: (value: number) => formatBytes(Number(value)) },
      },
    ],
  };
}

/**
 * LiveStrip renders the six stream metrics and the 60s sparkline. It reads the
 * shared live snapshot only, so changing the historical range never reconnects
 * the stream and the last snapshot stays visible while the connection drops.
 */
export function LiveStrip(): ReactElement {
  const { snapshot, mode } = useLive();
  const sparkOption = useMemo(
    () => (snapshot && snapshot.spark.length > 0 ? buildSparkOption(snapshot.spark) : null),
    [snapshot],
  );
  const ready = snapshot !== null;

  return (
    <section className="space-y-3" aria-labelledby="live-heading">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 id="live-heading" className="text-sm font-semibold">
          实时
        </h2>
        <div className="flex items-center gap-2 text-xs text-muted">
          <Badge tone={MODE_TONES[mode]}>{MODE_LABELS[mode]}</Badge>
          <span>{MODE_HINTS[mode]}</span>
        </div>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
        <StatCard
          label="进行中"
          value={ready ? formatTokens(snapshot.inflight) : "—"}
          hint="当前请求"
        />
        <StatCard
          label="QPS"
          value={ready ? formatQps(snapshot.qps) : "—"}
          hint="近 60 秒"
        />
        <StatCard
          label="上传"
          value={ready ? `${formatBytes(snapshot.upBps)}/s` : "—"}
          hint="字节/秒"
        />
        <StatCard
          label="下载"
          value={ready ? `${formatBytes(snapshot.downBps)}/s` : "—"}
          hint="字节/秒"
        />
        <StatCard
          label="P95 延迟"
          value={formatOptionalDuration(snapshot?.latencyP95Ms)}
          hint={
            snapshot?.latencyP50Ms == null
              ? "P50 —"
              : `P50 ${formatDuration(snapshot.latencyP50Ms)}`
          }
        />
        <StatCard
          label="错误数"
          value={ready ? formatTokens(snapshot.errors) : "—"}
          hint="近 60 秒"
        />
      </div>

      {sparkOption && (
        <Card title="最近 60 秒走势">
          <LineChart
            option={sparkOption}
            ariaLabel="最近 60 秒请求数与下行流量走势"
            height={200}
          />
        </Card>
      )}
    </section>
  );
}
