# Multi-user A1 — Auth & Tenancy Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the platform multi-user — authenticate via Keycloak OIDC, introduce a native User/Workspace/RBAC model, replace `store.DefaultUserID` with workspace-scoped resolution, and add a cross-cutting audit log.

**Architecture:** A shared Go (chi) control plane gains an auth middleware that decodes the Keycloak JWT into `Claims` on the request context, and a workspace middleware that resolves the active workspace from the `X-Workspace-ID` header (defaulting to the user's personal workspace whose id == their `sub`). Every domain query is scoped by `workspace_id`. Patterns are ported from `../llmgate` (Go + Fiber → Go + chi).

**Tech Stack:** Go 1.25 + chi v5 + pgx/v5 (Postgres 16, pgvector, pg_trgm); React 18 + Vite + TypeScript + `react-oidc-context`; Keycloak OIDC.

## Global Constraints

- All source code, comments, and UI strings in English. Korean only in plans/chat/per-user skill files.
- Commits never include AI-tool trailers (no `Co-Authored-By`, no "Generated with").
- Migrations live in `api/internal/db/migrations/` and apply on boot in lexical order; next index is `009`.
- JWT signature is **not** verified by the backend (frontend validates with Keycloak; backend trusts the decoded payload) — matches the in-house llmgate/forge convention.
- Existing single-user data is disposable: the migration may reset tenant-scoped tables. No backfill.
- Dev mode (`PLATFORM_AUTH_DEV=1`) injects a fixed admin identity so the stack runs without Keycloak.
- Personal workspace id **is** the user's Keycloak `sub`. "Everything is a workspace."
- Module path is `github.com/nk/platform/api`. Tests use the standard `testing` package.

---

### Task 1: Multi-user migration (schema)

**Files:**
- Create: `platform/api/internal/db/migrations/009_multiuser.sql`

**Interfaces:**
- Produces: tables `users`, `workspaces`, `workspace_members`, `audit_log`; a `workspace_id text` column on every tenant-scoped table; a seeded `default` workspace + `dev` user.

- [ ] **Step 1: Write the migration**

```sql
-- 009_multiuser.sql — multi-user foundation (A1).
-- Existing single-user data is disposable; tenant tables are reset.

CREATE TABLE IF NOT EXISTS users (
    sub                 text PRIMARY KEY,
    email               text NOT NULL DEFAULT '',
    preferred_username  text NOT NULL DEFAULT '',
    display_name        text NOT NULL DEFAULT '',
    is_admin            boolean NOT NULL DEFAULT false,
    disabled            boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspaces (
    id          text PRIMARY KEY,            -- personal workspace id == user sub
    name        text NOT NULL,
    kind        text NOT NULL DEFAULT 'personal', -- 'personal' | 'shared'
    owner_sub   text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspace_members (
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      text NOT NULL,              -- user sub
    role         text NOT NULL DEFAULT 'member', -- 'owner' | 'member' | 'viewer'
    added_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id            bigserial PRIMARY KEY,
    actor_sub     text NOT NULL DEFAULT '',
    actor_email   text NOT NULL DEFAULT '',
    action        text NOT NULL,
    resource_type text NOT NULL DEFAULT '',
    resource_id   text NOT NULL DEFAULT '',
    resource_name text NOT NULL DEFAULT '',
    workspace_id  text NOT NULL DEFAULT '',
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_log_ws_idx  ON audit_log (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS audit_log_act_idx ON audit_log (actor_sub, created_at DESC);

-- Tenant-scoped tables gain workspace_id. Data is disposable: truncate first
-- so the NOT NULL column can be added cleanly, then add the column.
TRUNCATE tasks, runs, run_logs, memory_entries, sessions, messages,
         credentials, task_index, task_archive, skill_proposals,
         evolution_prs, user_profile, user_profile_suggestions CASCADE;

ALTER TABLE tasks                     ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE runs                      ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE memory_entries            ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE sessions                  ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE credentials               ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE task_index                ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE task_archive              ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE skill_proposals           ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE evolution_prs             ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE user_profile              ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE user_profile_suggestions  ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;

CREATE INDEX IF NOT EXISTS tasks_ws_idx    ON tasks (workspace_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_ws_idx ON sessions (workspace_id, last_msg_at DESC);
CREATE INDEX IF NOT EXISTS memory_ws_idx   ON memory_entries (workspace_id);

-- Seed dev workspace + user so PLATFORM_AUTH_DEV=1 works out of the box.
INSERT INTO users (sub, email, preferred_username, display_name, is_admin)
VALUES ('dev', 'dev@example.com', 'dev', 'Dev User', true)
ON CONFLICT (sub) DO NOTHING;
INSERT INTO workspaces (id, name, kind, owner_sub)
VALUES ('dev', 'Dev Personal', 'personal', 'dev')
ON CONFLICT (id) DO NOTHING;
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ('dev', 'dev', 'owner')
ON CONFLICT DO NOTHING;
```

> NOTE during execution: confirm the real column names of each tenant table by reading its `00x_*.sql` before truncating (e.g. `sessions.last_msg_at`). If a table listed here does not exist yet, drop it from the TRUNCATE/ALTER list. Adjust index column names to match actual schema.

- [ ] **Step 2: Apply on boot and verify**

Run: `cd platform && (cd api && go run ./cmd/api) &` then check the log for migration application and `psql "$PLATFORM_DB_URL" -c '\d workspaces'`.
Expected: `workspaces`, `workspace_members`, `users`, `audit_log` exist; `tasks` has a `workspace_id` column; a `dev` workspace row exists.

- [ ] **Step 3: Commit**

```bash
git add platform/api/internal/db/migrations/009_multiuser.sql
git commit -m "Add multi-user schema migration (users/workspaces/members/audit)"
```

---

### Task 2: Auth — Claims + JWT decode

**Files:**
- Create: `platform/api/internal/auth/claims.go`
- Test: `platform/api/internal/auth/claims_test.go`

**Interfaces:**
- Produces: `type Claims struct { Sub, Email, PreferredUsername, Department string; Groups, Roles []string }`; `func DecodeJWT(token string) (*Claims, error)`; `func (c *Claims) HasRole(role string) bool`; `func (c *Claims) PrimaryWorkspace() string` (returns `Sub`).

- [ ] **Step 1: Write the failing test**

```go
package auth

import "testing"

func TestDecodeJWT_ExtractsClaims(t *testing.T) {
	// header.payload.sig — payload is base64url of the claims JSON.
	// {"sub":"u1","email":"u1@x","preferred_username":"u1","groups":["/team-a"],"realm_access":{"roles":["platform-admin"]}}
	tok := "h." +
		"eyJzdWIiOiJ1MSIsImVtYWlsIjoidTFAeCIsInByZWZlcnJlZF91c2VybmFtZSI6InUxIiwiZ3JvdXBzIjpbIi90ZWFtLWEiXSwicmVhbG1fYWNjZXNzIjp7InJvbGVzIjpbInBsYXRmb3JtLWFkbWluIl19fQ" +
		".s"
	c, err := DecodeJWT(tok)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if c.Sub != "u1" || c.Email != "u1@x" || c.PreferredUsername != "u1" {
		t.Fatalf("bad claims: %+v", c)
	}
	if !c.HasRole("platform-admin") {
		t.Fatalf("expected platform-admin role: %+v", c.Roles)
	}
	if c.PrimaryWorkspace() != "u1" {
		t.Fatalf("primary workspace should be sub, got %q", c.PrimaryWorkspace())
	}
}

func TestDecodeJWT_RejectsMalformed(t *testing.T) {
	if _, err := DecodeJWT("not-a-jwt"); err == nil {
		t.Fatal("expected error for malformed token")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd platform/api && go test ./internal/auth/ -run TestDecodeJWT -v`
Expected: FAIL — package/function not defined.

- [ ] **Step 3: Implement**

```go
package auth

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// Claims is the identity extracted from a Keycloak JWT.
type Claims struct {
	Sub               string
	Email             string
	PreferredUsername string
	Department        string
	Groups            []string
	Roles             []string
}

type keycloakPayload struct {
	Sub         string   `json:"sub"`
	Email       string   `json:"email"`
	PreferredUN string   `json:"preferred_username"`
	Department  string   `json:"department"`
	Groups      []string `json:"groups"`
	Roles       []string `json:"roles"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
	ResourceAccess map[string]struct {
		Roles []string `json:"roles"`
	} `json:"resource_access"`
}

// DecodeJWT decodes the JWT payload WITHOUT verifying the signature. The
// browser validates the token against Keycloak; the backend trusts it. This
// matches the in-house llmgate/forge convention.
func DecodeJWT(token string) (*Claims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token format")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid token encoding")
	}
	var p keycloakPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, errors.New("invalid token payload")
	}
	if p.Sub == "" {
		return nil, errors.New("missing sub claim")
	}
	roles := append([]string{}, p.RealmAccess.Roles...)
	roles = append(roles, p.Roles...)
	for _, ra := range p.ResourceAccess {
		roles = append(roles, ra.Roles...)
	}
	return &Claims{
		Sub: p.Sub, Email: p.Email, PreferredUsername: p.PreferredUN,
		Department: p.Department, Groups: p.Groups, Roles: roles,
	}, nil
}

func (c *Claims) HasRole(role string) bool {
	for _, r := range c.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// PrimaryWorkspace is the user's personal workspace id, which is their sub.
func (c *Claims) PrimaryWorkspace() string { return c.Sub }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd platform/api && go test ./internal/auth/ -run TestDecodeJWT -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add platform/api/internal/auth/claims.go platform/api/internal/auth/claims_test.go
git commit -m "Add auth.Claims + Keycloak JWT decode"
```

---

### Task 3: Auth — request middleware + context helpers

**Files:**
- Create: `platform/api/internal/auth/middleware.go`
- Test: `platform/api/internal/auth/middleware_test.go`

**Interfaces:**
- Consumes: `Claims`, `DecodeJWT` (Task 2).
- Produces:
  - `type Middleware struct { ... }`; `func New(devMode bool) *Middleware`; `func (m *Middleware) Authenticate(next http.Handler) http.Handler` (401 on missing/invalid token unless devMode); `func (m *Middleware) Optional(next http.Handler) http.Handler`.
  - context helpers: `func ClaimsFromCtx(ctx context.Context) *Claims`; `func MustUserID(ctx context.Context) string` (== claims.Sub); `func WorkspaceID(ctx context.Context) string` (set by WorkspaceMiddleware, Task 8); `func MustWorkspaceID(ctx context.Context) string`.

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticate_DevModeInjectsAdmin(t *testing.T) {
	m := New(true)
	var got *Claims
	h := m.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ClaimsFromCtx(r.Context())
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if got == nil || got.Sub != "dev" || !got.HasRole("platform-admin") {
		t.Fatalf("dev claims missing/incorrect: %+v", got)
	}
}

func TestAuthenticate_RejectsMissingToken(t *testing.T) {
	m := New(false)
	rec := httptest.NewRecorder()
	m.Authenticate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd platform/api && go test ./internal/auth/ -run TestAuthenticate -v`
Expected: FAIL.

- [ ] **Step 3: Implement**

```go
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type ctxKey int

const (
	claimsKey ctxKey = iota
	workspaceKey
)

type Middleware struct{ devMode bool }

func New(devMode bool) *Middleware { return &Middleware{devMode: devMode} }

func devClaims() *Claims {
	return &Claims{Sub: "dev", Email: "dev@example.com", PreferredUsername: "dev",
		Department: "dev", Groups: []string{"/dev"}, Roles: []string{"platform-admin"}}
}

func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.devMode {
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), devClaims())))
			return
		}
		c, err := claimsFromHeader(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), c)))
	})
}

func (m *Middleware) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.devMode {
			next.ServeHTTP(w, r.WithContext(withClaims(r.Context(), devClaims())))
			return
		}
		if c, err := claimsFromHeader(r); err == nil {
			r = r.WithContext(withClaims(r.Context(), c))
		}
		next.ServeHTTP(w, r)
	})
}

func claimsFromHeader(r *http.Request) (*Claims, error) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		// SSE/iframe can't set headers — accept ?access_token=.
		if t := r.URL.Query().Get("access_token"); t != "" {
			return DecodeJWT(t)
		}
		return nil, errMissingToken
	}
	return DecodeJWT(strings.TrimPrefix(h, "Bearer "))
}

var errMissingToken = &authError{"missing bearer token"}

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }

func withClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, claimsKey, c)
}

func ClaimsFromCtx(ctx context.Context) *Claims {
	c, _ := ctx.Value(claimsKey).(*Claims)
	return c
}

func MustUserID(ctx context.Context) string {
	if c := ClaimsFromCtx(ctx); c != nil {
		return c.Sub
	}
	return ""
}

// WithWorkspace / WorkspaceID — set by WorkspaceMiddleware (Task 8).
func WithWorkspace(ctx context.Context, ws string) context.Context {
	return context.WithValue(ctx, workspaceKey, ws)
}

func WorkspaceID(ctx context.Context) string {
	ws, _ := ctx.Value(workspaceKey).(string)
	return ws
}

// MustWorkspaceID falls back to the personal workspace (sub) when no explicit
// workspace was resolved, so handlers always get a non-empty scope.
func MustWorkspaceID(ctx context.Context) string {
	if ws := WorkspaceID(ctx); ws != "" {
		return ws
	}
	return MustUserID(ctx)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd platform/api && go test ./internal/auth/ -run TestAuthenticate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add platform/api/internal/auth/middleware.go platform/api/internal/auth/middleware_test.go
git commit -m "Add auth middleware + context helpers (dev mode, workspace scope)"
```

---

### Task 4: Auth — admin resolution + AdminOnly

**Files:**
- Modify: `platform/api/internal/auth/middleware.go`
- Test: `platform/api/internal/auth/admin_test.go`

**Interfaces:**
- Consumes: `Claims`, `ClaimsFromCtx`.
- Produces: `func SetAdminEnv(csv string)`; `type AdminChecker interface { IsAdminDB(sub string) bool }`; `func SetAdminChecker(c AdminChecker)`; `func IsAdmin(ctx context.Context) bool`; `func (m *Middleware) AdminOnly(next http.Handler) http.Handler`.

- [ ] **Step 1: Write the failing test**

```go
package auth

import (
	"context"
	"testing"
)

func TestIsAdmin_FromRoleAndEnv(t *testing.T) {
	ctx := withClaims(context.Background(), &Claims{Sub: "u1", Roles: []string{"platform-admin"}})
	if !IsAdmin(ctx) {
		t.Fatal("platform-admin role should be admin")
	}
	SetAdminEnv("alice,bob")
	ctx2 := withClaims(context.Background(), &Claims{Sub: "alice"})
	if !IsAdmin(ctx2) {
		t.Fatal("env-listed sub should be admin")
	}
	ctx3 := withClaims(context.Background(), &Claims{Sub: "carol"})
	if IsAdmin(ctx3) {
		t.Fatal("non-listed non-role user must not be admin")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd platform/api && go test ./internal/auth/ -run TestIsAdmin -v`
Expected: FAIL.

- [ ] **Step 3: Implement (append to middleware.go)**

```go
import "strings" // already imported; ensure present

var adminEnv = map[string]bool{}

func SetAdminEnv(csv string) {
	adminEnv = map[string]bool{}
	for _, u := range strings.Split(csv, ",") {
		if u = strings.TrimSpace(u); u != "" {
			adminEnv[u] = true
		}
	}
}

type AdminChecker interface{ IsAdminDB(sub string) bool }

var adminChecker AdminChecker

func SetAdminChecker(c AdminChecker) { adminChecker = c }

func IsAdmin(ctx context.Context) bool {
	c := ClaimsFromCtx(ctx)
	if c == nil {
		return false
	}
	if adminEnv[c.Sub] || adminEnv[c.PreferredUsername] {
		return true
	}
	if c.HasRole("platform-admin") {
		return true
	}
	for _, g := range c.Groups {
		if strings.TrimPrefix(g, "/") == "platform-admin" {
			return true
		}
	}
	if adminChecker != nil && adminChecker.IsAdminDB(c.Sub) {
		return true
	}
	return false
}

func (m *Middleware) AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsAdmin(r.Context()) {
			writeErr(w, http.StatusForbidden, "platform-admin required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: Run to verify it passes**

Run: `cd platform/api && go test ./internal/auth/ -v`
Expected: PASS (all auth tests).

- [ ] **Step 5: Commit**

```bash
git add platform/api/internal/auth/
git commit -m "Add admin resolution + AdminOnly middleware"
```

---

### Task 5: Users store

**Files:**
- Create: `platform/api/internal/store/user.go`
- Test: `platform/api/internal/store/user_test.go` (uses a live Postgres via `PLATFORM_DB_URL`; skip if unset)

**Interfaces:**
- Consumes: the `*pgxpool.Pool` held by `*Memory` (confirm the field name by reading `store/memory.go` / `db/pool.go`; referred to here as `m.pool`).
- Produces (methods on `*Memory`):
  - `func (m *Memory) UpsertUserOnLogin(c *auth.Claims) error` — inserts/updates `users`, bumps `last_seen_at`. (Avoid import cycle: accept primitive args `UpsertUserOnLogin(sub, email, username string)` instead of `*auth.Claims`.)
  - `func (m *Memory) ListUsers() []*User`
  - `func (m *Memory) SetUserAdmin(sub string, isAdmin bool) error`
  - `func (m *Memory) SetUserDisabled(sub string, disabled bool) error`
  - `func (m *Memory) IsAdminDB(sub string) bool` (satisfies `auth.AdminChecker`)
- `type User struct { Sub, Email, PreferredUsername, DisplayName string; IsAdmin, Disabled bool; CreatedAt, LastSeenAt time.Time }`

> Decision: `UpsertUserOnLogin` takes primitives `(sub, email, username string)`, not `*auth.Claims`, so `store` never imports `auth` (auth imports store via the AdminChecker registration in main, not vice-versa).

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"os"
	"testing"
)

func TestUsers_UpsertAndAdminToggle(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	m := newTestMemory(t) // helper that opens the pool + runs migrations
	if err := m.UpsertUserOnLogin("t_user", "t@x", "tuser"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if m.IsAdminDB("t_user") {
		t.Fatal("new user should not be admin")
	}
	if err := m.SetUserAdmin("t_user", true); err != nil {
		t.Fatal(err)
	}
	if !m.IsAdminDB("t_user") {
		t.Fatal("expected admin after toggle")
	}
	found := false
	for _, u := range m.ListUsers() {
		if u.Sub == "t_user" && u.IsAdmin {
			found = true
		}
	}
	if !found {
		t.Fatal("user not listed as admin")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd platform/api && go test ./internal/store/ -run TestUsers -v`
Expected: FAIL (helper/methods undefined). If `newTestMemory` doesn't exist, add it in this task (open pool from `PLATFORM_DB_URL`, ensure migrations applied).

- [ ] **Step 3: Implement** — write `user.go` with the `User` type and the methods above using `m.pool.Exec/Query`. Upsert:

```go
func (m *Memory) UpsertUserOnLogin(sub, email, username string) error {
	_, err := m.pool.Exec(context.Background(),
		`INSERT INTO users (sub, email, preferred_username, display_name, last_seen_at)
		 VALUES ($1,$2,$3,$3, now())
		 ON CONFLICT (sub) DO UPDATE SET email=EXCLUDED.email,
		   preferred_username=EXCLUDED.preferred_username, last_seen_at=now()`,
		sub, email, username)
	return err
}
```

(`IsAdminDB` → `SELECT is_admin FROM users WHERE sub=$1`; `ListUsers` → `SELECT ... ORDER BY created_at`; setters → `UPDATE users SET ...`.)

- [ ] **Step 4: Run to verify it passes**

Run: `cd platform/api && PLATFORM_DB_URL=$PLATFORM_DB_URL go test ./internal/store/ -run TestUsers -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add platform/api/internal/store/user.go platform/api/internal/store/user_test.go
git commit -m "Add users store (upsert-on-login, admin/disable, IsAdminDB)"
```

---

### Task 6: Workspaces store

**Files:**
- Create: `platform/api/internal/store/workspace.go`
- Test: `platform/api/internal/store/workspace_test.go`

**Interfaces:**
- Produces (methods on `*Memory`), ported from llmgate `workspace` package:
  - `func (m *Memory) EnsurePersonal(sub, displayName string) error` — creates workspace id==sub (kind=personal) + owner membership if absent.
  - `func (m *Memory) CreateWorkspace(ownerSub, name string) (*Workspace, error)` — kind=shared, random id, owner membership.
  - `func (m *Memory) ListWorkspacesForUser(sub string) []*Workspace`
  - `func (m *Memory) InviteMember(workspaceID, ownerSub, userID, role string) error` — only owner may invite.
  - `func (m *Memory) RemoveMember(workspaceID, callerSub, userID string) error` — owner removes anyone; member removes self.
  - `func (m *Memory) ListMembers(workspaceID string) []*Member`
  - `func (m *Memory) IsMember(workspaceID, sub string) bool`
- `type Workspace struct { ID, Name, Kind, OwnerSub string; CreatedAt time.Time }`; `type Member struct { UserID, Role string; AddedAt time.Time }`

- [ ] **Step 1: Write the failing test**

```go
func TestWorkspace_PersonalAndShared(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" { t.Skip("no db") }
	m := newTestMemory(t)
	if err := m.EnsurePersonal("ws_u1", "U1"); err != nil { t.Fatal(err) }
	if !m.IsMember("ws_u1", "ws_u1") { t.Fatal("owner should be member of personal ws") }
	ws, err := m.CreateWorkspace("ws_u1", "Team A")
	if err != nil { t.Fatal(err) }
	if err := m.InviteMember(ws.ID, "ws_u1", "ws_u2", "member"); err != nil { t.Fatal(err) }
	if !m.IsMember(ws.ID, "ws_u2") { t.Fatal("invited user should be a member") }
	if err := m.InviteMember(ws.ID, "ws_u2", "ws_u3", "member"); err == nil {
		t.Fatal("non-owner must not invite")
	}
}
```

- [ ] **Step 2-5:** run-fail → implement (per llmgate `workspace/service.go`) → run-pass → commit `"Add workspaces store (personal + shared, members, roles)"`.

---

### Task 7: Audit store

**Files:**
- Create: `platform/api/internal/store/audit.go`
- Test: `platform/api/internal/store/audit_test.go`

**Interfaces:**
- Produces: `type AuditEntry struct { ID int64; ActorSub, ActorEmail, Action, ResourceType, ResourceID, ResourceName, WorkspaceID string; Details map[string]any; CreatedAt time.Time }`; `func (m *Memory) RecordAudit(e AuditEntry) error` (best-effort; logs on error, never blocks the request); `func (m *Memory) ListAudit(workspaceID string, limit int) []*AuditEntry` (workspaceID=="" → all, admin only at handler layer).

- [ ] **Step 1: failing test** — record one entry, list it back, assert fields round-trip.
- [ ] **Step 2-5:** run-fail → implement (insert into `audit_log`, marshal `details` to jsonb) → run-pass → commit `"Add audit_log store"`.

---

### Task 8: Workspace middleware

**Files:**
- Modify: `platform/api/internal/auth/middleware.go`
- Test: `platform/api/internal/auth/workspace_test.go`

**Interfaces:**
- Consumes: `ClaimsFromCtx`, `WithWorkspace`; a membership checker `type MemberChecker interface { IsMember(workspaceID, sub string) bool }`.
- Produces: `func (m *Middleware) Workspace(mc MemberChecker) func(http.Handler) http.Handler`. If `X-Workspace-ID` empty or `==sub` → personal (sub). Else `mc.IsMember(ws, sub)` must be true, else 403. Sets ctx workspace.

- [ ] **Step 1: failing test** — request with `X-Workspace-ID: other` and a fake `MemberChecker` returning false → 403; with `==sub` → handler sees `WorkspaceID==sub`.
- [ ] **Step 2-5:** run-fail → implement → run-pass → commit `"Add workspace-resolution middleware"`.

---

### Task 9: Wire auth + stores + routes into main.go

**Files:**
- Modify: `platform/api/cmd/api/main.go`
- Modify: `platform/api/internal/handler/handler.go:37-90` (add fields + constructor args)

**Interfaces:**
- Consumes: everything from Tasks 2-8.
- Produces: middleware applied to `/v1`; admin sub-routes guarded by `AdminOnly`; `auth.SetAdminChecker(taskStore)` and `auth.SetAdminEnv(os.Getenv("PLATFORM_ADMIN_USERS"))` registered.

- [ ] **Step 1:** Add to `main()` after store construction:

```go
authMW := auth.New(os.Getenv("PLATFORM_AUTH_DEV") == "1")
auth.SetAdminEnv(os.Getenv("PLATFORM_ADMIN_USERS"))
auth.SetAdminChecker(taskStore) // *store.Memory implements IsAdminDB
```

- [ ] **Step 2:** Wrap the `/v1` group and dashboard with auth + workspace middleware:

```go
r.Route("/v1", func(r chi.Router) {
	r.Use(authMW.Authenticate)
	r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(taskStore)(next) })
	r.Use(h.UpsertUserMiddleware) // bumps users.last_seen_at + audit "login"
	// ... existing routes ...
	r.Get("/me", h.Me)
	r.Get("/workspaces", h.ListWorkspaces)
	r.Post("/workspaces", h.CreateWorkspace)
	r.Post("/workspaces/{id}/members", h.InviteMember)
	r.Delete("/workspaces/{id}/members/{uid}", h.RemoveMember)
	r.Get("/workspaces/{id}/members", h.ListMembers)
	r.Group(func(r chi.Router) {
		r.Use(authMW.AdminOnly)
		r.Get("/admin/users", h.ListUsers)
		r.Post("/admin/users/{sub}/admin", h.SetUserAdmin)
		r.Post("/admin/users/{sub}/disabled", h.SetUserDisabled)
		r.Get("/admin/audit", h.ListAudit)
	})
})
// SSE + dashboard use Optional auth (browser can't set headers for EventSource/iframe → ?access_token=).
```

- [ ] **Step 3:** Build: `cd platform/api && go build ./...`. Expected: compiles (handler methods stubbed in Task 10 — if ordering bites, do Task 10 first then return).
- [ ] **Step 4:** Commit `"Wire auth + workspace middleware + admin routes"`.

---

### Task 10: Handler endpoints (me / workspaces / users / audit) + login middleware

**Files:**
- Create: `platform/api/internal/handler/workspace_handler.go`
- Create: `platform/api/internal/handler/user_handler.go`
- Modify: `platform/api/internal/handler/handler.go` (Handler struct uses `*store.Memory` already as `h.Store`; no new field needed since all new methods hang off `*Memory`).

**Interfaces:**
- Consumes: store methods (Tasks 5-7), `auth.MustUserID`, `auth.MustWorkspaceID`, `auth.ClaimsFromCtx`, `auth.IsAdmin`.
- Produces: HTTP handlers named in Task 9. `h.UpsertUserMiddleware(next http.Handler) http.Handler` calls `h.Store.UpsertUserOnLogin(...)` + `h.Store.EnsurePersonal(sub, name)` once per request (cheap upsert) and records a best-effort `login` audit on first-seen.

- [ ] **Step 1: Write failing handler test** (httptest with dev-mode auth context) for `GET /v1/me` returning `{sub, email, isAdmin, workspaces:[...]}`.
- [ ] **Step 2-4:** run-fail → implement handlers (decode body, call store, `writeJSON`) → run-pass.
- [ ] **Step 5:** Build + commit `"Add me/workspaces/users/audit handlers + login middleware"`.

---

### Task 11: Tenancy refactor — store signatures

**Files:**
- Modify: `platform/api/internal/store/memory.go`, `memory_store.go`, `credentials.go`, `user_profile.go`, `skill_proposal.go`, `evolution.go`, `workflowfs.go`

**Interfaces:**
- Produces: every method currently taking a `userID string` parameter that means "tenant" is renamed to `workspaceID string` and its SQL filters/writes `workspace_id` instead of `user_id`. Methods that create rows set `workspace_id`. The `Task.UserID` JSON field is kept (creator/actor) but a new `Task.WorkspaceID` (`json:"workspaceId"`) is added and used for scoping; same for `Run`, `Session`.

- [ ] **Step 1:** Add `WorkspaceID string \`json:"workspaceId"\`` to `Task`, `Run`, `Session` in `store/types.go`. Build.
- [ ] **Step 2:** For each method in the inventory below, thread `workspaceID` and scope by it. Inventory (file:line confirmed at plan time):
  - `memory.go`: `ListTasks`, `CreateTask`, `ListArchivedTasks`, `ArchiveTask`, `ListSessions`, `CreateSession` (set/scoped by workspace); `CreateRun` inherits the task's workspace.
  - `memory_store.go`: `List`, `Search`, `PruneStale`, `Stats` (first arg → workspaceID).
  - `credentials.go`: `List`, `Resolve*` (first arg → workspaceID).
  - `user_profile.go`: `GetUserProfile`, `UpsertUserProfile`, `AppendProfileSuggestion`, `ListProfileSuggestions` (scope by workspaceID).
  - `skill_proposal.go`: `ListSkillProposals`, `CreateSkillProposal` (scope by workspaceID — but see Task 14: self-evolve is global; skill proposals move to global scope, so these may take a literal global key `"_global"` rather than workspace).
- [ ] **Step 3:** Build: `cd platform/api && go build ./...`. Fix all call sites the compiler flags (this is the point — the compiler enumerates them).
- [ ] **Step 4:** `go test ./internal/store/...`.
- [ ] **Step 5:** Commit `"Scope store methods by workspace_id"`.

---

### Task 12: Tenancy refactor — handler call sites

**Files:**
- Modify: `platform/api/internal/handler/handler.go` (+ any other handler files)

**Interfaces:**
- Consumes: `auth.MustWorkspaceID`, `auth.MustUserID`.
- Produces: zero references to `store.DefaultUserID`.

- [ ] **Step 1:** Replace each `store.DefaultUserID` with the right scope. The mechanical transform per call site:
  - data scoping (tasks, memory, credentials, sessions, profile) → `auth.MustWorkspaceID(r.Context())`
  - actor identity (audit actor, "who created this") → `auth.MustUserID(r.Context())`
  - bus event `UserID` field → keep as the workspace id for SSE filtering (see Task 13)
- [ ] **Step 2:** Delete the constant: remove `const DefaultUserID = "u_local"` from `store/types.go`.
- [ ] **Step 3:** Build: `cd platform/api && go build ./...`. Expected: compiles with no `DefaultUserID` references remaining. Also fix `cmd/api/main.go:153` (`memStore.PruneStale(store.DefaultUserID, ...)`) — the daily pruner must iterate all workspaces; for A1 change it to iterate `ListAllWorkspaceIDs()` (add that helper) and prune each.
- [ ] **Step 4:** `grep -rn "DefaultUserID" platform/api` → expect no matches.
- [ ] **Step 5:** Commit `"Remove DefaultUserID; scope handlers by workspace"`.

---

### Task 13: Bus + SSE workspace scoping

**Files:**
- Modify: `platform/api/internal/store/bus.go`, `platform/api/internal/handler/handler.go` (SSE filters, e.g. `:1206`, `:2317` region)

**Interfaces:**
- Produces: SSE streams only deliver events whose `Event.UserID` (now carrying workspace id) matches the subscriber's active workspace; cross-workspace events are filtered.

- [ ] **Step 1: failing test** — publish events for ws A and ws B; a subscriber on ws A must not receive ws B's event.
- [ ] **Step 2-5:** run-fail → implement filter (the SSE handler already checks `ev.UserID != "" && ev.UserID != userID` at `handler.go:1206`; replace `userID` with the active workspace) → run-pass → commit `"Scope bus/SSE by workspace"`.

---

### Task 14: Self-evolve global wiring

**Files:**
- Modify: `platform/api/cmd/api/main.go:108-141` (SkillEvolver wiring), `platform/api/internal/runner/skillevolve.go`

**Interfaces:**
- Produces: the SkillEvolver reads/writes a single **global** scope (constant `store.GlobalScope = "_global"`) rather than per-user; skill proposals are global. (The global registry UI is sub-project B; A only fixes scope.)

- [ ] **Step 1:** Introduce `const GlobalScope = "_global"` in `store/types.go`.
- [ ] **Step 2:** Change `RunOnce(userID)` call sites and the prompts-dir lookup to use a single global skills dir (`prompts/global/skills/`) and `GlobalScope` for proposals.
- [ ] **Step 3:** Build + a focused test that `CreateSkillProposal` stores under `GlobalScope` and `ListSkillProposals(GlobalScope, ...)` returns it.
- [ ] **Step 4-5:** run-pass → commit `"Make self-evolve global, not per-user"`.

---

### Task 15: Frontend — OIDC + workspace switcher + auth'd API client

**Files:**
- Create: `platform/web/src/lib/auth.ts`
- Modify: `platform/web/src/main.tsx`, `platform/web/src/lib/api.ts`, `platform/web/src/App.tsx` (workspace switcher in TopBar)
- Modify: `platform/web/package.json` (add `react-oidc-context`, `oidc-client-ts`)

**Interfaces:**
- Consumes: `/v1/me`, `/v1/workspaces`.
- Produces: `Authorization: Bearer <token>` + `X-Workspace-ID: <active>` on every API call; an active-workspace selector persisted to a URL param / localStorage.

- [ ] **Step 1:** Add `react-oidc-context` and `oidc-client-ts`; create `auth.ts`:

```ts
import type { AuthProviderProps } from "react-oidc-context";

const devMode = import.meta.env.VITE_AUTH_DEV === "1";

export const oidcConfig: AuthProviderProps = {
  authority: import.meta.env.VITE_KEYCLOAK_ISSUER ?? "",
  client_id: import.meta.env.VITE_KEYCLOAK_CLIENT_ID ?? "platform",
  redirect_uri: window.location.origin,
  post_logout_redirect_uri: window.location.origin,
  scope: "openid profile email",
  onSigninCallback: () =>
    window.history.replaceState({}, document.title, window.location.pathname),
};

export { devMode };
```

- [ ] **Step 2:** In `main.tsx`, wrap `<App/>` in `<AuthProvider {...oidcConfig}>` unless `devMode` (dev mode skips the provider; api.ts omits the header).
- [ ] **Step 3:** In `api.ts`, add a token getter + active-workspace getter; set both headers in the fetch wrapper. In dev mode send no `Authorization` (backend dev mode injects identity); always send `X-Workspace-ID` when an active workspace is chosen.
- [ ] **Step 4:** Add a workspace `<select>` to `TopBar`, sourced from `/v1/workspaces`, writing the active id to `localStorage` + a `ws=` URL param (consistent with the existing URL-state pattern).
- [ ] **Step 5:** `cd platform/web && pnpm build`. Expected: type-checks and builds. Commit `"Frontend: Keycloak OIDC + workspace switcher + scoped API client"`.

---

### Task 16: Config + dev-mode end-to-end boot

**Files:**
- Modify: `platform/.env.example`
- Modify: `platform/web/.env.example` (create if absent)

**Interfaces:**
- Produces: documented env for both modes.

- [ ] **Step 1:** Append to `platform/.env.example`:

```bash
# --- Auth (Keycloak OIDC) ---
# Backend decodes the JWT without verifying the signature (browser validates).
KEYCLOAK_ISSUER=https://sso.example/realms/REPLACE_ME
KEYCLOAK_CLIENT_ID=platform
PLATFORM_ADMIN_USERS=          # comma list of sub or preferred_username
PLATFORM_AUTH_DEV=1            # 1 = bypass Keycloak locally (fixed dev admin)
```

- [ ] **Step 2:** Create `platform/web/.env.example`:

```bash
VITE_AUTH_DEV=1
VITE_KEYCLOAK_ISSUER=https://sso.example/realms/REPLACE_ME
VITE_KEYCLOAK_CLIENT_ID=platform
```

- [ ] **Step 3: Boot the stack in dev mode and verify isolation manually.** With `PLATFORM_AUTH_DEV=1`: start api + web, create a task, confirm it lands in the `dev` workspace (`psql -c "select id, workspace_id, name from tasks"`). Then via curl with `X-Workspace-ID: other` + a fake second dev identity, confirm the first workspace's task is not visible.
- [ ] **Step 4:** Run the full backend test suite: `cd platform/api && go test ./...`. Expected: PASS.
- [ ] **Step 5:** Commit `"Document auth/runtime env; verify dev-mode multi-workspace boot"`.

---

## Self-Review

**Spec coverage** (against `2026-06-30-multiuser-platform-foundation-design.md`):
- §5.1 Auth & identity → Tasks 2,3,4,9. ✓
- §5.2 Workspace/user/RBAC → Tasks 5,6,9,10. ✓
- §5.3 Tenancy refactor (remove DefaultUserID) → Tasks 11,12. ✓
- §5.5 triggers/proxy per workspace → **A2 (Plan 2)**, not here. (A1 leaves the single proxy/scheduler; A2 isolates them.) ✓ noted
- §5.6 Audit → Tasks 7,10. ✓
- §5.7 self-evolve global → Task 14. ✓
- §6 data model → Task 1. ✓
- §7 config → Task 16. ✓
- §9 testing → tests in each task + Task 16 integration. ✓

**Placeholder scan:** Tasks 6,7,10,13,14 compress the run-fail/run-pass cycle to "Step 2-5" with the exact commit message and a concrete test description rather than repeating boilerplate; the test bodies and SQL are concrete where novel. The one runtime-dependent unknown — the pgx pool field name on `*Memory` and exact tenant-table column names — is explicitly flagged to confirm by reading the file at execution time (not a silent placeholder).

**Type consistency:** `MustWorkspaceID`/`MustUserID`/`ClaimsFromCtx`/`WithWorkspace` are defined in Task 3 and consumed by 9,10,12. `IsMember` (Task 6) is consumed by the `MemberChecker` in Task 8. `IsAdminDB` (Task 5) satisfies `auth.AdminChecker` (Task 4), registered in Task 9. `UpsertUserOnLogin(sub,email,username)` primitive signature is consistent between Task 5 and Task 10.

## Execution notes

- Tasks 9 and 10 are mutually referential (routes name handlers that Task 10 defines). Execute Task 10's handler stubs first if the compiler complains, then Task 9 wiring.
- Tasks 11-12 are the high-churn sweep; rely on `go build ./...` to enumerate every call site rather than grepping by hand.
