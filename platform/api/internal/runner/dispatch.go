package runner

import (
	"os"
	"path/filepath"

	"github.com/nk/platform/api/internal/store"
)

// Dispatcher picks between Node (if generated code exists on disk) and Mock
// (everything else — seed tasks and degraded fallback).
type Dispatcher struct {
	files *store.WorkflowFS
	node  *NodeRunner
	mock  *MockRunner
}

func NewDispatcher(files *store.WorkflowFS, node *NodeRunner, mock *MockRunner) *Dispatcher {
	return &Dispatcher{files: files, node: node, mock: mock}
}

func (d *Dispatcher) hasCode(task *store.Task) bool {
	_, err := os.Stat(filepath.Join(d.files.Dir(task.WorkspaceID, task.ID), "main.ts"))
	return err == nil
}

func (d *Dispatcher) pick(task *store.Task) Runner {
	if d.hasCode(task) {
		return d.node
	}
	return d.mock
}

func (d *Dispatcher) Start(task *store.Task, trigger string) *store.Run {
	return d.pick(task).Start(task, trigger)
}

func (d *Dispatcher) Cancel(runID string) bool {
	// Try both — only one will have a matching cancel handle.
	if d.node.Cancel(runID) {
		return true
	}
	return d.mock.Cancel(runID)
}
