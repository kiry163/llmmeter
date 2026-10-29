import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { Badge, Card, ErrorText, Spinner } from "../components/ui";
import { formatBytes, formatCost, formatDuration, formatTime, formatTokens } from "../lib/format";

export function Requests() {
  const requests = useQuery({
    queryKey: ["requests", { limit: 50 }],
    queryFn: () => api.getRequests({ limit: 50 }),
  });

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-xl font-semibold">请求</h1>
        <p className="text-sm text-muted">最近的网关请求明细，筛选与详情抽屉由后续迭代补齐。</p>
      </header>

      <Card title="最近请求">
        {requests.isPending && <Spinner />}
        {requests.isError && <ErrorText error={requests.error} />}
        {requests.data && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[720px] text-left text-sm">
              <thead className="text-xs text-muted">
                <tr className="border-b border-line">
                  <th className="py-2 pr-3 font-medium">时间</th>
                  <th className="py-2 pr-3 font-medium">路径类</th>
                  <th className="py-2 pr-3 font-medium">模型</th>
                  <th className="py-2 pr-3 font-medium">状态</th>
                  <th className="py-2 pr-3 font-medium">耗时</th>
                  <th className="py-2 pr-3 font-medium">上下行</th>
                  <th className="py-2 pr-3 font-medium">Tokens</th>
                  <th className="py-2 pr-3 font-medium">成本</th>
                </tr>
              </thead>
              <tbody>
                {requests.data.requests.map((record) => (
                  <tr key={record.id} className="border-b border-line/60">
                    <td className="py-2 pr-3 whitespace-nowrap">
                      {formatTime(record.startedAt)}
                    </td>
                    <td className="py-2 pr-3">{record.pathClass}</td>
                    <td className="py-2 pr-3">{record.model || "—"}</td>
                    <td className="py-2 pr-3">
                      <Badge tone={record.status >= 400 ? "danger" : "success"}>
                        {record.status}
                      </Badge>
                    </td>
                    <td className="py-2 pr-3">{formatDuration(record.latencyMs)}</td>
                    <td className="py-2 pr-3 whitespace-nowrap">
                      {formatBytes(record.reqBodyBytes + record.reqHeaderBytes)} /{" "}
                      {formatBytes(record.respBodyBytes + record.respHeaderBytes)}
                    </td>
                    <td className="py-2 pr-3">{formatTokens(record.totalTokens)}</td>
                    <td className="py-2 pr-3 whitespace-nowrap">
                      {record.priced ? formatCost(record.costNanoUsd) : "未定价"}
                    </td>
                  </tr>
                ))}
                {requests.data.requests.length === 0 && (
                  <tr>
                    <td colSpan={8} className="py-6 text-center text-muted">
                      暂无请求记录
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </div>
  );
}
