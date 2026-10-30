import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { ApiError, api } from "../../api/client";
import type { Settings as SettingsData } from "../../api/types";
import {
  Button,
  Card,
  ErrorText,
  Field,
  Input,
  Spinner,
  useToast,
} from "../../components/ui";
import { formatBytes } from "../../lib/format";
import { redactSettings, validateRetention, type FormErrors } from "../../lib/settings";

/**
 * Data is the data subpage: read-only runtime facts plus the detail retention
 * window, which shares its field with the gateway subpage.
 */
export function Data() {
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.getSettings });

  return (
    <div className="space-y-4">
      {settings.isPending && <Spinner />}
      {settings.isError && <ErrorText error={settings.error} />}
      {settings.data && <DataView settings={settings.data} />}
    </div>
  );
}

function DataView({ settings }: { settings: SettingsData }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const redacted = redactSettings(settings);
  const [retentionDays, setRetentionDays] = useState(String(settings.retentionDays));
  const [errors, setErrors] = useState<FormErrors>({});

  const save = useMutation({
    mutationFn: (days: number) => api.patchSettings({ retentionDays: days }),
    onSuccess: (data) => {
      queryClient.setQueryData(["settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      setRetentionDays(String(data.retentionDays));
      setErrors({});
      toast("明细保留天数已保存", "success");
    },
  });

  function submit() {
    const nextErrors: FormErrors = {};
    const retention = validateRetention(retentionDays);
    if (retention) nextErrors.retentionDays = retention;
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    save.mutate(Number(retentionDays));
  }

  const saveFields = save.error instanceof ApiError ? Object.entries(save.error.fields) : [];

  return (
    <div className="space-y-4">
      <Card title="运行信息">
        <dl className="grid gap-3 text-sm sm:grid-cols-2">
          <div>
            <dt className="text-xs text-muted">数据库路径</dt>
            <dd className="break-all" title={redacted.dbPath}>
              {redacted.dbPath}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted">数据库体积</dt>
            <dd>{formatBytes(redacted.dbSizeBytes)}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted">版本</dt>
            <dd>{redacted.version}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted">有效监听</dt>
            <dd className="break-all">{redacted.listen}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted">服务端时区</dt>
            <dd>{redacted.timezone}</dd>
          </div>
        </dl>
        <p className="mt-3 text-xs text-muted">
          监听地址由配置文件决定，修改后需重启服务。
        </p>
      </Card>

      <Card title="明细保留">
        <div className="max-w-xs">
          <Field label="明细保留（天）" hint="7–3650" error={errors.retentionDays}>
            <Input
              type="number"
              min={7}
              max={3650}
              value={retentionDays}
              onChange={(event) => setRetentionDays(event.target.value)}
            />
          </Field>
        </div>
        <p className="mt-3 text-xs text-muted">
          超出保留期的请求明细会被删除，与网关页共用同一设置；聚合统计不随明细清理，永久保留。
        </p>
        {save.isError && (
          <div className="mt-3 space-y-1">
            <ErrorText error={save.error} />
            {saveFields.length > 0 && (
              <ul className="space-y-0.5 text-xs text-danger">
                {saveFields.map(([field, message]) => (
                  <li key={field}>
                    {field === "retentionDays" ? "明细保留天数" : field}：{message}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
        <div className="mt-3 flex justify-end">
          <Button onClick={submit} disabled={save.isPending}>
            {save.isPending ? "保存中…" : "保存"}
          </Button>
        </div>
      </Card>
    </div>
  );
}
