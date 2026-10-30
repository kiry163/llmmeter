import { useEffect } from "react";

/** useDocumentTitle keeps the browser tab title in sync with the active page. */
export function useDocumentTitle(title: string): void {
  useEffect(() => {
    document.title = title ? `${title} · llmmeter` : "llmmeter";
  }, [title]);
}
