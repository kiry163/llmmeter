// Usage parse outcomes and their presentation, mirroring internal/usage.

export type UsageTone = "success" | "neutral" | "info" | "warning" | "danger";

const usageLabels: Record<string, string> = {
  ok: "正常",
  no_usage: "无用量",
  parse_failed: "解析失败",
  too_large: "未解析（响应过大）",
  not_applicable: "不适用",
  error: "错误",
};

const usageTones: Record<string, UsageTone> = {
  ok: "success",
  no_usage: "neutral",
  not_applicable: "info",
  too_large: "warning",
  parse_failed: "danger",
  error: "danger",
};

/** usageLabel maps a usage status to its Chinese label; unknown echoes itself. */
export function usageLabel(status: string): string {
  return usageLabels[status] ?? status;
}

/** usageTone maps a usage status to a badge tone; unknown is neutral. */
export function usageTone(status: string): UsageTone {
  return usageTones[status] ?? "neutral";
}
