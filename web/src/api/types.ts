// Wire types mirroring the JSON contracts of internal/api (spec §6).

export interface Bootstrap {
  version: string;
  authRequired: boolean;
  listen: string;
  timezone: string;
  earliestDay: string;
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
  lastError?: string;
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
  latencyMaxMs: number | null;
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
  cacheReadTokens: number;
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

export interface RequestsSummary {
  requests: number;
  costNanoUsd: number;
  totalTokens: number;
  errors: number;
}

export interface RequestsPage {
  requests: RequestRecord[];
  nextBeforeId: number;
  summary: RequestsSummary;
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
  };
  timezone?: string;
  retentionDays?: number;
  currentPassword?: string;
  newPassword?: string;
  clearPassword?: boolean;
}

export interface GatewayKeyResponse {
  gatewayKey: string;
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

export interface CatalogModel {
  id: string;
  ownedBy?: string;
}

export interface CatalogObserved {
  model: string;
  requests: number;
  lastSeen: string;
}

/** ModelCatalog combines the upstream listing with locally observed models. */
export interface ModelCatalog {
  fetchedAt: string | null;
  upstream: CatalogModel[];
  observed: CatalogObserved[];
  upstreamError: string;
}

export interface ImportedModel {
  id: number;
  name: string;
}

/** ImportModelsResult reports the models created and the names skipped. */
export interface ImportModelsResult {
  created: ImportedModel[];
  skipped: string[];
}

export interface TrendModel {
  requests: number;
  promptTokens: number;
  completionTokens: number;
  costNanoUsd: number;
  upBytes: number;
  downBytes: number;
}

export interface AnalysisRow {
  key: string;
  requests: number;
  errors: number;
  upBytes: number;
  downBytes: number;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens: number;
  costNanoUsd: number;
  unpriced: number;
}

export interface AnalysisTrendBucket {
  day: string;
  minuteOfDay: number;
  byModel: Record<string, TrendModel>;
}

export interface AnalysisTopRequest {
  id: number;
  startedAt: string;
  model: string;
  pathClass: string;
  stream: boolean;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens: number;
  totalTokens: number;
  costNanoUsd: number;
  latencyMs: number;
  ttftMs: number | null;
  status: number;
  usageStatus: string;
}

export interface AnalysisSlow {
  thresholdMs: number;
  p95Ms: number | null;
  countOver: number;
}

export interface AnalysisHealth {
  requests: number;
  errors: number;
  errorRate: number;
  unmetered: number;
  tooLarge: number;
  clientDisconnected: number;
  slow: AnalysisSlow;
}

export interface AnalysisCache {
  promptTokens: number;
  cacheReadTokens: number;
  hitRate: number;
  savedNanoUsd: number | null;
  available: boolean;
}

export interface AnalysisBody {
  range: {
    from: string;
    to: string;
    interval: Interval;
    partial: boolean;
  };
  trend: AnalysisTrendBucket[];
  models: AnalysisRow[];
  paths: AnalysisRow[];
  topRequests: {
    cost: AnalysisTopRequest[];
    tokens: AnalysisTopRequest[];
    latency: AnalysisTopRequest[];
  };
  health: AnalysisHealth;
  cache: AnalysisCache;
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
