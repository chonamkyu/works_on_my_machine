-- Long-running workflows: `service` kind. The runner keeps these alive in
-- the background and restarts them on crash. Existing rows default to
-- "oneshot" to preserve current behaviour.

ALTER TABLE tasks ADD COLUMN IF NOT EXISTS kind TEXT NOT NULL DEFAULT 'oneshot';

CREATE INDEX IF NOT EXISTS tasks_kind_idx ON tasks (user_id, kind) WHERE kind = 'service';
