import type { ReactElement, ReactNode } from "react";

export interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}

/** EmptyState is the shared placeholder for sections with no rows. */
export function EmptyState({
  title,
  description,
  action,
  className = "",
}: EmptyStateProps): ReactElement {
  return (
    <div
      className={`flex flex-col items-center gap-2 rounded-lg border border-dashed border-line px-6 py-10 text-center ${className}`}
    >
      <p className="text-sm font-medium text-ink">{title}</p>
      {description && <p className="max-w-sm text-xs text-muted">{description}</p>}
      {action}
    </div>
  );
}
