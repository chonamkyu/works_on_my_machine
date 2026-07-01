package supervisor

import (
	"github.com/nk/platform/api/internal/runner"
	"github.com/nk/platform/api/internal/store"
)

// Process is the "process" runtime backend: it spawns workflow tasks as
// Node.js child processes on the local machine, one process per run/service.
// Per-workspace isolation is achieved by the WorkflowFS path layout:
//
//	<Root>/ws/<workspaceID>/workflows/<taskID>/
//
// There is no per-workspace infrastructure to boot or tear down; the backend
// is always resident, so Health always returns Up:true.
type Process struct {
	node     *runner.NodeRunner
	services *runner.ServiceManager
	dispatch *runner.Dispatcher
	deps     Deps
}

// newProcess constructs a Process backend from the supplied Deps. It builds a
// NodeRunner and a ServiceManager internally so that the caller does not need
// to wire them separately.
func newProcess(deps Deps) *Process {
	mock := runner.NewMock(deps.Store, deps.Bus)
	node := runner.NewNode(
		deps.Store,
		deps.Bus,
		deps.Files,
		deps.SDKPath,
		deps.Hook,
		deps.Creds,
		deps.Ports,
	)
	var svcMgr *runner.ServiceManager
	if deps.Store != nil && deps.Bus != nil {
		svcMgr = runner.NewServiceManager(deps.Store, node, deps.Bus, deps.Hook, deps.Ports)
	}
	dispatch := runner.NewDispatcher(deps.Files, node, mock)
	return &Process{
		node:     node,
		services: svcMgr,
		dispatch: dispatch,
		deps:     deps,
	}
}

// StartRun spawns a new run for task in workspace ws. The task already carries
// task.WorkspaceID; ws is a redundant cross-check that callers can omit if
// they trust the task object.
func (p *Process) StartRun(ws string, task *store.Task, trigger string) *store.Run {
	return p.dispatch.Start(task, trigger)
}

// CancelRun signals cancellation of the named run. Returns true if a cancel
// handle was found and fired.
func (p *Process) CancelRun(ws, runID string) bool {
	return p.dispatch.Cancel(runID)
}

// StopService tears down the long-running service daemon for taskID.
func (p *Process) StopService(ws, taskID string) error {
	if p.services == nil {
		return nil
	}
	p.services.Restart(taskID) // Restart cancels the current proc; reconcile re-evaluates
	return nil
}

// EnsureService starts (or restarts) the service daemon for task. The
// ServiceManager's reconcile loop handles the actual lifecycle; here we just
// poke it if the task is already tracked.
func (p *Process) EnsureService(ws string, task *store.Task) error {
	if p.services == nil {
		return nil
	}
	p.services.Restart(task.ID)
	return nil
}

// Endpoint returns the base URL and port map for a task's running surfaces.
// ok is false if the task has no registered bindings (i.e. it is not currently
// running or has not declared any ports).
func (p *Process) Endpoint(ws, taskID string) (base string, ports map[string]int, ok bool) {
	if p.deps.Ports == nil {
		return "", nil, false
	}
	bindings := p.deps.Ports.ListForTask(taskID)
	if len(bindings) == 0 {
		return "", nil, false
	}
	portMap := make(map[string]int, len(bindings))
	for _, b := range bindings {
		portMap[b.Name] = b.Port
	}
	return "http://127.0.0.1", portMap, true
}

// Health always returns Up:true for the process backend — it is a resident
// component of the API server; there is no per-workspace infrastructure to
// probe.
func (p *Process) Health(ws string) Status {
	return Status{Up: true}
}

// ServiceRunning reports whether a live daemon is currently running for
// taskID in workspace ws.
func (p *Process) ServiceRunning(ws, taskID string) bool {
	if p.services == nil {
		return false
	}
	return p.services.IsRunning(taskID)
}

// RestartService bounces the service daemon for taskID in workspace ws.
func (p *Process) RestartService(ws, taskID string) error {
	if p.services == nil {
		return nil
	}
	p.services.Restart(taskID)
	return nil
}

// BootServices starts all kind=service tasks across all workspaces. It
// delegates to the ServiceManager's Start method, which reconciles the set of
// enabled services on the first tick.
func (p *Process) BootServices() error {
	if p.services != nil {
		p.services.Start()
	}
	return nil
}
