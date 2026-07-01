package mcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a minimal MCP JSON-RPC-over-HTTP client.
type Client struct {
	baseURL    string
	authHeader string
	authValue  string
	httpClient *http.Client
}

// NewClient creates a new MCP client.
//
// If insecureSkipVerify is true the TLS certificate chain is not verified
// (useful for internal / self-signed endpoints). When false standard TLS
// verification applies.
func NewClient(baseURL, authHeader, authValue string, insecureSkipVerify bool) *Client {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// #nosec G402 — only active when the caller opts in.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: insecureSkipVerify}, //nolint:gosec
	}
	return &Client{
		baseURL:    strings.TrimRight(baseURL, "/"),
		authHeader: authHeader,
		authValue:  authValue,
		httpClient: &http.Client{Timeout: 30 * time.Second, Transport: tr},
	}
}

// call performs a JSON-RPC request and returns the raw result payload.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	reqID := time.Now().UnixNano()
	body, err := json.Marshal(Request{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  params,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.authHeader != "" && c.authValue != "" {
		req.Header.Set(c.authHeader, c.authValue)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("mcp server returned %d: %s", resp.StatusCode, string(b))
	}

	var rpcResp Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("mcp error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return json.Marshal(rpcResp.Result)
}

// notify sends a JSON-RPC notification (no id, no response expected).
// Used for the notifications/initialized handshake required by the MCP spec.
func (c *Client) notify(ctx context.Context, method string, params any) error {
	payload := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		payload["params"] = params
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.authHeader != "" && c.authValue != "" {
		req.Header.Set(c.authHeader, c.authValue)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mcp notify %s returned %d: %s", method, resp.StatusCode, string(b))
	}
	return nil
}

// Initialize performs the MCP initialize handshake and sends
// notifications/initialized as required by the spec.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	type initParams struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct{} `json:"capabilities"`
		ClientInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"clientInfo"`
	}
	raw, err := c.call(ctx, "initialize", initParams{
		ProtocolVersion: "2024-11-05",
		ClientInfo: struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}{Name: "platform", Version: "1.0"},
	})
	if err != nil {
		return nil, err
	}
	var result InitializeResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	// MCP spec: send notifications/initialized before any other request.
	// Errors are non-fatal — some servers don't require it.
	_ = c.notify(ctx, "notifications/initialized", nil)
	return &result, nil
}

// ListTools retrieves the list of tools exposed by the MCP server.
func (c *Client) ListTools(ctx context.Context) ([]Tool, error) {
	raw, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}
	var result ToolsListResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool invokes a named tool with the given arguments.
func (c *Client) CallTool(ctx context.Context, name string, arguments map[string]any) (*CallToolResult, error) {
	raw, err := c.call(ctx, "tools/call", CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return nil, err
	}
	var result CallToolResult
	return &result, json.Unmarshal(raw, &result)
}
