-- Soft delete: when a task is removed, the supervisor stops it and the live
-- row in `tasks` goes away, but the code + config are preserved here so the
-- user can search, audit, or restore old work.
CREATE TABLE IF NOT EXISTS task_archive (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL DEFAULT 'oneshot',
    triggers      JSONB NOT NULL DEFAULT '[]',
    capabilities  JSONB NOT NULL DEFAULT '[]',
    model_config  JSONB NOT NULL DEFAULT '{}',
    files         JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL,
    deleted_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS task_archive_user_idx ON task_archive (user_id, deleted_at DESC);
