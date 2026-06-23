-- Schema for the platform control plane.
-- pgvector enables semantic search across memory and the task corpus.

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- tasks ----------------------------------------------------------------

CREATE TABLE IF NOT EXISTS tasks (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'idle',
    triggers      JSONB NOT NULL DEFAULT '[]',
    capabilities  JSONB NOT NULL DEFAULT '[]',
    model_config  JSONB NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS tasks_user_idx ON tasks (user_id, created_at DESC);

-- runs -----------------------------------------------------------------

CREATE TABLE IF NOT EXISTS runs (
    id           TEXT PRIMARY KEY,
    task_id      TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL,
    state        TEXT NOT NULL DEFAULT 'queued',
    trigger      TEXT NOT NULL DEFAULT 'manual',
    started_at   TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ,
    duration_ms  INTEGER NOT NULL DEFAULT 0,
    output       JSONB,
    error        TEXT
);

CREATE INDEX IF NOT EXISTS runs_task_started_idx ON runs (task_id, started_at DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS runs_state_idx ON runs (state);

-- run logs -------------------------------------------------------------

CREATE TABLE IF NOT EXISTS run_logs (
    id      BIGSERIAL PRIMARY KEY,
    run_id  TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    ts      TIMESTAMPTZ NOT NULL DEFAULT now(),
    stream  TEXT NOT NULL,
    text    TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS run_logs_run_idx ON run_logs (run_id, id);

-- credentials ----------------------------------------------------------

CREATE TABLE IF NOT EXISTS credentials (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL,
    connector     TEXT NOT NULL,
    label         TEXT NOT NULL,
    encrypted     BYTEA NOT NULL,
    scopes        TEXT[] NOT NULL DEFAULT '{}',
    status        TEXT NOT NULL DEFAULT 'active',
    last_used_at  TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, connector, label)
);

-- memory ---------------------------------------------------------------

CREATE TABLE IF NOT EXISTS memory_entries (
    id             TEXT PRIMARY KEY,
    user_id        TEXT NOT NULL,
    scope          TEXT NOT NULL CHECK (scope IN ('user', 'task')),
    scope_key      TEXT NOT NULL DEFAULT '',
    topic          TEXT NOT NULL,
    content        TEXT NOT NULL,
    embedding      vector(1024),
    pinned         BOOLEAN NOT NULL DEFAULT FALSE,
    source_run_id  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS memory_user_idx ON memory_entries (user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS memory_scope_idx ON memory_entries (user_id, scope, scope_key);
CREATE INDEX IF NOT EXISTS memory_content_trgm ON memory_entries USING gin (content gin_trgm_ops);
CREATE INDEX IF NOT EXISTS memory_embedding_idx ON memory_entries USING hnsw (embedding vector_cosine_ops);

-- evolution PRs --------------------------------------------------------

CREATE TABLE IF NOT EXISTS evolution_prs (
    id                    TEXT PRIMARY KEY,
    task_id               TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    user_id               TEXT NOT NULL,
    state                 TEXT NOT NULL DEFAULT 'pending',
    reason                TEXT NOT NULL,
    rationale             TEXT NOT NULL,
    diff_summary          TEXT NOT NULL,
    self_review           TEXT NOT NULL,
    old_main_ts           TEXT NOT NULL,
    new_main_ts           TEXT NOT NULL,
    old_workflow_yaml     TEXT NOT NULL,
    new_workflow_yaml     TEXT NOT NULL,
    based_on_reflections  TEXT[] NOT NULL DEFAULT '{}',
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at           TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS evolution_user_idx ON evolution_prs (user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS evolution_state_idx ON evolution_prs (state);

-- sessions + messages --------------------------------------------------

CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    title           TEXT NOT NULL DEFAULT 'Untitled',
    pinned_tasks    TEXT[] NOT NULL DEFAULT '{}',
    model_id        TEXT,
    archived_at     TIMESTAMPTZ,
    message_count   INTEGER NOT NULL DEFAULT 0,
    last_msg_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_user_idx ON sessions (user_id, last_msg_at DESC);

CREATE TABLE IF NOT EXISTS messages (
    id          BIGSERIAL PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    role        TEXT NOT NULL,
    content     TEXT NOT NULL,
    metadata    JSONB NOT NULL DEFAULT '{}',
    ts          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS messages_session_idx ON messages (session_id, ts);

-- task corpus index (search) -------------------------------------------

CREATE TABLE IF NOT EXISTS task_index (
    task_id       TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL,
    name          TEXT NOT NULL,
    description   TEXT NOT NULL,
    main_ts       TEXT NOT NULL,
    workflow_yaml TEXT NOT NULL,
    connectors    TEXT[] NOT NULL DEFAULT '{}',
    triggers      TEXT[] NOT NULL DEFAULT '{}',
    visibility    TEXT NOT NULL DEFAULT 'private',
    embedding     vector(1024),
    indexed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS task_index_lex ON task_index USING gin ((name || ' ' || description) gin_trgm_ops);
CREATE INDEX IF NOT EXISTS task_index_embedding ON task_index USING hnsw (embedding vector_cosine_ops);

-- triggers reference for cron / scheduler picks ------------------------
-- (Computed view; cron job re-syncs from tasks.triggers JSON.)
