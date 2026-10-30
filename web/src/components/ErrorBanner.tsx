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
    <div className={`error-banner ${className}`.trim()} role="alert">
      <ErrorText error={error} />
      {onRetry && (
        <Button variant="secondary" onClick={onRetry}>
          重试
        </Button>
      )}
    </div>
  );
}
