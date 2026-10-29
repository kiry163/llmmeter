import { useSyncExternalStore } from "react";
import { api } from "../api/client";
import type { LiveSnapshot } from "../api/types";

/** LiveMode reports how the overview is currently receiving live metrics. */
export type LiveMode = "sse" | "poll" | "down";

export interface LiveState {
  snapshot: LiveSnapshot | null;
  mode: LiveMode;
}

const idleState: LiveState = { snapshot: null, mode: "down" };

let state: LiveState = idleState;
const listeners = new Set<() => void>();
let stopConnection: (() => void) | null = null;

function publish(next: LiveState): void {
  if (next.snapshot === state.snapshot && next.mode === state.mode) return;
  state = next;
  for (const listener of listeners) listener();
}

/**
 * start opens one SSE connection shared by every mounted consumer. Three
 * consecutive failures fall back to polling the snapshot endpoint every five
 * seconds.
 */
function start(): () => void {
  let closed = false;
  let failures = 0;
  let source: EventSource | null = null;
  let retryTimer: number | undefined;
  let pollTimer: number | undefined;
  let polling = false;

  const poll = async (): Promise<void> => {
    if (closed) return;
    try {
      const snapshot = await api.getLiveSnapshot(60);
      if (closed) return;
      publish({ snapshot, mode: "poll" });
    } catch {
      if (closed) return;
      publish({ ...state, mode: "down" });
    } finally {
      if (!closed) pollTimer = setTimeout(() => void poll(), 5000);
    }
  };

  const beginPolling = (): void => {
    if (polling || closed) return;
    polling = true;
    source?.close();
    source = null;
    clearTimeout(retryTimer);
    retryTimer = undefined;
    void poll();
  };

  const connect = (): void => {
    if (closed) return;
    retryTimer = undefined;
    let es: EventSource;
    try {
      es = new EventSource("/api/live/stream?window=60");
    } catch {
      beginPolling();
      return;
    }
    source = es;

    es.addEventListener("open", () => {
      if (!closed) publish({ ...state, mode: "sse" });
    });

    es.addEventListener("tick", (event) => {
      if (closed) return;
      failures = 0;
      try {
        const snapshot = JSON.parse((event as MessageEvent<string>).data) as LiveSnapshot;
        publish({ snapshot, mode: "sse" });
      } catch {
        // Ignore malformed frames; the next tick replaces them.
      }
    });

    es.addEventListener("error", () => {
      if (closed) return;
      es.close();
      if (source === es) source = null;
      publish({ ...state, mode: "down" });
      failures += 1;
      if (failures >= 3) {
        beginPolling();
        return;
      }
      const delay = Math.min(1000 * 2 ** (failures - 1), 30_000);
      retryTimer = setTimeout(connect, delay);
    });
  };

  if (typeof EventSource === "undefined") {
    beginPolling();
  } else {
    connect();
  }

  return () => {
    closed = true;
    source?.close();
    source = null;
    clearTimeout(retryTimer);
    clearTimeout(pollTimer);
    publish(idleState);
  };
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (listeners.size === 1) stopConnection = start();
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      stopConnection?.();
      stopConnection = null;
    }
  };
}

function getLiveState(): LiveState {
  return state;
}

/** useLive returns the shared live snapshot and connection mode. */
export function useLive(): LiveState {
  return useSyncExternalStore(subscribe, getLiveState);
}
