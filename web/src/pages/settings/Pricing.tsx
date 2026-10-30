import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
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
import { DataTable, type Column } from "../../components/DataTable";
import { EmptyState } from "../../components/EmptyState";
import { ErrorBanner } from "../../components/ErrorBanner";
import {
  Badge,
  Button,
  Card,
  Drawer,
  ErrorText,
  Field,
  Input,
  Spinner,
  useToast,
} from "../../components/ui";
import { useTimezone } from "../../hooks/useTimezone";
import { formatTime } from "../../lib/format";
import {
  WEEKDAY_LABELS,
  activeWindow,
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

function blankWindow(): WindowDraft {
  return {
    key: nextWindowKey++,
    label: "",
    daysMask: 127,
    start: "09:00",
    end: "12:00",
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
      reasoningPerMTok:
        form.reasoning.trim() === "" ? null : optionalRate(form.reasoning),
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

function rateText4(value: number | null): string {
  if (value == null) return "继承输出";
  return `$${value.toFixed(4)}/M`;
}

function currentLabel(model: Model, now: Date, timeZone?: string): ReactNode {
  const matched = activeWindow(model.windows, now, timeZone);
  if (matched) return <Badge tone="success">当前：{matched.label}</Badge>;
  return <Badge tone="neutral">基础价</Badge>;
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
    <div className="space-y-3 rounded-md border border-line p-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-xs font-semibold text-muted">窗口 {index + 1}</span>
        <div className="flex items-center gap-1">
          <Button variant="ghost" disabled={!canMoveUp} onClick={() => onMove(-1)}>
            上移
          </Button>
          <Button variant="ghost" disabled={!canMoveDown} onClick={() => onMove(1)}>
            下移
          </Button>
          <Button variant="ghost" className="text-danger" onClick={onRemove}>
            删除
          </Button>
        </div>
      </div>

      <Field label="名称" error={errors[`${prefix}label`]}>
        <Input
          value={draft.label}
          placeholder="例如 高峰"
          onChange={(event) => onChange({ label: event.target.value })}
        />
      </Field>

      <div>
        <span className="mb-1 block text-xs text-muted">生效星期</span>
        <div className="flex flex-wrap gap-1">
          {WEEKDAY_LABELS.map((label, dayIndex) => {
            const selected = (draft.daysMask & (1 << dayIndex)) !== 0;
            return (
              <button
                key={label}
                type="button"
                aria-pressed={selected}
                className={`rounded-full px-2.5 py-0.5 text-xs font-medium transition-colors ${
                  selected
                    ? "bg-accent text-white"
                    : "border border-line text-muted hover:border-accent"
                }`}
                onClick={() => onChange({ daysMask: draft.daysMask ^ (1 << dayIndex) })}
              >
                {label}
              </button>
            );
          })}
        </div>
        {errors[`${prefix}daysMask`] && (
          <span className="mt-1 block text-xs text-danger">{errors[`${prefix}daysMask`]}</span>
        )}
      </div>

      <div className="grid grid-cols-2 gap-3">
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
      <p className="text-[11px] text-muted">结束早于开始时按跨夜处理，星期按开始日计算。</p>

      <div className="grid grid-cols-2 gap-3">
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
        <Field label="推理价" hint="留空继承" error={errors[`${prefix}reasoningPerMTok`]}>
          <Input
            inputMode="decimal"
            value={draft.reasoning}
            placeholder="继承"
            onChange={(event) => onChange({ reasoning: event.target.value })}
          />
        </Field>
      </div>
    </div>
  );
}

/** IMPORT_BATCH_SIZE is the API's per-request import cap. */
const IMPORT_BATCH_SIZE = 100;

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
 * Pricing is the settings pricing subpage: it merges the configured, upstream
 * and observed model catalogs, imports unconfigured models with zero rates and
 * edits base prices plus time windows.
 */
export function Pricing() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const models = useQuery({ queryKey: ["models"], queryFn: api.getModels });
  const catalog = useQuery({
    queryKey: ["model-catalog"],
    queryFn: () => api.getModelCatalog(false),
  });
  const timeZone = useTimezone();

  const [editorOpen, setEditorOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [errors, setErrors] = useState<FormErrors>({});
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [confirmImport, setConfirmImport] = useState(false);
  const [now, setNow] = useState(() => new Date());

  const refresh = useMutation({
    mutationFn: () => api.getModelCatalog(true),
    onSuccess: (data) => {
      queryClient.setQueryData(["model-catalog"], data);
      if (data.upstreamError) {
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
      setConfirmImport(false);
      const parts = [`补录 ${result.created.length} 个模型`];
      if (result.skipped.length > 0) parts.push(`跳过 ${result.skipped.length} 个`);
      toast(parts.join("，"), "success");
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "补录失败", "error");
    },
  });

  const save = useMutation({
    mutationFn: async (payload: { model: ModelInput; windows: WindowInput[] }) => {
      if (editingId == null) {
        const created = await api.createModel(payload.model);
        await api.putWindows(created.id, payload.windows);
        return;
      }
      await api.updateModel(editingId, payload.model);
      await api.putWindows(editingId, payload.windows);
    },
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["model-catalog"] });
      void queryClient.invalidateQueries({ queryKey: ["overview"] });
      toast("已生效", "success");
      setEditorOpen(false);
      setErrors({});
    },
    onError: () => {
      // A create may have succeeded before putWindows failed; refresh anyway.
      void queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteModel(id),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["models"] });
      void queryClient.invalidateQueries({ queryKey: ["model-catalog"] });
      void queryClient.invalidateQueries({ queryKey: ["overview"] });
      setConfirmDeleteId(null);
      if (editingId === id) {
        setEditorOpen(false);
        setErrors({});
      }
      toast("已删除", "success");
    },
    onError: (error) => {
      toast(error instanceof Error ? error.message : "删除失败", "error");
    },
  });

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  function openCreate() {
    save.reset();
    setEditingId(null);
    setForm(EMPTY_FORM);
    setErrors({});
    setEditorOpen(true);
  }

  function openEdit(model: Model) {
    save.reset();
    setEditingId(model.id);
    setForm(formFromModel(model));
    setErrors({});
    setEditorOpen(true);
  }

  function closeEditor() {
    setEditorOpen(false);
    setErrors({});
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

  const rows = mergeRows(models.data?.models ?? [], catalog.data);
  const unconfigured = rows.filter((row) => row.configured == null);
  const datalistNames = [
    ...new Set([
      ...(catalog.data?.upstream.map((model) => model.id) ?? []),
      ...(catalog.data?.observed.map((entry) => entry.model) ?? []),
    ]),
  ].sort();
  const saveFields = save.error instanceof ApiError ? Object.entries(save.error.fields) : [];
  const pending = models.isPending || catalog.isPending;

  const columns: Column<CatalogRow>[] = [
    {
      key: "name",
      header: "模型",
      render: (row) => (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className={row.configured ? "font-medium" : "font-medium text-warning"}>
            {row.name}
          </span>
          {row.configured && isZeroPriced(row.configured) && (
            <Badge tone="warning">0 价（待填）</Badge>
          )}
        </div>
      ),
    },
    {
      key: "source",
      header: "来源",
      render: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.configured && <Badge tone="success">已配置</Badge>}
          {row.upstream && <Badge tone="info">上游</Badge>}
          {row.observed && (
            <span title={`已观测 ${row.observed.requests} 次`}>
              <Badge tone="neutral">已观测</Badge>
            </span>
          )}
          {!row.configured && <Badge tone="warning">未配置</Badge>}
        </div>
      ),
    },
    {
      key: "input",
      header: "输入",
      className: "text-right tabular-nums",
      render: (row) => (row.configured ? rateText4(row.configured.inputPerMTok) : "—"),
    },
    {
      key: "output",
      header: "输出",
      className: "text-right tabular-nums",
      render: (row) => (row.configured ? rateText4(row.configured.outputPerMTok) : "—"),
    },
    {
      key: "cacheRead",
      header: "缓存读",
      className: "text-right tabular-nums",
      render: (row) => (row.configured ? rateText4(row.configured.cacheReadPerMTok) : "—"),
    },
    {
      key: "cacheWrite",
      header: "缓存写",
      className: "text-right tabular-nums",
      render: (row) => (row.configured ? rateText4(row.configured.cacheWritePerMTok) : "—"),
    },
    {
      key: "reasoning",
      header: "推理",
      className: "text-right tabular-nums",
      render: (row) => (row.configured ? rateText4(row.configured.reasoningPerMTok) : "—"),
    },
    {
      key: "windows",
      header: "窗口",
      render: (row) =>
        row.configured ? (
          <span title={row.configured.windows.map(describeWindow).join("\n") || "无时段窗口"}>
            <Badge tone="info">{row.configured.windows.length} 个</Badge>
          </span>
        ) : (
          "—"
        ),
    },
    {
      key: "current",
      header: "当前",
      render: (row) => (row.configured ? currentLabel(row.configured, now, timeZone) : "—"),
    },
    {
      key: "actions",
      header: "操作",
      className: "text-right",
      render: (row) => {
        if (row.configured == null) {
          return (
            <Button
              variant="secondary"
              disabled={importModels.isPending}
              onClick={() => importModels.mutate([row.name])}
            >
              补录
            </Button>
          );
        }
        if (confirmDeleteId === row.configured.id) {
          return (
            <span className="inline-flex items-center gap-1">
              <span className="text-xs text-danger">确认删除？</span>
              <Button
                variant="danger"
                disabled={remove.isPending}
                onClick={() => remove.mutate(row.configured!.id)}
              >
                删除
              </Button>
              <Button variant="ghost" onClick={() => setConfirmDeleteId(null)}>
                取消
              </Button>
            </span>
          );
        }
        return (
          <span className="inline-flex items-center gap-1">
            <Button variant="secondary" onClick={() => openEdit(row.configured!)}>
              编辑
            </Button>
            <Button
              variant="ghost"
              onClick={() => setConfirmDeleteId(row.configured!.id)}
            >
              删除
            </Button>
          </span>
        );
      },
    },
  ];

  return (
    <div className="space-y-4">
      <Card
        title="模型目录"
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Button
              variant="secondary"
              disabled={refresh.isPending || catalog.isPending}
              onClick={() => refresh.mutate()}
            >
              {refresh.isPending ? "刷新中…" : "刷新上游模型"}
            </Button>
            <Button onClick={() => openCreate()}>新增模型</Button>
          </div>
        }
      >
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2 text-xs text-muted">
          <span>
            {catalog.data?.fetchedAt
              ? `上次拉取：${formatTime(catalog.data.fetchedAt, timeZone)}`
              : "尚未成功拉取上游模型"}
          </span>
          {unconfigured.length > 0 &&
            (confirmImport ? (
              <span className="inline-flex flex-wrap items-center gap-1">
                <span className="text-danger">
                  确认补录 {unconfigured.length} 个未配置模型（0 费率）？
                </span>
                <Button
                  variant="secondary"
                  disabled={importModels.isPending}
                  onClick={() => importModels.mutate(unconfigured.map((row) => row.name))}
                >
                  {importModels.isPending ? "补录中…" : "确认补录"}
                </Button>
                <Button variant="ghost" onClick={() => setConfirmImport(false)}>
                  取消
                </Button>
              </span>
            ) : (
              <Button variant="secondary" onClick={() => setConfirmImport(true)}>
                一键补录（{unconfigured.length}）
              </Button>
            ))}
        </div>

        {catalog.data?.upstreamError && (
          <ErrorBanner
            error={`上游模型拉取失败：${catalog.data.upstreamError}`}
            onRetry={() => refresh.mutate()}
            className="mb-3"
          />
        )}
        {refresh.isError && (
          <ErrorBanner
            error={refresh.error}
            onRetry={() => refresh.mutate()}
            className="mb-3"
          />
        )}
        {models.isError && (
          <ErrorBanner
            error={models.error}
            onRetry={() => void models.refetch()}
            className="mb-3"
          />
        )}
        {catalog.isError && (
          <ErrorBanner
            error={catalog.error}
            onRetry={() => void catalog.refetch()}
            className="mb-3"
          />
        )}
        {importModels.isError && (
          <div className="mb-3">
            <ErrorText error={importModels.error} />
          </div>
        )}

        {pending && <Spinner />}
        {!pending && rows.length === 0 && (
          <EmptyState
            title="模型目录为空"
            description="尚无已配置、上游可见或日志已观测的模型。可以刷新上游目录，或手动新增价格。"
            action={<Button onClick={() => openCreate()}>新增模型</Button>}
          />
        )}
        {!pending && rows.length > 0 && (
          <DataTable
            columns={columns}
            rows={rows}
            rowKey={(row) => row.name}
            empty="暂无模型"
          />
        )}
        <p className="mt-3 text-xs text-muted">
          价格按每百万 tokens 计。补录会以 0 费率创建条目，模型可继续编辑；五项费率全为 0 时标记为「0
          价（待填）」。
        </p>
      </Card>

      <Drawer
        open={editorOpen}
        title={editingId == null ? "新增模型" : "编辑模型"}
        onClose={closeEditor}
      >
        <div className="space-y-6">
          <section className="space-y-3">
            <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">
              基础价格（$ / 百万 tokens）
            </h3>
            <Field label="模型名称" hint="可从目录选择或手动填写" error={errors.name}>
              <Input
                list="model-catalog-names"
                value={form.name}
                placeholder="例如 claude-sonnet-4"
                onChange={(event) => setForm({ ...form, name: event.target.value })}
              />
              <datalist id="model-catalog-names">
                {datalistNames.map((name) => (
                  <option key={name} value={name} />
                ))}
              </datalist>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="输入价" error={errors.inputPerMTok}>
                <Input
                  inputMode="decimal"
                  value={form.input}
                  placeholder="0"
                  onChange={(event) => setForm({ ...form, input: event.target.value })}
                />
              </Field>
              <Field label="输出价" error={errors.outputPerMTok}>
                <Input
                  inputMode="decimal"
                  value={form.output}
                  placeholder="0"
                  onChange={(event) => setForm({ ...form, output: event.target.value })}
                />
              </Field>
              <Field label="缓存读价" error={errors.cacheReadPerMTok}>
                <Input
                  inputMode="decimal"
                  value={form.cacheRead}
                  placeholder="0"
                  onChange={(event) => setForm({ ...form, cacheRead: event.target.value })}
                />
              </Field>
              <Field
                label="缓存写价"
                hint="留空按 0"
                error={errors.cacheWritePerMTok}
              >
                <Input
                  inputMode="decimal"
                  value={form.cacheWrite}
                  placeholder="0（可留空）"
                  onChange={(event) => setForm({ ...form, cacheWrite: event.target.value })}
                />
              </Field>
            </div>
            <Field
              label="推理价"
              hint="留空继承输出价"
              error={errors.reasoningPerMTok}
            >
              <Input
                inputMode="decimal"
                value={form.reasoning}
                placeholder="留空继承输出"
                onChange={(event) => setForm({ ...form, reasoning: event.target.value })}
              />
            </Field>
          </section>

          <section className="space-y-3">
            <div className="flex items-center justify-between gap-2">
              <h3 className="text-xs font-semibold uppercase tracking-wide text-muted">时段窗口</h3>
              <Button
                variant="secondary"
                onClick={() =>
                  setForm((current) => ({ ...current, windows: [...current.windows, blankWindow()] }))
                }
              >
                新增窗口
              </Button>
            </div>
            {errors.windows && <p className="text-xs text-danger">{errors.windows}</p>}
            {form.windows.length === 0 && (
              <p className="text-xs text-muted">
                暂无时段窗口，所有请求使用基础价。点击「新增窗口」添加高峰或夜间价。
              </p>
            )}
            {form.windows.map((draft, index) => (
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
            ))}
          </section>

          {save.isError && (
            <div className="space-y-1">
              <ErrorText error={save.error} />
              {saveFields.length > 0 && (
                <ul className="space-y-0.5 text-xs text-danger">
                  {saveFields.map(([field, message]) => (
                    <li key={field}>
                      {FIELD_LABELS[field] ?? field}：{message}
                    </li>
                  ))}
                </ul>
              )}
            </div>
          )}

          <div className="flex justify-end gap-2 border-t border-line pt-3">
            <Button variant="secondary" onClick={closeEditor}>
              取消
            </Button>
            <Button onClick={submit} disabled={save.isPending}>
              {save.isPending ? "保存中…" : "保存"}
            </Button>
          </div>
        </div>
      </Drawer>
    </div>
  );
}
