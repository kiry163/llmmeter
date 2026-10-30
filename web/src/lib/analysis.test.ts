import { describe, expect, it } from "vitest";
import type { AnalysisRow, AnalysisTrendBucket } from "../api/types";
import { filterModelRows, parseModelFilter, trendHasModel } from "./analysis";

function row(key: string, requests: number): AnalysisRow {
  return {
    key,
    requests,
    errors: 0,
    upBytes: 0,
    downBytes: 0,
    promptTokens: 0,
    completionTokens: 0,
    cacheReadTokens: 0,
    costNanoUsd: 0,
    unpriced: 0,
  };
}

function bucket(day: string, models: string[]): AnalysisTrendBucket {
  return {
    day,
    minuteOfDay: 0,
    byModel: Object.fromEntries(
      models.map((model) => [
        model,
        {
          requests: 1,
          promptTokens: 0,
          completionTokens: 0,
          costNanoUsd: 0,
          upBytes: 0,
          downBytes: 0,
        },
      ]),
    ),
  };
}

describe("analysis model drill-down", () => {
  it("reads and trims the model parameter", () => {
    expect(parseModelFilter(new URLSearchParams("model=deepseek-chat"))).toBe(
      "deepseek-chat",
    );
    expect(parseModelFilter(new URLSearchParams("model=%20gpt-4o%20"))).toBe("gpt-4o");
    expect(parseModelFilter(new URLSearchParams())).toBe("");
  });

  it("keeps every ranking row when no model is selected", () => {
    const rows = [row("a", 2), row("b", 1)];
    expect(filterModelRows(rows, "")).toEqual(rows);
  });

  it("keeps only the selected model in the ranking", () => {
    const rows = [row("a", 2), row("b", 1)];
    expect(filterModelRows(rows, "b").map((item) => item.key)).toEqual(["b"]);
    expect(filterModelRows(rows, "missing")).toEqual([]);
  });

  it("reports whether the trend carries the selected model", () => {
    const buckets = [bucket("2026-10-29", ["a"]), bucket("2026-10-30", ["b"])];
    expect(trendHasModel(buckets, "b")).toBe(true);
    expect(trendHasModel(buckets, "missing")).toBe(false);
    expect(trendHasModel([], "a")).toBe(false);
  });
});
