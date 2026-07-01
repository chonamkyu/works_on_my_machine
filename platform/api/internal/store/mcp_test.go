package store_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nk/platform/api/internal/store"
)

// fakeMCPHandler is a minimal JSON-RPC MCP server for store tests.
func fakeMCPHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			JSONRPC string  `json:"jsonrpc"`
			ID      *int64  `json:"id"`
			Method  string  `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// notification: no id
		if req.ID == nil {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"serverInfo":      map[string]any{"name": "test-store-mcp", "version": "2"},
					"capabilities":    map[string]any{},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "greet",
							"description": "says hello",
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("PLATFORM_DB_URL")
	if dsn == "" {
		t.Skip("PLATFORM_DB_URL not set; skipping live-DB test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

func TestMCP_CRUDPingStatsDisable(t *testing.T) {
	pool := newTestPool(t)
	mem := store.NewMemory(pool)

	// Start a fake MCP server backed by httptest.
	fakeSrv := httptest.NewServer(fakeMCPHandler(t))
	defer fakeSrv.Close()

	// ── 1. Create ─────────────────────────────────────────────────────────────
	srv, err := mem.CreateMCPServer("test-user", store.MCPServerInput{
		Name:       "store-test-" + time.Now().Format("150405"),
		BaseURL:    fakeSrv.URL,
		AuthType:   "bearer",
		AuthHeader: "Authorization",
		AuthValue:  "secret-token",
		Enabled:    store.BoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	if srv.ID == "" {
		t.Fatal("CreateMCPServer: got empty ID")
	}
	t.Cleanup(func() { _ = mem.DeleteMCPServer(srv.ID) })

	// ── 2. GetMCPServer: decrypted auth for internal callers ───────────────
	got, err := mem.GetMCPServer(srv.ID)
	if err != nil {
		t.Fatalf("GetMCPServer: %v", err)
	}
	if got.AuthValue != "secret-token" {
		t.Errorf("GetMCPServer: want AuthValue=%q, got %q", "secret-token", got.AuthValue)
	}

	// ── 3. ListMCPServers: auth MASKED ─────────────────────────────────────
	list := mem.ListMCPServers()
	var found *store.MCPServer
	for _, s := range list {
		if s.ID == srv.ID {
			found = s
			break
		}
	}
	if found == nil {
		t.Fatal("ListMCPServers: server not found in list")
	}
	if found.AuthValue != "" && found.AuthValue != "***" {
		t.Errorf("ListMCPServers: auth_value should be masked, got %q", found.AuthValue)
	}

	// ── 4. PingMCPServer ────────────────────────────────────────────────────
	ping, err := mem.PingMCPServer(srv.ID)
	if err != nil {
		t.Fatalf("PingMCPServer: %v", err)
	}
	if ping.Status != "ok" {
		t.Errorf("PingMCPServer: want status=ok, got %q (error: %s)", ping.Status, ping.Error)
	}
	if ping.ToolCount < 1 {
		t.Errorf("PingMCPServer: want ToolCount>=1, got %d", ping.ToolCount)
	}
	if ping.ServerName != "test-store-mcp" {
		t.Errorf("PingMCPServer: want ServerName=%q, got %q", "test-store-mcp", ping.ServerName)
	}
	// Verify health snapshot persisted to DB.
	refreshed, _ := mem.GetMCPServer(srv.ID)
	if refreshed.LastStatus != "ok" {
		t.Errorf("PingMCPServer: health snapshot not persisted; LastStatus=%q", refreshed.LastStatus)
	}
	if refreshed.ServerName != "test-store-mcp" {
		t.Errorf("PingMCPServer: server_name not persisted; got %q", refreshed.ServerName)
	}

	// ── 5. SetMCPToolDisabled + IsMCPToolDisabled ──────────────────────────
	if err := mem.SetMCPToolDisabled(srv.ID, "greet", "test-user", "testing", true); err != nil {
		t.Fatalf("SetMCPToolDisabled: %v", err)
	}
	if !mem.IsMCPToolDisabled(srv.ID, "greet") {
		t.Error("IsMCPToolDisabled: want true after disable")
	}
	// Re-enable
	if err := mem.SetMCPToolDisabled(srv.ID, "greet", "test-user", "", false); err != nil {
		t.Fatalf("SetMCPToolDisabled(false): %v", err)
	}
	if mem.IsMCPToolDisabled(srv.ID, "greet") {
		t.Error("IsMCPToolDisabled: want false after re-enable")
	}

	// ── 6. RecordMCPCall + GetMCPServerStats ──────────────────────────────
	mem.RecordMCPCall(srv.ID, "ws-1", "greet", 42, true)
	mem.RecordMCPCall(srv.ID, "ws-1", "greet", 55, false)

	stats := mem.GetMCPServerStats(srv.ID, 7)
	if len(stats) == 0 {
		t.Fatal("GetMCPServerStats: expected at least one bucket")
	}
	var total int
	for _, d := range stats {
		total += d.Count
	}
	if total < 2 {
		t.Errorf("GetMCPServerStats: expected total>=2, got %d", total)
	}

	// ── 7. UpdateMCPServer with empty auth_value preserves old one ─────────
	updated, err := mem.UpdateMCPServer(srv.ID, store.MCPServerInput{
		Name:    srv.Name + "-upd",
		BaseURL: srv.BaseURL,
		// AuthValue intentionally empty → should preserve "secret-token"
		// Enabled and InsecureSkipVerify are nil → must NOT be overwritten
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	if updated.Name != srv.Name+"-upd" {
		t.Errorf("UpdateMCPServer: name not updated, got %q", updated.Name)
	}
	// UpdateMCPServer must return masked (empty) AuthValue.
	if updated.AuthValue != "" {
		t.Errorf("UpdateMCPServer: AuthValue must be masked, got %q", updated.AuthValue)
	}
	// Fetch fresh and confirm auth preserved in DB.
	after, _ := mem.GetMCPServer(srv.ID)
	if after.AuthValue != "secret-token" {
		t.Errorf("UpdateMCPServer: auth_value not preserved; got %q", after.AuthValue)
	}
}

// TestMCP_UpdateMasksAuthValue asserts that UpdateMCPServer returns an empty
// AuthValue even when an auth secret is stored, consistent with Create/List.
func TestMCP_UpdateMasksAuthValue(t *testing.T) {
	pool := newTestPool(t)
	mem := store.NewMemory(pool)

	fakeSrv := httptest.NewServer(fakeMCPHandler(t))
	defer fakeSrv.Close()

	srv, err := mem.CreateMCPServer("test-user", store.MCPServerInput{
		Name:      "auth-mask-test-" + time.Now().Format("150405.000"),
		BaseURL:   fakeSrv.URL,
		AuthType:  "bearer",
		AuthValue: "super-secret",
	})
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	t.Cleanup(func() { _ = mem.DeleteMCPServer(srv.ID) })

	// Update name only; AuthValue empty → preserved in DB.
	updated, err := mem.UpdateMCPServer(srv.ID, store.MCPServerInput{
		Name: srv.Name + "-v2",
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer: %v", err)
	}
	if updated.AuthValue != "" {
		t.Errorf("UpdateMCPServer: AuthValue must be masked (empty), got %q", updated.AuthValue)
	}

	// GetMCPServer must still decrypt and return the real secret.
	got, err := mem.GetMCPServer(srv.ID)
	if err != nil {
		t.Fatalf("GetMCPServer: %v", err)
	}
	if got.AuthValue != "super-secret" {
		t.Errorf("GetMCPServer: expected decrypted AuthValue=%q, got %q", "super-secret", got.AuthValue)
	}
}

// TestMCP_UpdatePreservesFlags asserts that a name-only update (nil Enabled,
// nil InsecureSkipVerify) does not flip either flag to false.
func TestMCP_UpdatePreservesFlags(t *testing.T) {
	pool := newTestPool(t)
	mem := store.NewMemory(pool)

	fakeSrv := httptest.NewServer(fakeMCPHandler(t))
	defer fakeSrv.Close()

	// Create server with enabled=true (default via nil — let default apply).
	srv, err := mem.CreateMCPServer("test-user", store.MCPServerInput{
		Name:    "flags-test-" + time.Now().Format("150405.000"),
		BaseURL: fakeSrv.URL,
	})
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	t.Cleanup(func() { _ = mem.DeleteMCPServer(srv.ID) })

	// Confirm defaults: enabled=true, insecure=false.
	got, err := mem.GetMCPServer(srv.ID)
	if err != nil {
		t.Fatalf("GetMCPServer: %v", err)
	}
	if !got.Enabled {
		t.Fatal("CreateMCPServer: expected Enabled=true by default")
	}

	// Name-only update — booleans are nil pointers (not provided).
	_, err = mem.UpdateMCPServer(srv.ID, store.MCPServerInput{
		Name: srv.Name + "-renamed",
	})
	if err != nil {
		t.Fatalf("UpdateMCPServer (name-only): %v", err)
	}

	// Enabled must still be true after the rename.
	after, err := mem.GetMCPServer(srv.ID)
	if err != nil {
		t.Fatalf("GetMCPServer after update: %v", err)
	}
	if !after.Enabled {
		t.Error("UpdateMCPServer: name-only update silently disabled the server (pointer bool fix failed)")
	}
	if after.InsecureSkipVerify {
		t.Error("UpdateMCPServer: name-only update unexpectedly set InsecureSkipVerify=true")
	}
}

// TestMCP_HealthLoopNoDeadlock guards against the semaphore-drain deadlock
// when fewer than cap(sem)=4 servers are enabled. It runs one health-loop
// tick via a short-lived context (1 s) and asserts RunMCPHealthLoop returns.
func TestMCP_HealthLoopNoDeadlock(t *testing.T) {
	pool := newTestPool(t)
	mem := store.NewMemory(pool)

	fakeSrv := httptest.NewServer(fakeMCPHandler(t))
	defer fakeSrv.Close()

	// Create exactly 1 enabled server — well under the sem cap of 4.
	srv, err := mem.CreateMCPServer("test-user", store.MCPServerInput{
		Name:    "health-loop-test-" + time.Now().Format("150405.000"),
		BaseURL: fakeSrv.URL,
		Enabled: store.BoolPtr(true),
	})
	if err != nil {
		t.Fatalf("CreateMCPServer: %v", err)
	}
	t.Cleanup(func() { _ = mem.DeleteMCPServer(srv.ID) })

	// Cancel context after 1 s — the loop must return well before 5 s.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		mem.RunMCPHealthLoop(ctx, 200*time.Millisecond)
		close(done)
	}()

	select {
	case <-done:
		// Good — loop exited when context was cancelled.
	case <-time.After(5 * time.Second):
		t.Fatal("RunMCPHealthLoop did not return within 5 s (semaphore deadlock?)")
	}
}
