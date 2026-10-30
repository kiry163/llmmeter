import type {
  AnalysisBody,
  ApiError as ApiErrorShape,
  ApiErrorCode,
  Bootstrap,
  Breakdown,
  BreakdownBy,
  GatewayKeyResponse,
  Health,
  ImportModelsResult,
  Interval,
  LiveSnapshot,
  Me,
  Model,
  ModelCatalog,
  ModelInput,
  ModelPatch,
  ModelsResponse,
  Overview,
  RequestRecord,
  RequestsPage,
  Series,
  Settings,
  SettingsPatch,
  SetupInput,
  UpstreamTestInput,
  UpstreamTestResult,
  WindowInput,
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
  // The upstream probe reports failures as {ok:false,message} without an
  // error envelope, so fall back to the top-level message.
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

export const api = {
  bootstrap: (): Promise<Bootstrap> => request<Bootstrap>("/bootstrap"),

  me: (): Promise<Me> => request<Me>("/me"),

  health: (): Promise<Health> => request<Health>("/health"),

  getOverview: (params: RangeParams = {}): Promise<Overview> =>
    request<Overview>(`/overview${query({ from: params.from, to: params.to })}`),

  getSeries: (params: SeriesParams = {}): Promise<Series> =>
    request<Series>(
      `/series${query({ from: params.from, to: params.to, interval: params.interval })}`,
    ),

  getBreakdown: (params: BreakdownParams): Promise<Breakdown> =>
    request<Breakdown>(
      `/breakdown${query({ from: params.from, to: params.to, by: params.by })}`,
    ),

  getAnalysis: (params: AnalysisParams = {}): Promise<AnalysisBody> =>
    request<AnalysisBody>(
      `/analysis${query({ from: params.from, to: params.to, interval: params.interval })}`,
    ),

  getRequests: (params: RequestParams = {}): Promise<RequestsPage> =>
    request<RequestsPage>(
      `/requests${query({
        from: params.from,
        to: params.to,
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

  getModelCatalog: (refresh = false): Promise<ModelCatalog> =>
    request<ModelCatalog>(
      `/model-catalog${query({ refresh: refresh ? 1 : undefined })}`,
    ),

  getModels: (): Promise<ModelsResponse> => request<ModelsResponse>("/models"),

  createModel: (input: ModelInput): Promise<Model> =>
    request<Model>("/models", { method: "POST", body: JSON.stringify(input) }),

  importModels: (names: string[]): Promise<ImportModelsResult> =>
    request<ImportModelsResult>("/models/import", {
      method: "POST",
      body: JSON.stringify({ names }),
    }),

  updateModel: (id: number, patch: ModelPatch): Promise<Model> =>
    request<Model>(`/models/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),

  deleteModel: (id: number): Promise<void> =>
    request<void>(`/models/${id}`, { method: "DELETE" }),

  putWindows: (id: number, windows: WindowInput[]): Promise<Model> =>
    request<Model>(`/models/${id}/windows`, {
      method: "PUT",
      body: JSON.stringify({ windows }),
    }),

  getSettings: (): Promise<Settings> => request<Settings>("/settings"),

  patchSettings: (patch: SettingsPatch): Promise<Settings> =>
    request<Settings>("/settings", { method: "PATCH", body: JSON.stringify(patch) }),

  rotateKey: (): Promise<GatewayKeyResponse> =>
    request<GatewayKeyResponse>("/gateway-key/rotate", { method: "POST" }),

  login: (password: string): Promise<{ authenticated: boolean }> =>
    request<{ authenticated: boolean }>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ password }),
    }),

  logout: (): Promise<{ authenticated: boolean }> =>
    request<{ authenticated: boolean }>("/auth/logout", { method: "POST" }),

  setup: (input: SetupInput): Promise<GatewayKeyResponse> =>
    request<GatewayKeyResponse>("/setup", { method: "POST", body: JSON.stringify(input) }),

  testUpstream: (input: UpstreamTestInput): Promise<UpstreamTestResult> =>
    request<UpstreamTestResult>("/upstream/test", {
      method: "POST",
      body: JSON.stringify(input),
    }),

  getLiveSnapshot: (windowS: 60 | 300 = 60): Promise<LiveSnapshot> =>
    request<LiveSnapshot>(`/live/snapshot${query({ window: windowS })}`),
};
