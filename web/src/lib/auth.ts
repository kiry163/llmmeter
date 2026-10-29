import { UnauthorizedError } from "../api/client";

/** UNAUTHORIZED_EVENT is dispatched when a protected call loses its session. */
export const UNAUTHORIZED_EVENT = "llmmeter:unauthorized";

/** notifyUnauthorized raises the global redirect signal for a 401 response. */
export function notifyUnauthorized(error: unknown): void {
  if (error instanceof UnauthorizedError) {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  }
}

/**
 * safeNext keeps post-login redirects on this origin. Absolute URLs,
 * protocol-relative paths and the auth pages themselves fall back to "/".
 */
export function safeNext(raw: string | null): string {
  if (!raw) return "/";
  if (!raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
  const path = raw.split(/[?#]/)[0];
  if (
    path === "/login" ||
    path === "/setup" ||
    path.startsWith("/login/") ||
    path.startsWith("/setup/")
  ) {
    return "/";
  }
  return raw;
}
