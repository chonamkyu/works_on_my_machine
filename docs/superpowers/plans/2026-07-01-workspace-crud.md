# Workspace CRUD Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `DeleteWorkspace` to the backend store + handler + route, and replace the plain `<select>` workspace switcher in the frontend with a searchable custom dropdown that supports create, search, and delete.

**Architecture:** The store method `DeleteWorkspace(id, callerSub string, isAdmin bool)` refuses personal workspaces unconditionally; for shared workspaces it requires `owner_sub == callerSub OR isAdmin`. The handler extracts the admin flag from `auth.IsAdmin(ctx)` and passes it through. The frontend `WorkspaceSwitcher` component is a self-contained popover with a search input, workspace list rows (with trash icon for shared), and a "+ New workspace" inline form at the bottom — all using the existing dark/hairline/surface Tailwind tokens.

**Tech Stack:** Go 1.22+ / chi router / pgx v5 / React 18 + TypeScript / Tailwind CSS / Lucide React icons

## Global Constraints

- Module path: `github.com/nk/platform/api`
- Web at: `platform/web`
- Working directory: `/Users/nk/workspaces/works_on_my_machine`
- Branch: `main` — do NOT switch
- All source/UI strings must be English only
- Backend verify: `cd platform/api && PLATFORM_DB_URL='postgres://platform:platform@localhost:5432/platform?sslmode=disable' go build ./... && go vet ./... && go test ./...`
- Frontend verify: `cd platform/web && CI=true pnpm build`
- Do NOT push to remote

---

### Task 1: Store — `DeleteWorkspace`

**Files:**
- Modify: `platform/api/internal/store/workspace.go`

**Interfaces:**
- Produces: `func (m *Memory) DeleteWorkspace(id, callerSub string, isAdmin bool) error`
  - Refuses personal workspaces → `fmt.Errorf("cannot delete personal workspace")`
  - Non-owner non-admin → `fmt.Errorf("only the owner can delete a workspace")`
  - Not-found → `fmt.Errorf("workspace not found: %w", pgx.ErrNoRows)` (or equivalent)
  - On success: `DELETE FROM workspaces WHERE id=$1` (workspace_members cascade via FK)

- [ ] **Step 1: Read the file to understand imports and style**

  Open `platform/api/internal/store/workspace.go`. Note the import of `"github.com/jackc/pgx/v5"` (used by `RemoveMember` for `pgx.ErrNoRows`) and `"errors"` for `errors.Is`. Both are already imported.

- [ ] **Step 2: Add `DeleteWorkspace` to the store**

  Append at the end of `platform/api/internal/store/workspace.go`:

  ```go
  // DeleteWorkspace deletes a shared workspace. Personal workspaces cannot be
  // deleted. Only the workspace owner (or a platform admin) may delete.
  func (m *Memory) DeleteWorkspace(id, callerSub string, isAdmin bool) error {
  	ctx := context.Background()
  	var kind, ownerSub string
  	err := m.pool.QueryRow(ctx, `
  		SELECT kind, owner_sub FROM workspaces WHERE id=$1`, id).Scan(&kind, &ownerSub)
  	if err != nil {
  		if errors.Is(err, pgx.ErrNoRows) {
  			return fmt.Errorf("workspace not found: %w", err)
  		}
  		return fmt.Errorf("DeleteWorkspace lookup: %w", err)
  	}
  	if kind == "personal" {
  		return fmt.Errorf("cannot delete personal workspace")
  	}
  	if ownerSub != callerSub && !isAdmin {
  		return fmt.Errorf("only the owner can delete a workspace")
  	}
  	_, err = m.pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, id)
  	if err != nil {
  		return fmt.Errorf("DeleteWorkspace: %w", err)
  	}
  	return nil
  }
  ```

- [ ] **Step 3: Verify it compiles**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/api && go build ./internal/store/...
  ```

  Expected: no output (success).

- [ ] **Step 4: Commit**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine
  git add platform/api/internal/store/workspace.go
  git commit -m "store: add DeleteWorkspace (refuses personal; owner/admin only)"
  ```

---

### Task 2: Store test — `DeleteWorkspace`

**Files:**
- Modify: `platform/api/internal/store/workspace_test.go`

**Interfaces:**
- Consumes: `DeleteWorkspace(id, callerSub string, isAdmin bool) error` from Task 1
- Consumes: `CreateWorkspace(ownerSub, name string) (*Workspace, error)`
- Consumes: `EnsurePersonal(sub, displayName string) error`

- [ ] **Step 1: Append the `TestWorkspace_DeleteWorkspace` test function**

  Append at the end of `platform/api/internal/store/workspace_test.go`:

  ```go
  func TestWorkspace_DeleteWorkspace(t *testing.T) {
  	if os.Getenv("PLATFORM_DB_URL") == "" {
  		t.Skip("no db")
  	}
  	m := newTestMemory(t)
  	ctx := context.Background()

  	// Create two users: del_u1 (owner) and del_u2 (non-owner).
  	for _, sub := range []string{"del_u1", "del_u2", "del_adm"} {
  		if err := m.UpsertUserOnLogin(sub, sub+"@x", sub); err != nil {
  			t.Fatalf("upsert %s: %v", sub, err)
  		}
  	}

  	t.Cleanup(func() {
  		_, _ = m.pool.Exec(ctx, "DELETE FROM workspaces WHERE owner_sub IN ('del_u1','del_u2','del_adm')")
  		_, _ = m.pool.Exec(ctx, "DELETE FROM users WHERE sub IN ('del_u1','del_u2','del_adm')")
  	})

  	// EnsurePersonal for del_u1.
  	if err := m.EnsurePersonal("del_u1", "Del U1"); err != nil {
  		t.Fatal(err)
  	}

  	// Refusing to delete a personal workspace.
  	err := m.DeleteWorkspace("del_u1", "del_u1", false)
  	if err == nil {
  		t.Fatal("expected error deleting personal workspace, got nil")
  	}
  	if err.Error() != "cannot delete personal workspace" {
  		t.Fatalf("wrong error: %v", err)
  	}

  	// Non-owner cannot delete a shared workspace.
  	ws, err := m.CreateWorkspace("del_u1", "To Delete")
  	if err != nil {
  		t.Fatal(err)
  	}
  	err = m.DeleteWorkspace(ws.ID, "del_u2", false)
  	if err == nil {
  		t.Fatal("expected error for non-owner delete, got nil")
  	}
  	if err.Error() != "only the owner can delete a workspace" {
  		t.Fatalf("wrong error: %v", err)
  	}

  	// Owner can delete a shared workspace.
  	ws2, err := m.CreateWorkspace("del_u1", "Owner Deletes")
  	if err != nil {
  		t.Fatal(err)
  	}
  	if err := m.DeleteWorkspace(ws2.ID, "del_u1", false); err != nil {
  		t.Fatalf("owner delete: %v", err)
  	}
  	// Confirm it is gone (should return not-found error).
  	err = m.DeleteWorkspace(ws2.ID, "del_u1", false)
  	if err == nil {
  		t.Fatal("expected not-found error after deletion, got nil")
  	}

  	// Admin can delete a workspace they don't own.
  	ws3, err := m.CreateWorkspace("del_u1", "Admin Deletes")
  	if err != nil {
  		t.Fatal(err)
  	}
  	if err := m.DeleteWorkspace(ws3.ID, "del_adm", true); err != nil {
  		t.Fatalf("admin delete: %v", err)
  	}
  }
  ```

- [ ] **Step 2: Run the new test (requires DB)**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/api && PLATFORM_DB_URL='postgres://platform:platform@localhost:5432/platform?sslmode=disable' go test ./internal/store/... -run TestWorkspace_DeleteWorkspace -v
  ```

  Expected output includes `--- PASS: TestWorkspace_DeleteWorkspace`.
  If DB is unavailable: `--- SKIP: TestWorkspace_DeleteWorkspace (no db)` — that is also acceptable.

- [ ] **Step 3: Run full store test suite**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/api && PLATFORM_DB_URL='postgres://platform:platform@localhost:5432/platform?sslmode=disable' go test ./internal/store/... -v
  ```

  Expected: all previously-passing tests still pass.

- [ ] **Step 4: Commit**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine
  git add platform/api/internal/store/workspace_test.go
  git commit -m "store/test: TestWorkspace_DeleteWorkspace (personal refused, owner/admin)"
  ```

---

### Task 3: Handler — `DeleteWorkspace` + route

**Files:**
- Modify: `platform/api/internal/handler/workspace_handler.go`
- Modify: `platform/api/cmd/api/main.go`

**Interfaces:**
- Consumes: `(m *Memory) DeleteWorkspace(id, callerSub string, isAdmin bool) error` from Task 1
- Consumes: `auth.MustUserID(ctx) string` (already used throughout the handler file)
- Consumes: `auth.IsAdmin(ctx) bool` (already used in `ListMembers`)
- Consumes: `h.audit(r, action, resourceType, resourceID, resourceName)` (already used)
- Consumes: `chi.URLParam(r, "id") string` (already used)
- Produces: HTTP handler `func (h *Handler) DeleteWorkspace(w http.ResponseWriter, r *http.Request)` registered at `DELETE /v1/workspaces/{id}`

Error mapping:
- "cannot delete personal workspace" → 409 Conflict
- "only the owner can delete" → 403 Forbidden
- "workspace not found" → 404 Not Found
- anything else → 500

- [ ] **Step 1: Read workspace_handler.go to confirm imports (already have context)**

  Confirm: imports include `chi`, `auth`, `store`, `encoding/json`, `net/http`. They do (seen in file read above — lines 1-11). No new imports are needed.

- [ ] **Step 2: Add `DeleteWorkspace` handler to `workspace_handler.go`**

  Append after the `ListMembers` function (end of file):

  ```go
  // DeleteWorkspace permanently deletes a shared workspace.
  // Personal workspaces are never deletable. Only the owner or a platform admin
  // may delete a shared workspace.
  //
  // DELETE /v1/workspaces/{id}
  func (h *Handler) DeleteWorkspace(w http.ResponseWriter, r *http.Request) {
  	caller := auth.MustUserID(r.Context())
  	if caller == "" {
  		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
  		return
  	}
  	id := chi.URLParam(r, "id")
  	isAdmin := auth.IsAdmin(r.Context())
  	if err := h.Store.DeleteWorkspace(id, caller, isAdmin); err != nil {
  		msg := err.Error()
  		switch {
  		case msg == "cannot delete personal workspace":
  			writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
  		case msg == "only the owner can delete a workspace":
  			writeJSON(w, http.StatusForbidden, map[string]any{"error": msg})
  		case contains(msg, "workspace not found"):
  			writeJSON(w, http.StatusNotFound, map[string]any{"error": msg})
  		default:
  			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": msg})
  		}
  		return
  	}
  	h.audit(r, "workspace.delete", "workspace", id, id)
  	w.WriteHeader(http.StatusNoContent)
  }

  // contains is a tiny helper so the handler file has no extra import.
  func contains(s, substr string) bool {
  	return len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
  }

  func stringContains(s, substr string) bool {
  	for i := 0; i <= len(s)-len(substr); i++ {
  		if s[i:i+len(substr)] == substr {
  			return true
  		}
  	}
  	return false
  }
  ```

  > **Note:** Using a hand-rolled `contains` helper avoids importing `strings` if the handler file doesn't already import it. However, if `strings` is already imported (check the import block), use `strings.Contains(msg, "workspace not found")` instead and remove the helper functions.

- [ ] **Step 3: Check if `strings` is already imported in workspace_handler.go**

  Read the import block (lines 1-12 seen earlier):
  ```
  import (
      "encoding/json"
      "net/http"
      "github.com/go-chi/chi/v5"
      "github.com/nk/platform/api/internal/auth"
      "github.com/nk/platform/api/internal/store"
  )
  ```
  `strings` is NOT imported. Two options:
  - Option A: Add `"strings"` to the import and use `strings.Contains` (cleaner — recommended)
  - Option B: Keep the inline helper functions

  **Use Option A**: add `"strings"` to the import block and replace the helper functions with `strings.Contains(msg, "workspace not found")` in the switch case. The handler becomes:

  ```go
  // workspace_handler.go imports block — update to:
  import (
  	"encoding/json"
  	"net/http"
  	"strings"

  	"github.com/go-chi/chi/v5"

  	"github.com/nk/platform/api/internal/auth"
  	"github.com/nk/platform/api/internal/store"
  )
  ```

  And the DeleteWorkspace error switch:
  ```go
  	switch {
  	case msg == "cannot delete personal workspace":
  		writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
  	case msg == "only the owner can delete a workspace":
  		writeJSON(w, http.StatusForbidden, map[string]any{"error": msg})
  	case strings.Contains(msg, "workspace not found"):
  		writeJSON(w, http.StatusNotFound, map[string]any{"error": msg})
  	default:
  		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": msg})
  	}
  ```

  (Remove the `contains` and `stringContains` helper functions if you added them.)

- [ ] **Step 4: Register route in main.go**

  In `platform/api/cmd/api/main.go`, find the workspace routes block (around line 360-366):
  ```go
  r.Get("/workspaces", h.ListWorkspaces)
  r.Post("/workspaces", h.CreateWorkspace)
  r.Post("/workspaces/{id}/members", h.InviteMember)
  r.Delete("/workspaces/{id}/members/{uid}", h.RemoveMember)
  r.Get("/workspaces/{id}/members", h.ListMembers)
  ```

  Add after `r.Post("/workspaces", h.CreateWorkspace)`:
  ```go
  r.Delete("/workspaces/{id}", h.DeleteWorkspace)
  ```

  The block should now read:
  ```go
  r.Get("/workspaces", h.ListWorkspaces)
  r.Post("/workspaces", h.CreateWorkspace)
  r.Delete("/workspaces/{id}", h.DeleteWorkspace)
  r.Post("/workspaces/{id}/members", h.InviteMember)
  r.Delete("/workspaces/{id}/members/{uid}", h.RemoveMember)
  r.Get("/workspaces/{id}/members", h.ListMembers)
  ```

- [ ] **Step 5: Build and vet**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/api && go build ./... && go vet ./...
  ```

  Expected: no output (success).

- [ ] **Step 6: Run full test suite**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/api && PLATFORM_DB_URL='postgres://platform:platform@localhost:5432/platform?sslmode=disable' go test ./...
  ```

  Expected: all tests pass (or skip if no DB).

- [ ] **Step 7: Commit**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine
  git add platform/api/internal/handler/workspace_handler.go platform/api/cmd/api/main.go
  git commit -m "handler: DeleteWorkspace + DELETE /v1/workspaces/{id} route"
  ```

---

### Task 4: Frontend — `api.ts` additions

**Files:**
- Modify: `platform/web/src/lib/api.ts`

**Interfaces:**
- Produces: `api.createWorkspace(name: string): Promise<Workspace>` — POST `/v1/workspaces` body `{"name": name}`
- Produces: `api.deleteWorkspace(id: string): Promise<void>` — DELETE `/v1/workspaces/{id}`
- Note: `api.listWorkspaces()` already exists (line 434 of api.ts)

- [ ] **Step 1: Add `createWorkspace` and `deleteWorkspace` to the `api` object**

  In `platform/web/src/lib/api.ts`, find the `api` object (starting around line 334). Locate:
  ```typescript
  listWorkspaces: () => http<{ workspaces: Workspace[] }>("/v1/workspaces"),
  ```

  Replace that single line with:
  ```typescript
  listWorkspaces: () => http<{ workspaces: Workspace[] }>("/v1/workspaces"),
  createWorkspace: (name: string) =>
    http<Workspace>("/v1/workspaces", { method: "POST", body: JSON.stringify({ name }) }),
  deleteWorkspace: (id: string) =>
    http<void>(`/v1/workspaces/${encodeURIComponent(id)}`, { method: "DELETE" }),
  ```

- [ ] **Step 2: Verify TypeScript compiles**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/web && CI=true pnpm build 2>&1 | tail -20
  ```

  Expected: exits 0 with no TypeScript errors. (Build may warn about unused variables in other files — that is acceptable as long as it exits 0.)

- [ ] **Step 3: Commit**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine
  git add platform/web/src/lib/api.ts
  git commit -m "api.ts: add createWorkspace + deleteWorkspace"
  ```

---

### Task 5: Frontend — `WorkspaceSwitcher` component

**Files:**
- Create: `platform/web/src/components/WorkspaceSwitcher.tsx`
- Modify: `platform/web/src/components/TopBar.tsx`

**Interfaces:**
- Consumes: `api.listWorkspaces()`, `api.createWorkspace(name)`, `api.deleteWorkspace(id)` from Task 4
- Consumes: `type Workspace` from `../lib/api`
- Consumes: `applyWorkspace(id: string)` — the caller passes this as a prop
- Produces: `<WorkspaceSwitcher workspaces={...} activeWs={...} onSwitch={applyWorkspace} onRefetch={loadWorkspaces} />` component

**Design tokens in use (from existing TopBar code):**
- Container/header: `border-hairline`, `bg-canvas`
- Dropdown button: `border-hairline`, `bg-surface-1`, `text-ink-subtle`, `h-6`, `px-1.5`, `rounded`, `text-[11px]`
- Popover: `bg-surface-1`, `border border-hairline`, `rounded-md`, `shadow-lg`
- Active row: `bg-surface-2`
- Input: `border border-hairline`, `bg-canvas`, `text-ink`, `rounded`, `text-[11px]`

**Component behavior:**
- Trigger button: shows active workspace name + `ChevronDown` icon
- Toggle popover on click; close on outside-click (mousedown listener on document) or Esc keydown
- Popover top: search `<input>` filtering workspaces by name (case-insensitive substring)
- Workspace rows: click to switch (calls `onSwitch(id)` + closes popover). Active row highlighted. Show kind badge (`personal` / `shared`) in muted text. For shared workspaces, show `Trash2` icon button — on click, `window.confirm("Delete workspace \"<name>\"?")` → if confirmed, `api.deleteWorkspace(id)` → `onRefetch()` → if deleted workspace was active, switch to personal workspace.
- Bottom of popover: `+ New workspace` expand toggle → inline form with name `<input>` + Create button → `api.createWorkspace(name)` → `onRefetch()` → switch to the new workspace.

- [ ] **Step 1: Create `WorkspaceSwitcher.tsx`**

  Create `/Users/nk/workspaces/works_on_my_machine/platform/web/src/components/WorkspaceSwitcher.tsx` with:

  ```tsx
  import { useEffect, useRef, useState } from "react";
  import { ChevronDown, Trash2, Plus, X } from "lucide-react";
  import { api, type Workspace } from "../lib/api";

  interface Props {
    workspaces: Workspace[];
    activeWs: string | null;
    onSwitch: (id: string) => void;
    onRefetch: () => void;
  }

  export function WorkspaceSwitcher({ workspaces, activeWs, onSwitch, onRefetch }: Props) {
    const [open, setOpen] = useState(false);
    const [query, setQuery] = useState("");
    const [showCreate, setShowCreate] = useState(false);
    const [newName, setNewName] = useState("");
    const [creating, setCreating] = useState(false);
    const ref = useRef<HTMLDivElement>(null);

    const active = workspaces.find((w) => w.id === activeWs);
    const filtered = workspaces.filter((w) =>
      w.name.toLowerCase().includes(query.toLowerCase()),
    );
    const personal = workspaces.find((w) => w.kind === "personal");

    // Close on outside click.
    useEffect(() => {
      if (!open) return;
      function onDown(e: MouseEvent) {
        if (ref.current && !ref.current.contains(e.target as Node)) {
          setOpen(false);
          setQuery("");
          setShowCreate(false);
          setNewName("");
        }
      }
      document.addEventListener("mousedown", onDown);
      return () => document.removeEventListener("mousedown", onDown);
    }, [open]);

    // Close on Esc.
    useEffect(() => {
      if (!open) return;
      function onKey(e: KeyboardEvent) {
        if (e.key === "Escape") {
          setOpen(false);
          setQuery("");
          setShowCreate(false);
          setNewName("");
        }
      }
      document.addEventListener("keydown", onKey);
      return () => document.removeEventListener("keydown", onKey);
    }, [open]);

    async function handleDelete(ws: Workspace) {
      if (!window.confirm(`Delete workspace "${ws.name}"?`)) return;
      await api.deleteWorkspace(ws.id);
      onRefetch();
      // If deleted workspace was active, fall back to personal.
      if (ws.id === activeWs && personal) {
        onSwitch(personal.id);
      }
    }

    async function handleCreate() {
      if (!newName.trim()) return;
      setCreating(true);
      try {
        const ws = await api.createWorkspace(newName.trim());
        onRefetch();
        onSwitch(ws.id);
        setOpen(false);
        setNewName("");
        setShowCreate(false);
        setQuery("");
      } finally {
        setCreating(false);
      }
    }

    if (workspaces.length === 0) return null;

    return (
      <div ref={ref} className="relative">
        {/* Trigger button */}
        <button
          onClick={() => setOpen((v) => !v)}
          className="h-6 px-1.5 rounded border border-hairline bg-surface-1 text-[11px] text-ink-subtle hover:border-hairline-strong focus:outline-none flex items-center gap-1"
          title="Active workspace"
        >
          <span className="max-w-[120px] truncate">{active?.name ?? "—"}</span>
          <ChevronDown className="w-3 h-3 flex-shrink-0" />
        </button>

        {/* Popover */}
        {open && (
          <div className="absolute top-8 left-0 z-50 w-64 rounded-md border border-hairline bg-surface-1 shadow-lg py-1 flex flex-col">
            {/* Search */}
            <div className="px-2 pb-1 pt-1">
              <input
                autoFocus
                type="text"
                placeholder="Search workspaces…"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                className="w-full h-6 px-2 rounded border border-hairline bg-canvas text-[11px] text-ink placeholder:text-ink-tertiary focus:outline-none"
              />
            </div>

            {/* Workspace list */}
            <ul className="max-h-48 overflow-y-auto">
              {filtered.length === 0 && (
                <li className="px-3 py-1.5 text-[11px] text-ink-tertiary">No matches</li>
              )}
              {filtered.map((ws) => (
                <li
                  key={ws.id}
                  className={`group flex items-center gap-1 px-2 py-1 cursor-pointer text-[11px] ${
                    ws.id === activeWs ? "bg-surface-2 text-ink" : "text-ink-subtle hover:bg-surface-2 hover:text-ink"
                  }`}
                  onClick={() => {
                    onSwitch(ws.id);
                    setOpen(false);
                    setQuery("");
                  }}
                >
                  <span className="flex-1 truncate">{ws.name}</span>
                  <span className="text-[10px] text-ink-tertiary flex-shrink-0">
                    {ws.kind === "personal" ? "personal" : "shared"}
                  </span>
                  {ws.kind !== "personal" && (
                    <button
                      title="Delete workspace"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleDelete(ws);
                      }}
                      className="ml-1 opacity-0 group-hover:opacity-100 text-ink-tertiary hover:text-red-400 flex-shrink-0 focus:outline-none"
                    >
                      <Trash2 className="w-3 h-3" />
                    </button>
                  )}
                </li>
              ))}
            </ul>

            {/* Divider */}
            <div className="border-t border-hairline mx-2 my-1" />

            {/* Create workspace */}
            {!showCreate ? (
              <button
                onClick={() => setShowCreate(true)}
                className="flex items-center gap-1.5 px-2 py-1 text-[11px] text-ink-subtle hover:text-ink hover:bg-surface-2 w-full text-left focus:outline-none"
              >
                <Plus className="w-3 h-3" />
                <span>New workspace</span>
              </button>
            ) : (
              <div className="px-2 pb-2 flex flex-col gap-1">
                <div className="flex items-center gap-1">
                  <input
                    autoFocus
                    type="text"
                    placeholder="Workspace name…"
                    value={newName}
                    onChange={(e) => setNewName(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter") handleCreate();
                      if (e.key === "Escape") {
                        setShowCreate(false);
                        setNewName("");
                      }
                    }}
                    className="flex-1 h-6 px-2 rounded border border-hairline bg-canvas text-[11px] text-ink placeholder:text-ink-tertiary focus:outline-none"
                  />
                  <button
                    onClick={() => { setShowCreate(false); setNewName(""); }}
                    className="text-ink-tertiary hover:text-ink focus:outline-none"
                    title="Cancel"
                  >
                    <X className="w-3 h-3" />
                  </button>
                </div>
                <button
                  disabled={!newName.trim() || creating}
                  onClick={handleCreate}
                  className="h-6 px-2 rounded border border-hairline bg-surface-2 text-[11px] text-ink hover:bg-surface-1 disabled:opacity-40 focus:outline-none"
                >
                  {creating ? "Creating…" : "Create"}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
    );
  }
  ```

- [ ] **Step 2: Update `TopBar.tsx` to use `WorkspaceSwitcher`**

  In `platform/web/src/components/TopBar.tsx`:

  **Add the import** (after the existing imports line 1-4):
  ```tsx
  import { WorkspaceSwitcher } from "./WorkspaceSwitcher";
  ```

  **Replace the `<select>` block** (lines 86-99):
  ```tsx
  {workspaces.length > 0 && (
    <select
      value={activeWs ?? ""}
      onChange={handleWsChange}
      className="h-6 px-1.5 rounded border border-hairline bg-surface-1 text-[11px] text-ink-subtle hover:border-hairline-strong focus:outline-none"
      title="Active workspace"
    >
      {workspaces.map((w) => (
        <option key={w.id} value={w.id}>
          {w.name}
        </option>
      ))}
    </select>
  )}
  ```

  With:
  ```tsx
  <WorkspaceSwitcher
    workspaces={workspaces}
    activeWs={activeWs}
    onSwitch={applyWorkspace}
    onRefetch={loadWorkspaces}
  />
  ```

  **Remove the now-unused `handleWsChange` function** (lines 77-79):
  ```tsx
  function handleWsChange(e: React.ChangeEvent<HTMLSelectElement>) {
    applyWorkspace(e.target.value);
  }
  ```

  This function is no longer referenced after replacing the `<select>`.

- [ ] **Step 3: Build to verify TypeScript + imports**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/web && CI=true pnpm build 2>&1 | tail -30
  ```

  Expected: exits 0. No TypeScript type errors. If there are unused import errors (`React` from removing the `select` with `React.ChangeEvent`), also remove the `React` import from `TopBar.tsx` imports if it is only used for the `onChange` type (check — `React` is not in the current import line; `React.ChangeEvent` is inlined — it can be removed safely).

- [ ] **Step 4: Commit**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine
  git add platform/web/src/components/WorkspaceSwitcher.tsx platform/web/src/components/TopBar.tsx
  git commit -m "frontend: WorkspaceSwitcher with search, create, delete"
  ```

---

### Task 6: Full verification pass

**Files:** No file changes — verification and report only.

- [ ] **Step 1: Backend full build + vet + test**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/api && PLATFORM_DB_URL='postgres://platform:platform@localhost:5432/platform?sslmode=disable' go build ./... && go vet ./... && go test ./...
  ```

  Expected: exits 0. Tests either pass or skip (if no DB).

- [ ] **Step 2: Frontend full build**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine/platform/web && CI=true pnpm build
  ```

  Expected: exits 0.

- [ ] **Step 3: Curl smoke tests (requires running API + DB)**

  Start the API if not running. Then:

  ```bash
  # Step a: create a shared workspace
  WS=$(curl -sf -X POST http://localhost:8080/v1/workspaces \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer <dev_token>" \
    -d '{"name":"qa"}' | jq -r '.id')
  echo "Created workspace: $WS"

  # Step b: delete it — expect 204
  curl -sv -X DELETE http://localhost:8080/v1/workspaces/$WS \
    -H "Authorization: Bearer <dev_token>"
  # Expected: HTTP 204

  # Step c: try to delete the personal workspace — expect 409
  PERSONAL_ID=$(curl -sf http://localhost:8080/v1/workspaces \
    -H "Authorization: Bearer <dev_token>" | jq -r '.workspaces[] | select(.kind=="personal") | .id')
  curl -sv -X DELETE http://localhost:8080/v1/workspaces/$PERSONAL_ID \
    -H "Authorization: Bearer <dev_token>"
  # Expected: HTTP 409 {"error":"cannot delete personal workspace"}
  ```

  Note: `<dev_token>` — obtain from the local dev environment (e.g., from the browser's network panel or via Keycloak dev token endpoint). If running in dev mode with `PLATFORM_ADMIN_USERS` env var, that sub's JWT will also test admin deletion.

- [ ] **Step 4: Write report**

  Create `/Users/nk/workspaces/works_on_my_machine/.superpowers/sdd/feat-workspace-crud-report.md`:

  ```markdown
  # Workspace CRUD Feature Report

  ## Status
  COMPLETE

  ## Commit
  <git log --oneline -4 output here>

  ## DeleteWorkspace (Backend)
  - Personal workspaces: refused with 409 "cannot delete personal workspace"
  - Owner/admin only: non-owner → 403 "only the owner can delete a workspace"; isAdmin=true bypasses owner check
  - Route: DELETE /v1/workspaces/{id} registered in main.go

  ## WorkspaceSwitcher (Frontend)
  - Search: case-insensitive substring filter on workspace name
  - Create: inline form at bottom → createWorkspace → refetch → switch to new workspace
  - Delete: trash icon on shared rows only → window.confirm → deleteWorkspace → refetch → fall back to personal if active was deleted
  - Styling: dark/hairline/surface tokens consistent with TopBar

  ## Build / Test
  - go build ./... && go vet ./...: CLEAN
  - go test ./...: PASS (or SKIP if no DB)
  - pnpm build: CLEAN

  ## Curl Smoke
  - POST /v1/workspaces {name:"qa"} → 201 with id
  - DELETE /v1/workspaces/{id} → 204
  - DELETE /v1/workspaces/{personal_id} → 409

  ## Concerns
  - None
  ```

  Do NOT git-add `.superpowers/`.

- [ ] **Step 5: Final commit (all work + report)**

  ```bash
  cd /Users/nk/workspaces/works_on_my_machine
  git add platform/api/internal/store/workspace.go \
         platform/api/internal/store/workspace_test.go \
         platform/api/internal/handler/workspace_handler.go \
         platform/api/cmd/api/main.go \
         platform/web/src/lib/api.ts \
         platform/web/src/components/WorkspaceSwitcher.tsx \
         platform/web/src/components/TopBar.tsx
  git commit -m "Workspace switcher: create + search + delete (+ DeleteWorkspace API)"
  ```

  (If individual task commits were already made, this step may be a no-op or only commit the report. Check `git status` first.)
