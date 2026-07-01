import { useEffect, useState } from "react";

export interface GenerationEntry {
  generating: boolean;
  preview: string;
  startedAt: number;
}

const MAX_GENERATION_AGE_MS = 5 * 60 * 1000; // 5 minutes anti-stuck timeout

const store = new Map<string, GenerationEntry>();
const listeners = new Set<() => void>();

function notify() {
  // Purge stale entries before notifying so components see clean state.
  const now = Date.now();
  for (const [id, entry] of store) {
    if (entry.generating && now - entry.startedAt > MAX_GENERATION_AGE_MS) {
      store.delete(id);
    }
  }
  for (const cb of listeners) cb();
}

export function getGeneration(sessionId: string): GenerationEntry | undefined {
  return store.get(sessionId);
}

export function setGenerating(sessionId: string, startedAt: number): void {
  store.set(sessionId, { generating: true, preview: "", startedAt });
  notify();
}

export function appendPreview(sessionId: string, delta: string): void {
  const entry = store.get(sessionId);
  if (!entry?.generating) return;
  store.set(sessionId, { ...entry, preview: entry.preview + delta });
  notify();
}

export function clearGeneration(sessionId: string): void {
  if (!store.has(sessionId)) return;
  store.delete(sessionId);
  notify();
}

export function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

/** Re-renders the caller whenever the entry for sessionId changes. */
export function useGeneration(sessionId: string): GenerationEntry | undefined {
  const [, setTick] = useState(0);
  useEffect(() => {
    return subscribe(() => setTick((n) => n + 1));
  }, []);
  return getGeneration(sessionId);
}

/** Re-renders the caller whenever any session starts or stops generating.
 *  Returns the set of currently-generating sessionIds. */
export function useGeneratingSet(): Set<string> {
  const [, setTick] = useState(0);
  useEffect(() => {
    return subscribe(() => setTick((n) => n + 1));
  }, []);
  const result = new Set<string>();
  for (const [id, entry] of store) {
    if (entry.generating) result.add(id);
  }
  return result;
}
