import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { EChartsCoreOption } from "echarts/core";
import { useMemo, useState } from "react";
import type { ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { BreakdownRow, Interval, LiveSparkPoint, SeriesPoint } from "../api/types";
import { LineChart } from "../components/charts";
import { StatCard } from "../components/StatCard";
import { TimeRange } from "../components/TimeRange";
import type { RangeSelection } from "../components/TimeRange";
import { Badge, Card, ErrorText, Spinner } from "../components/ui";
import { useLive } from "../hooks/useLive";
import { formatBytes, formatCost, formatDuration, formatTokens } from "../lib/format";
import { autoInterval, isDateString, isRangePreset, presetRange, rangeDays } from "../lib/range";
import type { RangePreset } from "../lib/range";

type MetricKey = "traffic" | "requests" | "tokens" | "cost";

type BreakdownSortKey =
  | "model"
  | "requests"
  | "errors"
  | "upBytes"
  | "downBytes"
  | "tokens"
  | "cost"
  | "unpriced";

interface SortState {
  key: BreakdownSortKey;
  dir: "asc" | "desc";
}

interface BreakdownColumn {
  key: BreakdownSortKey;
  label: string;
  numeric?: boolean;
  render: (row: BreakdownRow) => ReactNode;
}

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

const DEFAULT_RANGE_PRESET: RangePreset = "7d";
const DEFAULT_METRIC: MetricKey = "traffic";

const CHART_PALETTE = ["#4f7cf7", "#f59e0b", "#10b981", "#ef4444"];
const CHART_MUTED = "#8b95a5";
const CHART_GRID = "rgba(139, 149, 165, 0.22)";
const CHART_TOOLTIP = {
  backgroundColor: "rgba(17, 24, 39, 0.92)",
  borderWidth: 0,
  textStyle: { color: "#f8fafc", fontSize: 12 },
};

const BREAKDOWN_COLUMNS: readonly BreakdownColumn[] = [
  {
    key: "model",
    label: "模型",
    render: (row) => (
      <span className="inline-flex flex-wrap items-center gap-2">
        <span className="font-medium">{row.key || "（未知）"}</span>
        {row.unpriced > 0 && <Badge tone="warning">未定价 {formatTokens(row.unpriced)}</Badge>}
      </span>
    ),
  },
  {
    key: "requests",
    label: "请求",
    numeric: true,
    render: (row) => formatTokens(row.requests),
  },
  {
    key: "errors",
    label: "错误",
    numeric: true,
    render: (row) => formatTokens(row.errors),
  },
  {
    key: "upBytes",
    label: "上行",
    numeric: true,
    render: (row) => formatBytes(row.upBytes),
  },
  {
    key: "downBytes",
    label: "下行",
    numeric: true,
    render: (row) => formatBytes(row.downBytes),
  },
  {
    key: "tokens",
    label: "tokens",
    numeric: true,
    render: (row) => formatTokens(row.promptTokens + row.completionTokens),
  },
  {
    key: "cost",
    label: "成本",
    numeric: true,
    render: (row) => formatCost(row.costNanoUsd),
  },
  {
    key: "unpriced",
    label: "未定价",
    numeric: true,
    render: (row) => formatTokens(row.unpriced),
  },
];

function parseRangeSelection(params: URLSearchParams): RangeSelection {
  const raw = params.get("range");
  const preset = isRangePreset(raw) ? raw : DEFAULT_RANGE_PRESET;
  if (preset === "custom") {
    const from = params.get("from");
    const to = params.get("to");
    if (isDateString(from) && isDateString(to) && from <= to) {
      return { preset, from, to };
    }
    return { preset, ...presetRange("7d") };
  }
  return { preset, ...presetRange(preset) };
}

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

function formatQps(qps: number): string {
  if (!Number.isFinite(qps) || qps <= 0) return "0";
  if (qps >= 100) return String(Math.round(qps));
  return qps.toFixed(2).replace(/0+$/, "").replace(/\.$/, "");
}

function breakdownValue(row: BreakdownRow, key: BreakdownSortKey): string | number {
  switch (key) {
    case "model":
      return row.key;
    case "requests":
      return row.requests;
    case "errors":
      return row.errors;
    case "upBytes":
      return row.upBytes;
    case "downBytes":
      return row.downBytes;
    case "tokens":
      return row.promptTokens + row.completionTokens;
    case "cost":
      return row.costNanoUsd;
    case "unpriced":
      return row.unpriced;
  }
}

/** Overview combines the live SSE snapshot with historical range queries. */
export function Overview() {
  const [searchParams, setSearchParams] = useSearchParams();
  const selection = useMemo(() => parseRangeSelection(searchParams), [searchParams]);
  const metric = parseMetric(searchParams);
  const interval = autoInterval(rangeDays(selection));
  const { snapshot, mode } = useLive();
  const [sort, setSort] = useState<SortState>({ key: "cost", dir: "desc" });

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
  const sparkOption = useMemo(
    () => (snapshot && snapshot.spark.length > 0 ? buildSparkOption(snapshot.spark) : null),
    [snapshot],
  );

  const sortedRows = useMemo(() => {
    const rows = breakdown.data?.rows ?? [];
    const sorted = [...rows];
    sorted.sort((left, right) => {
      const a = breakdownValue(left, sort.key);
      const b = breakdownValue(right, sort.key);
      const order =
        typeof a === "string" && typeof b === "string"
          ? a.localeCompare(b, "zh-Hans-CN")
          : Number(a) - Number(b);
      return sort.dir === "asc" ? order : -order;
    });
    return sorted;
  }, [breakdown.data, sort]);

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

  const toggleSort = (key: BreakdownSortKey) => {
    setSort((current) =>
      current.key === key
        ? { key, dir: current.dir === "asc" ? "desc" : "asc" }
        : { key, dir: key === "model" ? "asc" : "desc" },
    );
  };

  const modeLabel = mode === "sse" ? "实时" : mode === "poll" ? "轮询" : "未连接";
  const modeTone = mode === "sse" ? "success" : mode === "poll" ? "warning" : "danger";
  const modeHint =
    mode === "sse" ? "每 1 秒推送" : mode === "poll" ? "每 5 秒轮询" : "等待连接";

  const stats = overview.data?.current;
  const previous = overview.data?.previous;

  return (
    <div className="space-y-6">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">总览</h1>
          <p className="text-sm text-muted">
            {selection.from} 至 {selection.to} · {INTERVAL_LABELS[interval]}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {overview.data?.partial && <Badge tone="warning">仅统计保留期内</Badge>}
        </div>
      </header>

      <div className="rounded-lg border border-line bg-surface-raised p-3">
        <TimeRange value={selection} onChange={updateRange} />
      </div>

      <section className="space-y-3" aria-labelledby="live-heading">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 id="live-heading" className="text-sm font-semibold">
            实时
          </h2>
          <div className="flex items-center gap-2 text-xs text-muted">
            <Badge tone={modeTone}>{modeLabel}</Badge>
            <span>{modeHint}</span>
          </div>
        </div>

        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
          <StatCard
            label="当前请求"
            value={formatTokens(snapshot?.inflight ?? 0)}
            hint="进行中"
          />
          <StatCard label="QPS" value={formatQps(snapshot?.qps ?? 0)} hint="近 60 秒" />
          <StatCard
            label="上传"
            value={`${formatBytes(snapshot?.upBps ?? 0)}/s`}
            hint="字节/秒"
          />
          <StatCard
            label="下载"
            value={`${formatBytes(snapshot?.downBps ?? 0)}/s`}
            hint="字节/秒"
          />
          <StatCard
            label="P95 延迟"
            value={
              snapshot?.latencyP95Ms == null ? "—" : formatDuration(snapshot.latencyP95Ms)
            }
            hint={
              snapshot?.latencyP50Ms == null
                ? "P50 —"
                : `P50 ${formatDuration(snapshot.latencyP50Ms)}`
            }
          />
          <StatCard
            label="错误数"
            value={formatTokens(snapshot?.errors ?? 0)}
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

      <section className="space-y-3" aria-labelledby="range-heading">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 id="range-heading" className="text-sm font-semibold">
            区间统计
          </h2>
          <span className="text-xs text-muted">{INTERVAL_LABELS[interval]}</span>
        </div>

        {overview.isError && <ErrorText error={overview.error} />}
        {overview.isPending && !overview.data && <Spinner />}

        {stats && previous && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
            <StatCard
              label="请求数"
              value={formatTokens(stats.requests)}
              hint={`错误 ${formatTokens(stats.errors)}${
                stats.requests > 0
                  ? ` · 错误率 ${((stats.errors / stats.requests) * 100).toFixed(1)}%`
                  : ""
              }`}
              delta={{ current: stats.requests, previous: previous.requests }}
            />
            <StatCard
              label="总 tokens"
              value={formatTokens(stats.totalTokens)}
              hint={`输入 ${formatTokens(stats.promptTokens)} · 输出 ${formatTokens(
                stats.completionTokens,
              )}`}
              delta={{ current: stats.totalTokens, previous: previous.totalTokens }}
            />
            <StatCard
              label="成本"
              value={formatCost(stats.costNanoUsd)}
              hint={`未定价 ${formatTokens(stats.unpriced)} · 未计量 ${formatTokens(
                stats.unmetered,
              )}`}
              delta={{
                current: stats.costNanoUsd,
                previous: previous.costNanoUsd,
                goodDirection: "down",
              }}
            />
            <StatCard
              label="P95 延迟"
              value={
                stats.latencyP95Ms == null ? "—" : formatDuration(stats.latencyP95Ms)
              }
              hint={`P50 ${
                stats.latencyP50Ms == null ? "—" : formatDuration(stats.latencyP50Ms)
              } · 平均 ${
                stats.latencyAvgMs == null ? "—" : formatDuration(stats.latencyAvgMs)
              }`}
              delta={{
                current: stats.latencyP95Ms,
                previous: previous.latencyP95Ms,
                goodDirection: "down",
              }}
            />
            <StatCard
              label="上行流量"
              value={formatBytes(stats.upBytes)}
              hint="请求体与请求头"
              delta={{ current: stats.upBytes, previous: previous.upBytes }}
            />
            <StatCard
              label="下行流量"
              value={formatBytes(stats.downBytes)}
              hint="响应体与响应头"
              delta={{ current: stats.downBytes, previous: previous.downBytes }}
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
        {series.isError && <ErrorText error={series.error} />}
        {series.isPending && !series.data && <Spinner />}
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
        actions={<span className="text-xs text-muted">点击表头排序</span>}
      >
        {breakdown.isError && <ErrorText error={breakdown.error} />}
        {breakdown.isPending && !breakdown.data && <Spinner />}
        {breakdown.data && sortedRows.length === 0 && (
          <p className="py-6 text-center text-sm text-muted">该区间暂无数据</p>
        )}
        {sortedRows.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-sm">
              <thead>
                <tr className="border-b border-line text-xs text-muted">
                  {BREAKDOWN_COLUMNS.map((column) => (
                    <th
                      key={column.key}
                      scope="col"
                      aria-sort={
                        sort.key === column.key
                          ? sort.dir === "asc"
                            ? "ascending"
                            : "descending"
                          : "none"
                      }
                      className={`px-2 py-2 font-medium ${
                        column.numeric ? "text-right" : "text-left"
                      }`}
                    >
                      <button
                        type="button"
                        onClick={() => toggleSort(column.key)}
                        className={`inline-flex items-center gap-1 hover:text-ink ${
                          column.numeric ? "w-full justify-end" : ""
                        }`}
                      >
                        {column.label}
                        <span aria-hidden="true" className="text-[10px]">
                          {sort.key === column.key ? (sort.dir === "asc" ? "▲" : "▼") : "↕"}
                        </span>
                      </button>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {sortedRows.map((row) => (
                  <tr key={row.key || "（未知）"} className="border-b border-line/60 last:border-0">
                    {BREAKDOWN_COLUMNS.map((column) => (
                      <td
                        key={column.key}
                        className={`px-2 py-2 align-middle ${
                          column.numeric ? "text-right tabular-nums" : ""
                        }`}
                      >
                        {column.render(row)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
