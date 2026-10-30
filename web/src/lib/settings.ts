import type { Settings } from "../api/types";

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

/**
 * maskValue renders a secret as bullets plus its last four characters. Very
 * short secrets are fully masked so the value is never shown verbatim.
 */
export function maskValue(secret: string): string {
  const value = secret.trim();
  if (!value) return "未设置";
  if (value.length <= 4) return "••••";
  return `••••${value.slice(-4)}`;
}

/** RedactedSettings is the display-only projection of the API payload. */
export interface RedactedSettings {
  upstream: {
    baseUrl: string;
    timeoutS: number;
    apiKey: string;
  };
  timezone: string;
  retentionDays: number;
  gatewayKey: string;
  adminPasswordSet: boolean;
  listen: string;
  dbPath: string;
  dbSizeBytes: number;
  version: string;
}

/**
 * redactSettings turns the masked API payload into strings safe to render.
 * The API never sends secrets, so no plaintext is dropped here.
 */
export function redactSettings(settings: Settings): RedactedSettings {
  const { apiKey } = settings.upstream;
  const gateway = settings.gatewayKey;
  return {
    upstream: {
      baseUrl: settings.upstream.baseUrl,
      timeoutS: settings.upstream.timeoutS,
      apiKey: apiKey.set ? `••••${apiKey.last4}` : "未设置",
    },
    timezone: settings.timezone,
    retentionDays: settings.retentionDays,
    gatewayKey: gateway.set
      ? `${gateway.prefix}${gateway.last4 ? `••••${gateway.last4}` : "••••"}`
      : "未设置",
    adminPasswordSet: settings.adminPasswordSet,
    listen: settings.listen,
    dbPath: settings.dbPath,
    dbSizeBytes: settings.dbSizeBytes,
    version: settings.version,
  };
}

/** validateTimeout checks the upstream timeout in seconds. */
export function validateTimeout(raw: string): string | undefined {
  const value = Number(raw);
  if (raw.trim() === "" || !Number.isInteger(value) || value < 1 || value > 3600) {
    return "超时需在 1–3600 秒之间";
  }
  return undefined;
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

/**
 * validateClearPassword requires the current password before clearing it.
 * The server still verifies the value; this only blocks an empty submit.
 */
export function validateClearPassword(currentPassword: string): string | undefined {
  if (currentPassword.trim() === "") return "清除密码需要当前密码";
  return undefined;
}

/** SettingsDraft is the editable state of the settings form. */
export interface SettingsDraft {
  timeoutS: string;
  retentionDays: string;
  currentPassword: string;
  newPassword: string;
  confirmPassword: string;
  /** passwordSet is true when the server already has an admin password. */
  passwordSet: boolean;
}

/**
 * validateSettings validates the numeric ranges and an optional password
 * change. Leaving every password field empty means "keep the current one".
 */
export function validateSettings(draft: SettingsDraft): FormErrors {
  const errors: FormErrors = {};

  const timeout = validateTimeout(draft.timeoutS);
  if (timeout) errors.timeoutS = timeout;

  const retention = validateRetention(draft.retentionDays);
  if (retention) errors.retentionDays = retention;

  const changing =
    draft.currentPassword !== "" || draft.newPassword !== "" || draft.confirmPassword !== "";
  if (changing) {
    if (draft.passwordSet && draft.currentPassword === "") {
      errors.currentPassword = "请输入当前密码";
    }
    if (draft.newPassword.length < 8) {
      errors.newPassword = "新密码至少需要 8 个字符";
    } else if (draft.confirmPassword !== draft.newPassword) {
      errors.confirmPassword = "两次输入的新密码不一致";
    }
  }

  return errors;
}
