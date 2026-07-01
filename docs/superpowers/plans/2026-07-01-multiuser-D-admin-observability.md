# Multi-user D — Admin Dashboard & Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** An admin dashboard that makes the multi-user platform observable: an overview of users/workspaces/workloads, a user & workspace management surface, an audit-log viewer, a trigger-health ("are triggers actually connected?") view, and a self-evolve tracking view (proposal → accept → resulting skill version) so the self-evolution loop is trackable. The data infrastructure (audit_log, trigger_state, skill_proposals, evolution_prs, skills registry, mcp health) was built in A/B/C; D adds admin cross-workspace read endpoints + the frontend.

**Architecture:** New admin-only cross-workspace read endpoints on the chi control plane (overview counts, all-workspaces, all-trigger-state, self-evolve timeline), all under the existing `authMW.AdminOnly` group. A React `AdminView` (tabbed: Overview / Users / Workspaces / Audit / Triggers / Self-Evolve) mounted like the other overlays (`?overlay=admin` + `⌘K`), gated on `/v1/me` `isAdmin`.

**Tech Stack:** Go 1.26 + chi + pgx; React 18 + Vite. Reuses existing stores (users, workspaces, audit_log, trigger_state, skill_proposals, evolution_prs, skills, mcp).

## Global Constraints

- All source/comments/UI strings in English. Commits never include AI-tool trailers (local `commit-msg` hook enforces).
- ALL new endpoints are admin-only (`authMW.AdminOnly` group) and cross-workspace by nature. Non-admin → 403.
- Skills/self-evolve are GLOBAL (`store.GlobalScope`) per B; the self-evolve timeline reads global skill proposals + all-workspace evolution PRs + the skills registry.
- Live DB at `PLATFORM_DB_URL=postgres://platform:platform@localhost:5432/platform?sslmode=disable`.
- `store.Memory{ pool *pgxpool.Pool }`. Existing: `ListUsers`, `SetUserAdmin`, `SetUserDisabled`, `ListAudit(workspaceID, limit)` (workspaceID=="" → all), `ListTriggerState(workspaceID)`, `ListSkillProposals(workspaceID, status)`, `EvolutionStore.List(workspaceID)`, `ListSkills`. Auth: `auth.IsAdmin`, `authMW.AdminOnly`.
- Read-only observability — no destructive admin actions beyond the existing user admin/disable toggles.

---

### Task 1: Admin observability read endpoints

**Files:**
- Create: `platform/api/internal/handler/admin_handler.go`
- Modify: `platform/api/cmd/api/main.go` (routes, in the existing `AdminOnly` group)
- Add store methods: `platform/api/internal/store/admin.go` (or extend existing files)
- Test: `platform/api/internal/handler/admin_handler_test.go` + store test

**Interfaces (all admin-gated):**
- `GET /v1/admin/overview` → `{users, workspaces, mcpServers, skills, runningServices, activeRuns, pendingProposals, pendingEvolutionPRs}` counts + `recentAudit` (last ~10).
- `GET /v1/admin/workspaces` → all workspaces with `{id, name, kind, ownerSub, memberCount, taskCount, createdAt}` (cross-workspace).
- `GET /v1/admin/triggers` → all `trigger_state` across workspaces, joined to tasks for `{taskId, taskName, workspaceId, triggerId, lastFiredAt, nextFireAt, lastError}`.
- `GET /v1/admin/self-evolve` → `{proposals: [...], evolutionPRs: [...], recentSkillVersions: [...]}` — global skill proposals (all statuses) + all-workspace evolution PRs + the latest skill versions, so the UI can render the proposal→accept→effect timeline.
- New store methods as needed: `AdminOverviewCounts() (map or struct)`, `ListAllWorkspaces() []*WorkspaceWithCounts`, `ListAllTriggerState() []*TriggerStateWithTask`, `ListAllEvolutionPRs() []*EvolutionPR`. Reuse `ListSkillProposals(store.GlobalScope, "")` for proposals, `ListSkills()` for skills.

- [ ] **Step 1:** Add the store methods (parameterized cross-workspace queries + counts). Write a store test for `ListAllWorkspaces`/`ListAllTriggerState` (live DB, seed rows, assert cross-workspace results).
- [ ] **Step 2: failing handler test** — with injected ADMIN claims: `GET /v1/admin/overview` returns the counts JSON; `GET /v1/admin/triggers` returns rows; a NON-admin gets 403 on each (they're in the AdminOnly group).
- [ ] **Step 3:** Implement handlers + register routes in the `AdminOnly` group. `go build ./...`.
- [ ] **Step 4:** run tests (live DB), pass.
- [ ] **Step 5:** Commit `Add admin observability endpoints (overview/workspaces/triggers/self-evolve)`.

---

### Task 2: Admin dashboard shell + Overview + Users/Workspaces management

**Files:**
- Create: `platform/web/src/components/AdminView.tsx`
- Modify: `platform/web/src/lib/api.ts`, `platform/web/src/components/SettingsOverlay.tsx`, `platform/web/src/components/CommandPalette.tsx`

**Interfaces:**
- Consumes: `/v1/admin/overview`, `/v1/admin/users`, `/v1/admin/users/{sub}/admin`, `/v1/admin/users/{sub}/disabled`, `/v1/admin/workspaces`, `/v1/me`.
- Produces: an `AdminView` with an internal tab bar (Overview / Users / Workspaces / Audit / Triggers / Self-Evolve — the last three are Tasks 3-4; stub them as empty tabs here). The whole view is gated: if `!me.isAdmin`, render "admin only". Overview tab shows the count cards + recent audit. Users tab lists users with admin/disable toggles (calls the existing endpoints). Workspaces tab lists all workspaces with member/task counts. Mount `?overlay=admin` + a `⌘K` "Admin dashboard" entry, matching the Skills/MCP tab pattern.

- [ ] **Step 1:** api.ts admin client methods + types (`AdminOverview`, `AdminWorkspace`, reuse `User`).
- [ ] **Step 2:** `AdminView.tsx` shell with tab bar + Overview + Users + Workspaces tabs (admin-gated). Match existing view style + URL-state (`?overlay=admin`, and an `adminTab` param for the sub-tab if consistent with the codebase's URL-state approach).
- [ ] **Step 3:** Mount in SettingsOverlay (`OverlayTab` += `admin`) + CommandPalette entry.
- [ ] **Step 4:** `cd platform/web && CI=true pnpm build` clean.
- [ ] **Step 5:** Commit `Frontend: admin dashboard (overview + users + workspaces)`.

---

### Task 3: Audit-log viewer tab

**Files:**
- Modify: `platform/web/src/components/AdminView.tsx` (Audit tab), `platform/web/src/lib/api.ts` (`listAudit` if not present)

**Interfaces:**
- Consumes: `GET /v1/admin/audit?limit=` (existing, workspaceID=="" → all).
- Produces: the Audit tab — a table of audit entries (time, actor, action, resource type/id/name, workspace) with a client-side filter box (by action/actor/workspace substring). Handles empty state + reasonable default limit (e.g. 200).

- [ ] **Step 1:** api.ts `listAudit(limit)` + `AuditEntry` type.
- [ ] **Step 2:** Audit tab table + filter, matching the existing view style.
- [ ] **Step 3:** `pnpm build` clean.
- [ ] **Step 4:** Commit `Frontend: admin audit-log viewer`.

---

### Task 4: Trigger-health + Self-evolve tracking tabs

**Files:**
- Modify: `platform/web/src/components/AdminView.tsx` (Triggers + Self-Evolve tabs), `platform/web/src/lib/api.ts`

**Interfaces:**
- Consumes: `GET /v1/admin/triggers`, `GET /v1/admin/self-evolve`.
- Produces:
  - **Triggers tab** — a table of all triggers across workspaces: task name, workspace, trigger id, last fired, next fire, last error — with a health indicator (green if fired recently & no error / amber if never fired / red if last_error set). This is the "are triggers actually connected?" view.
  - **Self-Evolve tab** — a chronological tracking view of the self-evolution loop: skill proposals (pending/accepted/rejected with rationale/confidence), evolution PRs, and the resulting skill versions in the registry. Render as a timeline/list so an operator can SEE the loop working (proposal → accept → new skill version). Link accepted proposals to their resulting skill (by name) where possible.

- [ ] **Step 1:** api.ts `adminListTriggers()`, `adminSelfEvolve()` + types.
- [ ] **Step 2:** Triggers tab (health-colored table) + Self-Evolve tab (timeline). Match existing style; empty states.
- [ ] **Step 3:** `pnpm build` clean.
- [ ] **Step 4:** Commit `Frontend: trigger-health + self-evolve tracking views`.

---

## Self-Review

**Spec coverage** (overarching design §10 D + original asks "어드민 대시보드", "audit 로깅 확인", "트리거가 잘 연결되는지 확인", "self-evolve 추적관찰"): endpoints (T1), dashboard+overview+users+workspaces (T2), audit viewer (T3), trigger-health + self-evolve tracking (T4). ✓

**Placeholder scan:** T2-T4 compress steps with concrete tab descriptions + commit messages; T1 lists exact endpoints + store methods. The URL-state sub-tab param is "match the codebase's existing approach" (confirm `App.tsx` pattern at execution).

**Type consistency:** admin endpoints (T1) consumed by T2-T4 UI; `isAdmin` from `/v1/me` gates the whole `AdminView`; `authMW.AdminOnly` gates every T1 route; reuses `User`/`AuditEntry`/`TriggerState`/`SkillProposal`/`EvolutionPR` shapes already defined.

## Notes
- D is read-only observability + the existing user admin/disable toggles. No new destructive actions.
- The self-evolve "effect" link (accepted proposal → skill version) is best-effort by skill name; a hard proposal→version FK is a possible future refinement.
