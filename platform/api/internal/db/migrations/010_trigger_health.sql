-- 010_trigger_health.sql — per-trigger fire health (A2 Task 5).

CREATE TABLE IF NOT EXISTS trigger_state (
    task_id       text NOT NULL,
    trigger_id    text NOT NULL,
    workspace_id  text NOT NULL DEFAULT '',
    last_fired_at timestamptz,
    next_fire_at  timestamptz,
    last_error    text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, trigger_id)
);

CREATE INDEX IF NOT EXISTS trigger_state_ws_idx ON trigger_state (workspace_id);
