package store

import (
	"context"
	"os"
	"testing"
)

func TestWorkspace_PersonalAndShared(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no db")
	}
	m := newTestMemory(t)
	ctx := context.Background()

	// FK safety: users must exist before workspaces/members can reference them.
	if err := m.UpsertUserOnLogin("ws_u1", "ws_u1@x", "ws_u1"); err != nil {
		t.Fatalf("upsert ws_u1: %v", err)
	}
	if err := m.UpsertUserOnLogin("ws_u2", "ws_u2@x", "ws_u2"); err != nil {
		t.Fatalf("upsert ws_u2: %v", err)
	}
	if err := m.UpsertUserOnLogin("ws_u3", "ws_u3@x", "ws_u3"); err != nil {
		t.Fatalf("upsert ws_u3: %v", err)
	}

	// Clean up test data after test — workspace cascade deletes members.
	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, "DELETE FROM workspaces WHERE owner_sub IN ('ws_u1','ws_u2','ws_u3')")
		_, _ = m.pool.Exec(ctx, "DELETE FROM users WHERE sub IN ('ws_u1','ws_u2','ws_u3')")
	})

	// EnsurePersonal creates the personal workspace with owner membership.
	if err := m.EnsurePersonal("ws_u1", "U1"); err != nil {
		t.Fatal(err)
	}
	if !m.IsMember("ws_u1", "ws_u1") {
		t.Fatal("owner should be member of personal ws")
	}

	// EnsurePersonal is idempotent.
	if err := m.EnsurePersonal("ws_u1", "U1"); err != nil {
		t.Fatalf("EnsurePersonal idempotent: %v", err)
	}

	// ListWorkspacesForUser returns the personal ws.
	wsList := m.ListWorkspacesForUser("ws_u1")
	if len(wsList) == 0 {
		t.Fatal("expected at least one workspace for ws_u1")
	}
	var foundPersonal bool
	for _, w := range wsList {
		if w.ID == "ws_u1" && w.Kind == "personal" {
			foundPersonal = true
		}
	}
	if !foundPersonal {
		t.Fatal("personal workspace not found in list")
	}

	// CreateWorkspace makes a shared workspace with owner membership.
	ws, err := m.CreateWorkspace("ws_u1", "Team A")
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID == "" {
		t.Fatal("expected non-empty workspace id")
	}
	if ws.Kind != "shared" {
		t.Fatalf("expected kind=shared, got %q", ws.Kind)
	}
	if !m.IsMember(ws.ID, "ws_u1") {
		t.Fatal("creator should be member of new shared ws")
	}

	// InviteMember: owner can invite.
	if err := m.InviteMember(ws.ID, "ws_u1", "ws_u2", "member"); err != nil {
		t.Fatal(err)
	}
	if !m.IsMember(ws.ID, "ws_u2") {
		t.Fatal("invited user should be a member")
	}

	// ListMembers returns both owner and ws_u2.
	members := m.ListMembers(ws.ID)
	if len(members) < 2 {
		t.Fatalf("expected at least 2 members, got %d", len(members))
	}

	// Non-owner must not invite.
	if err := m.InviteMember(ws.ID, "ws_u2", "ws_u3", "member"); err == nil {
		t.Fatal("non-owner must not invite")
	}

	// RemoveMember: owner removes ws_u2.
	if err := m.RemoveMember(ws.ID, "ws_u1", "ws_u2"); err != nil {
		t.Fatalf("owner remove member: %v", err)
	}
	if m.IsMember(ws.ID, "ws_u2") {
		t.Fatal("ws_u2 should no longer be a member after removal")
	}

	// Member removes themselves.
	if err := m.InviteMember(ws.ID, "ws_u1", "ws_u2", "member"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveMember(ws.ID, "ws_u2", "ws_u2"); err != nil {
		t.Fatalf("member self-remove: %v", err)
	}
	if m.IsMember(ws.ID, "ws_u2") {
		t.Fatal("ws_u2 should be gone after self-remove")
	}

	// Non-owner cannot remove another member.
	if err := m.InviteMember(ws.ID, "ws_u1", "ws_u2", "member"); err != nil {
		t.Fatal(err)
	}
	if err := m.InviteMember(ws.ID, "ws_u1", "ws_u3", "member"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveMember(ws.ID, "ws_u2", "ws_u3"); err == nil {
		t.Fatal("non-owner should not remove another member")
	}
}

func TestWorkspace_DeleteWorkspace(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no db")
	}
	m := newTestMemory(t)
	ctx := context.Background()

	// Create two users: del_u1 (owner) and del_u2 (non-owner).
	for _, sub := range []string{"del_u1", "del_u2", "del_adm"} {
		if err := m.UpsertUserOnLogin(sub, sub+"@x", sub); err != nil {
			t.Fatalf("upsert %s: %v", sub, err)
		}
	}

	t.Cleanup(func() {
		_, _ = m.pool.Exec(ctx, "DELETE FROM workspaces WHERE owner_sub IN ('del_u1','del_u2','del_adm')")
		_, _ = m.pool.Exec(ctx, "DELETE FROM users WHERE sub IN ('del_u1','del_u2','del_adm')")
	})

	// EnsurePersonal for del_u1.
	if err := m.EnsurePersonal("del_u1", "Del U1"); err != nil {
		t.Fatal(err)
	}

	// Refusing to delete a personal workspace.
	err := m.DeleteWorkspace("del_u1", "del_u1", false)
	if err == nil {
		t.Fatal("expected error deleting personal workspace, got nil")
	}
	if err.Error() != "cannot delete personal workspace" {
		t.Fatalf("wrong error: %v", err)
	}

	// Non-owner cannot delete a shared workspace.
	ws, err := m.CreateWorkspace("del_u1", "To Delete")
	if err != nil {
		t.Fatal(err)
	}
	err = m.DeleteWorkspace(ws.ID, "del_u2", false)
	if err == nil {
		t.Fatal("expected error for non-owner delete, got nil")
	}
	if err.Error() != "only the owner can delete a workspace" {
		t.Fatalf("wrong error: %v", err)
	}

	// Owner can delete a shared workspace.
	ws2, err := m.CreateWorkspace("del_u1", "Owner Deletes")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteWorkspace(ws2.ID, "del_u1", false); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	// Confirm it is gone (should return not-found error).
	err = m.DeleteWorkspace(ws2.ID, "del_u1", false)
	if err == nil {
		t.Fatal("expected not-found error after deletion, got nil")
	}

	// Admin can delete a workspace they don't own.
	ws3, err := m.CreateWorkspace("del_u1", "Admin Deletes")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteWorkspace(ws3.ID, "del_adm", true); err != nil {
		t.Fatalf("admin delete: %v", err)
	}
}
