import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, api } from "../api/client";
import type { Bootstrap, Me } from "../api/types";
import { Button, Card, ErrorText, Input, Select, useToast } from "../components/ui";
import {
  browserTimezone,
  timezoneOptions,
  validateTimeout,
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

export function Setup() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const toast = useToast();

  const [baseUrl, setBaseUrl] = useState("https://api.openai.com/v1");
  const [apiKey, setApiKey] = useState("");
  const [timeoutS, setTimeoutS] = useState("30");
  const [timezone, setTimezone] = useState(browserTimezone);
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [errors, setErrors] = useState<FormErrors>({});
  const [testState, setTestState] = useState<{
    tone: "success" | "warning";
    text: string;
  } | null>(null);

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

  const setup = useMutation({
    mutationFn: () =>
      api.setup({
        upstream: {
          baseUrl: baseUrl.trim(),
          apiKey: apiKey.trim(),
          timeoutS: Number(timeoutS),
        },
        timezone,
        adminPassword: password,
      }),
    onError: async (error) => {
      // Another tab may have completed setup; refresh the guard so it routes
      // to the login or overview page instead of resubmitting.
      if (error instanceof ApiError && error.status === 409) {
        await queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      }
    },
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextErrors: FormErrors = {};
    if (baseUrl.trim() === "") nextErrors.baseUrl = "请填写上游 Base URL";
    if (apiKey.trim() === "") nextErrors.apiKey = "请填写上游 API Key";
    const timeoutError = validateTimeout(timeoutS);
    if (timeoutError) nextErrors.timeoutS = timeoutError;
    if (timezone.trim() === "") nextErrors.timezone = "请选择时区";
    if (password !== "" && password.length < 8) {
      nextErrors.password = "密码至少需要 8 个字符";
    } else if (password !== confirmPassword) {
      nextErrors.confirmPassword = "两次输入的密码不一致";
    }
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    setup.mutate();
  }

  function enterOverview() {
    const authRequired = password !== "";
    queryClient.setQueryData<Bootstrap>(["bootstrap"], (current) =>
      current ? { ...current, needsSetup: false, authRequired } : current,
    );
    queryClient.setQueryData<Me>(["me"], { authRequired, authenticated: true });
    navigate("/", { replace: true });
    void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
    void queryClient.invalidateQueries({ queryKey: ["me"] });
  }

  async function copyKey(key: string) {
    try {
      await navigator.clipboard.writeText(key);
      toast("已复制 gateway key", "success");
    } catch {
      toast("复制失败，请手动选择并复制", "error");
    }
  }

  const result = setup.data;
  if (result) {
    const gatewayBase = `${window.location.origin}/v1`;
    return (
      <div className="mx-auto max-w-xl p-6 text-ink">
        <h1 className="mb-2 text-xl font-semibold">初始化完成</h1>
        <p className="mb-4 text-sm text-muted">
          请立即复制并妥善保存 gateway key，此明文仅显示一次。
        </p>

        <Card title="Gateway Key">
          <code className="block break-all rounded bg-line/30 px-3 py-2 text-sm">
            {result.gatewayKey}
          </code>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button onClick={() => void copyKey(result.gatewayKey)}>复制</Button>
            <Button variant="secondary" onClick={enterOverview}>
              进入总览
            </Button>
          </div>
        </Card>

        <Card title="客户端接入" className="mt-4">
          <p className="mb-2 text-sm text-muted">
            将以下 <code className="text-ink">base_url</code> 与 gateway key 填入任意
            OpenAI 兼容客户端。
          </p>
          <pre className="overflow-x-auto rounded bg-line/30 px-3 py-2 text-xs">
            {`base_url = "${gatewayBase}"\napi_key  = "<上方生成的 gateway key>"`}
          </pre>
        </Card>
      </div>
    );
  }

  const fieldErrors = setup.error instanceof ApiError ? Object.entries(setup.error.fields) : [];

  return (
    <div className="mx-auto max-w-xl p-6 text-ink">
      <h1 className="mb-2 text-xl font-semibold">引导设置</h1>
      <p className="mb-4 text-sm text-muted">
        配置上游地址、时区与管理密码，完成后会生成一次性 gateway key。
      </p>

      <form onSubmit={submit} className="space-y-4">
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
            <Field label="Base URL" error={errors.baseUrl}>
              <Input
                value={baseUrl}
                onChange={(event) => setBaseUrl(event.target.value)}
                placeholder="https://api.openai.com/v1"
                autoComplete="off"
                required
              />
            </Field>
            <Field label="API Key" error={errors.apiKey}>
              <Input
                type="password"
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
                autoComplete="off"
                required
              />
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
              <p
                className={
                  testState.tone === "success"
                    ? "text-xs text-success"
                    : "text-xs text-warning"
                }
                role="status"
              >
                {testState.text}
              </p>
            )}
            <p className="text-xs text-muted">测试失败不影响提交，可稍后在设置页修改。</p>
          </div>
        </Card>

        <Card title="时区与管理密码">
          <div className="space-y-3">
            <Field label="时区" hint="用于日期分桶与高峰价匹配" error={errors.timezone}>
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
            <div className="grid gap-3 sm:grid-cols-2">
              <Field label="管理密码" hint="可选，至少 8 位" error={errors.password}>
                <Input
                  type="password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  autoComplete="new-password"
                />
              </Field>
              <Field label="确认密码" error={errors.confirmPassword}>
                <Input
                  type="password"
                  value={confirmPassword}
                  onChange={(event) => setConfirmPassword(event.target.value)}
                  autoComplete="new-password"
                />
              </Field>
            </div>
            <p className="text-xs text-muted">
              留空则仅允许本机访问；设置后登录管理界面需要密码。
            </p>
          </div>
        </Card>

        {setup.isError && (
          <div className="space-y-1">
            <ErrorText error={setup.error} />
            {fieldErrors.length > 0 && (
              <ul className="space-y-0.5 text-xs text-danger">
                {fieldErrors.map(([field, message]) => (
                  <li key={field}>
                    {field}：{message}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}

        <Button type="submit" disabled={setup.isPending}>
          {setup.isPending ? "提交中…" : "完成设置"}
        </Button>
      </form>
    </div>
  );
}
