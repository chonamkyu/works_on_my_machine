package supervisor_test

import (
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nk/platform/api/internal/portreg"
	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/supervisor"
)

// findRuntime returns the first available container runtime binary path,
// or ("", false) if neither podman nor docker is on PATH.
func findRuntime() (string, bool) {
	if p, err := exec.LookPath("podman"); err == nil {
		return p, true
	}
	if p, err := exec.LookPath("docker"); err == nil {
		return p, true
	}
	return "", false
}

// skipIfNoRuntime skips the test when no container runtime is available.
func skipIfNoRuntime(t *testing.T) string {
	t.Helper()
	rt, ok := findRuntime()
	if !ok {
		t.Skip("no container runtime (podman/docker) found on PATH; skipping container lifecycle test")
	}
	return rt
}

// imageAvailable checks whether the image is present in the local image store.
func imageAvailable(rt, image string) bool {
	cmd := exec.Command(rt, "image", "exists", image)
	return cmd.Run() == nil
}

// preferredImage returns an image already present in the local image store so
// the test never needs to pull (which would be slow). Priority: ubuntu:24.04,
// then ubuntu:20.04, then alpine.
func preferredImage(rt string) (string, bool) {
	candidates := []string{
		"docker.io/library/ubuntu:24.04",
		"ubuntu:24.04",
		"docker.io/library/ubuntu:20.04",
		"ubuntu:20.04",
		"docker.io/library/alpine:latest",
		"alpine:latest",
	}
	for _, img := range candidates {
		if imageAvailable(rt, img) {
			return img, true
		}
	}
	return "", false
}

// testRandID returns a random short hex string for unique test identifiers.
func testRandID() string {
	//nolint:gosec // test-only, not security-sensitive
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%08x", r.Uint32())
}

// TestContainerLifecycle exercises the full per-workspace container lifecycle:
//  1. EnsureService → container "platform-ws-<ws>" appears in `podman ps`.
//  2. Health(ws) → Up: true.
//  3. ServiceRunning(ws, taskID) → true.
//  4. Endpoint after port allocation → ok: true with correct port.
//  5. EnsureService is idempotent (no error on second call).
//  6. StopService → container is gone; Health → Down; ServiceRunning → false.
func TestContainerLifecycle(t *testing.T) {
	rt := skipIfNoRuntime(t)

	image, ok := preferredImage(rt)
	if !ok {
		t.Skip("no suitable local image found (ubuntu:24.04 / alpine); skipping to avoid slow image pull")
	}

	t.Setenv("PLATFORM_CONTAINER_IMAGE", image)

	ws := "test-lc-" + testRandID()
	taskID := "task-" + testRandID()
	containerNameExpected := "platform-ws-" + ws

	root := t.TempDir()
	wfs, err := store.NewWorkflowFS(root)
	if err != nil {
		t.Fatalf("NewWorkflowFS: %v", err)
	}
	ports := portreg.New()

	deps := supervisor.Deps{
		Store:   nil,
		Bus:     store.NewBus(),
		Files:   wfs,
		Creds:   nil,
		Ports:   ports,
		SDKPath: "",
		Node:    "",
	}

	c, err := supervisor.NewClient("container", deps)
	if err != nil {
		t.Fatalf("NewClient(\"container\"): %v", err)
	}

	// Always clean up the container, even on test failure.
	t.Cleanup(func() {
		out, rmErr := exec.Command(rt, "rm", "-f", containerNameExpected).CombinedOutput()
		if rmErr != nil {
			outStr := string(out)
			// Suppress "no such container" — that's fine, it means cleanup succeeded.
			if !strings.Contains(strings.ToLower(outStr), "no such") {
				t.Logf("cleanup rm %s: %v (%s)", containerNameExpected, rmErr, outStr)
			}
		}
	})

	task := &store.Task{
		ID:          taskID,
		UserID:      "user-test",
		WorkspaceID: ws,
		Kind:        "service",
	}

	// 1. EnsureService creates the workspace container.
	if err := c.EnsureService(ws, task); err != nil {
		t.Fatalf("EnsureService: %v", err)
	}

	// Verify container appears in `<runtime> ps --format {{.Names}}`.
	psOut, err := exec.Command(rt, "ps", "--format", "{{.Names}}").CombinedOutput()
	if err != nil {
		t.Fatalf("%s ps: %v (%s)", rt, err, psOut)
	}
	if !strings.Contains(string(psOut), containerNameExpected) {
		t.Errorf("container %q not found in ps output:\n%s", containerNameExpected, psOut)
	}

	// 2. Health reports Up.
	status := c.Health(ws)
	if !status.Up {
		t.Errorf("Health(%q) = Up:false after EnsureService; detail: %q", ws, status.Detail)
	}

	// 3. ServiceRunning returns true.
	if !c.ServiceRunning(ws, taskID) {
		t.Errorf("ServiceRunning(%q, %q) = false, expected true", ws, taskID)
	}

	// 4. Endpoint after port allocation.
	allocPort, err := ports.AllocateNamed(taskID, "ui", "ui")
	if err != nil {
		t.Fatalf("AllocateNamed: %v", err)
	}
	base, portMap, endpointOK := c.Endpoint(ws, taskID)
	if !endpointOK {
		t.Errorf("Endpoint(%q, %q) = ok:false after port allocation", ws, taskID)
	}
	if base != "http://127.0.0.1" {
		t.Errorf("Endpoint base = %q, want %q", base, "http://127.0.0.1")
	}
	if portMap["ui"] != allocPort {
		t.Errorf("Endpoint portMap[\"ui\"] = %d, want %d", portMap["ui"], allocPort)
	}

	// 5. EnsureService is idempotent.
	if err := c.EnsureService(ws, task); err != nil {
		t.Errorf("second EnsureService (idempotent check) failed: %v", err)
	}

	// 6. StopService removes the container.
	if err := c.StopService(ws, taskID); err != nil {
		t.Fatalf("StopService: %v", err)
	}

	// Give the runtime a moment to reflect the removal.
	time.Sleep(300 * time.Millisecond)

	// Container must be gone from `ps -a`.
	psAllOut, _ := exec.Command(rt, "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	if strings.Contains(string(psAllOut), containerNameExpected) {
		t.Errorf("container %q still present after StopService; ps -a:\n%s",
			containerNameExpected, psAllOut)
	}

	// Health must be Down.
	statusAfter := c.Health(ws)
	if statusAfter.Up {
		t.Errorf("Health(%q) = Up:true after StopService, expected false", ws)
	}

	// ServiceRunning must be false.
	if c.ServiceRunning(ws, taskID) {
		t.Errorf("ServiceRunning(%q, %q) = true after StopService, expected false", ws, taskID)
	}
}

// TestContainerNewClientReturnsClient is a smoke test: NewClient("container")
// returns a non-nil Client (requires runtime to be on PATH for factory wiring).
func TestContainerNewClientReturnsClient(t *testing.T) {
	skipIfNoRuntime(t)

	root := t.TempDir()
	deps := buildDeps(t, root)

	c, err := supervisor.NewClient("container", deps)
	if err != nil {
		t.Fatalf("NewClient(\"container\"): %v", err)
	}
	if c == nil {
		t.Fatal("NewClient(\"container\") returned nil")
	}
}

// TestContainerEndpointNoPort verifies Endpoint returns ok=false for an
// unregistered task (no runtime interaction needed).
func TestContainerEndpointNoPort(t *testing.T) {
	skipIfNoRuntime(t)

	root := t.TempDir()
	deps := buildDeps(t, root)

	c, err := supervisor.NewClient("container", deps)
	if err != nil {
		t.Fatalf("NewClient(\"container\"): %v", err)
	}

	_, _, ok := c.Endpoint("some-ws", "no-such-task")
	if ok {
		t.Error("Endpoint should return ok=false for task with no registered ports")
	}
}

// TestContainerHealthDownWhenNoContainer verifies Health returns Up:false when
// no container exists for the workspace.
func TestContainerHealthDownWhenNoContainer(t *testing.T) {
	skipIfNoRuntime(t)

	root := t.TempDir()
	deps := buildDeps(t, root)

	c, err := supervisor.NewClient("container", deps)
	if err != nil {
		t.Fatalf("NewClient(\"container\"): %v", err)
	}

	status := c.Health("nonexistent-ws-" + testRandID())
	if status.Up {
		t.Error("Health should return Up:false for workspace with no container")
	}
}

// TestContainerReapIdle_PreservesWorkspaceWithActiveRun verifies that ReapIdle
// keeps a workspace container alive when that workspace has an in-flight run,
// even though it has no service task, while reaping a truly idle workspace.
// Requires a container runtime (podman/docker) and a small local image.
func TestContainerReapIdle_PreservesWorkspaceWithActiveRun(t *testing.T) {
	rt := skipIfNoRuntime(t)

	image, ok := preferredImage(rt)
	if !ok {
		t.Skip("no suitable local image found (ubuntu:24.04 / alpine); skipping to avoid slow image pull")
	}
	t.Setenv("PLATFORM_CONTAINER_IMAGE", image)

	wsRunning := "reap-run-" + testRandID() // has an active run, no service task
	wsIdle := "reap-idle-" + testRandID()   // neither — must be reaped

	// Store: no service tasks, but wsRunning has an active run.
	fakeStore := &fakeServiceStore{
		tasks:      nil,
		activeRuns: map[string]bool{wsRunning: true},
	}

	deps := supervisor.Deps{Store: fakeStore}
	c, err := supervisor.NewClient("container", deps)
	if err != nil {
		t.Fatalf("NewClient(\"container\"): %v", err)
	}

	nameRunning := "platform-ws-" + wsRunning
	nameIdle := "platform-ws-" + wsIdle
	t.Cleanup(func() {
		_ = exec.Command(rt, "rm", "-f", nameRunning).Run()
		_ = exec.Command(rt, "rm", "-f", nameIdle).Run()
	})

	// Provision both workspace containers.
	if err := c.EnsureService(wsRunning, &store.Task{ID: "t-run", WorkspaceID: wsRunning}); err != nil {
		t.Fatalf("EnsureService running: %v", err)
	}
	if err := c.EnsureService(wsIdle, &store.Task{ID: "t-idle", WorkspaceID: wsIdle}); err != nil {
		t.Fatalf("EnsureService idle: %v", err)
	}

	if err := c.ReapIdle(); err != nil {
		t.Fatalf("ReapIdle: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	psOut, _ := exec.Command(rt, "ps", "-a", "--format", "{{.Names}}").CombinedOutput()
	ps := string(psOut)

	// Workspace with an active run must survive.
	if !strings.Contains(ps, nameRunning) {
		t.Errorf("ReapIdle removed container %q for a workspace with an active run; ps -a:\n%s", nameRunning, ps)
	}
	// Truly idle workspace must be reaped.
	if strings.Contains(ps, nameIdle) {
		t.Errorf("ReapIdle left idle container %q; ps -a:\n%s", nameIdle, ps)
	}
}
