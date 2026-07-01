package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/store"
)

// skillRepoAudit records a best-effort audit entry for a skill-repo mutation.
// It does not need a workspace context — skills are global, so we use the
// actor's sub as the workspace_id (consistent with how the admin sub is used
// elsewhere when there is no per-tenant scope).
func (h *Handler) skillRepoAudit(r *http.Request, action, skillName string) {
	ctx := r.Context()
	sub := auth.MustUserID(ctx)
	email := ""
	if c := auth.ClaimsFromCtx(ctx); c != nil {
		email = c.Email
	}
	_ = h.Store.RecordAudit(store.AuditEntry{
		ActorSub:     sub,
		ActorEmail:   email,
		Action:       action,
		ResourceType: "skill",
		ResourceID:   skillName,
		ResourceName: skillName,
		WorkspaceID:  sub,
	})
}

// ListGlobalSkills handles GET /v1/skill-repo.
// Returns all skills in the global repository. Open to any authenticated caller.
func (h *Handler) ListGlobalSkills(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	skills, err := h.Store.ListSkills(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if skills == nil {
		skills = []*store.Skill{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"skills": skills})
}

// GetGlobalSkill handles GET /v1/skill-repo/{name}.
// Returns skill metadata, all versions, and all aliases.
func (h *Handler) GetGlobalSkill(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	skill, err := h.Store.GetSkill(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	versions, err := h.Store.ListSkillVersions(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if versions == nil {
		versions = []*store.SkillVersion{}
	}

	aliases, err := h.Store.ListSkillAliases(name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if aliases == nil {
		aliases = []*store.SkillAlias{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"skill":    skill,
		"versions": versions,
		"aliases":  aliases,
	})
}

// GetGlobalSkillVersion handles GET /v1/skill-repo/{name}/versions/{n}.
func (h *Handler) GetGlobalSkillVersion(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	nStr := chi.URLParam(r, "n")

	n, err := strconv.Atoi(nStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "version must be an integer"})
		return
	}

	ver, err := h.Store.GetSkillVersion(name, n)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ver)
}

// ResolveGlobalSkill handles GET /v1/skill-repo/resolve?ref=name@ver.
func (h *Handler) ResolveGlobalSkill(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("ref")
	if ref == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "ref query param required"})
		return
	}

	parsed, err := store.ParseSkillRef(ref)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	ver, err := h.Store.ResolveSkill(parsed)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ver)
}

// CreateGlobalSkill handles POST /v1/skill-repo (admin only).
// Body: {name, description, tags, body, comment}
func (h *Handler) CreateGlobalSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Body        string   `json:"body"`
		Comment     string   `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}

	createdBy := auth.MustUserID(r.Context())
	skill, ver, err := h.Store.CreateSkill(createdBy, body.Name, body.Description, body.Tags, body.Body, body.Comment)
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") || strings.Contains(err.Error(), "unique") {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	h.skillRepoAudit(r, "skill.create", skill.Name)
	writeJSON(w, http.StatusCreated, map[string]any{"skill": skill, "version": ver})
}

// PublishGlobalSkillVersion handles POST /v1/skill-repo/{name}/versions (admin only).
// Body: {description, tags, body, comment}
func (h *Handler) PublishGlobalSkillVersion(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var body struct {
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Body        string   `json:"body"`
		Comment     string   `json:"comment"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	createdBy := auth.MustUserID(r.Context())
	ver, err := h.Store.PublishSkillVersion(name, createdBy, body.Description, body.Tags, body.Body, body.Comment)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	h.skillRepoAudit(r, "skill.publish", name)
	writeJSON(w, http.StatusCreated, ver)
}

// MoveGlobalSkillAlias handles POST /v1/skill-repo/{name}/aliases (admin only).
// Body: {alias, versionNo}
func (h *Handler) MoveGlobalSkillAlias(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	var body struct {
		Alias     string `json:"alias"`
		VersionNo int    `json:"versionNo"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Alias == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "alias required"})
		return
	}

	updatedBy := auth.MustUserID(r.Context())
	alias, err := h.Store.MoveSkillAlias(name, updatedBy, body.Alias, body.VersionNo)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	h.skillRepoAudit(r, "skill.move_alias", name)
	writeJSON(w, http.StatusOK, alias)
}

// DeleteGlobalSkill handles DELETE /v1/skill-repo/{name} (admin only).
func (h *Handler) DeleteGlobalSkill(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	deletedBy := auth.MustUserID(r.Context())
	if err := h.Store.DeleteSkill(name, deletedBy); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	h.skillRepoAudit(r, "skill.delete", name)
	w.WriteHeader(http.StatusNoContent)
}
