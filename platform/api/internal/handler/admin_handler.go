package handler

import (
	"net/http"

	"github.com/nk/platform/api/internal/store"
)

// AdminOverview handles GET /v1/admin/overview.
// Returns aggregate counts + the 10 most recent audit entries.
func (h *Handler) AdminOverview(w http.ResponseWriter, r *http.Request) {
	counts, err := h.Store.AdminOverview()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	recentAudit := h.Store.ListAudit("", 10)
	if recentAudit == nil {
		recentAudit = []*store.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"counts":      counts,
		"recentAudit": recentAudit,
	})
}

// AdminListWorkspaces handles GET /v1/admin/workspaces.
// Returns all workspaces across the platform with member and task counts.
func (h *Handler) AdminListWorkspaces(w http.ResponseWriter, r *http.Request) {
	workspaces, err := h.Store.ListAllWorkspaces()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if workspaces == nil {
		workspaces = []*store.AdminWorkspace{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workspaces": workspaces})
}

// AdminListTriggers handles GET /v1/admin/triggers.
// Returns all trigger_state rows across all workspaces, joined to task names.
func (h *Handler) AdminListTriggers(w http.ResponseWriter, r *http.Request) {
	triggers, err := h.Store.ListAllTriggerState()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if triggers == nil {
		triggers = []*store.AdminTriggerState{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"triggers": triggers})
}

// AdminSelfEvolve handles GET /v1/admin/self-evolve.
// Returns all global skill proposals, all-workspace evolution PRs, and the
// latest skill versions so the UI can render the proposal→accept→effect timeline.
func (h *Handler) AdminSelfEvolve(w http.ResponseWriter, r *http.Request) {
	proposals := h.Store.ListSkillProposals(store.GlobalScope, "")
	if proposals == nil {
		proposals = []*store.SkillProposal{}
	}

	evolutionPRs, err := h.Evolution.ListAllEvolutionPRs()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if evolutionPRs == nil {
		evolutionPRs = []*store.EvolutionPR{}
	}

	skills, err := h.Store.ListSkills(r.Context())
	if err != nil {
		// Non-fatal: return empty list rather than failing the whole response.
		skills = []*store.Skill{}
	}
	if skills == nil {
		skills = []*store.Skill{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"proposals":          proposals,
		"evolutionPRs":       evolutionPRs,
		"recentSkillVersions": skills,
	})
}
