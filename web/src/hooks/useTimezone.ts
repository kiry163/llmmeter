import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";

/**
 * useTimezone returns the configured IANA zone from the cached bootstrap
 * payload. Date presets, price-window matching and request timestamps all use
 * it instead of the browser zone. Undefined while bootstrap loads, which the
 * pure helpers treat as the browser zone.
 */
export function useTimezone(): string | undefined {
  const bootstrap = useQuery({ queryKey: ["bootstrap"], queryFn: api.bootstrap });
  return bootstrap.data?.timezone;
}
