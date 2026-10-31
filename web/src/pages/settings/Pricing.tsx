import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useState, type ReactElement } from "react";
import { ApiError, api } from "../../api/client";
import type {
  ModelCatalog,
  PricingModel,
  PricingWindow,
  Upstream,
} from "../../api/types";
import { Badge, Button, ErrorText, Input, Select, Spinner, useToast } from "../../components/ui";
import { formatTime } from "../../lib/format";

const DAYS = ["mon", "tue", "wed", "thu", "fri", "sat", "sun"] as const;
const DAY_LABELS: Record<string, string> = {
  mon: "一",
  tue: "二",
  wed: "三",
  thu: "四",
  fri: "五",
  sat: "六",
  sun: "日",
};

interface RateDraft {
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  reasoning: string;
}

interface WindowDraft extends RateDraft {
  key: number;
  label: string;
  days: string[];
  start: string;
  end: string;
}

interface ModelDraft extends RateDraft {
  key: number;
  name: string;
  windows: WindowDraft[];
  open: boolean;
}

let nextKey = 1;

function blankRate(): RateDraft {
  return { input: "", output: "", cacheRead: "0", cacheWrite: "0", reasoning: "" };
}

function rateText(value: number | null | undefined): string {
  return value == null ? "" : String(value);
}

function fromWindow(window: PricingWindow): WindowDraft {
  return {
    key: nextKey++,
    label: window.label,
    days: [...window.days],
    start: window.start,
    end: window.end,
    input: rateText(window.input),
    output: rateText(window.output),
    cacheRead: rateText(window.cacheRead),
    cacheWrite: rateText(window.cacheWrite),
    reasoning: rateText(window.reasoning),
  };
}

function fromModel(model: PricingModel): ModelDraft {
  return {
    key: nextKey++,
    name: model.name,
    input: rateText(model.input),
    output: rateText(model.output),
    cacheRead: rateText(model.cacheRead),
    cacheWrite: rateText(model.cacheWrite),
    reasoning: rateText(model.reasoning),
    open: false,
    windows: model.windows.map(fromWindow),
  };
}

function blankModel(): ModelDraft {
  return {
    key: nextKey++,
    name: "",
    ...blankRate(),
    open: false,
    windows: [],
  };
}

function blankWindow(): WindowDraft {
  return {
    key: nextKey++,
    label: "高峰",
    days: ["mon", "tue", "wed", "thu", "fri"],
    start: "00:00",
    end: "08:00",
    input: "",
    output: "",
    cacheRead: "",
    cacheWrite: "",
    reasoning: "",
  };
}

/** parseRate accepts a non-negative finite rate; blank means undefined. */
function parseRate(raw: string): number | undefined {
  const value = Number(raw.trim());
  return raw.trim() !== "" && Number.isFinite(value) && value >= 0 ? value : undefined;
}

interface OptionalRate {
  ok: boolean;
  value: number | null;
}

function parseOptionalRate(raw: string): OptionalRate {
  if (raw.trim() === "") return { ok: true, value: null };
  const value = parseRate(raw);
  return value === undefined ? { ok: false, value: null } : { ok: true, value };
}

function pricingError(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    const first = Object.entries(error.fields)[0];
    if (first) return `${first[0]}：${first[1]}`;
    return error.message;
  }
  return error instanceof Error ? error.message : fallback;
}

function buildModel(draft: ModelDraft): PricingModel | null {
  const input = parseRate(draft.input);
  const output = parseRate(draft.output);
  if (draft.name.trim() === "" || input === undefined || output === undefined) return null;
  const cacheRead = parseOptionalRate(draft.cacheRead);
  const cacheWrite = parseOptionalRate(draft.cacheWrite);
  const reasoning = parseOptionalRate(draft.reasoning);
  if (!cacheRead.ok || !cacheWrite.ok || !reasoning.ok) return null;
  const windows: PricingWindow[] = [];
  for (const window of draft.windows) {
    const rates = [
      parseOptionalRate(window.input),
      parseOptionalRate(window.output),
      parseOptionalRate(window.cacheRead),
      parseOptionalRate(window.cacheWrite),
      parseOptionalRate(window.reasoning),
    ];
    if (rates.some((rate) => !rate.ok)) return null;
    windows.push({
      label: window.label.trim(),
      days: window.days,
      start: window.start,
      end: window.end,
      input: rates[0].value,
      output: rates[1].value,
      cacheRead: rates[2].value,
      cacheWrite: rates[3].value,
      reasoning: rates[4].value,
    });
  }
  return {
    name: draft.name.trim(),
    input,
    output,
    cacheRead: cacheRead.value ?? 0,
    cacheWrite: cacheWrite.value ?? 0,
    reasoning: reasoning.value,
    windows,
  };
}

function windowInvalid(window: WindowDraft): boolean {
  return (
    window.label.trim() === "" ||
    window.days.length === 0 ||
    !/^([01]\d|2[0-3]):[0-5]\d$/.test(window.start) ||
    !/^([01]\d|2[0-3]):[0-5]\d$/.test(window.end) ||
    window.start === window.end
  );
}

function RateInputs({
  draft,
  onChange,
  optional = false,
}: {
  draft: RateDraft;
  onChange: (patch: Partial<RateDraft>) => void;
  optional?: boolean;
}): ReactElement {
  return (
    <div className="rate-grid">
      <label>
        <span>输入</span>
        <Input
          inputMode="decimal"
          value={draft.input}
          placeholder={optional ? "继承" : "0"}
          onChange={(event) => onChange({ input: event.target.value })}
        />
      </label>
      <label>
        <span>输出</span>
        <Input
          inputMode="decimal"
          value={draft.output}
          placeholder={optional ? "继承" : "0"}
          onChange={(event) => onChange({ output: event.target.value })}
        />
      </label>
      <label>
        <span>缓存读</span>
        <Input
          inputMode="decimal"
          value={draft.cacheRead}
          placeholder={optional ? "继承" : "0"}
          onChange={(event) => onChange({ cacheRead: event.target.value })}
        />
      </label>
      <label>
        <span>缓存写</span>
        <Input
          inputMode="decimal"
          value={draft.cacheWrite}
          placeholder={optional ? "继承" : "0"}
          onChange={(event) => onChange({ cacheWrite: event.target.value })}
        />
      </label>
      <label>
        <span>推理</span>
        <Input
          inputMode="decimal"
          value={draft.reasoning}
          placeholder={optional ? "继承" : "同输出"}
          onChange={(event) => onChange({ reasoning: event.target.value })}
        />
      </label>
    </div>
  );
}

function ModelEditor({
  draft,
  onChange,
  onRemove,
}: {
  draft: ModelDraft;
  onChange: (next: ModelDraft) => void;
  onRemove: () => void;
}): ReactElement {
  const patchWindow = (key: number, patch: Partial<WindowDraft>) => {
    onChange({
      ...draft,
      windows: draft.windows.map((window) =>
        window.key === key ? { ...window, ...patch } : window,
      ),
    });
  };

  return (
    <div className="price-model">
      <div className="price-model-head">
        <Input
          className="model-name-input"
          value={draft.name}
          placeholder="模型名称"
          onChange={(event) => onChange({ ...draft, name: event.target.value })}
        />
        <Button variant="ghost" onClick={onRemove}>
          删除模型
        </Button>
      </div>
      <RateInputs
        draft={draft}
        onChange={(patch) => onChange({ ...draft, ...patch })}
      />
      <div className="price-model-foot">
        <button
          type="button"
          className="ghost-btn"
          onClick={() => onChange({ ...draft, open: !draft.open })}
        >
          {draft.open ? "收起时段" : `时段价格（${draft.windows.length}）`}
        </button>
        <span className="muted">单位：USD / 1M tokens</span>
      </div>

      {draft.open && (
        <div className="window-list">
          {draft.windows.length === 0 && <p className="muted">没有时段覆盖，使用基础价格。</p>}
          {draft.windows.map((window, index) => (
            <div className={`window-card${windowInvalid(window) ? " invalid" : ""}`} key={window.key}>
              <div className="window-head">
                <Input
                  value={window.label}
                  placeholder="时段名称"
                  onChange={(event) => patchWindow(window.key, { label: event.target.value })}
                />
                <button
                  type="button"
                  className="ghost-btn"
                  disabled={index === 0}
                  aria-label="上移时段"
                  onClick={() => {
                    const windows = [...draft.windows];
                    [windows[index - 1], windows[index]] = [windows[index], windows[index - 1]];
                    onChange({ ...draft, windows });
                  }}
                >
                  ↑
                </button>
                <button
                  type="button"
                  className="ghost-btn"
                  disabled={index === draft.windows.length - 1}
                  aria-label="下移时段"
                  onClick={() => {
                    const windows = [...draft.windows];
                    [windows[index + 1], windows[index]] = [windows[index], windows[index + 1]];
                    onChange({ ...draft, windows });
                  }}
                >
                  ↓
                </button>
                <button
                  type="button"
                  className="ghost-btn danger-text"
                  onClick={() =>
                    onChange({
                      ...draft,
                      windows: draft.windows.filter((item) => item.key !== window.key),
                    })
                  }
                >
                  移除
                </button>
              </div>
              <div className="day-picks" role="group" aria-label="生效星期">
                {DAYS.map((day) => {
                  const active = window.days.includes(day);
                  return (
                    <button
                      key={day}
                      type="button"
                      className={active ? "on" : ""}
                      aria-pressed={active}
                      onClick={() =>
                        patchWindow(window.key, {
                          days: active
                            ? window.days.filter((item) => item !== day)
                            : [...window.days, day],
                        })
                      }
                    >
                      {DAY_LABELS[day]}
                    </button>
                  );
                })}
                <span className="time-range">
                  <Input
                    type="time"
                    value={window.start}
                    aria-label="开始时间"
                    onChange={(event) => patchWindow(window.key, { start: event.target.value })}
                  />
                  <span>至</span>
                  <Input
                    type="time"
                    value={window.end}
                    aria-label="结束时间"
                    onChange={(event) => patchWindow(window.key, { end: event.target.value })}
                  />
                </span>
              </div>
              <RateInputs
                draft={window}
                optional
                onChange={(patch) => patchWindow(window.key, patch)}
              />
            </div>
          ))}
          <Button
            variant="secondary"
            onClick={() => onChange({ ...draft, windows: [...draft.windows, blankWindow()] })}
          >
            添加时段
          </Button>
        </div>
      )}
    </div>
  );
}

function PricingEditor({
  upstream,
  models,
  timezone,
}: {
  upstream: Upstream;
  models: PricingModel[];
  timezone: string;
}): ReactElement {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [drafts, setDrafts] = useState<ModelDraft[]>(() => models.map(fromModel));
  const [filter, setFilter] = useState("");
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);

  const catalogQuery = useQuery({
    queryKey: ["catalog", upstream.id],
    queryFn: () => api.getCatalog(upstream.id),
  });

  useEffect(() => {
    if (catalogQuery.data) setCatalog(catalogQuery.data);
  }, [catalogQuery.data]);

  const refreshCatalog = useMutation({
    mutationFn: () => api.getCatalog(upstream.id, true),
    onSuccess: (body) => {
      setCatalog(body);
      toast("模型目录已刷新", "success");
    },
    onError: (error) => toast(pricingError(error, "刷新模型目录失败"), "error"),
  });

  const candidates = useMemo(() => {
    const configured = new Set(drafts.map((model) => model.name.trim()).filter(Boolean));
    const names = new Map<string, boolean>();
    for (const name of catalog?.configured ?? []) {
      if (!configured.has(name)) names.set(name, false);
    }
    for (const model of catalog?.upstream ?? []) {
      if (!configured.has(model.id)) names.set(model.id, true);
    }
    for (const observed of catalog?.observed ?? []) {
      if (!configured.has(observed.model)) names.set(observed.model, false);
    }
    return [...names.entries()].map(([name, upstreamDiscovered]) => ({
      name,
      upstreamDiscovered,
    }));
  }, [catalog, drafts]);

  const importMutation = useMutation({
    mutationFn: async (names: string[]) => {
      const created: string[] = [];
      for (let index = 0; index < names.length; index += 100) {
        const result = await api.importPricing(upstream.id, names.slice(index, index + 100));
        created.push(...result.created);
      }
      return created;
    },
    onSuccess: (created) => {
      setDrafts((current) => {
        const existing = new Set(current.map((model) => model.name.trim()));
        return [
          ...current,
          ...created
            .filter((name) => !existing.has(name))
            .map((name) => ({ ...blankModel(), name })),
        ];
      });
      void queryClient.invalidateQueries({ queryKey: ["pricing-model-names"] });
      toast(`已导入 ${created.length} 个模型，请补全价格`, "success");
    },
    onError: (error) => toast(pricingError(error, "导入失败"), "error"),
  });

  const save = useMutation({
    mutationFn: () => {
      const payload = drafts.map(buildModel);
      if (payload.some((model) => model === null)) {
        throw new Error("模型名称必填，价格必须是非负数；基础输入/输出价不能留空");
      }
      if (drafts.some((model) => model.windows.some(windowInvalid))) {
        throw new Error("时段名称、星期与起止时间不完整，且起止不能相同");
      }
      return api.putPricing(
        upstream.id,
        payload.filter((model): model is PricingModel => model !== null),
      );
    },
    onSuccess: (body) => {
      queryClient.setQueryData(["pricing", upstream.id], body);
      setDrafts(body.models.map(fromModel));
      void queryClient.invalidateQueries({ queryKey: ["pricing-model-names"] });
      toast("价格已保存", "success");
    },
    onError: (error) => toast(pricingError(error, "保存价格失败"), "error"),
  });

  const visible = drafts.filter((model) =>
    filter.trim() === "" ||
    model.name.toLowerCase().includes(filter.trim().toLowerCase()),
  );

  return (
    <>
      <div className="pricing-toolbar">
        <Input
          value={filter}
          placeholder="筛选模型"
          onChange={(event) => setFilter(event.target.value)}
        />
        <span className="muted">
          时区 {timezone} · 时段按本地时间匹配
        </span>
        <Button variant="secondary" onClick={() => setDrafts([...drafts, blankModel()])}>
          添加模型
        </Button>
        <Button disabled={save.isPending} onClick={() => save.mutate()}>
          {save.isPending ? "保存中…" : "保存价格"}
        </Button>
      </div>

      <div className="pricing-grid">
        <div className="card pricing-list">
          {visible.length === 0 ? (
            <div className="log-empty">
              {drafts.length === 0 ? "还没有价格模型" : "没有匹配的模型"}
            </div>
          ) : (
            visible.map((draft) => (
              <ModelEditor
                key={draft.key}
                draft={draft}
                onChange={(next) =>
                  setDrafts((current) =>
                    current.map((model) => (model.key === draft.key ? next : model)),
                  )
                }
                onRemove={() =>
                  setDrafts((current) => current.filter((model) => model.key !== draft.key))
                }
              />
            ))
          )}
        </div>

        <aside className="card catalog-card">
          <div className="card-head">
            <div>
              <div className="card-title">模型目录</div>
              <div className="card-sub">
                {catalog?.fetchedAt
                  ? `拉取于 ${formatTime(catalog.fetchedAt)}`
                  : "尚未拉取"}
              </div>
            </div>
            <Button
              variant="secondary"
              disabled={refreshCatalog.isPending}
              onClick={() => refreshCatalog.mutate()}
            >
              {refreshCatalog.isPending ? "刷新中…" : "刷新"}
            </Button>
          </div>
          <div className="catalog-body">
            {catalog?.upstreamError && (
              <p className="form-error">{catalog.upstreamError}</p>
            )}
            <div className="catalog-actions">
              <Button
                disabled={importMutation.isPending || candidates.length === 0}
                onClick={() => importMutation.mutate(candidates.map((item) => item.name))}
              >
                {importMutation.isPending
                  ? "导入中…"
                  : `导入未定价模型（${candidates.length}）`}
              </Button>
            </div>
            <div className="candidate-list">
              {candidates.length === 0 ? (
                <p className="muted">目录中的模型都已加入价格表。</p>
              ) : (
                candidates.slice(0, 80).map((candidate) => (
                  <span className="chip" key={candidate.name}>
                    {candidate.name}
                    {candidate.upstreamDiscovered && <Badge tone="info">上游</Badge>}
                  </span>
                ))
              )}
            </div>
            <p className="settings-note">
              导入的模型按 0 价格创建，请在左侧补全价格。预设价格快照：
              {catalog?.presetSnapshot || "—"}
            </p>
          </div>
        </aside>
      </div>
    </>
  );
}

/** Pricing selects one upstream and edits its complete local price list. */
export function Pricing(): ReactElement {
  const upstreams = useQuery({ queryKey: ["upstreams"], queryFn: api.getUpstreams });
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  const [selected, setSelected] = useState("");

  const upstreamId =
    selected || upstreams.data?.upstreams[0]?.id || "";
  const upstream = upstreams.data?.upstreams.find((item) => item.id === upstreamId);
  const pricing = useQuery({
    queryKey: ["pricing", upstreamId],
    enabled: upstreamId !== "",
    queryFn: () => api.getPricing(upstreamId),
  });

  if (upstreams.isPending) return <Spinner />;
  if (upstreams.isError) return <ErrorText error={upstreams.error} />;
  if (upstreams.data.upstreams.length === 0) {
    return (
      <div className="settings-panel active">
        <div className="card empty-setting">
          <p>还没有上游。请先在「上游与密钥」中添加上游，再来配置价格。</p>
        </div>
      </div>
    );
  }

  return (
    <div className="settings-panel active">
      <div className="settings-toolbar">
        <label className="filter">
          <span>上游</span>
          <Select value={upstreamId} onChange={(event) => setSelected(event.target.value)}>
            {upstreams.data.upstreams.map((item) => (
              <option key={item.id} value={item.id}>
                {item.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="filter">
          <span>模型数</span>
          <span className="toolbar-value">{pricing.data?.models.length ?? "—"}</span>
        </label>
      </div>

      {pricing.isPending && <Spinner />}
      {pricing.isError && <ErrorText error={pricing.error} />}
      {upstream && pricing.data && (
        <PricingEditor
          key={upstream.id}
          upstream={upstream}
          models={pricing.data.models}
          timezone={bootstrap.data?.timezone ?? "UTC"}
        />
      )}
    </div>
  );
}
