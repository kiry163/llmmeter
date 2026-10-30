import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError, api } from "../../api/client";
import type {
  CatalogObserved,
  CatalogModel,
  ImportedModel,
  Model,
  ModelCatalog,
  ModelInput,
  Window,
  WindowInput,
} from "../../api/types";
import { ErrorBanner } from "../../components/ErrorBanner";
import { Button, Field, Input, Spinner, useToast, ErrorText } from "../../components/ui";
import { useTimezone } from "../../hooks/useTimezone";
import { formatTime } from "../../lib/format";
import {
  WEEKDAY_LABELS,
  describeWindow,
  formatMinute,
  isZeroPriced,
  parseRateInput,
  parseTimeInput,
  validateModel,
  validateWindows,
} from "../../lib/pricing";
import type { FormErrors } from "../../lib/pricing";

/** FIELD_LABELS turns API validation keys into visible field names. */
const FIELD_LABELS: Record<string, string> = {
  name: "模型名称",
  inputPerMTok: "输入价",
  outputPerMTok: "输出价",
  cacheReadPerMTok: "缓存读价",
  cacheWritePerMTok: "缓存写价",
  reasoningPerMTok: "推理价",
  windows: "时段窗口",
  names: "模型名单",
};

const SOURCE_LABELS = {
  configured: "已配置",
  upstream: "上游",
  observed: "已观测",
} as const;

/** IMPORT_BATCH_SIZE is the API's per-request import cap. */
const IMPORT_BATCH_SIZE = 100;

interface CatalogRow {
  name: string;
  configured: Model | null;
  upstream: CatalogModel | null;
  observed: CatalogObserved | null;
}

/** mergeRows unions configured, upstream and observed models by name. */
function mergeRows(models: readonly Model[], catalog: ModelCatalog | undefined): CatalogRow[] {
  const rows = new Map<string, CatalogRow>();
  const ensure = (name: string): CatalogRow => {
    const existing = rows.get(name);
    if (existing) return existing;
    const row: CatalogRow = { name, configured: null, upstream: null, observed: null };
    rows.set(name, row);
    return row;
  };
  for (const model of models) ensure(model.name).configured = model;
  for (const upstream of catalog?.upstream ?? []) ensure(upstream.id).upstream = upstream;
  for (const observed of catalog?.observed ?? []) ensure(observed.model).observed = observed;
  return [...rows.values()].sort((left, right) => left.name.localeCompare(right.name));
}

interface WindowDraft {
  key: number;
  label: string;
  daysMask: number;
  start: string;
  end: string;
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  reasoning: string;
}

interface FormState {
  name: string;
  input: string;
  output: string;
  cacheRead: string;
  cacheWrite: string;
  reasoning: string;
  windows: WindowDraft[];
}

const EMPTY_FORM: FormState = {
  name: "",
  input: "",
  output: "",
  cacheRead: "",
  cacheWrite: "",
  reasoning: "",
  windows: [],
};

let nextWindowKey = 1;

/** WEEKDAY_MASK starts new windows on Monday–Friday. */
const WEEKDAY_MASK = 0b0011111;

function blankWindow(): WindowDraft {
  return {
    key: nextWindowKey++,
    label: "",
    daysMask: WEEKDAY_MASK,
    start: "00:00",
    end: "08:00",
    input: "",
    output: "",
    cacheRead: "",
    cacheWrite: "",
    reasoning: "",
  };
}

function rateText(value: number | null): string {
  return value == null ? "" : String(value);
}

function draftFromWindow(window: Window): WindowDraft {
  return {
    key: nextWindowKey++,
    label: window.label,
    daysMask: window.daysMask,
    start: formatMinute(window.startMinute),
    end: formatMinute(window.endMinute),
    input: rateText(window.inputPerMTok),
    output: rateText(window.outputPerMTok),
    cacheRead: rateText(window.cacheReadPerMTok),
    cacheWrite: rateText(window.cacheWritePerMTok),
    reasoning: rateText(window.reasoningPerMTok),
  };
}

function formFromModel(model: Model): FormState {
  return {
    name: model.name,
    input: String(model.inputPerMTok),
    output: String(model.outputPerMTok),
    cacheRead: String(model.cacheReadPerMTok),
    cacheWrite: String(model.cacheWritePerMTok),
    reasoning: rateText(model.reasoningPerMTok),
    windows: model.windows.map(draftFromWindow),
  };
}

function requiredRate(raw: string): number {
  return parseRateInput(raw, { required: true }) ?? Number.NaN;
}

function optionalRate(raw: string): number {
  return parseRateInput(raw, { required: false }) ?? Number.NaN;
}

/** windowRate keeps blank overrides as inherit (null) and flags bad numbers. */
function windowRate(raw: string): number | null {
  if (raw.trim() === "") return null;
  return parseRateInput(raw, { required: false }) ?? Number.NaN;
}

function buildPayload(form: FormState): { model: ModelInput; windows: WindowInput[] } {
  return {
    model: {
      name: form.name.trim(),
      inputPerMTok: requiredRate(form.input),
      outputPerMTok: requiredRate(form.output),
      cacheReadPerMTok: requiredRate(form.cacheRead),
      cacheWritePerMTok: optionalRate(form.cacheWrite),
      reasoningPerMTok: form.reasoning.trim() === "" ? null : optionalRate(form.reasoning),
    },
    windows: form.windows.map((draft) => ({
      label: draft.label.trim(),
      daysMask: draft.daysMask,
      startMinute: parseTimeInput(draft.start),
      endMinute: parseTimeInput(draft.end),
      inputPerMTok: windowRate(draft.input),
      outputPerMTok: windowRate(draft.output),
      cacheReadPerMTok: windowRate(draft.cacheRead),
      cacheWritePerMTok: windowRate(draft.cacheWrite),
      reasoningPerMTok: windowRate(draft.reasoning),
    })),
  };
}

/** baseRateErrors describes required blanks and bad optional rate text. */
function baseRateErrors(form: FormState): FormErrors {
  const errors: FormErrors = {};
  const required = [
    ["input", "inputPerMTok"],
    ["output", "outputPerMTok"],
    ["cacheRead", "cacheReadPerMTok"],
  ] as const;
  for (const [field, key] of required) {
    if (form[field].trim() === "") errors[key] = "请填写";
    else if (parseRateInput(form[field], { required: true }) == null) {
      errors[key] = "需为不小于 0 的数字";
    }
  }
  const optional = [
    ["cacheWrite", "cacheWritePerMTok"],
    ["reasoning", "reasoningPerMTok"],
  ] as const;
  for (const [field, key] of optional) {
    if (form[field].trim() !== "" && parseRateInput(form[field], { required: false }) == null) {
      errors[key] = "需为不小于 0 的数字";
    }
  }
  return errors;
}

/** displayRate renders a per-million-token price compactly. */
function displayRate(value: number | null): string {
  if (value == null) return "—";
  if (value === 0) return "$0";
  return `$${value < 1 ? value.toFixed(3) : value.toFixed(2)}`;
}

function windowSummary(model: Model): string {
  return model.windows.length > 0 ? `${model.windows.length} 个峰谷窗口` : "基础费率";
}

function sourceBadges(row: CatalogRow): ReactNode {
  return (
    <>
      {row.configured && <span className="source configured">{SOURCE_LABELS.configured}</span>}
      {row.upstream && <span className="source upstream">{SOURCE_LABELS.upstream}</span>}
      {row.observed && (
        <span
          className="source observed"
          title={`日志观测到 ${row.observed.requests} 次请求`}
        >
          {SOURCE_LABELS.observed}
        </span>
      )}
    </>
  );
}

function statusPill(row: CatalogRow): ReactNode {
  if (row.configured == null) return <span className="usage muted">未配置</span>;
  if (isZeroPriced(row.configured)) return <span className="usage warn">0 价（待填）</span>;
  return <span className="usage ok">已定价</span>;
}

function WindowEditor({
  index,
  draft,
  errors,
  canMoveUp,
  canMoveDown,
  onChange,
  onRemove,
  onMove,
}: {
  index: number;
  draft: WindowDraft;
  errors: FormErrors;
  canMoveUp: boolean;
  canMoveDown: boolean;
  onChange: (patch: Partial<WindowDraft>) => void;
  onRemove: () => void;
  onMove: (delta: number) => void;
}) {
  const prefix = `${index}.`;
  return (
    <div className="window-box">
      <div className="window-row">
        <span>
          <b>窗口 {index + 1}</b>
          {draft.label.trim() !== "" ? ` · ${draft.label}` : ""}
        </span>
        <span className="pricing-actions">
          <Button variant="secondary" disabled={!canMoveUp} onClick={() => onMove(-1)}>
            上移
          </Button>
          <Button variant="secondary" disabled={!canMoveDown} onClick={() => onMove(1)}>
            下移
          </Button>
          <Button variant="secondary" onClick={onRemove}>
            移除
          </Button>
        </span>
      </div>

      <div className="model-grid">
        <div className="span-2">
          <Field label="窗口名称" error={errors[`${prefix}label`]}>
            <Input
              value={draft.label}
              placeholder="例如 高峰时段"
              onChange={(event) => onChange({ label: event.target.value })}
            />
          </Field>
        </div>
        <div className="span-2">
          <span className="form-field">
            <span>生效星期</span>
            <span className="day-chips" role="group" aria-label="生效星期">
              {WEEKDAY_LABELS.map((label, dayIndex) => {
                const selected = (draft.daysMask & (1 << dayIndex)) !== 0;
                return (
                  <button
                    key={label}
                    type="button"
                    className={`day-chip${selected ? " on" : ""}`}
                    aria-pressed={selected}
                    onClick={() => onChange({ daysMask: draft.daysMask ^ (1 << dayIndex) })}
                  >
                    {label}
                  </button>
                );
              })}
            </span>
            {errors[`${prefix}daysMask`] && (
              <small className="form-error" role="alert">
                {errors[`${prefix}daysMask`]}
              </small>
            )}
          </span>
        </div>
        <Field label="开始" error={errors[`${prefix}startMinute`]}>
          <Input
            type="time"
            value={draft.start}
            onChange={(event) => onChange({ start: event.target.value })}
          />
        </Field>
        <Field label="结束" error={errors[`${prefix}endMinute`]}>
          <Input
            type="time"
            value={draft.end}
            onChange={(event) => onChange({ end: event.target.value })}
          />
        </Field>
      </div>

      <div className="win-form-grid">
        <Field label="输入价" hint="留空继承" error={errors[`${prefix}inputPerMTok`]}>
          <Input
            inputMode="decimal"
            value={draft.input}
            placeholder="继承"
            onChange={(event) => onChange({ input: event.target.value })}
          />
        </Field>
        <Field label="输出价" hint="留空继承" error={errors[`${prefix}outputPerMTok`]}>
          <Input
            inputMode="decimal"
            value={draft.output}
            placeholder="继承"
            onChange={(event) => onChange({ output: event.target.value })}
          />
        </Field>
        <Field label="缓存读价" hint="留空继承" error={errors[`${prefix}cacheReadPerMTok`]}>
          <Input
            inputMode="decimal"
            value={draft.cacheRead}
            placeholder="继承"
            onChange={(event) => onChange({ cacheRead: event.target.value })}
          />
        </Field>
        <Field label="缓存写价" hint="留空继承" error={errors[`${prefix}cacheWritePerMTok`]}>
          <Input
            inputMode="decimal"
            value={draft.cacheWrite}
            placeholder="继承"
            onChange={(event) => onChange({ cacheWrite: event.target.value })}
          />
        </Field>
      </div>
      <div className="win-form-grid">
        <Field label="推理价" hint="留空继承输出" error={errors[`${prefix}reasoningPerMTok`]}>
          <Input
            inputMode="decimal"
            value={draft.reasoning}
            placeholder="继承"
            onChange={(event) => onChange({ reasoning: event.target.value })}
          />
        </Field>
      </div>
      <div className="pricing-rates">结束早于开始时按跨夜处理，星期按开始日计算。</div>
    </div>
  );
}

/**
 * importInBatches creates every name through /models/import, splitting the
 * list because the API rejects batches above its cap.
 */
async function importInBatches(names: string[]): Promise<{
  created: ImportedModel[];
  skipped: string[];
}> {
  const created: ImportedModel[] = [];
  const skipped: string[] = [];
  for (let start = 0; start < names.length; start += IMPORT_BATCH_SIZE) {
    const result = await api.importModels(names.slice(start, start + IMPORT_BATCH_SIZE));
    created.push(...result.created);
    skipped.push(...result.skipped);
  }
  return { created, skipped };
}

/**
 * PartialSaveError marks a model that was created or updated but whose window
 * replacement failed, so the editor can retry against the saved id.
 */
class PartialSaveError extends Error {
  readonly modelId: number;
  readonly detail: unknown;

  constructor(modelId: number, detail: unknown) {
    super(detail instanceof Error ? detail.message : String(detail));
    this.name = "PartialSaveError";
    this.modelId = modelId;
    this.detail = detail;
  }
}

/**
 * Pricing is the settings pricing subpage: it merges the configured, upstream
 * and observed model catalogs, imports unconfigured models with zero rates and
 * edits base prices plus time windows.
 */
export function Pricing() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const timeZone = useTimezone();
  const models = useQuery({ queryKey: ["models"], queryFn: api.getModels });
  const catalog = useQuery({
    queryKey: ["model-catalog"],
    queryFn: () => api.getModelCatalog(false),
  });

  const [search, setSearch] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [errors, setErrors] = useState<FormErrors>({});
  const [focusWindows, setFocusWindows] = useState(false);
  const [deletingId, setDeletingId] = useState<number | null>(null);
  const dialogRef = useRef<HTMLDivElement | null>(null);
  const windowsRef = useRef<HTMLDivElement | null>(null);

  const refresh = useMutation({
    mutationFn: () => api.getModelCatalog(true),
    onSuccess: (data) => {
      queryClient.setQueryData(["model-catalog"], data);
      if (data.upstreamError !== "") {
        toast("上游拉取失败，已保留已观测模型", "error");
      } else {
        toast("已刷新上游模型", "success");
      }
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "刷新失败", "error");
    },
  });

  const importModels = useMutation({
    mutationFn: importInBatches,
    onSuccess: (result) => {
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["model-catalog"] });
      const parts = [`补录 ${result.created.length} 个模型`];
      if (result.skipped.length > 0) parts.push(`跳过 ${result.skipped.length} 个`);
      toast(parts.join("，"), "success");
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "补录失败", "error");
    },
  });

  const save = useMutation({
    mutationFn: async ({
      model,
      windows,
    }: {
      model: ModelInput;
      windows: WindowInput[];
    }): Promise<number> => {
      if (editingId == null) {
        const created = await api.createModel(model);
        try {
          await api.putWindows(created.id, windows);
        } catch (detail) {
          throw new PartialSaveError(created.id, detail);
        }
        return created.id;
      }
      await api.updateModel(editingId, model);
      try {
        await api.putWindows(editingId, windows);
      } catch (detail) {
        throw new PartialSaveError(editingId, detail);
      }
      return editingId;
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["model-catalog"] });
      void queryClient.invalidateQueries({ queryKey: ["overview"] });
      void queryClient.invalidateQueries({ queryKey: ["analysis"] });
      toast("价格已保存", "success");
      setEditorOpen(false);
      setErrors({});
    },
    onError: (error) => {
      // A create or update may have landed before putWindows failed.
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["model-catalog"] });
      if (error instanceof PartialSaveError) {
        setEditingId(error.modelId);
        toast("模型已保存，但时段窗口保存失败，请重试保存", "error");
      }
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteModel(id),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["model-catalog"] });
      void queryClient.invalidateQueries({ queryKey: ["overview"] });
      void queryClient.invalidateQueries({ queryKey: ["analysis"] });
      setDeletingId(null);
      if (editingId === id) {
        setEditorOpen(false);
        setErrors({});
      }
      toast("已删除模型配置", "success");
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "删除失败", "error");
    },
  });

  useEffect(() => {
    if (!editorOpen) return;
    const previousOverflow = document.body.style.overflow;
    const previousFocus =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    document.body.style.overflow = "hidden";
    const node = dialogRef.current;
    const first = node?.querySelector<HTMLElement>(
      'input:not([disabled]), button:not([disabled]), select:not([disabled]), textarea:not([disabled])',
    );
    first?.focus();

    function onKey(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        setEditorOpen(false);
        setErrors({});
        return;
      }
      if (event.key !== "Tab" || node == null) return;
      const focusable = Array.from(
        node.querySelectorAll<HTMLElement>(
          'button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), a[href], [tabindex]:not([tabindex="-1"])',
        ),
      );
      if (focusable.length === 0) return;
      const firstEl = focusable[0];
      const lastEl = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === firstEl) {
        event.preventDefault();
        lastEl.focus();
      } else if (!event.shiftKey && document.activeElement === lastEl) {
        event.preventDefault();
        firstEl.focus();
      }
    }

    window.addEventListener("keydown", onKey, true);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", onKey, true);
      previousFocus?.focus();
    };
  }, [editorOpen]);

  useEffect(() => {
    if (!editorOpen || !focusWindows) return;
    const timer = window.setTimeout(() => {
      windowsRef.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
      windowsRef.current?.querySelector<HTMLElement>("input, button")?.focus();
    }, 60);
    return () => window.clearTimeout(timer);
  }, [editorOpen, focusWindows]);

  function openCreate(name = "") {
    save.reset();
    setEditingId(null);
    setForm({ ...EMPTY_FORM, name });
    setErrors({});
    setFocusWindows(false);
    setDeletingId(null);
    setEditorOpen(true);
  }

  function openEdit(model: Model, windowsOnly = false) {
    save.reset();
    setEditingId(model.id);
    setForm(formFromModel(model));
    setErrors({});
    setFocusWindows(windowsOnly);
    setDeletingId(null);
    setEditorOpen(true);
  }

  function closeEditor() {
    setEditorOpen(false);
    setErrors({});
    setFocusWindows(false);
  }

  function updateWindow(key: number, patch: Partial<WindowDraft>) {
    setForm((current) => ({
      ...current,
      windows: current.windows.map((window) =>
        window.key === key ? { ...window, ...patch } : window,
      ),
    }));
  }

  function moveWindow(index: number, delta: number) {
    setForm((current) => {
      const target = index + delta;
      if (target < 0 || target >= current.windows.length) return current;
      const windows = [...current.windows];
      const [moved] = windows.splice(index, 1);
      windows.splice(target, 0, moved);
      return { ...current, windows };
    });
  }

  function submit() {
    const payload = buildPayload(form);
    const nextErrors = {
      ...validateModel(payload.model),
      ...validateWindows(payload.windows),
      ...baseRateErrors(form),
    };
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    save.mutate(payload);
  }

  function importMissing() {
    if (unconfigured.length === 0) return;
    importModels.mutate(unconfigured.map((row) => row.name));
  }

  const rows = mergeRows(models.data?.models ?? [], catalog.data);
  const unconfigured = rows.filter((row) => row.configured == null);
  const query = search.trim().toLowerCase();
  const visible = query === "" ? rows : rows.filter((row) => row.name.toLowerCase().includes(query));
  const datalistNames = [
    ...new Set([
      ...(catalog.data?.upstream.map((model) => model.id) ?? []),
      ...(catalog.data?.observed.map((entry) => entry.model) ?? []),
    ]),
  ].sort();
  const saveError = save.error;
  const saveFields =
    saveError instanceof ApiError
      ? Object.entries(saveError.fields)
      : saveError instanceof PartialSaveError && saveError.detail instanceof ApiError
        ? Object.entries(saveError.detail.fields)
        : [];
  const pending = models.isPending || catalog.isPending;

  function renderActions(row: CatalogRow): ReactNode {
    if (row.configured == null) {
      return (
        <span className="pricing-actions">
          <Button variant="secondary" onClick={() => openCreate(row.name)}>
            配置
          </Button>
        </span>
      );
    }
    const model = row.configured;
    if (deletingId === model.id) {
      return (
        <span className="pricing-actions">
          <Button
            variant="danger"
            disabled={remove.isPending}
            onClick={() => remove.mutate(model.id)}
          >
            {remove.isPending ? "删除中…" : "确认"}
          </Button>
          <Button variant="secondary" onClick={() => setDeletingId(null)}>
            取消
          </Button>
        </span>
      );
    }
    return (
      <span className="pricing-actions">
        <Button variant="secondary" onClick={() => openEdit(model)}>
          编辑
        </Button>
        <Button variant="secondary" onClick={() => openEdit(model, true)}>
          窗口
        </Button>
        <Button variant="secondary" onClick={() => setDeletingId(model.id)}>
          删除
        </Button>
      </span>
    );
  }

  const hasErrors =
    models.isError ||
    catalog.isError ||
    refresh.isError ||
    importModels.isError ||
    (catalog.data?.upstreamError ?? "") !== "";

  return (
    <>
      <div className="settings-panel active">
        <article className="card pricing-card">
        <div className="pricing-head">
          <div>
            <div className="card-title">模型目录</div>
            <div className="card-sub">
              {catalog.data?.fetchedAt
                ? `上次拉取：${formatTime(catalog.data.fetchedAt, timeZone)}`
                : "尚未成功拉取上游模型"}
            </div>
          </div>
          <div className="pricing-tools">
            <input
              className="search-input"
              type="search"
              value={search}
              placeholder="搜索模型…"
              aria-label="搜索模型"
              autoComplete="off"
              onChange={(event) => setSearch(event.target.value)}
            />
            <Button
              variant="secondary"
              disabled={refresh.isPending || pending}
              onClick={() => refresh.mutate()}
            >
              {refresh.isPending ? "刷新中…" : "刷新上游模型"}
            </Button>
            <Button
              variant="secondary"
              disabled={importModels.isPending || unconfigured.length === 0}
              onClick={importMissing}
            >
              {importModels.isPending ? "补录中…" : `一键补录（${unconfigured.length}）`}
            </Button>
            <Button onClick={() => openCreate()}>添加模型</Button>
          </div>
        </div>

        {hasErrors && (
          <div className="settings-body" style={{ display: "grid", gap: 8, paddingBottom: 0 }}>
            {models.isError && (
              <ErrorBanner error={models.error} onRetry={() => void models.refetch()} />
            )}
            {catalog.isError && (
              <ErrorBanner error={catalog.error} onRetry={() => void catalog.refetch()} />
            )}
            {catalog.data?.upstreamError && (
              <ErrorBanner
                error={`上游模型拉取失败：${catalog.data.upstreamError}`}
                onRetry={() => refresh.mutate()}
              />
            )}
            {refresh.isError && (
              <ErrorBanner error={refresh.error} onRetry={() => refresh.mutate()} />
            )}
            {importModels.isError && <ErrorBanner error={importModels.error} />}
          </div>
        )}

        {pending ? (
          <div className="log-empty">
            <Spinner label="加载模型目录…" />
          </div>
        ) : rows.length === 0 ? (
          <div className="log-empty">模型目录为空，刷新上游目录或添加模型。</div>
        ) : visible.length === 0 ? (
          <div className="log-empty">没有匹配的模型</div>
        ) : (
          <>
            <div className="pricing-table-wrap">
              <table className="pricing-table">
                <thead>
                  <tr>
                    <th>模型</th>
                    <th>来源</th>
                    <th className="num">输入</th>
                    <th className="num">输出</th>
                    <th className="num">缓存读</th>
                    <th>状态</th>
                    <th className="num">操作</th>
                  </tr>
                </thead>
                <tbody>
                  {visible.map((row) => (
                    <tr key={row.name}>
                      <td>
                        <div className="pricing-name">{row.name}</div>
                        <div className="pricing-rates">
                          {row.configured ? (
                            <span
                              title={
                                row.configured.windows.length > 0
                                  ? row.configured.windows.map(describeWindow).join("\n")
                                  : "无时段窗口，使用基础费率"
                              }
                            >
                              {windowSummary(row.configured)}
                            </span>
                          ) : (
                            "未配置价格"
                          )}
                        </div>
                      </td>
                      <td>{sourceBadges(row)}</td>
                      <td className="num">
                        {row.configured ? displayRate(row.configured.inputPerMTok) : "—"}
                      </td>
                      <td className="num">
                        {row.configured ? displayRate(row.configured.outputPerMTok) : "—"}
                      </td>
                      <td className="num">
                        {row.configured ? displayRate(row.configured.cacheReadPerMTok) : "—"}
                      </td>
                      <td>{statusPill(row)}</td>
                      <td className="num">{renderActions(row)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className="pricing-cards">
              {visible.map((row) => (
                <div className="pricing-row" key={row.name}>
                  <div className="pricing-row-top">
                    <div>
                      <div className="pricing-name">{row.name}</div>
                      <div style={{ marginTop: 4 }}>{sourceBadges(row)}</div>
                    </div>
                    <div>{statusPill(row)}</div>
                  </div>
                  <div className="pricing-row-meta">
                    <span>
                      输入{" "}
                      <b>{row.configured ? displayRate(row.configured.inputPerMTok) : "—"}</b>
                    </span>
                    <span>
                      输出{" "}
                      <b>{row.configured ? displayRate(row.configured.outputPerMTok) : "—"}</b>
                    </span>
                    <span>
                      缓存读{" "}
                      <b>{row.configured ? displayRate(row.configured.cacheReadPerMTok) : "—"}</b>
                    </span>
                    <span>{row.configured ? windowSummary(row.configured) : "未配置价格"}</span>
                  </div>
                  <div className="pricing-row-actions">{renderActions(row)}</div>
                </div>
              ))}
            </div>
          </>
        )}
      </article>
      <p className="settings-hint">
        价格按每百万 tokens 计，缓存写价留空按 0 计；reasoning 留空继承 output。峰谷窗口内命中时覆盖基础费率。
      </p>
      </div>

      {editorOpen && (
        <>
          <div className="modal-overlay open" onClick={closeEditor} aria-hidden="true" />
          <div
            className="modal open"
            role="dialog"
            aria-modal="true"
            aria-labelledby="model-modal-title"
            ref={dialogRef}
          >
            <div className="modal-head">
              <div className="modal-title" id="model-modal-title">
                {editingId == null ? "添加模型" : "编辑模型"}
              </div>
              <button
                type="button"
                className="icon-btn"
                aria-label="关闭"
                onClick={closeEditor}
              >
                ✕
              </button>
            </div>
            <div className="modal-body">
              <form
                className="model-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  submit();
                }}
              >
                <div className="model-grid">
                  <div className="span-2">
                    <Field label="模型名称" hint="可从目录选择或手动填写" error={errors.name}>
                      <Input
                        list="model-catalog-names"
                        value={form.name}
                        placeholder="deepseek-chat"
                        autoComplete="off"
                        onChange={(event) => setForm({ ...form, name: event.target.value })}
                      />
                      <datalist id="model-catalog-names">
                        {datalistNames.map((name) => (
                          <option key={name} value={name} />
                        ))}
                      </datalist>
                    </Field>
                  </div>
                  <Field label="输入 $/M" error={errors.inputPerMTok}>
                    <Input
                      inputMode="decimal"
                      value={form.input}
                      placeholder="0"
                      onChange={(event) => setForm({ ...form, input: event.target.value })}
                    />
                  </Field>
                  <Field label="输出 $/M" error={errors.outputPerMTok}>
                    <Input
                      inputMode="decimal"
                      value={form.output}
                      placeholder="0"
                      onChange={(event) => setForm({ ...form, output: event.target.value })}
                    />
                  </Field>
                  <Field label="缓存读 $/M" error={errors.cacheReadPerMTok}>
                    <Input
                      inputMode="decimal"
                      value={form.cacheRead}
                      placeholder="0"
                      onChange={(event) => setForm({ ...form, cacheRead: event.target.value })}
                    />
                  </Field>
                  <Field label="缓存写 $/M（可选）" hint="留空按 0" error={errors.cacheWritePerMTok}>
                    <Input
                      inputMode="decimal"
                      value={form.cacheWrite}
                      placeholder="按 0"
                      onChange={(event) => setForm({ ...form, cacheWrite: event.target.value })}
                    />
                  </Field>
                  <div className="span-2">
                    <Field
                      label="Reasoning $/M（可选）"
                      hint="留空继承输出价"
                      error={errors.reasoningPerMTok}
                    >
                      <Input
                        inputMode="decimal"
                        value={form.reasoning}
                        placeholder="继承 output"
                        onChange={(event) => setForm({ ...form, reasoning: event.target.value })}
                      />
                    </Field>
                  </div>
                </div>

                <div className="window-box" ref={windowsRef}>
                  <div className="window-row">
                    <span>
                      <b>峰谷窗口</b>
                      {form.windows.length > 0 ? ` · ${form.windows.length} 个` : ""}
                    </span>
                    <Button
                      variant="secondary"
                      onClick={() =>
                        setForm((current) => ({
                          ...current,
                          windows: [...current.windows, blankWindow()],
                        }))
                      }
                    >
                      添加窗口
                    </Button>
                  </div>
                  {errors.windows && (
                    <p className="form-error" role="alert">
                      {errors.windows}
                    </p>
                  )}
                  {form.windows.length === 0 ? (
                    <div className="pricing-rates">暂无窗口，使用基础费率。</div>
                  ) : (
                    form.windows.map((draft, index) => (
                      <WindowEditor
                        key={draft.key}
                        index={index}
                        draft={draft}
                        errors={errors}
                        canMoveUp={index > 0}
                        canMoveDown={index < form.windows.length - 1}
                        onChange={(patch) => updateWindow(draft.key, patch)}
                        onRemove={() =>
                          setForm((current) => ({
                            ...current,
                            windows: current.windows.filter((window) => window.key !== draft.key),
                          }))
                        }
                        onMove={(delta) => moveWindow(index, delta)}
                      />
                    ))
                  )}
                </div>

                {save.isError && (
                  <div>
                    <ErrorText error={save.error} />
                    {saveFields.length > 0 && (
                      <ul className="form-error" style={{ margin: "4px 0 0", paddingLeft: 16 }}>
                        {saveFields.map(([field, message]) => (
                          <li key={field}>
                            {FIELD_LABELS[field] ?? field}：{message}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}

                <div className="modal-actions">
                  <Button variant="secondary" onClick={closeEditor}>
                    取消
                  </Button>
                  <Button type="submit" disabled={save.isPending}>
                    {save.isPending ? "保存中…" : "保存模型"}
                  </Button>
                </div>
              </form>
            </div>
          </div>
        </>
      )}
    </>
  );
}
