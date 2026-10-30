import type { AnalysisRow, AnalysisTrendBucket } from "../api/types";
import type { ChartEventParams } from "../components/charts";

/** UNKNOWN_KEY labels trend series and ranking rows whose model key is empty. */
export const UNKNOWN_KEY = "（未知）";

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

/** trendHasModel reports whether any trend bucket carries the selected model. */
export function trendHasModel(buckets: AnalysisTrendBucket[], model: string): boolean {
  return buckets.some((bucket) => bucket.byModel[model] !== undefined);
}
