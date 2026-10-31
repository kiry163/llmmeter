import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import { browserTimezone, resolveTimezone } from "../lib/settings";

/**
 * useTimezone returns the configured IANA zone from the cached bootstrap
 * payload. Date presets, price-window matching and request timestamps all use
 * it instead of the browser zone. Undefined while bootstrap loads, which the
 * pure helpers treat as the browser zone. The backend's "Local" sentinel is
 * rendered with the browser zone because Intl rejects it.
 */
export function useTimezone(): string | undefined {
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  if (!bootstrap.data) return undefined;
  return resolveTimezone(bootstrap.data.timezone) ?? browserTimezone();
}
