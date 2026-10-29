import { skipToken, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useMemo, useState, type ReactNode } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import type { RatesSnapshot, RequestRecord } from "../api/types";
import { Filters } from "../components/Filters";
import { Badge, Button, Card, Drawer, ErrorText, Spinner } from "../components/ui";
import { formatBytes, formatCost, formatDuration, formatTime, formatTokens } from "../lib/format";
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

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section>
      <h3 className="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">{title}</h3>
      <div className="divide-y divide-line/60 rounded-md border border-line px-3 py-1">
        {children}
      </div>
    </section>
  );
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-start justify-between gap-3 py-1.5">
      <span className="shrink-0 text-muted">{label}</span>
      <span className="break-all text-right">{value}</span>
    </div>
  );
}

function RatesView({ rates }: { rates: RatesSnapshot }) {
  const entries = Object.entries(rates);
  if (entries.length === 0) return <p className="text-xs text-muted">未定价</p>;
  return (
    <dl className="space-y-1 text-xs">
      {entries.map(([key, value]) => (
        <div key={key} className="flex items-start justify-between gap-3">
          <dt className="shrink-0 text-muted">{key}</dt>
          <dd className="break-all text-right">
            {typeof value === "object" && value !== null ? JSON.stringify(value) : String(value)}
          </dd>
        </div>
      ))}
    </dl>
  );
}

function byteCell(bytes: number): string {
  return `${formatBytes(bytes)} · ${formatTokens(bytes)} B`;
}

function RequestDetail({ record }: { record: RequestRecord }) {
  const upBytes = record.reqBodyBytes + record.reqHeaderBytes;
  const downBytes = record.respBodyBytes + record.respHeaderBytes;

  return (
    <div className="space-y-4 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <Badge tone={statusTone(record.status)}>{record.status}</Badge>
        <Badge tone="info">{record.pathClass}</Badge>
        <Badge tone={record.stream ? "info" : "neutral"}>
          {record.stream ? "流式" : "非流式"}
        </Badge>
        <Badge tone={record.priced ? "success" : "warning"}>
          {record.priced ? "已定价" : "未定价"}
        </Badge>
        {record.clientDisconnected && <Badge tone="warning">客户端断开</Badge>}
      </div>

      <Section title="基本信息">
        <Row label="请求 ID" value={`#${record.id}`} />
        <Row label="模型" value={record.model || "—"} />
        <Row label="上游模型" value={record.upstreamModel || "—"} />
        <Row label="路径" value={record.path || "—"} />
      </Section>

      <Section title="时间">
        <Row label="开始" value={formatTime(record.startedAt)} />
        <Row label="完成" value={formatTime(record.completedAt)} />
        <Row label="本地日期" value={record.localDay || "—"} />
        <Row label="总耗时" value={formatDuration(record.latencyMs)} />
        <Row
          label="首字节 TTFT"
          value={record.ttftMs === null ? "—" : formatDuration(record.ttftMs)}
        />
      </Section>

      <Section title="流量">
        <Row label="请求头" value={byteCell(record.reqHeaderBytes)} />
        <Row label="请求体" value={byteCell(record.reqBodyBytes)} />
        <Row label="响应头" value={byteCell(record.respHeaderBytes)} />
        <Row label="响应体" value={byteCell(record.respBodyBytes)} />
        <Row
          label="合计"
          value={`上行 ${formatBytes(upBytes)} · 下行 ${formatBytes(downBytes)}`}
        />
      </Section>

      <Section title="Tokens">
        <Row label="输入" value={formatTokens(record.promptTokens)} />
        <Row label="输出" value={formatTokens(record.completionTokens)} />
        <Row label="缓存读" value={formatTokens(record.cacheReadTokens)} />
        <Row label="缓存写" value={formatTokens(record.cacheWriteTokens)} />
        <Row label="推理" value={formatTokens(record.reasoningTokens)} />
        <Row label="总计" value={formatTokens(record.totalTokens)} />
      </Section>

      <Section title="计费">
        <Row label="成本" value={record.priced ? formatCost(record.costNanoUsd) : "未定价"} />
        <Row label="计费窗口" value={record.windowLabel || "—"} />
        <Row label="用量状态" value={<UsageBadge status={record.usageStatus} />} />
        <div className="py-2">
          <div className="mb-1 text-muted">价格快照</div>
          {record.rates ? <RatesView rates={record.rates} /> : (
            <p className="text-xs text-muted">未定价</p>
          )}
        </div>
      </Section>

      <Section title="状态">
        <Row label="客户端断开" value={record.clientDisconnected ? "是" : "否"} />
        <Row
          label="错误"
          value={record.error ? <span className="text-danger">{record.error}</span> : "—"}
        />
      </Section>
    </div>
  );
}

/** Requests is the cursor-paginated request explorer with a detail drawer. */
export function Requests() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedId, setSelectedId] = useState<number | null>(null);

  const filters = useMemo(() => parseRequestsFilters(searchParams), [searchParams]);
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

  const detail = useQuery({
    queryKey: ["request", selectedId],
    queryFn: selectedId === null ? skipToken : () => api.getRequest(selectedId),
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
                        {formatTime(record.startedAt)}
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

      <Drawer
        open={selectedId !== null}
        title={selectedId === null ? "请求详情" : `请求 #${selectedId}`}
        onClose={() => setSelectedId(null)}
      >
        {detail.isPending && <Spinner />}
        {detail.isError && <ErrorText error={detail.error} />}
        {detail.data && <RequestDetail record={detail.data} />}
      </Drawer>
    </div>
  );
}
