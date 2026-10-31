import { expect, it } from "vitest";
import {
  browserTimezone,
  listenIsLoopback,
  resolveTimezone,
  timezoneOptions,
  validateRetention,
} from "./settings";

it("normalizes Local and invalid zones", () => {
  expect(resolveTimezone("Asia/Shanghai")).toBe("Asia/Shanghai");
  expect(resolveTimezone("UTC")).toBe("UTC");
  expect(resolveTimezone("Local")).toBeUndefined();
  expect(resolveTimezone("Not/AZone")).toBeUndefined();
  expect(resolveTimezone("")).toBeUndefined();
  expect(resolveTimezone(undefined)).toBeUndefined();
});

it("validates retention ranges", () => {
  expect(validateRetention("7")).toBeUndefined();
  expect(validateRetention("3650")).toBeUndefined();
  expect(validateRetention("6")).toBeTruthy();
  expect(validateRetention("3651")).toBeTruthy();
  expect(validateRetention("abc")).toBeTruthy();
});

it("offers common and browser timezones", () => {
  const options = timezoneOptions("Pacific/Auckland");
  expect(options).toContain("Asia/Shanghai");
  expect(options).toContain("Pacific/Auckland");
  expect(options).toContain(browserTimezone());
  expect(new Set(options).size).toBe(options.length);
});

it("detects loopback listen addresses", () => {
  expect(listenIsLoopback("127.0.0.1:8100")).toBe(true);
  expect(listenIsLoopback("localhost:8100")).toBe(true);
  expect(listenIsLoopback("[::1]:8100")).toBe(true);
  expect(listenIsLoopback("0.0.0.0:8100")).toBe(false);
  expect(listenIsLoopback(":8100")).toBe(false);
  expect(listenIsLoopback("192.168.1.5:8100")).toBe(false);
  expect(listenIsLoopback("")).toBe(false);
});
