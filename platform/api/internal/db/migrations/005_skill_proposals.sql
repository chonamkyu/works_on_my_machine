-- Phase 3/4: skill proposals the user can review and accept. Accepted ones
-- land in prompts/users/<userId>/skills/ on disk; rejected ones stay here as
-- training data for future proposals.
CREATE TABLE IF NOT EXISTS skill_proposals (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL,
    skill_name   TEXT NOT NULL,
    skill_body   TEXT NOT NULL,
    rationale    TEXT NOT NULL DEFAULT '',
    confidence   TEXT NOT NULL DEFAULT 'medium',
    applies_when TEXT NOT NULL DEFAULT 'always',
    status       TEXT NOT NULL DEFAULT 'pending',  -- pending | accepted | rejected
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS skill_prop_user_idx
    ON skill_proposals (user_id, status, created_at DESC);
