import { expect, it } from "vitest";
import { loggedOutMe, loginPathFor, safeNext } from "./auth";

it("builds login redirects and ignores auth pages", () => {
  expect(loginPathFor("/requests", "?model=x")).toBe(
    "/login?next=%2Frequests%3Fmodel%3Dx",
  );
  expect(loginPathFor("/", "")).toBe("/login?next=%2F");
  expect(loginPathFor("/login", "?next=%2F")).toBeNull();
});

it("clears the cached authentication flag before redirecting", () => {
  // AuthGate must render /login instead of bouncing back to the page whose
  // protected call just returned 401.
  expect(loggedOutMe({ authRequired: true, authenticated: true })).toEqual({
    authRequired: true,
    authenticated: false,
  });
  expect(loggedOutMe(undefined)).toEqual({ authRequired: true, authenticated: false });
  expect(loggedOutMe({ authRequired: false, authenticated: true })).toEqual({
    authRequired: false,
    authenticated: false,
  });
});

it("keeps next on internal paths", () => {
  expect(safeNext("/requests?model=x")).toBe("/requests?model=x");
  expect(safeNext("//evil.com")).toBe("/");
  expect(safeNext("https://evil.com")).toBe("/");
  expect(safeNext("/\\evil.com")).toBe("/");
  expect(safeNext("/login?next=%2F")).toBe("/");
  expect(safeNext(null)).toBe("/");
});
