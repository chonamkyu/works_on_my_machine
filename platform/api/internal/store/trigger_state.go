package store

import (
	"context"
	"time"
)

// TriggerState records the last-fire health for a single (task, trigger) pair.
type TriggerState struct {
	TaskID      string     `json:"taskId"`
	TriggerID   string     `json:"triggerId"`
	WorkspaceID string     `json:"workspaceId"`
	LastFiredAt *time.Time `json:"lastFiredAt,omitempty"`
	NextFireAt  *time.Time `json:"nextFireAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// RecordTriggerFire upserts a trigger_state row. It sets last_fired_at=now(),
// stores the supplied nextFireAt and fireErr, and bumps updated_at.
// Best-effort: the caller should log errors but must not panic.
func (m *Memory) RecordTriggerFire(taskID, triggerID, workspaceID string, nextFireAt *time.Time, fireErr string) error {
	_, err := m.pool.Exec(context.Background(), `
		INSERT INTO trigger_state (task_id, trigger_id, workspace_id, last_fired_at, next_fire_at, last_error, updated_at)
		VALUES ($1, $2, $3, now(), $4, $5, now())
		ON CONFLICT (task_id, trigger_id) DO UPDATE SET
			workspace_id  = EXCLUDED.workspace_id,
			last_fired_at = now(),
			next_fire_at  = EXCLUDED.next_fire_at,
			last_error    = EXCLUDED.last_error,
			updated_at    = now()`,
		taskID, triggerID, workspaceID, nextFireAt, fireErr)
	return err
}

// ListTriggerState returns all trigger_state rows for the given workspace,
// ordered by task_id, trigger_id for stable output.
func (m *Memory) ListTriggerState(workspaceID string) []*TriggerState {
	rows, err := m.pool.Query(context.Background(), `
		SELECT task_id, trigger_id, workspace_id, last_fired_at, next_fire_at, last_error, updated_at
		FROM trigger_state
		WHERE workspace_id = $1
		ORDER BY task_id, trigger_id`, workspaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*TriggerState
	for rows.Next() {
		var s TriggerState
		if err := rows.Scan(&s.TaskID, &s.TriggerID, &s.WorkspaceID, &s.LastFiredAt, &s.NextFireAt, &s.LastError, &s.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &s)
	}
	return out
}

// GetTriggerState returns all trigger_state rows for a specific task,
// ordered by trigger_id.
func (m *Memory) GetTriggerState(taskID string) []*TriggerState {
	rows, err := m.pool.Query(context.Background(), `
		SELECT task_id, trigger_id, workspace_id, last_fired_at, next_fire_at, last_error, updated_at
		FROM trigger_state
		WHERE task_id = $1
		ORDER BY trigger_id`, taskID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*TriggerState
	for rows.Next() {
		var s TriggerState
		if err := rows.Scan(&s.TaskID, &s.TriggerID, &s.WorkspaceID, &s.LastFiredAt, &s.NextFireAt, &s.LastError, &s.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &s)
	}
	return out
}
