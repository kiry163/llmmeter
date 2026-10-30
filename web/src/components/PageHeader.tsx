import type { ReactNode } from "react";

export function PageHeader({
  eyebrow,
  title,
  subtitle,
  actions,
}: {
  eyebrow: string;
  title: string;
  subtitle?: string;
  actions?: ReactNode;
}) {
  return (
    <div className="page-head">
      <div>
        <div className="page-eyebrow">{eyebrow}</div>
        <h1 className="page-title">{title}</h1>
        {subtitle && <div className="page-sub">{subtitle}</div>}
      </div>
      {actions}
    </div>
  );
}

export interface RangeOption<T extends string> {
  value: T;
  label: string;
}

export function RangeSeg<T extends string>({
  value,
  options,
  onChange,
  ariaLabel,
  small = false,
}: {
  value: T;
  options: readonly RangeOption<T>[];
  onChange: (value: T) => void;
  ariaLabel: string;
  small?: boolean;
}) {
  return (
    <div className={`seg${small ? " seg-sm" : ""}`} role="group" aria-label={ariaLabel}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          className={option.value === value ? "on" : ""}
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
