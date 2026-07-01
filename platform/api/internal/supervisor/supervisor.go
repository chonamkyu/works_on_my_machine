// Package supervisor defines the control-plane-facing seam through which the
// API delegates workflow execution to per-workspace runtimes. A2 backends
// ("process", "container", "k8s") each implement the Client interface; the
// factory NewClient selects the appropriate backend based on the runtime kind
// string (typically sourced from PLATFORM_RUNTIME).
//
// The existing runner.Runner interface (Start/Cancel) is preserved and will be
// adapted by the "process" backend in a later task. Client is the new,
// workspace-aware interface that the control plane uses going forward.
package supervisor

import (
	"fmt"

	"github.com/nk/platform/api/internal/portreg"
	"github.com/nk/platform/api/internal/runner"
	"github.com/nk/platform/api/internal/store"
)

// Status reports the liveness of a workspace runtime.
type Status struct {
	Up     bool
	Detail string
}

// Client is the control-plane-facing interface that every A2 runtime backend
// must implement. Methods are workspace-scoped: ws identifies the workspace
// whose runtime should handle the call.
type Client interface {
	// StartRun enqueues or spawns a new run for the given task in workspace ws.
	StartRun(ws string, task *store.Task, trigger string) *store.Run

	// CancelRun signals cancellation of runID inside workspace ws.
	// Returns true if the run was found and the signal was delivered.
	CancelRun(ws, runID string) bool

	// StopService stops the long-running service for taskID in workspace ws.
	StopService(ws, taskID string) error

	// EnsureService starts (or re-starts) the long-running service for task
	// in workspace ws, doing nothing if it is already healthy.
	EnsureService(ws string, task *store.Task) error

	// Endpoint returns the base URL and allocated port map for a task's
	// running surfaces, used by the reverse-proxy. ok is false if the task
	// is not currently running or has no exposed ports.
	Endpoint(ws, taskID string) (base string, ports map[string]int, ok bool)

	// Health returns the liveness of the runtime for workspace ws.
	Health(ws string) Status

	// ServiceRunning reports whether a live daemon is currently running for
	// taskID in workspace ws.
	ServiceRunning(ws, taskID string) bool

	// RestartService bounces a single service daemon (e.g. after accepting a
	// code evolution PR).
	RestartService(ws, taskID string) error

	// BootServices starts all kind=service tasks across all workspaces.
	// Called once at API boot.
	BootServices() error

	// ReapIdle stops workspace runtimes that are idle: no enabled service task
	// and no active (queued/running) run. The process backend is always
	// resident and returns nil immediately. The container and k8s backends stop
	// or delete the relevant runtime objects.
	ReapIdle() error
}

// StoreClient is the subset of store.Memory that the supervisor backends
// require. Using an interface rather than the concrete pointer allows tests to
// inject lightweight fakes without spinning up a real Postgres database.
type StoreClient interface {
	CreateRun(taskID, trigger string) *store.Run
	UpdateRun(id string, mutate func(*store.Run)) (*store.Run, error)
	ListServiceTasks() []*store.Task
	// WorkspacesWithActiveRuns returns workspace IDs with an in-flight
	// (queued/running) run. The idle reaper keeps these workspaces warm so a
	// run is never killed mid-execution.
	WorkspacesWithActiveRuns() (map[string]bool, error)
}

// Deps carries the shared dependencies that runtime backends need. It is a
// superset; each backend picks what it requires.
//
// Fields mirror the parameters accepted by runner.NewNode so that the
// "process" backend can forward them directly without an additional indirection
// layer.
type Deps struct {
	Store   StoreClient
	Bus     *store.Bus
	Files   *store.WorkflowFS
	Creds   *store.CredentialStore
	Ports   *portreg.Registry
	SDKPath string // absolute path to the built @platform/sdk directory
	Node    string // node binary path; empty means "node" on $PATH
	Hook    runner.PostRunHook
}

// NewClient constructs the Client for the named runtime kind. Accepted kinds:
//
//   - "process"   — per-workspace Node child processes (Phase 2 of runner)
//   - "container" — per-workspace OCI containers via Podman/Docker
//   - "k8s"       — per-workspace Kubernetes pods
//
// All three backends are implemented. An unknown kind returns an error.
func NewClient(kind string, deps Deps) (Client, error) {
	switch kind {
	case "process":
		return newProcess(deps), nil
	case "container":
		return newContainer(deps)
	case "k8s":
		return newK8s(deps)
	default:
		return nil, fmt.Errorf("supervisor: unknown runtime kind %q", kind)
	}
}
