package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestTriggerState_RecordAndQuery(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	m := newTestMemory(t)
	ctx := context.Background()

	taskID := "tsk_test_ts_" + randID()
	triggerID := "trg_cron_" + randID()
	wsID := "ws_test_ts_" + randID()

	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, "DELETE FROM trigger_state WHERE task_id = $1", taskID)
	})

	// First fire — no next_fire_at.
	if err := m.RecordTriggerFire(taskID, triggerID, wsID, nil, ""); err != nil {
		t.Fatalf("RecordTriggerFire: %v", err)
	}

	// GetTriggerState by task should return the row.
	states := m.GetTriggerState(taskID)
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %d", len(states))
	}
	s := states[0]
	if s.TaskID != taskID {
		t.Errorf("TaskID mismatch: got %q", s.TaskID)
	}
	if s.TriggerID != triggerID {
		t.Errorf("TriggerID mismatch: got %q", s.TriggerID)
	}
	if s.WorkspaceID != wsID {
		t.Errorf("WorkspaceID mismatch: got %q", s.WorkspaceID)
	}
	if s.LastFiredAt == nil {
		t.Error("LastFiredAt should not be nil after first fire")
	}
	if s.NextFireAt != nil {
		t.Error("NextFireAt should be nil (we passed nil)")
	}
	if s.LastError != "" {
		t.Errorf("LastError should be empty, got %q", s.LastError)
	}

	firstFiredAt := s.LastFiredAt

	// ListTriggerState by workspace should also return the row.
	wsList := m.ListTriggerState(wsID)
	if len(wsList) == 0 {
		t.Fatal("ListTriggerState returned empty for workspace")
	}
	var found bool
	for _, ts := range wsList {
		if ts.TaskID == taskID && ts.TriggerID == triggerID {
			found = true
		}
	}
	if !found {
		t.Error("trigger state not found in ListTriggerState result")
	}

	// Small sleep so the second fire lands at a distinct timestamp.
	time.Sleep(10 * time.Millisecond)

	// Second fire — with a next_fire_at and an error string.
	next := time.Now().Add(5 * time.Minute).UTC().Truncate(time.Second)
	if err := m.RecordTriggerFire(taskID, triggerID, wsID, &next, "dispatch timeout"); err != nil {
		t.Fatalf("RecordTriggerFire (2nd): %v", err)
	}

	// The row should be updated, not duplicated.
	states2 := m.GetTriggerState(taskID)
	if len(states2) != 1 {
		t.Fatalf("expected 1 state after upsert, got %d", len(states2))
	}
	s2 := states2[0]

	if s2.LastFiredAt == nil {
		t.Fatal("LastFiredAt nil after second fire")
	}
	if !s2.LastFiredAt.After(*firstFiredAt) {
		t.Errorf("second fire should update LastFiredAt: first=%v second=%v", firstFiredAt, s2.LastFiredAt)
	}
	if s2.NextFireAt == nil {
		t.Fatal("NextFireAt should be set after second fire")
	}
	if !s2.NextFireAt.Truncate(time.Second).Equal(next) {
		t.Errorf("NextFireAt mismatch: got %v want %v", s2.NextFireAt, next)
	}
	if s2.LastError != "dispatch timeout" {
		t.Errorf("LastError mismatch: got %q", s2.LastError)
	}

	// A different workspace should not see this row.
	otherList := m.ListTriggerState("ws_other_" + randID())
	for _, ts := range otherList {
		if ts.TaskID == taskID {
			t.Error("other workspace must not see this task's trigger state")
		}
	}
}
