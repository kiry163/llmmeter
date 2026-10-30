import { describe, expect, it } from "vitest";
import { tokensPerSecond } from "./speed";

describe("tokensPerSecond", () => {
  it("computes stream generation speed", () => {
    expect(tokensPerSecond(710, 6000, 500, true)).toBeCloseTo(129.09, 1);
  });
  it("returns null for non-stream, missing ttft, or non-positive generation", () => {
    expect(tokensPerSecond(710, 6000, 500, false)).toBeNull();
    expect(tokensPerSecond(710, 6000, null, true)).toBeNull();
    expect(tokensPerSecond(710, 500, 500, true)).toBeNull();
    expect(tokensPerSecond(0, 6000, 500, true)).toBeNull();
  });
  it("clamps generation to 1ms", () => {
    expect(tokensPerSecond(1, 10, 9.5, true)).toBeCloseTo(1000, 1);
  });
});
