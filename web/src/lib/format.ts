const byteUnits = ["B", "KiB", "MiB", "GiB", "TiB"] as const;

/** formatBytes renders a byte count with binary units and one decimal. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < byteUnits.length - 1) {
    value /= 1024;
    unit += 1;
  }
  if (unit === 0) return `${Math.round(value)} B`;
  return `${value.toFixed(1)} ${byteUnits[unit]}`;
}

/** formatBytesDecimal renders decimal units (KB/MB/GB) for dashboard labels. */
export function formatBytesDecimal(bytes: number, digits = 1): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"] as const;
  let value = bytes;
  let unit = 0;
  while (value >= 1000 && unit < units.length - 1) {
    value /= 1000;
    unit += 1;
  }
  return unit === 0 ? `${Math.round(value)} B` : `${value.toFixed(digits)} ${units[unit]}`;
}

/**
 * formatCost converts nano USD to USD. Amounts of at least one cent keep four
 * decimals; smaller amounts keep up to nine decimals without trailing zeros.
 */
export function formatCost(nanoUsd: number): string {
  if (!Number.isFinite(nanoUsd)) return "$0";
  const usd = nanoUsd / 1_000_000_000;
  const sign = usd < 0 ? "-" : "";
  const abs = Math.abs(usd);
  if (abs >= 0.01) return `${sign}$${abs.toFixed(4)}`;
  const fixed = abs.toFixed(9).replace(/0+$/, "").replace(/\.$/, "");
  return `${sign}$${fixed || "0"}`;
}

/** formatTokens renders an integer count with thousands separators. */
export function formatTokens(count: number): string {
  if (!Number.isFinite(count)) return "0";
  return Math.round(count).toLocaleString("en-US");
}

/** formatDuration renders milliseconds as ms, s, m/s or h/m. */
export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) {
    const seconds = (ms / 1000).toFixed(1).replace(/\.0$/, "");
    return `${seconds}s`;
  }
  if (ms < 3_600_000) {
    const minutes = Math.floor(ms / 60_000);
    const seconds = Math.floor((ms % 60_000) / 1000);
    return `${minutes}m ${seconds}s`;
  }
  const hours = Math.floor(ms / 3_600_000);
  const minutes = Math.floor((ms % 3_600_000) / 60_000);
  return `${hours}h ${minutes}m`;
}

/** formatOptionalDuration renders a nullable duration, em dash when absent. */
export function formatOptionalDuration(ms: number | null | undefined): string {
  return ms == null ? "—" : formatDuration(ms);
}

/** formatRate renders tokens per second with one decimal, em dash when absent. */
export function formatRate(value: number | null): string {
  if (value === null || !Number.isFinite(value)) return "—";
  return `${value.toFixed(1)} t/s`;
}

/**
 * formatPercent renders a 0–1 ratio as a percentage: one decimal at or above
 * 10%, two decimals below it, and trailing zeros dropped. Em dash when absent.
 */
export function formatPercent(ratio: number): string {
  if (!Number.isFinite(ratio)) return "—";
  const percent = ratio * 100;
  if (percent === 0) return "0%";
  const fixed = Math.abs(percent) >= 10 ? percent.toFixed(1) : percent.toFixed(2);
  return `${fixed.replace(/\.?0+$/, "")}%`;
}

/**
 * formatBytesWithExact renders a byte count and, when different, its exact
 * integer value too. Sub-KiB values render once instead of twice.
 */
export function formatBytesWithExact(bytes: number): string {
  const primary = formatBytes(bytes);
  const exact = `${formatTokens(bytes)} B`;
  return primary === exact ? primary : `${primary} · ${exact}`;
}

/** formatTime renders an RFC 3339 timestamp in tz (local when omitted). */
export function formatTime(iso: string, tz?: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone: tz,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  }).formatToParts(date);
  const pick = (type: Intl.DateTimeFormatPartTypes) =>
    parts.find((part) => part.type === type)?.value ?? "";
  return `${pick("year")}-${pick("month")}-${pick("day")} ${pick("hour")}:${pick("minute")}:${pick("second")}`;
}
