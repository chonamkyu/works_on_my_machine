package supervisor

// container.go — per-workspace OCI container backend for the supervisor.
//
// Model: one container per active workspace, named "platform-ws-<ws>".
// The container is created lazily (first EnsureService or StartRun call for
// that workspace) and can be removed via StopService or an explicit teardown.
//
// Image: configurable via PLATFORM_CONTAINER_IMAGE (default node:22-alpine).
// NOTE: the default node:22-alpine image is a placeholder; real workflow
// execution inside the container requires the @platform/sdk image — that is a
// deployment-time concern and is listed as a non-goal of this task. The
// container LIFECYCLE (create/inspect/stop/remove, port publishing, env
// injection) is fully implemented and tested here.
//
// Port publishing: each task's portreg bindings are published as host-port
// mappings (-p hostPort:containerPort) so the reverse-proxy can reach them
// on localhost just like the process backend.
//
// Workflow exec: StartRun creates a run record via store.Memory and directly
// execs "<runtime> exec -w <workflowDir> <container> node index.js" (no shell)
// to invoke the workflow entrypoint. When the SDK image is not the container
// image the exec will fail; the run is recorded as RunFailed with a clear message.

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/store"
)

const (
	defaultContainerImage = "docker.io/library/node:22-alpine"
	containerNamePrefix   = "platform-ws-"
)

// Container is the OCI-container runtime backend. It implements Client with
// one named container per workspace.
type Container struct {
	mu      sync.Mutex
	runtime string // resolved path to "podman" or "docker"
	image   string // base container image
	deps    Deps
	// cancelFuncs tracks cancel channels for active StartRun goroutines.
	// key: runID
	cancelFuncs map[string]chan struct{}
}

// newContainer detects the container runtime binary and constructs the backend.
// Preference order: PLATFORM_CONTAINER_RUNTIME env → podman → docker.
func newContainer(deps Deps) (*Container, error) {
	rt := os.Getenv("PLATFORM_CONTAINER_RUNTIME")
	if rt == "" {
		if p, err := exec.LookPath("podman"); err == nil {
			rt = p
		} else if d, err := exec.LookPath("docker"); err == nil {
			rt = d
		} else {
			return nil, fmt.Errorf("supervisor: no container runtime found (set PLATFORM_CONTAINER_RUNTIME or install podman/docker)")
		}
	} else {
		// Resolve the override to a full path so exec is unambiguous.
		if p, err := exec.LookPath(rt); err == nil {
			rt = p
		}
	}
	image := os.Getenv("PLATFORM_CONTAINER_IMAGE")
	if image == "" {
		image = defaultContainerImage
	}
	return &Container{
		runtime:     rt,
		image:       image,
		deps:        deps,
		cancelFuncs: make(map[string]chan struct{}),
	}, nil
}

// containerName returns the canonical container name for a workspace.
func containerName(ws string) string {
	return containerNamePrefix + ws
}

// runCmd executes a runtime subcommand and returns stdout.
func (c *Container) runCmd(args ...string) (string, error) {
	cmd := exec.Command(c.runtime, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// isContainerRunning returns true when the named container exists and is in
// the "running" state.
func (c *Container) isContainerRunning(name string) bool {
	out, err := c.runCmd("inspect", "--format", "{{.State.Status}}", name)
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "running"
}

// ensureWorkspaceContainer creates and starts the workspace container if it is
// not already running. Must be called with c.mu held.
func (c *Container) ensureWorkspaceContainer(ws string) error {
	name := containerName(ws)
	if c.isContainerRunning(name) {
		return nil
	}

	// Remove any stopped/exited container with the same name before re-creating.
	_, _ = c.runCmd("rm", "-f", name)

	// Build -p flags for currently registered task ports.
	portArgs := c.buildPortArgs()

	// Workflow directory mount.
	var mountArgs []string
	if c.deps.Files != nil {
		wsDir := c.deps.Files.Root + "/ws/" + ws + "/workflows"
		if err := os.MkdirAll(wsDir, 0o755); err == nil {
			mountArgs = []string{"-v", wsDir + ":/workspace/workflows:ro"}
		}
	}

	// Credential env vars.
	envArgs := c.buildCredEnvArgs(ws)

	// Build the full argument list.
	// `sleep infinity` keeps the container alive; actual workflow commands are
	// exec'd into it via `<runtime> exec`. Real SDK images override the
	// entrypoint; this default allows lifecycle tests with any small image.
	args := []string{
		"run", "-d",
		"--name", name,
		"--label", "platform=workspace",
		"--label", "platform.workspace=" + ws,
	}
	args = append(args, portArgs...)
	args = append(args, mountArgs...)
	args = append(args, envArgs...)
	args = append(args, c.image, "sleep", "infinity")

	if _, err := c.runCmd(args...); err != nil {
		return fmt.Errorf("supervisor: create container for ws %q: %w", ws, err)
	}
	return nil
}

// buildPortArgs returns -p 127.0.0.1:hostPort:hostPort flags for all
// currently registered task port bindings.
func (c *Container) buildPortArgs() []string {
	if c.deps.Ports == nil {
		return nil
	}
	bindings := c.deps.Ports.All()
	var args []string
	for _, b := range bindings {
		args = append(args, "-p", fmt.Sprintf("127.0.0.1:%d:%d", b.Port, b.Port))
	}
	return args
}

// buildCredEnvArgs returns -e PLATFORM_TOKEN_<CONNECTOR>=<value> for each
// active credential in the workspace.
func (c *Container) buildCredEnvArgs(ws string) []string {
	if c.deps.Creds == nil {
		return nil
	}
	creds := c.deps.Creds.List(ws)
	var args []string
	for _, cr := range creds {
		envKey := "PLATFORM_TOKEN_" + strings.ToUpper(strings.ReplaceAll(cr.Connector, "-", "_"))
		tok, _ := c.deps.Creds.Resolve(ws, cr.Connector, cr.Label)
		if tok != "" {
			args = append(args, "-e", envKey+"="+tok)
		}
	}
	return args
}

// removeWorkspaceContainer stops and removes the container for ws.
// Removal of an already-gone container is treated as success (idempotent).
func (c *Container) removeWorkspaceContainer(ws string) error {
	name := containerName(ws)
	if _, err := c.runCmd("rm", "-f", name); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no such") {
			return nil
		}
		return fmt.Errorf("supervisor: remove container for ws %q: %w", ws, err)
	}
	return nil
}

// containerInspect returns the raw inspect JSON for the named container.
// Used by tests.
func (c *Container) containerInspect(name string) (map[string]any, error) {
	out, err := c.runCmd("inspect", "--format", "json", name)
	if err != nil {
		return nil, err
	}
	var result []map[string]any
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no inspect data for %q", name)
	}
	return result[0], nil
}

// ---- Client interface -------------------------------------------------------

// EnsureService ensures the workspace container is running. The container
// backend uses one container per workspace; there is no per-task service.
func (c *Container) EnsureService(ws string, task *store.Task) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ensureWorkspaceContainer(ws)
}

// StopService stops and removes the workspace container.
// taskID is accepted for interface compatibility but unused.
func (c *Container) StopService(ws, taskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.removeWorkspaceContainer(ws)
}

// StartRun creates a run record and attempts to exec the workflow command
// inside the workspace container.
//
// NOTE: Full workflow execution requires the @platform/sdk image. When the
// container runs a generic image (e.g. node:22-alpine without the SDK) the
// exec will fail and the run will be recorded as RunFailed with a descriptive
// error. The container LIFECYCLE is still correctly exercised.
func (c *Container) StartRun(ws string, task *store.Task, trigger string) *store.Run {
	// Create the run record via store (if available) or build a local stub.
	var run *store.Run
	if c.deps.Store != nil {
		run = c.deps.Store.CreateRun(task.ID, trigger)
	} else {
		now := time.Now()
		run = &store.Run{
			ID:          "run_" + containerRandID(),
			TaskID:      task.ID,
			UserID:      task.UserID,
			WorkspaceID: ws,
			State:       store.RunQueued,
			Trigger:     trigger,
			StartedAt:   &now,
		}
	}

	// Ensure the workspace container is running before exec-ing into it.
	c.mu.Lock()
	ensureErr := c.ensureWorkspaceContainer(ws)
	c.mu.Unlock()
	if ensureErr != nil {
		return c.failRun(run, ensureErr)
	}

	// Workflow entrypoint: in a real SDK image this would be something like
	// `node /workspace/sdk/dist/runner.js --taskId <id>`. With a generic image
	// the command will likely fail — which is expected and documented.
	// Direct exec (no shell) avoids interpolating task.ID into a shell string.
	// Go's exec captures stdout+stderr directly; no 2>&1 redirection needed.
	scriptPath := "/workspace/workflows/" + task.ID + "/index.js"
	execArgs := []string{
		"exec", "-w", "/workspace/workflows/" + task.ID, containerName(ws),
		"node", scriptPath,
	}

	cancelCh := make(chan struct{})
	c.mu.Lock()
	c.cancelFuncs[run.ID] = cancelCh
	c.mu.Unlock()

	runID := run.ID
	go func() {
		cmd := exec.Command(c.runtime, execArgs...)
		var outBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &outBuf

		doneCh := make(chan error, 1)
		go func() { doneCh <- cmd.Run() }()

		select {
		case runErr := <-doneCh:
			c.mu.Lock()
			delete(c.cancelFuncs, runID)
			c.mu.Unlock()
			var finalState store.RunState
			var errMsg string
			if runErr != nil {
				finalState = store.RunFailed
				errMsg = fmt.Sprintf("exec: %v (NOTE: full workflow execution requires the platform SDK image)", runErr)
			} else {
				finalState = store.RunSuccess
			}
			output := outBuf.String()
			if c.deps.Store != nil {
				_, _ = c.deps.Store.UpdateRun(runID, func(r *store.Run) {
					now := time.Now()
					r.State = finalState
					r.FinishedAt = &now
					if r.StartedAt != nil {
						r.DurationMs = int(now.Sub(*r.StartedAt).Milliseconds())
					}
					r.Error = errMsg
					if output != "" {
						r.Output = output
					}
				})
			}
			if c.deps.Bus != nil {
				c.deps.Bus.Publish(store.Event{
					Channel: "run:" + runID,
					Payload: map[string]any{"state": string(finalState)},
				})
			}
		case <-cancelCh:
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			if c.deps.Store != nil {
				_, _ = c.deps.Store.UpdateRun(runID, func(r *store.Run) {
					now := time.Now()
					r.State = store.RunCancelled
					r.FinishedAt = &now
					if r.StartedAt != nil {
						r.DurationMs = int(now.Sub(*r.StartedAt).Milliseconds())
					}
				})
			}
			if c.deps.Bus != nil {
				c.deps.Bus.Publish(store.Event{
					Channel: "run:" + runID,
					Payload: map[string]any{"state": string(store.RunCancelled)},
				})
			}
		}
	}()

	return run
}

// CancelRun signals cancellation of runID. Returns true if the run was active.
func (c *Container) CancelRun(ws, runID string) bool {
	c.mu.Lock()
	ch, ok := c.cancelFuncs[runID]
	if ok {
		delete(c.cancelFuncs, runID)
	}
	c.mu.Unlock()
	if ok {
		close(ch)
	}
	return ok
}

// Endpoint returns the base URL and port map for a task running in ws.
// Port bindings are looked up from portreg, same as the process backend.
func (c *Container) Endpoint(ws, taskID string) (base string, ports map[string]int, ok bool) {
	if c.deps.Ports == nil {
		return "", nil, false
	}
	bindings := c.deps.Ports.ListForTask(taskID)
	if len(bindings) == 0 {
		return "", nil, false
	}
	portMap := make(map[string]int, len(bindings))
	for _, b := range bindings {
		portMap[b.Name] = b.Port
	}
	return "http://127.0.0.1", portMap, true
}

// Health returns whether the workspace container is running.
func (c *Container) Health(ws string) Status {
	name := containerName(ws)
	up := c.isContainerRunning(name)
	detail := ""
	if !up {
		detail = fmt.Sprintf("container %q is not running", name)
	}
	return Status{Up: up, Detail: detail}
}

// ServiceRunning reports whether the workspace container is up.
// All tasks share one container per workspace.
func (c *Container) ServiceRunning(ws, taskID string) bool {
	return c.isContainerRunning(containerName(ws))
}

// RestartService stops and re-creates the workspace container.
func (c *Container) RestartService(ws, taskID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.removeWorkspaceContainer(ws) // ignore: container may not exist
	return c.ensureWorkspaceContainer(ws)
}

// BootServices is a no-op. Containers are started lazily on first use.
func (c *Container) BootServices() error {
	return nil
}

// ---- helpers ----------------------------------------------------------------

// failRun transitions run to RunFailed via store update and returns it.
func (c *Container) failRun(run *store.Run, err error) *store.Run {
	if c.deps.Store != nil {
		_, _ = c.deps.Store.UpdateRun(run.ID, func(r *store.Run) {
			now := time.Now()
			r.State = store.RunFailed
			r.FinishedAt = &now
			r.Error = err.Error()
		})
	}
	run.State = store.RunFailed
	run.Error = err.Error()
	return run
}

// containerRandID returns a random short hex string for use in run IDs.
func containerRandID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%x", b)
}
