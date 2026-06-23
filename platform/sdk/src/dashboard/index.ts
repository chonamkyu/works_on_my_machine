/**
 * React hooks that workflow dashboards use to access platform data.
 *
 * Phase 0 ships type stubs; Phase 2 wires up the federated remote runtime.
 */

import type { MemoryEntry } from "../index.ts";

export interface WorkflowKvHook<T> {
  data: T | null;
  loading: boolean;
  error: Error | null;
}

export function useWorkflowKv<T>(_key: string): WorkflowKvHook<T> {
  return { data: null, loading: false, error: null };
}

export interface RunHistoryEntry {
  runId: string;
  state: string;
  startedAt: string;
  durationMs: number;
}

export function useRunHistory(_taskId: string, _limit = 20): RunHistoryEntry[] {
  return [];
}

export function useMemoryByTopic(_topic: string): MemoryEntry[] {
  return [];
}
