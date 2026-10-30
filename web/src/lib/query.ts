import type { RequestParams } from "../api/client";
import { DEFAULT_RANGE_PRESET, isDateString, isRangePreset, presetRange } from "./range";
import type { RangePreset } from "./range";

/** REQUESTS_PAGE_LIMIT is the page size used by the explorer. */
export const REQUESTS_PAGE_LIMIT = 50;

/** PATH_CLASS_FILTERS lists the path classes the gateway records. */
export const PATH_CLASS_FILTERS = ["chat", "responses", "models", "audio", "other"] as const;

export type PathClassFilter = (typeof PATH_CLASS_FILTERS)[number];

/** USAGE_STATUS_FILTERS lists every usage parse outcome. */
export const USAGE_STATUS_FILTERS = [
  "ok",
  "no_usage",
  "parse_failed",
  "too_large",
  "not_applicable",
  "error",
] as const;

export type UsageStatusFilter = (typeof USAGE_STATUS_FILTERS)[number];

/** USAGE_STATUS_LABELS maps each usage outcome to its Chinese label. */
export const USAGE_STATUS_LABELS: Record<UsageStatusFilter, string> = {
  ok: "正常",
  no_usage: "无用量",
  parse_failed: "解析失败",
  too_large: "体量过大",
  not_applicable: "不适用",
  error: "错误",
};

/** PricedFilter narrows the list to priced or unpriced requests. */
export type PricedFilter = "all" | "priced" | "unpriced";

/** PRICED_FILTER_LABELS maps each pricing filter to its Chinese label. */
export const PRICED_FILTER_LABELS: Record<PricedFilter, string> = {
  all: "全部",
  priced: "已定价",
  unpriced: "未定价",
};

/** RequestsFilters is the filter set persisted in the URL. */
export interface RequestsFilters {
  preset: RangePreset;
  from: string;
  to: string;
  model: string;
  pathClass: PathClassFilter | "";
  status: string;
  usageStatus: UsageStatusFilter | "";
  priced: PricedFilter;
  stream?: "all" | "stream" | "nonstream";
}

/**
 * parseRequestsFilters reads the explorer filters from the URL. Unknown values
 * fall back to the defaults so a hand-edited URL never triggers a bad request.
 */
export function parseRequestsFilters(
  params: URLSearchParams,
  timeZone?: string,
): RequestsFilters {
  const rawPreset = params.get("range");
  const rawFrom = params.get("from");
  const rawTo = params.get("to");
  let preset: RangePreset = DEFAULT_RANGE_PRESET;
  let from: string;
  let to: string;

  if (isRangePreset(rawPreset) && rawPreset !== "custom") {
    preset = rawPreset;
    ({ from, to } = presetRange(preset, new Date(), timeZone));
  } else if (
    rawPreset === "custom" &&
    isDateString(rawFrom) &&
    isDateString(rawTo) &&
    rawFrom <= rawTo
  ) {
    preset = "custom";
    from = rawFrom;
    to = rawTo;
  } else {
    ({ from, to } = presetRange(DEFAULT_RANGE_PRESET, new Date(), timeZone));
  }

  const rawModel = (params.get("model") ?? "").trim();
  const rawPathClass = params.get("pathClass") ?? "";
  const rawStatus = params.get("status") ?? "";
  const rawUsageStatus = params.get("usageStatus") ?? "";
  const rawPriced = params.get("priced");

  return {
    preset,
    from,
    to,
    model: rawModel,
    pathClass: PATH_CLASS_FILTERS.includes(rawPathClass as PathClassFilter)
      ? (rawPathClass as PathClassFilter)
      : "",
    status: /^\d{3}$/.test(rawStatus) ? rawStatus : "",
    usageStatus: USAGE_STATUS_FILTERS.includes(rawUsageStatus as UsageStatusFilter)
      ? (rawUsageStatus as UsageStatusFilter)
      : "",
    priced: rawPriced === "priced" || rawPriced === "unpriced" ? rawPriced : "all",
  };
}

/** serializeRequestsFilters writes only active filters back to the URL. */
export function serializeRequestsFilters(filters: RequestsFilters): URLSearchParams {
  const params = new URLSearchParams();
  params.set("range", filters.preset);
  if (filters.preset === "custom") {
    params.set("from", filters.from);
    params.set("to", filters.to);
  }
  if (filters.model) params.set("model", filters.model);
  if (filters.pathClass) params.set("pathClass", filters.pathClass);
  if (filters.status) params.set("status", filters.status);
  if (filters.usageStatus) params.set("usageStatus", filters.usageStatus);
  if (filters.priced !== "all") params.set("priced", filters.priced);
  return params;
}

/** RequestsQueryOptions mirrors the /api/requests query parameters. */
export interface RequestsQueryOptions {
  from?: string;
  to?: string;
  model?: string;
  pathClass?: string;
  status?: number;
  usageStatus?: string;
  priced?: boolean;
  beforeId?: number;
  limit?: number;
}

function append(
  search: URLSearchParams,
  key: string,
  value: string | number | boolean | null | undefined,
): void {
  if (value === null || value === undefined) return;
  if (typeof value === "string" && value.trim() === "") return;
  if (typeof value === "number" && !Number.isFinite(value)) return;
  search.set(key, String(value));
}

/**
 * buildRequestsQuery renders the /api/requests query string. Empty filters are
 * omitted, false is preserved for `priced`, and the cursor is `before_id`.
 */
export function buildRequestsQuery(options: RequestsQueryOptions = {}): string {
  const search = new URLSearchParams();
  append(search, "from", options.from);
  append(search, "to", options.to);
  append(search, "model", options.model);
  append(search, "pathClass", options.pathClass);
  append(search, "status", options.status);
  append(search, "usageStatus", options.usageStatus);
  append(search, "priced", options.priced);
  append(search, "before_id", options.beforeId);
  append(search, "limit", options.limit ?? REQUESTS_PAGE_LIMIT);
  return search.toString();
}

/** filtersToApiParams converts the URL-backed filters to API parameters. */
export function filtersToApiParams(filters: RequestsFilters): RequestParams {
  return {
    from: filters.from || undefined,
    to: filters.to || undefined,
    model: filters.model || undefined,
    pathClass: filters.pathClass || undefined,
    status: filters.status ? Number(filters.status) : undefined,
    usageStatus: filters.usageStatus || undefined,
    priced: filters.priced === "all" ? undefined : filters.priced === "priced",
  };
}
