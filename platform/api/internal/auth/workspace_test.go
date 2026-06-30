package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeMemberChecker struct{ member bool }

func (f *fakeMemberChecker) IsMember(_, _ string) bool { return f.member }

func TestWorkspace_NonMember403(t *testing.T) {
	m := New(false)
	mc := &fakeMemberChecker{member: false}

	claims := &Claims{Sub: "user1", Email: "user1@example.com"}
	reached := false
	h := m.Workspace(mc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Workspace-ID", "other-workspace")
	req = req.WithContext(withClaims(req.Context(), claims))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
	if reached {
		t.Fatal("handler must NOT be reached when non-member")
	}
}

func TestWorkspace_PersonalWorkspace(t *testing.T) {
	m := New(false)
	mc := &fakeMemberChecker{member: false}

	claims := &Claims{Sub: "user1", Email: "user1@example.com"}
	var gotWS string
	h := m.Workspace(mc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWS = WorkspaceID(r.Context())
	}))

	// Test with empty header
	req := httptest.NewRequest("GET", "/", nil)
	req = req.WithContext(withClaims(req.Context(), claims))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotWS != "user1" {
		t.Fatalf("want workspace=user1, got %q", gotWS)
	}

	// Test with header == sub
	gotWS = ""
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.Header.Set("X-Workspace-ID", "user1")
	req2 = req2.WithContext(withClaims(req2.Context(), claims))

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec2.Code)
	}
	if gotWS != "user1" {
		t.Fatalf("want workspace=user1, got %q", gotWS)
	}
}

func TestWorkspace_MemberPasses(t *testing.T) {
	m := New(false)
	mc := &fakeMemberChecker{member: true}

	claims := &Claims{Sub: "user1", Email: "user1@example.com"}
	var gotWS string
	h := m.Workspace(mc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotWS = WorkspaceID(r.Context())
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Workspace-ID", "other-workspace")
	req = req.WithContext(withClaims(req.Context(), claims))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if gotWS != "other-workspace" {
		t.Fatalf("want workspace=other-workspace, got %q", gotWS)
	}
}

func TestWorkspace_NoClaims_PassThrough(t *testing.T) {
	m := New(false)
	mc := &fakeMemberChecker{member: false}

	reached := false
	h := m.Workspace(mc)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
	}))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Workspace-ID", "some-workspace")
	// No claims injected

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 (pass-through), got %d", rec.Code)
	}
	if !reached {
		t.Fatal("handler must be reached when no claims")
	}
}
