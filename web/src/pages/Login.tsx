import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, api } from "../api/client";
import { Button, Card, Input } from "../components/ui";
import { safeNext } from "../lib/auth";

function loginError(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 401) return "密码错误，请重试";
    if (error.status === 429) return "尝试次数过多，请稍后再试";
    return error.message;
  }
  return error instanceof Error ? error.message : "登录失败";
}

export function Login() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [params] = useSearchParams();
  const next = safeNext(params.get("next"));
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ["bootstrap"] }),
        queryClient.invalidateQueries({ queryKey: ["me"] }),
      ]);
      navigate(next, { replace: true });
    },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    login.mutate();
  };

  const fieldErrors =
    login.error instanceof ApiError ? Object.entries(login.error.fields) : [];

  return (
    <div className="flex min-h-screen items-center justify-center bg-surface p-6 text-ink">
      <div className="w-full max-w-sm">
        <div className="mb-6 text-center">
          <div className="text-lg font-semibold tracking-tight">llmmeter</div>
          <p className="text-sm text-muted">管理界面需要管理密码</p>
        </div>
        <form onSubmit={submit} className="space-y-4">
          <Card title="登录">
            <label className="block text-sm">
              <span className="mb-1 block text-xs text-muted">管理密码</span>
              <Input
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                autoComplete="current-password"
                autoFocus
                required
              />
            </label>
            {login.isError && (
              <p className="mt-3 text-sm text-danger" role="alert">
                {loginError(login.error)}
              </p>
            )}
            {fieldErrors.length > 0 && (
              <ul className="mt-1 space-y-0.5 text-xs text-danger">
                {fieldErrors.map(([field, message]) => (
                  <li key={field}>
                    {field}：{message}
                  </li>
                ))}
              </ul>
            )}
          </Card>
          <Button type="submit" className="w-full" disabled={login.isPending}>
            {login.isPending ? "登录中…" : "登录"}
          </Button>
        </form>
      </div>
    </div>
  );
}
