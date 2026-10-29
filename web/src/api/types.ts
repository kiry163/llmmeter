// Wire types mirroring the JSON contracts of internal/api (spec §6).

export interface Bootstrap {
  version: string;
  needsSetup: boolean;
  authRequired: boolean;
  listen: string;
  timezone: string;
}

export interface Me {
  authRequired: boolean;
  authenticated: boolean;
}

export interface Health {
  ok: boolean;
  dbWritable: boolean;
  queueDepth: number;
  version: string;
}

export interface OverviewStats {
  requests: number;
  errors: number;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens: number;
  reasoningTokens: number;
  totalTokens: number;
  costNanoUsd: number;
  upBytes: number;
  downBytes: number;
  priced: number;
  unpriced: number;
  unmetered: number;
  latencyP50Ms: number | null;
  latencyP95Ms: number | null;
  latencyAvgMs: number | null;
}

export interface Overview {
  from: string;
  to: string;
  partial: boolean;
  current: OverviewStats;
  previous: OverviewStats;
}

export type Interval = "minute" | "hour" | "day";

export interface SeriesPoint {
  day: string;
  minuteOfDay: number;
  requests: number;
  errors: number;
  upBytes: number;
  downBytes: number;
  promptTokens: number;
  completionTokens: number;
  costNanoUsd: number;
}

export interface Series {
  points: SeriesPoint[];
}

export type BreakdownBy = "model" | "pathClass";

export interface BreakdownRow {
  key: string;
  requests: number;
  errors: number;
  upBytes: number;
  downBytes: number;
  promptTokens: number;
  completionTokens: number;
  costNanoUsd: number;
  unpriced: number;
}

export interface Breakdown {
  by: BreakdownBy;
  rows: BreakdownRow[];
}

/** Pricing snapshot stored with a request; shape is upstream defined. */
export interface RatesSnapshot {
  [key: string]: unknown;
}

export interface RequestRecord {
  id: number;
  startedAt: string;
  completedAt: string;
  localDay: string;
  minuteOfDay: number;
  path: string;
  pathClass: string;
  model: string;
  upstreamModel: string;
  stream: boolean;
  status: number;
  latencyMs: number;
  ttftMs: number | null;

  promptTokens: number;
  completionTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  totalTokens: number;

  reqBodyBytes: number;
  reqHeaderBytes: number;
  respBodyBytes: number;
  respHeaderBytes: number;

  windowLabel: string;
  rates: RatesSnapshot | null;
  costNanoUsd: number;
  priced: boolean;

  usageStatus: string;
  clientDisconnected: boolean;
  error: string;
}

export interface RequestsPage {
  requests: RequestRecord[];
  nextBeforeId: number;
}

export interface Window {
  id: number;
  label: string;
  daysMask: number;
  startMinute: number;
  endMinute: number;
  inputPerMTok: number | null;
  outputPerMTok: number | null;
  cacheReadPerMTok: number | null;
  cacheWritePerMTok: number | null;
  reasoningPerMTok: number | null;
}

export interface Model {
  id: number;
  name: string;
  inputPerMTok: number;
  outputPerMTok: number;
  cacheReadPerMTok: number;
  cacheWritePerMTok: number;
  reasoningPerMTok: number | null;
  windows: Window[];
}

export interface ModelsResponse {
  models: Model[];
}

export interface ModelInput {
  name: string;
  inputPerMTok: number;
  outputPerMTok: number;
  cacheReadPerMTok: number;
  cacheWritePerMTok: number;
  reasoningPerMTok: number | null;
}

export type ModelPatch = Partial<ModelInput>;

export interface WindowInput {
  label: string;
  daysMask: number;
  startMinute: number;
  endMinute: number;
  inputPerMTok: number | null;
  outputPerMTok: number | null;
  cacheReadPerMTok: number | null;
  cacheWritePerMTok: number | null;
  reasoningPerMTok: number | null;
}

export interface MaskedSecret {
  set: boolean;
  last4: string;
}

export interface MaskedGatewayKey {
  set: boolean;
  prefix: string;
  last4: string;
}

export interface Settings {
  upstream: {
    baseUrl: string;
    timeoutS: number;
    apiKey: MaskedSecret;
  };
  timezone: string;
  retentionDays: number;
  gatewayKey: MaskedGatewayKey;
  adminPasswordSet: boolean;
  listen: string;
  dbPath: string;
  dbSizeBytes: number;
  version: string;
}

export interface SettingsPatch {
  upstream?: {
    baseUrl?: string;
    apiKey?: string;
    timeoutS?: number;
  };
  timezone?: string;
  retentionDays?: number;
  currentPassword?: string;
  newPassword?: string;
}

export interface GatewayKeyResponse {
  gatewayKey: string;
}

export interface SetupInput {
  upstream: {
    baseUrl: string;
    apiKey: string;
    timeoutS: number;
  };
  timezone: string;
  adminPassword: string;
}

export interface UpstreamTestInput {
  baseUrl: string;
  apiKey: string;
}

export interface UpstreamTestResult {
  ok: boolean;
  status: number;
  message?: string;
}

export interface LiveModelRate {
  model: string;
  qps: number;
  errors: number;
}

export interface LiveSparkPoint {
  offsetS: number;
  requests: number;
  upBytes: number;
  downBytes: number;
}

export interface InflightInfo {
  pathClass: string;
  model: string;
  elapsedMs: number;
}

export interface LiveSnapshot {
  ts: string;
  windowS: number;
  inflight: number;
  qps: number;
  errors: number;
  upBps: number;
  downBps: number;
  latencyP50Ms: number | null;
  latencyP95Ms: number | null;
  byModel: LiveModelRate[];
  spark: LiveSparkPoint[];
  inflightRequests: InflightInfo[];
}

export type ApiErrorCode =
  | "validation"
  | "unauthorized"
  | "forbidden"
  | "not_found"
  | "conflict"
  | "internal"
  | "network"
  | "unknown";

/** ApiError is the shape every failed API call exposes. */
export interface ApiError {
  code: ApiErrorCode;
  message: string;
  fields: Record<string, string>;
  status: number;
}
