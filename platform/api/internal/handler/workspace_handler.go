package handler

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/store"
)

// ListWorkspaces returns all workspaces the caller belongs to.
//
// GET /v1/workspaces
func (h *Handler) ListWorkspaces(w http.ResponseWriter, r *http.Request) {
	sub := auth.MustUserID(r.Context())
	if sub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
		return
	}
	workspaces := h.Store.ListWorkspacesForUser(sub)
	if workspaces == nil {
		workspaces = []*store.Workspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": workspaces})
}

// CreateWorkspace creates a new shared workspace owned by the caller.
//
// POST /v1/workspaces  body: {"name": "..."}
func (h *Handler) CreateWorkspace(w http.ResponseWriter, r *http.Request) {
	sub := auth.MustUserID(r.Context())
	if sub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}
	ws, err := h.Store.CreateWorkspace(sub, body.Name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, ws)
}

// InviteMember adds a user to a workspace. Caller must be workspace owner.
//
// POST /v1/workspaces/{id}/members  body: {"userId": "...", "role": "..."}
func (h *Handler) InviteMember(w http.ResponseWriter, r *http.Request) {
	callerSub := auth.MustUserID(r.Context())
	if callerSub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
		return
	}
	wsID := chi.URLParam(r, "id")
	var body struct {
		UserID string `json:"userId"`
		Role   string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "userId required"})
		return
	}
	if body.Role == "" {
		body.Role = "member"
	}
	if err := h.Store.InviteMember(wsID, callerSub, body.UserID, body.Role); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// RemoveMember removes a user from a workspace.
//
// DELETE /v1/workspaces/{id}/members/{uid}
func (h *Handler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	callerSub := auth.MustUserID(r.Context())
	if callerSub == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthenticated"})
		return
	}
	wsID := chi.URLParam(r, "id")
	uid := chi.URLParam(r, "uid")
	if err := h.Store.RemoveMember(wsID, callerSub, uid); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListMembers returns all members of a workspace.
// Caller must be a member of the workspace or a platform admin.
//
// GET /v1/workspaces/{id}/members
func (h *Handler) ListMembers(w http.ResponseWriter, r *http.Request) {
	wsID := chi.URLParam(r, "id")
	caller := auth.MustUserID(r.Context())
	if !auth.IsAdmin(r.Context()) && !h.Store.IsMember(wsID, caller) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "not a member of this workspace"})
		return
	}
	members := h.Store.ListMembers(wsID)
	if members == nil {
		members = []*store.Member{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}
