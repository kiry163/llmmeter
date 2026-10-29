import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type ReactNode } from "react";
import { ApiError, api } from "../api/client";
import type { Model, ModelInput, Window, WindowInput } from "../api/types";
import { Badge, Button, Card, Drawer, ErrorText, Input, Spinner, useToast } from "../components/ui";
import {
  WEEKDAY_LABELS,
  activeWindow,
  describeWindow,
  formatMinute,
  parseRateInput,
  parseTimeInput,
  validateModel,
  validateWindows,
} from "../lib/pricing";
import type { FormErrors } from "../lib/pricing";

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
  return parseRateInput(raw) ?? Number.NaN;
}

function buildPayload(form: FormState): { model: ModelInput; windows: WindowInput[] } {
  return {
    model: {
      name: form.name.trim(),
      inputPerMTok: requiredRate(form.input),
      outputPerMTok: requiredRate(form.output),
      cacheReadPerMTok: requiredRate(form.cacheRead),
      cacheWritePerMTok: requiredRate(form.cacheWrite),
      reasoningPerMTok: parseRateInput(form.reasoning),
    },
    windows: form.windows.map((draft) => ({
      label: draft.label.trim(),
      daysMask: draft.daysMask,
      startMinute: parseTimeInput(draft.start),
      endMinute: parseTimeInput(draft.end),
      inputPerMTok: parseRateInput(draft.input),
      outputPerMTok: parseRateInput(draft.output),
      cacheReadPerMTok: parseRateInput(draft.cacheRead),
      cacheWritePerMTok: parseRateInput(draft.cacheWrite),
      reasoningPerMTok: parseRateInput(draft.reasoning),
    })),
  };
}

function rateText4(value: number | null): string {
  if (value == null) return "继承输出";
  return `$${value.toFixed(4)}/M`;
}

function currentLabel(model: Model, now: Date): ReactNode {
  const matched = activeWindow(model.windows, now);
  if (matched) return <Badge tone="success">当前：{matched.label}</Badge>;
  return <Badge tone="neutral">基础价</Badge>;
}

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

export function Pricing() {
  const queryClient = useQueryClient();
  const toast = useToast();
  const models = useQuery({ queryKey: ["models"], queryFn: api.getModels });

  const [editorOpen, setEditorOpen] = useState(false);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [form, setForm] = useState<FormState>(EMPTY_FORM);
  const [errors, setErrors] = useState<FormErrors>({});
  const [confirmDeleteId, setConfirmDeleteId] = useState<number | null>(null);
  const [now, setNow] = useState(() => new Date());

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
      toast("已生效", "success");
      setEditorOpen(false);
      setErrors({});
    },
    onError: () => {
      // A create may have succeeded before putWindows failed; refresh anyway.
      void queryClient.invalidateQueries({ queryKey: ["models"] });
    },
  });

  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const remove = useMutation({
    mutationFn: (id: number) => api.deleteModel(id),
    onSuccess: (_data, id) => {
      void queryClient.invalidateQueries({ queryKey: ["models"] });
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
    };
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;
    save.mutate(payload);
  }

  const list = models.data?.models ?? [];
  const errorFields = save.error instanceof ApiError ? Object.entries(save.error.fields) : [];

  return (
    <div className="space-y-4">
      <header className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold">价格</h1>
          <p className="text-sm text-muted">
            模型基础价与时段窗口。请求按本地时间匹配窗口，未命中时使用基础价。
          </p>
        </div>
        <Button onClick={openCreate}>新增模型</Button>
      </header>

      <Card title="模型列表">
        {models.isPending && <Spinner />}
        {models.isError && <ErrorText error={models.error} />}
        {models.isSuccess && list.length === 0 && (
          <div className="space-y-2 py-6 text-center">
            <p className="text-sm text-muted">尚未配置价格模型，未匹配的请求会被标记为未定价。</p>
            <Button onClick={openCreate}>新增模型</Button>
          </div>
        )}
        {list.length > 0 && (
          <div className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="text-xs text-muted">
                <tr className="border-b border-line">
                  <th className="py-2 pr-3 font-medium">模型</th>
                  <th className="py-2 pr-3 font-medium">输入</th>
                  <th className="py-2 pr-3 font-medium">输出</th>
                  <th className="py-2 pr-3 font-medium">缓存读</th>
                  <th className="py-2 pr-3 font-medium">缓存写</th>
                  <th className="py-2 pr-3 font-medium">推理</th>
                  <th className="py-2 pr-3 font-medium">窗口</th>
                  <th className="py-2 pr-3 font-medium">当前</th>
                  <th className="py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {list.map((model) => (
                  <tr key={model.id} className="border-b border-line/60 align-top">
                    <td className="py-2 pr-3 font-medium">{model.name}</td>
                    <td className="py-2 pr-3">{rateText4(model.inputPerMTok)}</td>
                    <td className="py-2 pr-3">{rateText4(model.outputPerMTok)}</td>
                    <td className="py-2 pr-3">{rateText4(model.cacheReadPerMTok)}</td>
                    <td className="py-2 pr-3">{rateText4(model.cacheWritePerMTok)}</td>
                    <td className="py-2 pr-3">{rateText4(model.reasoningPerMTok)}</td>
                    <td className="py-2 pr-3">
                      <span title={model.windows.map(describeWindow).join("\n")}>
                        <Badge tone="info">{model.windows.length} 个</Badge>
                      </span>
                    </td>
                    <td className="py-2 pr-3">{currentLabel(model, now)}</td>
                    <td className="py-2 text-right">
                      {confirmDeleteId === model.id ? (
                        <span className="inline-flex items-center gap-1">
                          <span className="text-xs text-danger">确认删除？</span>
                          <Button
                            variant="danger"
                            disabled={remove.isPending}
                            onClick={() => remove.mutate(model.id)}
                          >
                            删除
                          </Button>
                          <Button variant="ghost" onClick={() => setConfirmDeleteId(null)}>
                            取消
                          </Button>
                        </span>
                      ) : (
                        <span className="inline-flex items-center gap-1">
                          <Button variant="secondary" onClick={() => openEdit(model)}>
                            编辑
                          </Button>
                          <Button variant="ghost" onClick={() => setConfirmDeleteId(model.id)}>
                            删除
                          </Button>
                        </span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
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
            <Field label="模型名称" error={errors.name}>
              <Input
                value={form.name}
                placeholder="例如 claude-sonnet-4"
                onChange={(event) => setForm({ ...form, name: event.target.value })}
              />
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
              <Field label="缓存写价" error={errors.cacheWritePerMTok}>
                <Input
                  inputMode="decimal"
                  value={form.cacheWrite}
                  placeholder="0"
                  onChange={(event) => setForm({ ...form, cacheWrite: event.target.value })}
                />
              </Field>
            </div>
            <Field
              label="推理价"
              hint="留空则继承输出价"
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
              {errorFields.length > 0 && (
                <ul className="space-y-0.5 text-xs text-danger">
                  {errorFields.map(([field, message]) => (
                    <li key={field}>
                      {field}：{message}
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
