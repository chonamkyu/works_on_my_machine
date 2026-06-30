package store

import (
	"context"
	"os"
	"testing"
)

// TestSkillProposal_GlobalScope verifies that proposals stored with
// WorkspaceID=GlobalScope ("_global") are returned by ListSkillProposals
// for GlobalScope and NOT for a per-workspace lookup.
//
// This mirrors what the SkillEvolver does: it calls CreateSkillProposal with
// WorkspaceID=GlobalScope, so the stored rows must be retrievable via
// ListSkillProposals(GlobalScope, ...) and must NOT appear in a per-tenant
// ListSkillProposals("some-other-ws", ...) call.
func TestSkillProposal_GlobalScope(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL — set PLATFORM_DB_URL to run this test")
	}

	m := newTestMemory(t)

	// Create a skill proposal scoped to GlobalScope (the evolver's path).
	p, err := m.CreateSkillProposal(&SkillProposal{
		UserID:      GlobalScope,
		WorkspaceID: GlobalScope,
		SkillName:   "test-global-skill",
		SkillBody:   "# Test global skill\nDo the thing.",
		Rationale:   "test rationale",
		Confidence:  "high",
		AppliesWhen: "always",
	})
	if err != nil {
		t.Fatalf("CreateSkillProposal: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected non-empty proposal ID")
	}

	// Clean up this row regardless of test outcome.
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, "DELETE FROM skill_proposals WHERE id=$1", p.ID)
	})

	// ListSkillProposals(GlobalScope, "pending") MUST return the proposal.
	globalList := m.ListSkillProposals(GlobalScope, "pending")
	found := false
	for _, sp := range globalList {
		if sp.ID == p.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("global proposal %q not found in ListSkillProposals(%q, pending) — got %d rows",
			p.ID, GlobalScope, len(globalList))
	}

	// ListSkillProposals for a different workspace must NOT return the global proposal.
	const unrelatedWS = "ws-unrelated-99"
	tenantList := m.ListSkillProposals(unrelatedWS, "pending")
	for _, sp := range tenantList {
		if sp.ID == p.ID {
			t.Fatalf("global proposal %q leaked into tenant list for %q", p.ID, unrelatedWS)
		}
	}
}

// TestSkillProposal_TenantDoesNotLeakToGlobal verifies the reverse: a per-tenant
// proposal is NOT returned when querying the global scope.
func TestSkillProposal_TenantDoesNotLeakToGlobal(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL — set PLATFORM_DB_URL to run this test")
	}

	m := newTestMemory(t)

	const tenantWS = "ws-tenant-isolation-test"

	p, err := m.CreateSkillProposal(&SkillProposal{
		UserID:      tenantWS,
		WorkspaceID: tenantWS,
		SkillName:   "test-tenant-skill",
		SkillBody:   "# Tenant skill",
		Confidence:  "medium",
	})
	if err != nil {
		t.Fatalf("CreateSkillProposal: %v", err)
	}

	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, "DELETE FROM skill_proposals WHERE id=$1", p.ID)
	})

	// Tenant list must include it.
	tenantList := m.ListSkillProposals(tenantWS, "pending")
	found := false
	for _, sp := range tenantList {
		if sp.ID == p.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("tenant proposal %q not found in its own workspace list", p.ID)
	}

	// Global list must NOT include the per-tenant proposal.
	globalList := m.ListSkillProposals(GlobalScope, "pending")
	for _, sp := range globalList {
		if sp.ID == p.ID {
			t.Fatalf("per-tenant proposal %q leaked into global list", p.ID)
		}
	}
}
