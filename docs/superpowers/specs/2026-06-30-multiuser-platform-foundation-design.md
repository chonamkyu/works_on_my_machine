# Multi-user platform foundation (Spec A)

Date: 2026-06-30
Status: Draft for review
Scope: Sub-project **A** of a larger multi-user evolution (A → B/C/D, see Roadmap).

## 1. Problem

The platform is effectively single-user. `store.DefaultUserID` is hardcoded
across the control plane (≈40+ call sites in `api/internal/handler/handler.go`
alone) and every domain query, memory entry, credential, OAuth flow, skill
overlay, and task list resolves to that one constant. There is no identity
layer: no middleware extracts "who is calling" from a request.

The data model already anticipates multi-tenancy — `tasks`, `memory_entries`,
`user_profile`, etc. carry a `user_id` column, and per-user skill overlays
live at `prompts/users/<userId>/skills/` — but the runtime pins everything to
`DefaultUserID`. The runner is likewise already abstracted: `runner.Runner`
is an interface, and `node.go` documents the intended next step verbatim:

> "Phase 1: spawn directly from the control plane. Phase 2 will move this
> behind the supervisor in a per-user pod."

`PLATFORM_SUPERVISOR_HOST` / `PLATFORM_SUPERVISOR_PORT` env vars are already
reserved. This spec realizes that Phase 2 and the identity layer it depends
on.

## 2. Goals / non-goals

**Goals (this spec, A1 + A2):**

- Authenticate users via Keycloak OIDC (SSO), matching the in-house pattern
  used by `../llmgate` and `../forge`.
- Introduce a native `User` + `Workspace` + RBAC model. **No external authz
  service** (`../grant` is *not* used); permissions and user management are
  implemented in-platform.
- Replace every `store.DefaultUserID` use with workspace-scoped resolution.
- Isolate each workspace's workloads behind a `SupervisorClient` abstraction
  with three backends: `process`, `container`, `k8s`.
- Per-workspace reverse-proxy and trigger dispatch with membership
  authorization.
- A cross-cutting `audit_log` (write infrastructure + key events).
- Re-wire the self-evolve loop to target a **global** scope rather than a
  per-user overlay (the global skill registry itself is sub-project B).

**Non-goals (deferred to B/C/D — see Roadmap):**

- The global Skill repository UI + self-evolve observability timeline (B/D).
- The central MCP registry + health loop (C).
- The full admin dashboard + audit-log viewer + trigger-health dashboard (D).
- Production k8s deployment wiring (ArgoCD/manifests beyond a basic
  supervisor manifest). The `k8s` backend ships as interface + basic
  manifest; cluster rollout is follow-on.

## 3. Reference: what we port from llmgate

llmgate already implements nearly every pattern we need (in Go + Fiber; we
adapt to Go + chi). The spec is deliberately a **port-and-adapt**, not a
green-field invention.

| llmgate source | We port to | Used for |
|---|---|---|
| `backend/internal/middleware/auth.go` | `api/internal/auth/` (chi) | Keycloak JWT decode, `Claims`, admin gating, `WorkspaceMiddleware` |
| `backend/internal/workspace/{model,service,handler}.go` | `api/internal/store/workspace.go` + handlers | `EnsurePersonal` (personal ws id = `sub`), create/invite/members |
| `backend/internal/user/{model,service,handler}.go` | `api/internal/store/user.go` + handlers | user list, roles, admin toggle |
| `backend/internal/skill/*` (GatewayAudit) | `api/internal/store/audit.go` | audit log table + helpers |

Key adaptation: llmgate decodes the Keycloak JWT **without verifying the
signature** ("the frontend validates the token with Keycloak; the backend
trusts it"). We keep this convention for consistency with the in-house
stack. (A JWKS-verifying path can be added later behind a flag; out of scope
here.)

Note: llmgate currently collapses tenancy into `SharedTeamID = "shared"`
(single-tenant) even though the scaffolding for groups/workspaces exists. We
do **not** collapse — we use the full workspace model.

## 4. Architecture

```
                  Keycloak SSO (in-house realm)
                          | OIDC (browser validates token)
+--------------- shared control plane (api :8787, chi) ---------------+
|  authMiddleware   -> Claims{sub, groups, roles, email, dept} in ctx |
|  workspaceMiddleware -> X-Workspace-ID -> ctx workspace_id          |
|  RBAC (owner/member/viewer; platform-admin)                         |
|  audit_log (cross-cutting writes)                                   |
|  GLOBAL: self-evolve targets global scope (registry body = B)       |
|  SupervisorClient -- resolves workspace -> runtime endpoint         |
|  reverse-proxy /w/<ws>/d/<task>/... (membership-authorized)         |
+----------------+-----------------------------+----------------------+
        start/stop/cancel run          start/stop/cancel run
        +-------v--------+             +-------v--------+
        | ws A runtime   |             | ws B runtime   |   ... hundreds
        | (proc|ctr|pod) |             | (proc|ctr|pod) |
        |  NodeRunner    |             |  NodeRunner    |
        |  ServiceMgr    |             |  ServiceMgr    |
        |  workflows/    |             |  workflows/    |
        |  creds (isol.) |             |  creds (isol.) |
        +----------------+             +----------------+
```

The control plane no longer spawns workflows directly. `runner.Runner` is
promoted to a `SupervisorClient`; the control plane delegates start/stop/
cancel to the owning workspace's supervisor.

## 5. Components

### 5.1 Auth & identity (`api/internal/auth/`)

- chi middleware `Authenticate` reads `Authorization: Bearer <jwt>`, decodes
  the payload (no signature check), builds `Claims{Sub, Email,
  PreferredUsername, Groups, Roles, Department}`, stores it in
  `context.Context` (chi equivalent of llmgate's `c.Locals("claims")`).
- `MustUserID(ctx) == claims.Sub`. `MustWorkspaceID(ctx)` (see 5.2).
- Admin resolution: `PLATFORM_ADMIN_USERS` (comma list, by sub or
  preferred_username) ∪ `platform-admin` realm role ∪ `platform-admin` group
  ∪ DB `users.is_admin`. `AdminOnly` middleware → 403 otherwise.
- **Dev mode**: `PLATFORM_AUTH_DEV=1` injects a fixed dev `Claims`
  (`sub=dev`, `platform-admin`) so the stack runs locally without Keycloak.
  This is the migration path off `DefaultUserID`: dev mode reproduces
  today's single-user behavior under a real `sub`.

### 5.2 Workspace / user / RBAC (`api/internal/store/{workspace,user}.go`)

- **Personal workspace**: `EnsurePersonal(sub)` creates a workspace whose id
  *is* the user's `sub` on first login. Personal is just a workspace with one
  member (owner). Unifies "personal" and "team" handling, per decision.
- **Shared workspaces**: create / invite / remove-member / list-members.
  Roles: `owner | member | viewer`.
- `workspaceMiddleware`: if `X-Workspace-ID` is empty or `== sub`, resolve to
  personal; else verify `workspace_members(workspace_id, user_id)` exists —
  403 for non-members. Stores `workspace_id` in ctx.
- **Native user management**: list users, view roles, toggle admin, disable a
  user. Gated by `AdminOnly`.

### 5.3 Tenancy refactor (remove `DefaultUserID`)

- Add `workspaceID string` (and where needed `userID string`) to store method
  signatures currently keyed on `DefaultUserID`.
- Replace every `store.DefaultUserID` call site in handlers with
  `workspaceID := auth.MustWorkspaceID(r.Context())`.
- Bus channels that embed user/workspace (`task:created:<id>`,
  `chat:progress:<session>`, etc.) become workspace-scoped; SSE filtering
  already checks `ev.UserID` — extend to workspace.
- The constant `store.DefaultUserID` is deleted once no references remain.

### 5.4 Per-workspace isolated runtime (`api/internal/runner/`, `api/internal/supervisor/`)

- `SupervisorClient` interface: `StartRun(ws, task, trigger) -> Run`,
  `CancelRun(ws, runID)`, `StopService(ws, taskID)`, `Health(ws)`. Promoted
  from today's `Runner` (which already abstracts MockRunner / NodeRunner).
- Backends selected by `PLATFORM_RUNTIME`:
  - **`process`** (default; dev): supervisor spawns Node child processes in
    per-workspace dirs `~/.platform/ws/<ws>/workflows/<task>/`, with
    per-workspace env and decrypted creds. Wraps today's `NodeRunner` /
    `ServiceManager` with a workspace path prefix. Zero infra.
  - **`container`** (dev parity): one podman/docker container per workspace
    (filesystem, network, resource limits). Project already runs `podman
    compose`, so podman is present.
  - **`k8s`** (prod): one pod in a per-workspace namespace; supervisor runs as
    the in-pod agent. Idle workspaces scale to zero; a request spins one up; a
    workspace with a long-running `kind=service` task stays warm. Ships as
    interface impl + a basic supervisor manifest; cluster rollout is follow-on.
- `ServiceManager` (keeps `kind=service` daemons alive) moves behind the
  supervisor so service lifetime is per-workspace.

### 5.5 Reverse-proxy & triggers per workspace

- Proxy path becomes `/w/<ws>/d/<task>/...` (was `/d/<task>/`); the handler
  resolves the workspace runtime endpoint via the supervisor and authorizes
  the caller's membership. Unregistered port names still 404 (current policy).
  **Back-compat**: the legacy `/d/<task>/...` path is kept as an alias that
  resolves the task's owning workspace, so URLs already emitted by existing
  workflows (and `PLATFORM_PUBLIC_URL`-based absolute links) keep working;
  authorization still applies.
- The cron scheduler (`api/internal/scheduler/cron.go`) iterates tasks per
  workspace and dispatches each fire to the owning workspace's supervisor
  (today it iterates `ListTasks(DefaultUserID)` against one global cron).
- Each trigger records `last_fired_at`, `next_fire_at`, `last_error`. This is
  the data foundation for "are triggers actually connected?" — the full
  health dashboard is sub-project D, but correctness + the fields land here.

### 5.6 Audit logging (`api/internal/store/audit.go`)

- Table `audit_log(id, actor_sub, actor_email, action, resource_type,
  resource_id, resource_name, workspace_id, details JSONB, created_at)` —
  modeled on llmgate `GatewayAudit`.
- Write helper + recording at: login (first-seen), workspace
  create/invite/member-change, task create/delete, run start/cancel,
  credential change, admin actions.
- Query/viewer UI is **D**; only write infrastructure + events are in A.

### 5.7 Self-evolve global wiring

- Today self-evolve writes a per-user skill overlay
  (`prompts/users/<userId>/skills/`). Per the decision that self-evolve must
  be **shared across all workspaces**, A re-targets the loop to a global
  scope. The global skill *registry* (llmgate-style versioned/immutable/alias
  store, the "view-at-a-glance" browser, and the proposal→accept→effect
  observability timeline) is **sub-project B**; A only fixes the scope wiring
  so proposals are global, not per-user.

## 6. Data model / migrations

New migration(s) under `api/internal/db/migrations/` (next index after
`008_chat_handler.sql`, e.g. `009_multiuser.sql`):

- `users(sub PK, email, preferred_username, display_name, is_admin,
  disabled, created_at, last_seen_at)`
- `workspaces(id PK, name, kind {personal|shared}, owner_sub, created_at)`
- `workspace_members(workspace_id, user_id, role {owner|member|viewer},
  added_at, PRIMARY KEY(workspace_id, user_id))`
- `audit_log(...)` as in 5.6
- Add `workspace_id` to existing tenant-scoped tables (`tasks`,
  `memory_entries`, `user_profile`, `user_profile_suggestions`,
  `skill_proposals`, `evolution_prs`, `task_archive`, `sessions`,
  `messages`, `credentials`, `task_index`). Composite indexes become
  `(workspace_id, ...)`; pgvector / pg_trgm indexes likewise.
- **No backfill — existing data is disposable** (confirmed by the user). The
  migration may truncate tenant-scoped tables before adding the
  `workspace_id` column; there is no legacy-row migration to preserve. A
  `default` workspace + dev user are seeded so dev mode (`PLATFORM_AUTH_DEV=1`)
  has a working tenant out of the box.

## 7. Configuration (`platform/.env`)

Add, matching the llmgate/forge convention (real realm values filled in by
the operator; start in dev mode):

```bash
# --- Auth (Keycloak OIDC) ---
KEYCLOAK_ISSUER=https://sso.example/realms/<realm>   # placeholder
KEYCLOAK_CLIENT_ID=platform                          # placeholder
PLATFORM_ADMIN_USERS=                                # comma list (sub or username)
PLATFORM_AUTH_DEV=1                                  # 1 = bypass Keycloak locally

# --- Runtime substrate ---
PLATFORM_RUNTIME=process                             # process | container | k8s
```

Frontend (`web/`, `react-oidc-context`): `authority = KEYCLOAK_ISSUER`,
`client_id = KEYCLOAK_CLIENT_ID`, scope `openid profile email`, with an
`X-Workspace-ID` header set on API calls from the active-workspace switcher.

## 8. Error handling

- Missing/invalid token → 401 (dev mode bypasses).
- Non-member accessing a workspace (API or proxy) → 403.
- Supervisor unreachable for a workspace → run marked failed with a clear
  error; `Health(ws)` surfaces the condition (no silent hang).
- Migration is idempotent; since legacy data is disposable, tenant tables may
  be reset rather than migrated.

## 9. Testing

- Unit: JWT decode + `Claims` (llmgate `virtualkey_test` style), admin
  resolution, `workspaceMiddleware` membership checks.
- Integration: workspace isolation — workspace A cannot read workspace B's
  tasks/memory/creds; proxy authorization; dev↔Keycloak mode switch.
- Supervisor: `process` backend start/cancel/health; `container` backend
  smoke test (skipped if podman absent); `k8s` interface conformance against
  a fake.
- Migration: applies cleanly on an empty/reset DB; seeded `default` workspace
  + dev user exist; tenant tables are workspace-scoped from the start.

## 10. Roadmap (out of scope for this spec)

Each gets its own spec → plan → implementation cycle, all porting the
corresponding llmgate package:

- **B — Global Skills repository**: llmgate `skill` package
  (name@version, immutable versions, `@latest` alias, `Resolve` for runtime
  inlining). Self-evolve proposals land here; "view-at-a-glance" browser;
  proposal→accept→effect observability timeline.
- **C — Central MCP registry**: llmgate `mcp` package (server register/update,
  per-tool disable, `RecordCall` stats/sparklines, `Ping` + `RunHealthLoop`).
  Per-workspace enablement; today's `tools/mcp-server` becomes a client.
- **D — Admin & observability**: full admin dashboard, audit-log viewer,
  trigger-health/connection dashboard, self-evolve tracking.

## 11. Open questions / risks

- **JWT signature verification**: we follow the in-house "trust the frontend"
  convention. If the threat model later requires backend verification, add a
  JWKS path behind a flag (not in A).
- **Legacy data dropped**: existing single-user data is intentionally
  discarded (user-confirmed). No production data exists yet, so this is safe;
  if that changes before rollout, revisit with a backfill step.
- **`process` backend isolation is logical, not hard**: acceptable given the
  trusted-team (hundreds, internal) threat model; hard isolation comes with
  `container`/`k8s`.
