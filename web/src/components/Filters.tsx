import { useEffect, useState, type ReactNode } from "react";
import {
  PATH_CLASS_FILTERS,
  PRICED_FILTER_LABELS,
  USAGE_STATUS_FILTERS,
  USAGE_STATUS_LABELS,
} from "../lib/query";
import type { PricedFilter, RequestsFilters } from "../lib/query";
import { TimeRange } from "./TimeRange";
import { Button, Input, Select } from "./ui";

export interface FiltersProps {
  filters: RequestsFilters;
  models: readonly string[];
  onChange: (next: RequestsFilters) => void;
  onReset: () => void;
  className?: string;
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <label className="block text-xs text-muted">
      <span className="mb-1 block">{label}</span>
      {children}
    </label>
  );
}

const PRICED_OPTIONS: readonly PricedFilter[] = ["all", "priced", "unpriced"];

/** StatusInput keeps a local draft so partially typed codes do not refetch. */
function StatusInput({ value, onChange }: { value: string; onChange: (next: string) => void }) {
  const [draft, setDraft] = useState(value);

  useEffect(() => setDraft(value), [value]);

  return (
    <Input
      type="text"
      inputMode="numeric"
      placeholder="全部"
      maxLength={3}
      value={draft}
      aria-label="状态码"
      onChange={(event) => {
        const next = event.target.value.trim();
        setDraft(next);
        if (next === "" || /^\d{3}$/.test(next)) onChange(next);
      }}
      onBlur={() => {
        if (!/^\d{3}$/.test(draft)) setDraft(value);
      }}
    />
  );
}

/** Filters renders the request explorer filter bar backed by the URL. */
export function Filters({ filters, models, onChange, onReset, className = "" }: FiltersProps) {
  return (
    <div className={`space-y-3 ${className}`}>
      <TimeRange
        value={{ preset: filters.preset, from: filters.from, to: filters.to }}
        onChange={(selection) => onChange({ ...filters, ...selection })}
      />

      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-6">
        <Field label="模型">
          <Select
            className="w-full"
            value={filters.model}
            onChange={(event) => onChange({ ...filters, model: event.target.value })}
          >
            <option value="">全部模型</option>
            {models.map((name) => (
              <option key={name} value={name}>
                {name}
              </option>
            ))}
          </Select>
        </Field>

        <Field label="路径类">
          <Select
            className="w-full"
            value={filters.pathClass}
            onChange={(event) =>
              onChange({
                ...filters,
                pathClass: event.target.value as RequestsFilters["pathClass"],
              })
            }
          >
            <option value="">全部路径</option>
            {PATH_CLASS_FILTERS.map((pathClass) => (
              <option key={pathClass} value={pathClass}>
                {pathClass}
              </option>
            ))}
          </Select>
        </Field>

        <Field label="状态码">
          <StatusInput
            value={filters.status}
            onChange={(status) => onChange({ ...filters, status })}
          />
        </Field>

        <Field label="用量状态">
          <Select
            className="w-full"
            value={filters.usageStatus}
            onChange={(event) =>
              onChange({
                ...filters,
                usageStatus: event.target.value as RequestsFilters["usageStatus"],
              })
            }
          >
            <option value="">全部</option>
            {USAGE_STATUS_FILTERS.map((status) => (
              <option key={status} value={status}>
                {USAGE_STATUS_LABELS[status]}
              </option>
            ))}
          </Select>
        </Field>

        <Field label="定价">
          <Select
            className="w-full"
            value={filters.priced}
            onChange={(event) =>
              onChange({ ...filters, priced: event.target.value as PricedFilter })
            }
          >
            {PRICED_OPTIONS.map((priced) => (
              <option key={priced} value={priced}>
                {PRICED_FILTER_LABELS[priced]}
              </option>
            ))}
          </Select>
        </Field>

        <div className="flex items-end">
          <Button variant="secondary" className="w-full" onClick={onReset}>
            重置筛选
          </Button>
        </div>
      </div>
    </div>
  );
}
