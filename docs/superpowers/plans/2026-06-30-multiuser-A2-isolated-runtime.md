# Multi-user A2 — Per-Workspace Isolated Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move workflow execution behind a per-workspace `SupervisorClient` so each workspace's runs, services, credentials, reverse-proxy, and triggers are isolated — with `process`, `container`, and `k8s` backends selected by `PLATFORM_RUNTIME`.

**Architecture:** The control plane stops spawning Node directly. The existing `runner.Runner` interface is promoted to a `SupervisorClient`; a `process` backend wraps today's `NodeRunner`/`ServiceManager` under per-workspace working directories; a `container` backend runs one podman/docker container per workspace; a `k8s` backend targets one pod per workspace namespace (interface + manifest, cluster rollout deferred). The reverse-proxy and cron scheduler become workspace-aware.

**Tech Stack:** Go 1.25 + chi v5; podman/docker CLI; Kubernetes client-go (k8s backend); robfig/cron.

**Depends on:** Plan A1 (auth, workspace model, `auth.MustWorkspaceID`, `workspace_id` on tasks/runs). Do not start until A1 is merged.

## Global Constraints

- All source/comments/UI strings in English; no AI-tool commit trailers.
- `PLATFORM_RUNTIME` ∈ {`process` (default), `container`, `k8s`}.
- Per-workspace workflow trees live at `~/.platform/ws/<workspace_id>/workflows/<task_id>/` (was `~/.platform/workflows/<task_id>/`).
- Unregistered proxy port names return 404 (never forward arbitrary loopback ports) — preserve current policy.
- Legacy `/d/<task>/...` proxy path keeps working as an alias resolving the task's owning workspace.
- Trusted-team threat model: `process` isolation is logical (separate dirs/env/creds); hard isolation comes from `container`/`k8s`.

---

### Task 1: SupervisorClient interface

**Files:**
- Create: `platform/api/internal/supervisor/supervisor.go`
- Modify: `platform/api/internal/runner/iface.go`

**Interfaces:**
- Produces:
```go
package supervisor

type Client interface {
	StartRun(ws string, task *store.Task, trigger string) *store.Run
	CancelRun(ws, runID string) bool
	StopService(ws, taskID string) error
	EnsureService(ws string, task *store.Task) error
	// Endpoint returns the base URL + allocated port map for a task's running
	// surfaces, used by the reverse-proxy. ok=false if not running.
	Endpoint(ws, taskID string) (base string, ports map[string]int, ok bool)
	Health(ws string) Status
}
type Status struct { Up bool; Detail string }
```
- The existing `runner.Runner` (`Start`, `Cancel`) is kept and adapted by the `process` backend; `Client` is the new control-plane-facing seam.

- [ ] **Step 1:** Write `supervisor.go` with the `Client` interface + `Status` (no impl yet) and a `NewClient(kind string, deps Deps) (Client, error)` factory switching on `PLATFORM_RUNTIME`.
- [ ] **Step 2:** `cd platform/api && go build ./internal/supervisor/`. Expected: compiles (factory returns `errUnsupported` for unimplemented kinds for now).
- [ ] **Step 3:** Commit `"Add SupervisorClient interface + factory"`.

---

### Task 2: process backend (per-workspace dirs)

**Files:**
- Create: `platform/api/internal/supervisor/process.go`
- Modify: `platform/api/internal/store/workflowfs.go` (path now includes workspace), `platform/api/internal/runner/node.go` (accept a workspace-scoped root)
- Test: `platform/api/internal/supervisor/process_test.go`

**Interfaces:**
- Consumes: `runner.NodeRunner`, `runner.ServiceManager`, `store.WorkflowFS`.
- Produces: `type Process struct{...}` implementing `Client`; spawns under `~/.platform/ws/<ws>/workflows/<task>/`; injects per-workspace decrypted creds + env.

- [ ] **Step 1: failing test** — `StartRun("wsA", task, "manual")` writes/spawns under a temp root containing `wsA`, and `Endpoint` returns the allocated ports; a second workspace `wsB` gets a separate directory.
- [ ] **Step 2:** Run to verify it fails.
- [ ] **Step 3:** Implement: make `WorkflowFS` take a `(workspaceID, taskID)` path key; wrap `NodeRunner.Start` so the spawn cwd + `PLATFORM_TOKEN_*` env are resolved per workspace; delegate `CancelRun` to the node runner's cancel map keyed by `(ws,runID)`.
- [ ] **Step 4:** Run to verify it passes; `go build ./...`.
- [ ] **Step 5:** Commit `"Add process supervisor backend (per-workspace dirs/creds)"`.

---

### Task 3: Route control-plane run/cancel/service through the supervisor

**Files:**
- Modify: `platform/api/cmd/api/main.go` (construct `supervisor.Client`, pass to handler + scheduler + service manager)
- Modify: `platform/api/internal/handler/handler.go` (TriggerRun, CancelRun, SetServiceEnabled call the supervisor with the task's workspace)
- Modify: `platform/api/internal/runner/dispatch.go` (dispatcher becomes workspace-aware or is wrapped by the supervisor)

**Interfaces:**
- Consumes: Task 1-2.
- Produces: every run start/cancel/service toggle goes through `supervisor.Client` with `ws := task.WorkspaceID`.

- [ ] **Step 1: failing test** — handler `TriggerRun` for a task in `wsA` calls the supervisor with `ws=="wsA"` (inject a fake `Client`, assert the recorded ws).
- [ ] **Step 2-4:** run-fail → implement → run-pass → `go build ./...`.
- [ ] **Step 5:** Commit `"Route runs/services through the supervisor per workspace"`.

---

### Task 4: Per-workspace reverse-proxy

**Files:**
- Modify: `platform/api/internal/handler/handler.go` (`ServeDashboard` at the `/d/{taskId}/*` handler)
- Modify: `platform/api/cmd/api/main.go` (add `/w/{ws}/d/{taskId}/*` route)
- Test: `platform/api/internal/handler/proxy_test.go`

**Interfaces:**
- Consumes: `supervisor.Client.Endpoint`, `auth` membership, `store.GetTask`.
- Produces: `/w/<ws>/d/<task>/...` routes to that workspace's runtime, authorized by membership; legacy `/d/<task>/...` resolves the task's owning workspace and applies the same authorization.

- [ ] **Step 1: failing test** — a member of `wsA` proxying `/w/wsA/d/<task>/` reaches the fake endpoint; a non-member gets 403; an unregistered port name gets 404.
- [ ] **Step 2-4:** run-fail → implement (resolve endpoint via supervisor, reuse existing `httputil.ReverseProxy` wiring, keep the unregistered-name 404 policy) → run-pass.
- [ ] **Step 5:** Commit `"Per-workspace reverse-proxy with membership authz + legacy alias"`.

---

### Task 5: Workspace-aware cron scheduler + trigger health fields

**Files:**
- Modify: `platform/api/internal/scheduler/cron.go`
- Modify: `platform/api/internal/store/memory.go` (add `ListAllWorkspaceIDs()`, `ListTasksAllWorkspaces()` or iterate per workspace; persist trigger health)
- Migration: `platform/api/internal/db/migrations/010_trigger_health.sql`
- Test: `platform/api/internal/scheduler/cron_test.go`

**Interfaces:**
- Consumes: `supervisor.Client.StartRun`, workspace list.
- Produces: cron Sync iterates tasks across all workspaces and dispatches each fire to `supervisor.StartRun(task.WorkspaceID, task, "cron")`; each fire records `last_fired_at`, computes `next_fire_at`, captures `last_error`.

- [ ] **Step 1:** Migration adds `trigger_state(task_id, trigger_id, last_fired_at, next_fire_at, last_error, PRIMARY KEY(task_id,trigger_id))`.
- [ ] **Step 2: failing test** — a cron entry firing records `last_fired_at` and dispatches with the task's workspace (fake supervisor).
- [ ] **Step 3-4:** run-fail → implement (replace `ListTasks(DefaultUserID)` — now removed — with a cross-workspace listing; record health) → run-pass.
- [ ] **Step 5:** Commit `"Workspace-aware cron dispatch + trigger health fields"`.

---

### Task 6: container backend (podman/docker)

**Files:**
- Create: `platform/api/internal/supervisor/container.go`
- Test: `platform/api/internal/supervisor/container_test.go` (skipped if no `podman`/`docker` on PATH)

**Interfaces:**
- Produces: `type Container struct{...}` implementing `Client`; one container per active workspace, mounting the workspace's workflow dir, publishing the allocated ports, injecting creds via env. Lifecycle: lazy start on first run, stop on idle (no running services + no active run for N minutes).

- [ ] **Step 1: failing test** (guarded by runtime presence) — `EnsureService`/`StartRun` launches a container named `platform-ws-<ws>` and `Endpoint` returns published ports; `StopService` removes it.
- [ ] **Step 2-4:** run-fail (or skip) → implement using `exec.Command("podman", "run"/"rm"/"inspect", ...)` with a small image that runs the same Node entrypoint → run-pass.
- [ ] **Step 5:** Commit `"Add container supervisor backend (podman/docker, one per workspace)"`.

---

### Task 7: k8s backend (interface impl + manifest)

**Files:**
- Create: `platform/api/internal/supervisor/k8s.go`
- Create: `platform/infra/k8s/supervisor-pod.yaml` (template)
- Test: `platform/api/internal/supervisor/k8s_test.go` (uses a fake clientset; no live cluster)

**Interfaces:**
- Produces: `type K8s struct{...}` implementing `Client`; one namespace+pod per workspace (`platform-ws-<ws>`), idle scale-to-zero, warm if a `kind=service` task exists. Endpoint resolves the pod's Service ClusterIP/port. Cluster rollout (ArgoCD/manifests beyond the template) is **out of scope** — this task lands a working interface against a fake clientset + a deployable manifest template.

- [ ] **Step 1:** Add `k8s.io/client-go` (+ `apimachinery`) to `go.mod`.
- [ ] **Step 2: failing test** — against `fake.NewSimpleClientset()`, `EnsureService("wsA", svcTask)` creates a namespace + pod + service; `Health` reports up when the fake pod is Running.
- [ ] **Step 3-4:** run-fail → implement (create namespace, apply pod/service from the template, watch readiness) → run-pass.
- [ ] **Step 5:** Commit `"Add k8s supervisor backend (fake-clientset tested) + pod manifest"`.

---

### Task 8: Runtime selection + idle lifecycle + end-to-end

**Files:**
- Modify: `platform/api/cmd/api/main.go` (factory selects backend from `PLATFORM_RUNTIME`; start an idle-reaper goroutine)
- Modify: `platform/.env.example` (already has `PLATFORM_RUNTIME` from A1 spec — ensure documented)
- Test: end-to-end script under `platform/scripts/`

**Interfaces:**
- Produces: boot wires the configured backend; idle workspaces with no services + no active runs are reaped (`container`/`k8s`); `process` is always-resident.

- [ ] **Step 1:** Implement the factory switch + an idle-reaper ticker that calls `StopService`/pod-delete for idle workspaces.
- [ ] **Step 2:** Boot in `process` mode, run a task in two different workspaces, confirm separate dirs + correct proxy routing.
- [ ] **Step 3:** Boot in `container` mode (if podman present), confirm one container per workspace and idle reap.
- [ ] **Step 4:** `cd platform/api && go test ./...` (k8s/container tests skip without deps). Expected: PASS.
- [ ] **Step 5:** Commit `"Select runtime backend by PLATFORM_RUNTIME + idle reaper"`.

---

## Self-Review

**Spec coverage** (against `2026-06-30-multiuser-platform-foundation-design.md` §5.4, §5.5):
- SupervisorClient abstraction → Task 1. ✓
- process / container / k8s backends → Tasks 2,6,7. ✓
- runs/services through supervisor → Task 3. ✓
- per-workspace reverse-proxy + legacy alias → Task 4. ✓
- workspace-aware triggers + health fields → Task 5. ✓
- runtime selection + idle lifecycle → Task 8. ✓

**Placeholder scan:** Backend-impl tasks (2,6,7) give a concrete failing test, the exact subprocess/clientset mechanism, and the commit message; per-backend body detail is deferred to execution because it depends on the existing `NodeRunner.Start` internals (read `runner/node.go` fully before Task 2). This is flagged, not silent.

**Type consistency:** `Client` interface (Task 1) is the single seam consumed by Tasks 3,4,5,8; `Endpoint(ws,taskID)->(base,ports,ok)` is used identically by the proxy (Task 4). `task.WorkspaceID` (from A1 Task 11) is the workspace key throughout.

## Execution notes

- A2 must run after A1 is merged; it depends on `auth.MustWorkspaceID`, `task.WorkspaceID`, and the removal of `DefaultUserID`.
- Read `runner/node.go` and `runner/service.go` in full before Task 2 — the process backend reuses their spawn/cancel/keepalive logic; do not reimplement it.
- The k8s backend is intentionally test-against-fake only; real cluster deployment (ArgoCD app, image build/push) is a follow-on, consistent with the spec's non-goals.
