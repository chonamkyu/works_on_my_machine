package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/store"
)

// buildSkillRepoRouter builds a chi router that mirrors the /v1/skill-repo
// routes registered in main.go. When devMode=true the Authenticate middleware
// auto-injects dev-admin claims. Pass devMode=false only when you are
// pre-injecting claims via injectClaims/withWorkspace into the request
// context before calling ServeHTTP (the Authenticate step is skipped so the
// injected context is preserved).
func buildSkillRepoRouter(h *handler.Handler, authMW *auth.Middleware, s *store.Memory, devMode bool) chi.Router {
	r := chi.NewRouter()
	if devMode {
		r.Use(authMW.Authenticate)
	}
	r.Use(h.UpsertUserMiddleware)
	r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(s)(next) })

	// Read routes — open to any authenticated caller.
	r.Get("/v1/skill-repo", h.ListGlobalSkills)
	r.Get("/v1/skill-repo/resolve", h.ResolveGlobalSkill)
	r.Get("/v1/skill-repo/{name}", h.GetGlobalSkill)
	r.Get("/v1/skill-repo/{name}/versions/{n}", h.GetGlobalSkillVersion)

	// Write routes — admin only.
	r.Group(func(r chi.Router) {
		r.Use(authMW.AdminOnly)
		r.Post("/v1/skill-repo", h.CreateGlobalSkill)
		r.Post("/v1/skill-repo/{name}/versions", h.PublishGlobalSkillVersion)
		r.Post("/v1/skill-repo/{name}/aliases", h.MoveGlobalSkillAlias)
		r.Delete("/v1/skill-repo/{name}", h.DeleteGlobalSkill)
	})
	return r
}

// TestSkillRepoAdminCreate: admin POST /v1/skill-repo → 201 with skill + version.
func TestSkillRepoAdminCreate(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	// devMode = true → auto-injects platform-admin role
	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildSkillRepoRouter(h, authMW, s, true)

	t.Cleanup(func() { _ = s.DeleteSkill("h1", "test-cleanup") })

	body := `{"name":"h1","description":"test skill","tags":["test"],"body":"Do H1 things.","comment":"initial"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skill-repo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("admin POST /v1/skill-repo: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["skill"] == nil {
		t.Error("response missing 'skill' field")
	}
	if resp["version"] == nil {
		t.Error("response missing 'version' field")
	}
	t.Log("admin create h1 → 201: PASS")
}

// TestSkillRepoGetSkill: GET /v1/skill-repo/{name} returns skill + versions + aliases.
func TestSkillRepoGetSkill(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildSkillRepoRouter(h, authMW, s, true)

	t.Cleanup(func() { _ = s.DeleteSkill("h1", "test-cleanup") })

	// Create the skill first.
	_, _, err := s.CreateSkill("dev", "h1", "test skill", []string{"test"}, "Do H1 things.", "initial")
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/h1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/skill-repo/h1: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["skill"] == nil {
		t.Error("response missing 'skill'")
	}
	versions, ok := resp["versions"].([]any)
	if !ok {
		t.Fatalf("versions not an array: %T", resp["versions"])
	}
	if len(versions) < 1 {
		t.Error("expected at least 1 version")
	}
	aliases, ok := resp["aliases"].([]any)
	if !ok {
		t.Fatalf("aliases not an array: %T", resp["aliases"])
	}
	if len(aliases) < 1 {
		t.Error("expected at least 1 alias (latest)")
	}
	t.Logf("GET h1 → skill+%d versions+%d aliases: PASS", len(versions), len(aliases))
}

// TestSkillRepoPublishVersion: admin POST /v1/skill-repo/{name}/versions publishes v2.
func TestSkillRepoPublishVersion(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildSkillRepoRouter(h, authMW, s, true)

	t.Cleanup(func() { _ = s.DeleteSkill("h1", "test-cleanup") })

	// Create initial version.
	_, _, err := s.CreateSkill("dev", "h1", "test skill", []string{"test"}, "Do H1 things.", "initial")
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	// Publish v2 via the HTTP handler.
	body := `{"description":"updated","tags":["test","v2"],"body":"Do H1 things better.","comment":"v2"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skill-repo/h1/versions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("admin POST versions: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	// GET the skill back and confirm versions len == 2.
	req2 := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/h1", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("GET after publish: want 200, got %d", rr2.Code)
	}
	var resp map[string]any
	if err := json.Unmarshal(rr2.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	versions := resp["versions"].([]any)
	if len(versions) != 2 {
		t.Errorf("versions after publish: want 2, got %d", len(versions))
	}
	t.Logf("publish v2 → versions len=%d: PASS", len(versions))
}

// TestSkillRepoNonAdminForbidden: a non-admin POST create → 403.
func TestSkillRepoNonAdminForbidden(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	// Use devMode=false + inject claims directly so the Authenticate middleware
	// is skipped (it is already tested elsewhere) and AdminOnly sees the
	// non-admin context.
	authMW := auth.New(false)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	// devMode=false: no Authenticate step; claims injected below.
	r := buildSkillRepoRouter(h, authMW, s, false)

	body := `{"name":"h1","description":"test","tags":[],"body":"body","comment":""}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skill-repo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// Inject non-admin claims directly (no platform-admin role).
	req = injectClaims(req, "regular-user", nil)
	req = withWorkspace(req, "regular-user")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin POST create: want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	t.Log("non-admin POST → 403: PASS")
}

// TestSkillRepoResolve: GET /v1/skill-repo/resolve?ref=h1@1 returns v1 body.
func TestSkillRepoResolve(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildSkillRepoRouter(h, authMW, s, true)

	t.Cleanup(func() { _ = s.DeleteSkill("h1", "test-cleanup") })

	// Create h1 v1.
	_, v1, err := s.CreateSkill("dev", "h1", "test skill", []string{"test"}, "Do H1 things.", "initial")
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if v1.VersionNo != 1 {
		t.Fatalf("expected v1.VersionNo=1, got %d", v1.VersionNo)
	}

	// Publish v2 so @latest moves away from v1.
	_, err = s.PublishSkillVersion("h1", "dev", "v2 desc", []string{"test"}, "Do H1 things v2.", "v2")
	if err != nil {
		t.Fatalf("PublishSkillVersion: %v", err)
	}

	// Resolve h1@1 — should return version 1's body.
	req := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/resolve?ref=h1%401", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("resolve h1@1: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var ver store.SkillVersion
	if err := json.Unmarshal(rr.Body.Bytes(), &ver); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if ver.VersionNo != 1 {
		t.Errorf("resolve h1@1: want versionNo=1, got %d", ver.VersionNo)
	}
	if !strings.Contains(ver.Body, "Do H1 things") {
		t.Errorf("resolve h1@1: unexpected body %q", ver.Body)
	}
	t.Logf("resolve h1@1 → versionNo=%d body=%q: PASS", ver.VersionNo, ver.Body)
}

// TestSkillRepoResolveNotShadowedByName verifies that the static /resolve path
// is not captured by the /{name} wildcard — i.e., chi routes it correctly.
func TestSkillRepoResolveNotShadowedByName(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildSkillRepoRouter(h, authMW, s, true)

	// No "resolve" skill exists — a bad route would return 404 for "skill not found".
	// The resolve handler should return 400 (missing ref param), not 404 with
	// "skill not found: resolve" — that would mean /{name} captured the path.
	req := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/resolve", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	// Resolve handler returns 400 when ?ref is missing.
	if rr.Code != http.StatusBadRequest {
		t.Errorf("resolve routing: want 400 (missing ref), got %d body=%s", rr.Code, rr.Body.String())
	}
	// Confirm the body is about a missing ref, not "skill not found: resolve".
	body := rr.Body.String()
	if strings.Contains(body, "skill not found: resolve") {
		t.Errorf("resolve path was shadowed by /{name} route; body=%s", body)
	}
	t.Logf("resolve not shadowed: got %d %s: PASS", rr.Code, body)
}

// TestSkillRepoFullFlow: combined end-to-end test that exercises create →
// read-back → publish → resolve in one pass, cleaning up "h1" on exit.
func TestSkillRepoFullFlow(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildSkillRepoRouter(h, authMW, s, true)

	t.Cleanup(func() { _ = s.DeleteSkill("h1", "test-cleanup") })

	// 1. Admin create h1.
	createBody := `{"name":"h1","description":"full-flow skill","tags":["ft"],"body":"Step 1.","comment":"create"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/skill-repo", strings.NewReader(createBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: want 201, got %d body=%s", rr.Code, rr.Body.String())
	}

	// 2. GET h1 → skill + versions (1) + aliases (latest→1).
	req2 := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/h1", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("get after create: want 200, got %d", rr2.Code)
	}
	var getResp map[string]any
	_ = json.Unmarshal(rr2.Body.Bytes(), &getResp)
	versions := getResp["versions"].([]any)
	if len(versions) != 1 {
		t.Errorf("versions after create: want 1, got %d", len(versions))
	}

	// 3. Admin publish v2.
	pubBody := `{"description":"v2","tags":["ft","v2"],"body":"Step 2.","comment":"publish"}`
	req3 := httptest.NewRequest(http.MethodPost, "/v1/skill-repo/h1/versions", strings.NewReader(pubBody))
	req3.Header.Set("Content-Type", "application/json")
	rr3 := httptest.NewRecorder()
	r.ServeHTTP(rr3, req3)
	if rr3.Code != http.StatusCreated {
		t.Fatalf("publish: want 201, got %d body=%s", rr3.Code, rr3.Body.String())
	}

	// 4. GET h1 → versions len == 2.
	req4 := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/h1", nil)
	rr4 := httptest.NewRecorder()
	r.ServeHTTP(rr4, req4)
	_ = json.Unmarshal(rr4.Body.Bytes(), &getResp)
	versions = getResp["versions"].([]any)
	if len(versions) != 2 {
		t.Errorf("versions after publish: want 2, got %d", len(versions))
	}

	// 5. Resolve h1@1 → returns v1.
	req5 := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/resolve?ref=h1%401", nil)
	rr5 := httptest.NewRecorder()
	r.ServeHTTP(rr5, req5)
	if rr5.Code != http.StatusOK {
		t.Fatalf("resolve h1@1: want 200, got %d body=%s", rr5.Code, rr5.Body.String())
	}
	var verResp store.SkillVersion
	_ = json.Unmarshal(rr5.Body.Bytes(), &verResp)
	if verResp.VersionNo != 1 {
		t.Errorf("resolve h1@1: want versionNo=1, got %d", verResp.VersionNo)
	}

	// 6. GET /v1/skill-repo/{name}/versions/1 directly.
	req6 := httptest.NewRequest(http.MethodGet, "/v1/skill-repo/h1/versions/1", nil)
	rr6 := httptest.NewRecorder()
	r.ServeHTTP(rr6, req6)
	if rr6.Code != http.StatusOK {
		t.Fatalf("get version: want 200, got %d body=%s", rr6.Code, rr6.Body.String())
	}

	// 7. Non-admin publish attempt → 403.
	authMWNonAdmin := auth.New(false)
	rNonAdmin := buildSkillRepoRouter(h, authMWNonAdmin, s, false)
	req7 := httptest.NewRequest(http.MethodPost, "/v1/skill-repo/h1/versions", strings.NewReader(pubBody))
	req7.Header.Set("Content-Type", "application/json")
	req7 = injectClaims(req7, "regular-user", nil)
	req7 = withWorkspace(req7, "regular-user")
	rr7 := httptest.NewRecorder()
	rNonAdmin.ServeHTTP(rr7, req7)
	if rr7.Code != http.StatusForbidden {
		t.Errorf("non-admin publish: want 403, got %d", rr7.Code)
	}

	t.Log(fmt.Sprintf("full flow: create→get→publish→versions=%d→resolve→403: PASS", len(versions)))
}
