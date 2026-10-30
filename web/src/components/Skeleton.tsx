import type { ReactElement } from "react";

/** Skeleton is the shared loading placeholder; size it with className. */
export function Skeleton({ className = "h-4 w-full" }: { className?: string }): ReactElement {
  return (
    <div
      aria-hidden="true"
      className={`animate-pulse rounded-md bg-line/50 ${className}`}
    />
  );
}
