import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, api } from "../api/client";
import { AuthLayout } from "../components/AuthLayout";
import { Button, Input } from "../components/ui";
import { useDocumentTitle } from "../hooks/useDocumentTitle";
import { safeNext } from "../lib/auth";

function loginError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return "密码不正确，请重试";
    if (error.status === 429) return "尝试次数过多，请稍后再试";
    return error.message;
  }
  return error instanceof Error ? error.message : "登录失败";
}

/** Login is the split-screen sign-in page for the admin password. */
export function Login() {
  useDocumentTitle("登录");
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: (value: string) => api.login(value),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
        queryClient.invalidateQueries({ queryKey: ["me"] }),
      ]);
      navigate(next, { replace: true });
    },
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    login.mutate(password);
  }

  function changePassword(value: string) {
    setPassword(value);
    if (login.isError && !login.isPending) login.reset();
  }

  return (
    <AuthLayout
      eyebrow="local gateway"
      heading={
        <>
          把每一次调用
          <br />
          变成可读的账单。
        </>
      }
      lead="OpenAI 兼容透传、逐条记账、实时大盘。所有数据只留在本机。"
      stats={[
        { value: "OpenAI 兼容", label: "上游协议" },
        { value: "本地 SQLite", label: "数据存储" },
        { value: "SSE 实时", label: "推送通道" },
      ]}
    >
      <div className="auth-card">
        <div className="page-eyebrow">sign in</div>
        <h2>登录</h2>
        <p className="auth-sub">管理界面需要管理密码</p>
        <form onSubmit={submit}>
          <label className="auth-field">
            <span>管理密码</span>
            <Input
              type="password"
              value={password}
              onChange={(event) => changePassword(event.target.value)}
              placeholder="输入管理密码"
              autoComplete="current-password"
              autoFocus
              required
              aria-invalid={login.isError}
              aria-describedby={login.isError ? "login-error" : undefined}
            />
          </label>
          {login.isError && (
            <div className="auth-error" id="login-error" role="alert">
              {loginError(login.error)}
            </div>
          )}
          <Button type="submit" className="auth-submit" disabled={login.isPending}>
            {login.isPending ? "登录中…" : "登录"}
          </Button>
        </form>
        <div className="auth-meta">本机访问 · 会话有效 12 小时</div>
      </div>
    </AuthLayout>
  );
}
