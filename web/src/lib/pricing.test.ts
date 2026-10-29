import { expect, it } from "vitest";
import {
  describeWindow,
  parseRateInput,
  parseTimeInput,
  validateModel,
  validateWindows,
  windowMatches,
} from "./pricing";

it("rejects empty days and equal minutes", () => {
  const errs = validateWindows([{ label: "x", daysMask: 0, startMinute: 60, endMinute: 60 }]);
  expect(errs["0.daysMask"]).toBeTruthy();
  expect(errs["0.endMinute"]).toBeTruthy();
});

it("accepts cross-midnight window", () => {
  expect(validateWindows([{ label: "night", daysMask: 31, startMinute: 1320, endMinute: 120 }])).toEqual({});
});

it("rejects blank labels and out-of-range minutes", () => {
  const errs = validateWindows([
    { label: "   ", daysMask: 1, startMinute: 0, endMinute: 1440 },
    { label: "ok", daysMask: 127, startMinute: -1, endMinute: 60 },
  ]);
  expect(errs["0.label"]).toBeTruthy();
  expect(errs["0.endMinute"]).toBeTruthy();
  expect(errs["1.startMinute"]).toBeTruthy();
});

it("rejects non-integer minutes and days masks", () => {
  const errs = validateWindows([
    { label: "x", daysMask: 1.5, startMinute: 60.5, endMinute: 90 },
  ]);
  expect(errs["0.daysMask"]).toBeTruthy();
  expect(errs["0.startMinute"]).toBeTruthy();
});

it("rejects days masks outside 1..127", () => {
  expect(validateWindows([{ label: "x", daysMask: 128, startMinute: 0, endMinute: 60 }])["0.daysMask"])
    .toBeTruthy();
  expect(validateWindows([{ label: "x", daysMask: 0, startMinute: 0, endMinute: 60 }])["0.daysMask"])
    .toBeTruthy();
});

it("rejects invalid rate overrides but allows null and zero", () => {
  const bad = validateWindows([
    {
      label: "x",
      daysMask: 1,
      startMinute: 0,
      endMinute: 60,
      inputPerMTok: -1,
      outputPerMTok: Number.NaN,
      cacheReadPerMTok: Number.POSITIVE_INFINITY,
    },
  ]);
  expect(bad["0.inputPerMTok"]).toBeTruthy();
  expect(bad["0.outputPerMTok"]).toBeTruthy();
  expect(bad["0.cacheReadPerMTok"]).toBeTruthy();

  const good = validateWindows([
    {
      label: "x",
      daysMask: 1,
      startMinute: 0,
      endMinute: 60,
      inputPerMTok: 0,
      outputPerMTok: null,
      cacheReadPerMTok: null,
      cacheWritePerMTok: null,
      reasoningPerMTok: null,
    },
  ]);
  expect(good).toEqual({});
});

it("caps the window count at 32", () => {
  const wins = Array.from({ length: 33 }, () => ({
    label: "x",
    daysMask: 1,
    startMinute: 0,
    endMinute: 60,
  }));
  const errs = validateWindows(wins);
  expect(errs.windows).toBeTruthy();
  expect(Object.keys(errs)).toEqual(["windows"]);
});

it("describes day runs and overnight windows", () => {
  expect(describeWindow({ daysMask: 31, startMinute: 540, endMinute: 720 })).toBe(
    "周一–周五 09:00–12:00",
  );
  expect(describeWindow({ daysMask: 127, startMinute: 0, endMinute: 60 })).toBe(
    "每天 00:00–01:00",
  );
  expect(describeWindow({ daysMask: 1 | 4, startMinute: 60, endMinute: 120 })).toBe(
    "周一、周三 01:00–02:00",
  );
  expect(describeWindow({ daysMask: 31, startMinute: 1320, endMinute: 120 })).toBe(
    "周一–周五 22:00–02:00",
  );
});

it("matches day masks with Monday as bit 0", () => {
  const monday = new Date(2026, 9, 26, 9, 0);
  const sunday = new Date(2026, 10, 1, 9, 0);
  const w = { label: "peak", daysMask: 1, startMinute: 540, endMinute: 720 };
  expect(windowMatches(w, monday)).toBe(true);
  expect(windowMatches(w, new Date(2026, 9, 26, 8, 59))).toBe(false);
  expect(windowMatches(w, sunday)).toBe(false);
});

it("matches cross-midnight windows against the previous day", () => {
  const fridayNight = { label: "night", daysMask: 1 << 4, startMinute: 1320, endMinute: 120 };
  expect(windowMatches(fridayNight, new Date(2026, 9, 30, 23, 0))).toBe(true);
  expect(windowMatches(fridayNight, new Date(2026, 9, 31, 1, 0))).toBe(true);
  expect(windowMatches(fridayNight, new Date(2026, 9, 31, 23, 0))).toBe(false);
  expect(
    windowMatches({ ...fridayNight, daysMask: 1 << 5 }, new Date(2026, 9, 31, 1, 0)),
  ).toBe(false);
});

it("parses rates with blank meaning inherit", () => {
  expect(parseRateInput("")).toBeNull();
  expect(parseRateInput("  ")).toBeNull();
  expect(parseRateInput("0")).toBe(0);
  expect(parseRateInput("1.25")).toBe(1.25);
  expect(Number.isNaN(parseRateInput("abc"))).toBe(true);
});

it("parses and rejects time inputs", () => {
  expect(parseTimeInput("09:05")).toBe(545);
  expect(parseTimeInput("22:00")).toBe(1320);
  expect(Number.isNaN(parseTimeInput("24:00"))).toBe(true);
  expect(Number.isNaN(parseTimeInput(""))).toBe(true);
});

it("validates base model fields", () => {
  const base = {
    name: "claude",
    inputPerMTok: 1,
    outputPerMTok: 2,
    cacheReadPerMTok: 0,
    cacheWritePerMTok: 0,
    reasoningPerMTok: null,
  };
  expect(validateModel(base)).toEqual({});
  expect(validateModel({ ...base, name: "  " }).name).toBeTruthy();
  expect(validateModel({ ...base, inputPerMTok: Number.NaN }).inputPerMTok).toBeTruthy();
  expect(validateModel({ ...base, outputPerMTok: -1 }).outputPerMTok).toBeTruthy();
  expect(validateModel({ ...base, reasoningPerMTok: Number.NaN }).reasoningPerMTok).toBeTruthy();
});
