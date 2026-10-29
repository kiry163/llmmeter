import type { Interval } from "../api/types";

/** RangePreset is one of the overview time-range shortcuts. */
export type RangePreset = "today" | "24h" | "7d" | "30d" | "custom";

/** DateRange is an inclusive local-day range in YYYY-MM-DD form. */
export interface DateRange {
  from: string;
  to: string;
}

/** RANGE_PRESETS lists the shortcuts in display order. */
export const RANGE_PRESETS: readonly RangePreset[] = ["today", "24h", "7d", "30d", "custom"];

/** RANGE_LABELS maps each shortcut to its Chinese label. */
export const RANGE_LABELS: Record<RangePreset, string> = {
  today: "今天",
  "24h": "24h",
  "7d": "7d",
  "30d": "30d",
  custom: "自定义",
};

const DATE_RE = /^(\d{4})-(\d{2})-(\d{2})$/;

function formatDay(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${date.getFullYear()}-${month}-${day}`;
}

function shiftDay(date: Date, days: number): Date {
  const shifted = new Date(date.getFullYear(), date.getMonth(), date.getDate());
  shifted.setDate(shifted.getDate() + days);
  return shifted;
}

/**
 * presetRange resolves a shortcut against nowLocal. The API accepts local
 * dates only, so "24h" is the two-calendar-day window ending today.
 */
export function presetRange(preset: RangePreset, nowLocal: Date = new Date()): DateRange {
  const to = formatDay(nowLocal);
  switch (preset) {
    case "today":
      return { from: to, to };
    case "24h":
      return { from: formatDay(shiftDay(nowLocal, -1)), to };
    case "7d":
      return { from: formatDay(shiftDay(nowLocal, -6)), to };
    case "30d":
      return { from: formatDay(shiftDay(nowLocal, -29)), to };
    case "custom":
      return { from: to, to };
  }
}

/** autoInterval picks a series bucket from an inclusive day span. */
export function autoInterval(spanDays: number): Interval {
  if (!Number.isFinite(spanDays) || spanDays <= 2) return "minute";
  if (spanDays <= 7) return "hour";
  return "day";
}

/** isRangePreset reports whether value is a known shortcut. */
export function isRangePreset(value: string | null | undefined): value is RangePreset {
  return RANGE_PRESETS.includes(value as RangePreset);
}

/** isDateString reports whether value is a YYYY-MM-DD date. */
export function isDateString(value: string | null | undefined): value is string {
  return typeof value === "string" && DATE_RE.test(value);
}

function parseDay(value: string): number | null {
  const match = DATE_RE.exec(value);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  return Date.UTC(year, month - 1, day);
}

/** rangeDays counts the inclusive days of a validated date range. */
export function rangeDays(range: DateRange): number {
  const from = parseDay(range.from);
  const to = parseDay(range.to);
  if (from === null || to === null || to < from) return 1;
  return Math.round((to - from) / 86_400_000) + 1;
}
