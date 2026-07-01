package store

import (
	"context"
	"os"
	"testing"
)

// TestWorkspacesWithActiveRuns verifies that WorkspacesWithActiveRuns returns
// exactly the set of workspaces that have at least one queued/running run, and
// excludes workspaces whose runs have all finished. Requires a live DB.
func TestWorkspacesWithActiveRuns(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	m := newTestMemory(t)
	ctx := context.Background()

	wsActive := "war_active"
	wsDone := "war_done"

	// Tasks carry the workspace_id that CreateRun copies onto each run.
	taskActive := m.CreateTask(&Task{ID: "war_task_active", UserID: wsActive, WorkspaceID: wsActive, Name: "active"})
	taskDone := m.CreateTask(&Task{ID: "war_task_done", UserID: wsDone, WorkspaceID: wsDone, Name: "done"})

	t.Cleanup(func() {
		// runs cascade on tasks delete; sessions are keyed by task id.
		_, _ = m.pool.Exec(ctx, "DELETE FROM tasks WHERE id IN ($1,$2)", taskActive.ID, taskDone.ID)
	})

	// wsActive: one queued run + one running run.
	queued := m.CreateRun(taskActive.ID, "manual") // state = queued
	running := m.CreateRun(taskActive.ID, "manual")
	if _, err := m.UpdateRun(running.ID, func(r *Run) { r.State = RunRunning }); err != nil {
		t.Fatalf("UpdateRun running: %v", err)
	}

	// wsDone: a run that has finished (success) — must NOT count as active.
	finished := m.CreateRun(taskDone.ID, "manual")
	if _, err := m.UpdateRun(finished.ID, func(r *Run) { r.State = RunSuccess }); err != nil {
		t.Fatalf("UpdateRun finished: %v", err)
	}

	set, err := m.WorkspacesWithActiveRuns()
	if err != nil {
		t.Fatalf("WorkspacesWithActiveRuns: %v", err)
	}

	if !set[wsActive] {
		t.Errorf("expected %q in active set (has queued + running runs); set=%v", wsActive, set)
	}
	if set[wsDone] {
		t.Errorf("did not expect %q in active set (run is finished); set=%v", wsDone, set)
	}

	// Finish the queued+running runs; the workspace should drop out of the set.
	if _, err := m.UpdateRun(queued.ID, func(r *Run) { r.State = RunCancelled }); err != nil {
		t.Fatalf("UpdateRun cancel queued: %v", err)
	}
	if _, err := m.UpdateRun(running.ID, func(r *Run) { r.State = RunSuccess }); err != nil {
		t.Fatalf("UpdateRun finish running: %v", err)
	}
	set2, err := m.WorkspacesWithActiveRuns()
	if err != nil {
		t.Fatalf("WorkspacesWithActiveRuns (after finish): %v", err)
	}
	if set2[wsActive] {
		t.Errorf("workspace %q should have dropped out of active set after runs finished; set=%v", wsActive, set2)
	}
}
