import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { EChartsCoreOption } from "echarts/core";
import { useMemo } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { BreakdownRow, Interval, SeriesPoint } from "../api/types";
import { DataTable } from "../components/DataTable";
import type { Column } from "../components/DataTable";
import {
  CHART_GRID,
  CHART_MUTED,
  CHART_PALETTE,
  CHART_TOOLTIP,
  LineChart,
} from "../components/charts";
import { ErrorBanner } from "../components/ErrorBanner";
import { LiveStrip } from "../components/LiveStrip";
import { PageHeader } from "../components/PageHeader";
import { RangeBar } from "../components/RangeBar";
import { Skeleton } from "../components/Skeleton";
import { StatCard } from "../components/StatCard";
import { Badge, Card } from "../components/ui";
import { useTimezone } from "../hooks/useTimezone";
import { formatBytes, formatCost, formatTokens } from "../lib/format";
import { autoInterval, parseRangeSelection, rangeDays } from "../lib/range";
import type { RangeSelection } from "../lib/range";

type MetricKey = "traffic" | "requests" | "tokens" | "cost";

interface ChartSeries {
  name: string;
  values: number[];
}

const METRICS: readonly MetricKey[] = ["traffic", "requests", "tokens", "cost"];

const METRIC_LABELS: Record<MetricKey, string> = {
  traffic: "流量",
  requests: "请求",
  tokens: "tokens",
  cost: "成本",
};

const INTERVAL_LABELS: Record<Interval, string> = {
  minute: "按分钟",
  hour: "按小时",
  day: "按天",
};

const DEFAULT_METRIC: MetricKey = "traffic";

const MODEL_COLUMNS: Column<BreakdownRow>[] = [
  {
    key: "model",
    header: "模型",
    render: (row) => (
      <span className="inline-flex flex-wrap items-center gap-2">
        <span className="font-medium">{row.key || "（未知）"}</span>
        {row.unpriced > 0 && <Badge tone="warning">未定价 {formatTokens(row.unpriced)}</Badge>}
      </span>
    ),
  },
  {
    key: "requests",
    header: "请求",
    className: "text-right tabular-nums",
    render: (row) => formatTokens(row.requests),
  },
  {
    key: "tokens",
    header: "Tokens",
    className: "text-right tabular-nums",
    render: (row) => formatTokens(row.promptTokens + row.completionTokens),
  },
  {
    key: "cost",
    header: "花费",
    className: "text-right tabular-nums",
    render: (row) => formatCost(row.costNanoUsd),
  },
];

function parseMetric(params: URLSearchParams): MetricKey {
  const raw = params.get("metric") ?? "";
  return METRICS.includes(raw as MetricKey) ? (raw as MetricKey) : DEFAULT_METRIC;
}

function seriesLabel(point: SeriesPoint, interval: Interval): string {
  if (interval === "day") return point.day;
  const day = point.day.slice(5);
  if (interval === "hour") {
    return `${day} ${String(point.minuteOfDay).padStart(2, "0")}:00`;
  }
  const hour = String(Math.floor(point.minuteOfDay / 60)).padStart(2, "0");
  const minute = String(point.minuteOfDay % 60).padStart(2, "0");
  return `${day} ${hour}:${minute}`;
}

function metricSeries(
  points: SeriesPoint[],
  metric: MetricKey,
): { series: ChartSeries[]; unit: string; formatter: (value: number) => string } {
  switch (metric) {
    case "traffic":
      return {
        series: [
          { name: "上行", values: points.map((point) => point.upBytes) },
          { name: "下行", values: points.map((point) => point.downBytes) },
        ],
        unit: "字节",
        formatter: formatBytes,
      };
    case "requests":
      return {
        series: [{ name: "请求数", values: points.map((point) => point.requests) }],
        unit: "次",
        formatter: formatTokens,
      };
    case "tokens":
      return {
        series: [
          {
            name: "tokens",
            values: points.map((point) => point.promptTokens + point.completionTokens),
          },
        ],
        unit: "tokens",
        formatter: formatTokens,
      };
    case "cost":
      return {
        series: [{ name: "成本", values: points.map((point) => point.costNanoUsd) }],
        unit: "USD",
        formatter: formatCost,
      };
  }
}

function buildSeriesOption(
  points: SeriesPoint[],
  interval: Interval,
  metric: MetricKey,
): EChartsCoreOption {
  const { series, unit, formatter } = metricSeries(points, metric);
  return {
    animationDuration: 250,
    color: CHART_PALETTE,
    grid: {
      left: 8,
      right: 16,
      top: series.length > 1 ? 36 : 16,
      bottom: 8,
      containLabel: true,
    },
    tooltip: { trigger: "axis", axisPointer: { type: "line" }, ...CHART_TOOLTIP },
    legend: {
      show: series.length > 1,
      top: 0,
      icon: "roundRect",
      itemWidth: 10,
      itemHeight: 6,
      textStyle: { color: CHART_MUTED },
    },
    xAxis: {
      type: "category",
      boundaryGap: false,
      data: points.map((point) => seriesLabel(point, interval)),
      axisLabel: { color: CHART_MUTED, hideOverlap: true },
      axisLine: { lineStyle: { color: CHART_GRID } },
      axisTick: { show: false },
    },
    yAxis: {
      type: "value",
      name: unit,
      nameTextStyle: { color: CHART_MUTED },
      axisLabel: { color: CHART_MUTED, formatter },
      splitLine: { lineStyle: { color: CHART_GRID } },
      axisLine: { show: false },
    },
    series: series.map((item) => ({
      type: "line",
      name: item.name,
      data: item.values,
      showSymbol: false,
      lineStyle: { width: 2 },
      areaStyle: { opacity: 0.08 },
      emphasis: { focus: "series" },
      tooltip: { valueFormatter: (value: number) => formatter(Number(value)) },
    })),
  };
}

function withPartial(text: string, partial: boolean): string {
  return partial ? `${text} · 仅保留期内` : text;
}

/** Overview combines the live stream strip with the historical range views. */
export function Overview() {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const timeZone = useTimezone();
  const selection = useMemo(
    () => parseRangeSelection(searchParams, timeZone),
    [searchParams, timeZone],
  );
  const metric = parseMetric(searchParams);
  const interval = autoInterval(rangeDays(selection));

  const overview = useQuery({
    queryKey: ["overview", selection.from, selection.to],
    queryFn: () => api.getOverview({ from: selection.from, to: selection.to }),
    placeholderData: keepPreviousData,
  });

  const series = useQuery({
    queryKey: ["series", selection.from, selection.to, interval],
    queryFn: () =>
      api.getSeries({ from: selection.from, to: selection.to, interval }),
    placeholderData: keepPreviousData,
  });

  const breakdown = useQuery({
    queryKey: ["breakdown", "model", selection.from, selection.to],
    queryFn: () => api.getBreakdown({ from: selection.from, to: selection.to, by: "model" }),
    placeholderData: keepPreviousData,
  });

  const seriesPoints = series.data?.points;
  const seriesOption = useMemo(
    () => buildSeriesOption(seriesPoints ?? [], interval, metric),
    [seriesPoints, interval, metric],
  );

  const topModels = useMemo(() => {
    const rows = breakdown.data?.rows ?? [];
    return [...rows].sort((left, right) => right.costNanoUsd - left.costNanoUsd).slice(0, 5);
  }, [breakdown.data]);

  const updateRange = (next: RangeSelection) => {
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current);
        params.set("range", next.preset);
        if (next.preset === "custom") {
          params.set("from", next.from);
          params.set("to", next.to);
        } else {
          params.delete("from");
          params.delete("to");
        }
        return params;
      },
      { replace: true },
    );
  };

  const updateMetric = (next: MetricKey) => {
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current);
        params.set("metric", next);
        return params;
      },
      { replace: true },
    );
  };

  const openModel = (row: BreakdownRow) => {
    const params = new URLSearchParams();
    if (row.key) params.set("model", row.key);
    params.set("range", "custom");
    params.set("from", selection.from);
    params.set("to", selection.to);
    navigate(`/analysis?${params.toString()}`);
  };

  const stats = overview.data?.current;
  const previous = overview.data?.previous;
  const partial = overview.data?.partial ?? false;

  const costHint = [
    stats && stats.unpriced > 0 ? `未定价 ${formatTokens(stats.unpriced)}` : null,
    partial ? "仅保留期内" : null,
  ]
    .filter((part): part is string => part !== null)
    .join(" · ");

  return (
    <div className="space-y-6">
      <PageHeader title="总览" description="实时状态与区间用量概览" />

      <LiveStrip />

      <section className="space-y-3" aria-labelledby="range-heading">
        <div className="flex flex-col gap-2 xl:flex-row xl:flex-wrap xl:items-center xl:justify-between">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id="range-heading" className="text-sm font-semibold">
              区间统计
            </h2>
            <span className="text-xs text-muted">
              {selection.from} 至 {selection.to} · {INTERVAL_LABELS[interval]}
            </span>
            {partial && <Badge tone="warning">仅保留期内</Badge>}
          </div>
          <RangeBar value={selection} onChange={updateRange} timeZone={timeZone} />
        </div>

        {overview.isError && (
          <ErrorBanner error={overview.error} onRetry={() => void overview.refetch()} />
        )}

        {overview.isPending && !overview.data && (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            {[0, 1, 2, 3].map((index) => (
              <Skeleton key={index} className="h-24 w-full" />
            ))}
          </div>
        )}

        {stats && previous && (
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <StatCard
              label="花费"
              value={formatCost(stats.costNanoUsd)}
              hint={costHint || undefined}
              delta={{
                current: stats.costNanoUsd,
                previous: previous.costNanoUsd,
                goodDirection: "down",
              }}
            />
            <StatCard
              label="Tokens"
              value={formatTokens(stats.totalTokens)}
              hint={withPartial(
                `输入 ${formatTokens(stats.promptTokens)} · 输出 ${formatTokens(
                  stats.completionTokens,
                )}`,
                partial,
              )}
              delta={{ current: stats.totalTokens, previous: previous.totalTokens }}
            />
            <StatCard
              label="请求"
              value={formatTokens(stats.requests)}
              hint={withPartial(
                `错误 ${formatTokens(stats.errors)} · 未解析 ${formatTokens(stats.unmetered)}`,
                partial,
              )}
              delta={{ current: stats.requests, previous: previous.requests }}
            />
            <StatCard
              label="流量"
              value={formatBytes(stats.upBytes + stats.downBytes)}
              hint={withPartial(
                `上传 ${formatBytes(stats.upBytes)} · 下载 ${formatBytes(stats.downBytes)}`,
                partial,
              )}
              delta={{
                current: stats.upBytes + stats.downBytes,
                previous: previous.upBytes + previous.downBytes,
              }}
            />
          </div>
        )}
      </section>

      <Card
        title="用量趋势"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            {series.isFetching && (
              <span className="text-xs text-muted" role="status">
                更新中…
              </span>
            )}
            <div className="flex flex-wrap gap-1" role="group" aria-label="图表指标">
              {METRICS.map((key) => (
                <button
                  key={key}
                  type="button"
                  aria-pressed={metric === key}
                  onClick={() => updateMetric(key)}
                  className={`rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                    metric === key ? "bg-accent/15 text-accent" : "text-muted hover:text-ink"
                  }`}
                >
                  {METRIC_LABELS[key]}
                </button>
              ))}
            </div>
          </div>
        }
      >
        {series.isError && (
          <ErrorBanner error={series.error} onRetry={() => void series.refetch()} />
        )}
        {series.isPending && !series.data && <Skeleton className="h-[320px] w-full" />}
        {series.data && (
          <LineChart
            option={seriesOption}
            ariaLabel={`${METRIC_LABELS[metric]}趋势图`}
            height={320}
            empty={seriesPoints?.length === 0}
            emptyLabel="该区间暂无数据"
          />
        )}
      </Card>

      <Card
        title="模型分布"
        actions={<span className="text-xs text-muted">Top 5 · 按花费降序</span>}
      >
        {breakdown.isError && (
          <ErrorBanner error={breakdown.error} onRetry={() => void breakdown.refetch()} />
        )}
        {breakdown.isPending && !breakdown.data && <Skeleton className="h-40 w-full" />}
        {breakdown.data && (
          <DataTable
            columns={MODEL_COLUMNS}
            rows={topModels}
            rowKey={(row) => row.key || "（未知）"}
            onRowClick={openModel}
            empty={<span className="text-sm text-muted">该区间暂无数据</span>}
          />
        )}
      </Card>
    </div>
  );
}
