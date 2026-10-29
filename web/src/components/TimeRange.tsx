import { presetRange, RANGE_LABELS, RANGE_PRESETS } from "../lib/range";
import type { DateRange, RangePreset } from "../lib/range";
import { Button, Input } from "./ui";

export interface RangeSelection extends DateRange {
  preset: RangePreset;
}

export interface TimeRangeProps {
  value: RangeSelection;
  onChange: (next: RangeSelection) => void;
  className?: string;
}

/** TimeRange renders the preset shortcuts and the editable date bounds. */
export function TimeRange({ value, onChange, className = "" }: TimeRangeProps) {
  return (
    <div
      className={`flex flex-col gap-2 lg:flex-row lg:items-center lg:justify-between ${className}`}
    >
      <div className="flex flex-wrap items-center gap-1" role="group" aria-label="常用时间区间">
        {RANGE_PRESETS.map((preset) => (
          <Button
            key={preset}
            variant={value.preset === preset ? "primary" : "secondary"}
            aria-pressed={value.preset === preset}
            onClick={() =>
              onChange(
                preset === "custom"
                  ? { preset, from: value.from, to: value.to }
                  : { preset, ...presetRange(preset) },
              )
            }
            className="px-2.5 py-1"
          >
            {RANGE_LABELS[preset]}
          </Button>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
        <label className="flex items-center gap-1">
          <span>从</span>
          <span className="block w-36">
            <Input
              type="date"
              value={value.from}
              max={value.to}
              aria-label="开始日期"
              onChange={(event) => {
                const from = event.target.value;
                if (!from) return;
                onChange({ preset: "custom", from, to: from > value.to ? from : value.to });
              }}
            />
          </span>
        </label>
        <label className="flex items-center gap-1">
          <span>至</span>
          <span className="block w-36">
            <Input
              type="date"
              value={value.to}
              min={value.from}
              aria-label="结束日期"
              onChange={(event) => {
                const to = event.target.value;
                if (!to) return;
                onChange({ preset: "custom", from: to < value.from ? to : value.from, to });
              }}
            />
          </span>
        </label>
      </div>
    </div>
  );
}
