package supervisor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nk/platform/api/internal/portreg"
	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/supervisor"
)

// buildDeps constructs a minimal Deps for the process backend using the given
// workflow root. It does not open a live DB; the store.Memory pointer is nil,
// meaning tests that exercise actual runs must either supply their own DB or
// limit scope to directory/path-level assertions.
func buildDeps(t *testing.T, root string) supervisor.Deps {
	t.Helper()
	wfs, err := store.NewWorkflowFS(root)
	if err != nil {
		t.Fatalf("NewWorkflowFS: %v", err)
	}
	return supervisor.Deps{
		Store:   nil,
		Bus:     store.NewBus(),
		Files:   wfs,
		Creds:   nil,
		Ports:   portreg.New(),
		SDKPath: "",
		Node:    "",
	}
}

// TestWorkspaceDirIsolation verifies that Dir() for two different workspaces
// resolves to different paths that are both under ws/<wsid>/workflows/<taskid>.
func TestWorkspaceDirIsolation(t *testing.T) {
	root := t.TempDir()
	wfs, err := store.NewWorkflowFS(root)
	if err != nil {
		t.Fatalf("NewWorkflowFS: %v", err)
	}

	dirA := wfs.Dir("wsA", "task1")
	dirB := wfs.Dir("wsB", "task1")

	if dirA == dirB {
		t.Fatalf("expected different dirs for wsA and wsB, got same: %s", dirA)
	}
	if !filepath.IsAbs(dirA) {
		t.Errorf("dirA should be absolute, got %q", dirA)
	}

	// Both paths must be under their respective ws/<wsid>/workflows/<taskid>.
	wantA := filepath.Join(root, "ws", "wsA", "workflows", "task1")
	wantB := filepath.Join(root, "ws", "wsB", "workflows", "task1")
	if dirA != wantA {
		t.Errorf("dirA: got %q, want %q", dirA, wantA)
	}
	if dirB != wantB {
		t.Errorf("dirB: got %q, want %q", dirB, wantB)
	}
}

// TestWriteFilesIsolation verifies that WriteFiles for wsA does not affect wsB.
func TestWriteFilesIsolation(t *testing.T) {
	root := t.TempDir()
	wfs, err := store.NewWorkflowFS(root)
	if err != nil {
		t.Fatalf("NewWorkflowFS: %v", err)
	}

	if err := wfs.WriteFiles("wsA", "task1", map[string]string{
		"main.ts": "export default async function(ctx) { return 'hello from wsA'; }",
	}); err != nil {
		t.Fatalf("WriteFiles wsA: %v", err)
	}

	// wsB/task1 must not exist yet.
	_, err = os.Stat(wfs.Dir("wsB", "task1"))
	if !os.IsNotExist(err) {
		t.Errorf("wsB/task1 dir should not exist after writing to wsA/task1, stat err: %v", err)
	}

	// wsA/task1/main.ts must exist.
	got, err := wfs.ReadFile("wsA", "task1", "main.ts")
	if err != nil {
		t.Fatalf("ReadFile wsA/task1/main.ts: %v", err)
	}
	if got == "" {
		t.Error("ReadFile returned empty for wsA/task1/main.ts")
	}

	// wsB/task1/main.ts must return ErrNotFound.
	_, err = wfs.ReadFile("wsB", "task1", "main.ts")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound for wsB/task1/main.ts, got: %v", err)
	}
}

// TestNewClientProcessReturnsClient verifies that NewClient("process", deps)
// no longer returns errUnsupported and produces a valid Client.
func TestNewClientProcessReturnsClient(t *testing.T) {
	root := t.TempDir()
	deps := buildDeps(t, root)

	c, err := supervisor.NewClient("process", deps)
	if err != nil {
		t.Fatalf("NewClient(\"process\"): %v", err)
	}
	if c == nil {
		t.Fatal("NewClient returned nil client")
	}
}

// TestEndpointUnknownTask verifies that Endpoint returns ok=false for a task
// that has no registered ports.
func TestEndpointUnknownTask(t *testing.T) {
	root := t.TempDir()
	deps := buildDeps(t, root)

	c, err := supervisor.NewClient("process", deps)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	_, _, ok := c.Endpoint("wsA", "nonexistent-task-id")
	if ok {
		t.Error("Endpoint should return ok=false for unknown task")
	}
}

// TestHealthAlwaysUp verifies that the process backend reports Up=true for any
// workspace (it is always resident — no per-workspace infrastructure to check).
func TestHealthAlwaysUp(t *testing.T) {
	root := t.TempDir()
	deps := buildDeps(t, root)

	c, err := supervisor.NewClient("process", deps)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	status := c.Health("wsA")
	if !status.Up {
		t.Errorf("Health(\"wsA\") = Up:%v, want Up:true", status.Up)
	}
}
