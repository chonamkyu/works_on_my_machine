package store

import (
	"context"
	"testing"
	"time"
)

// TestAdminStore exercises ListAllWorkspaces, ListAllTriggerState, and
// AdminOverview against a live DB. Requires PLATFORM_DB_URL.
func TestAdminStore(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	suffix := randID()
	ownerSub := "admin_test_owner_" + suffix
	wsID := "admin_test_ws_" + suffix

	// FK: user must exist before workspace/members can reference it.
	if err := m.UpsertUserOnLogin(ownerSub, ownerSub+"@x", ownerSub); err != nil {
		t.Fatalf("UpsertUserOnLogin: %v", err)
	}

	// Create workspace directly so we control the id.
	_, err := m.pool.Exec(ctx,
		`INSERT INTO workspaces (id, name, kind, owner_sub) VALUES ($1, $2, 'shared', $3)`,
		wsID, "Admin Test WS "+suffix, ownerSub)
	if err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	_, _ = m.pool.Exec(ctx,
		`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`,
		wsID, ownerSub)

	// Create a task in that workspace.
	task := m.CreateTask(&Task{
		ID:          "tsk_admintest_" + suffix,
		UserID:      ownerSub,
		WorkspaceID: wsID,
		Name:        "admin-test-task-" + suffix,
	})

	// Insert a trigger_state row for that task.
	now := time.Now()
	if err := m.RecordTriggerFire(task.ID, "trg_admin_test_"+suffix, wsID, &now, ""); err != nil {
		t.Fatalf("RecordTriggerFire: %v", err)
	}

	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, `DELETE FROM trigger_state WHERE task_id=$1`, task.ID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM workspace_members WHERE workspace_id=$1`, wsID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, wsID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM users WHERE sub=$1`, ownerSub)
	})

	// --- ListAllWorkspaces ---
	workspaces, err := m.ListAllWorkspaces()
	if err != nil {
		t.Fatalf("ListAllWorkspaces: %v", err)
	}
	var found *AdminWorkspace
	for _, w := range workspaces {
		if w.ID == wsID {
			found = w
			break
		}
	}
	if found == nil {
		t.Fatalf("ListAllWorkspaces: seeded workspace %s not found in results (len=%d)", wsID, len(workspaces))
	}
	if found.MemberCount < 1 {
		t.Errorf("MemberCount: want >= 1, got %d", found.MemberCount)
	}
	if found.TaskCount < 1 {
		t.Errorf("TaskCount: want >= 1, got %d", found.TaskCount)
	}
	t.Logf("ListAllWorkspaces: found ws %s memberCount=%d taskCount=%d", wsID, found.MemberCount, found.TaskCount)

	// --- ListAllTriggerState ---
	triggers, err := m.ListAllTriggerState()
	if err != nil {
		t.Fatalf("ListAllTriggerState: %v", err)
	}
	var foundTrig *AdminTriggerState
	for _, ts := range triggers {
		if ts.TaskID == task.ID {
			foundTrig = ts
			break
		}
	}
	if foundTrig == nil {
		t.Fatalf("ListAllTriggerState: trigger for task %s not found (len=%d)", task.ID, len(triggers))
	}
	if foundTrig.TaskName == "" {
		t.Errorf("ListAllTriggerState: TaskName should be populated via join, got empty")
	}
	if foundTrig.WorkspaceID != wsID {
		t.Errorf("ListAllTriggerState: WorkspaceID: want %s, got %s", wsID, foundTrig.WorkspaceID)
	}
	t.Logf("ListAllTriggerState: found trigger taskName=%q workspaceID=%q", foundTrig.TaskName, foundTrig.WorkspaceID)

	// --- AdminOverview ---
	counts, err := m.AdminOverview()
	if err != nil {
		t.Fatalf("AdminOverview: %v", err)
	}
	// Non-negative sanity checks — the DB has at least our seeded data.
	if counts.Users < 1 {
		t.Errorf("AdminOverview.Users: want >= 1, got %d", counts.Users)
	}
	if counts.Workspaces < 1 {
		t.Errorf("AdminOverview.Workspaces: want >= 1, got %d", counts.Workspaces)
	}
	if counts.ActiveRuns < 0 {
		t.Errorf("AdminOverview.ActiveRuns: want >= 0, got %d", counts.ActiveRuns)
	}
	if counts.PendingProposals < 0 {
		t.Errorf("AdminOverview.PendingProposals: want >= 0, got %d", counts.PendingProposals)
	}
	if counts.PendingEvolutionPRs < 0 {
		t.Errorf("AdminOverview.PendingEvolutionPRs: want >= 0, got %d", counts.PendingEvolutionPRs)
	}
	t.Logf("AdminOverview: users=%d workspaces=%d mcpServers=%d skills=%d runningServices=%d activeRuns=%d pendingProposals=%d pendingEvoPRs=%d",
		counts.Users, counts.Workspaces, counts.MCPServers, counts.Skills,
		counts.RunningServices, counts.ActiveRuns, counts.PendingProposals, counts.PendingEvolutionPRs)
}

// TestListAllEvolutionPRs verifies the cross-workspace evolution PR listing.
func TestListAllEvolutionPRs(t *testing.T) {
	m := newTestMemory(t)
	ctx := context.Background()

	suffix := randID()
	ownerSub := "evo_test_owner_" + suffix
	wsID := "evo_test_ws_" + suffix

	if err := m.UpsertUserOnLogin(ownerSub, ownerSub+"@x", ownerSub); err != nil {
		t.Fatalf("UpsertUserOnLogin: %v", err)
	}
	_, _ = m.pool.Exec(ctx,
		`INSERT INTO workspaces (id, name, kind, owner_sub) VALUES ($1, $2, 'shared', $3)`,
		wsID, "Evo Test WS "+suffix, ownerSub)

	task := m.CreateTask(&Task{
		ID:          "tsk_evotest_" + suffix,
		UserID:      ownerSub,
		WorkspaceID: wsID,
		Name:        "evo-test-task-" + suffix,
	})

	evoStore := NewEvolutionStore(m.pool)
	pr := evoStore.Create(&EvolutionPR{
		TaskID:      task.ID,
		UserID:      ownerSub,
		WorkspaceID: wsID,
		Reason:      "admin_test_" + suffix,
	})

	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, `DELETE FROM evolution_prs WHERE id=$1`, pr.ID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM workspaces WHERE id=$1`, wsID)
		_, _ = m.pool.Exec(ctx, `DELETE FROM users WHERE sub=$1`, ownerSub)
	})

	prs, err := evoStore.ListAllEvolutionPRs()
	if err != nil {
		t.Fatalf("ListAllEvolutionPRs: %v", err)
	}
	var found bool
	for _, p := range prs {
		if p.ID == pr.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("ListAllEvolutionPRs: seeded PR %s not found (len=%d)", pr.ID, len(prs))
	}
	t.Logf("ListAllEvolutionPRs: found seeded PR %s across all workspaces", pr.ID)
}
