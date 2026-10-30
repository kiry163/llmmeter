import { skipToken, useQuery } from "@tanstack/react-query";
import type { ReactElement, ReactNode } from "react";
import { api } from "../api/client";
import type { RatesSnapshot, RequestRecord } from "../api/types";
import { useTimezone } from "../hooks/useTimezone";
import {
  formatBytes,
  formatBytesWithExact,
  formatCost,
  formatDuration,
  formatRate,
  formatTime,
  formatTokens,
} from "../lib/format";
import { tokensPerSecond } from "../lib/speed";
import { TOO_LARGE_HINT, usageLabel, usageTone } from "../lib/usage";
import { Badge, Drawer, ErrorText, Spinner } from "./ui";

function statusTone(status: number): "success" | "warning" | "danger" {
  if (status >= 500) return "danger";
  if (status >= 400) return "warning";
  return "success";
}

const RATE_LABELS: Record<string, string> = {
  model: "模型",
  window: "窗口",
  input_per_mtok: "输入 / M",
  output_per_mtok: "输出 / M",
  cache_read_per_mtok: "缓存读 / M",
  cache_write_per_mtok: "缓存写 / M",
  reasoning_per_mtok: "推理 / M",
};

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="drawer-section">
      <h3>{title}</h3>
      <dl className="kv">{children}</dl>
    </section>
  );
}

function Row({ label, value, mono = false }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <>
      <dt>{label}</dt>
      <dd className={mono ? "mono" : ""}>{value}</dd>
    </>
  );
}

function RatesView({ rates }: { rates: RatesSnapshot }) {
  const entries = Object.entries(rates);
  if (entries.length === 0) return <dd className="muted">未定价</dd>;
  return (
    <>
      {entries.map(([key, value]) => (
        <Row
          key={key}
          label={RATE_LABELS[key] ?? key}
          value={typeof value === "number" ? `$${value.toFixed(6)}` : String(value)}
        />
      ))}
    </>
  );
}

function RequestBody({ record, timeZone }: { record: RequestRecord; timeZone?: string }) {
  const upBytes = record.reqBodyBytes + record.reqHeaderBytes;
  const downBytes = record.respBodyBytes + record.respHeaderBytes;
  const rate = tokensPerSecond(
    record.completionTokens,
    record.latencyMs,
    record.ttftMs,
    record.stream,
  );

  return (
    <>
      <div className="drawer-status">
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

      <Section title="概要">
        <Row label="请求 ID" value={`#${record.id}`} mono />
        <Row label="模型" value={record.model || "—"} />
        <Row label="上游模型" value={record.upstreamModel || "—"} mono />
        <Row label="路径" value={record.path || "—"} mono />
        <Row label="开始" value={formatTime(record.startedAt, timeZone)} />
        <Row label="总耗时" value={formatDuration(record.latencyMs)} />
        <Row
          label="首字 TTFT"
          value={record.ttftMs === null ? "—" : formatDuration(record.ttftMs)}
        />
      </Section>

      <Section title="Tokens">
        <Row label="输入" value={formatTokens(record.promptTokens)} />
        <Row label="输出" value={formatTokens(record.completionTokens)} />
        <Row label="缓存读" value={formatTokens(record.cacheReadTokens)} />
        <Row label="缓存写" value={formatTokens(record.cacheWriteTokens)} />
        <Row label="推理" value={formatTokens(record.reasoningTokens)} />
        <Row label="总计" value={formatTokens(record.totalTokens)} />
        <Row label="速度" value={formatRate(rate)} />
      </Section>

      <Section title="流量">
        <Row label="请求头（估算）" value={formatBytesWithExact(record.reqHeaderBytes)} />
        <Row label="请求体" value={formatBytesWithExact(record.reqBodyBytes)} />
        <Row label="响应头（估算）" value={formatBytesWithExact(record.respHeaderBytes)} />
        <Row label="响应体" value={formatBytesWithExact(record.respBodyBytes)} />
        <Row
          label="合计"
          value={`上行 ${formatBytes(upBytes)} · 下行 ${formatBytes(downBytes)}`}
        />
      </Section>

      <Section title="计费">
        <Row label="成本" value={record.priced ? formatCost(record.costNanoUsd) : "未定价"} />
        <Row label="窗口" value={record.windowLabel || "—"} />
        {record.rates ? <RatesView rates={record.rates} /> : <Row label="价格快照" value="未定价" />}
      </Section>

      <Section title="状态">
        <Row
          label="HTTP 状态"
          value={<Badge tone={statusTone(record.status)}>{record.status}</Badge>}
        />
        <Row
          label="用量状态"
          value={
            <span title={record.usageStatus === "too_large" ? TOO_LARGE_HINT : undefined}>
              <Badge tone={usageTone(record.usageStatus)}>{usageLabel(record.usageStatus)}</Badge>
            </span>
          }
        />
        {record.usageStatus === "too_large" && (
          <Row label="说明" value={TOO_LARGE_HINT} />
        )}
        <Row label="客户端断开" value={record.clientDisconnected ? "是" : "否"} />
        <Row
          label="错误"
          value={record.error ? <span className="danger-text">{record.error}</span> : "—"}
        />
      </Section>
    </>
  );
}

export function RequestDrawer({
  requestId,
  onClose,
}: {
  requestId: number | null;
  onClose: () => void;
}): ReactElement {
  const timeZone = useTimezone();
  const detail = useQuery({
    queryKey: ["request", requestId],
    queryFn: requestId === null ? skipToken : () => api.getRequest(requestId),
  });

  return (
    <Drawer
      open={requestId !== null}
      title={requestId === null ? "请求详情" : `请求 #${requestId}`}
      onClose={onClose}
    >
      {requestId !== null && detail.isPending && <Spinner label="加载请求…" />}
      {detail.isError && <ErrorText error={detail.error} />}
      {detail.data && <RequestBody record={detail.data} timeZone={timeZone} />}
    </Drawer>
  );
}
