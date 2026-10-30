import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, api } from "../../api/client";
import type { Settings as SettingsData, SettingsPatch } from "../../api/types";
import { Badge, Button, ErrorText, Spinner, useToast } from "../../components/ui";
import {
  listenIsLoopback,
  redactSettings,
  timezoneOptions,
  validateClearPassword,
  validateRetention,
  validateSettings,
  type FormErrors,
} from "../../lib/settings";

/** FIELD_LABELS turns API validation keys into the visible field names. */
const FIELD_LABELS: Record<string, string> = {
  baseUrl: "上游 Base URL",
  apiKey: "上游 API Key",
  timezone: "时区",
  retentionDays: "明细保留天数",
  currentPassword: "当前密码",
  newPassword: "新密码",
  clearPassword: "清除密码",
};

type PasswordMode = "change" | "clear" | null;

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

/**
 * Gateway is the upstream and access settings subpage. The upstream card saves
 * the OpenAI-compatible client settings; the access card owns the gateway key,
 * timezone, admin password and detail retention.
 */
export function Gateway() {
  const settings = useQuery({ queryKey: ["settings"], queryFn: api.getSettings });

  return (
    <div className="settings-panel active">
      {settings.isPending && <Spinner />}
      {settings.isError && <ErrorText error={settings.error} />}
      {settings.data && <GatewayForm settings={settings.data} />}
    </div>
  );
}

/** FieldErrors renders the per-field messages of a rejected API request. */
function FieldErrors({ error }: { error: unknown }) {
  if (!(error instanceof ApiError)) return null;
  const entries = Object.entries(error.fields);
  if (entries.length === 0) return null;
  return (
    <div role="alert">
      {entries.map(([field, message]) => (
        <small key={field} className="form-error">
          {FIELD_LABELS[field] ?? field}：{message}
        </small>
      ))}
    </div>
  );
}

function GatewayForm({ settings }: { settings: SettingsData }) {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const toast = useToast();
  const redacted = redactSettings(settings);
  const loopback = listenIsLoopback(settings.listen);

  const [baseUrl, setBaseUrl] = useState(settings.upstream.baseUrl);
  const [apiKey, setApiKey] = useState("");
  const [timezone, setTimezone] = useState(settings.timezone);
  const [retentionDays, setRetentionDays] = useState(String(settings.retentionDays));
  const [passwordMode, setPasswordMode] = useState<PasswordMode>(null);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [errors, setErrors] = useState<FormErrors>({});
  const [testState, setTestState] = useState<{ ok: boolean; message: string } | null>(null);
  const [confirmRotate, setConfirmRotate] = useState(false);
  const [rotatedKey, setRotatedKey] = useState<string | null>(null);

  function cancelPasswordEdit() {
    setPasswordMode(null);
    setCurrentPassword("");
    setNewPassword("");
    setConfirmPassword("");
    setErrors((prev) => {
      const next = { ...prev };
      delete next.currentPassword;
      delete next.newPassword;
      delete next.confirmPassword;
      return next;
    });
  }

  const test = useMutation({
    mutationFn: () => api.testUpstream({ baseUrl: baseUrl.trim(), apiKey: apiKey.trim() }),
    onSuccess: (result) => {
      setTestState({
        ok: result.ok,
        message: result.ok
          ? "上游连接正常"
          : `连接失败：${result.message ?? `HTTP ${result.status}`}`,
      });
    },
    onError: (error) => {
      setTestState({
        ok: false,
        message: `连接失败：${error instanceof Error ? error.message : String(error)}`,
      });
    },
  });

  const saveUpstream = useMutation({
    mutationFn: (patch: SettingsPatch) => api.patchSettings(patch),
    onSuccess: (data) => {
      queryClient.setQueryData(["settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      setBaseUrl(data.upstream.baseUrl);
      setApiKey("");
      setErrors({});
      toast("上游设置已保存", "success");
    },
    onError: (error) => toast(errorMessage(error, "保存上游设置失败"), "error"),
  });

  const saveAccess = useMutation({
    mutationFn: (patch: SettingsPatch) => api.patchSettings(patch),
    onSuccess: (data, patch) => {
      queryClient.setQueryData(["settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      if (patch.newPassword) {
        // Changing the password rotates the session epoch, so the old cookie
        // is no longer accepted; ask for the new password.
        queryClient.clear();
        toast("管理密码已更新，请重新登录", "success");
        navigate("/login", { replace: true });
        return;
      }
      setTimezone(data.timezone);
      setRetentionDays(String(data.retentionDays));
      cancelPasswordEdit();
      setErrors({});
      toast("网关设置已保存", "success");
    },
    onError: (error) => toast(errorMessage(error, "保存网关设置失败"), "error"),
  });

  const clearPassword = useMutation({
    mutationFn: (current: string) =>
      api.patchSettings({ clearPassword: true, currentPassword: current }),
    onSuccess: (data) => {
      queryClient.setQueryData(["settings"], data);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      cancelPasswordEdit();
      toast("管理密码已清除", "success");
    },
    onError: (error) => toast(errorMessage(error, "清除密码失败"), "error"),
  });

  const rotate = useMutation({
    mutationFn: api.rotateKey,
    onSuccess: (data) => {
      setRotatedKey(data.gatewayKey);
      setConfirmRotate(false);
      void queryClient.invalidateQueries({ queryKey: ["settings"] });
      void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      void queryClient.invalidateQueries({ queryKey: ["me"] });
      toast("Gateway key 已轮换", "success");
    },
    onError: (error) => toast(errorMessage(error, "轮换失败"), "error"),
  });

  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      queryClient.clear();
      navigate("/login", { replace: true });
    },
    onError: (error) => toast(errorMessage(error, "退出失败"), "error"),
  });

  function submitUpstream() {
    const nextErrors: FormErrors = {};
    if (baseUrl.trim() === "") nextErrors.baseUrl = "请填写上游 Base URL";
    setErrors((prev) => ({ ...prev, ...nextErrors }));
    if (Object.keys(nextErrors).length > 0) return;

    const upstream: NonNullable<SettingsPatch["upstream"]> = {
      baseUrl: baseUrl.trim(),
    };
    if (apiKey.trim() !== "") upstream.apiKey = apiKey.trim();
    saveUpstream.mutate({ upstream });
  }

  function submitAccess() {
    const nextErrors: FormErrors = {};
    const retention = validateRetention(retentionDays);
    if (retention) nextErrors.retentionDays = retention;

    if (passwordMode === "change") {
      const passwordErrors = validateSettings({
        retentionDays,
        currentPassword,
        newPassword,
        confirmPassword,
        passwordSet: settings.adminPasswordSet,
      });
      if (newPassword === "") passwordErrors.newPassword = "请输入新密码";
      if (settings.adminPasswordSet && currentPassword === "") {
        passwordErrors.currentPassword = "请输入当前密码";
      }
      Object.assign(nextErrors, passwordErrors);
    }

    setErrors((prev) => ({ ...prev, ...nextErrors }));
    if (Object.keys(nextErrors).length > 0) return;

    const patch: SettingsPatch = {
      timezone,
      retentionDays: Number(retentionDays),
    };
    if (passwordMode === "change") {
      patch.newPassword = newPassword;
      if (settings.adminPasswordSet) patch.currentPassword = currentPassword;
    }
    saveAccess.mutate(patch);
  }

  function requestClear() {
    const error = validateClearPassword(currentPassword);
    if (error) {
      setErrors((prev) => ({ ...prev, currentPassword: error }));
      return;
    }
    setErrors((prev) => {
      const next = { ...prev };
      delete next.currentPassword;
      return next;
    });
    clearPassword.mutate(currentPassword);
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

  return (
    <>
      <div className="settings-grid">
        <article className="card settings-card">
          <div className="card-head">
            <div>
              <div className="card-title">上游</div>
              <div className="card-sub">OpenAI 兼容服务</div>
            </div>
          </div>
          <div className="settings-body form-grid">
            <label className="form-field span-2">
              <span>Base URL</span>
              <input
                value={baseUrl}
                onChange={(event) => setBaseUrl(event.target.value)}
                placeholder="https://api.deepseek.com"
                autoComplete="off"
              />
              {errors.baseUrl && (
                <small className="form-error" role="alert">
                  {errors.baseUrl}
                </small>
              )}
            </label>

            <label className="form-field span-2">
              <span>上游 API Key</span>
              <input
                type="password"
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
                placeholder="留空表示不修改"
                autoComplete="new-password"
              />
              <small>当前 {redacted.upstream.apiKey}</small>
              {errors.apiKey && (
                <small className="form-error" role="alert">
                  {errors.apiKey}
                </small>
              )}
            </label>

            <div className="form-field">
              <span>连接</span>
              <div className="inline-key">
                <Button
                  variant="secondary"
                  onClick={() => test.mutate()}
                  disabled={test.isPending || baseUrl.trim() === ""}
                >
                  {test.isPending ? "测试中…" : "测试连接"}
                </Button>
                {testState && (
                  <Badge tone={testState.ok ? "success" : "danger"}>{testState.message}</Badge>
                )}
              </div>
            </div>

            {saveUpstream.isError && (
              <div className="span-2">
                <ErrorText error={saveUpstream.error} />
                <FieldErrors error={saveUpstream.error} />
              </div>
            )}

            <div className="form-actions span-2">
              <Button onClick={submitUpstream} disabled={saveUpstream.isPending}>
                {saveUpstream.isPending ? "保存中…" : "保存上游"}
              </Button>
            </div>
          </div>
        </article>

        <article className="card settings-card">
          <div className="card-head">
            <div>
              <div className="card-title">网关与访问</div>
              <div className="card-sub">本机监听与鉴权</div>
            </div>
            {settings.adminPasswordSet && (
              <Button
                variant="ghost"
                onClick={() => logout.mutate()}
                disabled={logout.isPending}
              >
                {logout.isPending ? "退出中…" : "退出登录"}
              </Button>
            )}
          </div>
          <div className="settings-body form-grid">
            <div className="form-field span-2">
              <span>Gateway Key</span>
              <div className="inline-key">
                <code title={settings.gatewayKey.set ? "已生成" : "未生成"}>
                  {redacted.gatewayKey}
                </code>
                <Button
                  variant="secondary"
                  onClick={() => setConfirmRotate(true)}
                  disabled={rotate.isPending || confirmRotate}
                >
                  {rotate.isPending ? "轮换中…" : "轮换"}
                </Button>
              </div>
              <small>明文只显示一次</small>
            </div>

            {rotatedKey ? (
              <div className="new-key span-2">
                <code>{rotatedKey}</code>
                <Button onClick={() => void copyRotatedKey()}>复制</Button>
                <Button variant="ghost" onClick={() => setRotatedKey(null)}>
                  我已保存
                </Button>
              </div>
            ) : (
              confirmRotate && (
                <div className="inline-key span-2">
                  <span className="form-error">轮换后旧 key 立即失效，确认继续？</span>
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
              )
            )}

            {rotate.isError && (
              <div className="span-2">
                <ErrorText error={rotate.error} />
              </div>
            )}

            <div className="form-field">
              <span>有效监听</span>
              <input value={settings.listen} readOnly aria-readonly="true" />
            </div>

            <label className="form-field">
              <span>时区</span>
              <select
                value={timezone}
                onChange={(event) => setTimezone(event.target.value)}
              >
                {timezoneOptions(timezone).map((zone) => (
                  <option key={zone} value={zone}>
                    {zone}
                  </option>
                ))}
              </select>
              {errors.timezone && (
                <small className="form-error" role="alert">
                  {errors.timezone}
                </small>
              )}
            </label>

            <div className="form-field span-2">
              <span>管理密码</span>
              <div className="inline-key">
                <span className={`status ${settings.adminPasswordSet ? "ok" : "warn"}`}>
                  {settings.adminPasswordSet ? "已设置" : "未设置"}
                </span>
                {passwordMode === null && (
                  <>
                    <Button
                      variant="secondary"
                      onClick={() => setPasswordMode("change")}
                      disabled={saveAccess.isPending || clearPassword.isPending}
                    >
                      {settings.adminPasswordSet ? "修改" : "设置"}
                    </Button>
                    {settings.adminPasswordSet && (
                      <Button
                        variant="secondary"
                        onClick={() => setPasswordMode("clear")}
                        disabled={!loopback || clearPassword.isPending}
                        title={
                          loopback
                            ? "清除后本机免登录访问"
                            : "仅回环监听时允许清除密码"
                        }
                      >
                        清除
                      </Button>
                    )}
                  </>
                )}
                {passwordMode !== null && (
                  <Button variant="ghost" onClick={cancelPasswordEdit}>
                    取消
                  </Button>
                )}
              </div>
              {settings.adminPasswordSet && !loopback && (
                <small>当前监听不是回环地址，清除密码会导致重启被拒绝，因此已禁用。</small>
              )}
            </div>

            {passwordMode === "change" && (
              <>
                {settings.adminPasswordSet && (
                  <label className="form-field span-2">
                    <span>当前密码</span>
                    <input
                      type="password"
                      value={currentPassword}
                      onChange={(event) => setCurrentPassword(event.target.value)}
                      autoComplete="current-password"
                    />
                    {errors.currentPassword && (
                      <small className="form-error" role="alert">
                        {errors.currentPassword}
                      </small>
                    )}
                  </label>
                )}
                <label className="form-field">
                  <span>新密码</span>
                  <input
                    type="password"
                    value={newPassword}
                    onChange={(event) => setNewPassword(event.target.value)}
                    autoComplete="new-password"
                  />
                  {errors.newPassword && (
                    <small className="form-error" role="alert">
                      {errors.newPassword}
                    </small>
                  )}
                </label>
                <label className="form-field">
                  <span>确认新密码</span>
                  <input
                    type="password"
                    value={confirmPassword}
                    onChange={(event) => setConfirmPassword(event.target.value)}
                    autoComplete="new-password"
                  />
                  {errors.confirmPassword && (
                    <small className="form-error" role="alert">
                      {errors.confirmPassword}
                    </small>
                  )}
                </label>
                <p className="settings-hint span-2">
                  修改密码后当前会话将失效，需要重新登录。
                </p>
              </>
            )}

            {passwordMode === "clear" && (
              <>
                <label className="form-field span-2">
                  <span>当前密码</span>
                  <input
                    type="password"
                    value={currentPassword}
                    onChange={(event) => setCurrentPassword(event.target.value)}
                    autoComplete="current-password"
                  />
                  {errors.currentPassword && (
                    <small className="form-error" role="alert">
                      {errors.currentPassword}
                    </small>
                  )}
                </label>
                <div className="inline-key span-2">
                  <span className="form-error">
                    清除后本机将免登录访问，确认继续？
                  </span>
                  <Button
                    variant="danger"
                    onClick={requestClear}
                    disabled={clearPassword.isPending}
                  >
                    {clearPassword.isPending ? "清除中…" : "确认清除"}
                  </Button>
                </div>
                {clearPassword.isError && (
                  <div className="span-2">
                    <ErrorText error={clearPassword.error} />
                    <FieldErrors error={clearPassword.error} />
                  </div>
                )}
              </>
            )}

            <label className="form-field span-2">
              <span>明细保留（天）</span>
              <input
                type="number"
                min={7}
                max={3650}
                value={retentionDays}
                onChange={(event) => setRetentionDays(event.target.value)}
              />
              {errors.retentionDays && (
                <small className="form-error" role="alert">
                  {errors.retentionDays}
                </small>
              )}
            </label>

            {saveAccess.isError && (
              <div className="span-2">
                <ErrorText error={saveAccess.error} />
                <FieldErrors error={saveAccess.error} />
              </div>
            )}

            {passwordMode !== "clear" && (
              <div className="form-actions span-2">
                <Button onClick={submitAccess} disabled={saveAccess.isPending}>
                  {saveAccess.isPending ? "保存中…" : "保存网关设置"}
                </Button>
              </div>
            )}
          </div>
        </article>
      </div>
      <p className="settings-hint">修改监听地址需要重启进程；其余设置保存后即时生效。</p>
    </>
  );
}
