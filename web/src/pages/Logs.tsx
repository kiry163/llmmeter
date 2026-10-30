import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useMemo, useState, type ReactElement } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { RequestRecord } from "../api/types";
import { DataTable } from "../components/DataTable";
import type { Column } from "../components/DataTable";
import { EmptyState } from "../components/EmptyState";
import { ErrorBanner } from "../components/ErrorBanner";
import { Filters } from "../components/Filters";
import { PageHeader } from "../components/PageHeader";
import { RequestDrawer } from "../components/RequestDrawer";
import { Skeleton } from "../components/Skeleton";
import { Badge, Button, Card } from "../components/ui";
import { useTimezone } from "../hooks/useTimezone";
import {
  formatCost,
  formatDuration,
  formatRate,
  formatTime,
  formatTokens,
} from "../lib/format";
import {
  REQUESTS_PAGE_LIMIT,
  buildRequestsQuery,
  filtersToApiParams,
  parseRequestsFilters,
  serializeRequestsFilters,
} from "../lib/query";
import type { RequestsFilters } from "../lib/query";
import { presetRange } from "../lib/range";
import { tokensPerSecond } from "../lib/speed";
import { TOO_LARGE_HINT, usageLabel, usageTone } from "../lib/usage";

const SKELETON_ROWS = [0, 1, 2, 3, 4, 5];

function statusTone(status: number): "success" | "warning" | "danger" {
  if (status >= 500) return "danger";
  if (status >= 400) return "warning";
  return "success";
}

/** UsageBadge explains the too_large outcome on hover. */
function UsageBadge({ status }: { status: string }): ReactElement {
  const hint = status === "too_large" ? TOO_LARGE_HINT : undefined;
  return (
    <span title={hint} className="inline-flex">
      <Badge tone={usageTone(status)}>{usageLabel(status)}</Badge>
    </span>
  );
}

function ModelCell({ record }: { record: RequestRecord }): ReactElement {
  const upstreamHint =
    record.upstreamModel && record.upstreamModel !== record.model
      ? `上游模型：${record.upstreamModel}`
      : undefined;
  return (
    <span className="inline-flex flex-wrap items-center gap-2" title={upstreamHint}>
      <span className="font-medium">{record.model || "—"}</span>
      <Badge tone={record.stream ? "info" : "neutral"}>
        {record.stream ? "流式" : "非流式"}
      </Badge>
    </span>
  );
}

function TokenCell({ record }: { record: RequestRecord }): ReactElement {
  return (
    <span className="block whitespace-nowrap">
      <span className="block tabular-nums">
        输入 {formatTokens(record.promptTokens)} / 输出 {formatTokens(record.completionTokens)}
      </span>
      <span className="block text-xs text-muted">
        缓存↓ {formatTokens(record.cacheReadTokens)}
      </span>
    </span>
  );
}

function LatencyCell({ record }: { record: RequestRecord }): ReactElement {
  return (
    <span className="block whitespace-nowrap">
      <span className="block tabular-nums">
        首字 {record.ttftMs === null ? "—" : formatDuration(record.ttftMs)}
      </span>
      <span className="block text-xs tabular-nums text-muted">
        总 {formatDuration(record.latencyMs)}
      </span>
    </span>
  );
}

function SpeedCell({ record }: { record: RequestRecord }): ReactElement {
  const rate = tokensPerSecond(
    record.completionTokens,
    record.latencyMs,
    record.ttftMs,
    record.stream,
  );
  return <span className="tabular-nums">{formatRate(rate)}</span>;
}

function CostCell({ record }: { record: RequestRecord }): ReactElement {
  return record.priced ? (
    <span className="tabular-nums">{formatCost(record.costNanoUsd)}</span>
  ) : (
    <span className="text-muted">未定价</span>
  );
}

function StatusCell({ record }: { record: RequestRecord }): ReactElement {
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5">
      <Badge tone={statusTone(record.status)}>{record.status}</Badge>
      <UsageBadge status={record.usageStatus} />
    </span>
  );
}

function timeLabel(record: RequestRecord, timeZone: string | undefined, today: string): string {
  const full = formatTime(record.startedAt, timeZone);
  return record.localDay === today ? full.slice(11) : full;
}

/** LogCard is the narrow-screen rendering of one log row. */
function LogCard({
  record,
  timeZone,
  today,
  onOpen,
}: {
  record: RequestRecord;
  timeZone: string | undefined;
  today: string;
  onOpen: (id: number) => void;
}): ReactElement {
  return (
    <article
      role="button"
      tabIndex={0}
      aria-label={`请求 #${record.id}`}
      onClick={() => onOpen(record.id)}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onOpen(record.id);
        }
      }}
      className="cursor-pointer rounded-lg border border-line bg-surface-raised p-3 text-sm hover:bg-line/20 focus:bg-line/20 focus:outline-none"
    >
      <div className="flex flex-wrap items-start justify-between gap-2">
        <ModelCell record={record} />
        <StatusCell record={record} />
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted">
        <span className="tabular-nums">{timeLabel(record, timeZone, today)}</span>
        <Badge tone="info">{record.pathClass}</Badge>
        <span>#{record.id}</span>
      </div>
      <dl className="mt-2 grid grid-cols-2 gap-x-3 gap-y-2 text-xs">
        <div>
          <dt className="text-muted">Token</dt>
          <dd>
            <TokenCell record={record} />
          </dd>
        </div>
        <div>
          <dt className="text-muted">费用</dt>
          <dd>
            <CostCell record={record} />
          </dd>
        </div>
        <div>
          <dt className="text-muted">耗时</dt>
          <dd>
            <LatencyCell record={record} />
          </dd>
        </div>
        <div>
          <dt className="text-muted">速度</dt>
          <dd>
            <SpeedCell record={record} />
          </dd>
        </div>
      </dl>
    </article>
  );
}

/** Logs is the cursor-paginated request explorer with URL-backed filters. */
export function Logs(): ReactElement {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const timeZone = useTimezone();

  const filters = useMemo(
    () => parseRequestsFilters(searchParams, timeZone),
    [searchParams, timeZone],
  );
  const apiParams = useMemo(() => filtersToApiParams(filters), [filters]);
  const queryKey = buildRequestsQuery({ ...apiParams, stream: filters.stream });
  const today = presetRange("today", new Date(), timeZone).to;

  const models = useQuery({ queryKey: ["models"], queryFn: api.getModels });

  const list = useInfiniteQuery({
    queryKey: ["requests", queryKey],
    queryFn: ({ pageParam }) =>
      api.getRequests({ ...apiParams, beforeId: pageParam, limit: REQUESTS_PAGE_LIMIT }),
    initialPageParam: undefined as number | undefined,
    getNextPageParam: (lastPage, _allPages, lastPageParam) => {
      const next = lastPage.nextBeforeId;
      if (lastPage.requests.length === 0 || next <= 0) return undefined;
      // Guard against a backend that echoes the cursor on an exhausted page.
      if (lastPageParam !== undefined && next >= lastPageParam) return undefined;
      return next;
    },
  });

  const rows = useMemo(
    () => list.data?.pages.flatMap((page) => page.requests) ?? [],
    [list.data],
  );

  const modelOptions = useMemo(() => {
    const names = new Set<string>();
    for (const model of models.data?.models ?? []) names.add(model.name);
    for (const record of rows) if (record.model) names.add(record.model);
    if (filters.model) names.add(filters.model);
    return [...names].sort((left, right) => left.localeCompare(right));
  }, [models.data, rows, filters.model]);

  const hasActiveFilters =
    filters.model !== "" ||
    filters.pathClass !== "" ||
    filters.status !== "" ||
    filters.usageStatus !== "" ||
    filters.stream !== "all" ||
    filters.priced !== "all";

  const columns = useMemo<Column<RequestRecord>[]>(
    () => [
      {
        key: "time",
        header: "时间",
        className: "whitespace-nowrap tabular-nums",
        render: (record) => timeLabel(record, timeZone, today),
      },
      {
        key: "model",
        header: "模型",
        render: (record) => <ModelCell record={record} />,
      },
      {
        key: "tokens",
        header: "Token",
        render: (record) => <TokenCell record={record} />,
      },
      {
        key: "cost",
        header: "费用",
        className: "text-right tabular-nums",
        render: (record) => <CostCell record={record} />,
      },
      {
        key: "latency",
        header: "耗时",
        render: (record) => <LatencyCell record={record} />,
      },
      {
        key: "speed",
        header: "速度",
        className: "text-right tabular-nums",
        render: (record) => <SpeedCell record={record} />,
      },
      {
        key: "status",
        header: "状态",
        render: (record) => <StatusCell record={record} />,
      },
    ],
    [timeZone, today],
  );

  const updateFilters = (next: RequestsFilters) => {
    // A new filter set is a new cursor: dropping the search string resets
    // before_id pagination (the list query key changes with it).
    setSearchParams(serializeRequestsFilters(next), { replace: true });
  };

  return (
    <div className="space-y-4">
      <PageHeader
        title="日志"
        description={`${filters.from} 至 ${filters.to} · 点击行查看明细`}
        actions={
          rows.length > 0 ? (
            <span className="text-xs text-muted">已加载 {formatTokens(rows.length)} 条</span>
          ) : undefined
        }
      />

      <div className="rounded-lg border border-line bg-surface-raised p-3">
        <Filters
          filters={filters}
          models={modelOptions}
          onChange={updateFilters}
          onReset={() => setSearchParams(new URLSearchParams(), { replace: true })}
        />
      </div>

      <Card>
        {list.isError && (
          <ErrorBanner error={list.error} onRetry={() => void list.refetch()} />
        )}

        {list.isPending && (
          <div className="space-y-2" role="status" aria-label="加载日志">
            {SKELETON_ROWS.map((index) => (
              <Skeleton key={index} className="h-12 w-full" />
            ))}
          </div>
        )}

        {list.data && rows.length === 0 && (
          <EmptyState
            title={hasActiveFilters ? "没有匹配的请求" : "暂无请求记录"}
            description={
              hasActiveFilters
                ? "当前筛选条件下没有日志，试试放宽或重置筛选。"
                : "所选时间段内还没有经过网关的请求。"
            }
            action={
              hasActiveFilters ? (
                <Button
                  variant="secondary"
                  onClick={() => setSearchParams(new URLSearchParams(), { replace: true })}
                >
                  重置筛选
                </Button>
              ) : undefined
            }
          />
        )}

        {rows.length > 0 && (
          <>
            <div className="hidden min-[769px]:block">
              <DataTable
                columns={columns}
                rows={rows}
                rowKey={(record) => String(record.id)}
                onRowClick={(record) => setSelectedId(record.id)}
                scrollClassName="max-h-[calc(100vh-22rem)] overflow-y-auto"
              />
            </div>

            <ul className="space-y-2 min-[769px]:hidden">
              {rows.map((record) => (
                <li key={record.id}>
                  <LogCard
                    record={record}
                    timeZone={timeZone}
                    today={today}
                    onOpen={setSelectedId}
                  />
                </li>
              ))}
            </ul>

            <div className="mt-3 flex flex-wrap items-center justify-end gap-3">
              {list.hasNextPage ? (
                <Button
                  variant="secondary"
                  disabled={list.isFetchingNextPage}
                  onClick={() => void list.fetchNextPage()}
                >
                  {list.isFetchingNextPage ? "加载中…" : "加载更多"}
                </Button>
              ) : (
                <span className="text-xs text-muted">已到末尾</span>
              )}
            </div>
          </>
        )}
      </Card>

      <RequestDrawer requestId={selectedId} onClose={() => setSelectedId(null)} />
    </div>
  );
}
