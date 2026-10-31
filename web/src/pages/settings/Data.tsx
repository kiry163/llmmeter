import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../api/client";
import { Badge, Button, ErrorText, Spinner, useToast } from "../../components/ui";
import { formatBytesDecimal } from "../../lib/format";

/**
 * Data is the read-only runtime subpage: SQLite locations, sizes, schema and
 * process health. Retention is edited on the access subpage.
 */
export function Data() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const data = useQuery({ queryKey: ["data"], queryFn: api.getData });
  const health = useQuery({ queryKey: ["health"], queryFn: api.health });

  const reload = useMutation({
    mutationFn: api.reloadConfig,
    onSuccess: () => {
      void queryClient.invalidateQueries();
      toast("配置已重新加载", "success");
    },
    onError: (error) =>
      toast(error instanceof Error ? error.message : "重新加载失败", "error"),
  });

  return (
    <div className="settings-panel active">
      {data.isPending && <Spinner />}
      {data.isError && <ErrorText error={data.error} />}
      {data.data && (
        <>
          <div className="settings-grid">
            <article className="card settings-card">
              <div className="card-head">
                <div>
                  <div className="card-title">数据库</div>
                  <div className="card-sub">SQLite · 本机单文件</div>
                </div>
                <Badge tone={health.data?.dbWritable ? "success" : "warning"}>
                  {health.data?.dbWritable ? "可写" : "只读/异常"}
                </Badge>
              </div>
              <div className="settings-body">
                <dl className="kv">
                  <dt>路径</dt>
                  <dd className="mono" title={data.data.dbPath}>
                    {data.data.dbPath}
                  </dd>
                  <dt>主库体积</dt>
                  <dd>{formatBytesDecimal(data.data.dbBytes)}</dd>
                  <dt>WAL 体积</dt>
                  <dd>{formatBytesDecimal(data.data.walBytes)}</dd>
                  <dt>Schema</dt>
                  <dd>v{data.data.schemaVersion}</dd>
                  <dt>最早流量</dt>
                  <dd>{data.data.earliestDay || "—"}</dd>
                </dl>
              </div>
            </article>

            <article className="card settings-card">
              <div className="card-head">
                <div>
                  <div className="card-title">运行信息</div>
                  <div className="card-sub">当前进程</div>
                </div>
                <Badge tone={health.data?.ok ? "success" : "danger"}>
                  {health.data?.ok ? "正常" : "异常"}
                </Badge>
              </div>
              <div className="settings-body">
                <dl className="kv">
                  <dt>版本</dt>
                  <dd>{data.data.version}</dd>
                  <dt>有效监听</dt>
                  <dd className="mono">{data.data.effectiveListen}</dd>
                  <dt>服务端时区</dt>
                  <dd>{data.data.timezone}</dd>
                  <dt>明细保留</dt>
                  <dd>{data.data.retentionDays} 天</dd>
                  <dt>写入队列</dt>
                  <dd>{data.data.writeQueue}</dd>
                  <dt>配置文件</dt>
                  <dd className="mono" title={data.data.configPath}>
                    {data.data.configPath}
                  </dd>
                </dl>
                {health.data?.lastError && (
                  <p className="form-error" role="status">
                    {health.data.lastError}
                  </p>
                )}
                <div className="form-actions">
                  <Button
                    variant="secondary"
                    disabled={reload.isPending}
                    onClick={() => reload.mutate()}
                  >
                    {reload.isPending ? "重新加载中…" : "重新加载配置"}
                  </Button>
                </div>
              </div>
            </article>
          </div>
          <p className="settings-hint">
            备份时停止进程后复制主库即可；不停机请使用
            <code>sqlite3 {data.data.dbPath} ".backup backup.db"</code>。数据库路径与监听地址由配置文件决定，修改后需要重启进程。
          </p>
        </>
      )}
    </div>
  );
}
