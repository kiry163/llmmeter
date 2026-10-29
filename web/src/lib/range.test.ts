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
