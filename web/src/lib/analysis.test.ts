import { describe, expect, it } from "vitest";
import type { AnalysisRow, AnalysisTrendBucket } from "../api/types";
import {
  UNKNOWN_KEY,
  aggregateTrend,
  chartClickModel,
  filterModelRows,
  mergeRankRows,
  parseModelFilter,
  trendHasModel,
} from "./analysis";
import type { TrendBucket } from "./analysis";

function row(key: string, requests: number, upstreamName = "u"): AnalysisRow {
  return {
    upstreamId: upstreamName,
    upstreamName,
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

function apiBucket(day: string, models: string[]): AnalysisTrendBucket {
  return {
    day,
    minuteOfDay: 0,
    series: models.map((model) => ({
      upstreamId: "u",
      upstreamName: "u",
      model,
      requests: 1,
      promptTokens: 2,
      completionTokens: 3,
      cacheReadTokens: 0,
      costNanoUsd: 0,
      upBytes: 0,
      downBytes: 0,
    })),
  };
}

function bucket(day: string, models: string[]): TrendBucket {
  return aggregateTrend([
    apiBucket(
      day,
      models,
    ),
  ])[0] ?? { day, minuteOfDay: 0, byModel: {} };
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

describe("trend aggregation", () => {
  it("folds the same model across upstreams into one series", () => {
    const buckets = aggregateTrend([
      {
        day: "2026-10-30",
        minuteOfDay: 1,
        series: [
          {
            upstreamId: "a",
            upstreamName: "A",
            model: "m",
            requests: 1,
            promptTokens: 10,
            completionTokens: 5,
            cacheReadTokens: 2,
            costNanoUsd: 3,
            upBytes: 1,
            downBytes: 2,
          },
          {
            upstreamId: "b",
            upstreamName: "B",
            model: "m",
            requests: 2,
            promptTokens: 20,
            completionTokens: 8,
            cacheReadTokens: 4,
            costNanoUsd: 6,
            upBytes: 3,
            downBytes: 4,
          },
        ],
      },
    ]);
    expect(buckets[0].byModel.m).toEqual({
      requests: 3,
      promptTokens: 30,
      completionTokens: 13,
      cacheReadTokens: 6,
      costNanoUsd: 9,
      upBytes: 4,
      downBytes: 6,
    });
  });

  it("merges ranking rows by model and keeps upstream names", () => {
    const merged = mergeRankRows([
      { ...row("m", 2, "A"), costNanoUsd: 1 },
      { ...row("m", 3, "B"), costNanoUsd: 2 },
    ]);
    expect(merged).toHaveLength(1);
    expect(merged[0].requests).toBe(5);
    expect(merged[0].costNanoUsd).toBe(3);
    expect(merged[0].upstreamName).toBe("A / B");
  });
});

describe("chartClickModel", () => {
  it("reads the model from a curve click", () => {
    expect(
      chartClickModel({ componentType: "series", seriesName: "deepseek-chat", seriesIndex: 0 }),
    ).toBe("deepseek-chat");
    expect(chartClickModel({ seriesName: "gpt-4o" })).toBe("gpt-4o");
  });

  it("reads the model from a legend click", () => {
    expect(
      chartClickModel({
        componentType: "legend",
        name: "deepseek-chat",
        value: "deepseek-chat",
      }),
    ).toBe("deepseek-chat");
    expect(chartClickModel({ componentType: "legend", value: "gpt-4o" })).toBe("gpt-4o");
    expect(chartClickModel({ componentType: "legend", name: "claude-sonnet" })).toBe(
      "claude-sonnet",
    );
  });

  it("never falls back to the axis bucket label on a curve click", () => {
    expect(chartClickModel({ componentType: "series", name: "10-30 12:00" })).toBeNull();
  });

  it("drops the unknown bucket placeholder", () => {
    expect(chartClickModel({ componentType: "series", seriesName: UNKNOWN_KEY })).toBeNull();
    expect(chartClickModel({ componentType: "legend", value: UNKNOWN_KEY })).toBeNull();
  });

  it("returns null when the click carries no model", () => {
    expect(chartClickModel({})).toBeNull();
    expect(chartClickModel({ componentType: "legend" })).toBeNull();
    expect(chartClickModel({ componentType: "legend", value: 42 })).toBeNull();
    expect(chartClickModel({ componentType: "series", seriesName: "  " })).toBeNull();
    expect(chartClickModel({ componentType: "xAxis", name: "10-30" })).toBeNull();
  });
});
