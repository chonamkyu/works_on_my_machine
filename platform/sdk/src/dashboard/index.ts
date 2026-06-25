/**
 * Dashboard surface for workflow authors.
 *
 * Single-file shortcuts:
 *   - `dashboard.serve(handler)`   — raw http server on PLATFORM_PORT
 *   - `dashboard.serveHtml(html)`  — one static HTML page
 *   - `dashboard.route([...])`     — method+path routing for small surfaces
 *
 * Multi-file projects (real api/ + ui/ structure):
 *   - `dashboard.api(routes, { mount: "/api" })` — typed JSON routes, auto
 *     /openapi.json + /_docs (Swagger UI) at the mount path
 *   - `dashboard.serveDir("./ui", { fallback: "index.html" })` — static files
 *   - `dashboard.bundle({ api, ui })` — combine the two in one call
 *
 * The control plane reverse-proxies `/d/<task_id>/*` to the workflow's
 * loopback port for any `kind: service` workflow that was given one.
 */

export { dashboard } from "./server.js";
export type { RouteDef, RouteSchema, ApiOpts } from "./api.js";

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
