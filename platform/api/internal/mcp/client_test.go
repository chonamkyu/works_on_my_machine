package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nk/platform/api/internal/mcp"
)

// fakeHandler routes JSON-RPC method calls for the test MCP server.
func fakeHandler(t *testing.T, recordedAuthHeader *string) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Record any custom auth header sent by the client.
		if v := r.Header.Get("X-Test-Auth"); v != "" {
			*recordedAuthHeader = v
		}

		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int64          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Notification (no id) — just ACK.
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
					"serverInfo": map[string]any{
						"name":    "test-mcp",
						"version": "1",
					},
					"capabilities": map[string]any{},
				},
			})
		case "tools/list":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{
						{
							"name":        "echo",
							"description": "echoes the input",
							"inputSchema": map[string]any{"type": "object"},
						},
					},
				},
			})
		case "tools/call":
			json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"content": []map[string]any{
						{"type": "text", "text": "ok"},
					},
					"isError": false,
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestMCPClient(t *testing.T) {
	var recordedAuth string
	srv := httptest.NewServer(fakeHandler(t, &recordedAuth))
	defer srv.Close()

	ctx := context.Background()

	t.Run("initialize returns server name", func(t *testing.T) {
		c := mcp.NewClient(srv.URL, "", "", false)
		result, err := c.Initialize(ctx)
		if err != nil {
			t.Fatalf("Initialize error: %v", err)
		}
		if result.ServerInfo.Name != "test-mcp" {
			t.Errorf("got server name %q, want %q", result.ServerInfo.Name, "test-mcp")
		}
	})

	t.Run("list tools returns echo", func(t *testing.T) {
		c := mcp.NewClient(srv.URL, "", "", false)
		if _, err := c.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		tools, err := c.ListTools(ctx)
		if err != nil {
			t.Fatalf("ListTools error: %v", err)
		}
		if len(tools) != 1 {
			t.Fatalf("got %d tools, want 1", len(tools))
		}
		if tools[0].Name != "echo" {
			t.Errorf("got tool name %q, want %q", tools[0].Name, "echo")
		}
	})

	t.Run("call tool returns content ok", func(t *testing.T) {
		c := mcp.NewClient(srv.URL, "", "", false)
		if _, err := c.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		result, err := c.CallTool(ctx, "echo", map[string]any{"msg": "hello"})
		if err != nil {
			t.Fatalf("CallTool error: %v", err)
		}
		if len(result.Content) == 0 {
			t.Fatal("got empty content")
		}
		if result.Content[0].Text != "ok" {
			t.Errorf("got content text %q, want %q", result.Content[0].Text, "ok")
		}
	})

	t.Run("auth header is forwarded", func(t *testing.T) {
		recordedAuth = "" // reset
		c := mcp.NewClient(srv.URL, "X-Test-Auth", "secret-token", false)
		if _, err := c.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}
		if recordedAuth != "secret-token" {
			t.Errorf("auth header not forwarded: got %q, want %q", recordedAuth, "secret-token")
		}
	})
}
