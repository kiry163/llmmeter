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
