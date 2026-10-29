import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { Card, ErrorText, Spinner } from "../components/ui";
import { formatBytes, formatCost, formatDuration, formatTokens } from "../lib/format";

export function Overview() {
  const overview = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.getOverview(),
  });

  const stats = overview.data?.current;

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-xl font-semibold">总览</h1>
        <p className="text-sm text-muted">
          {overview.data
            ? `${overview.data.from} 至 ${overview.data.to}${overview.data.partial ? "（部分聚合早于明细保留期）" : ""}`
            : "最近七天用量概览"}
        </p>
      </header>

      {overview.isPending && <Spinner />}
      {overview.isError && <ErrorText error={overview.error} />}

      {stats && (
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-3 xl:grid-cols-6">
          <Card title="请求数">
            <div className="text-2xl font-semibold">{formatTokens(stats.requests)}</div>
            <div className="text-xs text-muted">错误 {formatTokens(stats.errors)}</div>
          </Card>
          <Card title="总 tokens">
            <div className="text-2xl font-semibold">{formatTokens(stats.totalTokens)}</div>
            <div className="text-xs text-muted">
              输入 {formatTokens(stats.promptTokens)} / 输出 {formatTokens(stats.completionTokens)}
            </div>
          </Card>
          <Card title="成本">
            <div className="text-2xl font-semibold">{formatCost(stats.costNanoUsd)}</div>
            <div className="text-xs text-muted">
              未定价 {formatTokens(stats.unpriced)} · 未计量 {formatTokens(stats.unmetered)}
            </div>
          </Card>
          <Card title="P95 延迟">
            <div className="text-2xl font-semibold">
              {stats.latencyP95Ms == null ? "—" : formatDuration(stats.latencyP95Ms)}
            </div>
            <div className="text-xs text-muted">
              平均 {stats.latencyAvgMs == null ? "—" : formatDuration(stats.latencyAvgMs)}
            </div>
          </Card>
          <Card title="上行流量">
            <div className="text-2xl font-semibold">{formatBytes(stats.upBytes)}</div>
            <div className="text-xs text-muted">请求体与请求头</div>
          </Card>
          <Card title="下行流量">
            <div className="text-2xl font-semibold">{formatBytes(stats.downBytes)}</div>
            <div className="text-xs text-muted">响应体与响应头</div>
          </Card>
        </div>
      )}
    </div>
  );
}
