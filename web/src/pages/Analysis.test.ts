import { describe, expect, it } from "vitest";
import type { AnalysisTrendBucket } from "../api/types";
import { buildTrendOption } from "./Analysis";

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
          costNanoUsd: 1,
          upBytes: 0,
          downBytes: 0,
        },
      ]),
    ),
  };
}

describe("analysis trend option", () => {
  it("packs legend click payloads so legend clicks reach the click handler", () => {
    const option = buildTrendOption([bucket("2026-10-30", ["a", "b"])], "hour", "cost", "");
    const legend = option.legend as { show?: boolean; triggerEvent?: boolean };
    expect(legend.show).toBe(true);
    expect(legend.triggerEvent).toBe(true);
  });

  it("names stacked series after their model", () => {
    const option = buildTrendOption([bucket("2026-10-30", ["a", ""])], "hour", "cost", "");
    const series = option.series as { name: string }[];
    expect(series.map((item) => item.name)).toEqual(["a", "（未知）"]);
  });
});
