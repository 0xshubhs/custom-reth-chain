/**
 * Small page-wide UI state shared by many components without a provider:
 *
 *   - the hovered address (Etherscan's highlight of the same address
 *     everywhere in a table); each AddressLink subscribes to "am I hovered",
 *     so only the links that change re-render;
 *   - the Age / Date Time column toggle, remembered across pages.
 */
import { useCallback, useSyncExternalStore } from "react";

function store<T>(initial: T) {
  let value = initial;
  const listeners = new Set<() => void>();
  return {
    get: () => value,
    set: (v: T) => {
      if (v === value) return;
      value = v;
      listeners.forEach((l) => l());
    },
    subscribe: (cb: () => void) => {
      listeners.add(cb);
      return () => listeners.delete(cb);
    },
  };
}

/* Hovered address ----------------------------------------------------------- */

const hovered = store<string | null>(null);

export function useAddressHighlight(addr: string | null | undefined) {
  const key = addr ? addr.toLowerCase() : null;
  const on = useSyncExternalStore(
    hovered.subscribe,
    () => key !== null && hovered.get() === key,
    () => false,
  );
  const enter = useCallback(() => hovered.set(key), [key]);
  const leave = useCallback(() => {
    if (hovered.get() === key) hovered.set(null);
  }, [key]);
  return { on, enter, leave };
}

/* Age or Date Time ------------------------------------------------------------ */

type TimeMode = "age" | "datetime";
const TIME_KEY = "time-column";

function initialTime(): TimeMode {
  try {
    return localStorage.getItem(TIME_KEY) === "datetime" ? "datetime" : "age";
  } catch {
    return "age";
  }
}

const timeMode = store<TimeMode>(typeof window === "undefined" ? "age" : initialTime());

export function useTimeMode() {
  const mode = useSyncExternalStore(timeMode.subscribe, timeMode.get, () => "age" as TimeMode);
  const toggle = useCallback(() => {
    const next: TimeMode = timeMode.get() === "age" ? "datetime" : "age";
    try {
      localStorage.setItem(TIME_KEY, next);
    } catch {
      /* storage blocked: still switch for this view */
    }
    timeMode.set(next);
  }, []);
  return { mode, toggle };
}
