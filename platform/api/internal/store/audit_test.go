package store

import (
	"context"
	"testing"
)

func TestAudit_RecordAndList(t *testing.T) {
	m := newTestMemory(t)

	actorSub := "test_audit_actor_sub"
	wsID := "ws_test_audit_001"

	t.Cleanup(func() {
		_, _ = m.pool.Exec(context.Background(), "DELETE FROM audit_log WHERE actor_sub=$1", actorSub)
	})

	entry := AuditEntry{
		ActorSub:     actorSub,
		ActorEmail:   "auditor@example.com",
		Action:       "test.audit_action",
		ResourceType: "task",
		ResourceID:   "tsk_audit_test_001",
		ResourceName: "my-test-task",
		WorkspaceID:  wsID,
		Details: map[string]any{
			"reason": "test run",
			"count":  float64(42),
		},
	}

	if err := m.RecordAudit(entry); err != nil {
		t.Fatalf("RecordAudit: %v", err)
	}

	results := m.ListAudit(wsID, 10)
	if len(results) == 0 {
		t.Fatal("ListAudit returned empty slice; expected at least 1 entry")
	}

	// Find our entry (there could be other rows from prior test runs if cleanup failed)
	var got *AuditEntry
	for _, r := range results {
		if r.ActorSub == actorSub {
			got = r
			break
		}
	}
	if got == nil {
		t.Fatalf("our entry not found in ListAudit results (len=%d)", len(results))
	}

	if got.ActorEmail != entry.ActorEmail {
		t.Errorf("ActorEmail: got %q want %q", got.ActorEmail, entry.ActorEmail)
	}
	if got.Action != entry.Action {
		t.Errorf("Action: got %q want %q", got.Action, entry.Action)
	}
	if got.ResourceType != entry.ResourceType {
		t.Errorf("ResourceType: got %q want %q", got.ResourceType, entry.ResourceType)
	}
	if got.ResourceID != entry.ResourceID {
		t.Errorf("ResourceID: got %q want %q", got.ResourceID, entry.ResourceID)
	}
	if got.ResourceName != entry.ResourceName {
		t.Errorf("ResourceName: got %q want %q", got.ResourceName, entry.ResourceName)
	}
	if got.WorkspaceID != entry.WorkspaceID {
		t.Errorf("WorkspaceID: got %q want %q", got.WorkspaceID, entry.WorkspaceID)
	}
	if got.ID == 0 {
		t.Error("ID should be non-zero after insert")
	}
	if got.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}

	// Details round-trip
	if got.Details == nil {
		t.Fatal("Details is nil; expected map")
	}
	if got.Details["reason"] != "test run" {
		t.Errorf("Details[reason]: got %v want %q", got.Details["reason"], "test run")
	}
	if got.Details["count"] != float64(42) {
		t.Errorf("Details[count]: got %v want 42", got.Details["count"])
	}

	// Empty workspaceID returns across all workspaces (admin view)
	all := m.ListAudit("", 10)
	found := false
	for _, r := range all {
		if r.ActorSub == actorSub {
			found = true
			break
		}
	}
	if !found {
		t.Error("ListAudit(\"\", ...) should include our entry (admin view)")
	}

	// nil Details should not panic
	nilEntry := AuditEntry{
		ActorSub: actorSub,
		Action:   "test.nil_details",
		Details:  nil,
	}
	if err := m.RecordAudit(nilEntry); err != nil {
		t.Fatalf("RecordAudit with nil Details: %v", err)
	}
}
