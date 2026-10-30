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
    <div className={`empty-state ${className}`.trim()}>
      <p className="empty-title">{title}</p>
      {description && <p className="empty-copy">{description}</p>}
      {action}
    </div>
  );
}
