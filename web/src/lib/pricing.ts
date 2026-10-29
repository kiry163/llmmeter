/**
 * Pricing helpers mirroring the backend window rules (internal/settings and
 * internal/pricing). DaysMask bit 0 is Monday and bit 6 is Sunday.
 */

export const MAX_WINDOWS = 32;

export const WEEKDAY_LABELS = ["周一", "周二", "周三", "周四", "周五", "周六", "周日"] as const;

/** WindowInput-like shape; rates are optional so drafts can be validated. */
export interface WindowRates {
  label: string;
  daysMask: number;
  startMinute: number;
  endMinute: number;
  inputPerMTok?: number | null;
  outputPerMTok?: number | null;
  cacheReadPerMTok?: number | null;
  cacheWritePerMTok?: number | null;
  reasoningPerMTok?: number | null;
}

export interface WindowTiming {
  daysMask: number;
  startMinute: number;
  endMinute: number;
}

export interface ModelFormInput {
  name: string;
  inputPerMTok: number;
  outputPerMTok: number;
  cacheReadPerMTok: number;
  cacheWritePerMTok: number;
  reasoningPerMTok: number | null;
}

export type FormErrors = Record<string, string>;

const RATE_KEYS = [
  "inputPerMTok",
  "outputPerMTok",
  "cacheReadPerMTok",
  "cacheWritePerMTok",
  "reasoningPerMTok",
] as const;

function isWholeMinute(value: number): boolean {
  return Number.isInteger(value) && value >= 0 && value <= 1439;
}

/**
 * validateWindows mirrors the backend validation and reports every problem as
 * "<index>.<field>" so the editor can annotate individual inputs.
 */
export function validateWindows(windows: readonly WindowRates[]): FormErrors {
  const errors: FormErrors = {};
  if (windows.length > MAX_WINDOWS) {
    errors.windows = `最多 ${MAX_WINDOWS} 个时段窗口`;
    return errors;
  }
  windows.forEach((window, index) => {
    const prefix = `${index}.`;
    if (window.label == null || window.label.trim() === "") {
      errors[`${prefix}label`] = "请填写时段名称";
    }
    if (!isWholeMinute(window.startMinute)) {
      errors[`${prefix}startMinute`] = "开始时间需在 00:00–23:59";
    }
    if (!isWholeMinute(window.endMinute)) {
      errors[`${prefix}endMinute`] = "结束时间需在 00:00–23:59";
    } else if (
      isWholeMinute(window.startMinute) &&
      window.startMinute === window.endMinute
    ) {
      errors[`${prefix}endMinute`] = "开始与结束时间不能相同";
    }
    if (!Number.isInteger(window.daysMask) || window.daysMask < 1 || window.daysMask > 127) {
      errors[`${prefix}daysMask`] = "至少选择一个星期";
    }
    for (const key of RATE_KEYS) {
      const rate = window[key];
      if (rate != null && !(Number.isFinite(rate) && rate >= 0)) {
        errors[`${prefix}${key}`] = "需为不小于 0 的数字";
      }
    }
  });
  return errors;
}

/** validateModel mirrors the backend base rate validation. */
export function validateModel(input: ModelFormInput): FormErrors {
  const errors: FormErrors = {};
  if (input.name.trim() === "") errors.name = "请填写模型名称";
  for (const key of RATE_KEYS) {
    const rate = input[key];
    if (rate != null && !(Number.isFinite(rate) && rate >= 0)) {
      errors[key] = "需为不小于 0 的数字";
    }
  }
  return errors;
}

/** formatMinute renders a minutes-since-midnight value as HH:MM. */
export function formatMinute(minute: number): string {
  if (!Number.isFinite(minute)) return "";
  const hours = Math.floor(minute / 60);
  const minutes = minute % 60;
  return `${String(hours).padStart(2, "0")}:${String(minutes).padStart(2, "0")}`;
}

/** parseTimeInput converts HH:MM to minutes, returning NaN for invalid input. */
export function parseTimeInput(value: string): number {
  const match = /^(\d{1,2}):(\d{2})$/.exec(value.trim());
  if (!match) return Number.NaN;
  const hours = Number(match[1]);
  const minutes = Number(match[2]);
  if (hours > 23 || minutes > 59) return Number.NaN;
  return hours * 60 + minutes;
}

/** parseRateInput turns a text field into a rate; blank inherits (null). */
export function parseRateInput(value: string): number | null {
  if (value.trim() === "") return null;
  return Number(value);
}

/**
 * describeWindow renders the day run and wall-clock span, e.g.
 * "周一–周五 09:00–12:00"; cross-midnight spans read "22:00–02:00".
 */
export function describeWindow(window: WindowTiming): string {
  return `${describeDays(window.daysMask)} ${formatMinute(window.startMinute)}–${formatMinute(window.endMinute)}`;
}

function describeDays(mask: number): string {
  const days: number[] = [];
  for (let index = 0; index < 7; index += 1) {
    if ((mask & (1 << index)) !== 0) days.push(index);
  }
  if (days.length === 0) return "未选择";
  if (days.length === 7) return "每天";
  const parts: string[] = [];
  let runStart = days[0];
  let previous = days[0];
  for (let i = 1; i <= days.length; i += 1) {
    const day = days[i];
    if (day === previous + 1) {
      previous = day;
      continue;
    }
    parts.push(runStart === previous ? WEEKDAY_LABELS[runStart] : `${WEEKDAY_LABELS[runStart]}–${WEEKDAY_LABELS[previous]}`);
    runStart = day;
    previous = day;
  }
  return parts.join("、");
}

const WEEKDAY_INDEX: Record<string, number> = {
  Sun: 0,
  Mon: 1,
  Tue: 2,
  Wed: 3,
  Thu: 4,
  Fri: 5,
  Sat: 6,
};

/** wallClock reads the wall-clock minute and weekday in timeZone. */
function wallClock(at: Date, timeZone?: string): { minute: number; weekday: number } {
  if (!timeZone) {
    return { minute: at.getHours() * 60 + at.getMinutes(), weekday: at.getDay() };
  }
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    hour: "2-digit",
    minute: "2-digit",
    weekday: "short",
    hour12: false,
  }).formatToParts(at);
  const pick = (type: Intl.DateTimeFormatPartTypes): string =>
    parts.find((part) => part.type === type)?.value ?? "";
  // Some engines render midnight as "24" with hour12:false.
  const hour = Number(pick("hour")) % 24;
  return { minute: hour * 60 + Number(pick("minute")), weekday: WEEKDAY_INDEX[pick("weekday")] ?? 0 };
}

/**
 * windowMatches reports whether at falls inside window, replicating the
 * backend's local wall-clock rule in timeZone (the backend's configured zone):
 * the day mask applies to the start day for the evening part and to the
 * previous day for the part after midnight.
 */
export function windowMatches(window: WindowTiming, at: Date, timeZone?: string): boolean {
  if (window.endMinute === window.startMinute) return false;
  const { minute, weekday } = wallClock(at, timeZone);
  const current = 1 << ((weekday + 6) % 7);
  const previous = 1 << ((weekday + 5) % 7);
  if (window.endMinute > window.startMinute) {
    return (window.daysMask & current) !== 0 && minute >= window.startMinute && minute < window.endMinute;
  }
  return (
    ((window.daysMask & current) !== 0 && minute >= window.startMinute) ||
    ((window.daysMask & previous) !== 0 && minute < window.endMinute)
  );
}

/** activeWindow returns the first window matching at in timeZone, if any. */
export function activeWindow<T extends WindowTiming>(
  windows: readonly T[],
  at: Date,
  timeZone?: string,
): T | null {
  return windows.find((window) => windowMatches(window, at, timeZone)) ?? null;
}
