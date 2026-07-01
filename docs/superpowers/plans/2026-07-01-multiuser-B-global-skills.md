# Multi-user B — Global Skills Repository Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A global, versioned Skills repository (Claude Code `SKILL.md` format) — immutable numeric versions + mutable aliases (`@latest`) — that any authenticated user can browse and reference `name@version`, that the self-evolve loop publishes into, and that the generator resolves skill bodies from. Ported from `../llmgate`'s `internal/skill` package, adapted to a single GLOBAL scope (skills are shared across all workspaces, per the platform's global self-evolve decision).

**Architecture:** New Postgres tables `skills` / `skill_versions` / `skill_aliases` (no team scoping — global). A Go store + HTTP handlers on the chi control plane (reads open to any authenticated caller; writes gated by `platform-admin`). The self-evolve accept path publishes a new version into the registry instead of writing a per-user file. The control plane resolves the registry's `@latest` skill bodies and injects them into each `/generate` call (replacing the `_global` file overlay). A React "Skills" view browses skills/versions/aliases at a glance.

**Tech Stack:** Go 1.26 + chi + pgx; React 18 + Vite; ports llmgate `backend/internal/skill/{model,service,handler}.go` + `migrations/00050_skills.sql`.

## Global Constraints

- All source/comments/UI strings in English. Commits never include AI-tool trailers (a local `.git/hooks/commit-msg` enforces this).
- Skills are GLOBAL (no `team_id`/`workspace_id` column on `skills`): `UNIQUE(name)`. Any authenticated caller reads; only `platform-admin` (`auth.IsAdmin`) may create/publish/move-alias/delete.
- Versions are IMMUTABLE (never updated in place); a new version is a new row. `@latest` auto-moves to the newest version on publish.
- `name@version` refs: version is a numeric string (immutable) or an alias (`latest`, `stable`, …); a bare `name` means `name@latest`.
- Live DB at `PLATFORM_DB_URL=postgres://platform:platform@localhost:5432/platform?sslmode=disable`; migrations apply on boot; next index is `011`.
- Reuse the existing `RecordAudit` for skill mutations (action `skill.create` / `skill.publish` / `skill.move_alias` / `skill.delete`).
- `store.Memory` is `{ pool *pgxpool.Pool }`; methods hang off `*Memory`. Auth helpers: `auth.MustUserID`, `auth.IsAdmin`, `auth.AdminOnly`.
- Base/default skill files (`generator/prompts/skills/*.md`) stay as-is; the registry REPLACES the per-user/`_global` overlay layer only.

---

### Task 1: Skills schema migration

**Files:**
- Create: `platform/api/internal/db/migrations/011_skills.sql`

**Interfaces:**
- Produces tables `skills`, `skill_versions`, `skill_aliases` (global; adapted from llmgate `00050_skills.sql` with `team_id` removed and `UNIQUE(name)`).

- [ ] **Step 1: Write the migration** (adapt llmgate `00050_skills.sql`; drop `team_id`, make `name` globally unique):

```sql
-- 011_skills.sql — global Skills repository (SKILL.md: frontmatter + body).
-- Versions immutable; aliases mutable pointers (@latest auto-moves). Global scope.
CREATE TABLE IF NOT EXISTS skills (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL UNIQUE,          -- slug from frontmatter.name
    description text NOT NULL DEFAULT '',       -- mirrors latest version
    tags        text[] NOT NULL DEFAULT '{}',
    created_by  text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_skills_tags ON skills USING GIN (tags);

CREATE TABLE IF NOT EXISTS skill_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id     uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version_no   int NOT NULL,
    frontmatter  jsonb NOT NULL DEFAULT '{}'::jsonb,
    body         text NOT NULL,
    raw          text NOT NULL,                 -- assembled SKILL.md (frontmatter+body)
    description  text NOT NULL DEFAULT '',
    tags         text[] NOT NULL DEFAULT '{}',
    comment      text NOT NULL DEFAULT '',
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (skill_id, version_no)
);
CREATE INDEX IF NOT EXISTS idx_skill_versions_skill ON skill_versions(skill_id, version_no DESC);

CREATE TABLE IF NOT EXISTS skill_aliases (
    skill_id   uuid NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    alias      text NOT NULL,
    version_no int NOT NULL,
    updated_by text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, alias),
    FOREIGN KEY (skill_id, version_no) REFERENCES skill_versions(skill_id, version_no) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_skill_aliases_skill ON skill_aliases(skill_id);
```

- [ ] **Step 2:** Apply to dev DB via psql; confirm `\d skills`, `\d skill_versions`, `\d skill_aliases`. `cd platform/api && go build ./...`.
- [ ] **Step 3:** Commit `Add global skills repository schema`.

---

### Task 2: Skill store + ref parsing

**Files:**
- Create: `platform/api/internal/store/skill.go`
- Test: `platform/api/internal/store/skill_test.go`

**Interfaces (methods on `*Memory`; ported/adapted from llmgate `skill/service.go` + `model.go`):**
- Types: `Skill{ID,Name,Description string; Tags []string; LatestVersion int; CreatedBy,CreatedAt,UpdatedAt}`; `SkillVersion{ID,SkillID string; VersionNo int; Frontmatter json.RawMessage; Body,Raw,Description string; Tags []string; Comment,CreatedBy,CreatedAt}`; `SkillAlias{SkillID,Alias string; VersionNo int; UpdatedBy,UpdatedAt}`; `SkillRef{Name,Version string}`.
- `func ParseSkillRef(s string) (SkillRef, error)` — `name@version`, bare → `@latest`. (Port llmgate `ParseRef`.)
- `func (m *Memory) ListSkills() []*Skill`
- `func (m *Memory) GetSkill(name string) (*Skill, error)`
- `func (m *Memory) CreateSkill(createdBy, name, description string, tags []string, body, comment string) (*Skill, *SkillVersion, error)` — inserts skill + version 1 + `latest` alias. Assembles `raw` = frontmatter (name/description/tags) + body. Errors if name exists.
- `func (m *Memory) PublishSkillVersion(name, createdBy, description string, tags []string, body, comment string) (*SkillVersion, error)` — appends immutable version N+1, moves `latest` alias to it, mirrors description/tags onto `skills`.
- `func (m *Memory) ListSkillVersions(name string) ([]*SkillVersion, error)`
- `func (m *Memory) GetSkillVersion(name string, versionNo int) (*SkillVersion, error)`
- `func (m *Memory) ListSkillAliases(name string) ([]*SkillAlias, error)`
- `func (m *Memory) MoveSkillAlias(name, updatedBy, alias string, versionNo int) (*SkillAlias, error)`
- `func (m *Memory) ResolveSkill(ref SkillRef) (*SkillVersion, error)` — numeric → that version; alias → the version the alias points to. (Port llmgate `Resolve`.)
- `func (m *Memory) DeleteSkill(name, deletedBy string) error`

- [ ] **Step 1: Write failing test** — `TestSkills_CreatePublishResolveAlias`: create "s1" (v1) → `ResolveSkill(ParseSkillRef("s1"))` returns v1 body; `PublishSkillVersion("s1", ...)` → v2, `@latest` now resolves to v2, `ParseSkillRef("s1@1")` still resolves to v1; `ListSkillVersions` returns 2; `MoveSkillAlias("s1","u","stable",1)` → `s1@stable` resolves to v1. Live DB; `t.Cleanup` deletes "s1".
- [ ] **Step 2:** run-fail (`PLATFORM_DB_URL=... go test ./internal/store/ -run TestSkills -v`).
- [ ] **Step 3:** Implement `skill.go` porting llmgate logic (transactions for publish: insert version + upsert alias atomically; frontmatter assembled as YAML `---\nname: ...\ndescription: ...\ntags: [...]\n---\n` + body).
- [ ] **Step 4:** run-pass; `go build ./...`.
- [ ] **Step 5:** Commit `Add global skills store (versions, aliases, resolve)`.

---

### Task 3: Skill HTTP handlers + routes

**Files:**
- Create: `platform/api/internal/handler/skill_repo_handler.go`
- Modify: `platform/api/cmd/api/main.go` (routes)
- Test: `platform/api/internal/handler/skill_repo_handler_test.go`

**Interfaces:**
- Consumes: Task 2 store methods, `auth.MustUserID`, `auth.IsAdmin`, `RecordAudit`.
- Produces routes under `/v1/skill-repo` (distinct from the existing per-user `/v1/skills` overlay endpoints, which stay until Task 5 migrates them):
  - `GET /v1/skill-repo` → `ListSkills` (open to any authenticated caller)
  - `GET /v1/skill-repo/{name}` → skill + versions + aliases
  - `GET /v1/skill-repo/{name}/versions/{n}` → a version's body/raw
  - `GET /v1/skill-repo/resolve?ref=name@ver` → resolved version
  - `POST /v1/skill-repo` (admin) → create; `POST /v1/skill-repo/{name}/versions` (admin) → publish; `POST /v1/skill-repo/{name}/aliases` (admin, body `{alias, versionNo}`) → move; `DELETE /v1/skill-repo/{name}` (admin).
- Write handlers record `RecordAudit` (`skill.create`/`skill.publish`/`skill.move_alias`/`skill.delete`, actor=MustUserID). Wrap the write routes in an `authMW.AdminOnly` sub-group.

- [ ] **Step 1: failing handler test** (dev-mode/injected admin claims): create a skill via POST, GET it back, publish v2, GET versions shows 2. A non-admin (injected non-admin claims) POST → 403.
- [ ] **Step 2-4:** run-fail → implement handlers + register routes → run-pass. `go build ./...`.
- [ ] **Step 5:** Commit `Add skill-repo HTTP handlers (reads open, writes admin-gated)`.

---

### Task 4: Self-evolve publishes into the registry

**Files:**
- Modify: `platform/api/internal/handler/handler.go` (`AcceptSkillProposal`)
- Modify: `platform/api/cmd/api/main.go` (SkillEvolver accept callback if it writes files)

**Interfaces:**
- Consumes: `PublishSkillVersion`/`CreateSkill` (Task 2), `GetSkillProposal`.
- Produces: accepting a skill proposal publishes it into the registry (create the skill if new, else publish a new version) instead of (or in addition to) writing `perUserSkillsDir(GlobalScope)/<name>.md`. Keep it admin-gated (already is). Record audit.

- [ ] **Step 1: failing test** — accept a pending proposal for skill "evo1" (via the admin path) → `GetSkill("evo1")` exists with a version whose body matches the proposal; accepting a second proposal for "evo1" → v2.
- [ ] **Step 2-4:** run-fail → implement (in `AcceptSkillProposal`, after resolving the proposal, call CreateSkill/PublishSkillVersion with the proposal's SkillName/SkillBody; on conflict-exists, publish a version) → run-pass.
- [ ] **Step 5:** Commit `Self-evolve accepted proposals publish into the skills registry`.

---

### Task 5: Generator resolves registry skills

**Files:**
- Modify: `platform/api/cmd/api/main.go` and/or `platform/api/internal/handler/handler.go` (wherever the `/generate` request is assembled and overlay skills are loaded)
- Modify: `platform/api/internal/generator/client.go` if the skill payload shape changes

**Interfaces:**
- Consumes: `ListSkills` + `ResolveSkill` (Task 2).
- Produces: before each `/generate` call, the control plane resolves the `@latest` body of every registry skill and passes them to the generator as the skill overlay (replacing the `_global` file-overlay read). Base skill files under `generator/prompts/skills/` remain untouched; the registry provides the evolved overlay layer.

- [ ] **Step 1:** Read how the control plane currently loads overlay skills (search `loadOverlaySkills` / `perUserSkillsDir` / the `/generate` call site) and how they're passed to the generator.
- [ ] **Step 2: failing test** — a unit test that the "active skills" assembler returns the registry's `@latest` bodies (create 2 skills, assert the assembled overlay contains both bodies).
- [ ] **Step 3:** Implement: replace the `_global` overlay file read with `ListSkills()` → `ResolveSkill(name@latest)` bodies. Keep the payload shape the generator already expects.
- [ ] **Step 4:** run-pass; `go build ./...`; boot smoke — `/generate` path still works (create a task via chat if feasible, or at least the assembler unit test).
- [ ] **Step 5:** Commit `Generator resolves skills from the global registry`.

---

### Task 6: Frontend Skills browser (view-at-a-glance)

**Files:**
- Create: `platform/web/src/components/SkillsView.tsx`
- Modify: `platform/web/src/lib/api.ts` (skill-repo client methods + types), `platform/web/src/App.tsx` / `SettingsOverlay.tsx` (mount the view, e.g. a "Skills" tab/overlay + a `⌘K` entry consistent with existing patterns)

**Interfaces:**
- Consumes: `/v1/skill-repo*` endpoints.
- Produces: a view listing all skills (name, description, tags, latest version) at a glance; clicking a skill shows its versions + aliases + the selected version's body; admins get publish / move-alias actions. Follow the existing Linear-style UI + URL-state conventions.

- [ ] **Step 1:** Add `api.listSkills()`, `api.getSkill(name)`, `api.getSkillVersion`, `api.resolveSkill`, and (admin) `api.createSkill/publishSkillVersion/moveSkillAlias/deleteSkill` to `api.ts` with types.
- [ ] **Step 2:** Build `SkillsView.tsx` — a two-pane browser (list left, detail right) matching the existing component style; gate write actions on `/v1/me` `isAdmin`.
- [ ] **Step 3:** Mount it (command palette entry + overlay/route), consistent with `SettingsOverlay`/URL-state.
- [ ] **Step 4:** `cd platform/web && CI=true pnpm build` — type-checks + builds clean.
- [ ] **Step 5:** Commit `Frontend: global skills browser`.

---

## Self-Review

**Spec coverage** (against overarching design §10 B + the "view-at-a-glance"/versioned/alias requirement): schema (T1), versioned/immutable/alias store + resolve (T2), reads-open/writes-admin API (T3), self-evolve→registry (T4), generator resolution (T5), browser UI (T6). ✓

**Placeholder scan:** T3/T4/T5/T6 compress the run-fail/pass cycle to "Step 2-4" with concrete test descriptions + exact commit messages; the migration SQL (T1) and store signatures (T2) are concrete. The generator overlay-loading call site is explicitly "read first" in T5 (confirm at execution).

**Type consistency:** `ParseSkillRef`/`ResolveSkill`/`SkillVersion` (T2) consumed by T3 (resolve endpoint), T5 (generator overlay). `PublishSkillVersion`/`CreateSkill` (T2) consumed by T4. `isAdmin` from `/v1/me` (A1) gates T6 writes; `auth.AdminOnly` gates T3 writes.

## Notes
- Skills are global + admin-write; this matches the platform decision that self-evolve is shared across all workspaces. If per-workspace private skills are wanted later, that's a separate feature (add a scope column).
