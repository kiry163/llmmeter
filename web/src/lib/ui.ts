import type { SeriesPoint } from "../api/types";
import { presetRange } from "./range";

export const RANGE_OPTIONS = [
  { value: "today", label: "今日" },
  { value: "7d", label: "7 天" },
  { value: "30d", label: "30 天" },
] as const;

export type UiRange = (typeof RANGE_OPTIONS)[number]["value"];

export function rangeBounds(range: UiRange, timeZone: string | undefined): {
  from: string;
  to: string;
} {
  return presetRange(range, new Date(), timeZone);
}

export function modelColor(name: string, palette: readonly string[]): string {
  let hash = 0;
  for (let i = 0; i < name.length; i += 1) {
    hash = (hash * 31 + name.charCodeAt(i)) | 0;
  }
  return palette[Math.abs(hash) % palette.length] ?? palette[0];
}

export function compactNumber(value: number): string {
  if (!Number.isFinite(value)) return "0";
  return new Intl.NumberFormat("en-US", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);
}

export function shortTime(iso: string, timeZone?: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  try {
    return new Intl.DateTimeFormat("en-GB", {
      timeZone,
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    })
      .format(date)
      .replace(",", "");
  } catch {
    return iso;
  }
}

export function hourLabel(point: SeriesPoint, interval: "hour" | "day"): string {
  if (interval === "hour") return `${String(point.minuteOfDay).padStart(2, "0")}:00`;
  return point.day.slice(5);
}

export function statusTone(status: number): "ok" | "warn" | "err" {
  if (status >= 500) return "err";
  if (status >= 400) return "warn";
  return "ok";
}

export function deltaTone(current: number, previous: number): "up" | "down" | "flat" {
  if (current > previous) return "up";
  if (current < previous) return "down";
  return "flat";
}
