import type { AnalysisRow, AnalysisTrendBucket, AnalysisTrendSeries } from "../api/types";
import type { ChartEventParams } from "../components/charts";

/** UNKNOWN_KEY labels trend series and ranking rows whose model key is empty. */
export const UNKNOWN_KEY = "（未知）";

/** TrendStats is one model's aggregated contribution inside a chart bucket. */
export interface TrendStats {
  requests: number;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens: number;
  costNanoUsd: number;
  upBytes: number;
  downBytes: number;
}

/** TrendBucket is the chart-facing bucket; upstreams are folded into models. */
export interface TrendBucket {
  day: string;
  minuteOfDay: number;
  byModel: Record<string, TrendStats>;
}

/**
 * chartClickModel resolves the model behind a trend chart click or null when
 * the click carries no usable model. ECharts reports curve clicks with
 * seriesName, while legend clicks report componentType "legend" and put the
 * entry name in value (they never carry seriesName).
 */
export function chartClickModel(params: ChartEventParams): string | null {
  const label =
    params.componentType === "legend"
      ? (params.value ?? params.name ?? params.seriesName)
      : params.seriesName;
  if (typeof label !== "string") return null;
  const model = label.trim();
  if (!model || model === UNKNOWN_KEY) return null;
  return model;
}

/** parseModelFilter reads the overview model drill-down from the URL. */
export function parseModelFilter(params: URLSearchParams): string {
  return (params.get("model") ?? "").trim();
}

/** filterModelRows keeps the drill-down model, or every row when unset. */
export function filterModelRows(rows: AnalysisRow[], model: string): AnalysisRow[] {
  return model ? rows.filter((row) => row.key === model) : rows;
}

/** aggregateTrend folds per-upstream series into per-model chart buckets. */
export function aggregateTrend(trend: AnalysisTrendBucket[]): TrendBucket[] {
  return trend.map((bucket) => {
    const byModel: Record<string, TrendStats> = {};
    for (const series of bucket.series) {
      const model = series.model || UNKNOWN_KEY;
      const stats = (byModel[model] ??= emptyStats());
      addSeries(stats, series);
    }
    return { day: bucket.day, minuteOfDay: bucket.minuteOfDay, byModel };
  });
}

function emptyStats(): TrendStats {
  return {
    requests: 0,
    promptTokens: 0,
    completionTokens: 0,
    cacheReadTokens: 0,
    costNanoUsd: 0,
    upBytes: 0,
    downBytes: 0,
  };
}

function addSeries(stats: TrendStats, series: AnalysisTrendSeries): void {
  stats.requests += series.requests;
  stats.promptTokens += series.promptTokens;
  stats.completionTokens += series.completionTokens;
  stats.cacheReadTokens += series.cacheReadTokens;
  stats.costNanoUsd += series.costNanoUsd;
  stats.upBytes += series.upBytes;
  stats.downBytes += series.downBytes;
}

/** trendHasModel reports whether any trend bucket carries the selected model. */
export function trendHasModel(buckets: TrendBucket[], model: string): boolean {
  return buckets.some((bucket) => bucket.byModel[model] !== undefined);
}

/** ModelRankRow is the common shape of breakdown and analysis ranking rows. */
export interface ModelRankRow {
  upstreamId: string;
  upstreamName: string;
  key: string;
  requests: number;
  errors: number;
  upBytes: number;
  downBytes: number;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens?: number;
  costNanoUsd: number;
  unpriced: number;
}

/** mergeRankRows folds per-upstream ranking rows into one row per key. */
export function mergeRankRows(rows: ModelRankRow[]): AnalysisRow[] {
  const byModel = new Map<string, AnalysisRow>();
  for (const row of rows) {
    const current = byModel.get(row.key);
    if (!current) {
      byModel.set(row.key, {
        ...row,
        cacheReadTokens: row.cacheReadTokens ?? 0,
        upstreamId: "",
        upstreamName: row.upstreamName,
      });
      continue;
    }
    current.requests += row.requests;
    current.errors += row.errors;
    current.upBytes += row.upBytes;
    current.downBytes += row.downBytes;
    current.promptTokens += row.promptTokens;
    current.completionTokens += row.completionTokens;
    current.cacheReadTokens += row.cacheReadTokens ?? 0;
    current.costNanoUsd += row.costNanoUsd;
    current.unpriced += row.unpriced;
    if (row.upstreamName && !current.upstreamName.split(" / ").includes(row.upstreamName)) {
      current.upstreamName = current.upstreamName
        ? `${current.upstreamName} / ${row.upstreamName}`
        : row.upstreamName;
    }
  }
  return [...byModel.values()];
}
