import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { Badge, ErrorText, Spinner } from "../../components/ui";
import { formatBytesDecimal } from "../../lib/format";

/**
 * Data is the data subpage: read-only runtime facts about the SQLite store and
 * the running process. The retention window itself is changed on the gateway
 * subpage, which shares the same settings record.
 */
export function Data() {
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.getSettings });
  const health = useQuery({ queryKey: ["health"], queryFn: api.health });

  return (
    <div className="settings-panel active">
      {settings.isPending && <Spinner />}
      {settings.isError && <ErrorText error={settings.error} />}
      {settings.data && (
        <>
          <div className="settings-grid">
            <article className="card settings-card">
              <div className="card-head">
                <div>
                  <div className="card-title">数据库</div>
                  <div className="card-sub">SQLite · 本机</div>
                </div>
              </div>
              <div className="settings-body">
                <dl className="kv">
                  <dt>路径</dt>
                  <dd className="mono" title={settings.data.dbPath}>
                    {settings.data.dbPath}
                  </dd>
                  <dt>体积</dt>
                  <dd>{formatBytesDecimal(settings.data.dbSizeBytes)}</dd>
                </dl>
              </div>
            </article>

            <article className="card settings-card">
              <div className="card-head">
                <div>
                  <div className="card-title">运行信息</div>
                  <div className="card-sub">当前进程</div>
                </div>
              </div>
              <div className="settings-body">
                <dl className="kv">
                  <dt>版本</dt>
                  <dd>{settings.data.version}</dd>
                  <dt>有效监听</dt>
                  <dd className="mono">{settings.data.listen}</dd>
                  <dt>服务端时区</dt>
                  <dd>{settings.data.timezone}</dd>
                  <dt>明细保留</dt>
                  <dd>{settings.data.retentionDays} 天</dd>
                  {health.data && (
                    <>
                      <dt>服务状态</dt>
                      <dd>
                        <Badge tone={health.data.ok ? "success" : "danger"}>
                          {health.data.ok ? "正常" : "异常"}
                        </Badge>
                      </dd>
                      <dt>写入队列</dt>
                      <dd>{health.data.queueDepth}</dd>
                    </>
                  )}
                </dl>
                {health.data?.lastError && (
                  <p className="form-error" role="status">
                    {health.data.lastError}
                  </p>
                )}
              </div>
            </article>
          </div>
          <p className="settings-hint">
            数据库路径与监听地址由配置文件决定，修改后需要重启进程；明细保留天数在上游与访问页调整。
          </p>
        </>
      )}
    </div>
  );
}
