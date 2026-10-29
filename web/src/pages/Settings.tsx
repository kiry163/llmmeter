import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { Badge, Card, ErrorText, Spinner } from "../components/ui";
import { formatBytes } from "../lib/format";

export function Settings() {
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.getSettings });

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-xl font-semibold">设置</h1>
        <p className="text-sm text-muted">上游、时区、保留策略与访问控制，编辑表单由后续迭代补齐。</p>
      </header>

      {settings.isPending && <Spinner />}
      {settings.isError && <ErrorText error={settings.error} />}

      {settings.data && (
        <div className="grid gap-3 lg:grid-cols-2">
          <Card title="上游">
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between gap-4">
                <dt className="text-muted">Base URL</dt>
                <dd className="truncate">{settings.data.upstream.baseUrl || "—"}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">API Key</dt>
                <dd>
                  {settings.data.upstream.apiKey.set
                    ? `已设置 ····${settings.data.upstream.apiKey.last4}`
                    : "未设置"}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">超时</dt>
                <dd>{settings.data.upstream.timeoutS} 秒</dd>
              </div>
            </dl>
          </Card>

          <Card title="Gateway Key">
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between gap-4">
                <dt className="text-muted">状态</dt>
                <dd>
                  {settings.data.gatewayKey.set ? (
                    <Badge tone="success">
                      {settings.data.gatewayKey.prefix}····{settings.data.gatewayKey.last4}
                    </Badge>
                  ) : (
                    <Badge tone="warning">未生成</Badge>
                  )}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">管理密码</dt>
                <dd>{settings.data.adminPasswordSet ? "已设置" : "未设置"}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">时区</dt>
                <dd>{settings.data.timezone}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">明细保留</dt>
                <dd>{settings.data.retentionDays} 天</dd>
              </div>
            </dl>
          </Card>

          <Card title="运行信息">
            <dl className="space-y-2 text-sm">
              <div className="flex justify-between gap-4">
                <dt className="text-muted">监听地址</dt>
                <dd>{settings.data.listen}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">数据库</dt>
                <dd className="max-w-72 truncate" title={settings.data.dbPath}>
                  {settings.data.dbPath}
                </dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">数据库体积</dt>
                <dd>{formatBytes(settings.data.dbSizeBytes)}</dd>
              </div>
              <div className="flex justify-between gap-4">
                <dt className="text-muted">版本</dt>
                <dd>{settings.data.version}</dd>
              </div>
            </dl>
          </Card>

          <Card title="访问控制">编辑功能将在后续任务中接入。</Card>
        </div>
      )}
    </div>
  );
}
