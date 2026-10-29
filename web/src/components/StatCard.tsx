import type { ReactNode } from "react";

export interface StatDelta {
  current: number | null;
  previous: number | null;
  /** Whether growth is favourable; omitted keeps the delta tone neutral. */
  goodDirection?: "up" | "down";
}

export interface StatCardProps {
  label: string;
  value: ReactNode;
  hint?: ReactNode;
  delta?: StatDelta;
  className?: string;
}

interface DeltaView {
  arrow: string;
  text: string;
  direction: "up" | "down" | "flat";
}

function readDelta(delta: StatDelta): DeltaView | null {
  const { current, previous } = delta;
  if (current === null || previous === null) return null;
  if (!Number.isFinite(current) || !Number.isFinite(previous)) return null;

  if (previous === 0) {
    if (current === 0) return { arrow: "→", text: "0%", direction: "flat" };
    return current > 0
      ? { arrow: "↑", text: "新增", direction: "up" }
      : { arrow: "↓", text: "新增", direction: "down" };
  }

  const percent = ((current - previous) / previous) * 100;
  if (!Number.isFinite(percent) || Math.abs(percent) < 0.05) {
    return { arrow: "→", text: "0%", direction: "flat" };
  }
  const magnitude = Math.abs(percent);
  const text = magnitude >= 100 ? `${Math.round(magnitude)}%` : `${magnitude.toFixed(1)}%`;
  return { arrow: percent > 0 ? "↑" : "↓", text, direction: percent > 0 ? "up" : "down" };
}

function deltaTone(direction: DeltaView["direction"], goodDirection?: "up" | "down"): string {
  if (direction === "flat" || goodDirection === undefined) return "text-muted";
  return (direction === "up") === (goodDirection === "up") ? "text-success" : "text-danger";
}

/** StatCard renders one metric with an optional period-over-period delta. */
export function StatCard({ label, value, hint, delta, className = "" }: StatCardProps) {
  const view = delta ? readDelta(delta) : null;

  return (
    <div className={`rounded-lg border border-line bg-surface-raised p-3 ${className}`}>
      <div className="text-xs text-muted">{label}</div>
      <div className="mt-1 min-h-7 text-2xl font-semibold leading-tight tabular-nums">
        {value}
      </div>
      <div className="mt-1 flex min-h-4 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs">
        {view && (
          <span className="inline-flex items-center gap-1 text-muted" title="与上一周期相比">
            较上期
            <span className={deltaTone(view.direction, delta?.goodDirection)}>
              <span aria-hidden="true">{view.arrow}</span>
              <span className="sr-only">
                {view.direction === "up" ? "上升" : view.direction === "down" ? "下降" : "持平"}
              </span>{" "}
              {view.text}
            </span>
          </span>
        )}
        {hint && <span className="text-muted">{hint}</span>}
      </div>
    </div>
  );
}
