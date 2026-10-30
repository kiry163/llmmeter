import { expect, it } from "vitest";
import type { Settings } from "../api/types";
import {
  browserTimezone,
  listenIsLoopback,
  maskValue,
  redactSettings,
  timezoneOptions,
  validateClearPassword,
  validateRetention,
  validateSettings,
  validateTimeout,
} from "./settings";

it("masks secrets", () => {
  expect(maskValue("sk-secret-1234")).toBe("••••1234");
  expect(maskValue("")).toBe("未设置");
  expect(maskValue("abc")).toBe("••••");
});

it("validates timeout and retention ranges", () => {
  expect(validateTimeout("30")).toBeUndefined();
  expect(validateTimeout("1")).toBeUndefined();
  expect(validateTimeout("3600")).toBeUndefined();
  expect(validateTimeout("0")).toBeTruthy();
  expect(validateTimeout("3601")).toBeTruthy();
  expect(validateTimeout("3.5")).toBeTruthy();
  expect(validateTimeout("")).toBeTruthy();

  expect(validateRetention("7")).toBeUndefined();
  expect(validateRetention("3650")).toBeUndefined();
  expect(validateRetention("6")).toBeTruthy();
  expect(validateRetention("3651")).toBeTruthy();
  expect(validateRetention("abc")).toBeTruthy();
});

it("validates the settings form", () => {
  const base = {
    timeoutS: "30",
    retentionDays: "90",
    currentPassword: "",
    newPassword: "",
    confirmPassword: "",
    passwordSet: false,
  };
  expect(validateSettings(base)).toEqual({});
  expect(validateSettings({ ...base, timeoutS: "0" }).timeoutS).toBeTruthy();
  expect(validateSettings({ ...base, retentionDays: "3651" }).retentionDays).toBeTruthy();
});

it("requires the current password when one is set", () => {
  const base = {
    timeoutS: "30",
    retentionDays: "90",
    currentPassword: "",
    newPassword: "newpass12",
    confirmPassword: "newpass12",
    passwordSet: true,
  };
  expect(validateSettings(base).currentPassword).toBeTruthy();
  expect(validateSettings({ ...base, currentPassword: "hunter22" })).toEqual({});
});

it("requires the current password to clear it", () => {
  expect(validateClearPassword("")).toBeTruthy();
  expect(validateClearPassword("   ")).toBeTruthy();
  expect(validateClearPassword("hunter22")).toBeUndefined();
});

it("rejects short and mismatched passwords", () => {
  const base = {
    timeoutS: "30",
    retentionDays: "90",
    currentPassword: "",
    newPassword: "",
    confirmPassword: "",
    passwordSet: false,
  };
  expect(validateSettings({ ...base, newPassword: "short", confirmPassword: "short" }).newPassword)
    .toBeTruthy();
  expect(
    validateSettings({ ...base, newPassword: "newpass12", confirmPassword: "otherpass" })
      .confirmPassword,
  ).toBeTruthy();
  expect(
    validateSettings({ ...base, newPassword: "newpass12", confirmPassword: "newpass12" }),
  ).toEqual({});
});

it("redacts settings for display only", () => {
  const settings: Settings = {
    upstream: {
      baseUrl: "https://api.example.com/v1",
      timeoutS: 30,
      apiKey: { set: true, last4: "1234" },
    },
    timezone: "Asia/Shanghai",
    retentionDays: 90,
    gatewayKey: { set: true, prefix: "sk-local-", last4: "abcd" },
    adminPasswordSet: true,
    listen: "127.0.0.1:8100",
    dbPath: "/tmp/llmmeter.db",
    dbSizeBytes: 1024,
    version: "test",
  };
  const view = redactSettings(settings);
  expect(view.upstream.apiKey).toBe("••••1234");
  expect(view.gatewayKey).toBe("sk-local-••••abcd");
  expect(view.upstream.baseUrl).toBe("https://api.example.com/v1");
  expect(view.listen).toBe("127.0.0.1:8100");

  const empty = redactSettings({
    ...settings,
    upstream: { ...settings.upstream, apiKey: { set: false, last4: "" } },
    gatewayKey: { set: false, prefix: "", last4: "" },
  });
  expect(empty.upstream.apiKey).toBe("未设置");
  expect(empty.gatewayKey).toBe("未设置");
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
