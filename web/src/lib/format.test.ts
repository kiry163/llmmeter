import { describe, expect, it } from "vitest";
import { formatBytes, formatCost, formatDuration } from "./format";

describe("format", () => {
  it("formats bytes", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(1536)).toBe("1.5 KiB");
  });
  it("formats nano USD", () => {
    expect(formatCost(2_000_000_000)).toBe("$2.0000");
    expect(formatCost(1_500)).toBe("$0.0000015");
  });
  it("formats duration", () => {
    expect(formatDuration(900)).toBe("900ms");
    expect(formatDuration(65_000)).toBe("1m 5s");
  });
});
