package runner

import "github.com/nk/platform/api/internal/store"

// Runner abstracts over the concrete way a workflow run is executed.
//
// - MockRunner simulates a run for tasks without generated code (legacy seeds).
// - NodeRunner spawns the actual workflow as a Node child process.
//
// The control plane picks between them per-task based on whether the
// generator has produced files.
type Runner interface {
	Start(task *store.Task, trigger string) *store.Run
	Cancel(runID string) bool
}
