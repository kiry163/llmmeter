import type { ReactElement } from "react";
import { Button, ErrorText } from "./ui";

export interface ErrorBannerProps {
  error: unknown;
  onRetry?: () => void;
  className?: string;
}

/** ErrorBanner shows a section-level failure without blanking the page. */
export function ErrorBanner({ error, onRetry, className = "" }: ErrorBannerProps): ReactElement {
  return (
    <div
      className={`flex flex-wrap items-center justify-between gap-3 rounded-md border border-danger/40 bg-danger/10 px-3 py-2 ${className}`}
    >
      <ErrorText error={error} />
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          重试
        </Button>
      )}
    </div>
  );
}
