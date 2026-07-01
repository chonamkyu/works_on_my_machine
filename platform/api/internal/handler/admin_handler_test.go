package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/db"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/store"
)

// buildAdminRouter wires up the four admin observability routes under the
// AdminOnly middleware. Claims and workspace are injected into the request
// context directly by the caller (via injectClaims + withWorkspace), so the
// router does not include the Authenticate middleware — matching the pattern
// used in TestTaskCrossTenantIsolation and other handler tests.
func buildAdminRouter(h *handler.Handler, s *store.Memory) chi.Router {
	authMW := auth.New(false)
	r := chi.NewRouter()
	// No authMW.Authenticate — callers inject claims directly into the context.
	r.Group(func(r chi.Router) {
		r.Use(authMW.AdminOnly)
		r.Get("/v1/admin/overview", h.AdminOverview)
		r.Get("/v1/admin/workspaces", h.AdminListWorkspaces)
		r.Get("/v1/admin/triggers", h.AdminListTriggers)
		r.Get("/v1/admin/self-evolve", h.AdminSelfEvolve)
	})
	return r
}

// TestAdminOverview_AdminGets200 verifies that an admin caller gets 200 with
// expected JSON keys from GET /v1/admin/overview.
func TestAdminOverview_AdminGets200(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("skipping admin handler DB test: db.Open: %v", err)
	}
	defer pool.Close()

	s := store.NewMemory(pool)
	evo := store.NewEvolutionStore(pool)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, evo, nil, nil, nil)
	r := buildAdminRouter(h, s)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/overview", nil)
	req = injectClaims(req, "admin-user", []string{"platform-admin"})
	req = withWorkspace(req, "admin-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/overview as admin: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode overview response: %v", err)
	}
	if _, ok := resp["counts"]; !ok {
		t.Errorf("response missing 'counts' key; got keys: %v", mapKeys(resp))
	}
	if _, ok := resp["recentAudit"]; !ok {
		t.Errorf("response missing 'recentAudit' key; got keys: %v", mapKeys(resp))
	}
	counts, ok := resp["counts"].(map[string]any)
	if !ok {
		t.Fatalf("counts is not a map: %T", resp["counts"])
	}
	for _, key := range []string{"users", "workspaces", "mcpServers", "skills", "runningServices", "activeRuns", "pendingProposals", "pendingEvolutionPRs"} {
		if _, ok := counts[key]; !ok {
			t.Errorf("counts missing key %q", key)
		}
	}
	t.Logf("GET /v1/admin/overview: 200 counts=%v", counts)
}

// TestAdminTriggers_AdminGets200 verifies that GET /v1/admin/triggers returns
// 200 with a "triggers" array for an admin caller.
func TestAdminTriggers_AdminGets200(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("skipping admin handler DB test: db.Open: %v", err)
	}
	defer pool.Close()

	s := store.NewMemory(pool)
	evo := store.NewEvolutionStore(pool)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, evo, nil, nil, nil)
	r := buildAdminRouter(h, s)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/triggers", nil)
	req = injectClaims(req, "admin-user", []string{"platform-admin"})
	req = withWorkspace(req, "admin-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/triggers as admin: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode triggers response: %v", err)
	}
	if _, ok := resp["triggers"]; !ok {
		t.Errorf("response missing 'triggers' key; got keys: %v", mapKeys(resp))
	}
	t.Logf("GET /v1/admin/triggers: 200 (trigger rows returned)")
}

// TestAdminEndpoints_NonAdmin403 verifies that a non-admin caller gets 403 on
// every admin observability endpoint.
func TestAdminEndpoints_NonAdmin403(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("skipping admin handler DB test: db.Open: %v", err)
	}
	defer pool.Close()

	s := store.NewMemory(pool)
	evo := store.NewEvolutionStore(pool)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, evo, nil, nil, nil)
	r := buildAdminRouter(h, s)

	endpoints := []string{
		"/v1/admin/overview",
		"/v1/admin/workspaces",
		"/v1/admin/triggers",
		"/v1/admin/self-evolve",
	}

	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			// non-admin: no platform-admin role
			req = injectClaims(req, "non-admin-user", nil)
			req = withWorkspace(req, "non-admin-user")
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("GET %s as non-admin: want 403, got %d body=%s", ep, rr.Code, rr.Body.String())
			}
		})
	}
	t.Logf("all admin observability endpoints → 403 for non-admin: PASS")
}

// TestAdminWorkspaces_AdminGets200 verifies GET /v1/admin/workspaces returns
// 200 with a "workspaces" array that contains cross-workspace data.
func TestAdminWorkspaces_AdminGets200(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("skipping admin handler DB test: db.Open: %v", err)
	}
	defer pool.Close()

	s := store.NewMemory(pool)
	evo := store.NewEvolutionStore(pool)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, evo, nil, nil, nil)
	r := buildAdminRouter(h, s)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/workspaces", nil)
	req = injectClaims(req, "admin-user", []string{"platform-admin"})
	req = withWorkspace(req, "admin-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/workspaces as admin: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode workspaces response: %v", err)
	}
	if _, ok := resp["workspaces"]; !ok {
		t.Errorf("response missing 'workspaces' key; got keys: %v", mapKeys(resp))
	}
	t.Logf("GET /v1/admin/workspaces: 200")
}

// TestAdminSelfEvolve_AdminGets200 verifies GET /v1/admin/self-evolve returns
// 200 with the expected keys.
func TestAdminSelfEvolve_AdminGets200(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("skipping admin handler DB test: db.Open: %v", err)
	}
	defer pool.Close()

	s := store.NewMemory(pool)
	evo := store.NewEvolutionStore(pool)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, evo, nil, nil, nil)
	r := buildAdminRouter(h, s)

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/self-evolve", nil)
	req = injectClaims(req, "admin-user", []string{"platform-admin"})
	req = withWorkspace(req, "admin-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/admin/self-evolve as admin: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode self-evolve response: %v", err)
	}
	for _, key := range []string{"proposals", "evolutionPRs", "recentSkillVersions"} {
		if _, ok := resp[key]; !ok {
			t.Errorf("response missing %q key; got keys: %v", key, mapKeys(resp))
		}
	}
	t.Logf("GET /v1/admin/self-evolve: 200 keys=%v", mapKeys(resp))
}

// mapKeys returns sorted key names for debug output.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
