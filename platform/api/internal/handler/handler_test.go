package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/db"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/store"
)

// openTestDB opens a live DB for handler tests. It requires PLATFORM_DB_URL to
// be set; if not, the test is skipped.
func openTestDB(t *testing.T) *store.Memory {
	t.Helper()
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("skipping handler DB test: db.Open: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	s := store.NewMemory(pool)
	s.Seed(context.Background(), "dev")
	return s
}

// injectDevClaims injects dev-mode auth claims into a request context — the
// same claims auth.New(true) injects in production dev mode.
func injectDevClaims(r *http.Request) *http.Request {
	c := &auth.Claims{
		Sub:               "dev",
		Email:             "dev@example.com",
		PreferredUsername: "dev",
		Roles:             []string{"platform-admin"},
	}
	// Use auth package's exported context setter via Authenticate middleware
	// with devMode=true.
	mw := auth.New(true)
	var captured *http.Request
	inner := http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		captured = req
	})
	rr := httptest.NewRecorder()
	mw.Authenticate(inner).ServeHTTP(rr, r)
	_ = c // c already embedded by devMode
	return captured
}

// TestMeDevMode: GET /v1/me with dev-mode auth must return 200 + {sub:"dev", isAdmin:true, workspaces:[...]}.
func TestMeDevMode(t *testing.T) {
	s := openTestDB(t)

	// Bootstrap: ensure the dev user + personal workspace exist.
	auth.SetAdminEnv("") // clear env list; platform-admin role grants admin in dev mode
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	r := chi.NewRouter()
	authMW := auth.New(true)
	r.Use(authMW.Authenticate)
	r.Use(h.UpsertUserMiddleware)
	r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(s)(next) })
	r.Get("/v1/me", h.Me)

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/me: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["sub"] != "dev" {
		t.Errorf("want sub=dev, got %v", resp["sub"])
	}
	if resp["isAdmin"] != true {
		t.Errorf("want isAdmin=true, got %v", resp["isAdmin"])
	}
	wss, ok := resp["workspaces"].([]any)
	if !ok {
		t.Fatalf("workspaces not an array: %T", resp["workspaces"])
	}
	t.Logf("workspaces count: %d", len(wss))
}

// TestWorkspaceCreateList: POST /v1/workspaces then GET /v1/workspaces must include the new workspace.
func TestWorkspaceCreateList(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	r := chi.NewRouter()
	authMW := auth.New(true)
	r.Use(authMW.Authenticate)
	r.Use(h.UpsertUserMiddleware)
	r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(s)(next) })
	r.Post("/v1/workspaces", h.CreateWorkspace)
	r.Get("/v1/workspaces", h.ListWorkspaces)

	// Create workspace
	body := `{"name":"test-ws"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/workspaces", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("POST /v1/workspaces: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var ws store.Workspace
	if err := json.Unmarshal(rr.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	if ws.Name != "test-ws" {
		t.Errorf("want name=test-ws, got %q", ws.Name)
	}

	// List workspaces — must include the one we just created
	req2 := httptest.NewRequest(http.MethodGet, "/v1/workspaces", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("GET /v1/workspaces: want 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	var listResp struct {
		Workspaces []store.Workspace `json:"workspaces"`
	}
	if err := json.Unmarshal(rr2.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	found := false
	for _, w := range listResp.Workspaces {
		if w.ID == ws.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("created workspace %s not found in list; got %+v", ws.ID, listResp.Workspaces)
	}
}
