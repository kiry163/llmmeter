import type {
  AnalysisBody,
  ApiError as ApiErrorShape,
  ApiErrorCode,
  Bootstrap,
  Breakdown,
  BreakdownBy,
  DataInfo,
  General,
  GeneralPatch,
  Health,
  Interval,
  KeyCreateInput,
  KeyCreated,
  KeyPatch,
  KeySecret,
  KeysResponse,
  LiveSnapshot,
  LocalKey,
  Me,
  ModelCatalog,
  Overview,
  PricingBody,
  PricingImportResult,
  PricingModel,
  ProbeResult,
  RequestRecord,
  RequestsPage,
  Series,
  Upstream,
  UpstreamInput,
  UpstreamPatch,
  UpstreamSecret,
  UpstreamTestResult,
  UpstreamsResponse,
} from "./types";

const API_BASE = "/api";

/** ApiError is thrown for every non-2xx response and for network failures. */
export class ApiError extends Error implements ApiErrorShape {
  readonly code: ApiErrorCode;
  readonly fields: Record<string, string>;
  readonly status: number;

  constructor(code: ApiErrorCode, message: string, status = 0, fields: Record<string, string> = {}) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.fields = fields;
    this.status = status;
  }
}

/** UnauthorizedError is thrown for protected calls made without a session. */
export class UnauthorizedError extends ApiError {
  constructor(message: string, fields: Record<string, string> = {}) {
    super("unauthorized", message, 401, fields);
    this.name = "UnauthorizedError";
  }
}

type QueryValue = string | number | boolean | null | undefined;

interface QueryParams {
  [key: string]: QueryValue;
}

function query(params: QueryParams = {}): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === "") continue;
    search.set(key, String(value));
  }
  const encoded = search.toString();
  return encoded ? `?${encoded}` : "";
}

interface ErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
    fields?: Record<string, string>;
  };
  message?: string;
}

function parseError(status: number, text: string): ApiError {
  let envelope: ErrorEnvelope | undefined;
  if (text) {
    try {
      envelope = JSON.parse(text) as ErrorEnvelope;
    } catch {
      envelope = undefined;
    }
  }
  const body = envelope?.error;
  const known = body?.code;
  const codes: ApiErrorCode[] = [
    "validation",
    "unauthorized",
    "forbidden",
    "not_found",
    "conflict",
    "internal",
  ];
  const code = codes.includes(known as ApiErrorCode) ? (known as ApiErrorCode) : "unknown";
  // The upstream probe reports failures as {ok:false,error} without an error
  // envelope, so fall back to the top-level message.
  const message =
    body?.message ?? envelope?.message ?? (text.trim() || `HTTP ${status}`);
  if (status === 401) {
    return new UnauthorizedError(message, body?.fields ?? {});
  }
  return new ApiError(code, message, status, body?.fields ?? {});
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("X-Requested-With", "llmmeter");
  if (init.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE}${path}`, {
      ...init,
      headers,
      credentials: "same-origin",
    });
  } catch (err) {
    const message = err instanceof Error ? err.message : "network error";
    throw new ApiError("network", message);
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const text = await response.text();
  let body: unknown;
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      body = undefined;
    }
  }

  if (!response.ok) {
    throw parseError(response.status, text);
  }
  return body as T;
}

export interface RangeParams {
  from?: string;
  to?: string;
  upstream?: string;
}

export interface SeriesParams extends RangeParams {
  interval?: Interval;
}

export interface BreakdownParams extends RangeParams {
  by: BreakdownBy;
}

export interface AnalysisParams extends RangeParams {
  interval?: Interval;
}

export interface RequestParams extends RangeParams {
  key?: string;
  model?: string;
  pathClass?: string;
  status?: number;
  statusClass?: "2xx" | "4xx" | "5xx";
  usageStatus?: string;
  priced?: boolean;
  stream?: boolean;
  beforeId?: number;
  limit?: number;
}

export interface ProbeInput {
  baseUrl: string;
  apiKey: string;
}

export const api = {
  bootstrap: (): Promise<Bootstrap> => request<Bootstrap>("/bootstrap"),

  me: (): Promise<Me> => request<Me>("/me"),

  health: (): Promise<Health> => request<Health>("/health"),

  getOverview: (params: RangeParams = {}): Promise<Overview> =>
    request<Overview>(
      `/overview${query({
        from: params.from,
        to: params.to,
        upstream: params.upstream,
      })}`,
    ),

  getSeries: (params: SeriesParams = {}): Promise<Series> =>
    request<Series>(
      `/series${query({
        from: params.from,
        to: params.to,
        interval: params.interval,
        upstream: params.upstream,
      })}`,
    ),

  getBreakdown: (params: BreakdownParams): Promise<Breakdown> =>
    request<Breakdown>(
      `/breakdown${query({
        from: params.from,
        to: params.to,
        by: params.by,
        upstream: params.upstream,
      })}`,
    ),

  getAnalysis: (params: AnalysisParams = {}): Promise<AnalysisBody> =>
    request<AnalysisBody>(
      `/analysis${query({
        from: params.from,
        to: params.to,
        interval: params.interval,
        upstream: params.upstream,
      })}`,
    ),

  getRequests: (params: RequestParams = {}): Promise<RequestsPage> =>
    request<RequestsPage>(
      `/requests${query({
        from: params.from,
        to: params.to,
        upstream: params.upstream,
        key: params.key,
        model: params.model,
        pathClass: params.pathClass,
        status: params.status,
        statusClass: params.statusClass,
        usageStatus: params.usageStatus,
        priced: params.priced,
        stream: params.stream,
        before_id: params.beforeId,
        limit: params.limit,
      })}`,
    ),

  getRequest: (id: number): Promise<RequestRecord> => request<RequestRecord>(`/requests/${id}`),

  getLiveSnapshot: (windowS: 60 | 300 = 60): Promise<LiveSnapshot> =>
    request<LiveSnapshot>(`/live/snapshot${query({ window: windowS })}`),

  getUpstreams: (): Promise<UpstreamsResponse> => request<UpstreamsResponse>("/upstreams"),

  createUpstream: (input: UpstreamInput): Promise<Upstream> =>
    request<Upstream>("/upstreams", { method: "POST", body: JSON.stringify(input) }),

  updateUpstream: (id: string, patch: UpstreamPatch): Promise<Upstream> =>
    request<Upstream>(`/upstreams/${id}`, {
      method: "PATCH",
      body: JSON.stringify(patch),
    }),

  deleteUpstream: (id: string): Promise<void> =>
    request<void>(`/upstreams/${id}`, { method: "DELETE" }),

  testUpstream: (id: string): Promise<UpstreamTestResult> =>
    request<UpstreamTestResult>(`/upstreams/${id}/test`, { method: "POST" }),

  probeUpstream: (input: ProbeInput): Promise<ProbeResult> =>
    request<ProbeResult>("/upstreams/probe", {
      method: "POST",
      body: JSON.stringify(input),
    }),

  revealUpstreamKey: (id: string): Promise<UpstreamSecret> =>
    request<UpstreamSecret>(`/upstreams/${id}/secret`),

  getPricing: (upstreamId: string): Promise<PricingBody> =>
    request<PricingBody>(`/upstreams/${upstreamId}/pricing`),

  putPricing: (upstreamId: string, models: PricingModel[]): Promise<PricingBody> =>
    request<PricingBody>(`/upstreams/${upstreamId}/pricing`, {
      method: "PUT",
      body: JSON.stringify({ models }),
    }),

  importPricing: (upstreamId: string, names: string[]): Promise<PricingImportResult> =>
    request<PricingImportResult>(`/upstreams/${upstreamId}/pricing/import`, {
      method: "POST",
      body: JSON.stringify({ names }),
    }),

  getCatalog: (upstreamId: string, refresh = false): Promise<ModelCatalog> =>
    request<ModelCatalog>(
      `/upstreams/${upstreamId}/catalog${query({ refresh: refresh ? 1 : undefined })}`,
    ),

  getKeys: (): Promise<KeysResponse> => request<KeysResponse>("/keys"),

  createKey: (input: KeyCreateInput): Promise<KeyCreated> =>
    request<KeyCreated>("/keys", { method: "POST", body: JSON.stringify(input) }),

  updateKey: (id: string, patch: KeyPatch): Promise<LocalKey> =>
    request<LocalKey>(`/keys/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),

  deleteKey: (id: string): Promise<void> =>
    request<void>(`/keys/${id}`, { method: "DELETE" }),

  rotateKey: (id: string): Promise<KeyCreated> =>
    request<KeyCreated>(`/keys/${id}/rotate`, { method: "POST" }),

  revealKey: (id: string): Promise<KeySecret> =>
    request<KeySecret>(`/keys/${id}/secret`),

  getGeneral: (): Promise<General> => request<General>("/general"),

  patchGeneral: (patch: GeneralPatch): Promise<General> =>
    request<General>("/general", { method: "PATCH", body: JSON.stringify(patch) }),

  getData: (): Promise<DataInfo> => request<DataInfo>("/data"),

  reloadConfig: (): Promise<{ reloaded: boolean }> =>
    request<{ reloaded: boolean }>("/config/reload", { method: "POST" }),

  login: (password: string): Promise<{ authenticated: boolean }> =>
    request<{ authenticated: boolean }>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ password }),
    }),

  logout: (): Promise<{ authenticated: boolean }> =>
    request<{ authenticated: boolean }>("/auth/logout", { method: "POST" }),

  setPassword: (current: string, newPassword: string): Promise<{ adminPasswordSet: boolean }> =>
    request<{ adminPasswordSet: boolean }>("/auth/password", {
      method: "POST",
      body: JSON.stringify({ current, newPassword }),
    }),
};
