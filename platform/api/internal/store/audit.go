package store

import (
	"context"
	"encoding/json"
	"time"
)

// AuditEntry is a single row from the audit_log table.
type AuditEntry struct {
	ID           int64          `json:"id"`
	ActorSub     string         `json:"actorSub"`
	ActorEmail   string         `json:"actorEmail"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	ResourceName string         `json:"resourceName"`
	WorkspaceID  string         `json:"workspaceId"`
	Details      map[string]any `json:"details"`
	CreatedAt    time.Time      `json:"createdAt"`
}

// RecordAudit inserts an audit log entry. It is best-effort: the error is
// returned to the caller but the caller is expected to ignore/log it rather
// than aborting the request.
func (m *Memory) RecordAudit(e AuditEntry) error {
	details := e.Details
	if details == nil {
		details = map[string]any{}
	}
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		detailsJSON = []byte("{}")
	}

	_, err = m.pool.Exec(context.Background(), `
		INSERT INTO audit_log
			(actor_sub, actor_email, action, resource_type, resource_id, resource_name, workspace_id, details)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		e.ActorSub, e.ActorEmail, e.Action,
		e.ResourceType, e.ResourceID, e.ResourceName,
		e.WorkspaceID, detailsJSON)
	return err
}

// ListAudit returns audit entries ordered by created_at DESC, capped at limit.
// If workspaceID is empty all workspaces are included (admin view).
// If limit <= 0 it defaults to 200.
func (m *Memory) ListAudit(workspaceID string, limit int) []*AuditEntry {
	if limit <= 0 {
		limit = 200
	}

	var rows interface {
		Next() bool
		Scan(dest ...any) error
		Close()
	}

	var err error
	if workspaceID == "" {
		r, qErr := m.pool.Query(context.Background(), `
			SELECT id, actor_sub, actor_email, action, resource_type, resource_id, resource_name, workspace_id, details, created_at
			FROM audit_log
			ORDER BY created_at DESC
			LIMIT $1`, limit)
		if qErr != nil {
			return nil
		}
		rows = r
		err = qErr
	} else {
		r, qErr := m.pool.Query(context.Background(), `
			SELECT id, actor_sub, actor_email, action, resource_type, resource_id, resource_name, workspace_id, details, created_at
			FROM audit_log
			WHERE workspace_id = $1
			ORDER BY created_at DESC
			LIMIT $2`, workspaceID, limit)
		if qErr != nil {
			return nil
		}
		rows = r
		err = qErr
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []*AuditEntry
	for rows.Next() {
		var e AuditEntry
		var detailsJSON []byte
		if scanErr := rows.Scan(
			&e.ID, &e.ActorSub, &e.ActorEmail, &e.Action,
			&e.ResourceType, &e.ResourceID, &e.ResourceName,
			&e.WorkspaceID, &detailsJSON, &e.CreatedAt,
		); scanErr != nil {
			continue
		}
		if len(detailsJSON) > 0 {
			_ = json.Unmarshal(detailsJSON, &e.Details)
		}
		if e.Details == nil {
			e.Details = map[string]any{}
		}
		out = append(out, &e)
	}
	return out
}
