"use client";

import { useSyncExternalStore } from "react";

type Theme = "dark" | "light";

const STORAGE_KEY = "lineup-theme";

// The <html data-lt> attribute is an external system (mutated directly on
// the DOM, outside React). useSyncExternalStore is the correct primitive
// for reading it: it returns the deterministic "dark" default during SSR
// (matching the inline bootstrap script) and reconciles with the real
// attribute on mount, without a setState-in-effect render cascade.
const listeners = new Set<() => void>();

function subscribe(onStoreChange: () => void) {
  listeners.add(onStoreChange);
  return () => listeners.delete(onStoreChange);
}

function getSnapshot(): Theme {
  return document.documentElement.dataset.lt === "light" ? "light" : "dark";
}

function getServerSnapshot(): Theme {
  return "dark";
}

function applyTheme(next: Theme) {
  document.documentElement.dataset.lt = next;
  try {
    localStorage.setItem(STORAGE_KEY, next);
  } catch {
    // Storage can fail (private browsing, quota, disabled) — the DOM
    // attribute above and the listener notification below must still
    // happen so the UI stays in sync even if persistence doesn't.
  }
  listeners.forEach((listener) => listener());
}

export function ThemeToggle() {
  const theme = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);

  return (
    <button
      type="button"
      onClick={() => applyTheme(theme === "dark" ? "light" : "dark")}
      aria-label="Toggle theme"
      className="whitespace-nowrap rounded-full border border-line bg-panel px-3.5 py-[7px] text-xs font-medium text-mut"
    >
      {theme === "dark" ? "☾ Dark" : "☀ Light"}
    </button>
  );
}
