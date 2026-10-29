import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { api } from "../api/client";
import { Button, Card, ErrorText, Input } from "../components/ui";

export function Login() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [params] = useSearchParams();
  const [password, setPassword] = useState("");

  const login = useMutation({
    mutationFn: () => api.login(password),
    onSuccess: async () => {
      await queryClient.invalidateQueries();
      navigate(params.get("next") ?? "/", { replace: true });
    },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    login.mutate();
  };

  return (
    <div className="mx-auto flex min-h-screen max-w-sm flex-col justify-center p-6">
      <h1 className="mb-2 text-xl font-semibold">登录</h1>
      <p className="mb-4 text-sm text-muted">llmmeter 管理界面需要管理密码。</p>
      <form onSubmit={submit} className="space-y-4">
        <Card title="管理密码">
          <label className="block text-sm">
            <span className="mb-1 block text-muted">密码</span>
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
            <div className="mt-3">
              <ErrorText error={login.error} />
            </div>
          )}
        </Card>
        <Button type="submit" disabled={login.isPending}>
          {login.isPending ? "登录中…" : "登录"}
        </Button>
      </form>
    </div>
  );
}
