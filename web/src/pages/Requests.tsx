import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { Filters } from "../components/Filters";
import { RequestDrawer } from "../components/RequestDrawer";
import { Badge, Button, Card, ErrorText, Spinner } from "../components/ui";
import { useTimezone } from "../hooks/useTimezone";
import {
  formatBytes,
  formatCost,
  formatDuration,
  formatTime,
  formatTokens,
} from "../lib/format";
import {
  REQUESTS_PAGE_LIMIT,
  USAGE_STATUS_LABELS,
  buildRequestsQuery,
  filtersToApiParams,
  parseRequestsFilters,
  serializeRequestsFilters,
} from "../lib/query";
import type { RequestsFilters, UsageStatusFilter } from "../lib/query";

function statusTone(status: number): "success" | "warning" | "danger" {
  if (status >= 500) return "danger";
  if (status >= 400) return "warning";
  return "success";
}

function UsageBadge({ status }: { status: string }) {
  const label = USAGE_STATUS_LABELS[status as UsageStatusFilter] ?? status;
  const tone =
    status === "ok"
      ? "success"
      : status === "no_usage"
        ? "neutral"
        : status === "not_applicable"
          ? "info"
          : status === "too_large"
            ? "warning"
            : "danger";
  return <Badge tone={tone}>{label}</Badge>;
}

/** Requests is the cursor-paginated request explorer with a detail drawer. */
export function Requests() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const timeZone = useTimezone();

  const filters = useMemo(
    () => parseRequestsFilters(searchParams, timeZone),
    [searchParams, timeZone],
  );
  const apiParams = useMemo(() => filtersToApiParams(filters), [filters]);
  const queryKey = buildRequestsQuery(apiParams);

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
    filters.priced !== "all";

  const updateFilters = (next: RequestsFilters) => {
    setSearchParams(serializeRequestsFilters(next), { replace: true });
  };

  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">请求</h1>
          <p className="text-sm text-muted">
            {filters.from} 至 {filters.to} · 点击行查看明细
          </p>
        </div>
        {rows.length > 0 && (
          <span className="text-xs text-muted">已加载 {formatTokens(rows.length)} 条</span>
        )}
      </header>

      <div className="rounded-lg border border-line bg-surface-raised p-3">
        <Filters
          filters={filters}
          models={modelOptions}
          onChange={updateFilters}
          onReset={() => setSearchParams(new URLSearchParams(), { replace: true })}
        />
      </div>

      <Card title="请求明细">
        {list.isPending && <Spinner />}
        {list.isError && <ErrorText error={list.error} />}

        {list.data && (
          <>
            <div className="overflow-x-auto">
              <table className="w-full min-w-[1080px] text-left text-sm">
                <thead className="text-xs text-muted">
                  <tr className="border-b border-line">
                    <th className="py-2 pr-3 font-medium">时间</th>
                    <th className="py-2 pr-3 font-medium">路径类</th>
                    <th className="py-2 pr-3 font-medium">模型</th>
                    <th className="py-2 pr-3 font-medium">状态</th>
                    <th className="py-2 pr-3 font-medium">延迟</th>
                    <th className="py-2 pr-3 font-medium">上行</th>
                    <th className="py-2 pr-3 font-medium">下行</th>
                    <th className="py-2 pr-3 font-medium">总 tokens</th>
                    <th className="py-2 pr-3 font-medium">成本</th>
                    <th className="py-2 pr-3 font-medium">用量</th>
                  </tr>
                </thead>
                <tbody>
                  {rows.map((record) => (
                    <tr
                      key={record.id}
                      tabIndex={0}
                      onClick={() => setSelectedId(record.id)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          setSelectedId(record.id);
                        }
                      }}
                      className="cursor-pointer border-b border-line/60 hover:bg-line/20 focus:bg-line/20 focus:outline-none"
                    >
                      <td className="py-2 pr-3 whitespace-nowrap">
                        {formatTime(record.startedAt, timeZone)}
                      </td>
                      <td className="py-2 pr-3">
                        <Badge tone="info">{record.pathClass}</Badge>
                      </td>
                      <td className="py-2 pr-3">{record.model || "—"}</td>
                      <td className="py-2 pr-3">
                        <Badge tone={statusTone(record.status)}>{record.status}</Badge>
                      </td>
                      <td className="py-2 pr-3 whitespace-nowrap">
                        {formatDuration(record.latencyMs)}
                      </td>
                      <td className="py-2 pr-3 whitespace-nowrap">
                        {formatBytes(record.reqBodyBytes + record.reqHeaderBytes)}
                      </td>
                      <td className="py-2 pr-3 whitespace-nowrap">
                        {formatBytes(record.respBodyBytes + record.respHeaderBytes)}
                      </td>
                      <td className="py-2 pr-3">{formatTokens(record.totalTokens)}</td>
                      <td className="py-2 pr-3 whitespace-nowrap">
                        {record.priced ? (
                          formatCost(record.costNanoUsd)
                        ) : (
                          <span className="text-muted">未定价</span>
                        )}
                      </td>
                      <td className="py-2 pr-3">
                        <UsageBadge status={record.usageStatus} />
                      </td>
                    </tr>
                  ))}
                  {rows.length === 0 && (
                    <tr>
                      <td colSpan={10} className="py-8 text-center text-muted">
                        {hasActiveFilters ? "没有匹配的请求，试试放宽筛选。" : "暂无请求记录"}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>

            {rows.length > 0 && (
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
            )}
          </>
        )}
      </Card>

      <RequestDrawer requestId={selectedId} onClose={() => setSelectedId(null)} />
    </div>
  );
}
