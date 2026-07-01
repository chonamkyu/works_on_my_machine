package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/mcp"
	"github.com/nk/platform/api/internal/store"
)

// ListMCPServers returns all registered MCP servers with auth masked.
func (h *Handler) ListMCPServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": h.Store.ListMCPServers()})
}

// ListAllMCPTools returns the static tool catalog across all enabled servers.
func (h *Handler) ListAllMCPTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tools": h.Store.ListAllMCPTools()})
}

// ListMCPTools performs a live tools/list against a single server.
func (h *Handler) ListMCPTools(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tools, err := h.Store.ListMCPTools(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tools": tools})
}

// GetMCPServerStats returns daily call counts for the server.
func (h *Handler) GetMCPServerStats(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	days := 14
	if d := r.URL.Query().Get("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 {
			if n > 90 {
				n = 90
			}
			days = n
		}
	}
	writeJSON(w, http.StatusOK, h.Store.GetMCPServerStats(id, days))
}

// PingMCPServer performs a live health check against the server.
func (h *Handler) PingMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	result, err := h.Store.PingMCPServer(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// CallMCPTool proxies a tool-call to the named MCP server.
func (h *Handler) CallMCPTool(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var body struct {
		Tool      string         `json:"tool"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Tool == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "tool is required"})
		return
	}

	// Check disabled before doing any network work.
	if h.Store.IsMCPToolDisabled(id, body.Tool) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "tool disabled"})
		return
	}

	sv, err := h.Store.GetMCPServer(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}

	client := mcp.NewClient(sv.BaseURL, sv.AuthHeader, sv.AuthValue, sv.InsecureSkipVerify)
	ctx := r.Context()

	start := time.Now()
	var callErr error
	var result *mcp.CallToolResult

	if _, err := client.Initialize(ctx); err != nil {
		callErr = err
	} else {
		result, callErr = client.CallTool(ctx, body.Tool, body.Arguments)
	}

	durationMs := int(time.Since(start).Milliseconds())
	success := callErr == nil

	wsID := auth.MustWorkspaceID(ctx)
	// Record call best-effort in a goroutine.
	go h.Store.RecordMCPCall(id, wsID, body.Tool, durationMs, success)

	if callErr != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": callErr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// CreateMCPServer registers a new MCP server. Admin only.
func (h *Handler) CreateMCPServer(w http.ResponseWriter, r *http.Request) {
	var in store.MCPServerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Default enabled to true when not explicitly set.
	if in.Enabled == nil {
		in.Enabled = store.BoolPtr(true)
	}
	sv, err := h.Store.CreateMCPServer(auth.MustUserID(r.Context()), in)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, "mcp.create", "mcp_server", sv.ID, sv.Name)
	writeJSON(w, http.StatusOK, sv)
}

// UpdateMCPServer modifies an existing MCP server. Admin only.
func (h *Handler) UpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var in store.MCPServerInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	sv, err := h.Store.UpdateMCPServer(id, in)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	// Ensure auth_value is never returned.
	sv.AuthValue = ""
	h.audit(r, "mcp.update", "mcp_server", id, sv.Name)
	writeJSON(w, http.StatusOK, sv)
}

// DeleteMCPServer removes an MCP server. Admin only.
func (h *Handler) DeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Store.DeleteMCPServer(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, "mcp.delete", "mcp_server", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// SetMCPToolDisabled enables or disables a specific tool. Admin only.
func (h *Handler) SetMCPToolDisabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tool := chi.URLParam(r, "tool")

	var body struct {
		Disabled bool   `json:"disabled"`
		Reason   string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	if err := h.Store.SetMCPToolDisabled(id, tool, auth.MustUserID(r.Context()), body.Reason, body.Disabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, "mcp.tool_disabled", "mcp_tool", id, tool)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
