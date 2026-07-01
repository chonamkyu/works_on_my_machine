package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/mcp"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// MCPServer mirrors the mcp_servers row.
// AuthValue is returned DECRYPTED by GetMCPServer (internal use) and
// MASKED ("") by ListMCPServers (client-facing).
type MCPServer struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	BaseURL            string     `json:"baseUrl"`
	AuthType           string     `json:"authType"`
	AuthHeader         string     `json:"authHeader"`
	AuthValue          string     `json:"authValue"` // masked on list; decrypted on get
	InsecureSkipVerify bool       `json:"insecureSkipVerify"`
	Enabled            bool       `json:"enabled"`
	LastPingAt         *string    `json:"lastPingAt,omitempty"`
	LastStatus         string     `json:"lastStatus"`
	LastError          string     `json:"lastError"`
	ToolCount          int        `json:"toolCount"`
	ServerName         string     `json:"serverName"`
	ServerVersion      string     `json:"serverVersion"`
	CreatedBy          string     `json:"createdBy"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

// MCPServerInput is used for create and update operations.
// AuthValue is optional on update — empty means "preserve existing".
// Enabled and InsecureSkipVerify are pointers so that nil means "not
// provided" (preserve current value on update) while an explicit false is
// honoured. Use BoolPtr to construct values.
type MCPServerInput struct {
	Name               string `json:"name"`
	BaseURL            string `json:"baseUrl"`
	AuthType           string `json:"authType"`
	AuthHeader         string `json:"authHeader"`
	AuthValue          string `json:"authValue"` // empty = preserve on update
	InsecureSkipVerify *bool  `json:"insecureSkipVerify"`
	Enabled            *bool  `json:"enabled"`
}

// BoolPtr is a convenience helper for constructing MCPServerInput literals.
func BoolPtr(b bool) *bool { return &b }

// MCPToolEntry is one row in the global tool catalog view.
type MCPToolEntry struct {
	ServerID    string `json:"serverId"`
	ServerName  string `json:"serverName"`
	ToolName    string `json:"toolName"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
	Calls       int    `json:"calls"`
}

// MCPDailyCount is one bucket in the per-server call-count time series.
type MCPDailyCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// MCPPingResult is the outcome of a single health probe.
type MCPPingResult struct {
	Status        string `json:"status"` // ok | unreachable | auth_failed | protocol_error
	Error         string `json:"error,omitempty"`
	ToolCount     int    `json:"toolCount"`
	ServerName    string `json:"serverName,omitempty"`
	ServerVersion string `json:"serverVersion,omitempty"`
}

// ── AES-GCM cipher (same approach as credentials.go) ─────────────────────────

// newMCPCipher builds an AES-GCM cipher from PLATFORM_ENCRYPTION_KEY.
// Duplicates the ~12-line setup from CredentialStore.NewCredentialStore to
// avoid an import cycle between store and its own subpackages; noted per task
// brief.
func newMCPCipher() (cipher.AEAD, error) {
	keyHex := os.Getenv("PLATFORM_ENCRYPTION_KEY")
	if keyHex == "" {
		// Dev-only stable key. Real deployments must set this.
		keyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil || (len(key) != 16 && len(key) != 32) {
		return nil, fmt.Errorf("PLATFORM_ENCRYPTION_KEY must be 32 or 64 hex chars")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func mcpEncrypt(gcm cipher.AEAD, plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return hex.EncodeToString(sealed), nil
}

func mcpDecrypt(gcm cipher.AEAD, encHex string) (string, error) {
	if encHex == "" {
		return "", nil
	}
	data, err := hex.DecodeString(encHex)
	if err != nil {
		return "", err
	}
	if len(data) < gcm.NonceSize() {
		return "", fmt.Errorf("ciphertext too short")
	}
	nonce, body := data[:gcm.NonceSize()], data[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, body, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// ── CRUD ─────────────────────────────────────────────────────────────────────

// CreateMCPServer inserts a new global MCP server row.
// AuthValue is AES-GCM encrypted before storage.
func (m *Memory) CreateMCPServer(createdBy string, in MCPServerInput) (*MCPServer, error) {
	gcm, err := newMCPCipher()
	if err != nil {
		return nil, fmt.Errorf("mcp cipher: %w", err)
	}
	encAuth, err := mcpEncrypt(gcm, in.AuthValue)
	if err != nil {
		return nil, fmt.Errorf("mcp encrypt: %w", err)
	}
	if in.AuthType == "" {
		in.AuthType = "none"
	}
	// Default booleans for create: enabled=true, insecure=false.
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	insecure := false
	if in.InsecureSkipVerify != nil {
		insecure = *in.InsecureSkipVerify
	}
	var sv MCPServer
	var pingAt *string
	err = m.pool.QueryRow(context.Background(), `
		INSERT INTO mcp_servers
		  (name, base_url, auth_type, auth_header, auth_value, insecure_skip_verify, enabled, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING id, name, base_url, auth_type, auth_header, auth_value,
		          insecure_skip_verify, enabled,
		          last_ping_at::text, last_status, last_error, tool_count,
		          server_name, server_version, created_by,
		          created_at, updated_at`,
		in.Name, in.BaseURL, in.AuthType, in.AuthHeader, encAuth,
		insecure, enabled, createdBy,
	).Scan(
		&sv.ID, &sv.Name, &sv.BaseURL, &sv.AuthType, &sv.AuthHeader, &sv.AuthValue,
		&sv.InsecureSkipVerify, &sv.Enabled,
		&pingAt, &sv.LastStatus, &sv.LastError, &sv.ToolCount,
		&sv.ServerName, &sv.ServerVersion, &sv.CreatedBy,
		&sv.CreatedAt, &sv.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	sv.LastPingAt = pingAt
	// Decrypt for internal consistency; but Create returns masked per spec
	// (callers use GetMCPServer for decrypted, ListMCPServers for masked).
	sv.AuthValue = ""
	return &sv, nil
}

// UpdateMCPServer partially updates the server. AuthValue is preserved when
// the input value is empty (UI masked-placeholder behavior from llmgate).
func (m *Memory) UpdateMCPServer(id string, in MCPServerInput) (*MCPServer, error) {
	gcm, err := newMCPCipher()
	if err != nil {
		return nil, fmt.Errorf("mcp cipher: %w", err)
	}

	sets := []string{}
	args := []any{}
	put := func(col string, v any) {
		args = append(args, v)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}

	if in.Name != "" {
		put("name", in.Name)
	}
	if in.BaseURL != "" {
		put("base_url", in.BaseURL)
	}
	if in.AuthType != "" {
		put("auth_type", in.AuthType)
	}
	// AuthHeader: allow explicit empty to clear.
	if in.AuthType != "" {
		put("auth_header", in.AuthHeader)
	}
	if in.AuthValue != "" {
		encAuth, err := mcpEncrypt(gcm, in.AuthValue)
		if err != nil {
			return nil, fmt.Errorf("mcp encrypt: %w", err)
		}
		put("auth_value", encAuth)
	}
	if in.InsecureSkipVerify != nil {
		put("insecure_skip_verify", *in.InsecureSkipVerify)
	}
	if in.Enabled != nil {
		put("enabled", *in.Enabled)
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)

	q := fmt.Sprintf(`UPDATE mcp_servers SET %s WHERE id = $%d`,
		strings.Join(sets, ", "), len(args))
	tag, err := m.pool.Exec(context.Background(), q, args...)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	sv, err := m.GetMCPServer(id)
	if err != nil {
		return nil, err
	}
	sv.AuthValue = "" // mask on return — consistent with CreateMCPServer/ListMCPServers
	return sv, nil
}

// DeleteMCPServer removes the server row and cascades to tool_calls and
// tool_disabled via the FK.
func (m *Memory) DeleteMCPServer(id string) error {
	tag, err := m.pool.Exec(context.Background(),
		`DELETE FROM mcp_servers WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetMCPServer returns the server row with auth_value DECRYPTED.
// For internal callers (Ping, health loop, etc.).
func (m *Memory) GetMCPServer(id string) (*MCPServer, error) {
	gcm, err := newMCPCipher()
	if err != nil {
		return nil, fmt.Errorf("mcp cipher: %w", err)
	}
	var sv MCPServer
	var pingAt *string
	var encAuth string
	err = m.pool.QueryRow(context.Background(), `
		SELECT id, name, base_url, auth_type, auth_header, auth_value,
		       insecure_skip_verify, enabled,
		       last_ping_at::text, last_status, last_error, tool_count,
		       server_name, server_version, created_by, created_at, updated_at
		FROM mcp_servers WHERE id = $1`, id).
		Scan(&sv.ID, &sv.Name, &sv.BaseURL, &sv.AuthType, &sv.AuthHeader, &encAuth,
			&sv.InsecureSkipVerify, &sv.Enabled,
			&pingAt, &sv.LastStatus, &sv.LastError, &sv.ToolCount,
			&sv.ServerName, &sv.ServerVersion, &sv.CreatedBy, &sv.CreatedAt, &sv.UpdatedAt)
	if err != nil {
		return nil, err
	}
	sv.LastPingAt = pingAt
	sv.AuthValue, _ = mcpDecrypt(gcm, encAuth)
	return &sv, nil
}

// ListMCPServers returns all servers with auth_value MASKED.
func (m *Memory) ListMCPServers() []*MCPServer {
	rows, err := m.pool.Query(context.Background(), `
		SELECT id, name, base_url, auth_type, auth_header,
		       insecure_skip_verify, enabled,
		       last_ping_at::text, last_status, last_error, tool_count,
		       server_name, server_version, created_by, created_at, updated_at
		FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*MCPServer
	for rows.Next() {
		var sv MCPServer
		var pingAt *string
		if err := rows.Scan(
			&sv.ID, &sv.Name, &sv.BaseURL, &sv.AuthType, &sv.AuthHeader,
			&sv.InsecureSkipVerify, &sv.Enabled,
			&pingAt, &sv.LastStatus, &sv.LastError, &sv.ToolCount,
			&sv.ServerName, &sv.ServerVersion, &sv.CreatedBy, &sv.CreatedAt, &sv.UpdatedAt,
		); err == nil {
			sv.LastPingAt = pingAt
			sv.AuthValue = "" // masked
			out = append(out, &sv)
		}
	}
	return out
}

// ── Ping ──────────────────────────────────────────────────────────────────────

// PingMCPServer performs a live Initialize+ListTools health check against the
// server, then persists the result as a health snapshot. Returns the outcome.
func (m *Memory) PingMCPServer(id string) (*MCPPingResult, error) {
	sv, err := m.GetMCPServer(id)
	if err != nil {
		return nil, err
	}
	c := mcp.NewClient(sv.BaseURL, sv.AuthHeader, sv.AuthValue, sv.InsecureSkipVerify)
	pingCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := &MCPPingResult{Status: "ok"}
	init, err := c.Initialize(pingCtx)
	if err != nil {
		result.Status = classifyMCPErr(err)
		result.Error = err.Error()
		m.saveMCPPing(id, result)
		return result, nil
	}
	result.ServerName = init.ServerInfo.Name
	result.ServerVersion = init.ServerInfo.Version

	tools, err := c.ListTools(pingCtx)
	if err != nil {
		result.Status = "protocol_error"
		result.Error = err.Error()
		m.saveMCPPing(id, result)
		return result, nil
	}
	result.ToolCount = len(tools)
	m.saveMCPPing(id, result)
	return result, nil
}

func classifyMCPErr(err error) string {
	if err == nil {
		return "ok"
	}
	s := err.Error()
	switch {
	case strings.Contains(s, "401") || strings.Contains(s, "403") || strings.Contains(s, "unauthorized"):
		return "auth_failed"
	case strings.Contains(s, "no such host") || strings.Contains(s, "connection refused") ||
		strings.Contains(s, "timeout") || strings.Contains(s, "deadline"):
		return "unreachable"
	}
	return "protocol_error"
}

func (m *Memory) saveMCPPing(id string, r *MCPPingResult) {
	_, _ = m.pool.Exec(context.Background(), `
		UPDATE mcp_servers
		SET last_ping_at    = NOW(),
		    last_status     = $1,
		    last_error      = $2,
		    tool_count      = $3,
		    server_name     = $4,
		    server_version  = $5
		WHERE id = $6`,
		r.Status, r.Error, r.ToolCount, r.ServerName, r.ServerVersion, id)
}

// ── Tools ─────────────────────────────────────────────────────────────────────

// ListMCPTools performs a live tools/list against a single server.
func (m *Memory) ListMCPTools(id string) ([]mcp.Tool, error) {
	sv, err := m.GetMCPServer(id)
	if err != nil {
		return nil, err
	}
	c := mcp.NewClient(sv.BaseURL, sv.AuthHeader, sv.AuthValue, sv.InsecureSkipVerify)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := c.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("mcp initialize: %w", err)
	}
	return c.ListTools(ctx)
}

// ListAllMCPTools returns the catalog across all enabled servers, annotated
// with the disabled flag and recent call count (last 7 days).
func (m *Memory) ListAllMCPTools() []MCPToolEntry {
	servers := m.ListMCPServers()
	// Build disabled set.
	disabledRows, _ := m.pool.Query(context.Background(),
		`SELECT server_id::text, tool_name FROM mcp_tool_disabled`)
	disabled := map[string]bool{}
	if disabledRows != nil {
		defer disabledRows.Close()
		for disabledRows.Next() {
			var sid, name string
			if err := disabledRows.Scan(&sid, &name); err == nil {
				disabled[sid+"|"+name] = true
			}
		}
	}
	// Build call counts per (server, tool).
	countRows, _ := m.pool.Query(context.Background(), `
		SELECT server_id::text, tool_name, COUNT(*) AS n
		FROM mcp_tool_calls
		WHERE called_at >= NOW() - INTERVAL '7 days'
		GROUP BY server_id, tool_name`)
	callCounts := map[string]int{}
	if countRows != nil {
		defer countRows.Close()
		for countRows.Next() {
			var sid, name string
			var n int
			if err := countRows.Scan(&sid, &name, &n); err == nil {
				callCounts[sid+"|"+name] = n
			}
		}
	}

	var out []MCPToolEntry
	for _, sv := range servers {
		if !sv.Enabled {
			continue
		}
		tools, err := m.ListMCPTools(sv.ID)
		if err != nil {
			continue
		}
		for _, t := range tools {
			key := sv.ID + "|" + t.Name
			out = append(out, MCPToolEntry{
				ServerID:    sv.ID,
				ServerName:  sv.Name,
				ToolName:    t.Name,
				Description: t.Description,
				Disabled:    disabled[key],
				Calls:       callCounts[key],
			})
		}
	}
	return out
}

// ── Call recording & stats ────────────────────────────────────────────────────

// RecordMCPCall logs a single tool invocation. Best-effort — errors are
// swallowed so a logging failure never breaks the parent workflow.
func (m *Memory) RecordMCPCall(serverID, workspaceID, toolName string, durationMs int, success bool) {
	_, _ = m.pool.Exec(context.Background(), `
		INSERT INTO mcp_tool_calls (server_id, workspace_id, tool_name, duration_ms, success)
		VALUES ($1,$2,$3,$4,$5)`,
		serverID, workspaceID, toolName, durationMs, success)
}

// GetMCPServerStats returns daily call counts for the last `days` calendar
// days (sparkline data).
func (m *Memory) GetMCPServerStats(serverID string, days int) []MCPDailyCount {
	if days <= 0 || days > 90 {
		days = 7
	}
	rows, err := m.pool.Query(context.Background(), `
		WITH gen AS (
		  SELECT generate_series(
		    CURRENT_DATE - ($1::int - 1) * INTERVAL '1 day',
		    CURRENT_DATE,
		    INTERVAL '1 day'
		  )::date AS d
		)
		SELECT gen.d::text, COALESCE(COUNT(c.id), 0) AS calls
		FROM gen
		LEFT JOIN mcp_tool_calls c
		  ON c.server_id = $2
		 AND c.called_at::date = gen.d
		GROUP BY gen.d
		ORDER BY gen.d`,
		days, serverID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []MCPDailyCount
	for rows.Next() {
		var d MCPDailyCount
		if err := rows.Scan(&d.Date, &d.Count); err == nil {
			out = append(out, d)
		}
	}
	return out
}

// ── Per-tool disable flag ─────────────────────────────────────────────────────

// SetMCPToolDisabled toggles the disable flag for a tool globally.
func (m *Memory) SetMCPToolDisabled(serverID, toolName, disabledBy, reason string, disabled bool) error {
	if disabled {
		_, err := m.pool.Exec(context.Background(), `
			INSERT INTO mcp_tool_disabled (server_id, tool_name, disabled_by, reason)
			VALUES ($1,$2,$3,$4)
			ON CONFLICT (server_id, tool_name)
			DO UPDATE SET disabled_by = EXCLUDED.disabled_by,
			              reason = EXCLUDED.reason,
			              disabled_at = NOW()`,
			serverID, toolName, disabledBy, reason)
		return err
	}
	_, err := m.pool.Exec(context.Background(),
		`DELETE FROM mcp_tool_disabled WHERE server_id=$1 AND tool_name=$2`,
		serverID, toolName)
	return err
}

// IsMCPToolDisabled is the hot-path check used before calling a tool.
func (m *Memory) IsMCPToolDisabled(serverID, toolName string) bool {
	var n int
	_ = m.pool.QueryRow(context.Background(),
		`SELECT COUNT(*) FROM mcp_tool_disabled WHERE server_id=$1 AND tool_name=$2`,
		serverID, toolName).Scan(&n)
	return n > 0
}

// ── Background health loop ────────────────────────────────────────────────────

// RunMCPHealthLoop pings every enabled server on a ticker. Stops when ctx is
// done. Safe to run as a goroutine — panics are recovered.
func (m *Memory) RunMCPHealthLoop(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.pingAllEnabledMCP(ctx)
		}
	}
}

func (m *Memory) pingAllEnabledMCP(ctx context.Context) {
	rows, err := m.pool.Query(ctx, `SELECT id::text FROM mcp_servers WHERE enabled = true`)
	if err != nil {
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{} // acquire concurrency slot before spawning
		go func(id string) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("mcp health loop panic for %s: %v", id, r)
				}
				<-sem // release slot
				wg.Done()
			}()
			if ctx.Err() != nil {
				return
			}
			if _, err := m.PingMCPServer(id); err != nil {
				log.Printf("mcp health ping %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()
}
