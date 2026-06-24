/**
 * Dashboard surface for workflow authors.
 *
 * - `dashboard.serve(handler)`   — spin an HTTP server on PLATFORM_PORT
 * - `dashboard.serveHtml(html)`  — one-page convenience
 * - `dashboard.route([...])`     — method+path routing
 *
 * The control plane reverse-proxies `/d/<task_id>/*` to the workflow's
 * loopback port for any `kind: service` workflow that was given one.
 *
 * The React hooks below are stubs; they'll get a real wiring when the
 * dashboard runtime gains a fetch helper.
 */

export { dashboard } from "./server.js";

import type { MemoryEntry } from "../index.js";

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
