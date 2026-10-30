import type { AnalysisRow, AnalysisTrendBucket } from "../api/types";

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
