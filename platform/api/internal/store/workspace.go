package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Workspace represents a workspace row from the workspaces table.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"` // "personal" | "shared"
	OwnerSub  string    `json:"ownerSub"`
	CreatedAt time.Time `json:"createdAt"`
}

// Member represents a workspace_members row.
type Member struct {
	UserID  string    `json:"userId"`
	Role    string    `json:"role"` // "owner" | "member" | "viewer"
	AddedAt time.Time `json:"addedAt"`
}

// EnsurePersonal creates the personal workspace (id==sub, kind=personal) and
// owner membership if absent. It is idempotent — safe to call on every login.
// The caller must have already created the user row (FK constraint).
func (m *Memory) EnsurePersonal(sub, displayName string) error {
	ctx := context.Background()
	_, err := m.pool.Exec(ctx, `
		INSERT INTO workspaces (id, name, kind, owner_sub)
		VALUES ($1, $2, 'personal', $1)
		ON CONFLICT (id) DO NOTHING`,
		sub, displayName+" (personal)")
	if err != nil {
		return fmt.Errorf("EnsurePersonal workspace: %w", err)
	}
	_, err = m.pool.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $1, 'owner')
		ON CONFLICT (workspace_id, user_id) DO NOTHING`,
		sub)
	if err != nil {
		return fmt.Errorf("EnsurePersonal membership: %w", err)
	}
	return nil
}

// CreateWorkspace creates a new shared workspace owned by ownerSub and inserts
// the owner as a member. Returns the new workspace.
func (m *Memory) CreateWorkspace(ownerSub, name string) (*Workspace, error) {
	ctx := context.Background()
	ws := &Workspace{
		ID:      "ws_" + randID(),
		Name:    name,
		Kind:    "shared",
		OwnerSub: ownerSub,
	}
	err := m.pool.QueryRow(ctx, `
		INSERT INTO workspaces (id, name, kind, owner_sub)
		VALUES ($1, $2, 'shared', $3)
		RETURNING created_at`,
		ws.ID, ws.Name, ws.OwnerSub,
	).Scan(&ws.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("CreateWorkspace: %w", err)
	}
	_, err = m.pool.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')`,
		ws.ID, ownerSub)
	if err != nil {
		return nil, fmt.Errorf("CreateWorkspace membership: %w", err)
	}
	return ws, nil
}

// ListWorkspacesForUser returns all workspaces the given user belongs to.
func (m *Memory) ListWorkspacesForUser(sub string) []*Workspace {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT w.id, w.name, w.kind, w.owner_sub, w.created_at
		FROM workspaces w
		JOIN workspace_members wm ON wm.workspace_id = w.id
		WHERE wm.user_id = $1
		ORDER BY w.kind DESC, w.created_at ASC`, sub)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Workspace
	for rows.Next() {
		var w Workspace
		if err := rows.Scan(&w.ID, &w.Name, &w.Kind, &w.OwnerSub, &w.CreatedAt); err != nil {
			continue
		}
		out = append(out, &w)
	}
	return out
}

// InviteMember adds userID to workspaceID with the given role. Only the
// workspace owner (identified by ownerSub) may invite; any other caller gets
// an error.
func (m *Memory) InviteMember(workspaceID, ownerSub, userID, role string) error {
	ctx := context.Background()
	// Verify caller is the owner of this workspace.
	var callerRole string
	err := m.pool.QueryRow(ctx, `
		SELECT role FROM workspace_members
		WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, ownerSub).Scan(&callerRole)
	if err != nil || callerRole != "owner" {
		return fmt.Errorf("only the workspace owner can invite members")
	}
	_, err = m.pool.Exec(ctx, `
		INSERT INTO workspace_members (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, user_id) DO UPDATE SET role = EXCLUDED.role`,
		workspaceID, userID, role)
	if err != nil {
		return fmt.Errorf("InviteMember: %w", err)
	}
	return nil
}

// RemoveMember removes userID from workspaceID. The workspace owner can remove
// anyone (except themselves); a non-owner can only remove themselves.
func (m *Memory) RemoveMember(workspaceID, callerSub, userID string) error {
	ctx := context.Background()
	var callerRole string
	if err := m.pool.QueryRow(ctx, `
		SELECT role FROM workspace_members
		WHERE workspace_id=$1 AND user_id=$2`,
		workspaceID, callerSub).Scan(&callerRole); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("RemoveMember: lookup caller: %w", err)
	}

	if callerRole != "owner" && callerSub != userID {
		return fmt.Errorf("insufficient permissions to remove member")
	}
	tag, err := m.pool.Exec(ctx, `
		DELETE FROM workspace_members
		WHERE workspace_id=$1 AND user_id=$2 AND role != 'owner'`,
		workspaceID, userID)
	if err != nil {
		return fmt.Errorf("RemoveMember: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("member not found or cannot remove owner")
	}
	return nil
}

// ListMembers returns all members of the given workspace.
func (m *Memory) ListMembers(workspaceID string) []*Member {
	ctx := context.Background()
	rows, err := m.pool.Query(ctx, `
		SELECT user_id, role, added_at
		FROM workspace_members
		WHERE workspace_id=$1
		ORDER BY role DESC, added_at ASC`, workspaceID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []*Member
	for rows.Next() {
		var mem Member
		if err := rows.Scan(&mem.UserID, &mem.Role, &mem.AddedAt); err != nil {
			continue
		}
		out = append(out, &mem)
	}
	return out
}

// IsMember reports whether sub is a member of workspaceID.
func (m *Memory) IsMember(workspaceID, sub string) bool {
	ctx := context.Background()
	var ok bool
	m.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM workspace_members
			WHERE workspace_id=$1 AND user_id=$2
		)`, workspaceID, sub).Scan(&ok)
	return ok
}
