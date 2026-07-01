package handler_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/store"
)

// fakeMCPServer returns an httptest.Server that mimics a minimal MCP server.
// It handles initialize, tools/list, tools/call, and notifications/initialized.
func fakeMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Method string `json:"method"`
			ID     any    `json:"id"`
		}
		_ = json.Unmarshal(body, &req)

		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","serverInfo":{"name":"test-mcp","version":"1.0"},"capabilities":{}}}`))
		case "tools/list":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[{"name":"echo","description":"echoes input","inputSchema":{}}]}}`))
		case "tools/call":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello"}],"isError":false}}`))
		case "notifications/initialized":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"result":null}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildMCPRouter builds a chi router that mirrors the /v1/mcp routes from main.go.
// When devMode=true the Authenticate middleware auto-injects platform-admin claims.
// Pass devMode=false when you are pre-injecting claims via injectClaims/withWorkspace.
func buildMCPRouter(h *handler.Handler, authMW *auth.Middleware, s *store.Memory, devMode bool) chi.Router {
	r := chi.NewRouter()
	if devMode {
		r.Use(authMW.Authenticate)
	}
	r.Use(h.UpsertUserMiddleware)
	r.Use(func(next http.Handler) http.Handler { return authMW.Workspace(s)(next) })

	// Read routes — open to any authenticated caller.
	r.Get("/v1/mcp", h.ListMCPServers)
	r.Get("/v1/mcp/tools", h.ListAllMCPTools)
	r.Get("/v1/mcp/{id}/tools", h.ListMCPTools)
	r.Get("/v1/mcp/{id}/stats", h.GetMCPServerStats)
	r.Post("/v1/mcp/{id}/ping", h.PingMCPServer)
	r.Post("/v1/mcp/{id}/call", h.CallMCPTool)

	// Admin-only write routes.
	r.Group(func(r chi.Router) {
		r.Use(authMW.AdminOnly)
		r.Post("/v1/mcp", h.CreateMCPServer)
		r.Put("/v1/mcp/{id}", h.UpdateMCPServer)
		r.Delete("/v1/mcp/{id}", h.DeleteMCPServer)
		r.Post("/v1/mcp/{id}/tools/{tool}/disabled", h.SetMCPToolDisabled)
	})
	return r
}

// TestMCPAdminCreate: admin POST /v1/mcp → 200, response has id field, authValue is masked.
func TestMCPAdminCreate(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fakeSrv := fakeMCPServer(t)

	authMW := auth.New(true) // dev mode → platform-admin claims
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildMCPRouter(h, authMW, s, true)

	body := `{"name":"test-mcp-create","baseUrl":"` + fakeSrv.URL + `","authType":"none"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("admin POST /v1/mcp: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	serverID, ok := resp["id"].(string)
	if !ok || serverID == "" {
		t.Fatalf("response missing or empty 'id' field: %v", resp)
	}
	if v, _ := resp["authValue"].(string); v != "" {
		t.Errorf("authValue must be masked (empty), got %q", v)
	}
	t.Cleanup(func() { _ = s.DeleteMCPServer(serverID) })
	t.Logf("admin create MCP server %s: PASS", serverID)
}

// TestMCPListServersAuthMasked: after create, GET /v1/mcp lists it and auth_value is masked.
func TestMCPListServersAuthMasked(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fakeSrv := fakeMCPServer(t)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildMCPRouter(h, authMW, s, true)

	// Create a server with an auth value.
	in := store.MCPServerInput{
		Name:      "test-mcp-list-" + randSuffix(),
		BaseURL:   fakeSrv.URL,
		AuthType:  "header",
		AuthHeader: "Authorization",
		AuthValue: "Bearer secret-token",
		Enabled:   store.BoolPtr(true),
	}
	sv, err := s.CreateMCPServer("dev", in)
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	serverID := sv.ID
	t.Cleanup(func() { _ = s.DeleteMCPServer(serverID) })

	req := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /v1/mcp: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Servers []map[string]any `json:"servers"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	found := false
	for _, srv := range resp.Servers {
		if sid, _ := srv["id"].(string); sid == serverID {
			found = true
			if v, _ := srv["authValue"].(string); v != "" {
				t.Errorf("authValue must be masked in list, got %q", v)
			}
			break
		}
	}
	if !found {
		t.Errorf("created server %s not found in list", serverID)
	}
	t.Logf("list servers: auth masked: PASS")
}

// TestMCPPing: admin creates server, POST /v1/mcp/{id}/ping → 200, status ok, toolCount 1.
func TestMCPPing(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fakeSrv := fakeMCPServer(t)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildMCPRouter(h, authMW, s, true)

	// Create server.
	in := store.MCPServerInput{
		Name:    "test-mcp-ping-" + randSuffix(),
		BaseURL: fakeSrv.URL,
		Enabled: store.BoolPtr(true),
	}
	sv, err := s.CreateMCPServer("dev", in)
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	serverID := sv.ID
	t.Cleanup(func() { _ = s.DeleteMCPServer(serverID) })

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+serverID+"/ping", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /v1/mcp/%s/ping: want 200, got %d body=%s", serverID, rr.Code, rr.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode ping response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("ping status: want ok, got %v (full resp: %v)", resp["status"], resp)
	}
	tc, _ := resp["toolCount"].(float64)
	if int(tc) != 1 {
		t.Errorf("ping toolCount: want 1, got %v", resp["toolCount"])
	}
	t.Logf("ping → status=%v toolCount=%v: PASS", resp["status"], resp["toolCount"])
}

// TestMCPNonAdminCreate: non-admin POST /v1/mcp → 403.
func TestMCPNonAdminCreate(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fakeSrv := fakeMCPServer(t)

	// devMode=false so we can inject non-admin claims.
	authMW := auth.New(false)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildMCPRouter(h, authMW, s, false)

	wsID := "ws-mcp-nonadmin-" + randSuffix()
	body := `{"name":"should-fail","baseUrl":"` + fakeSrv.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = injectClaims(req, wsID, nil) // no platform-admin role
	req = withWorkspace(req, wsID)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("non-admin POST /v1/mcp: want 403, got %d body=%s", rr.Code, rr.Body.String())
	}
	t.Logf("non-admin create → 403: PASS")
}

// TestMCPDisabledToolRejected: admin create server → admin disable "echo" → call → 403.
func TestMCPDisabledToolRejected(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fakeSrv := fakeMCPServer(t)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildMCPRouter(h, authMW, s, true)

	in := store.MCPServerInput{
		Name:    "test-mcp-disabled-" + randSuffix(),
		BaseURL: fakeSrv.URL,
		Enabled: store.BoolPtr(true),
	}
	sv, err := s.CreateMCPServer("dev", in)
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	serverID := sv.ID
	t.Cleanup(func() { _ = s.DeleteMCPServer(serverID) })

	// Disable the "echo" tool.
	disableBody := `{"disabled":true,"reason":"test disable"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+serverID+"/tools/echo/disabled", strings.NewReader(disableBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("disable echo: want 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Now call the disabled tool — should get 403.
	callBody := `{"tool":"echo","arguments":{}}`
	req2 := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+serverID+"/call", strings.NewReader(callBody))
	req2.Header.Set("Content-Type", "application/json")
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusForbidden {
		t.Errorf("call disabled tool: want 403, got %d body=%s", rr2.Code, rr2.Body.String())
	}
	var errResp map[string]any
	_ = json.Unmarshal(rr2.Body.Bytes(), &errResp)
	if errResp["error"] != "tool disabled" {
		t.Errorf("expected error=tool disabled, got %v", errResp["error"])
	}
	t.Logf("disabled tool call → 403 tool disabled: PASS")
}

// TestMCPEnabledToolCallRecorded: admin create server → call echo → 200, verify stats row.
func TestMCPEnabledToolCallRecorded(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fakeSrv := fakeMCPServer(t)

	authMW := auth.New(true)
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	r := buildMCPRouter(h, authMW, s, true)

	in := store.MCPServerInput{
		Name:    "test-mcp-call-" + randSuffix(),
		BaseURL: fakeSrv.URL,
		Enabled: store.BoolPtr(true),
	}
	sv, err := s.CreateMCPServer("dev", in)
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	serverID := sv.ID
	t.Cleanup(func() { _ = s.DeleteMCPServer(serverID) })

	// Call the echo tool.
	callBody := `{"tool":"echo","arguments":{}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp/"+serverID+"/call", strings.NewReader(callBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /v1/mcp/%s/call: want 200, got %d body=%s", serverID, rr.Code, rr.Body.String())
	}
	var callResp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &callResp); err != nil {
		t.Fatalf("decode call response: %v", err)
	}
	if callResp["content"] == nil {
		t.Errorf("call response missing content field: %v", callResp)
	}

	// Give the async RecordMCPCall goroutine a moment to finish.
	time.Sleep(100 * time.Millisecond)

	// Verify via GetMCPServerStats that there's at least one call recorded today.
	stats := s.GetMCPServerStats(serverID, 1)
	if len(stats) == 0 {
		t.Fatal("GetMCPServerStats: expected at least one bucket")
	}
	totalCalls := 0
	for _, d := range stats {
		totalCalls += d.Count
	}
	if totalCalls == 0 {
		t.Errorf("expected at least 1 call recorded in stats, got 0; stats=%v", stats)
	}
	t.Logf("call echo → 200, stats totalCalls=%d: PASS", totalCalls)
}
