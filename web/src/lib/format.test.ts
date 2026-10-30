import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatBytesWithExact,
  formatCost,
  formatDuration,
  formatOptionalDuration,
  formatPercent,
  formatRate,
} from "./format";

describe("format", () => {
  it("formats bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
  });
  it("shows exact bytes only when they add information", () => {
    expect(formatBytesWithExact(512)).toBe("512 B");
    expect(formatBytesWithExact(1024)).toBe("1.0 KiB · 1,024 B");
  });
  it("formats nano USD", () => {
    expect(formatCost(2_000_000_000)).toBe("$2.0000");
    expect(formatCost(1_500)).toBe("$0.0000015");
  });
  it("formats duration", () => {
    expect(formatDuration(900)).toBe("900ms");
    expect(formatDuration(65_000)).toBe("1m 5s");
    expect(formatOptionalDuration(null)).toBe("—");
    expect(formatOptionalDuration(900)).toBe("900ms");
  });
  it("formats error rate and cache hit", () => {
    expect(formatPercent(0.0083)).toBe("0.83%");
    expect(formatPercent(0.444)).toBe("44.4%");
    expect(formatPercent(0)).toBe("0%");
  });
  it("formats rates and handles null", () => {
    expect(formatRate(null)).toBe("—");
    expect(formatRate(129.09)).toBe("129.1 t/s");
    expect(formatRate(0)).toBe("0.0 t/s");
  });
});
