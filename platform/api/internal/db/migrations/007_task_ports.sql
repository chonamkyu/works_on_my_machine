-- Multi-port forwarding: the agent declares one entry per HTTP surface a
-- service task exposes (UI, API, websocket, etc.). The platform allocates
-- a loopback port per entry and reverse-proxies /d/<task>/<name>/.
ALTER TABLE tasks ADD COLUMN IF NOT EXISTS ports JSONB NOT NULL DEFAULT '[]';
