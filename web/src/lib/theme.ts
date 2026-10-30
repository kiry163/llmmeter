import { useSyncExternalStore } from "react";

export type Theme = "dark" | "light";

const STORAGE_KEY = "llmmeter.theme";
const EVENT = "llmmeter:theme";

function systemTheme(): Theme {
  if (typeof window === "undefined") return "dark";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function readTheme(): Theme {
  if (typeof document === "undefined") return "dark";
  return document.documentElement.dataset.theme === "light" ? "light" : "dark";
}

export function setTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
  try {
    localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // A blocked storage API should not break the app.
  }
  window.dispatchEvent(new CustomEvent(EVENT, { detail: theme }));
}

export function toggleTheme(): Theme {
  const next = readTheme() === "dark" ? "light" : "dark";
  setTheme(next);
  return next;
}

function subscribe(callback: () => void): () => void {
  const media = window.matchMedia("(prefers-color-scheme: dark)");
  const onChange = () => {
    // Only follow the system when the user has not made an explicit choice.
    let saved: string | null = null;
    try {
      saved = localStorage.getItem(STORAGE_KEY);
    } catch {
      saved = null;
    }
    if (saved === null) {
      document.documentElement.dataset.theme = systemTheme();
    }
    callback();
  };
  window.addEventListener(EVENT, callback);
  media.addEventListener("change", onChange);
  return () => {
    window.removeEventListener(EVENT, callback);
    media.removeEventListener("change", onChange);
  };
}

export function useTheme(): Theme {
  return useSyncExternalStore(subscribe, readTheme, () => "dark");
}
