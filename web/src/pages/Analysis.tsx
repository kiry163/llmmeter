import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { EChartsCoreOption } from "echarts/core";
import { useCallback, useMemo, useState, type ReactElement, type ReactNode } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type {
  AnalysisCache,
  AnalysisHealth,
  AnalysisRow,
  AnalysisTopRequest,
  AnalysisTrendBucket,
  Interval,
  TrendModel,
} from "../api/types";
import { DataTable } from "../components/DataTable";
import type { Column } from "../components/DataTable";
import { ErrorBanner } from "../components/ErrorBanner";
import { PageHeader } from "../components/PageHeader";
import { RangeBar } from "../components/RangeBar";
import { RequestDrawer } from "../components/RequestDrawer";
import { Skeleton } from "../components/Skeleton";
import {
  CHART_GRID,
  CHART_MUTED,
  CHART_PALETTE,
  CHART_TOOLTIP,
  LineChart,
} from "../components/charts";
import type { ChartEventParams } from "../components/charts";
import { Badge, Card } from "../components/ui";
import { useTimezone } from "../hooks/useTimezone";
import {
  formatBytes,
  formatCost,
  formatDuration,
  formatPercent,
  formatTime,
  formatTokens,
} from "../lib/format";
import { autoInterval, parseRangeSelection, rangeDays } from "../lib/range";
import type { RangeSelection } from "../lib/range";

type MetricKey = "cost" | "tokens" | "traffic" | "requests";

type TopTab = "cost" | "tokens" | "latency";

type RankKey = "requests" | "errors" | "tokens" | "cacheRead" | "traffic" | "cost";

/** RankSort is a ranking order; metric records which trend metric it belongs to. */
interface RankSort {
  metric: MetricKey;
  key: RankKey;
  direction: "asc" | "desc";
}

const METRICS: readonly MetricKey[] = ["cost", "tokens", "traffic", "requests"];

const METRIC_LABELS: Record<MetricKey, string> = {
  cost: "花费",
  tokens: "Tokens",
  traffic: "流量",
  requests: "请求",
};

const TOP_TABS: readonly TopTab[] = ["cost", "tokens", "latency"];

const TOP_LABELS: Record<TopTab, string> = {
  cost: "花费",
  tokens: "Tokens",
  latency: "耗时",
};

const INTERVAL_LABELS: Record<Interval, string> = {
  minute: "按分钟",
  hour: "按小时",
  day: "按天",
};

const RANK_LABELS: Record<RankKey, string> = {
  requests: "请求",
  errors: "错误",
  tokens: "Tokens",
  cacheRead: "缓存读",
  traffic: "流量",
  cost: "花费",
};

const DEFAULT_METRIC: MetricKey = "cost";

const DEFAULT_TOP_TAB: TopTab = "cost";

const UNKNOWN_KEY = "（未知）";

/** metricValue reads one model's contribution for the active trend metric. */
function metricValue(model: TrendModel, metric: MetricKey): number {
  switch (metric) {
    case "cost":
      return model.costNanoUsd;
    case "tokens":
      return model.promptTokens + model.completionTokens;
    case "traffic":
      return model.upBytes + model.downBytes;
    case "requests":
      return model.requests;
  }
}

/** METRIC_FORMATTERS renders tooltips and the y axis for each trend metric. */
const METRIC_FORMATTERS: Record<MetricKey, (value: number) => string> = {
  cost: formatCost,
  tokens: formatTokens,
  traffic: formatBytes,
  requests: formatTokens,
};

const METRIC_UNITS: Record<MetricKey, string> = {
  cost: "USD",
  tokens: "tokens",
  traffic: "字节",
  requests: "次",
};

function parseMetric(params: URLSearchParams): MetricKey {
  const raw = params.get("metric") ?? "";
  return METRICS.includes(raw as MetricKey) ? (raw as MetricKey) : DEFAULT_METRIC;
}

function parseTopTab(params: URLSearchParams): TopTab {
  const raw = params.get("top") ?? "";
  return TOP_TABS.includes(raw as TopTab) ? (raw as TopTab) : DEFAULT_TOP_TAB;
}

/** bucketLabel renders a trend bucket at the interval's precision. */
function bucketLabel(bucket: AnalysisTrendBucket, interval: Interval): string {
  if (interval === "day") return bucket.day;
  const day = bucket.day.slice(5);
  if (interval === "hour") {
    return `${day} ${String(bucket.minuteOfDay).padStart(2, "0")}:00`;
  }
  const hour = String(Math.floor(bucket.minuteOfDay / 60)).padStart(2, "0");
  const minute = String(bucket.minuteOfDay % 60).padStart(2, "0");
  return `${day} ${hour}:${minute}`;
}

function buildTrendOption(
  buckets: AnalysisTrendBucket[],
  interval: Interval,
  metric: MetricKey,
): EChartsCoreOption {
  const formatter = METRIC_FORMATTERS[metric];
  const totals = new Map<string, number>();
  for (const bucket of buckets) {
    for (const [model, stats] of Object.entries(bucket.byModel)) {
      totals.set(model, (totals.get(model) ?? 0) + metricValue(stats, metric));
    }
  }
  const models = [...totals.keys()].sort(
    (left, right) => (totals.get(right) ?? 0) - (totals.get(left) ?? 0),
  );

  return {
    animationDuration: 250,
    color: CHART_PALETTE,
    grid: { left: 8, right: 16, top: models.length > 0 ? 44 : 16, bottom: 8, containLabel: true },
    tooltip: { trigger: "axis", axisPointer: { type: "line" }, ...CHART_TOOLTIP },
    legend: {
      show: models.length > 0,
      type: "scroll",
      top: 0,
      icon: "roundRect",
      itemWidth: 10,
      itemHeight: 6,
      textStyle: { color: CHART_MUTED },
      pageTextStyle: { color: CHART_MUTED },
    },
    xAxis: {
      type: "category",
      boundaryGap: false,
      data: buckets.map((bucket) => bucketLabel(bucket, interval)),
      axisLabel: { color: CHART_MUTED, hideOverlap: true },
      axisLine: { lineStyle: { color: CHART_GRID } },
      axisTick: { show: false },
    },
    yAxis: {
      type: "value",
      name: METRIC_UNITS[metric],
      nameTextStyle: { color: CHART_MUTED },
      axisLabel: { color: CHART_MUTED, formatter },
      splitLine: { lineStyle: { color: CHART_GRID } },
      axisLine: { show: false },
    },
    series: models.map((model) => ({
      type: "line",
      name: model || UNKNOWN_KEY,
      stack: "total",
      data: buckets.map((bucket) => {
        const stats = bucket.byModel[model];
        return stats ? metricValue(stats, metric) : 0;
      }),
      showSymbol: false,
      lineStyle: { width: 1 },
      areaStyle: { opacity: 0.45 },
      emphasis: { focus: "series" },
      tooltip: { valueFormatter: (value: number) => formatter(Number(value)) },
    })),
  };
}

/** rankValue reads the sortable numeric field of a ranking row. */
function rankValue(row: AnalysisRow, key: RankKey): number {
  switch (key) {
    case "requests":
      return row.requests;
    case "errors":
      return row.errors;
    case "tokens":
      return row.promptTokens + row.completionTokens;
    case "cacheRead":
      return row.cacheReadTokens;
    case "traffic":
      return row.upBytes + row.downBytes;
    case "cost":
      return row.costNanoUsd;
  }
}

function effectiveSort(sort: RankSort | null, metric: MetricKey): RankSort {
  if (sort && sort.metric === metric) return sort;
  return { metric, key: metric, direction: "desc" };
}

/** logsHref builds a log URL carrying an explicit range and any filters. */
function logsHref(
  selection: { from: string; to: string },
  filters: Record<string, string>,
): string {
  const params = new URLSearchParams();
  params.set("range", "custom");
  params.set("from", selection.from);
  params.set("to", selection.to);
  for (const [key, value] of Object.entries(filters)) {
    if (value) params.set(key, value);
  }
  return `/logs?${params.toString()}`;
}

function SortHeader({
  label,
  rankKey,
  sort,
  onSort,
}: {
  label: string;
  rankKey: RankKey;
  sort: RankSort;
  onSort: (key: RankKey) => void;
}): ReactElement {
  const active = sort.key === rankKey;
  return (
    <button
      type="button"
      onClick={() => onSort(rankKey)}
      aria-label={`按${label}排序`}
      aria-pressed={active}
      className={`inline-flex items-center gap-1 whitespace-nowrap ${
        active ? "text-ink" : "text-muted hover:text-ink"
      }`}
    >
      {label}
      <span aria-hidden="true">{active ? (sort.direction === "desc" ? "↓" : "↑") : "↕"}</span>
    </button>
  );
}

/** Metric renders one compact label/value/hint block, as a button when clickable. */
function Metric({
  label,
  value,
  hint,
  onClick,
}: {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  onClick?: () => void;
}): ReactElement {
  const body = (
    <>
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-1 min-h-7 text-xl font-semibold leading-tight tabular-nums">{value}</div>
      {hint && <div className="mt-1 text-xs text-muted">{hint}</div>}
    </>
  );
  const base = "rounded-lg border border-line bg-surface-raised p-3";
  if (onClick) {
    return (
      <button
        type="button"
        onClick={onClick}
        className={`${base} text-left transition-colors hover:border-accent`}
      >
        {body}
      </button>
    );
  }
  return <div className={base}>{body}</div>;
}

function HealthSection({
  health,
  partial,
  onOpenLogs,
}: {
  health: AnalysisHealth;
  partial: boolean;
  onOpenLogs: (filters: Record<string, string>) => void;
}): ReactElement {
  const slowHint = `> ${formatDuration(health.slow.thresholdMs)}`;
  return (
    <Card title="异常与慢请求">
      <div className="grid grid-cols-2 gap-2 lg:grid-cols-4">
        <Metric
          label="请求"
          value={formatTokens(health.requests)}
          hint={partial ? "仅保留期内" : undefined}
        />
        <Metric
          label="错误"
          value={formatTokens(health.errors)}
          hint={`错误率 ${formatPercent(health.errorRate)} · 点击查看日志`}
          onClick={() => onOpenLogs({})}
        />
        <Metric
          label="未计量"
          value={formatTokens(health.unmetered)}
          hint="用量未解析"
        />
        <Metric
          label="未解析（响应过大）"
          value={formatTokens(health.tooLarge)}
          hint="点击查看日志"
          onClick={() => onOpenLogs({ usageStatus: "too_large" })}
        />
        <Metric
          label="客户端断开"
          value={formatTokens(health.clientDisconnected)}
          hint="请求未完成即断开"
        />
        <Metric label="慢请求" value={formatTokens(health.slow.countOver)} hint={`总耗时 ${slowHint}`} />
        <Metric
          label="P95 总耗时"
          value={health.slow.p95Ms === null ? "—" : formatDuration(health.slow.p95Ms)}
          hint={
            health.slow.p95Ms === null
              ? "超出明细保留期，无法估算"
              : "总耗时分布"
          }
        />
      </div>
    </Card>
  );
}

function CacheSection({ cache }: { cache: AnalysisCache }): ReactElement {
  return (
    <Card title="缓存">
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-3">
        <Metric label="命中率" value={formatPercent(cache.hitRate)} hint="cacheRead / prompt" />
        <Metric label="缓存读 tokens" value={formatTokens(cache.cacheReadTokens)} />
        <Metric
          label="估算节省"
          value={cache.available ? formatCost(cache.savedNanoUsd ?? 0) : "—"}
          hint={
            cache.available ? "按价格快照估算" : "超出明细保留期，无法估算"
          }
        />
      </div>
    </Card>
  );
}

/** Analysis combines the model trend, rankings, top requests and health. */
export function Analysis(): ReactElement {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const timeZone = useTimezone();
  const selection = useMemo(
    () => parseRangeSelection(searchParams, timeZone),
    [searchParams, timeZone],
  );
  const metric = parseMetric(searchParams);
  const topTab = parseTopTab(searchParams);
  const interval = autoInterval(rangeDays(selection));
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [modelSort, setModelSort] = useState<RankSort | null>(null);
  const [pathSort, setPathSort] = useState<RankSort | null>(null);

  const analysis = useQuery({
    queryKey: ["analysis", selection.from, selection.to, interval],
    queryFn: () =>
      api.getAnalysis({ from: selection.from, to: selection.to, interval }),
    placeholderData: keepPreviousData,
  });

  const data = analysis.data;
  const partial = data?.range.partial ?? false;

  const trendOption = useMemo(
    () => buildTrendOption(data?.trend ?? [], interval, metric),
    [data?.trend, interval, metric],
  );

  const openLogs = useCallback(
    (filters: Record<string, string>) => {
      navigate(logsHref(selection, filters));
    },
    [navigate, selection],
  );

  const trendEvents = useMemo(
    () => ({
      click: (params: ChartEventParams) => {
        const model = params.seriesName === UNKNOWN_KEY ? "" : (params.seriesName ?? "");
        openLogs(model ? { model } : {});
      },
    }),
    [openLogs],
  );

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

  const updateParam = (key: string, value: string) => {
    setSearchParams(
      (current) => {
        const params = new URLSearchParams(current);
        params.set(key, value);
        return params;
      },
      { replace: true },
    );
  };

  const effectiveModelSort = effectiveSort(modelSort, metric);
  const effectivePathSort = effectiveSort(pathSort, metric);

  const modelRows = useMemo(() => {
    const rows = [...(data?.models ?? [])];
    rows.sort((left, right) => {
      const diff = rankValue(left, effectiveModelSort.key) - rankValue(right, effectiveModelSort.key);
      return effectiveModelSort.direction === "desc" ? -diff : diff;
    });
    return rows;
  }, [data?.models, effectiveModelSort]);

  const pathRows = useMemo(() => {
    const rows = [...(data?.paths ?? [])];
    rows.sort((left, right) => {
      const diff = rankValue(left, effectivePathSort.key) - rankValue(right, effectivePathSort.key);
      return effectivePathSort.direction === "desc" ? -diff : diff;
    });
    return rows;
  }, [data?.paths, effectivePathSort]);

  const toggleSort = (
    current: RankSort | null,
    key: RankKey,
  ): RankSort => {
    const base = effectiveSort(current, metric);
    if (base.key === key) {
      return { metric, key, direction: base.direction === "desc" ? "asc" : "desc" };
    }
    return { metric, key, direction: "desc" };
  };

  const rankingColumns = (
    name: string,
    sort: RankSort,
    onSort: (key: RankKey) => void,
  ): Column<AnalysisRow>[] => [
    {
      key: "name",
      header: name,
      render: (row) => (
        <span className="inline-flex flex-wrap items-center gap-2">
          <span className="font-medium">{row.key || UNKNOWN_KEY}</span>
          {row.unpriced > 0 && <Badge tone="warning">未定价 {formatTokens(row.unpriced)}</Badge>}
        </span>
      ),
    },
    {
      key: "requests",
      header: <SortHeader label="请求" rankKey="requests" sort={sort} onSort={onSort} />,
      className: "text-right tabular-nums",
      render: (row) => formatTokens(row.requests),
    },
    {
      key: "errors",
      header: <SortHeader label="错误" rankKey="errors" sort={sort} onSort={onSort} />,
      className: "text-right tabular-nums",
      render: (row) => formatTokens(row.errors),
    },
    {
      key: "tokens",
      header: <SortHeader label="Tokens" rankKey="tokens" sort={sort} onSort={onSort} />,
      className: "text-right tabular-nums",
      render: (row) => formatTokens(row.promptTokens + row.completionTokens),
    },
    {
      key: "cacheRead",
      header: <SortHeader label="缓存读" rankKey="cacheRead" sort={sort} onSort={onSort} />,
      className: "text-right tabular-nums",
      render: (row) => formatTokens(row.cacheReadTokens),
    },
    {
      key: "traffic",
      header: <SortHeader label="流量" rankKey="traffic" sort={sort} onSort={onSort} />,
      className: "text-right tabular-nums",
      render: (row) => formatBytes(row.upBytes + row.downBytes),
    },
    {
      key: "cost",
      header: <SortHeader label="花费" rankKey="cost" sort={sort} onSort={onSort} />,
      className: "text-right tabular-nums",
      render: (row) => formatCost(row.costNanoUsd),
    },
  ];

  const topColumns: Column<AnalysisTopRequest>[] = [
    {
      key: "time",
      header: "时间",
      className: "whitespace-nowrap text-xs text-muted",
      render: (row) => formatTime(row.startedAt, timeZone),
    },
    { key: "model", header: "模型", render: (row) => row.model || UNKNOWN_KEY },
    {
      key: "stream",
      header: "流式",
      render: (row) => (
        <Badge tone={row.stream ? "info" : "neutral"}>
          {row.stream ? "流式" : "非流式"}
        </Badge>
      ),
    },
    {
      key: "tokens",
      header: "Tokens",
      className: "text-right tabular-nums",
      render: (row) => formatTokens(row.totalTokens),
    },
    {
      key: "cost",
      header: "费用",
      className: "text-right tabular-nums",
      render: (row) => formatCost(row.costNanoUsd),
    },
    {
      key: "latency",
      header: "总耗时",
      className: "text-right tabular-nums",
      render: (row) => formatDuration(row.latencyMs),
    },
  ];

  const topRows = data?.topRequests[topTab] ?? [];

  return (
    <div className="space-y-6">
      <PageHeader title="分析" description="按模型拆分的趋势、Top 榜单、异常与缓存" />

      <section className="space-y-3" aria-labelledby="analysis-range-heading">
        <div className="flex flex-col gap-2 xl:flex-row xl:flex-wrap xl:items-center xl:justify-between">
          <div className="flex flex-wrap items-center gap-2">
            <h2 id="analysis-range-heading" className="text-sm font-semibold">
              区间
            </h2>
            <span className="text-xs text-muted">
              {selection.from} 至 {selection.to} · {INTERVAL_LABELS[interval]}
            </span>
            {partial && <Badge tone="warning">仅保留期内</Badge>}
            {analysis.isFetching && (
              <span className="text-xs text-muted" role="status">
                更新中…
              </span>
            )}
          </div>
          <RangeBar value={selection} onChange={updateRange} timeZone={timeZone} />
        </div>

        {analysis.isError && (
          <ErrorBanner error={analysis.error} onRetry={() => void analysis.refetch()} />
        )}
      </section>

      {analysis.isPending && !data && (
        <div className="space-y-4">
          <Skeleton className="h-[360px] w-full" />
          <Skeleton className="h-48 w-full" />
          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <Skeleton className="h-48 w-full" />
            <Skeleton className="h-48 w-full" />
          </div>
        </div>
      )}

      {data && (
        <>
          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            <Card
              title="用量趋势"
              actions={
                <div className="flex flex-wrap gap-1" role="group" aria-label="趋势指标">
                  {METRICS.map((key) => (
                    <button
                      key={key}
                      type="button"
                      aria-pressed={metric === key}
                      onClick={() => updateParam("metric", key)}
                      className={`rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                        metric === key ? "bg-accent/15 text-accent" : "text-muted hover:text-ink"
                      }`}
                    >
                      {METRIC_LABELS[key]}
                    </button>
                  ))}
                </div>
              }
            >
              <div className="mb-2 text-xs text-muted">
                按模型堆叠 · 点击图例切换显示，点击曲线跳转日志
              </div>
              <LineChart
                option={trendOption}
                ariaLabel={`按模型拆分的${METRIC_LABELS[metric]}趋势图`}
                height={320}
                empty={data.trend.length === 0}
                emptyLabel="该区间暂无数据"
                onEvents={trendEvents}
              />
            </Card>

            <Card
              title="Top 榜单"
              actions={
                <div className="flex flex-wrap gap-1" role="group" aria-label="Top 榜单类型">
                  {TOP_TABS.map((tab) => (
                    <button
                      key={tab}
                      type="button"
                      aria-pressed={topTab === tab}
                      disabled={partial}
                      onClick={() => updateParam("top", tab)}
                      className={`rounded-md px-2 py-1 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
                        topTab === tab ? "bg-accent/15 text-accent" : "text-muted hover:text-ink"
                      }`}
                    >
                      {TOP_LABELS[tab]}
                    </button>
                  ))}
                </div>
              }
            >
              {partial ? (
                <p className="rounded-md border border-dashed border-line px-4 py-6 text-center text-sm text-muted">
                  超出明细保留期，无法显示 Top 榜单
                </p>
              ) : (
                <DataTable
                  columns={topColumns}
                  rows={topRows}
                  rowKey={(row) => String(row.id)}
                  onRowClick={(row) => setSelectedId(row.id)}
                  empty={<span className="text-sm text-muted">该区间暂无数据</span>}
                />
              )}
            </Card>

            <HealthSection
              health={data.health}
              partial={partial}
              onOpenLogs={openLogs}
            />
            <CacheSection cache={data.cache} />
          </div>

          <Card
            title="模型排名"
            actions={
              <span className="text-xs text-muted">
                按{RANK_LABELS[effectiveModelSort.key]}排序 · 点击表头切换
              </span>
            }
          >
            <DataTable
              columns={rankingColumns("模型", effectiveModelSort, (key) =>
                setModelSort((current) => toggleSort(current, key)),
              )}
              rows={modelRows}
              rowKey={(row) => row.key || UNKNOWN_KEY}
              onRowClick={(row) => openLogs(row.key ? { model: row.key } : {})}
              empty={<span className="text-sm text-muted">该区间暂无数据</span>}
            />
          </Card>

          <Card
            title="路径排名"
            actions={
              <span className="text-xs text-muted">
                按{RANK_LABELS[effectivePathSort.key]}排序 · 点击表头切换
              </span>
            }
          >
            <DataTable
              columns={rankingColumns("路径", effectivePathSort, (key) =>
                setPathSort((current) => toggleSort(current, key)),
              )}
              rows={pathRows}
              rowKey={(row) => row.key || UNKNOWN_KEY}
              onRowClick={(row) => openLogs(row.key ? { pathClass: row.key } : {})}
              empty={<span className="text-sm text-muted">该区间暂无数据</span>}
            />
          </Card>
        </>
      )}

      <RequestDrawer requestId={selectedId} onClose={() => setSelectedId(null)} />
    </div>
  );
}
