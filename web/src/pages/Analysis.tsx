import { useQuery } from "@tanstack/react-query";
import type { EChartsCoreOption } from "echarts/core";
import { useCallback, useMemo, useState, type ReactElement } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  AnalysisHealth,
  AnalysisRow,
  AnalysisTopRequest,
  AnalysisTrendBucket,
  Interval,
  TrendModel,
} from "../api/types";
import { ErrorBanner } from "../components/ErrorBanner";
import { PageHeader, RangeSeg } from "../components/PageHeader";
import { RequestDrawer } from "../components/RequestDrawer";
import {
  CHART_PALETTE,
  CHART_TOOLTIP,
  LineChart,
  chartTooltip,
  useChartTheme,
} from "../components/charts";
import type { ChartTheme } from "../components/charts";
import { useTimezone } from "../hooks/useTimezone";
import { UNKNOWN_KEY } from "../lib/analysis";
import {
  formatBytes,
  formatCost,
  formatDuration,
  formatPercent,
  formatTime,
  formatTokens,
} from "../lib/format";
import { RANGE_OPTIONS, compactNumber, modelColor, rangeBounds } from "../lib/ui";
import type { UiRange } from "../lib/ui";

type MetricKey = "cost" | "tokens" | "requests" | "bytes";

type TopTab = "cost" | "tokens" | "latency";

const METRIC_OPTIONS: readonly { value: MetricKey; label: string }[] = [
  { value: "cost", label: "花费" },
  { value: "tokens", label: "Tokens" },
  { value: "requests", label: "请求" },
  { value: "bytes", label: "流量" },
];

const METRIC_LABELS: Record<MetricKey, string> = {
  cost: "花费",
  tokens: "Tokens",
  requests: "请求",
  bytes: "流量",
};

const TOP_OPTIONS: readonly { value: TopTab; label: string }[] = [
  { value: "cost", label: "花费" },
  { value: "tokens", label: "Tokens" },
  { value: "latency", label: "耗时" },
];

const EMPTY_HIDDEN: ReadonlySet<string> = new Set<string>();

/** metricValue reads one model's contribution for the active trend metric. */
function metricValue(model: TrendModel | undefined, metric: MetricKey): number {
  if (!model) return 0;
  switch (metric) {
    case "cost":
      return model.costNanoUsd;
    case "tokens":
      return model.promptTokens + model.completionTokens;
    case "requests":
      return model.requests;
    case "bytes":
      return model.upBytes + model.downBytes;
  }
}

/** METRIC_FORMATTERS renders tooltips and the y axis for each trend metric. */
const METRIC_FORMATTERS: Record<MetricKey, (value: number) => string> = {
  cost: formatCost,
  tokens: formatTokens,
  requests: formatTokens,
  bytes: formatBytes,
};

function parseRange(params: URLSearchParams): UiRange {
  const raw = params.get("range") ?? "";
  return RANGE_OPTIONS.some((option) => option.value === raw) ? (raw as UiRange) : "today";
}

function parseMetric(params: URLSearchParams): MetricKey {
  const raw = params.get("metric") ?? "";
  return METRIC_OPTIONS.some((option) => option.value === raw)
    ? (raw as MetricKey)
    : "cost";
}

function parseTopTab(params: URLSearchParams): TopTab {
  const raw = params.get("top") ?? "";
  return TOP_OPTIONS.some((option) => option.value === raw) ? (raw as TopTab) : "cost";
}

/** bucketLabel renders a trend bucket at the interval's precision. */
function bucketLabel(bucket: AnalysisTrendBucket, interval: Interval): string {
  if (interval === "day") return bucket.day.slice(5);
  // Hour buckets carry the hour index in minuteOfDay (the SQL divides by 60).
  if (interval === "hour") {
    return `${String(bucket.minuteOfDay).padStart(2, "0")}:00`;
  }
  const hour = String(Math.floor(bucket.minuteOfDay / 60)).padStart(2, "0");
  const minute = String(bucket.minuteOfDay % 60).padStart(2, "0");
  return `${hour}:${minute}`;
}

/** eachDay lists the inclusive YYYY-MM-DD dates between from and to. */
function eachDay(from: string, to: string): string[] {
  const start = Date.parse(`${from}T00:00:00Z`);
  const end = Date.parse(`${to}T00:00:00Z`);
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) return [];
  const days: string[] = [];
  for (let time = start; time <= end; time += 86_400_000) {
    days.push(new Date(time).toISOString().slice(0, 10));
  }
  return days;
}

/**
 * fillBuckets restores the empty buckets the API omits: 24 hours for today,
 * one bucket per calendar day for the longer ranges.
 */
function fillBuckets(
  trend: AnalysisTrendBucket[],
  range: UiRange,
  bounds: { from: string; to: string },
): AnalysisTrendBucket[] {
  if (range === "today") {
    const byHour = new Map<number, Record<string, TrendModel>>();
    for (const bucket of trend) {
      if (bucket.day !== bounds.to) continue;
      byHour.set(bucket.minuteOfDay, bucket.byModel);
    }
    return Array.from({ length: 24 }, (_, hour) => ({
      day: bounds.to,
      minuteOfDay: hour,
      byModel: byHour.get(hour) ?? {},
    }));
  }
  const byDay = new Map<string, Record<string, TrendModel>>(
    trend.map((bucket) => [bucket.day, bucket.byModel] as const),
  );
  return eachDay(bounds.from, bounds.to).map((day) => ({
    day,
    minuteOfDay: 0,
    byModel: byDay.get(day) ?? {},
  }));
}

/** trendModelNames lists the models present in the range, largest first. */
function trendModelNames(buckets: AnalysisTrendBucket[], metric: MetricKey): string[] {
  const totals = new Map<string, number>();
  for (const bucket of buckets) {
    for (const [model, stats] of Object.entries(bucket.byModel)) {
      totals.set(model, (totals.get(model) ?? 0) + metricValue(stats, metric));
    }
  }
  return [...totals.keys()].sort(
    (left, right) => (totals.get(right) ?? 0) - (totals.get(left) ?? 0),
  );
}

export interface TrendOptionStyle {
  /** hidden names are left out of the series so the custom legend can toggle them. */
  hidden?: ReadonlySet<string>;
  colorFor?: (model: string) => string;
  theme?: ChartTheme;
  /** showLegend is false when the page renders its own legend controls. */
  showLegend?: boolean;
}

export function buildTrendOption(
  buckets: AnalysisTrendBucket[],
  interval: Interval,
  metric: MetricKey,
  modelFilter: string,
  style: TrendOptionStyle = {},
): EChartsCoreOption {
  const formatter = METRIC_FORMATTERS[metric];
  const allModels = trendModelNames(buckets, metric);
  const hidden = style.hidden ?? EMPTY_HIDDEN;
  const models = modelFilter
    ? allModels.includes(modelFilter)
      ? [modelFilter]
      : []
    : allModels.filter((model) => !hidden.has(model));
  const palette = style.theme?.palette ?? CHART_PALETTE;
  const colorFor = style.colorFor ?? ((model: string) => modelColor(model, palette));

  return {
    animationDuration: 250,
    grid: {
      left: 6,
      right: 14,
      top: (style.showLegend ?? true) && models.length > 0 ? 44 : 16,
      bottom: 4,
      containLabel: true,
    },
    tooltip: style.theme ? chartTooltip(style.theme) : CHART_TOOLTIP,
    legend: {
      show: (style.showLegend ?? true) && models.length > 0,
      type: "scroll",
      top: 0,
      icon: "roundRect",
      itemWidth: 10,
      itemHeight: 6,
      textStyle: { color: style.theme?.muted ?? "#8d99ad" },
      pageTextStyle: { color: style.theme?.muted ?? "#8d99ad" },
      // ECharts only packs legend click payloads (componentType/value) when
      // triggerEvent is on; without it the legend click handler never fires.
      triggerEvent: true,
      data: models.map((model) => model || UNKNOWN_KEY),
    },
    xAxis: {
      type: "category",
      boundaryGap: false,
      data: buckets.map((bucket) => bucketLabel(bucket, interval)),
      axisLabel: { color: style.theme?.muted ?? "#8d99ad", hideOverlap: true },
      axisLine: { lineStyle: { color: style.theme?.grid ?? "rgba(148,163,184,.15)" } },
      axisTick: { show: false },
    },
    yAxis: {
      type: "value",
      min: 0,
      axisLabel: {
        color: style.theme?.muted ?? "#8d99ad",
        formatter: (value: number) => formatter(value),
      },
      splitLine: { lineStyle: { color: style.theme?.grid ?? "rgba(148,163,184,.15)" } },
      axisLine: { show: false },
    },
    series: models.map((model) => {
      const color = colorFor(model);
      return {
        type: "line",
        name: model || UNKNOWN_KEY,
        stack: "total",
        smooth: true,
        data: buckets.map((bucket) => metricValue(bucket.byModel[model], metric)),
        showSymbol: false,
        lineStyle: { width: 1, color },
        itemStyle: { color },
        areaStyle: { opacity: 0.18, color },
        emphasis: { focus: "series" },
        tooltip: { valueFormatter: (value: number) => formatter(Number(value)) },
      };
    }),
  };
}

function logsHref(range: UiRange, filters: { model?: string; path?: string }): string {
  const params = new URLSearchParams({ range });
  if (filters.model) params.set("model", filters.model);
  if (filters.path) params.set("path", filters.path);
  return `/logs?${params.toString()}`;
}

function topValue(row: AnalysisTopRequest, tab: TopTab): string {
  switch (tab) {
    case "cost":
      return formatCost(row.costNanoUsd);
    case "tokens":
      return formatTokens(row.totalTokens);
    case "latency":
      return formatDuration(row.latencyMs);
  }
}

function HealthGrid({
  health,
  partial,
}: {
  health: AnalysisHealth;
  partial: boolean;
}): ReactElement {
  const items: { label: string; value: string; sub: string }[] = [
    { label: "请求", value: formatTokens(health.requests), sub: "当前区间" },
    {
      label: "错误率",
      value: formatPercent(health.errorRate),
      sub: `${formatTokens(health.errors)} 个错误`,
    },
    { label: "未计量", value: formatTokens(health.unmetered), sub: "usage 异常" },
    { label: "未解析", value: formatTokens(health.tooLarge), sub: "响应过大" },
    {
      label: "慢请求",
      value: formatTokens(health.slow.countOver),
      sub: `> ${formatDuration(health.slow.thresholdMs)}`,
    },
    {
      label: "P95",
      value: health.slow.p95Ms === null ? "—" : formatDuration(health.slow.p95Ms),
      sub: health.slow.p95Ms === null ? "超出明细保留期" : "延迟",
    },
  ];

  return (
    <div className="health-grid" style={partial ? { opacity: 0.72 } : undefined}>
      {items.map((item) => (
        <div className="health-item" key={item.label}>
          <div className="health-label">{item.label}</div>
          <div className="health-value">{item.value}</div>
          <div className="health-sub">{item.sub}</div>
        </div>
      ))}
    </div>
  );
}

function RankList({
  rows,
  colorFor,
  emptyLabel,
  onOpen,
}: {
  rows: AnalysisRow[];
  colorFor: (key: string) => string;
  emptyLabel: string;
  onOpen: (key: string) => void;
}): ReactElement {
  if (rows.length === 0) return <div className="log-empty">{emptyLabel}</div>;
  const max = Math.max(...rows.map((row) => row.costNanoUsd), 1);
  return (
    <div className="rank-list">
      {rows.map((row) => {
        const color = colorFor(row.key);
        return (
          <div
            className="rank rank-row"
            key={row.key || UNKNOWN_KEY}
            role="button"
            tabIndex={0}
            onClick={() => onOpen(row.key)}
            onKeyDown={(event) => {
              if (event.key !== "Enter" && event.key !== " ") return;
              event.preventDefault();
              onOpen(row.key);
            }}
          >
            <div className="rank-top">
              <span className="rank-name">
                <i className="sw" style={{ background: color }} />
                <span>{row.key || UNKNOWN_KEY}</span>
              </span>
              <span className="rank-val">{formatCost(row.costNanoUsd)}</span>
            </div>
            <div className="rank-bar">
              <i
                style={{
                  width: `${Math.max(2, (row.costNanoUsd / max) * 100)}%`,
                  background: color,
                }}
              />
            </div>
            <div className="rank-requests">
              {formatTokens(row.requests)} 请求 ·{" "}
              {compactNumber(row.promptTokens + row.completionTokens)} tok
            </div>
          </div>
        );
      })}
    </div>
  );
}

/** Analysis combines the model trend, top requests, health, cache and rankings. */
export function Analysis(): ReactElement {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const timeZone = useTimezone();
  const theme = useChartTheme();

  const range = parseRange(searchParams);
  const metric = parseMetric(searchParams);
  const topTab = parseTopTab(searchParams);
  const bounds = useMemo(() => rangeBounds(range, timeZone), [range, timeZone]);
  const interval: Interval = range === "today" ? "hour" : "day";

  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set<string>());
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const analysis = useQuery({
    queryKey: ["analysis", bounds.from, bounds.to, interval],
    queryFn: () =>
      api.getAnalysis({ from: bounds.from, to: bounds.to, interval }),
  });

  const data = analysis.data;
  const partial = data?.range.partial ?? false;

  const buckets = useMemo(
    () => fillBuckets(data?.trend ?? [], range, bounds),
    [data?.trend, range, bounds],
  );
  const modelNames = useMemo(() => trendModelNames(buckets, metric), [buckets, metric]);
  const visibleModels = useMemo(
    () => modelNames.filter((model) => !hidden.has(model)),
    [modelNames, hidden],
  );
  const colorFor = useCallback(
    (model: string) => modelColor(model, theme.palette),
    [theme.palette],
  );
  const chartHasData = useMemo(
    () =>
      visibleModels.some((model) =>
        buckets.some((bucket) => metricValue(bucket.byModel[model], metric) > 0),
      ),
    [buckets, visibleModels, metric],
  );
  const chartOption = useMemo(
    () =>
      buildTrendOption(buckets, interval, metric, "", {
        hidden,
        colorFor,
        theme,
        showLegend: false,
      }),
    [buckets, interval, metric, hidden, colorFor, theme],
  );

  const updateParam = useCallback(
    (key: string, value: string) => {
      setSearchParams(
        (current) => {
          const params = new URLSearchParams(current);
          params.set(key, value);
          return params;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const toggleModel = (model: string) => {
    setHidden((current) => {
      const next = new Set(current);
      if (next.has(model)) next.delete(model);
      else next.add(model);
      return next;
    });
  };

  const modelRank = useMemo(
    () =>
      [...(data?.models ?? [])]
        .sort((left, right) => right.costNanoUsd - left.costNanoUsd)
        .slice(0, 5),
    [data?.models],
  );
  const pathRank = useMemo(
    () =>
      [...(data?.paths ?? [])]
        .sort((left, right) => right.costNanoUsd - left.costNanoUsd)
        .slice(0, 5),
    [data?.paths],
  );

  const topRows = data?.topRequests[topTab] ?? [];
  const rangeLabel = RANGE_OPTIONS.find((option) => option.value === range)?.label ?? "";
  const topEmptyLabel = partial
    ? "超出明细保留期，无法显示 Top 榜单"
    : "当前区间没有请求";
  const rankEmptyLabel = partial ? "超出明细保留期，无法统计" : "当前区间没有请求";

  return (
    <>
      <PageHeader
        eyebrow="analysis"
        title="分析"
        subtitle="趋势、榜单与异常定位"
        actions={
          <RangeSeg
            value={range}
            options={RANGE_OPTIONS}
            onChange={(value) => updateParam("range", value)}
            ariaLabel="分析时间范围"
          />
        }
      />

      {analysis.isError && (
        <ErrorBanner error={analysis.error} onRetry={() => void analysis.refetch()} />
      )}

      {!data && analysis.isPending && (
        <div className="analysis-grid" aria-hidden="true">
          <div className="card analysis-trend">
            <div className="skeleton" style={{ height: 330, margin: 14 }} />
          </div>
          <div className="card top-card">
            <div className="skeleton" style={{ height: 330, margin: 14 }} />
          </div>
          <div className="card health-card">
            <div className="skeleton" style={{ height: 190, margin: 14 }} />
          </div>
          <div className="card cache-card">
            <div className="skeleton" style={{ height: 190, margin: 14 }} />
          </div>
          <div className="card rank-card">
            <div className="skeleton" style={{ height: 230, margin: 14 }} />
          </div>
          <div className="card rank-card">
            <div className="skeleton" style={{ height: 230, margin: 14 }} />
          </div>
        </div>
      )}

      {data && (
        <div className="analysis-grid">
          <article className="card analysis-trend">
            <div className="card-head chart-head">
              <div>
                <div className="card-title">按模型趋势</div>
                <div className="card-sub">
                  {rangeLabel} · 按模型 · {METRIC_LABELS[metric]}
                  {analysis.isFetching ? " · 更新中…" : ""}
                </div>
              </div>
              <div className="chart-tools">
                <div className="chart-mode" role="group" aria-label="指标">
                  {METRIC_OPTIONS.map((option) => (
                    <button
                      key={option.value}
                      type="button"
                      className={option.value === metric ? "on" : ""}
                      aria-pressed={option.value === metric}
                      onClick={() => updateParam("metric", option.value)}
                    >
                      {option.label}
                    </button>
                  ))}
                </div>
                <div className="legend" role="group" aria-label="模型图例">
                  {modelNames.map((model) => {
                    const off = hidden.has(model);
                    return (
                      <button
                        key={model || UNKNOWN_KEY}
                        type="button"
                        className={off ? "off" : ""}
                        aria-pressed={!off}
                        onClick={() => toggleModel(model)}
                      >
                        <i className="sw" style={{ background: colorFor(model) }} />
                        {model || UNKNOWN_KEY}
                      </button>
                    );
                  })}
                </div>
              </div>
            </div>
            <div className="chart-wrap analysis-chart">
              <LineChart
                option={chartOption}
                ariaLabel={`按模型拆分的${METRIC_LABELS[metric]}趋势图`}
                height={264}
                empty={modelNames.length === 0 || visibleModels.length === 0 || !chartHasData}
                emptyLabel={
                  modelNames.length === 0
                    ? "当前区间没有请求"
                    : visibleModels.length === 0
                      ? "没有可见模型 · 点击图例恢复"
                      : metric === "cost"
                        ? "当前区间没有已计价花费"
                        : "当前区间暂无数据"
                }
              />
            </div>
          </article>

          <article className="card top-card">
            <div className="card-head">
              <div>
                <div className="card-title">Top 榜单</div>
                <div className="card-sub">点击查看请求详情</div>
              </div>
              <RangeSeg
                value={topTab}
                options={TOP_OPTIONS}
                onChange={(value) => updateParam("top", value)}
                ariaLabel="榜单"
                small
              />
            </div>
            <div className="top-list">
              {topRows.length === 0 ? (
                <div className="log-empty">{topEmptyLabel}</div>
              ) : (
                topRows.slice(0, 8).map((row, index) => (
                  <div
                    className="top-row"
                    key={row.id}
                    role="button"
                    tabIndex={0}
                    onClick={() => setSelectedId(row.id)}
                    onKeyDown={(event) => {
                      if (event.key !== "Enter" && event.key !== " ") return;
                      event.preventDefault();
                      setSelectedId(row.id);
                    }}
                  >
                    <span className="top-rank">{String(index + 1).padStart(2, "0")}</span>
                    <div className="top-main">
                      <div className="top-model">
                        <i className="sw" style={{ background: colorFor(row.model) }} />
                        <span>{row.model || UNKNOWN_KEY}</span>
                      </div>
                      <div className="top-sub">
                        {formatTime(row.startedAt, timeZone)} · {row.pathClass}
                      </div>
                    </div>
                    <span className="top-value">{topValue(row, topTab)}</span>
                  </div>
                ))
              )}
            </div>
          </article>

          <article className="card health-card">
            <div className="card-head">
              <div>
                <div className="card-title">健康与异常</div>
                <div className="card-sub">
                  当前区间{partial ? " · 仅保留期内" : ""}
                </div>
              </div>
              {partial && <span className="status neutral">部分数据</span>}
            </div>
            <HealthGrid health={data.health} partial={partial} />
          </article>

          <article className="card cache-card">
            <div className="card-head">
              <div>
                <div className="card-title">缓存</div>
                <div className="card-sub">
                  {data.cache.available ? "估算节省" : "估算节省 · 不可用"}
                </div>
              </div>
            </div>
            <div className="cache-body">
              <div className="cache-main">
                <span className="cache-rate">{formatPercent(data.cache.hitRate)}</span>
                <span className="cache-meta">命中率</span>
              </div>
              <div className="cache-meta">
                <span>
                  缓存读 <b>{compactNumber(data.cache.cacheReadTokens)}</b>
                </span>
                <span>
                  输入 <b>{compactNumber(data.cache.promptTokens)}</b>
                </span>
                <span>
                  估算节省{" "}
                  <b>
                    {data.cache.available
                      ? formatCost(data.cache.savedNanoUsd ?? 0)
                      : "—"}
                  </b>
                </span>
              </div>
              <p className="cache-note">
                {data.cache.available
                  ? "按记录内的历史费率快照估算；未定价请求不计入。"
                  : "缓存节省超出明细保留期，无法估算。"}
              </p>
            </div>
          </article>

          <article className="card rank-card">
            <div className="card-head">
              <div>
                <div className="card-title">模型排行</div>
                <div className="card-sub">按花费 · 点击筛选日志</div>
              </div>
            </div>
            <RankList
              rows={modelRank}
              colorFor={colorFor}
              emptyLabel={rankEmptyLabel}
              onOpen={(model) => navigate(logsHref(range, { model }))}
            />
          </article>

          <article className="card rank-card">
            <div className="card-head">
              <div>
                <div className="card-title">路径排行</div>
                <div className="card-sub">按花费 · 点击筛选日志</div>
              </div>
            </div>
            <RankList
              rows={pathRank}
              colorFor={() => "var(--accent)"}
              emptyLabel={rankEmptyLabel}
              onOpen={(path) => navigate(logsHref(range, { path }))}
            />
          </article>
        </div>
      )}

      <RequestDrawer requestId={selectedId} onClose={() => setSelectedId(null)} />
    </>
  );
}
