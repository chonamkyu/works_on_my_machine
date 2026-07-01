package store

import (
	"context"
	"time"
)

// AdminOverviewCounts holds cheap aggregate counts for the admin overview endpoint.
type AdminOverviewCounts struct {
	Users               int `json:"users"`
	Workspaces          int `json:"workspaces"`
	MCPServers          int `json:"mcpServers"`
	Skills              int `json:"skills"`
	RunningServices     int `json:"runningServices"`
	ActiveRuns          int `json:"activeRuns"`
	PendingProposals    int `json:"pendingProposals"`
	PendingEvolutionPRs int `json:"pendingEvolutionPRs"`
}

// AdminWorkspace is a cross-workspace workspace row with denormalized counts.
type AdminWorkspace struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Kind        string    `json:"kind"`
	OwnerSub    string    `json:"ownerSub"`
	MemberCount int       `json:"memberCount"`
	TaskCount   int       `json:"taskCount"`
	CreatedAt   time.Time `json:"createdAt"`
}

// AdminTriggerState is a cross-workspace trigger_state row joined to its task.
type AdminTriggerState struct {
	TaskID      string     `json:"taskId"`
	TaskName    string     `json:"taskName"`
	WorkspaceID string     `json:"workspaceId"`
	TriggerID   string     `json:"triggerId"`
	LastFiredAt *time.Time `json:"lastFiredAt,omitempty"`
	NextFireAt  *time.Time `json:"nextFireAt,omitempty"`
	LastError   string     `json:"lastError,omitempty"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// AdminOverview returns aggregate counts for the admin overview. All queries
// are cheap SELECT count(*) scans with no joins.
func (m *Memory) AdminOverview() (*AdminOverviewCounts, error) {
	ctx := context.Background()
	c := &AdminOverviewCounts{}

	row := m.pool.QueryRow(ctx, `SELECT count(*) FROM users`)
	if err := row.Scan(&c.Users); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM workspaces`)
	if err := row.Scan(&c.Workspaces); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM mcp_servers WHERE enabled=true`)
	if err := row.Scan(&c.MCPServers); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM skills`)
	if err := row.Scan(&c.Skills); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM tasks WHERE kind='service' AND status != 'disabled'`)
	if err := row.Scan(&c.RunningServices); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM runs WHERE state IN ('queued','running')`)
	if err := row.Scan(&c.ActiveRuns); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM skill_proposals WHERE workspace_id=$1 AND status='pending'`, GlobalScope)
	if err := row.Scan(&c.PendingProposals); err != nil {
		return nil, err
	}

	row = m.pool.QueryRow(ctx, `SELECT count(*) FROM evolution_prs WHERE state='pending'`)
	if err := row.Scan(&c.PendingEvolutionPRs); err != nil {
		return nil, err
	}

	return c, nil
}

// ListAllWorkspaces returns all workspaces with denormalized member and task
// counts. The query joins once to workspace_members and once to tasks using
// aggregated subqueries so the main scan stays simple.
func (m *Memory) ListAllWorkspaces() ([]*AdminWorkspace, error) {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT
			w.id,
			w.name,
			w.kind,
			w.owner_sub,
			COALESCE((SELECT count(*) FROM workspace_members wm WHERE wm.workspace_id = w.id), 0) AS member_count,
			COALESCE((SELECT count(*) FROM tasks t WHERE t.workspace_id = w.id), 0) AS task_count,
			w.created_at
		FROM workspaces w
		ORDER BY w.created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*AdminWorkspace
	for rows.Next() {
		var ws AdminWorkspace
		if err := rows.Scan(&ws.ID, &ws.Name, &ws.Kind, &ws.OwnerSub, &ws.MemberCount, &ws.TaskCount, &ws.CreatedAt); err != nil {
			continue
		}
		out = append(out, &ws)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListAllTriggerState returns all trigger_state rows across every workspace,
// joined to their parent task for the task name. Ordered newest updated first.
func (m *Memory) ListAllTriggerState() ([]*AdminTriggerState, error) {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT
			ts.task_id,
			COALESCE(t.name, ''),
			ts.workspace_id,
			ts.trigger_id,
			ts.last_fired_at,
			ts.next_fire_at,
			COALESCE(ts.last_error, ''),
			ts.updated_at
		FROM trigger_state ts
		LEFT JOIN tasks t ON t.id = ts.task_id
		ORDER BY ts.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*AdminTriggerState
	for rows.Next() {
		var s AdminTriggerState
		if err := rows.Scan(&s.TaskID, &s.TaskName, &s.WorkspaceID, &s.TriggerID,
			&s.LastFiredAt, &s.NextFireAt, &s.LastError, &s.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListAllEvolutionPRs returns all evolution PR rows across every workspace,
// newest first. Added to EvolutionStore to keep evolution logic co-located.
func (s *EvolutionStore) ListAllEvolutionPRs() ([]*EvolutionPR, error) {
	rows, err := s.pool.Query(context.Background(), `
		SELECT id, task_id, user_id, workspace_id, state, reason, rationale, diff_summary, self_review,
		       old_main_ts, new_main_ts, old_workflow_yaml, new_workflow_yaml,
		       based_on_reflections, created_at, resolved_at
		FROM evolution_prs
		ORDER BY created_at DESC
		LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*EvolutionPR
	for rows.Next() {
		pr, err := scanEvolution(rows)
		if err == nil {
			out = append(out, pr)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
