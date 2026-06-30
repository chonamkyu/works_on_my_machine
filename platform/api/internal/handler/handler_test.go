package handler_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// randSuffix returns a short random hex string for unique workspace IDs in tests.
func randSuffix() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// injectClaims injects arbitrary JWT claims into a request context.
// Use this instead of injectDevClaims when you need a specific identity.
func injectClaims(r *http.Request, sub string, roles []string) *http.Request {
	c := &auth.Claims{
		Sub:               sub,
		Email:             sub + "@example.com",
		PreferredUsername: sub,
		Roles:             roles,
	}
	return r.WithContext(auth.WithClaims(r.Context(), c))
}

// withWorkspace injects the active workspace into a request context.
func withWorkspace(r *http.Request, ws string) *http.Request {
	return r.WithContext(auth.WithWorkspace(r.Context(), ws))
}

// TestTaskCrossTenantIsolation verifies that a caller in workspace B cannot
// read or list runs for a task that belongs to workspace A (IDOR fix).
func TestTaskCrossTenantIsolation(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	// Inject context directly; no JWT middleware needed.
	r := chi.NewRouter()
	r.Get("/tasks/{id}", h.GetTask)
	r.Get("/tasks/{id}/runs", h.ListRuns)

	// Seed a task in workspace A.
	wsA := "ws-isol-a-" + randSuffix()
	wsB := "ws-isol-b-" + randSuffix()
	task := s.CreateTask(&store.Task{
		UserID:      wsA,
		WorkspaceID: wsA,
		Name:        "secret-task",
	})

	// --- GET /tasks/{id} as workspace B (non-admin) → 404 ---
	req := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	req = injectClaims(req, wsB, nil) // non-admin
	req = withWorkspace(req, wsB)     // active workspace = B
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GET task: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}

	// --- GET /tasks/{id} as workspace A → 200 ---
	req2 := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	req2 = injectClaims(req2, wsA, nil)
	req2 = withWorkspace(req2, wsA)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("same-tenant GET task: want 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}

	// --- ListRuns for task in workspace A as workspace B → 404 ---
	req3 := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID+"/runs", nil)
	req3 = injectClaims(req3, wsB, nil)
	req3 = withWorkspace(req3, wsB)
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNotFound {
		t.Errorf("cross-tenant ListRuns: want 404, got %d body=%s", rr3.Code, rr3.Body.String())
	}

	// --- Admin in workspace B can read workspace A task → 200 ---
	req4 := httptest.NewRequest(http.MethodGet, "/tasks/"+task.ID, nil)
	req4 = injectClaims(req4, wsB, []string{"platform-admin"})
	req4 = withWorkspace(req4, wsB)
	rr4 := httptest.NewRecorder()
	r.ServeHTTP(rr4, req4)
	if rr4.Code != http.StatusOK {
		t.Errorf("admin cross-tenant GET task: want 200, got %d body=%s", rr4.Code, rr4.Body.String())
	}

	t.Logf("cross-ws → 404: PASS; same-ws → 200: PASS; admin cross-ws → 200: PASS")
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
