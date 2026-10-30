import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { ApiError, api } from "../api/client";
import { AuthLayout } from "../components/AuthLayout";
import { Button, Input, Select, useToast } from "../components/ui";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import {
  browserTimezone,
  timezoneOptions,
  type FormErrors,
} from "../lib/settings";

const FIELD_LABELS: Record<string, string> = {
  baseUrl: "上游 Base URL",
  apiKey: "上游 API Key",
  timezone: "时区",
  adminPassword: "管理密码",
};

/** Setup is the two-step first-run wizard that creates the gateway key. */
export function Setup() {
  useDocumentTitle("首次配置");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const toast = useToast();

  const [baseUrl, setBaseUrl] = useState("https://api.deepseek.com");
  const [apiKey, setApiKey] = useState("");
  const [timezone, setTimezone] = useState(browserTimezone);
  const [password, setPassword] = useState("");
  const [errors, setErrors] = useState<FormErrors>({});

  const setup = useMutation({
    mutationFn: () =>
      api.setup({
        upstream: {
          baseUrl: baseUrl.trim(),
          apiKey: apiKey.trim(),
        },
        timezone,
        adminPassword: password,
      }),
    onError: (error) => {
      // Another tab may have completed setup; refresh the guard so it routes
      // to the login or overview page instead of resubmitting.
      if (error instanceof ApiError && error.status === 409) {
        void queryClient.invalidateQueries({ queryKey: ["bootstrap"] });
      }
    },
  });

  function clearError(field: string) {
    setErrors((current) => {
      if (!(field in current)) return current;
      const next = { ...current };
      delete next[field];
      return next;
    });
    if (setup.isError && !setup.isPending) setup.reset();
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const next: FormErrors = {};
    if (!/^https?:\/\//i.test(baseUrl.trim())) {
      next.baseUrl = "请输入 http(s):// 开头的 Base URL";
    }
    if (apiKey.trim() === "") next.apiKey = "请填写上游 API Key";
    if (password !== "" && password.length < 8) {
      next.adminPassword = "管理密码至少需要 8 个字符";
    }
    setErrors(next);
    if (Object.keys(next).length > 0) return;
    setup.mutate();
  }

  async function enterOverview() {
    // Refresh the guard before leaving so it does not bounce back to /setup
    // with the stale needsSetup flag.
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
      queryClient.invalidateQueries({ queryKey: ["me"] }),
      queryClient.invalidateQueries({ queryKey: ["settings"] }),
    ]);
    navigate("/", { replace: true });
  }

  async function copyKey() {
    const key = setup.data?.gatewayKey;
    if (!key) return;
    try {
      await navigator.clipboard.writeText(key);
      toast("gateway key 已复制", "success");
    } catch {
      toast("复制失败，请手动选择并复制", "error");
    }
  }

  const gatewayKey = setup.data?.gatewayKey;
  const apiFields: Record<string, string> =
    setup.error instanceof ApiError ? setup.error.fields : {};
  const problems: string[] = [
    ...Object.entries(errors).map(([field, message]) => `${FIELD_LABELS[field] ?? field}：${message}`),
    ...(setup.isError
      ? Object.keys(apiFields).length > 0
        ? Object.entries(apiFields).map(
            ([field, message]) => `${FIELD_LABELS[field] ?? field}：${message}`,
          )
        : [setup.error instanceof Error ? setup.error.message : "配置失败，请重试"]
      : []),
  ];
  const hasProblems = problems.length > 0;

  return (
    <AuthLayout
      eyebrow="first run"
      heading={
        <>
          第一次打开，
          <br />
          先把网关接上。
        </>
      }
      lead="填写上游服务与可选的访问密码，生成客户端使用的 gateway key。整个过程只写入本机 SQLite。"
      stats={[
        { value: "1", label: "个上游" },
        { value: "0", label: "外部依赖" },
        { value: "本机", label: "数据位置" },
      ]}
    >
      <div className="auth-card setup-card">
        <div className="setup-steps">
          <span className={gatewayKey ? "" : "on"}>1 上游</span>
          <span className={gatewayKey ? "on" : ""}>2 Gateway Key</span>
        </div>

        {gatewayKey ? (
          <div>
            <div className="page-eyebrow">ready</div>
            <h2>网关已就绪</h2>
            <p className="auth-sub">这是客户端使用的 gateway key，明文只显示这一次。</p>
            <div className="key-box">
              <code>{gatewayKey}</code>
              <Button
                variant="secondary"
                onClick={() => void copyKey()}
                aria-label="复制 gateway key"
              >
                复制
              </Button>
            </div>
            <div className="setup-warning">
              请立刻保存。之后只能在设置页轮换，无法再次查看。
            </div>
            <Button className="auth-submit" onClick={() => void enterOverview()}>
              进入总览
            </Button>
          </div>
        ) : (
          <form onSubmit={submit} aria-describedby={hasProblems ? "setup-error" : undefined}>
            <div className="page-eyebrow">first run</div>
            <h2>首次配置</h2>
            <p className="auth-sub">填写上游服务，生成客户端使用的 gateway key。</p>

            <label className="auth-field">
              <span>上游 Base URL</span>
              <Input
                value={baseUrl}
                onChange={(event) => {
                  setBaseUrl(event.target.value);
                  clearError("baseUrl");
                }}
                placeholder="https://api.deepseek.com"
                autoComplete="off"
                required
                aria-invalid={errors.baseUrl !== undefined || apiFields.baseUrl !== undefined}
              />
            </label>

            <label className="auth-field">
              <span>上游 API Key</span>
              <Input
                type="password"
                value={apiKey}
                onChange={(event) => {
                  setApiKey(event.target.value);
                  clearError("apiKey");
                }}
                placeholder="sk-..."
                autoComplete="new-password"
                required
                aria-invalid={errors.apiKey !== undefined || apiFields.apiKey !== undefined}
              />
            </label>

            <label className="auth-field">
              <span>时区</span>
              <Select
                value={timezone}
                onChange={(event) => {
                  setTimezone(event.target.value);
                  clearError("timezone");
                }}
                aria-invalid={errors.timezone !== undefined || apiFields.timezone !== undefined}
              >
                {timezoneOptions(timezone).map((zone) => (
                  <option key={zone} value={zone}>
                    {zone}
                  </option>
                ))}
              </Select>
            </label>

            <label className="auth-field">
              <span>管理密码（可选）</span>
              <Input
                type="password"
                value={password}
                onChange={(event) => {
                  setPassword(event.target.value);
                  clearError("adminPassword");
                }}
                placeholder="留空则仅本机访问"
                autoComplete="new-password"
                aria-invalid={errors.adminPassword !== undefined || apiFields.adminPassword !== undefined}
              />
            </label>

            {hasProblems && (
              <div className="auth-error" id="setup-error" role="alert">
                {problems.map((problem) => (
                  <div key={problem}>{problem}</div>
                ))}
              </div>
            )}

            <Button type="submit" className="auth-submit" disabled={setup.isPending}>
              {setup.isPending ? "生成中…" : "生成 Gateway Key"}
            </Button>
            <div className="auth-meta">不要在上游 Base URL 后加 /v1</div>
          </form>
        )}
      </div>
    </AuthLayout>
  );
}
