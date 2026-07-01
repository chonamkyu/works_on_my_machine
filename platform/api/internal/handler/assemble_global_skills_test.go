package handler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/nk/platform/api/internal/handler"
)

// TestAssembleGlobalSkills verifies that assembleGlobalSkills resolves @latest
// for each skill in the registry and returns both name and raw content.
// Requires a live DB (PLATFORM_DB_URL).
func TestAssembleGlobalSkills(t *testing.T) {
	s := openTestDB(t)

	// Clean up both skills after the test regardless of outcome.
	t.Cleanup(func() {
		_ = s.DeleteSkill("ag-skill-alpha", "test-cleanup")
		_ = s.DeleteSkill("ag-skill-beta", "test-cleanup")
	})

	// Create two registry skills.
	_, _, err := s.CreateSkill("dev", "ag-skill-alpha", "Alpha skill", []string{"test"}, "Do alpha things.", "initial")
	if err != nil {
		t.Fatalf("CreateSkill ag-skill-alpha: %v", err)
	}
	_, _, err = s.CreateSkill("dev", "ag-skill-beta", "Beta skill", []string{"test"}, "Do beta things.", "initial")
	if err != nil {
		t.Fatalf("CreateSkill ag-skill-beta: %v", err)
	}

	// Build a minimal handler wired to the live store.
	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)

	got := h.AssembleGlobalSkills(context.Background())

	// We need at least the two skills we just created (there may be others).
	byName := map[string]string{}
	for _, gs := range got {
		byName[gs.Name] = gs.Raw
	}

	if _, ok := byName["ag-skill-alpha"]; !ok {
		t.Error("assembleGlobalSkills: missing ag-skill-alpha")
	}
	if _, ok := byName["ag-skill-beta"]; !ok {
		t.Error("assembleGlobalSkills: missing ag-skill-beta")
	}

	// Verify the Raw field contains the body content we created.
	if !strings.Contains(byName["ag-skill-alpha"], "Do alpha things.") {
		t.Errorf("ag-skill-alpha raw missing body: %q", byName["ag-skill-alpha"])
	}
	if !strings.Contains(byName["ag-skill-beta"], "Do beta things.") {
		t.Errorf("ag-skill-beta raw missing body: %q", byName["ag-skill-beta"])
	}

	// Publish a new version of alpha and confirm @latest updates.
	_, err = s.PublishSkillVersion("ag-skill-alpha", "dev", "Alpha v2", []string{"test"}, "Do alpha things v2.", "v2")
	if err != nil {
		t.Fatalf("PublishSkillVersion ag-skill-alpha: %v", err)
	}

	got2 := h.AssembleGlobalSkills(context.Background())
	byName2 := map[string]string{}
	for _, gs := range got2 {
		byName2[gs.Name] = gs.Raw
	}

	if !strings.Contains(byName2["ag-skill-alpha"], "Do alpha things v2.") {
		t.Errorf("after publish, ag-skill-alpha @latest raw missing v2 body: %q", byName2["ag-skill-alpha"])
	}
	// Beta should still be at v1.
	if !strings.Contains(byName2["ag-skill-beta"], "Do beta things.") {
		t.Errorf("ag-skill-beta raw changed unexpectedly: %q", byName2["ag-skill-beta"])
	}

	t.Logf("assembleGlobalSkills: returned %d skills, alpha@latest=v2, beta=v1: PASS", len(got2))
}
