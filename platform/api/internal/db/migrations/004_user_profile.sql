-- Phase 2 self-tuning: stable, hand-editable user profile that the agent
-- always honors. Distinct from memory_entries (which are noisy and many);
-- profile is the small set of facts that change rarely.
CREATE TABLE IF NOT EXISTS user_profile (
    user_id            TEXT PRIMARY KEY,
    preferred_language TEXT NOT NULL DEFAULT '',
    reply_tone         TEXT NOT NULL DEFAULT '',
    expertise_level    TEXT NOT NULL DEFAULT '',
    hard_rules         JSONB NOT NULL DEFAULT '[]',
    default_repos      JSONB NOT NULL DEFAULT '[]',
    default_channels   JSONB NOT NULL DEFAULT '[]',
    default_models     JSONB NOT NULL DEFAULT '{}',
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Reflection-proposed profile changes the user has not yet accepted. The UI
-- shows them as pending suggestions in Settings -> Profile; accepting copies
-- the value to user_profile and marks the row resolved.
CREATE TABLE IF NOT EXISTS user_profile_suggestions (
    id          TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL,
    field       TEXT NOT NULL,
    value       TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT 'chat-reflect',
    session_id  TEXT,
    status      TEXT NOT NULL DEFAULT 'pending',   -- pending | accepted | rejected
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS profile_sugg_user_idx
    ON user_profile_suggestions (user_id, status, created_at DESC);

-- Phase 2 memory utility tracking: which memories are actually useful?
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS last_read_at TIMESTAMPTZ;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS read_count   INTEGER NOT NULL DEFAULT 0;
ALTER TABLE memory_entries ADD COLUMN IF NOT EXISTS helpful_count INTEGER NOT NULL DEFAULT 0;
