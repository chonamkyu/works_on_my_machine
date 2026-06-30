package handler

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/store"
)

// UpsertUserMiddleware runs on every authenticated request. It upserts the
// user row + personal workspace so they always exist before any FK reference.
// Errors are logged but never block the request.
func (h *Handler) UpsertUserMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := auth.ClaimsFromCtx(r.Context()); c != nil {
			displayName := c.PreferredUsername
			if displayName == "" {
				displayName = c.Sub
			}
			if err := h.Store.UpsertUserOnLogin(c.Sub, c.Email, c.PreferredUsername); err != nil {
				log.Printf("UpsertUserMiddleware: UpsertUserOnLogin(%s): %v", c.Sub, err)
			} else if err := h.Store.EnsurePersonal(c.Sub, displayName); err != nil {
				log.Printf("UpsertUserMiddleware: EnsurePersonal(%s): %v", c.Sub, err)
			} else {
				// Best-effort login audit — don't block on error.
				_ = h.Store.RecordAudit(store.AuditEntry{
					ActorSub:     c.Sub,
					ActorEmail:   c.Email,
					Action:       "login",
					ResourceType: "user",
					ResourceID:   c.Sub,
					WorkspaceID:  c.Sub,
				})
			}
		}
		next.ServeHTTP(w, r)
	})
}

// Me returns the caller's identity + admin flag + workspaces.
//
// GET /v1/me
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	c := auth.ClaimsFromCtx(r.Context())
	if c == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
		return
	}
	workspaces := h.Store.ListWorkspacesForUser(c.Sub)
	if workspaces == nil {
		workspaces = []*store.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sub":        c.Sub,
		"email":      c.Email,
		"isAdmin":    auth.IsAdmin(r.Context()),
		"workspaces": workspaces,
	})
}

// ListUsers returns all users (admin only).
//
// GET /v1/admin/users
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users := h.Store.ListUsers()
	if users == nil {
		users = []*store.User{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// SetUserAdmin sets or clears the is_admin flag.
//
// POST /v1/admin/users/{sub}/admin  body: {"isAdmin": bool}
func (h *Handler) SetUserAdmin(w http.ResponseWriter, r *http.Request) {
	sub := chi.URLParam(r, "sub")
	var body struct {
		IsAdmin bool `json:"isAdmin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := h.Store.SetUserAdmin(sub, body.IsAdmin); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// SetUserDisabled sets or clears the disabled flag.
//
// POST /v1/admin/users/{sub}/disabled  body: {"disabled": bool}
func (h *Handler) SetUserDisabled(w http.ResponseWriter, r *http.Request) {
	sub := chi.URLParam(r, "sub")
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := h.Store.SetUserDisabled(sub, body.Disabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ListAudit returns audit log entries (admin: all workspaces).
//
// GET /v1/admin/audit?limit=N
func (h *Handler) ListAudit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if l := r.URL.Query().Get("limit"); l != "" {
		var n int
		if err := json.Unmarshal([]byte(l), &n); err == nil && n > 0 {
			limit = n
		}
	}
	entries := h.Store.ListAudit("", limit)
	if entries == nil {
		entries = []*store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
