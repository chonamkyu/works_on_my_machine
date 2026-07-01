package store

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestGetSkillNotFoundSentinel(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	m := newTestMemory(t)

	_, err := m.GetSkill("does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSkill(does-not-exist): want errors.Is(err, ErrNotFound)==true, got %v", err)
	}
}

func TestParseSkillRef(t *testing.T) {
	tests := []struct {
		input       string
		wantName    string
		wantVersion string
		wantErr     bool
	}{
		{"myskill", "myskill", "latest", false},
		{"myskill@latest", "myskill", "latest", false},
		{"myskill@2", "myskill", "2", false},
		{"myskill@stable", "myskill", "stable", false},
		{"", "", "", true},
		{"@version", "", "", true},
	}
	for _, tc := range tests {
		ref, err := ParseSkillRef(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseSkillRef(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseSkillRef(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if ref.Name != tc.wantName || ref.Version != tc.wantVersion {
			t.Errorf("ParseSkillRef(%q) = {%q,%q}, want {%q,%q}",
				tc.input, ref.Name, ref.Version, tc.wantName, tc.wantVersion)
		}
	}
}

func TestSkills_CreatePublishResolveAlias(t *testing.T) {
	if os.Getenv("PLATFORM_DB_URL") == "" {
		t.Skip("no PLATFORM_DB_URL")
	}
	m := newTestMemory(t)
	ctx := context.Background()

	// Cleanup at end.
	t.Cleanup(func() {
		_ = m.DeleteSkill("s1", "test-cleanup")
	})

	// Also clean up any pre-existing "s1" from a previous run.
	_ = m.DeleteSkill("s1", "pre-test-cleanup")

	// --- Create v1 ---
	sk, v1, err := m.CreateSkill("user1", "s1", "first skill", []string{"ai", "test"}, "body of v1", "initial version")
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if sk.Name != "s1" {
		t.Errorf("expected skill name 's1', got %q", sk.Name)
	}
	if v1.VersionNo != 1 {
		t.Errorf("expected version 1, got %d", v1.VersionNo)
	}
	if v1.Body != "body of v1" {
		t.Errorf("expected body 'body of v1', got %q", v1.Body)
	}

	// Duplicate create must fail.
	_, _, err2 := m.CreateSkill("user1", "s1", "dup", nil, "body", "")
	if err2 == nil {
		t.Error("expected error on duplicate CreateSkill")
	}

	// --- Resolve bare name -> @latest -> v1 ---
	ref1, err := ParseSkillRef("s1")
	if err != nil {
		t.Fatalf("ParseSkillRef s1: %v", err)
	}
	resolved, err := m.ResolveSkill(ref1)
	if err != nil {
		t.Fatalf("ResolveSkill(s1@latest): %v", err)
	}
	if resolved.Body != "body of v1" {
		t.Errorf("resolve latest after create: want body 'body of v1', got %q", resolved.Body)
	}

	// --- Publish v2 ---
	v2, err := m.PublishSkillVersion("s1", "user2", "updated skill", []string{"ai", "v2"}, "body of v2", "second version")
	if err != nil {
		t.Fatalf("PublishSkillVersion: %v", err)
	}
	if v2.VersionNo != 2 {
		t.Errorf("expected version 2, got %d", v2.VersionNo)
	}

	// @latest must now resolve to v2.
	refLatest, _ := ParseSkillRef("s1@latest")
	resolvedLatest, err := m.ResolveSkill(refLatest)
	if err != nil {
		t.Fatalf("ResolveSkill(s1@latest) after publish: %v", err)
	}
	if resolvedLatest.Body != "body of v2" {
		t.Errorf("@latest after publish v2: want 'body of v2', got %q", resolvedLatest.Body)
	}

	// s1@1 must still resolve to v1 (immutable).
	ref1pin, _ := ParseSkillRef("s1@1")
	resolvedV1, err := m.ResolveSkill(ref1pin)
	if err != nil {
		t.Fatalf("ResolveSkill(s1@1): %v", err)
	}
	if resolvedV1.Body != "body of v1" {
		t.Errorf("s1@1 still body of v1: got %q", resolvedV1.Body)
	}

	// --- ListSkillVersions must show 2 versions ---
	versions, err := m.ListSkillVersions("s1")
	if err != nil {
		t.Fatalf("ListSkillVersions: %v", err)
	}
	if len(versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(versions))
	}

	// --- GetSkillVersion ---
	gv, err := m.GetSkillVersion("s1", 1)
	if err != nil {
		t.Fatalf("GetSkillVersion(s1,1): %v", err)
	}
	if gv.VersionNo != 1 {
		t.Errorf("GetSkillVersion no: %d", gv.VersionNo)
	}

	// --- GetSkill has LatestVersion=2 ---
	got, err := m.GetSkill("s1")
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.LatestVersion != 2 {
		t.Errorf("LatestVersion want 2, got %d", got.LatestVersion)
	}

	// --- MoveSkillAlias: create "stable" pointing at v1 ---
	alias, err := m.MoveSkillAlias("s1", "user1", "stable", 1)
	if err != nil {
		t.Fatalf("MoveSkillAlias: %v", err)
	}
	if alias.Alias != "stable" || alias.VersionNo != 1 {
		t.Errorf("alias: %+v", alias)
	}

	// Resolve s1@stable -> v1 body.
	refStable, _ := ParseSkillRef("s1@stable")
	resolvedStable, err := m.ResolveSkill(refStable)
	if err != nil {
		t.Fatalf("ResolveSkill(s1@stable): %v", err)
	}
	if resolvedStable.Body != "body of v1" {
		t.Errorf("s1@stable should be v1 body, got %q", resolvedStable.Body)
	}

	// --- ListSkillAliases: should have at least "latest" and "stable" ---
	aliases, err := m.ListSkillAliases("s1")
	if err != nil {
		t.Fatalf("ListSkillAliases: %v", err)
	}
	if len(aliases) < 2 {
		t.Errorf("expected >= 2 aliases, got %d", len(aliases))
	}

	// --- ListSkills ---
	all, err := m.ListSkills(ctx)
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	found := false
	for _, s := range all {
		if s.Name == "s1" {
			found = true
		}
	}
	if !found {
		t.Error("ListSkills: s1 not found")
	}
}
