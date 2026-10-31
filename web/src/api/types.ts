// Wire types mirroring the JSON contracts of internal/api.

export interface Bootstrap {
  version: string;
  authRequired: boolean;
  listen: string;
  timezone: string;
  configPath: string;
  dbPath: string;
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

/** Requested interval; responses always report a resolved minute/hour/day. */
export type Interval = "auto" | "minute" | "hour" | "day";
export type ResolvedInterval = Exclude<Interval, "auto">;

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
  upstreamId: string;
  upstreamName: string;
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

/** Rate snapshot stored with a request; shape is upstream defined. */
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
  upstreamId: string;
  upstreamName: string;
  keyId: string;
  keyName: string;
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

export interface Upstream {
  id: string;
  name: string;
  baseUrl: string;
  apiKeyMasked: string;
  enabled: boolean;
  lastUsedAt: string | null;
  modelCount: number;
  keyCount: number;
}

export interface UpstreamsResponse {
  upstreams: Upstream[];
}

export interface UpstreamInput {
  name: string;
  baseUrl: string;
  apiKey?: string;
  enabled?: boolean;
}

export type UpstreamPatch = Partial<UpstreamInput>;

export interface UpstreamSecret {
  apiKey: string;
}

export interface ProbeModel {
  id: string;
  ownedBy?: string;
  matched: boolean;
}

export interface Preset {
  model: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  reasoning: number;
}

export interface ProbeResult {
  ok: boolean;
  latencyMs: number;
  provider: string;
  presetSnapshot: string;
  models: ProbeModel[];
  presets: Preset[];
  error: string;
}

export interface UpstreamTestResult {
  ok: boolean;
  latencyMs: number;
  modelCount: number;
  error: string;
}

export interface LocalKey {
  id: string;
  name: string;
  keyMasked: string;
  enabled: boolean;
  upstreamId: string;
  upstreamName: string;
  lastUsedAt: string | null;
}

export interface KeysResponse {
  keys: LocalKey[];
}

export interface KeyCreated {
  id: string;
  name: string;
  key: string;
  keyMasked: string;
  upstreamId: string;
  upstreamName: string;
}

export interface KeySecret {
  key: string;
}

export interface KeyCreateInput {
  name: string;
  upstreamId: string;
}

export interface KeyPatch {
  name?: string;
  upstreamId?: string;
  enabled?: boolean;
}

export interface PricingWindow {
  label: string;
  days: string[];
  start: string;
  end: string;
  input: number | null;
  output: number | null;
  cacheRead: number | null;
  cacheWrite: number | null;
  reasoning: number | null;
}

export interface PricingModel {
  name: string;
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  reasoning: number | null;
  windows: PricingWindow[];
}

export interface PricingBody {
  models: PricingModel[];
}

export interface PricingImportResult {
  created: string[];
  skipped: string[];
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

export interface ModelCatalog {
  fetchedAt: string | null;
  provider: string;
  presetSnapshot: string;
  configured: string[];
  upstream: CatalogModel[];
  observed: CatalogObserved[];
  presets: Preset[];
  upstreamError: string;
}

export interface General {
  timezone: string;
  retentionDays: number;
}

export interface GeneralPatch {
  timezone?: string;
  retentionDays?: number;
}

export interface DataInfo {
  configPath: string;
  dbPath: string;
  dbBytes: number;
  walBytes: number;
  schemaVersion: number;
  version: string;
  retentionDays: number;
  timezone: string;
  effectiveListen: string;
  writeQueue: number;
  earliestDay: string;
}

export interface LiveUpstreamSnapshot {
  requests: number;
  inflight: number;
  qps: number;
  errors: number;
  upBps: number;
  downBps: number;
  latencyP50Ms: number | null;
  latencyP95Ms: number | null;
  promptTokens: number;
  cacheReadTokens: number;
  cacheHitRate: number | null;
  spark: LiveSparkPoint[];
}

export interface LiveModelRate {
  upstreamId: string;
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
  upstreamId: string;
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
  promptTokens: number;
  cacheReadTokens: number;
  cacheHitRate: number | null;
  byModel: LiveModelRate[];
  byUpstream: Record<string, LiveUpstreamSnapshot>;
  spark: LiveSparkPoint[];
  inflightRequests: InflightInfo[];
}

export interface AnalysisTrendSeries {
  upstreamId: string;
  upstreamName: string;
  model: string;
  requests: number;
  promptTokens: number;
  completionTokens: number;
  cacheReadTokens: number;
  costNanoUsd: number;
  upBytes: number;
  downBytes: number;
}

export interface AnalysisTrendBucket {
  day: string;
  minuteOfDay: number;
  series: AnalysisTrendSeries[];
}

export interface AnalysisRow {
  upstreamId: string;
  upstreamName: string;
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

export interface AnalysisTopRequest {
  id: number;
  startedAt: string;
  upstreamId: string;
  upstreamName: string;
  keyId: string;
  keyName: string;
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
    interval: ResolvedInterval;
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
