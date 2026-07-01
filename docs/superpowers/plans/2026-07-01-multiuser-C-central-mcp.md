# Multi-user C — Central MCP Registry Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A central registry of MCP (Model Context Protocol) servers — register/edit/delete, discover their tools via live `tools/list`, health-ping them in the background, record per-tool call stats (sparklines), and disable individual tools — mirroring the global Skills repository. Ported from `../llmgate`'s `internal/mcp` package, adapted to a single GLOBAL scope (admin-managed central registry; all workspaces' agents can use enabled servers).

**Architecture:** New Postgres tables `mcp_servers` / `mcp_tool_calls` / `mcp_tool_disabled` (global — no team scoping). A Go MCP JSON-RPC-over-HTTP client (`initialize` / `tools/list` / `tools/call`), an MCP store on `*store.Memory` (CRUD + Ping + stats + tool-disable), a background health-ping loop started in `main.go`, chi HTTP handlers (reads open to any authenticated caller; register/edit/delete/disable-tool gated by `platform-admin`), a call-tool proxy endpoint that records stats, and a React "MCP Servers" browser. `auth_value` (bearer token / api key) is encrypted at rest reusing the credentials AES-GCM cipher.

**Tech Stack:** Go 1.26 + chi + pgx + net/http; React 18 + Vite; ports llmgate `backend/internal/mcp/{model,service,client,handler}.go` + `migrations/00003_agents_mcp.sql`, `00065_mcp_tool_calls.sql`, `00068_mcp_tool_disabled.sql`.

## Global Constraints

- All source/comments/UI strings in English. Commits never include AI-tool trailers (local `commit-msg` hook enforces).
- MCP servers are GLOBAL (no `team_id`/`workspace_id` on `mcp_servers`): `UNIQUE(name)`. Any authenticated caller may LIST servers / view tools / view stats. Only `platform-admin` (`auth.IsAdmin` / `authMW.AdminOnly`) may create/update/delete a server or set a tool disabled.
- `auth_value` is stored ENCRYPTED (AES-GCM via the existing credentials cipher / `PLATFORM_ENCRYPTION_KEY`) and MASKED on read (never return the plaintext token to clients).
- MCP client TLS chain verification is configurable per server (`insecure_skip_verify` bool, default false) — self-hosted internal MCP endpoints may use self-signed certs; default secure.
- Live DB at `PLATFORM_DB_URL=postgres://platform:platform@localhost:5432/platform?sslmode=disable`; migrations apply on boot; next index is `012`.
- `store.Memory{ pool *pgxpool.Pool }`. Reuse the existing credentials AES helper for encrypt/decrypt (see `store/credentials.go` — expose or reuse its cipher). Auth helpers `auth.MustUserID`, `auth.IsAdmin`, `auth.AdminOnly`; `RecordAudit` for mutations (`mcp.create`/`mcp.update`/`mcp.delete`/`mcp.tool_disabled`).
- Call stats (`mcp_tool_calls`) carry `workspace_id` (the calling workspace) for later per-workspace rollups, but the registry itself is global.

---

### Task 1: MCP schema migration

**Files:**
- Create: `platform/api/internal/db/migrations/012_mcp.sql`

**Interfaces:** tables `mcp_servers` (global), `mcp_tool_calls`, `mcp_tool_disabled` (adapted from llmgate; `team_id` removed, `UNIQUE(name)`, health columns folded into `mcp_servers`).

- [ ] **Step 1: Write the migration**

```sql
-- 012_mcp.sql — central MCP registry (global; admin-managed).
CREATE TABLE IF NOT EXISTS mcp_servers (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text NOT NULL UNIQUE,
    base_url     text NOT NULL,
    auth_type    text NOT NULL DEFAULT 'none',   -- none | bearer | api_key
    auth_header  text NOT NULL DEFAULT '',        -- e.g. Authorization, X-Api-Key
    auth_value   text NOT NULL DEFAULT '',        -- AES-GCM encrypted
    insecure_skip_verify boolean NOT NULL DEFAULT false,
    enabled      boolean NOT NULL DEFAULT true,
    -- health snapshot (refreshed by the pinger)
    last_ping_at   timestamptz,
    last_status    text NOT NULL DEFAULT 'unknown',  -- unknown|ok|unreachable|auth_failed|protocol_error
    last_error     text NOT NULL DEFAULT '',
    tool_count     int NOT NULL DEFAULT 0,
    server_name    text NOT NULL DEFAULT '',
    server_version text NOT NULL DEFAULT '',
    created_by   text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mcp_tool_calls (
    id          bigserial PRIMARY KEY,
    server_id   uuid NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    workspace_id text NOT NULL DEFAULT '',
    tool_name   text NOT NULL,
    called_at   timestamptz NOT NULL DEFAULT now(),
    duration_ms int NOT NULL DEFAULT 0,
    success     boolean NOT NULL DEFAULT true
);
CREATE INDEX IF NOT EXISTS idx_mcp_tool_calls_server ON mcp_tool_calls(server_id, called_at DESC);

CREATE TABLE IF NOT EXISTS mcp_tool_disabled (
    server_id   uuid NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_name   text NOT NULL,
    disabled_by text NOT NULL DEFAULT '',
    disabled_at timestamptz NOT NULL DEFAULT now(),
    reason      text NOT NULL DEFAULT '',
    PRIMARY KEY (server_id, tool_name)
);
```

- [ ] **Step 2:** Apply via psql; confirm `\d mcp_servers`, `\d mcp_tool_calls`, `\d mcp_tool_disabled`. `go build ./...`.
- [ ] **Step 3:** Commit `Add central MCP registry schema`.

---

### Task 2: MCP JSON-RPC client

**Files:**
- Create: `platform/api/internal/mcp/client.go`
- Create: `platform/api/internal/mcp/model.go` (JSON-RPC + tool types)
- Test: `platform/api/internal/mcp/client_test.go`

**Interfaces (ported from llmgate `mcp/client.go` + `model.go`):**
- Types: `Request/Response/ResponseError` (JSON-RPC 2.0), `InitializeResult{ServerInfo{Name,Version}, Capabilities}`, `Tool{Name, Description string; InputSchema json.RawMessage}`, `ToolsListResult{Tools []Tool}`, `CallToolParams{Name, Arguments}`, `CallToolResult{Content []ContentBlock, IsError bool}`.
- `func NewClient(baseURL, authHeader, authValue string, insecureSkipVerify bool) *Client`
- `func (c *Client) Initialize(ctx) (*InitializeResult, error)` — POST JSON-RPC `initialize` + the `notifications/initialized` handshake.
- `func (c *Client) ListTools(ctx) ([]Tool, error)`
- `func (c *Client) CallTool(ctx, name string, args map[string]any) (*CallToolResult, error)`

- [ ] **Step 1: failing test** — spin up an `httptest.Server` that speaks minimal MCP JSON-RPC (responds to `initialize` with serverInfo + to `tools/list` with one tool + to `tools/call` echoing). Assert `Initialize` returns the server name, `ListTools` returns the tool, `CallTool` returns the content. (No external network.)
- [ ] **Step 2-4:** run-fail → implement client + model (net/http POST, JSON-RPC framing, `tls.Config{InsecureSkipVerify: insecure}` on the transport) → run-pass.
- [ ] **Step 5:** Commit `Add MCP JSON-RPC client`.

---

### Task 3: MCP store + health loop

**Files:**
- Create: `platform/api/internal/store/mcp.go`
- Modify: `platform/api/cmd/api/main.go` (start `RunHealthLoop`)
- Test: `platform/api/internal/store/mcp_test.go`

**Interfaces (methods on `*Memory`; ported from llmgate `mcp/service.go`, global scope):**
- Types: `MCPServer{...}` (mirrors the `mcp_servers` row; `AuthValue` masked on read), `MCPToolEntry{ServerID, ServerName, ToolName, Description string; Disabled bool; Calls int}`, `MCPDailyCount{Date string; Count int}`, `MCPPingResult{Status, Error, ServerName, ServerVersion string; ToolCount int}`.
- `CreateMCPServer(createdBy string, req MCPServerInput) (*MCPServer, error)` — encrypt auth_value.
- `UpdateMCPServer(id string, req MCPServerInput) (*MCPServer, error)` — preserve existing auth_value when the update omits it (llmgate behavior).
- `DeleteMCPServer(id string) error`; `GetMCPServer(id string) (*MCPServer, error)` (decrypted auth for internal use); `ListMCPServers() []*MCPServer` (masked auth).
- `PingMCPServer(id string) (*MCPPingResult, error)` — build a client from the (decrypted) server, `Initialize` + `ListTools`, persist health snapshot (last_status/last_error/tool_count/server_name/version/last_ping_at).
- `ListMCPTools(id string) ([]Tool, error)` — live `tools/list` for one server.
- `ListAllMCPTools() []MCPToolEntry` — catalog across all enabled servers (tool + disabled flag + call count).
- `RecordMCPCall(serverID, workspaceID, toolName string, durationMs int, success bool)` — best-effort insert.
- `GetMCPServerStats(serverID string, days int) []MCPDailyCount` — daily call counts (sparkline).
- `SetMCPToolDisabled(serverID, toolName, disabledBy, reason string, disabled bool) error`; `IsMCPToolDisabled(serverID, toolName string) bool`.
- `RunMCPHealthLoop(ctx, interval)` — background goroutine pinging every enabled server.

- [ ] **Step 1: failing test** (live DB) — `CreateMCPServer` (auth_value round-trips encrypted: `GetMCPServer` decrypts, `ListMCPServers` masks); `SetMCPToolDisabled`+`IsMCPToolDisabled`; `RecordMCPCall`+`GetMCPServerStats` returns a bucket. (Ping/ListTools tested against an httptest MCP server, reusing Task 2's fake or a fresh one.) `t.Cleanup` deletes the server.
- [ ] **Step 2-4:** run-fail → implement (reuse credentials cipher for encrypt/decrypt; `RunMCPHealthLoop` mirrors llmgate's `RunHealthLoop`/`pingAllEnabled`) → run-pass. Wire `go mcpStore.RunMCPHealthLoop(ctx, 60s)` in main.go (configurable `PLATFORM_MCP_PING_TICK`, default 60s).
- [ ] **Step 5:** Commit `Add MCP registry store + background health loop`.

---

### Task 4: MCP HTTP handlers + call proxy

**Files:**
- Create: `platform/api/internal/handler/mcp_handler.go`
- Modify: `platform/api/cmd/api/main.go` (routes)
- Test: `platform/api/internal/handler/mcp_handler_test.go`

**Interfaces:**
- Consumes: Task 3 store, `auth.MustUserID`/`auth.IsAdmin`, `RecordAudit`.
- Routes under `/v1/mcp`:
  - `GET /v1/mcp` → ListMCPServers (masked) (open)
  - `GET /v1/mcp/tools` → ListAllMCPTools catalog (open)
  - `GET /v1/mcp/{id}/tools` → live ListMCPTools (open)
  - `GET /v1/mcp/{id}/stats?days=14` → GetMCPServerStats (open)
  - `POST /v1/mcp/{id}/ping` → PingMCPServer (open — health check)
  - `POST /v1/mcp/{id}/call` → proxy a tool call (body `{tool, arguments}`), records `RecordMCPCall` with the caller's workspace; rejects disabled tools (409/403). (Any authenticated caller may call; the server is global.)
  - `POST /v1/mcp` (ADMIN) create; `PUT /v1/mcp/{id}` (ADMIN) update; `DELETE /v1/mcp/{id}` (ADMIN) delete; `POST /v1/mcp/{id}/tools/{tool}/disabled` (ADMIN, body `{disabled, reason}`) set disable.
- Writes wrapped in `authMW.AdminOnly`; record `RecordAudit`. Never leak decrypted `auth_value` in any response.

- [ ] **Step 1: failing handler test** (dev-mode/injected claims, live DB): admin POST create a server (pointing at an httptest MCP server) → 200; GET /v1/mcp lists it with auth_value MASKED; POST ping → status ok + tool_count; a non-admin POST create → 403; admin disable a tool → `/v1/mcp/{id}/call` on that tool → rejected.
- [ ] **Step 2-4:** run-fail → implement handlers + routes → run-pass.
- [ ] **Step 5:** Commit `Add MCP registry HTTP handlers + call proxy (writes admin-gated)`.

---

### Task 5: Frontend MCP Servers browser

**Files:**
- Create: `platform/web/src/components/McpView.tsx`
- Modify: `platform/web/src/lib/api.ts`, `platform/web/src/components/SettingsOverlay.tsx` (+ `CommandPalette.tsx`)

**Interfaces:**
- Consumes: `/v1/mcp*` endpoints.
- Produces: a browser listing MCP servers with health status (ok/unreachable/…), tool count, and a per-server sparkline from `/stats`; clicking a server shows its live tool catalog with per-tool disable toggles (admin); admins get a register/edit form (name, baseURL, auth type/header/value, insecure-skip-verify) and delete. Follow the existing MemoryView/SkillsView style + SettingsOverlay tab + `⌘K` entry + URL-state.

- [ ] **Step 1:** api.ts client methods + types (server, tool entry, daily count, ping result).
- [ ] **Step 2:** `McpView.tsx` — list (health badge + tool count + sparkline) / detail (tools + disable toggles + register/edit form), write actions gated on `/v1/me` isAdmin.
- [ ] **Step 3:** Mount (SettingsOverlay tab `?overlay=mcp` + palette entry).
- [ ] **Step 4:** `cd platform/web && CI=true pnpm build` clean.
- [ ] **Step 5:** Commit `Frontend: MCP servers browser`.

---

## Self-Review

**Spec coverage** (overarching design §10 C + "MCP centralization like skills"): schema (T1), JSON-RPC client (T2), store + health loop + stats + tool-disable (T3), reads-open/writes-admin API + call proxy + audit (T4), browser (T5). ✓

**Placeholder scan:** T2/T4 compress the run-fail/pass cycle with concrete test descriptions + commit messages; migration SQL (T1) + store signatures (T3) are concrete. The credentials-cipher reuse is flagged "reuse `store/credentials.go`" (confirm the exposed helper at execution).

**Type consistency:** `Tool`/`CallToolResult` (T2 client) consumed by T3 (`PingMCPServer`/`ListMCPTools`) + T4 (call proxy). `MCPServer` (T3) consumed by T4 handlers + T5 UI. `IsMCPToolDisabled` (T3) gates the T4 call proxy. `isAdmin` (A1 `/v1/me`) gates T5 writes; `auth.AdminOnly` gates T4 writes.

## Notes
- MCP servers are global + admin-managed (consistent with global Skills). The existing `platform/tools/mcp-server` (which EXPOSES the control plane as MCP tools) is unrelated to this registry (which CONSUMES external MCP servers) — leave it untouched.
- Deep integration (agent/workflow tool-use loop automatically calling enabled MCP tools) beyond the `/call` proxy endpoint is a follow-on; C delivers the central registry + health + catalog + a working call proxy.
