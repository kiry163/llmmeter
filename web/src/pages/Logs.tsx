import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useMemo, useState, type KeyboardEvent, type ReactElement } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { RequestParams } from "../api/client";
import type { RequestRecord } from "../api/types";
import { useChartTheme } from "../components/charts";
import { PageHeader, RangeSeg } from "../components/PageHeader";
import { RequestDrawer } from "../components/RequestDrawer";
import { Spinner } from "../components/ui";
import { useTimezone } from "../hooks/useTimezone";
import {
  formatCost,
  formatDuration,
  formatOptionalDuration,
  formatRate,
  formatTime,
  formatTokens,
} from "../lib/format";
import {
  PATH_CLASS_FILTERS,
  USAGE_STATUS_FILTERS,
  type PathClassFilter,
  type UsageStatusFilter,
} from "../lib/query";
import { presetRange } from "../lib/range";
import { tokensPerSecond } from "../lib/speed";
import { RANGE_OPTIONS, modelColor, statusTone, type UiRange } from "../lib/ui";
import { TOO_LARGE_HINT, usageLabel } from "../lib/usage";

const PAGE_SIZE = 12;

const STATUS_CLASSES = ["2xx", "4xx", "5xx"] as const;
type StatusClass = (typeof STATUS_CLASSES)[number];

type StreamFilter = "all" | "true" | "false";
type PricingFilter = "all" | "priced" | "unpriced";

interface LogsFilters {
  range: UiRange;
  upstream: string;
  key: string;
  model: string;
  pathClass: PathClassFilter | "";
  statusClass: StatusClass | "";
  usageStatus: UsageStatusFilter | "";
  stream: StreamFilter;
  pricing: PricingFilter;
}

const DEFAULT_FILTERS: LogsFilters = {
  range: "today",
  upstream: "",
  key: "",
  model: "",
  pathClass: "",
  statusClass: "",
  usageStatus: "",
  stream: "all",
  pricing: "all",
};

const USAGE_CLASS: Record<string, "ok" | "warn" | "err" | "muted"> = {
  ok: "ok",
  no_usage: "muted",
  parse_failed: "warn",
  too_large: "warn",
  not_applicable: "muted",
  error: "err",
};

function parseLogsFilters(params: URLSearchParams): LogsFilters {
  const rawRange = params.get("range");
  const range: UiRange = RANGE_OPTIONS.some((option) => option.value === rawRange)
    ? (rawRange as UiRange)
    : DEFAULT_FILTERS.range;

  const rawPath = params.get("path") ?? "";
  const rawStatus = params.get("status") ?? "";
  const rawUsage = params.get("usage") ?? "";
  const rawStream = params.get("stream") ?? "";
  const rawPricing = params.get("pricing") ?? "";

  return {
    range,
    upstream: (params.get("upstream") ?? "").trim(),
    key: (params.get("key") ?? "").trim(),
    model: (params.get("model") ?? "").trim(),
    pathClass: PATH_CLASS_FILTERS.includes(rawPath as PathClassFilter)
      ? (rawPath as PathClassFilter)
      : "",
    statusClass: STATUS_CLASSES.includes(rawStatus as StatusClass)
      ? (rawStatus as StatusClass)
      : "",
    usageStatus: USAGE_STATUS_FILTERS.includes(rawUsage as UsageStatusFilter)
      ? (rawUsage as UsageStatusFilter)
      : "",
    stream: rawStream === "true" || rawStream === "false" ? rawStream : "all",
    pricing: rawPricing === "priced" || rawPricing === "unpriced" ? rawPricing : "all",
  };
}

function serializeLogsFilters(filters: LogsFilters): URLSearchParams {
  const params = new URLSearchParams();
  if (filters.range !== DEFAULT_FILTERS.range) params.set("range", filters.range);
  if (filters.upstream) params.set("upstream", filters.upstream);
  if (filters.key) params.set("key", filters.key);
  if (filters.model) params.set("model", filters.model);
  if (filters.pathClass) params.set("path", filters.pathClass);
  if (filters.statusClass) params.set("status", filters.statusClass);
  if (filters.usageStatus) params.set("usage", filters.usageStatus);
  if (filters.stream !== "all") params.set("stream", filters.stream);
  if (filters.pricing !== "all") params.set("pricing", filters.pricing);
  return params;
}

function timeLabel(iso: string, timeZone: string | undefined): string {
  // The prototype shows MM-DD HH:mm:ss; formatTime starts with the year.
  return formatTime(iso, timeZone).slice(5);
}

function UsageBadge({ status }: { status: string }): ReactElement {
  return (
    <span
      className={`usage ${USAGE_CLASS[status] ?? "muted"}`}
      title={status === "too_large" ? TOO_LARGE_HINT : undefined}
    >
      {usageLabel(status)}
    </span>
  );
}

function StatusBadges({ record }: { record: RequestRecord }): ReactElement {
  return (
    <>
      <span className={`status ${statusTone(record.status)}`}>{record.status}</span>
      <UsageBadge status={record.usageStatus} />
    </>
  );
}

function ModelLabel({ record }: { record: RequestRecord }): ReactElement {
  const theme = useChartTheme();
  const upstreamHint =
    record.upstreamModel && record.upstreamModel !== record.model
      ? `上游模型：${record.upstreamModel}`
      : undefined;
  return (
    <div className="log-model" title={upstreamHint}>
      {record.model && (
        <span
          className="dot"
          style={{
            display: "inline-block",
            flex: "0 0 auto",
            width: 5,
            height: 5,
            borderRadius: "50%",
            background: modelColor(record.model, theme.palette),
          }}
        />
      )}
      <span>{record.model || "—"}</span>
      <span className={`stream-badge${record.stream ? " on" : ""}`}>
        {record.stream ? "流式" : "非流式"}
      </span>
      <span className="log-route">
        {record.upstreamName || "—"} · {record.keyName || "—"}
      </span>
    </div>
  );
}

function CostValue({ record }: { record: RequestRecord }): ReactElement {
  return record.priced ? (
    <span>{formatCost(record.costNanoUsd)}</span>
  ) : (
    <span className="unpriced">未定价</span>
  );
}

function TokenValue({ record }: { record: RequestRecord }): ReactElement {
  return (
    <div className="log-token">
      <b>
        {formatTokens(record.promptTokens)} / {formatTokens(record.completionTokens)}
      </b>
      <small>缓存 ↓ {formatTokens(record.cacheReadTokens)}</small>
    </div>
  );
}

function LatencyValue({ record }: { record: RequestRecord }): ReactElement {
  return (
    <div className="log-lat">
      <span>首字 {formatOptionalDuration(record.ttftMs)}</span>
      <small>总 {formatDuration(record.latencyMs)}</small>
    </div>
  );
}

function speedOf(record: RequestRecord): number | null {
  return tokensPerSecond(
    record.completionTokens,
    record.latencyMs,
    record.ttftMs,
    record.stream,
  );
}

function rowKeyHandler(onOpen: () => void) {
  return (event: KeyboardEvent<HTMLElement>) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onOpen();
    }
  };
}

function LogRow({
  record,
  timeZone,
  selected,
  onOpen,
}: {
  record: RequestRecord;
  timeZone: string | undefined;
  selected: boolean;
  onOpen: (id: number) => void;
}): ReactElement {
  return (
    <tr
      className={selected ? "selected" : undefined}
      tabIndex={0}
      onClick={() => onOpen(record.id)}
      onKeyDown={rowKeyHandler(() => onOpen(record.id))}
    >
      <td>{timeLabel(record.startedAt, timeZone)}</td>
      <td>
        <ModelLabel record={record} />
      </td>
      <td>
        <TokenValue record={record} />
      </td>
      <td className="num">
        <CostValue record={record} />
      </td>
      <td>
        <LatencyValue record={record} />
      </td>
      <td className="num">{formatRate(speedOf(record))}</td>
      <td>
        <StatusBadges record={record} />
      </td>
    </tr>
  );
}

function LogCard({
  record,
  timeZone,
  onOpen,
}: {
  record: RequestRecord;
  timeZone: string | undefined;
  onOpen: (id: number) => void;
}): ReactElement {
  const speed = speedOf(record);
  return (
    <div
      className="log-card-row"
      role="button"
      tabIndex={0}
      aria-label={`请求 #${record.id}`}
      onClick={() => onOpen(record.id)}
      onKeyDown={rowKeyHandler(() => onOpen(record.id))}
    >
      <div className="log-card-top">
        <ModelLabel record={record} />
        <CostValue record={record} />
      </div>
      <div className="log-card-meta">
        <span>{timeLabel(record.startedAt, timeZone)}</span>
        <span>
          <b>{formatTokens(record.promptTokens)}</b> /{" "}
          <b>{formatTokens(record.completionTokens)}</b> tok
        </span>
        <span>缓存 ↓ {formatTokens(record.cacheReadTokens)}</span>
        <span>
          首字 {formatOptionalDuration(record.ttftMs)} · 总{" "}
          {formatDuration(record.latencyMs)}
        </span>
        {speed !== null && <span>{formatRate(speed)}</span>}
        <span>
          <StatusBadges record={record} />
        </span>
      </div>
    </div>
  );
}

/** Logs is the cursor-paginated request explorer with URL-backed filters. */
export function Logs(): ReactElement {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const timeZone = useTimezone();

  const filters = useMemo(() => parseLogsFilters(searchParams), [searchParams]);

  const apiParams = useMemo<RequestParams>(() => {
    const bounds = presetRange(filters.range, new Date(), timeZone);
    return {
      from: bounds.from,
      to: bounds.to,
      upstream: filters.upstream || undefined,
      key: filters.key || undefined,
      model: filters.model || undefined,
      pathClass: filters.pathClass || undefined,
      statusClass: filters.statusClass || undefined,
      usageStatus: filters.usageStatus || undefined,
      stream: filters.stream === "all" ? undefined : filters.stream === "true",
      priced:
        filters.pricing === "all" ? undefined : filters.pricing === "priced",
    };
  }, [filters, timeZone]);

  const list = useInfiniteQuery({
    queryKey: ["requests", apiParams],
    queryFn: ({ pageParam }) =>
      api.getRequests({ ...apiParams, beforeId: pageParam, limit: PAGE_SIZE }),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (lastPage, _allPages, lastPageParam) => {
      const next = lastPage.nextBeforeId;
      if (lastPage.requests.length === 0 || lastPage.requests.length < PAGE_SIZE || next <= 0) {
        return undefined;
      }
      // Guards a backend that echoes the cursor on an exhausted page.
      if (lastPageParam !== undefined && next >= lastPageParam) return undefined;
      return next;
    },
  });

  const upstreams = useQuery({
    queryKey: ["upstreams"],
    queryFn: api.getUpstreams,
  });
  const keys = useQuery({
    queryKey: ["keys"],
    queryFn: api.getKeys,
  });
  const pricingSets = useQuery({
    queryKey: ["pricing-model-names", upstreams.data?.upstreams.map((u) => u.id).join(",")],
    enabled: (upstreams.data?.upstreams.length ?? 0) > 0,
    queryFn: async () => {
      const lists = await Promise.all(
        (upstreams.data?.upstreams ?? []).map(async (upstream) => {
          try {
            const body = await api.getPricing(upstream.id);
            return body.models.map((model) => model.name);
          } catch {
            return [];
          }
        }),
      );
      return lists.flat();
    },
  });

  const rows = useMemo(
    () => list.data?.pages.flatMap((page) => page.requests) ?? [],
    [list.data],
  );
  const summary = list.data?.pages[0]?.summary;

  const keyOptions = useMemo(
    () =>
      (keys.data?.keys ?? []).filter(
        (key) => !filters.upstream || key.upstreamId === filters.upstream,
      ),
    [keys.data, filters.upstream],
  );

  const modelOptions = useMemo(() => {
    const names = new Set<string>(pricingSets.data ?? []);
    for (const record of rows) {
      if (record.model) names.add(record.model);
    }
    if (filters.model) names.add(filters.model);
    return [...names].filter((name) => name !== "").sort((a, b) => a.localeCompare(b));
  }, [pricingSets.data, rows, filters.model]);

  const hasActiveFilters =
    filters.upstream !== "" ||
    filters.key !== "" ||
    filters.model !== "" ||
    filters.pathClass !== "" ||
    filters.statusClass !== "" ||
    filters.usageStatus !== "" ||
    filters.stream !== "all" ||
    filters.pricing !== "all";

  const updateFilter = (patch: Partial<LogsFilters>) => {
    const next = { ...filters, ...patch };
    setSearchParams(serializeLogsFilters(next), { replace: true });
  };

  const resetFilters = () => {
    setSearchParams(new URLSearchParams(), { replace: true });
  };

  const showEmpty =
    list.isSuccess && rows.length === 0 && !list.isFetchingNextPage;

  return (
    <div className="view active">
      <PageHeader
        eyebrow="logs"
        title="日志"
        subtitle="请求明细、筛选与排障"
        actions={
          <RangeSeg
            value={filters.range}
            options={RANGE_OPTIONS}
            onChange={(range) => updateFilter({ range })}
            ariaLabel="日志时间范围"
          />
        }
      />

      <section className="card log-filters" aria-label="筛选">
        <label className="filter">
          <span>上游</span>
          <select
            value={filters.upstream}
            onChange={(event) =>
              updateFilter({ upstream: event.target.value, key: "" })
            }
          >
            <option value="">全部</option>
            {upstreams.data?.upstreams.map((upstream) => (
              <option key={upstream.id} value={upstream.id}>
                {upstream.name}
              </option>
            ))}
          </select>
        </label>

        <label className="filter">
          <span>网关密钥</span>
          <select
            value={filters.key}
            onChange={(event) => {
              const keyId = event.target.value;
              const selectedKey = keyOptions.find((key) => key.id === keyId);
              updateFilter({
                key: keyId,
                upstream: selectedKey?.upstreamId ?? filters.upstream,
              });
            }}
          >
            <option value="">全部</option>
            {keyOptions.map((key) => (
              <option key={key.id} value={key.id}>
                {key.name}
              </option>
            ))}
          </select>
        </label>

        <label className="filter">
          <span>模型</span>
          <select
            value={filters.model}
            onChange={(event) => updateFilter({ model: event.target.value })}
          >
            <option value="">全部</option>
            {modelOptions.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </select>
        </label>

        <label className="filter">
          <span>路径类</span>
          <select
            value={filters.pathClass}
            onChange={(event) =>
              updateFilter({ pathClass: event.target.value as LogsFilters["pathClass"] })
            }
          >
            <option value="">全部</option>
            {PATH_CLASS_FILTERS.map((pathClass) => (
              <option key={pathClass} value={pathClass}>
                {pathClass}
              </option>
            ))}
          </select>
        </label>

        <label className="filter">
          <span>状态</span>
          <select
            value={filters.statusClass}
            onChange={(event) =>
              updateFilter({ statusClass: event.target.value as LogsFilters["statusClass"] })
            }
          >
            <option value="">全部</option>
            {STATUS_CLASSES.map((statusClass) => (
              <option key={statusClass} value={statusClass}>
                {statusClass}
              </option>
            ))}
          </select>
        </label>

        <label className="filter">
          <span>用量</span>
          <select
            value={filters.usageStatus}
            onChange={(event) =>
              updateFilter({ usageStatus: event.target.value as LogsFilters["usageStatus"] })
            }
          >
            <option value="">全部</option>
            {USAGE_STATUS_FILTERS.map((status) => (
              <option key={status} value={status}>
                {usageLabel(status)}
              </option>
            ))}
          </select>
        </label>

        <label className="filter">
          <span>流式</span>
          <select
            value={filters.stream}
            onChange={(event) =>
              updateFilter({ stream: event.target.value as StreamFilter })
            }
          >
            <option value="all">全部</option>
            <option value="true">流式</option>
            <option value="false">非流式</option>
          </select>
        </label>

        <label className="filter">
          <span>定价</span>
          <select
            value={filters.pricing}
            onChange={(event) =>
              updateFilter({ pricing: event.target.value as PricingFilter })
            }
          >
            <option value="all">全部</option>
            <option value="priced">已定价</option>
            <option value="unpriced">未定价</option>
          </select>
        </label>

        <button type="button" className="ghost-btn" onClick={resetFilters}>
          重置
        </button>
      </section>

      <div className="log-summary" aria-label="筛选结果摘要">
        <span className="summary-item">
          <span>请求</span>
          <b>{summary ? formatTokens(summary.requests) : "—"}</b>
        </span>
        <span className="summary-item">
          <span>花费</span>
          <b>{summary ? formatCost(summary.costNanoUsd) : "—"}</b>
        </span>
        <span className="summary-item">
          <span>Tokens</span>
          <b>{summary ? formatTokens(summary.totalTokens) : "—"}</b>
        </span>
        <span className="summary-item">
          <span>错误</span>
          <b>{summary ? formatTokens(summary.errors) : "—"}</b>
        </span>
      </div>

      <section className="card log-card">
        {list.isError && (
          <div className="error-banner">
            <span>
              {list.error instanceof Error ? list.error.message : "日志加载失败"}
            </span>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void list.refetch()}
            >
              重试
            </button>
          </div>
        )}

        {list.isPending && (
          <div className="log-empty">
            <Spinner label="加载日志…" />
          </div>
        )}

        {showEmpty && (
          <div className="log-empty">
            {hasActiveFilters ? "没有匹配的请求" : "暂无请求记录"}
          </div>
        )}

        {rows.length > 0 && (
          <>
            <div className="log-table-wrap">
              <table className="log-table">
                <thead>
                  <tr>
                    <th>时间</th>
                    <th>模型</th>
                    <th>Token</th>
                    <th className="num">费用</th>
                    <th>耗时</th>
                    <th className="num">速度</th>
                    <th>状态</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((record) => (
                    <LogRow
                      key={record.id}
                      record={record}
                      timeZone={timeZone}
                      selected={selectedId === record.id}
                      onOpen={setSelectedId}
                    />
                  ))}
                </tbody>
              </table>
            </div>

            <div className="log-cards">
              {rows.map((record) => (
                <LogCard
                  key={record.id}
                  record={record}
                  timeZone={timeZone}
                  onOpen={setSelectedId}
                />
              ))}
            </div>

            <div className="log-more">
              {list.hasNextPage && (
                <button
                  type="button"
                  className="ghost-btn"
                  disabled={list.isFetchingNextPage}
                  onClick={() => void list.fetchNextPage()}
                >
                  {list.isFetchingNextPage ? "加载中…" : "加载更多"}
                </button>
              )}
              <span>
                {summary
                  ? `已显示 ${formatTokens(rows.length)} / ${formatTokens(summary.requests)}`
                  : ""}
              </span>
            </div>
          </>
        )}
      </section>

      <RequestDrawer requestId={selectedId} onClose={() => setSelectedId(null)} />
    </div>
  );
}
