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
