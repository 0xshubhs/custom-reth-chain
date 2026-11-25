/**
 * Light / Dim / Dark, like Etherscan. public/theme-boot.js applies the theme
 * before first paint; this keeps React and <html data-theme> in step after.
 * With no saved choice the page follows the system (light or dark) and keeps
 * following it while it changes; picking a theme saves it.
 */
import { useCallback, useSyncExternalStore } from "react";

export type Theme = "light" | "dim" | "dark";

export const THEMES: { id: Theme; label: string }[] = [
  { id: "light", label: "Light" },
  { id: "dim", label: "Dim" },
  { id: "dark", label: "Dark" },
];

const KEY = "theme";
const META: Record<Theme, string> = { light: "#f7f7f7", dim: "#1c1c1c", dark: "#0a0a0a" };
const listeners = new Set<() => void>();

function isTheme(v: unknown): v is Theme {
  return v === "light" || v === "dim" || v === "dark";
}

function saved(): Theme | null {
  try {
    const v = localStorage.getItem(KEY);
    return isTheme(v) ? v : null;
  } catch {
    return null;
  }
}

function system(): Theme {
  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  } catch {
    return "light";
  }
}

function read(): Theme {
  if (typeof document === "undefined") return "light";
  const v = document.documentElement.getAttribute("data-theme");
  return isTheme(v) ? v : "light";
}

function apply(t: Theme) {
  document.documentElement.setAttribute("data-theme", t);
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", META[t]);
  listeners.forEach((l) => l());
}

function subscribe(cb: () => void) {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

if (typeof window !== "undefined" && window.matchMedia) {
  window.matchMedia("(prefers-color-scheme: dark)").addEventListener?.("change", () => {
    if (!saved()) apply(system());
  });
}

export function useTheme() {
  const theme = useSyncExternalStore(subscribe, read, () => "light" as Theme);
  const setTheme = useCallback((t: Theme) => {
    try {
      localStorage.setItem(KEY, t);
    } catch {
      /* private mode: still switch for this page view */
    }
    apply(t);
  }, []);
  return { theme, setTheme, isDark: theme !== "light" };
}

/** Sync the theme-color meta with what theme-boot.js picked. Call once at start. */
export function initThemeMeta() {
  apply(read());
}
