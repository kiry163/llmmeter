import { expect, it } from "vitest";
import { autoInterval, presetRange } from "./range";

it("picks interval by span", () => {
  expect(autoInterval(1)).toBe("minute");
  expect(autoInterval(3)).toBe("hour");
  expect(autoInterval(30)).toBe("day");
});

it("builds preset ranges", () => {
  expect(presetRange("7d", new Date(2026, 9, 29))).toEqual({
    from: "2026-10-23", to: "2026-10-29",
  });
});

it("resolves presets in the configured zone, not the browser zone", () => {
  // 20:00 UTC is already the next day in Asia/Shanghai.
  const at = new Date("2026-10-29T20:00:00Z");
  expect(presetRange("today", at, "Asia/Shanghai")).toEqual({
    from: "2026-10-30", to: "2026-10-30",
  });
  expect(presetRange("today", at, "UTC")).toEqual({
    from: "2026-10-29", to: "2026-10-29",
  });
  expect(presetRange("24h", at, "Asia/Shanghai")).toEqual({
    from: "2026-10-29", to: "2026-10-30",
  });
});
