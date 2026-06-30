-- 009_multiuser.sql — multi-user foundation (A1).
-- Existing single-user data is disposable; tenant tables are reset.

CREATE TABLE IF NOT EXISTS users (
    sub                 text PRIMARY KEY,
    email               text NOT NULL DEFAULT '',
    preferred_username  text NOT NULL DEFAULT '',
    display_name        text NOT NULL DEFAULT '',
    is_admin            boolean NOT NULL DEFAULT false,
    disabled            boolean NOT NULL DEFAULT false,
    created_at          timestamptz NOT NULL DEFAULT now(),
    last_seen_at        timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspaces (
    id          text PRIMARY KEY,            -- personal workspace id == user sub
    name        text NOT NULL,
    kind        text NOT NULL DEFAULT 'personal', -- 'personal' | 'shared'
    owner_sub   text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS workspace_members (
    workspace_id text NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id      text NOT NULL,              -- user sub
    role         text NOT NULL DEFAULT 'member', -- 'owner' | 'member' | 'viewer'
    added_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, user_id)
);

CREATE TABLE IF NOT EXISTS audit_log (
    id            bigserial PRIMARY KEY,
    actor_sub     text NOT NULL DEFAULT '',
    actor_email   text NOT NULL DEFAULT '',
    action        text NOT NULL,
    resource_type text NOT NULL DEFAULT '',
    resource_id   text NOT NULL DEFAULT '',
    resource_name text NOT NULL DEFAULT '',
    workspace_id  text NOT NULL DEFAULT '',
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS audit_log_ws_idx  ON audit_log (workspace_id, created_at DESC);
CREATE INDEX IF NOT EXISTS audit_log_act_idx ON audit_log (actor_sub, created_at DESC);

-- Tenant-scoped tables gain workspace_id. Data is disposable: truncate first
-- so the NOT NULL column can be added cleanly, then add the column.
TRUNCATE tasks, runs, memory_entries, sessions, credentials,
         task_index, task_archive, skill_proposals,
         evolution_prs, user_profile, user_profile_suggestions CASCADE;

ALTER TABLE tasks                     ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE runs                      ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE memory_entries            ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE sessions                  ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE credentials               ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE task_index                ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE task_archive              ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE skill_proposals           ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE evolution_prs             ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE user_profile              ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;
ALTER TABLE user_profile_suggestions  ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL;

CREATE INDEX IF NOT EXISTS tasks_ws_idx    ON tasks (workspace_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS sessions_ws_idx ON sessions (workspace_id, last_msg_at DESC);
CREATE INDEX IF NOT EXISTS memory_ws_idx   ON memory_entries (workspace_id);

-- Seed dev workspace + user so PLATFORM_AUTH_DEV=1 works out of the box.
INSERT INTO users (sub, email, preferred_username, display_name, is_admin)
VALUES ('dev', 'dev@example.com', 'dev', 'Dev User', true)
ON CONFLICT (sub) DO NOTHING;
INSERT INTO workspaces (id, name, kind, owner_sub)
VALUES ('dev', 'Dev Personal', 'personal', 'dev')
ON CONFLICT (id) DO NOTHING;
INSERT INTO workspace_members (workspace_id, user_id, role)
VALUES ('dev', 'dev', 'owner')
ON CONFLICT DO NOTHING;
