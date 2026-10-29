import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, api } from "../api/client";
import type { Settings as SettingsData, SettingsPatch } from "../api/types";
import {
  Badge,
  Button,
  Card,
  ErrorText,
  Input,
  Select,
  Spinner,
  useToast,
} from "../components/ui";
import { formatBytes } from "../lib/format";
import {
  listenIsLoopback,
  redactSettings,
  timezoneOptions,
  validateSettings,
  type FormErrors,
} from "../lib/settings";

function Field({
  label,
  hint,
  error,
  children,
}: {
  label: string;
  hint?: string;
  error?: string;
  children: ReactNode;
}) {
  return (
    <label className="block">
      <span className="mb-1 flex items-baseline justify-between gap-2 text-xs text-muted">
        <span>{label}</span>
        {hint && <span className="text-[11px]">{hint}</span>}
      </span>
      {children}
      {error && <span className="mt-1 block text-xs text-danger">{error}</span>}
    </label>
  );
}

export function Settings() {
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.getSettings });

  return (
    <div className="space-y-4">
      <header>
        <h1 className="text-xl font-semibold">设置</h1>
        <p className="text-sm text-muted">上游、时区、保留策略与访问控制。</p>
      </header>

      {settings.isPending && <Spinner />}
      {settings.isError && <ErrorText error={settings.error} />}
      {settings.data && <SettingsForm settings={settings.data} />}
    </div>
  );
}

function SettingsForm({ settings }: { settings: SettingsData }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const redacted = redactSettings(settings);

  const [baseUrl, setBaseUrl] = useState(settings.upstream.baseUrl);
  const [timeoutS, setTimeoutS] = useState(String(settings.upstream.timeoutS));
  const [timezone, setTimezone] = useState(settings.timezone);
  const [retentionDays, setRetentionDays] = useState(String(settings.retentionDays));
  const [apiKey, setApiKey] = useState("");
  const [replacingKey, setReplacingKey] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [errors, setErrors] = useState<FormErrors>({});
  const [testState, setTestState] = useState<{
    tone: "success" | "warning";
    text: string;
  } | null>(null);
  const [confirmRotate, setConfirmRotate] = useState(false);
  const [rotatedKey, setRotatedKey] = useState<string | null>(null);
  const [confirmClear, setConfirmClear] = useState(false);

  function resetForm(data: SettingsData) {
    setBaseUrl(data.upstream.baseUrl);
    setTimeoutS(String(data.upstream.timeoutS));
    setTimezone(data.timezone);
    setRetentionDays(String(data.retentionDays));
    setApiKey("");
    setReplacingKey(false);
    setCurrentPassword("");
    setNewPassword("");
    setConfirmPassword("");
    setConfirmClear(false);
    setErrors({});
  }

  const test = useMutation({
    mutationFn: () =>
      api.testUpstream({ baseUrl: baseUrl.trim(), apiKey: apiKey.trim() }),
    onSuccess: (result) => {
      setTestState(
        result.ok
          ? { tone: "success", text: "上游连接正常" }
          : {
              tone: "warning",
              text: `连接失败：${result.message ?? `HTTP ${result.status}`}`,
            },
      );
    },
    onError: (error) => {
      setTestState({
        tone: "warning",
        text: `连接失败：${error instanceof Error ? error.message : String(error)}`,
      });
    },
  });

  const save = useMutation({
    mutationFn: (patch: SettingsPatch) => api.patchSettings(patch),
    onSuccess: (data, patch) => {
      queryClient.setQueryData(["settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      if (patch.newPassword) {
        // Changing the password rotates the session epoch, so the old cookie
        // is no longer accepted; ask for the new password.
        queryClient.clear();
        toast("管理密码已更新，请重新登录", "success");
        navigate("/login", { replace: true });
        return;
      }
      resetForm(data);
      toast("设置已保存", "success");
    },
  });

  const clearPassword = useMutation({
    mutationFn: (current: string) =>
      api.patchSettings({ clearPassword: true, currentPassword: current }),
    onSuccess: (data) => {
      queryClient.setQueryData(["settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
      setConfirmClear(false);
      toast("管理密码已清除", "success");
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "清除密码失败", "error");
    },
  });

  const rotate = useMutation({
    mutationFn: api.rotateKey,
    onSuccess: (data) => {
      setRotatedKey(data.gatewayKey);
      setConfirmRotate(false);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      toast("Gateway key 已轮换", "success");
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "轮换失败", "error");
    },
  });

  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/login", { replace: true });
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "退出失败", "error");
    },
  });

  function submit() {
    const nextErrors = validateSettings({
      timeoutS,
      retentionDays,
      currentPassword,
      newPassword,
      confirmPassword,
      passwordSet: settings.adminPasswordSet,
    });
    if (baseUrl.trim() === "") nextErrors.baseUrl = "请填写上游 Base URL";
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    const upstream: NonNullable<SettingsPatch["upstream"]> = {
      baseUrl: baseUrl.trim(),
      timeoutS: Number(timeoutS),
    };
    if (apiKey.trim() !== "") upstream.apiKey = apiKey.trim();

    const patch: SettingsPatch = {
      upstream,
      timezone,
      retentionDays: Number(retentionDays),
    };
    if (newPassword !== "") {
      patch.newPassword = newPassword;
      if (currentPassword !== "") patch.currentPassword = currentPassword;
    }
    save.mutate(patch);
  }

  async function copyRotatedKey() {
    if (!rotatedKey) return;
    try {
      await navigator.clipboard.writeText(rotatedKey);
      toast("已复制 gateway key", "success");
    } catch {
      toast("复制失败，请手动选择并复制", "error");
    }
  }

  const saveFields = save.error instanceof ApiError ? Object.entries(save.error.fields) : [];
  const loopback = listenIsLoopback(settings.listen);
  const testClasses =
    testState?.tone === "success" ? "text-xs text-success" : "text-xs text-warning";

  return (
    <div className="space-y-4">
      <Card
        title="上游"
        actions={
          <Button
            variant="secondary"
            onClick={() => test.mutate()}
            disabled={test.isPending}
          >
            {test.isPending ? "测试中…" : "测试连接"}
          </Button>
        }
      >
        <div className="space-y-3">
          <Field
            label="Base URL"
            hint="不要带 /v1，客户端请求路径会原样拼接"
            error={errors.baseUrl}
          >
            <Input
              value={baseUrl}
              onChange={(event) => setBaseUrl(event.target.value)}
              placeholder="https://api.deepseek.com"
              autoComplete="off"
            />
          </Field>

          <Field label="API Key" hint="明文不会回显">
            {replacingKey ? (
              <div className="flex gap-2">
                <Input
                  type="password"
                  value={apiKey}
                  onChange={(event) => setApiKey(event.target.value)}
                  placeholder="输入新的 API Key"
                  autoComplete="off"
                  autoFocus
                />
                <Button
                  variant="ghost"
                  onClick={() => {
                    setReplacingKey(false);
                    setApiKey("");
                  }}
                >
                  取消
                </Button>
              </div>
            ) : (
              <div className="flex items-center gap-2">
                <span className="text-sm">{redacted.upstream.apiKey}</span>
                <Button variant="secondary" onClick={() => setReplacingKey(true)}>
                  替换
                </Button>
              </div>
            )}
            {replacingKey && (
              <span className="mt-1 block text-xs text-muted">
                留空保存表示保持当前 key 不变。
              </span>
            )}
          </Field>

          <Field label="超时（秒）" error={errors.timeoutS}>
            <Input
              type="number"
              min={1}
              max={3600}
              value={timeoutS}
              onChange={(event) => setTimeoutS(event.target.value)}
            />
          </Field>

          {testState && (
            <p className={testClasses} role="status">
              {testState.text}
            </p>
          )}
        </div>
      </Card>

      <Card title="时间与保留">
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="时区" hint="影响日期分桶与高峰价匹配">
            <Select
              className="w-full"
              value={timezone}
              onChange={(event) => setTimezone(event.target.value)}
            >
              {timezoneOptions(timezone).map((zone) => (
                <option key={zone} value={zone}>
                  {zone}
                </option>
              ))}
            </Select>
          </Field>
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
      </Card>

      <Card
        title="管理密码"
        actions={
          settings.adminPasswordSet ? (
            <Button
              variant="ghost"
              onClick={() => logout.mutate()}
              disabled={logout.isPending}
            >
              退出登录
            </Button>
          ) : (
            <Badge tone="warning">未设置</Badge>
          )
        }
      >
        <div className="space-y-3">
          {settings.adminPasswordSet && (
            <Field label="当前密码" error={errors.currentPassword}>
              <Input
                type="password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
                autoComplete="current-password"
              />
            </Field>
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <Field
              label="新密码"
              hint={settings.adminPasswordSet ? "留空则不修改" : "可选，至少 8 位"}
              error={errors.newPassword}
            >
              <Input
                type="password"
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
                autoComplete="new-password"
              />
            </Field>
            <Field label="确认新密码" error={errors.confirmPassword}>
              <Input
                type="password"
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
                autoComplete="new-password"
              />
            </Field>
          </div>
          <p className="text-xs text-muted">
            {settings.adminPasswordSet
              ? "修改密码后当前会话将失效，需要重新登录。"
              : "未设置密码时仅允许本机访问，设置后可远程登录管理界面。"}
          </p>
          {settings.adminPasswordSet && (
            <div className="border-t border-line pt-3">
              {loopback ? (
                confirmClear ? (
                  <div className="flex flex-wrap items-center gap-2 text-sm">
                    <span className="text-danger">
                      清除后本机将免登录访问，确认继续？
                    </span>
                    <Button
                      variant="danger"
                      disabled={clearPassword.isPending}
                      onClick={() => {
                        if (currentPassword === "") {
                          setErrors((prev) => ({
                            ...prev,
                            currentPassword: "清除密码需要当前密码",
                          }));
                          return;
                        }
                        clearPassword.mutate(currentPassword);
                      }}
                    >
                      {clearPassword.isPending ? "清除中…" : "确认清除"}
                    </Button>
                    <Button variant="ghost" onClick={() => setConfirmClear(false)}>
                      取消
                    </Button>
                  </div>
                ) : (
                  <div className="flex flex-wrap items-center gap-2">
                    <Button
                      variant="secondary"
                      onClick={() => setConfirmClear(true)}
                      disabled={clearPassword.isPending}
                    >
                      清除密码
                    </Button>
                    <span className="text-xs text-muted">
                      需要当前密码；清除后本机免登录，仅在回环监听时可用。
                    </span>
                  </div>
                )
              ) : (
                <p className="text-xs text-muted">
                  当前监听地址不是回环，清除密码后将无法远程登录且重启会拒绝启动，因此已禁用。
                </p>
              )}
            </div>
          )}
        </div>
      </Card>

      <Card title="Gateway Key">
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-2 text-sm">
            <span className="text-muted">当前</span>
            {settings.gatewayKey.set ? (
              <Badge tone="success">{redacted.gatewayKey}</Badge>
            ) : (
              <Badge tone="warning">未生成</Badge>
            )}
          </div>

          {rotatedKey ? (
            <div className="rounded-md border border-warning/40 bg-warning/10 p-3">
              <p className="mb-2 text-xs text-warning">
                新 gateway key 明文仅显示一次，请立即复制保存。
              </p>
              <code className="block break-all rounded bg-surface px-3 py-2 text-sm">
                {rotatedKey}
              </code>
              <div className="mt-2 flex flex-wrap gap-2">
                <Button onClick={() => void copyRotatedKey()}>复制</Button>
                <Button variant="ghost" onClick={() => setRotatedKey(null)}>
                  我已保存
                </Button>
              </div>
            </div>
          ) : confirmRotate ? (
            <div className="flex flex-wrap items-center gap-2 text-sm">
              <span className="text-danger">轮换后旧 key 立即失效，确认继续？</span>
              <Button
                variant="danger"
                onClick={() => rotate.mutate()}
                disabled={rotate.isPending}
              >
                {rotate.isPending ? "轮换中…" : "确认轮换"}
              </Button>
              <Button variant="ghost" onClick={() => setConfirmRotate(false)}>
                取消
              </Button>
            </div>
          ) : (
            <div className="flex flex-wrap items-center gap-2">
              <Button variant="secondary" onClick={() => setConfirmRotate(true)}>
                轮换
              </Button>
              <span className="text-xs text-muted">
                轮换后需更新客户端配置中的 api_key。
              </span>
            </div>
          )}
        </div>
      </Card>

      <Card title="运行信息">
        <dl className="grid gap-3 text-sm sm:grid-cols-2">
          <div>
            <dt className="text-xs text-muted">有效监听</dt>
            <dd>
              {redacted.listen}
              <span className="ml-2 text-xs text-warning">修改需重启服务</span>
            </dd>
          </div>
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
        </dl>
      </Card>

      {save.isError && (
        <div className="space-y-1">
          <ErrorText error={save.error} />
          {saveFields.length > 0 && (
            <ul className="space-y-0.5 text-xs text-danger">
              {saveFields.map(([field, message]) => (
                <li key={field}>
                  {field}：{message}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}

      <div className="flex justify-end">
        <Button onClick={submit} disabled={save.isPending}>
          {save.isPending ? "保存中…" : "保存设置"}
        </Button>
      </div>
    </div>
  );
}
