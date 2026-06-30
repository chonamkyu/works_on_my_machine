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
	"time"

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

// TestChatFirstMessageToTask verifies the "first message to a task" flow:
// when sessionID == taskID and no explicit session row exists yet, POST /chat/<taskID>/messages
// must succeed (200, no 404), and the response must be a single well-formed JSON body.
// A cross-workspace request to the same task ID must still return 404 exactly once.
func TestChatFirstMessageToTask(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	r := chi.NewRouter()
	r.Post("/v1/chat/{sessionId}/messages", h.ChatMessage)
	r.Get("/v1/chat/{sessionId}/messages", h.ListChatMessages)

	wsA := "ws-chat-a-" + randSuffix()
	wsB := "ws-chat-b-" + randSuffix()

	// Create a task in workspace A (no session row — simulates first-message flow).
	task := s.CreateTask(&store.Task{
		UserID:      wsA,
		WorkspaceID: wsA,
		Name:        "chat-task",
	})

	// --- POST /v1/chat/<taskID>/messages as workspace A → must succeed (not 404) ---
	body := `{"content":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/"+task.ID+"/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectClaims(req, wsA, nil)
	req = withWorkspace(req, wsA)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code == http.StatusNotFound {
		t.Errorf("first-message-to-task: want non-404, got 404; double-write regression? body=%s", rr.Body.String())
	}
	// Ensure response is valid JSON (single write — no double-write garbage).
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Errorf("first-message-to-task: response is not valid JSON (double-write?): %v; body=%q", err, rr.Body.String())
	}

	// --- GET /v1/chat/<taskID>/messages as workspace A → must succeed ---
	req2 := httptest.NewRequest(http.MethodGet, "/v1/chat/"+task.ID+"/messages", nil)
	req2 = injectClaims(req2, wsA, nil)
	req2 = withWorkspace(req2, wsA)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code == http.StatusNotFound {
		t.Errorf("ListChatMessages first-message-to-task: want non-404, got 404; body=%s", rr2.Body.String())
	}
	var listResp map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &listResp); err != nil {
		t.Errorf("ListChatMessages: response is not valid JSON: %v; body=%q", err, rr2.Body.String())
	}

	// --- Cross-workspace: workspace B posting to workspace A's task → single 404 ---
	req3 := httptest.NewRequest(http.MethodPost, "/v1/chat/"+task.ID+"/messages", strings.NewReader(body))
	req3.Header.Set("Content-Type", "application/json")
	req3 = injectClaims(req3, wsB, nil)
	req3 = withWorkspace(req3, wsB)
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusNotFound {
		t.Errorf("cross-workspace ChatMessage: want 404, got %d; body=%s", rr3.Code, rr3.Body.String())
	}
	// Must be valid single JSON (no double-write even for the 404 path).
	var errResp map[string]any
	if err := json.Unmarshal(rr3.Body.Bytes(), &errResp); err != nil {
		t.Errorf("cross-workspace ChatMessage: 404 response is not valid JSON (double-write?): %v; body=%q", err, rr3.Body.String())
	}

	t.Logf("first-message-to-task → %d: PASS; cross-ws → 404: PASS", rr.Code)
}

// TestActivityStreamWorkspaceIsolation verifies that the ActivityStream SSE
// endpoint does NOT send events from wsB to a subscriber in wsA.
// This test verifies the fix for C1: cross-tenant SSE isolation.
func TestActivityStreamWorkspaceIsolation(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	b := store.NewBus()
	h := handler.New(s, b, nil, nil, nil, nil, nil, nil, nil, nil)

	wsA := "ws-sse-a-" + randSuffix()
	wsB := "ws-sse-b-" + randSuffix()

	// Subscriber is workspace A
	req := httptest.NewRequest(http.MethodGet, "/v1/users/"+wsA+"/activity", nil)
	req = injectClaims(req, wsA, nil)
	req = withWorkspace(req, wsA)

	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	done := make(chan struct{})
	var body string
	go func() {
		defer close(done)
		hRouter := chi.NewRouter()
		hRouter.Get("/v1/users/{id}/activity", h.ActivityStream)
		hRouter.ServeHTTP(rr, req)
		body = rr.Body.String()
	}()

	// Give the SSE handler a moment to subscribe
	time.Sleep(50 * time.Millisecond)

	// Publish an event for wsB — should NOT be received by wsA subscriber
	b.Publish(store.Event{Channel: "task:created:task-b", UserID: wsB, Payload: map[string]any{"id": "task-b"}})
	// Publish an event for wsA — SHOULD be received
	b.Publish(store.Event{Channel: "task:created:task-a", UserID: wsA, Payload: map[string]any{"id": "task-a"}})

	// Wait for context timeout (so SSE stream closes)
	<-done

	if strings.Contains(body, "task-b") {
		t.Errorf("cross-workspace event leaked to wsA subscriber: body contains task-b\nbody=%s", body)
	}
	if !strings.Contains(body, "task-a") {
		t.Errorf("same-workspace event not received by wsA subscriber: body missing task-a\nbody=%s", body)
	}
	t.Logf("SSE isolation: wsB event not leaked, wsA event received: PASS")
}

// TestDeleteMemoryCrossTenant verifies that a workspace cannot delete memory
// entries belonging to a different workspace.
func TestDeleteMemoryCrossTenant(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("no db: %v", err)
	}
	defer pool.Close()
	ms := store.NewMemoryStore(pool)

	wsA := "ws-delmem-a-" + randSuffix()
	wsB := "ws-delmem-b-" + randSuffix()

	// Write an entry for wsA
	entry := ms.Write(&store.MemoryEntry{
		WorkspaceID: wsA,
		UserID:      wsA,
		Scope:       store.ScopeUser,
		ScopeKey:    "test-key",
		Topic:       "test-topic",
		Content:     "test-value",
	})
	if entry == nil {
		t.Fatal("Write returned nil")
	}

	// Try to delete as wsB — should return false (0 rows affected)
	deleted := ms.Delete(entry.ID, wsB)
	if deleted {
		t.Error("cross-tenant delete succeeded, expected failure")
	}

	// Verify entry still exists by listing
	entries := ms.List(wsA, store.ScopeUser, "")
	found := false
	for _, e := range entries {
		if e.ID == entry.ID {
			found = true
			break
		}
	}
	if !found {
		t.Error("entry was deleted by cross-tenant delete, should still exist")
	}

	// Delete as wsA — should succeed
	deleted = ms.Delete(entry.ID, wsA)
	if !deleted {
		t.Error("same-tenant delete failed, expected success")
	}
	t.Log("cross-tenant delete blocked, same-tenant delete succeeded: PASS")
}

// TestAcceptEvolutionPRCrossTenant verifies that GetEvolutionPR returns 404
// when a different workspace attempts to read a PR that belongs to another workspace.
func TestAcceptEvolutionPRCrossTenant(t *testing.T) {
	pool, err := db.Open(context.Background())
	if err != nil {
		t.Skipf("no db: %v", err)
	}
	defer pool.Close()
	s := store.NewMemory(pool)
	s.Seed(context.Background(), "dev")
	evo := store.NewEvolutionStore(pool)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	wsA := "ws-evo-a-" + randSuffix()
	wsB := "ws-evo-b-" + randSuffix()

	// Create a real task in wsA (evolution_prs.task_id has a FK → tasks.id).
	task := s.CreateTask(&store.Task{
		UserID:      wsA,
		WorkspaceID: wsA,
		Name:        "evo-test-task",
	})

	// Create a PR for wsA
	pr := evo.Create(&store.EvolutionPR{
		TaskID:      task.ID,
		UserID:      wsA,
		WorkspaceID: wsA,
		Reason:      "test",
	})

	// Try to GET it as wsB — should 404
	h := handler.New(s, nil, nil, nil, nil, nil, evo, nil, nil, nil)
	r := chi.NewRouter()
	r.Get("/evolution/{id}", h.GetEvolutionPR)

	req := httptest.NewRequest(http.MethodGet, "/evolution/"+pr.ID, nil)
	req = injectClaims(req, wsB, nil)
	req = withWorkspace(req, wsB)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Errorf("cross-tenant GetEvolutionPR: want 404, got %d body=%s", rr.Code, rr.Body.String())
	}

	// GET as wsA — should 200
	req2 := httptest.NewRequest(http.MethodGet, "/evolution/"+pr.ID, nil)
	req2 = injectClaims(req2, wsA, nil)
	req2 = withWorkspace(req2, wsA)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Errorf("same-tenant GetEvolutionPR: want 200, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	t.Log("cross-tenant GetEvolutionPR → 404, same-tenant → 200: PASS")
}

// TestCreateTaskAudit verifies that creating a task records a task.create
// audit log entry for the calling workspace.
func TestCreateTaskAudit(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	wsA := "ws-audit-" + randSuffix()
	b := store.NewBus()
	h := handler.New(s, b, nil, nil, nil, nil, nil, nil, nil, nil)

	r := chi.NewRouter()
	r.Post("/tasks", h.CreateTask)

	body := `{"name":"audit-test-task","description":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectClaims(req, wsA, nil)
	req = withWorkspace(req, wsA)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("CreateTask: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Check audit log has a task.create entry for wsA
	entries := s.ListAudit(wsA, 10)
	found := false
	for _, e := range entries {
		if e.Action == "task.create" && e.WorkspaceID == wsA {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no task.create audit entry found for workspace %s", wsA)
	}
	t.Log("task.create audit entry found: PASS")
}
