import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { EChartsCoreOption } from "echarts/core";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { RequestRecord, SeriesPoint } from "../api/types";
import { EmptyState } from "../components/EmptyState";
import { ErrorBanner } from "../components/ErrorBanner";
import { Odometer } from "../components/Odometer";
import { PageHeader, RangeSeg } from "../components/PageHeader";
import type { RangeOption } from "../components/PageHeader";
import { RequestDrawer } from "../components/RequestDrawer";
import { Sparkline } from "../components/Sparkline";
import { UpstreamFilter } from "../components/UpstreamFilter";
import { chartTooltip, LineChart, useChartTheme } from "../components/charts";
import type { ChartTheme } from "../components/charts";
import { useLive } from "../hooks/useLive";
import { useTimezone } from "../hooks/useTimezone";
import { mergeRankRows } from "../lib/analysis";
import {
  formatBytesDecimal,
  formatCost,
  formatDuration,
  formatPercent,
  formatTime,
  formatTokens,
} from "../lib/format";
import { presetRange } from "../lib/range";
import { compactNumber, deltaTone, modelColor, statusTone } from "../lib/ui";

type UiRange = "today" | "7d" | "30d" | "total";
type SeriesKey = "input" | "cache" | "output" | "cost";

const DAY_MS = 86_400_000;

const RANGE_VALUES: readonly UiRange[] = ["today", "7d", "30d", "total"];

const BASE_RANGES: readonly RangeOption<UiRange>[] = [
  { value: "today", label: "今日" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
];

const TOTAL_RANGE: RangeOption<UiRange> = { value: "total", label: "全部" };

const TOKEN_SERIES: readonly {
  key: Exclude<SeriesKey, "cost">;
  label: string;
  colorIndex: number;
}[] = [
  { key: "input", label: "输入", colorIndex: 0 },
  { key: "cache", label: "缓存读", colorIndex: 1 },
  { key: "output", label: "输出", colorIndex: 2 },
];

interface Bucket {
  label: string;
  input: number;
  cache: number;
  output: number;
  tokens: number;
  costNano: number;
}

const EMPTY_BUCKET = { input: 0, cache: 0, output: 0, tokens: 0, costNano: 0 } as const;

function pad2(value: number): string {
  return String(value).padStart(2, "0");
}

function trafficDisplay(bytes: number): { value: number; unit: string; decimals: number } {
  if (bytes >= 1_000_000_000) return { value: bytes / 1_000_000_000, unit: "GB", decimals: 2 };
  if (bytes >= 1_000_000) return { value: bytes / 1_000_000, unit: "MB", decimals: 1 };
  if (bytes >= 1_000) return { value: bytes / 1_000, unit: "KB", decimals: 1 };
  return { value: bytes, unit: "B", decimals: 0 };
}

function readRange(raw: string | null): UiRange {
  return RANGE_VALUES.includes(raw as UiRange) ? (raw as UiRange) : "today";
}

function shiftDay(day: string, delta: number): string {
  const time = Date.parse(`${day}T00:00:00Z`);
  if (!Number.isFinite(time)) return day;
  return new Date(time + delta * DAY_MS).toISOString().slice(0, 10);
}

function daySpan(from: string, to: string): string[] {
  const start = Date.parse(`${from}T00:00:00Z`);
  const end = Date.parse(`${to}T00:00:00Z`);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return [];
  const days: string[] = [];
  for (let time = start; time <= end; time += DAY_MS) {
    days.push(new Date(time).toISOString().slice(0, 10));
  }
  return days;
}

function bucketFrom(point: SeriesPoint | undefined, label: string): Bucket {
  if (!point) return { label, ...EMPTY_BUCKET };
  const input = Math.max(0, point.promptTokens - point.cacheReadTokens);
  return {
    label,
    input,
    cache: point.cacheReadTokens,
    output: point.completionTokens,
    tokens: input + point.cacheReadTokens + point.completionTokens,
    costNano: point.costNanoUsd,
  };
}

function hourlyBuckets(points: readonly SeriesPoint[] | undefined, day: string): Bucket[] {
  const byHour = new Map<number, SeriesPoint>();
  for (const point of points ?? []) {
    if (point.day === day) byHour.set(point.minuteOfDay, point);
  }
  return Array.from({ length: 24 }, (_, hour) =>
    bucketFrom(byHour.get(hour), `${pad2(hour)}:00`),
  );
}

function dailyBuckets(
  points: readonly SeriesPoint[] | undefined,
  from: string,
  to: string,
): Bucket[] {
  const byDay = new Map<string, SeriesPoint>();
  for (const point of points ?? []) byDay.set(point.day, point);
  return daySpan(from, to).map((day) => bucketFrom(byDay.get(day), day.slice(5)));
}

function currentHour(timeZone: string | undefined): number {
  try {
    return Number(
      new Intl.DateTimeFormat("en-GB", {
        timeZone,
        hour: "2-digit",
        hour12: false,
        hourCycle: "h23",
      }).format(new Date()),
    );
  } catch {
    return new Date().getHours();
  }
}

function Delta({
  current,
  previous,
  title = "较昨日",
}: {
  current: number | undefined;
  previous: number | undefined;
  title?: string;
}) {
  if (current === undefined || previous === undefined || !(previous > 0)) {
    return <span className="delta flat">—</span>;
  }
  const percent = ((current - previous) / previous) * 100;
  if (Math.abs(percent) < 0.05) {
    return <span className="delta flat">≈ 0%</span>;
  }
  return (
    <span className={`delta ${deltaTone(current, previous)}`} title={title}>
      {percent >= 0 ? "↑ " : "↓ "}
      {Math.abs(percent).toFixed(1)}%
    </span>
  );
}

function buildChartOption(
  buckets: readonly Bucket[],
  hidden: Record<SeriesKey, boolean>,
  theme: ChartTheme,
): EChartsCoreOption {
  const labels = buckets.map((bucket) => bucket.label);
  const series: Record<string, unknown>[] = [];

  for (const item of TOKEN_SERIES) {
    if (hidden[item.key]) continue;
    const color = theme.palette[item.colorIndex] ?? theme.palette[0];
    series.push({
      type: "line",
      name: item.label,
      stack: "tokens",
      yAxisIndex: 0,
      data: buckets.map((bucket) => bucket[item.key]),
      showSymbol: false,
      lineStyle: { width: 1.1, color },
      itemStyle: { color },
      areaStyle: { opacity: 0.26 },
      emphasis: { focus: "series" },
      tooltip: { valueFormatter: (value: number) => compactNumber(Number(value)) },
    });
  }

  if (!hidden.cost) {
    series.push({
      type: "line",
      name: "花费",
      yAxisIndex: 1,
      data: buckets.map((bucket) => bucket.costNano / 1_000_000_000),
      showSymbol: false,
      smooth: false,
      lineStyle: { width: 1.8, color: theme.palette[3] ?? theme.palette[0] },
      itemStyle: { color: theme.palette[3] ?? theme.palette[0] },
      emphasis: { focus: "series" },
      tooltip: {
        valueFormatter: (value: number) => formatCost(Number(value) * 1_000_000_000),
      },
    });
  }

  return {
    animationDuration: 260,
    color: theme.palette,
    grid: { left: 6, right: 8, top: 14, bottom: 4, containLabel: true },
    tooltip: chartTooltip(theme),
    xAxis: {
      type: "category",
      boundaryGap: false,
      data: labels,
      axisLabel: { color: theme.muted, hideOverlap: true },
      axisLine: { lineStyle: { color: theme.grid } },
      axisTick: { show: false },
    },
    yAxis: [
      {
        type: "value",
        axisLabel: {
          color: theme.muted,
          formatter: (value: number) => compactNumber(Number(value)),
        },
        splitLine: { lineStyle: { color: theme.grid } },
        axisLine: { show: false },
        axisTick: { show: false },
      },
      {
        type: "value",
        position: "right",
        axisLabel: {
          color: theme.muted,
          formatter: (value: number) => formatCost(Number(value) * 1_000_000_000),
        },
        splitLine: { show: false },
        axisLine: { show: false },
        axisTick: { show: false },
      },
    ],
    series,
  };
}

function RecentRow({
  record,
  timeZone,
  isNew,
  color,
  onOpen,
}: {
  record: RequestRecord;
  timeZone: string | undefined;
  isNew: boolean;
  color: string;
  onOpen: (id: number) => void;
}) {
  const formatted = formatTime(record.startedAt, timeZone);
  const clock = formatted.length >= 19 ? formatted.slice(11) : formatted;
  return (
    <tr
      className={isNew ? "new" : undefined}
      tabIndex={0}
      title="查看请求详情"
      style={{ cursor: "pointer" }}
      onClick={() => onOpen(record.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen(record.id);
        }
      }}
    >
      <td>{clock}</td>
      <td className="model">
        <span className="dot" style={{ background: color }} aria-hidden="true" />
        {record.model || "未知"}
        {record.upstreamName && (
          <small className="rank-upstream">{record.upstreamName}</small>
        )}
      </td>
      <td className="num hide-sm">{formatTokens(record.promptTokens)}</td>
      <td className="num hide-sm">{formatTokens(record.completionTokens)}</td>
      <td className="num">{formatDuration(record.latencyMs)}</td>
      <td className="num">
        {record.priced ? formatCost(record.costNanoUsd) : "未定价"}
      </td>
      <td className="num">
        <span className={`status ${statusTone(record.status)}`}>{record.status}</span>
      </td>
    </tr>
  );
}

/** Overview is the landing dashboard: live state, today's totals and the range chart. */
export function Overview() {
  const [searchParams, setSearchParams] = useSearchParams();
  const timeZone = useTimezone();
  const theme = useChartTheme();
  const { snapshot, mode } = useLive();
  const [requestId, setRequestId] = useState<number | null>(null);
  const [hidden, setHidden] = useState<Record<SeriesKey, boolean>>({
    input: false,
    cache: false,
    output: false,
    cost: false,
  });

  const requestedRange = readRange(searchParams.get("range"));
  const upstream = (searchParams.get("upstream") ?? "").trim();
  const live = upstream ? snapshot?.byUpstream[upstream] : snapshot;

  const bootstrap = useQuery({
    queryKey: ["bootstrap"],
    queryFn: api.bootstrap,
    staleTime: 5 * 60_000,
  });

  const today = useMemo(() => presetRange("today", new Date(), timeZone), [timeZone]);
  const yesterday = useMemo(() => shiftDay(today.from, -1), [today.from]);
  const sparkFrom = useMemo(() => shiftDay(today.from, -23), [today.from]);
  const earliestDay = bootstrap.data?.earliestDay ?? "";
  const range: UiRange =
    requestedRange === "total" && bootstrap.data !== undefined && !earliestDay
      ? "today"
      : requestedRange;
  const lifetimeFrom = earliestDay || today.from;

  const chartRange = useMemo(() => {
    switch (range) {
      case "today":
        return { from: today.from, to: today.to, interval: "hour" as const };
      case "7d": {
        const bounds = presetRange("7d", new Date(), timeZone);
        return { ...bounds, interval: "day" as const };
      }
      case "30d": {
        const bounds = presetRange("30d", new Date(), timeZone);
        return { ...bounds, interval: "day" as const };
      }
      case "total":
        return { from: lifetimeFrom, to: today.to, interval: "day" as const };
    }
  }, [range, today.from, today.to, lifetimeFrom, timeZone]);

  const todayOverview = useQuery({
    queryKey: ["overview", today.from, today.to, upstream],
    queryFn: () => api.getOverview({ from: today.from, to: today.to, upstream }),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    refetchInterval: 15_000,
  });

  const previousOverview = useQuery({
    queryKey: ["overview", yesterday, yesterday, upstream],
    queryFn: () => api.getOverview({ from: yesterday, to: yesterday, upstream }),
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  });

  const lifetimeOverview = useQuery({
    queryKey: ["overview", lifetimeFrom, today.to, upstream],
    queryFn: () => api.getOverview({ from: lifetimeFrom, to: today.to, upstream }),
    placeholderData: keepPreviousData,
    staleTime: 60_000,
    refetchInterval: 60_000,
  });

  const chartSeries = useQuery({
    queryKey: ["series", chartRange.from, chartRange.to, chartRange.interval, upstream],
    queryFn: () =>
      api.getSeries({
        from: chartRange.from,
        to: chartRange.to,
        interval: chartRange.interval,
        upstream,
      }),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    refetchInterval: 15_000,
  });

  const sparkSeries = useQuery({
    queryKey: ["series", sparkFrom, today.to, "day", upstream],
    queryFn: () => api.getSeries({ from: sparkFrom, to: today.to, interval: "day", upstream }),
    placeholderData: keepPreviousData,
    staleTime: 60_000,
  });

  const todayHours = useQuery({
    queryKey: ["series", today.from, today.to, "hour", upstream],
    queryFn: () => api.getSeries({ from: today.from, to: today.to, interval: "hour", upstream }),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    refetchInterval: 15_000,
  });

  const modelBreakdown = useQuery({
    queryKey: ["breakdown", "model", today.from, today.to, upstream],
    queryFn: () => api.getBreakdown({ from: today.from, to: today.to, by: "model", upstream }),
    placeholderData: keepPreviousData,
    staleTime: 30_000,
    refetchInterval: 15_000,
  });

  const recent = useQuery({
    queryKey: ["requests", today.from, today.to, 8, upstream],
    queryFn: () => api.getRequests({ from: today.from, to: today.to, limit: 8, upstream }),
    placeholderData: keepPreviousData,
    staleTime: 15_000,
    refetchInterval: 15_000,
  });

  const todayStats = todayOverview.data?.current;
  const yesterdayStats = previousOverview.data?.current;
  const lifetimeStats = lifetimeOverview.data?.current;
  const todayTraffic = todayStats
    ? trafficDisplay(todayStats.upBytes + todayStats.downBytes)
    : null;
  const chartPartial =
    (range === "total" ? lifetimeOverview.data?.partial : todayOverview.data?.partial) ?? false;

  const hourBuckets = useMemo(
    () => hourlyBuckets(todayHours.data?.points, today.from),
    [todayHours.data, today.from],
  );

  const chartBuckets = useMemo(() => {
    if (chartRange.interval === "hour") return hourBuckets;
    return dailyBuckets(chartSeries.data?.points, chartRange.from, chartRange.to);
  }, [chartRange, chartSeries.data, hourBuckets]);

  const sparkValues = useMemo(() => {
    const byDay = new Map<string, SeriesPoint>();
    for (const point of sparkSeries.data?.points ?? []) byDay.set(point.day, point);
    const cost: number[] = [];
    const tokens: number[] = [];
    const traffic: number[] = [];
    for (const day of daySpan(sparkFrom, today.to)) {
      const point = byDay.get(day);
      cost.push((point?.costNanoUsd ?? 0) / 1_000_000_000);
      tokens.push((point?.promptTokens ?? 0) + (point?.completionTokens ?? 0));
      traffic.push((point?.upBytes ?? 0) + (point?.downBytes ?? 0));
    }
    return { cost, tokens, traffic };
  }, [sparkSeries.data, sparkFrom, today.to]);

  const chartTotals = useMemo(() => {
    let tokens = 0;
    let costNano = 0;
    for (const bucket of chartBuckets) {
      tokens += bucket.tokens;
      costNano += bucket.costNano;
    }
    return { tokens, costNano };
  }, [chartBuckets]);

  const chartOption = useMemo(
    () => buildChartOption(chartBuckets, hidden, theme),
    [chartBuckets, hidden, theme],
  );

  const topModels = useMemo(() => {
    const rows = modelBreakdown.data?.rows ?? [];
    return mergeRankRows(rows)
      .sort((left, right) => right.costNanoUsd - left.costNanoUsd)
      .slice(0, 5);
  }, [modelBreakdown.data]);

  const maxModelCost = Math.max(...topModels.map((row) => row.costNanoUsd), 1);

  const hourCostNanos = hourBuckets.map((bucket) => bucket.costNano);
  const maxHourCost = Math.max(...hourCostNanos, 1);
  const nowHour = currentHour(timeZone);
  const hasHourData = (todayHours.data?.points.length ?? 0) > 0;

  const allHidden = hidden.input && hidden.cache && hidden.output && hidden.cost;
  const hasChartData = chartTotals.tokens > 0 || chartTotals.costNano > 0;
  const chartEmpty = allHidden || !hasChartData;

  const rangeMeta: Record<UiRange, string> = {
    today: "今日 · 按小时",
    "7d": "近 7 天 · 按天",
    "30d": "近 30 天 · 按天",
    total: `全部 ${chartBuckets.length} 天 · 按天`,
  };
  const chartSub = `${rangeMeta[range]} · ${compactNumber(chartTotals.tokens)} tokens · ${formatCost(
    chartTotals.costNano,
  )}${chartPartial ? " · 仅保留期内" : ""}`;

  const rangeOptions = earliestDay ? [...BASE_RANGES, TOTAL_RANGE] : [...BASE_RANGES];

  const overviewError =
    todayOverview.error ?? previousOverview.error ?? lifetimeOverview.error;

  const updateRange = (next: UiRange) => {
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current);
        params.set("range", next);
        params.delete("from");
        params.delete("to");
        return params;
      },
      { replace: true },
    );
  };

  const updateUpstream = (next: string) => {
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current);
        if (next) params.set("upstream", next);
        else params.delete("upstream");
        return params;
      },
      { replace: true },
    );
  };

  const toggleSeries = (key: SeriesKey) => {
    setHidden((current) => ({ ...current, [key]: !current[key] }));
  };

  const refetchOverview = () => {
    void todayOverview.refetch();
    void previousOverview.refetch();
    void lifetimeOverview.refetch();
  };

  const heroLoading = todayOverview.isPending && !todayOverview.data;
  const recentRequests = recent.data?.requests ?? [];

  return (
    <div className="view active">
      <PageHeader
        eyebrow="overview"
        title="总览"
        subtitle="实时状态与区间用量"
        actions={
          <div className="page-actions">
            <UpstreamFilter value={upstream} onChange={updateUpstream} />
            <RangeSeg
              value={range}
              options={rangeOptions}
              onChange={updateRange}
              ariaLabel="时间范围"
            />
          </div>
        }
      />

      {overviewError && <ErrorBanner error={overviewError} onRetry={refetchOverview} />}

      <section className="hero" aria-label="今日用量">
        {heroLoading ? (
          [0, 1, 2].map((index) => (
            <div className="card metric" key={index}>
              <div className="skeleton" style={{ height: 108, width: "100%" }} aria-hidden="true" />
            </div>
          ))
        ) : (
          <>
            <article className="card metric">
              <div className="metric-top">
                <span className="label">今日花费</span>
                <span className="chip">USD</span>
              </div>
              <div className="metric-value">
                <span className="cur">$</span>
                {todayStats ? (
                  <Odometer
                    value={todayStats.costNanoUsd / 1_000_000_000}
                    decimals={4}
                    label="今日花费"
                  />
                ) : (
                  <span className="odo">—</span>
                )}
              </div>
              <div className="metric-foot">
                <div className="metric-meta">
                  <span>
                    累计 <b>{lifetimeStats ? formatCost(lifetimeStats.costNanoUsd) : "—"}</b>
                  </span>
                  <Delta
                    current={todayStats?.costNanoUsd}
                    previous={yesterdayStats?.costNanoUsd}
                  />
                </div>
                <Sparkline
                  values={sparkValues.cost}
                  color={theme.palette[0]}
                  label="近 24 天花费趋势"
                />
              </div>
            </article>

            <article className="card metric">
              <div className="metric-top">
                <span className="label">今日 Tokens</span>
                <span className="chip">TOK</span>
              </div>
              <div className="metric-value">
                {todayStats ? (
                  <Odometer value={todayStats.totalTokens} decimals={0} label="今日 Tokens" />
                ) : (
                  <span className="odo">—</span>
                )}
              </div>
              <div className="metric-foot">
                <div className="metric-meta">
                  <span>
                    累计 <b>{lifetimeStats ? formatTokens(lifetimeStats.totalTokens) : "—"}</b>
                  </span>
                  <Delta
                    current={todayStats?.totalTokens}
                    previous={yesterdayStats?.totalTokens}
                  />
                  <div className="metric-breakdown">
                    <span>
                      输入 <b>{todayStats ? compactNumber(todayStats.promptTokens) : "—"}</b>
                    </span>
                    <span>
                      输出 <b>{todayStats ? compactNumber(todayStats.completionTokens) : "—"}</b>
                    </span>
                  </div>
                </div>
                <Sparkline
                  values={sparkValues.tokens}
                  color={theme.palette[1]}
                  label="近 24 天 Tokens 趋势"
                />
              </div>
            </article>

            <article className="card metric">
              <div className="metric-top">
                <span className="label">今日流量</span>
                <span className="chip">NET</span>
              </div>
              <div className="metric-value">
                {todayStats && todayTraffic ? (
                  <Odometer
                    value={todayTraffic.value}
                    decimals={todayTraffic.decimals}
                    label={`今日流量（${todayTraffic.unit}）`}
                  />
                ) : (
                  <span className="odo">—</span>
                )}
                <span className="unit">{todayTraffic?.unit ?? "MB"}</span>
              </div>
              <div className="metric-foot">
                <div className="metric-meta">
                  <span>
                    累计{" "}
                    <b>
                      {lifetimeStats
                        ? formatBytesDecimal(lifetimeStats.upBytes + lifetimeStats.downBytes)
                        : "—"}
                    </b>
                  </span>
                  <Delta
                    current={
                      todayStats ? todayStats.upBytes + todayStats.downBytes : undefined
                    }
                    previous={
                      yesterdayStats ? yesterdayStats.upBytes + yesterdayStats.downBytes : undefined
                    }
                  />
                  <div className="metric-breakdown">
                    <span>
                      上传{" "}
                      <b>{todayStats ? formatBytesDecimal(todayStats.upBytes) : "—"}</b>
                    </span>
                    <span>
                      下载{" "}
                      <b>{todayStats ? formatBytesDecimal(todayStats.downBytes) : "—"}</b>
                    </span>
                  </div>
                </div>
                <Sparkline
                  values={sparkValues.traffic}
                  color={theme.palette[2]}
                  label="近 24 天流量趋势"
                />
              </div>
            </article>
          </>
        )}
      </section>

      <section
        className="card strip"
        aria-label="实时状态"
        title={mode === "down" ? "实时连接中断" : undefined}
      >
        <div className="strip-stats">
          <div className="stat">
            <span className="stat-label">进行中</span>
            <span className="stat-value">
              {snapshot ? (live?.inflight ?? 0) : "—"}
            </span>
            <span className="stat-sub">请求</span>
          </div>
          <div className="stat">
            <span className="stat-label">P95</span>
            <span className="stat-value">
              {live && live.latencyP95Ms !== null
                ? formatDuration(live.latencyP95Ms)
                : "—"}
            </span>
            <span className="stat-sub">延迟</span>
          </div>
          <div className="stat">
            <span className="stat-label">错误率</span>
            <span className="stat-value">
              {live
                ? formatPercent(live.errors / Math.max(live.qps * (snapshot?.windowS ?? 60), 1))
                : snapshot
                  ? "0%"
                  : "—"}
            </span>
            <span className="stat-sub">{mode === "down" ? "未连接" : "近 60s"}</span>
          </div>
          <div className="stat">
            <span className="stat-label">缓存命中</span>
            <span className="stat-value">
              {todayStats && todayStats.promptTokens > 0
                ? formatPercent(todayStats.cacheReadTokens / todayStats.promptTokens)
                : "—"}
            </span>
            <span className="stat-sub">今日</span>
          </div>
        </div>
      </section>

      <section className="grid-main" aria-label="用量与花费">
        <article className="card">
          <div className="card-head chart-head">
            <div>
              <div className="card-title">用量与花费</div>
              <div className="card-sub">{chartSub}</div>
            </div>
            <div className="chart-tools">
              <div className="legend" role="group" aria-label="图例，点击切换显示">
                {TOKEN_SERIES.map((item) => (
                  <button
                    key={item.key}
                    type="button"
                    className={hidden[item.key] ? "off" : undefined}
                    aria-pressed={!hidden[item.key]}
                    onClick={() => toggleSeries(item.key)}
                  >
                    <i
                      className="sw"
                      style={{ background: theme.palette[item.colorIndex] }}
                      aria-hidden="true"
                    />
                    {item.label}
                  </button>
                ))}
                <button
                  type="button"
                  className={hidden.cost ? "off" : undefined}
                  aria-pressed={!hidden.cost}
                  onClick={() => toggleSeries("cost")}
                >
                  <i
                    className="sw line"
                    style={{ background: theme.palette[3] }}
                    aria-hidden="true"
                  />
                  花费
                </button>
              </div>
            </div>
          </div>

          {chartSeries.isError && (
            <div style={{ padding: "10px 16px 0" }}>
              <ErrorBanner
                error={chartSeries.error}
                onRetry={() => void chartSeries.refetch()}
              />
            </div>
          )}

          {chartSeries.isPending && !chartSeries.data ? (
            <div className="chart-wrap">
              <div
                className="skeleton"
                style={{ height: "100%", width: "100%" }}
                aria-hidden="true"
              />
            </div>
          ) : (
            <LineChart
              option={chartOption}
              ariaLabel="Token 用量与花费趋势"
              height={300}
              empty={chartEmpty}
              emptyLabel={allHidden ? "全部系列已隐藏 · 点击图例恢复" : "暂无用量数据"}
            />
          )}
        </article>
      </section>

      <section className="grid-bottom">
        <article className="card span-3">
          <div className="card-head">
            <div>
              <div className="card-title">模型排行</div>
              <div className="card-sub">今日花费</div>
            </div>
          </div>

          {modelBreakdown.isError && (
            <div style={{ padding: "10px 16px 0" }}>
              <ErrorBanner
                error={modelBreakdown.error}
                onRetry={() => void modelBreakdown.refetch()}
              />
            </div>
          )}

          {modelBreakdown.isPending && !modelBreakdown.data ? (
            <div className="rank-list">
              {[0, 1, 2, 3, 4].map((index) => (
                <div className="skeleton" style={{ height: 30 }} key={index} aria-hidden="true" />
              ))}
            </div>
          ) : topModels.length === 0 ? (
            <EmptyState title="今日暂无模型数据" description="尚无已计费的模型用量" />
          ) : (
            <div className="rank-list">
              {topModels.map((row) => {
                const color = modelColor(row.key || "unknown", theme.palette);
                return (
                  <div className="rank" key={row.key || "unknown"}>
                    <div className="rank-top">
                      <span className="rank-name">
                        <i className="sw" style={{ background: color }} aria-hidden="true" />
                        <span>{row.key || "未知模型"}</span>
                        {row.upstreamName && (
                          <small className="rank-upstream">{row.upstreamName}</small>
                        )}
                      </span>
                      <span className="rank-val">{formatCost(row.costNanoUsd)}</span>
                    </div>
                    <div className="rank-bar">
                      <i
                        style={{
                          width: `${Math.max(2, (row.costNanoUsd / maxModelCost) * 100)}%`,
                          background: color,
                        }}
                      />
                    </div>
                  </div>
                );
              })}
            </div>
          )}
        </article>

        <article className="card span-4">
          <div className="card-head">
            <div>
              <div className="card-title">按小时花费</div>
              <div className="card-sub">今日 · 本地时区</div>
            </div>
          </div>

          {todayHours.isError && (
            <div style={{ padding: "10px 16px 0" }}>
              <ErrorBanner
                error={todayHours.error}
                onRetry={() => void todayHours.refetch()}
              />
            </div>
          )}

          {todayHours.isPending && !todayHours.data ? (
            <div className="hours-wrap">
              <div className="skeleton" style={{ height: 108 }} aria-hidden="true" />
            </div>
          ) : !hasHourData ? (
            <EmptyState title="今日暂无按小时数据" />
          ) : (
            <div className="hours-wrap">
              <div className="hours">
                {hourCostNanos.map((costNano, hour) => (
                  <div
                    className={`hour${hour === nowHour ? " now" : ""}`}
                    key={hour}
                    title={`${pad2(hour)}:00 · ${formatCost(costNano)}`}
                  >
                    <i
                      style={{
                        height: `${Math.max(3, (costNano / maxHourCost) * 100)}%`,
                      }}
                    />
                  </div>
                ))}
              </div>
              <div className="hour-label">
                <span>00</span>
                <span>06</span>
                <span>12</span>
                <span>18</span>
                <span>23</span>
              </div>
            </div>
          )}
        </article>

        <article className="card span-5">
          <div className="card-head">
            <div>
              <div className="card-title">最近请求</div>
              <div className="card-sub">实时更新 · 最新 8 条</div>
            </div>
          </div>

          <div className="recent-wrap">
            {recent.isError && (
              <div style={{ padding: "0 8px 8px" }}>
                <ErrorBanner error={recent.error} onRetry={() => void recent.refetch()} />
              </div>
            )}
            <table aria-label="最近请求">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>模型</th>
                  <th className="num hide-sm">输入</th>
                  <th className="num hide-sm">输出</th>
                  <th className="num">延迟</th>
                  <th className="num">费用</th>
                  <th className="num">状态</th>
                </tr>
              </thead>
              <tbody>
                {recent.isPending && !recent.data ? (
                  <tr>
                    <td colSpan={7}>
                      <div className="skeleton" style={{ height: 96 }} aria-hidden="true" />
                    </td>
                  </tr>
                ) : recentRequests.length > 0 ? (
                  recentRequests.map((record, index) => (
                    <RecentRow
                      key={record.id}
                      record={record}
                      timeZone={timeZone}
                      isNew={index === 0}
                      color={modelColor(record.model || "unknown", theme.palette)}
                      onOpen={setRequestId}
                    />
                  ))
                ) : (
                  <tr>
                    <td colSpan={7}>
                      <EmptyState title="今日暂无请求" />
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </article>
      </section>

      <RequestDrawer requestId={requestId} onClose={() => setRequestId(null)} />
    </div>
  );
}
