/** COMMON_TIMEZONES lists the zones offered before the browser zone. */
export const COMMON_TIMEZONES = [
  "Asia/Shanghai",
  "Asia/Hong_Kong",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Asia/Kolkata",
  "Europe/London",
  "Europe/Paris",
  "Europe/Berlin",
  "America/New_York",
  "America/Chicago",
  "America/Denver",
  "America/Los_Angeles",
  "Australia/Sydney",
  "UTC",
] as const;

/** FormErrors maps a field name to a human-readable validation message. */
export type FormErrors = Record<string, string>;

/** browserTimezone returns the browser's IANA zone, defaulting to UTC. */
export function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
  } catch {
    return "UTC";
  }
}

const timezoneCache = new Map<string, string | undefined>();

/**
 * resolveTimezone returns a usable IANA zone or undefined. The backend may
 * report "Local" for the host zone; Intl cannot format with that name, so the
 * caller falls back to the browser zone.
 */
export function resolveTimezone(zone: string | null | undefined): string | undefined {
  const value = zone?.trim();
  const key = value ?? "";
  if (timezoneCache.has(key)) return timezoneCache.get(key);
  let resolved: string | undefined;
  if (value && value !== "Local") {
    try {
      new Intl.DateTimeFormat("en-US", { timeZone: value }).format();
      resolved = value;
    } catch {
      resolved = undefined;
    }
  }
  timezoneCache.set(key, resolved);
  return resolved;
}

/**
 * timezoneOptions returns the common zones followed by the current zone and
 * the browser zone, without duplicates.
 */
export function timezoneOptions(current: string): string[] {
  const options: string[] = [...COMMON_TIMEZONES];
  for (const zone of [current.trim(), browserTimezone()]) {
    if (zone && !options.includes(zone)) options.push(zone);
  }
  return options;
}

/** validateRetention checks the detail retention window in days. */
export function validateRetention(raw: string): string | undefined {
  const value = Number(raw);
  if (raw.trim() === "" || !Number.isInteger(value) || value < 7 || value > 3650) {
    return "保留天数需在 7–3650 天之间";
  }
  return undefined;
}

/**
 * listenIsLoopback reports whether a listen address binds only the local
 * machine. An empty or wildcard host (":8100", "0.0.0.0:8100") is not.
 */
export function listenIsLoopback(listen: string): boolean {
  const value = listen.trim();
  if (value === "") return false;
  const bracketed = /^\[([^\]]+)\](?::\d+)?$/.exec(value);
  let host = bracketed ? bracketed[1] : value;
  if (!bracketed && (value.match(/:/g)?.length ?? 0) === 1) {
    host = value.slice(0, value.lastIndexOf(":"));
  }
  if (host === "") return false;
  return host.toLowerCase() === "localhost" || host === "127.0.0.1" || host === "::1";
}
