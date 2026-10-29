import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../api/client";
import { Button, Card, ErrorText, Input } from "../components/ui";

function browserTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai";
  } catch {
    return "Asia/Shanghai";
  }
}

export function Setup() {
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [baseUrl, setBaseUrl] = useState("https://api.openai.com/v1");
  const [apiKey, setApiKey] = useState("");
  const [timezone, setTimezone] = useState(browserTimezone);
  const [timeoutS, setTimeoutS] = useState("30");
  const [password, setPassword] = useState("");

  const setup = useMutation({
    mutationFn: () =>
      api.setup({
        upstream: {
          baseUrl: baseUrl.trim(),
          apiKey: apiKey.trim(),
          timeoutS: Number(timeoutS) || 0,
        },
        timezone: timezone.trim(),
        adminPassword: password,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries();
    },
  });

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setup.mutate();
  };

  if (setup.data) {
    return (
      <div className="mx-auto max-w-xl p-6">
        <h1 className="mb-2 text-xl font-semibold">初始化完成</h1>
        <p className="mb-4 text-sm text-muted">
          请立即复制并妥善保存 gateway key，此明文仅显示一次。
        </p>
        <Card title="Gateway Key">
          <code className="block break-all rounded bg-line/30 px-3 py-2 text-sm">
            {setup.data.gatewayKey}
          </code>
          <div className="mt-4 flex gap-2">
            <Button onClick={() => void navigator.clipboard.writeText(setup.data.gatewayKey)}>
              复制
            </Button>
            <Button variant="secondary" onClick={() => navigate("/")}>
              进入总览
            </Button>
          </div>
        </Card>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-xl p-6">
      <h1 className="mb-2 text-xl font-semibold">引导设置</h1>
      <p className="mb-4 text-sm text-muted">配置上游地址、管理密码，并生成 gateway key。</p>

      <form onSubmit={submit} className="space-y-4">
        <Card title="上游">
          <div className="space-y-3">
            <label className="block text-sm">
              <span className="mb-1 block text-muted">Base URL</span>
              <Input
                value={baseUrl}
                onChange={(event) => setBaseUrl(event.target.value)}
                placeholder="https://api.openai.com/v1"
                required
              />
            </label>
            <label className="block text-sm">
              <span className="mb-1 block text-muted">API Key</span>
              <Input
                type="password"
                value={apiKey}
                onChange={(event) => setApiKey(event.target.value)}
                autoComplete="off"
              />
            </label>
            <div className="grid grid-cols-2 gap-3">
              <label className="block text-sm">
                <span className="mb-1 block text-muted">时区</span>
                <Input
                  value={timezone}
                  onChange={(event) => setTimezone(event.target.value)}
                  placeholder="Asia/Shanghai"
                />
              </label>
              <label className="block text-sm">
                <span className="mb-1 block text-muted">超时（秒）</span>
                <Input
                  type="number"
                  min={0}
                  max={3600}
                  value={timeoutS}
                  onChange={(event) => setTimeoutS(event.target.value)}
                />
              </label>
            </div>
          </div>
        </Card>

        <Card title="管理密码">
          <label className="block text-sm">
            <span className="mb-1 block text-muted">密码（可留空，稍后在设置页添加）</span>
            <Input
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              autoComplete="new-password"
            />
          </label>
        </Card>

        {setup.isError && <ErrorText error={setup.error} />}

        <Button type="submit" disabled={setup.isPending}>
          {setup.isPending ? "提交中…" : "完成设置"}
        </Button>
      </form>
    </div>
  );
}
