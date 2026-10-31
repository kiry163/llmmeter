import { UnauthorizedError } from "../api/client";
import type { Me } from "../api/types";

/** UNAUTHORIZED_EVENT is dispatched when a protected call loses its session. */
export const UNAUTHORIZED_EVENT = "llmmeter:unauthorized";

/** notifyUnauthorized raises the global redirect signal for a 401 response. */
export function notifyUnauthorized(error: unknown): void {
  if (error instanceof UnauthorizedError) {
    window.dispatchEvent(new Event(UNAUTHORIZED_EVENT));
  }
}

/**
 * loginPathFor returns the login URL for a lost session, or null when the
 * viewer is already on an auth page. Keeping this pure lets the guard and the
 * 401 handler share one rule.
 */
export function loginPathFor(pathname: string, search: string): string | null {
  if (pathname === "/login") return null;
  return `/login?next=${encodeURIComponent(`${pathname}${search}`)}`;
}

/**
 * loggedOutMe clears the cached authentication flag before a 401 redirect so
 * AuthGate does not bounce /login back to the route that just failed.
 */
export function loggedOutMe(current: Me | undefined): Me {
  return { authRequired: current?.authRequired ?? true, authenticated: false };
}

/**
 * safeNext keeps post-login redirects on this origin. Absolute URLs,
 * protocol-relative paths and the auth pages themselves fall back to "/".
 */
export function safeNext(raw: string | null): string {
  if (!raw) return "/";
  if (!raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/";
  const path = raw.split(/[?#]/)[0];
  if (path === "/login" || path.startsWith("/login/")) {
    return "/";
  }
  return raw;
}
