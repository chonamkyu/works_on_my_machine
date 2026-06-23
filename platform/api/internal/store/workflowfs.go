package store

import (
	"errors"
	"os"
	"path/filepath"
)

// WorkflowFS persists workflow source files on disk. Each task gets its own
// directory keyed by task id. Phase 1 uses a flat git working tree per task;
// Phase 2 will swap this for a per-user bare repo with proper `active` refs.
type WorkflowFS struct {
	Root string
}

func NewWorkflowFS(root string) (*WorkflowFS, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &WorkflowFS{Root: root}, nil
}

func (w *WorkflowFS) Dir(taskID string) string {
	return filepath.Join(w.Root, taskID)
}

// WriteFiles persists the supplied map of filename -> contents under the
// task directory, creating it if needed. Existing files are overwritten.
func (w *WorkflowFS) WriteFiles(taskID string, files map[string]string) error {
	dir := w.Dir(taskID)
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

func (w *WorkflowFS) ReadFile(taskID, name string) (string, error) {
	b, err := os.ReadFile(filepath.Join(w.Dir(taskID), name))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}
