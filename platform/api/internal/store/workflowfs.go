package store

import (
	"errors"
	"os"
	"path/filepath"
)

// WorkflowFS persists workflow source files on disk. Each workspace gets its
// own sub-tree so that files from different workspaces never share a directory:
//
//	<Root>/ws/<workspaceID>/workflows/<taskID>/
//
// Phase 1 used a flat Root/taskID layout; Phase 2 (this file) adds the
// per-workspace isolation required by multi-tenant deployments.
type WorkflowFS struct {
	Root string
}

func NewWorkflowFS(root string) (*WorkflowFS, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &WorkflowFS{Root: root}, nil
}

// Dir returns the on-disk directory for a (workspace, task) pair.
func (w *WorkflowFS) Dir(workspaceID, taskID string) string {
	return filepath.Join(w.Root, "ws", workspaceID, "workflows", taskID)
}

// WriteFiles persists the supplied map of filename -> contents under the
// task directory, creating it if needed. Existing files are overwritten.
func (w *WorkflowFS) WriteFiles(workspaceID, taskID string, files map[string]string) error {
	dir := w.Dir(workspaceID, taskID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (w *WorkflowFS) ReadFile(workspaceID, taskID, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(w.Dir(workspaceID, taskID), name))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}
