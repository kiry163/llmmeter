import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactElement } from "react";
import { ApiError, api } from "../../api/client";
import type {
  KeyCreated,
  LocalKey,
  ProbeResult,
  Upstream,
  UpstreamInput,
} from "../../api/types";
import { Badge, Button, ErrorText, Field, Input, Spinner, useToast } from "../../components/ui";
import { formatTime } from "../../lib/format";
import { useTimezone } from "../../hooks/useTimezone";

function messageOf(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

function errorFields(error: unknown): Record<string, string> {
  return error instanceof ApiError ? error.fields : {};
}

/** SecretPanel shows a plaintext secret exactly once, with clipboard copy. */
function SecretPanel({
  title,
  value,
  onClose,
}: {
  title: string;
  value: string;
  onClose: () => void;
}): ReactElement {
  const toast = useToast();
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      toast("已复制到剪贴板", "success");
    } catch {
      toast("复制失败，请手动选择", "error");
    }
  };
  return (
    <article className="card secret-panel">
      <div className="card-head">
        <div>
          <div className="card-title">{title}</div>
          <div className="card-sub">仅本次显示，关闭后不再明文展示</div>
        </div>
        <button type="button" className="ghost-btn" onClick={onClose}>
          关闭
        </button>
      </div>
      <div className="secret-row">
        <code>{value}</code>
        <Button variant="secondary" onClick={() => void copy()}>
          复制
        </Button>
      </div>
    </article>
  );
}

interface UpstreamDraft {
  name: string;
  baseUrl: string;
  apiKey: string;
  enabled: boolean;
}

function draftFrom(upstream: Upstream | null): UpstreamDraft {
  return {
    name: upstream?.name ?? "",
    baseUrl: upstream?.baseUrl ?? "",
    apiKey: "",
    enabled: upstream?.enabled ?? true,
  };
}

function UpstreamEditor({
  upstream,
  onDone,
}: {
  upstream: Upstream | null;
  onDone: () => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [draft, setDraft] = useState<UpstreamDraft>(() => draftFrom(upstream));
  const [revealed, setRevealed] = useState<string | null>(null);
  const [probe, setProbe] = useState<ProbeResult | null>(null);
  const [errors, setErrors] = useState<Record<string, string>>({});

  const save = useMutation({
    mutationFn: async (input: UpstreamInput) =>
      upstream ? api.updateUpstream(upstream.id, input) : api.createUpstream(input),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["upstreams"] });
      void queryClient.invalidateQueries({ queryKey: ["keys"] });
      toast(upstream ? "上游已更新" : "上游已添加", "success");
      onDone();
    },
    onError: (error) => {
      setErrors(errorFields(error));
      toast(messageOf(error, "保存上游失败"), "error");
    },
  });

  const probeMutation = useMutation({
    mutationFn: () =>
      api.probeUpstream({
        baseUrl: draft.baseUrl.trim(),
        apiKey: draft.apiKey.trim(),
      }),
    onSuccess: (result) => setProbe(result),
    onError: (error) => toast(messageOf(error, "探测失败"), "error"),
  });

  const reveal = useMutation({
    mutationFn: () => api.revealUpstreamKey(upstream!.id),
    onSuccess: (body) => setRevealed(body.apiKey),
    onError: (error) => toast(messageOf(error, "读取密钥失败"), "error"),
  });

  const submit = () => {
    const nextErrors: Record<string, string> = {};
    if (draft.name.trim() === "") nextErrors.name = "请填写名称";
    if (draft.baseUrl.trim() === "") nextErrors.baseUrl = "请填写 Base URL";
    if (!upstream && draft.apiKey.trim() === "") nextErrors.apiKey = "请填写 API Key";
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    const input: UpstreamInput = {
      name: draft.name.trim(),
      baseUrl: draft.baseUrl.trim(),
      enabled: draft.enabled,
    };
    if (draft.apiKey.trim() !== "") input.apiKey = draft.apiKey.trim();
    save.mutate(input);
  };

  return (
    <article className="card settings-card editor-card">
      <div className="card-head">
        <div>
          <div className="card-title">{upstream ? `编辑 ${upstream.name}` : "添加上游"}</div>
          <div className="card-sub">OpenAI 兼容上游 · 保存后立即生效</div>
        </div>
        <button type="button" className="ghost-btn" onClick={onDone}>
          取消
        </button>
      </div>
      <div className="settings-body form-grid">
        <Field label="名称" error={errors.name}>
          <Input
            value={draft.name}
            placeholder="例如 DeepSeek"
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
          />
        </Field>
        <Field label="Base URL" hint="不要带 /v1，例如 https://api.deepseek.com" error={errors.baseUrl}>
          <Input
            value={draft.baseUrl}
            placeholder="https://api.deepseek.com"
            onChange={(event) => setDraft({ ...draft, baseUrl: event.target.value })}
          />
        </Field>
        <Field
          label="API Key"
          hint={upstream ? "留空表示不修改；填写则覆盖旧密钥" : "只保存在本机配置文件"}
          error={errors.apiKey}
        >
          <div className="inline-field">
            <Input
              type="password"
              value={draft.apiKey}
              placeholder={upstream ? upstream.apiKeyMasked : "sk-..."}
              onChange={(event) => setDraft({ ...draft, apiKey: event.target.value })}
            />
            {upstream && (
              <Button
                variant="secondary"
                disabled={reveal.isPending}
                onClick={() => reveal.mutate()}
              >
                {revealed ? "已显示" : "显示"}
              </Button>
            )}
          </div>
        </Field>
        {revealed && (
          <div className="secret-inline">
            <span>当前密钥</span>
            <code>{revealed}</code>
            <button type="button" className="ghost-btn" onClick={() => setRevealed(null)}>
              隐藏
            </button>
          </div>
        )}
        <label className="check-field">
          <input
            type="checkbox"
            checked={draft.enabled}
            onChange={(event) => setDraft({ ...draft, enabled: event.target.checked })}
          />
          启用该上游
        </label>
        <div className="form-actions">
          <Button variant="secondary" disabled={probeMutation.isPending} onClick={() => probeMutation.mutate()}>
            {probeMutation.isPending ? "探测中…" : "探测模型"}
          </Button>
          <Button disabled={save.isPending} onClick={submit}>
            {save.isPending ? "保存中…" : "保存"}
          </Button>
        </div>
        {probe && (
          <div className={`probe-result ${probe.ok ? "ok" : "err"}`}>
            <div>
              {probe.ok
                ? `连接正常 · ${probe.latencyMs}ms · ${probe.models.length} 个模型`
                : `连接失败：${probe.error || "未知错误"}`}
              {probe.provider && <span className="muted"> · {probe.provider}</span>}
            </div>
            {probe.models.length > 0 && (
              <div className="model-chips">
                {probe.models.slice(0, 24).map((model) => (
                  <span className={`chip${model.matched ? " matched" : ""}`} key={model.id}>
                    {model.id}
                  </span>
                ))}
                {probe.models.length > 24 && (
                  <span className="chip">+{probe.models.length - 24}</span>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </article>
  );
}

function UpstreamCard({
  upstream,
  onEdit,
  onSecret,
}: {
  upstream: Upstream;
  onEdit: () => void;
  onSecret: (secret: string, title: string) => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const toast = useToast();
  const timeZone = useTimezone();
  const [test, setTest] = useState<{ ok: boolean; text: string } | null>(null);

  const testMutation = useMutation({
    mutationFn: () => api.testUpstream(upstream.id),
    onSuccess: (result) => {
      setTest({
        ok: result.ok,
        text: result.ok
          ? `正常 · ${result.latencyMs}ms · ${result.modelCount} 个模型`
          : `失败：${result.error || "未知错误"}`,
      });
      void queryClient.invalidateQueries({ queryKey: ["upstreams"] });
    },
    onError: (error) => setTest({ ok: false, text: messageOf(error, "测试失败") }),
  });

  const reveal = useMutation({
    mutationFn: () => api.revealUpstreamKey(upstream.id),
    onSuccess: (body) => onSecret(body.apiKey, `${upstream.name} 的 API Key`),
    onError: (error) => toast(messageOf(error, "读取密钥失败"), "error"),
  });

  const remove = useMutation({
    mutationFn: () => api.deleteUpstream(upstream.id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["upstreams"] });
      void queryClient.invalidateQueries({ queryKey: ["keys"] });
      toast("上游已删除", "success");
    },
    onError: (error) => toast(messageOf(error, "删除上游失败"), "error"),
  });

  const confirmRemove = () => {
    if (window.confirm(`删除上游「${upstream.name}」？仍有密钥引用时会被拒绝。`)) {
      remove.mutate();
    }
  };

  return (
    <article className="card upstream-card">
      <div className="upstream-head">
        <div>
          <div className="upstream-name">
            {upstream.name}
            <Badge tone={upstream.enabled ? "success" : "neutral"}>
              {upstream.enabled ? "启用" : "停用"}
            </Badge>
          </div>
          <div className="upstream-url mono">{upstream.baseUrl}</div>
        </div>
        <div className="upstream-actions">
          <Button variant="secondary" disabled={testMutation.isPending} onClick={() => testMutation.mutate()}>
            {testMutation.isPending ? "测试中…" : "测试"}
          </Button>
          <Button variant="secondary" onClick={onEdit}>
            编辑
          </Button>
          <Button variant="secondary" onClick={() => reveal.mutate()}>
            密钥
          </Button>
          <Button variant="ghost" disabled={remove.isPending} onClick={confirmRemove}>
            删除
          </Button>
        </div>
      </div>
      <div className="upstream-meta">
        <span>
          密钥 <b className="mono">{upstream.apiKeyMasked || "未设置"}</b>
        </span>
        <span>
          模型 <b>{upstream.modelCount}</b>
        </span>
        <span>
          网关密钥 <b>{upstream.keyCount}</b>
        </span>
        <span>
          最近使用{" "}
          <b>{upstream.lastUsedAt ? formatTime(upstream.lastUsedAt, timeZone) : "—"}</b>
        </span>
      </div>
      {test && (
        <div className={`probe-result ${test.ok ? "ok" : "err"}`}>{test.text}</div>
      )}
    </article>
  );
}

function KeyRow({
  localKey,
  upstreams,
  onSecret,
}: {
  localKey: LocalKey;
  upstreams: Upstream[];
  onSecret: (secret: string, title: string) => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const toast = useToast();
  const timeZone = useTimezone();

  const invalidate = () => {
    void queryClient.invalidateQueries({ queryKey: ["keys"] });
    void queryClient.invalidateQueries({ queryKey: ["upstreams"] });
  };

  const update = useMutation({
    mutationFn: (patch: { upstreamId?: string; enabled?: boolean }) =>
      api.updateKey(localKey.id, patch),
    onSuccess: () => {
      invalidate();
      toast("网关密钥已更新", "success");
    },
    onError: (error) => toast(messageOf(error, "更新密钥失败"), "error"),
  });

  const rotate = useMutation({
    mutationFn: () => api.rotateKey(localKey.id),
    onSuccess: (created) => {
      invalidate();
      onSecret(created.key, `${created.name} 的新密钥`);
      toast("密钥已轮换，旧密钥立即失效", "success");
    },
    onError: (error) => toast(messageOf(error, "轮换失败"), "error"),
  });

  const reveal = useMutation({
    mutationFn: () => api.revealKey(localKey.id),
    onSuccess: (body) => onSecret(body.key, `${localKey.name} 的密钥`),
    onError: (error) => toast(messageOf(error, "读取密钥失败"), "error"),
  });

  const remove = useMutation({
    mutationFn: () => api.deleteKey(localKey.id),
    onSuccess: () => {
      invalidate();
      toast("网关密钥已删除", "success");
    },
    onError: (error) => toast(messageOf(error, "删除密钥失败"), "error"),
  });

  return (
    <tr>
      <td>
        <b>{localKey.name}</b>
      </td>
      <td className="mono">{localKey.keyMasked}</td>
      <td>
        <select
          className="ui-input compact"
          value={localKey.upstreamId}
          disabled={update.isPending}
          onChange={(event) => update.mutate({ upstreamId: event.target.value })}
        >
          {upstreams.map((upstream) => (
            <option key={upstream.id} value={upstream.id}>
              {upstream.name}
            </option>
          ))}
        </select>
      </td>
      <td>
        <label className="switch">
          <input
            type="checkbox"
            checked={localKey.enabled}
            disabled={update.isPending}
            onChange={(event) => update.mutate({ enabled: event.target.checked })}
          />
          <span>{localKey.enabled ? "启用" : "停用"}</span>
        </label>
      </td>
      <td>{localKey.lastUsedAt ? formatTime(localKey.lastUsedAt, timeZone) : "—"}</td>
      <td>
        <div className="row-actions">
          <button type="button" className="ghost-btn" onClick={() => reveal.mutate()}>
            显示
          </button>
          <button
            type="button"
            className="ghost-btn"
            onClick={() => {
              if (window.confirm(`轮换「${localKey.name}」？旧密钥会立即失效。`)) {
                rotate.mutate();
              }
            }}
          >
            轮换
          </button>
          <button
            type="button"
            className="ghost-btn danger-text"
            onClick={() => {
              if (window.confirm(`删除网关密钥「${localKey.name}」？`)) {
                remove.mutate();
              }
            }}
          >
            删除
          </button>
        </div>
      </td>
    </tr>
  );
}

function CreateKey({
  upstreams,
  onCreated,
}: {
  upstreams: Upstream[];
  onCreated: (created: KeyCreated) => void;
}): ReactElement {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [name, setName] = useState("");
  const [upstreamId, setUpstreamId] = useState(upstreams[0]?.id ?? "");
  const [error, setError] = useState("");

  const create = useMutation({
    mutationFn: () => api.createKey({ name: name.trim(), upstreamId }),
    onSuccess: (created) => {
      void queryClient.invalidateQueries({ queryKey: ["keys"] });
      void queryClient.invalidateQueries({ queryKey: ["upstreams"] });
      setName("");
      setError("");
      onCreated(created);
      toast("网关密钥已创建", "success");
    },
    onError: (err) => {
      setError(errorFields(err).name ?? messageOf(err, "创建密钥失败"));
    },
  });

  return (
    <div className="inline-create">
      <Input
        value={name}
        placeholder="密钥名称，例如 omp"
        onChange={(event) => setName(event.target.value)}
      />
      <select
        className="ui-input"
        value={upstreamId}
        onChange={(event) => setUpstreamId(event.target.value)}
      >
        {upstreams.map((upstream) => (
          <option key={upstream.id} value={upstream.id}>
            {upstream.name}
          </option>
        ))}
      </select>
      <Button
        disabled={create.isPending || name.trim() === "" || upstreamId === ""}
        onClick={() => create.mutate()}
      >
        {create.isPending ? "创建中…" : "新建密钥"}
      </Button>
      {error && <small className="form-error">{error}</small>}
    </div>
  );
}

/** Upstreams manages OpenAI-compatible upstreams and their local gateway keys. */
export function Upstreams(): ReactElement {
  const queryClient = useQueryClient();
  const upstreams = useQuery({ queryKey: ["upstreams"], queryFn: api.getUpstreams });
  const keys = useQuery({ queryKey: ["keys"], queryFn: api.getKeys });
  const [editing, setEditing] = useState<Upstream | null | undefined>(undefined);
  const [secret, setSecret] = useState<{ title: string; value: string } | null>(null);

  const refresh = () => {
    void queryClient.invalidateQueries({ queryKey: ["upstreams"] });
    void queryClient.invalidateQueries({ queryKey: ["keys"] });
  };

  const revealSecret = (value: string, title: string) => setSecret({ title, value });

  if (upstreams.isPending) return <Spinner />;
  if (upstreams.isError) return <ErrorText error={upstreams.error} />;

  const list = upstreams.data.upstreams;
  const keyList = keys.data?.keys ?? [];

  return (
    <div className="settings-panel active">
      {secret && (
        <SecretPanel
          title={secret.title}
          value={secret.value}
          onClose={() => setSecret(null)}
        />
      )}

      <section className="settings-section">
        <div className="section-head">
          <div>
            <h2>上游</h2>
            <p>网关把请求原样转发到选定的上游；每个网关密钥固定绑定一个上游。</p>
          </div>
          <Button onClick={() => setEditing(null)}>添加上游</Button>
        </div>

        {editing !== undefined && (
          <UpstreamEditor
            key={editing?.id ?? "new"}
            upstream={editing}
            onDone={() => {
              setEditing(undefined);
              refresh();
            }}
          />
        )}

        {list.length === 0 ? (
          <div className="card empty-setting">
            <p>还没有上游。添加一个 OpenAI 兼容服务即可开始转发。</p>
          </div>
        ) : (
          <div className="upstream-list">
            {list.map((upstream) => (
              <UpstreamCard
                key={upstream.id}
                upstream={upstream}
                onEdit={() => setEditing(upstream)}
                onSecret={revealSecret}
              />
            ))}
          </div>
        )}
      </section>

      <section className="settings-section">
        <div className="section-head">
          <div>
            <h2>网关密钥</h2>
            <p>客户端使用这些密钥访问本机网关；轮换后旧密钥立即失效。</p>
          </div>
        </div>

        {list.length === 0 ? (
          <div className="card empty-setting">
            <p>先添加上游，才能创建网关密钥。</p>
          </div>
        ) : (
          <>
            <CreateKey
              upstreams={list}
              onCreated={(created) =>
                revealSecret(created.key, `${created.name} 的网关密钥`)
              }
            />
            <div className="card table-card">
              {keys.isPending ? (
                <Spinner />
              ) : keyList.length === 0 ? (
                <div className="log-empty">还没有网关密钥</div>
              ) : (
                <div className="table-scroll">
                  <table className="data-table">
                    <thead>
                      <tr>
                        <th>名称</th>
                        <th>密钥</th>
                        <th>绑定上游</th>
                        <th>状态</th>
                        <th>最近使用</th>
                        <th>操作</th>
                      </tr>
                    </thead>
                    <tbody>
                      {keyList.map((localKey) => (
                        <KeyRow
                          key={localKey.id}
                          localKey={localKey}
                          upstreams={list}
                          onSecret={revealSecret}
                        />
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </div>
          </>
        )}
      </section>

      <p className="settings-hint">
        客户端把请求发往 <code>{window.location.origin}/v1</code>，并带上这里创建的
        Bearer 密钥；网关按密钥绑定关系选择上游。
      </p>
    </div>
  );
}
