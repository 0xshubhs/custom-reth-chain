/**
 * Live block feed: one EventSource on /api/v2/stream (`event: block`,
 * `data: BlockSummary`), shared by every component that wants it and closed
 * when the last one unmounts.
 *
 * Graceful fallback: when the stream cannot be opened (no EventSource, a proxy
 * that buffers or refuses it, the engine down) the status turns "polling" and
 * pages refetch on a timer instead (see `useLivePolling`). The stream is
 * retried in the background with a growing delay, and the status goes back to
 * "live" as soon as it opens.
 */
import { useEffect, useRef, useSyncExternalStore } from "react";

import { API_BASE } from "./api";
import type { BlockSummary } from "./types";

export type LiveStatus = "connecting" | "live" | "polling";

type Listener = (b: BlockSummary) => void;

const blockListeners = new Set<Listener>();
const statusListeners = new Set<() => void>();
let status: LiveStatus = "connecting";
let es: EventSource | null = null;
let users = 0;
let retryTimer: ReturnType<typeof setTimeout> | null = null;
let failures = 0;
let openedOnce = false;

function setStatus(s: LiveStatus) {
  if (s === status) return;
  status = s;
  statusListeners.forEach((l) => l());
}

function close() {
  if (es) {
    es.close();
    es = null;
  }
  if (retryTimer) {
    clearTimeout(retryTimer);
    retryTimer = null;
  }
}

function scheduleRetry() {
  close();
  setStatus("polling");
  failures++;
  // 15 s, 30 s, 60 s ... capped at 5 minutes.
  const delay = Math.min(300_000, 15_000 * 2 ** Math.min(failures - 1, 5));
  retryTimer = setTimeout(() => {
    retryTimer = null;
    if (users > 0) open();
  }, delay);
}

function open() {
  if (typeof window === "undefined" || typeof EventSource === "undefined") {
    setStatus("polling");
    return;
  }
  close();
  openedOnce = false;
  try {
    es = new EventSource(`${API_BASE}/stream`);
  } catch {
    scheduleRetry();
    return;
  }
  es.onopen = () => {
    openedOnce = true;
    failures = 0;
    setStatus("live");
  };
  es.addEventListener("block", (ev) => {
    try {
      const b = JSON.parse((ev as MessageEvent<string>).data) as BlockSummary;
      if (typeof b?.number === "number") blockListeners.forEach((l) => l(b));
    } catch {
      /* a malformed event is skipped, the next one still counts */
    }
  });
  es.onerror = () => {
    // The browser reconnects by itself while readyState is CONNECTING. A
    // stream that never opened (404, 503, wrong content type) is CLOSED, or
    // keeps failing: either way fall back to polling and retry later.
    if (!es || es.readyState === EventSource.CLOSED || !openedOnce) {
      scheduleRetry();
    } else {
      setStatus("connecting");
    }
  };
}

function acquire() {
  users++;
  if (users === 1) {
    setStatus("connecting");
    open();
  }
}

function release() {
  users = Math.max(0, users - 1);
  if (users === 0) {
    close();
    failures = 0;
  }
}

function subscribeStatus(cb: () => void) {
  statusListeners.add(cb);
  return () => statusListeners.delete(cb);
}

/**
 * Subscribe to new blocks for the lifetime of the calling component. Returns
 * the feed status for the "Live" indicator.
 */
export function useLiveBlocks(onBlock?: Listener, enabled = true): LiveStatus {
  const cb = useRef(onBlock);
  cb.current = onBlock;

  useEffect(() => {
    if (!enabled) return;
    const l: Listener = (b) => cb.current?.(b);
    blockListeners.add(l);
    acquire();
    return () => {
      blockListeners.delete(l);
      release();
    };
  }, [enabled]);

  return useSyncExternalStore(
    subscribeStatus,
    () => status,
    () => "connecting" as LiveStatus,
  );
}

/** Poll interval for list queries: off while the stream is live. */
export function pollInterval(s: LiveStatus, ms = 12_000): number | false {
  return s === "live" ? false : ms;
}
