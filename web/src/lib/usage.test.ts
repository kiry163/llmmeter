import { describe, expect, it } from "vitest";
import { usageLabel, usageTone } from "./usage";

describe("usageLabel", () => {
  it("labels every usage outcome", () => {
    expect(usageLabel("ok")).toBe("正常");
    expect(usageLabel("no_usage")).toBe("无用量");
    expect(usageLabel("parse_failed")).toBe("解析失败");
    expect(usageLabel("too_large")).toBe("未解析（响应过大）");
    expect(usageLabel("not_applicable")).toBe("不适用");
    expect(usageLabel("error")).toBe("错误");
  });
  it("maps too_large and unknown", () => {
    expect(usageLabel("too_large")).toBe("未解析（响应过大）");
    expect(usageLabel("weird")).toBe("weird");
  });
});

describe("usageTone", () => {
  it("tones every usage outcome", () => {
    expect(usageTone("ok")).toBe("success");
    expect(usageTone("no_usage")).toBe("neutral");
    expect(usageTone("not_applicable")).toBe("info");
    expect(usageTone("too_large")).toBe("warning");
    expect(usageTone("parse_failed")).toBe("danger");
    expect(usageTone("error")).toBe("danger");
  });
  it("treats unknown statuses as neutral", () => {
    expect(usageTone("weird")).toBe("neutral");
  });
});
