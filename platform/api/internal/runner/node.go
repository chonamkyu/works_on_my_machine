package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/portreg"
	"github.com/nk/platform/api/internal/store"
)

// NodeRunner runs a generated workflow as a Node.js child process.
//
// Phase 1: spawn directly from the control plane. Each task's workflow tree
// lives at ~/.platform/workflows/<task_id>/ and a one-time setup creates
// node_modules/@platform/sdk as a symlink to the built SDK so the workflow's
// bare imports resolve.
//
// Phase 2 will move this behind the supervisor in a per-user pod.
type NodeRunner struct {
	store   *store.Memory
	bus     *store.Bus
	files   *store.WorkflowFS
	sdkPath string
	node    string
	hook    PostRunHook
	creds   *store.CredentialStore
	ports   *portreg.Registry

	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

func NewNode(s *store.Memory, b *store.Bus, files *store.WorkflowFS, sdkPath string, hook PostRunHook, creds *store.CredentialStore, ports *portreg.Registry) *NodeRunner {
	return &NodeRunner{
		store:   s,
		bus:     b,
		files:   files,
		sdkPath: sdkPath,
		node:    nodeBinary(),
		hook:    hook,
		creds:   creds,
		ports:   ports,
		cancel:  map[string]context.CancelFunc{},
	}
}

func nodeBinary() string {
	if v := os.Getenv("PLATFORM_NODE"); v != "" {
		return v
	}
	return "node"
}

// Start launches a run synchronously up to the point of spawning; the
// process runs asynchronously and emits state/log events via the bus.
func (r *NodeRunner) Start(task *store.Task, trigger string) *store.Run {
	run := r.store.CreateRun(task.ID, trigger)
	ctx, cancel := context.WithCancel(context.Background())

	r.mu.Lock()
	r.cancel[run.ID] = cancel
	r.mu.Unlock()

	r.bus.Publish(store.Event{
		Channel: "trigger:fired:" + task.ID,
		UserID:  task.UserID,
		TaskID:  task.ID,
		RunID:   run.ID,
		Payload: map[string]any{"trigger": trigger},
	})

	go r.execute(ctx, task, run)
	return run
}

// RunForeground spawns the workflow synchronously (used by the service
// supervisor). It returns when the process exits or the context is
// cancelled; in both cases the run row is closed out. State/log fan-out is
// identical to Start().
func (r *NodeRunner) RunForeground(ctx context.Context, task *store.Task, trigger string) error {
	run := r.store.CreateRun(task.ID, trigger)
	r.bus.Publish(store.Event{
		Channel: "trigger:fired:" + task.ID,
		UserID:  task.UserID,
		TaskID:  task.ID,
		RunID:   run.ID,
		Payload: map[string]any{"trigger": trigger},
	})
	r.execute(ctx, task, run)
	rr, _ := r.store.GetRun(run.ID)
	if rr != nil && rr.State == store.RunFailed {
		return fmt.Errorf("run %s failed: %s", run.ID, rr.Error)
	}
	return nil
}

func (r *NodeRunner) Cancel(runID string) bool {
	r.mu.Lock()
	cancel, ok := r.cancel[runID]
	delete(r.cancel, runID)
	r.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// DryRunResult is the structured outcome of a smoke-run with mocked
// connectors. ExitCode == 0 and empty FatalError means the workflow at
// least loaded + reached its first awaited boundary without crashing.
type DryRunResult struct {
	OK         bool     `json:"ok"`
	ExitCode   int      `json:"exitCode"`
	FatalError string   `json:"fatalError,omitempty"`
	Stdout     []string `json:"stdout,omitempty"`
	Stderr     []string `json:"stderr,omitempty"`
	DurationMs int      `json:"durationMs"`
	TimedOut   bool     `json:"timedOut,omitempty"`
}

// RunDry spawns the workflow with PLATFORM_DRY_RUN=1 so SDK connectors return
// mocks. The point is to catch hallucinated SDK methods, missing imports,
// syntax errors, undefined variables — anything that would fail at load or
// in the first awaited tick. The workflow is killed after a tight budget
// (default 20s) since a healthy oneshot exits fast under mocks, and a
// healthy service binds + parks (which we treat as "ok" once stable).
func (r *NodeRunner) RunDry(parent context.Context, task *store.Task, budget time.Duration) DryRunResult {
	if budget == 0 {
		budget = 20 * time.Second
	}
	res := DryRunResult{}
	start := time.Now()
	defer func() { res.DurationMs = int(time.Since(start).Milliseconds()) }()

	dir := r.files.Dir(task.WorkspaceID, task.ID)
	if _, err := os.Stat(filepath.Join(dir, "main.ts")); err != nil {
		res.FatalError = fmt.Sprintf("no main.ts: %v", err)
		return res
	}
	if err := r.ensureSDKSymlink(dir); err != nil {
		res.FatalError = fmt.Sprintf("sdk link: %v", err)
		return res
	}
	if err := r.writeLauncher(dir); err != nil {
		res.FatalError = fmt.Sprintf("launcher: %v", err)
		return res
	}
	if err := r.installDeps(parent, task, nil, dir); err != nil {
		res.FatalError = fmt.Sprintf("install deps: %v", err)
		return res
	}

	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()

	args := []string{"--experimental-strip-types", "--no-warnings", "_launcher.mjs"}
	cmd := exec.CommandContext(ctx, r.node, args...)
	cmd.Dir = dir
	// Build env WITHOUT allocating a real loopback port — the actual service
	// daemon already owns that port. Dry-run uses a one-off mock port so the
	// SDK's dashboard.serve() can bind on its own ephemeral socket.
	env := os.Environ()
	env = append(env,
		"PLATFORM_RUN_ID=dry-run",
		"PLATFORM_TASK_ID="+task.ID,
		"PLATFORM_USER_ID="+task.UserID,
		"PLATFORM_DRY_RUN=1",
	)
	if task.Kind == "service" {
		// Pick a free port so dashboard.serve() succeeds during dry-run.
		// We don't reserve it in portreg — it's released as soon as the
		// dry-run process exits.
		if p, err := pickFreePortLocal(); err == nil {
			env = append(env, fmt.Sprintf("PLATFORM_PORT=%d", p))
		}
	}
	cmd.Env = env

	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		res.FatalError = fmt.Sprintf("spawn: %v", err)
		return res
	}

	// Read up to ~200 lines each side so we have enough for diagnosis but
	// don't unbounded-buffer if the workflow is chatty.
	res.Stdout = readLines(stdoutPipe, 200)
	res.Stderr = readLines(stderrPipe, 200)

	waitErr := cmd.Wait()
	res.ExitCode = -1
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
		}
	} else {
		res.ExitCode = 0
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.TimedOut = true
		// Service-style workflows are SUPPOSED to park forever — treat
		// "timed out + no fatal stderr signals" as a soft success.
		if task.Kind == "service" && !stderrHasFatal(res.Stderr) {
			res.OK = true
			return res
		}
		res.FatalError = "dry-run timed out without exiting"
		return res
	}
	if stderrHasFatal(res.Stderr) {
		res.FatalError = firstFatal(res.Stderr)
		return res
	}
	res.OK = res.ExitCode == 0
	return res
}

// stderrHasFatal scans for runtime errors that almost always mean broken
// generated code (vs. a logged warning).
func stderrHasFatal(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "TypeError:") ||
			strings.Contains(l, "ReferenceError:") ||
			strings.Contains(l, "SyntaxError:") ||
			strings.Contains(l, "Cannot find module") ||
			strings.Contains(l, "Cannot read prop") ||
			strings.Contains(l, "is not a function") {
			return true
		}
	}
	return false
}

func firstFatal(lines []string) string {
	for _, l := range lines {
		if strings.Contains(l, "Error:") || strings.Contains(l, "is not a function") {
			return strings.TrimSpace(l)[:min(300, len(l))]
		}
	}
	return ""
}

func readLines(rc io.ReadCloser, max int) []string {
	out := []string{}
	scan := bufio.NewScanner(rc)
	scan.Buffer(make([]byte, 0, 65536), 1<<20)
	for scan.Scan() {
		if len(out) >= max {
			continue
		}
		out = append(out, scan.Text())
	}
	return out
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// pickFreePortLocal asks the OS for an ephemeral TCP port. Used by dry-run
// only — portreg.pickFreePort is package-private so we keep a small
// duplicate here rather than exporting it.
func pickFreePortLocal() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func (r *NodeRunner) execute(ctx context.Context, task *store.Task, run *store.Run) {
	dir := r.files.Dir(task.WorkspaceID, task.ID)
	if _, err := os.Stat(filepath.Join(dir, "main.ts")); err != nil {
		r.fail(task, run, fmt.Sprintf("no main.ts: %v", err))
		return
	}

	if err := r.ensureSDKSymlink(dir); err != nil {
		r.fail(task, run, fmt.Sprintf("sdk link: %v", err))
		return
	}
	if err := r.writeLauncher(dir); err != nil {
		r.fail(task, run, fmt.Sprintf("launcher: %v", err))
		return
	}
	if err := r.installDeps(ctx, task, run, dir); err != nil {
		r.fail(task, run, fmt.Sprintf("install deps: %v", err))
		return
	}

	r.transition(task, run, store.RunRunning, func(rr *store.Run) {
		now := time.Now()
		rr.StartedAt = &now
	})

	args := []string{"--experimental-strip-types", "--no-warnings", "_launcher.mjs"}
	cmd := exec.CommandContext(ctx, r.node, args...)
	cmd.Dir = dir
	cmd.Env = r.childEnv(task, run)

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		r.fail(task, run, fmt.Sprintf("spawn: %v", err))
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); r.pipe(task, run, "stdout", stdout) }()
	go func() { defer wg.Done(); r.pipe(task, run, "stderr", stderr) }()

	waitErr := cmd.Wait()
	wg.Wait()

	r.mu.Lock()
	delete(r.cancel, run.ID)
	r.mu.Unlock()

	if ctx.Err() == context.Canceled {
		r.transition(task, run, store.RunCancelled, func(rr *store.Run) {
			now := time.Now()
			rr.FinishedAt = &now
			if rr.StartedAt != nil {
				rr.DurationMs = int(now.Sub(*rr.StartedAt).Milliseconds())
			}
			rr.Error = "cancelled"
		})
		r.afterTerminal(task, run)
		return
	}

	if waitErr != nil {
		r.transition(task, run, store.RunFailed, func(rr *store.Run) {
			now := time.Now()
			rr.FinishedAt = &now
			if rr.StartedAt != nil {
				rr.DurationMs = int(now.Sub(*rr.StartedAt).Milliseconds())
			}
			var exitErr *exec.ExitError
			if errors.As(waitErr, &exitErr) {
				rr.Error = fmt.Sprintf("exit %d", exitErr.ExitCode())
			} else {
				rr.Error = waitErr.Error()
			}
		})
		r.afterTerminal(task, run)
		return
	}

	r.transition(task, run, store.RunSuccess, func(rr *store.Run) {
		now := time.Now()
		rr.FinishedAt = &now
		if rr.StartedAt != nil {
			rr.DurationMs = int(now.Sub(*rr.StartedAt).Milliseconds())
		}
	})
	r.afterTerminal(task, run)
}

func (r *NodeRunner) afterTerminal(task *store.Task, run *store.Run) {
	if r.hook == nil {
		return
	}
	go func() {
		if updated, err := r.store.GetRun(run.ID); err == nil {
			r.hook(task, updated)
		}
	}()
}

func (r *NodeRunner) childEnv(task *store.Task, run *store.Run) []string {
	env := os.Environ()
	runID := "dry-run"
	if run != nil {
		runID = run.ID
	}
	env = append(env,
		"PLATFORM_RUN_ID="+runID,
		"PLATFORM_TASK_ID="+task.ID,
		"PLATFORM_USER_ID="+task.UserID,
	)
	// Service workflows expose one or more named HTTP surfaces (UI, API, …).
	// Allocate a loopback port per declared TaskPort and inject as
	// `PLATFORM_PORT_<NAME_UPPER>=<n>`. The legacy `PLATFORM_PORT` env stays
	// pointing at the role=="ui" port so existing one-port workflows keep
	// working unchanged.
	if r.ports != nil && task.Kind == "service" {
		declared := task.Ports
		if len(declared) == 0 {
			// Implicit single UI port for tasks that haven't declared any —
			// matches the pre-multi-port behavior exactly.
			declared = []store.TaskPort{{Name: "ui", Role: "ui"}}
		}
		var uiPort int
		for _, p := range declared {
			port, err := r.ports.AllocateNamed(task.ID, p.Name, p.Role)
			if err != nil {
				continue
			}
			envName := "PLATFORM_PORT_" + strings.ToUpper(strings.ReplaceAll(p.Name, "-", "_"))
			env = append(env, fmt.Sprintf("%s=%d", envName, port))
			if p.Role == "ui" || (uiPort == 0 && p.Name == "ui") {
				uiPort = port
			}
		}
		if uiPort != 0 {
			env = append(env, fmt.Sprintf("PLATFORM_PORT=%d", uiPort))
		}
	}
	if r.creds != nil {
		for _, conn := range task.Capabilities {
			_, val, primary, err := r.creds.ResolveDefaultFull(task.UserID, conn)
			if err != nil {
				continue
			}
			upper := strings.ToUpper(strings.ReplaceAll(conn, "-", "_"))
			if primary != "" {
				env = append(env, fmt.Sprintf("PLATFORM_TOKEN_%s=%s", upper, primary))
			}
			if len(val) > 0 {
				if b, err := json.Marshal(val); err == nil {
					env = append(env, fmt.Sprintf("PLATFORM_CREDENTIAL_%s=%s", upper, string(b)))
				}
			}
		}
	}
	return env
}

const launcherSource = `// Auto-generated by the platform runner. Do not edit.
import { ctx } from '@platform/sdk';
const mod = await import('./main.ts');
const fn = mod.default;
if (typeof fn !== 'function') {
  ctx.log.error('main.ts must default-export a function');
  process.exit(2);
}
try {
  const result = await fn(ctx);
  if (result !== undefined) {
    process.stdout.write('[result] ' + JSON.stringify(result) + '\n');
  }
} catch (err) {
  ctx.log.error('workflow failed:', err?.stack || err?.message || String(err));
  process.exit(1);
}
`

func (r *NodeRunner) writeLauncher(dir string) error {
	return os.WriteFile(filepath.Join(dir, "_launcher.mjs"), []byte(launcherSource), 0o644)
}

var importRe = regexp.MustCompile(`(?m)^\s*(?:import|export)\s+(?:[^'"]*?\s+from\s+)?['"]([^'"]+)['"]`)
var dynImportRe = regexp.MustCompile(`import\(\s*['"]([^'"]+)['"]\s*\)`)
var requireRe = regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)`)

// installDeps inspects main.ts for non-platform imports and ensures the
// workflow directory has them installed. We write/refresh package.json and
// run `pnpm install` when the set of deps changes.
func (r *NodeRunner) installDeps(ctx context.Context, task *store.Task, run *store.Run, dir string) error {
	main, err := os.ReadFile(filepath.Join(dir, "main.ts"))
	if err != nil {
		return err
	}
	pkgs := extractExternalDeps(string(main))
	if len(pkgs) == 0 {
		return nil
	}

	pkgJSONPath := filepath.Join(dir, "package.json")
	existing := map[string]any{}
	if b, err := os.ReadFile(pkgJSONPath); err == nil {
		_ = json.Unmarshal(b, &existing)
	}
	deps, _ := existing["dependencies"].(map[string]any)
	if deps == nil {
		deps = map[string]any{}
	}
	changed := false
	for _, p := range pkgs {
		if _, ok := deps[p]; !ok {
			deps[p] = "*"
			changed = true
		}
	}
	if !changed {
		return nil
	}

	r.publishLog(task, run, "stdout", fmt.Sprintf("installing dependencies: %s", strings.Join(pkgs, ", ")))

	existing["name"] = "workflow-" + task.ID
	existing["private"] = true
	existing["type"] = "module"
	existing["dependencies"] = deps
	out, _ := json.MarshalIndent(existing, "", "  ")
	if err := os.WriteFile(pkgJSONPath, out, 0o644); err != nil {
		return err
	}
	// Use pnpm if available; fall back to npm.
	pm := "pnpm"
	if _, err := exec.LookPath("pnpm"); err != nil {
		pm = "npm"
	}
	cmd := exec.CommandContext(ctx, pm, "install", "--silent", "--prefer-offline")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "CI=1")
	combined, err := cmd.CombinedOutput()
	if err != nil {
		r.publishLog(task, run, "stderr", fmt.Sprintf("%s install failed: %s", pm, string(combined)))
		return fmt.Errorf("%s install: %w", pm, err)
	}
	r.publishLog(task, run, "stdout", "dependencies installed")
	return nil
}

// extractExternalDeps returns the set of bare-specifier imports referenced
// by `src`, excluding @platform/* and node: built-ins. Scoped packages are
// returned as `@scope/name`.
func extractExternalDeps(src string) []string {
	seen := map[string]bool{}
	collect := func(spec string) {
		spec = strings.TrimSpace(spec)
		if spec == "" || strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
			return
		}
		if strings.HasPrefix(spec, "node:") {
			return
		}
		// Normalize scoped vs unscoped subpath imports — keep only the package name.
		pkg := spec
		if strings.HasPrefix(spec, "@") {
			parts := strings.SplitN(spec, "/", 3)
			if len(parts) >= 2 {
				pkg = parts[0] + "/" + parts[1]
			}
		} else if i := strings.Index(spec, "/"); i > 0 {
			pkg = spec[:i]
		}
		if strings.HasPrefix(pkg, "@platform/") {
			return
		}
		// Skip node built-ins like `fs`, `path`, `crypto` (very common false positives).
		switch pkg {
		case "fs", "path", "crypto", "os", "child_process", "stream", "util",
			"http", "https", "url", "events", "buffer", "process", "assert":
			return
		}
		seen[pkg] = true
	}
	for _, m := range importRe.FindAllStringSubmatch(src, -1) {
		collect(m[1])
	}
	for _, m := range dynImportRe.FindAllStringSubmatch(src, -1) {
		collect(m[1])
	}
	for _, m := range requireRe.FindAllStringSubmatch(src, -1) {
		collect(m[1])
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func (r *NodeRunner) ensureSDKSymlink(dir string) error {
	nm := filepath.Join(dir, "node_modules")
	platDir := filepath.Join(nm, "@platform")
	link := filepath.Join(platDir, "sdk")

	if fi, err := os.Lstat(link); err == nil {
		if fi.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		_ = os.RemoveAll(link)
	}

	if err := os.MkdirAll(platDir, 0o755); err != nil {
		return err
	}
	return os.Symlink(r.sdkPath, link)
}

// publishLog emits a single synthetic log line on behalf of the runner
// (e.g. "installing dependencies …"). The supervised child process uses pipe.
func (r *NodeRunner) publishLog(task *store.Task, run *store.Run, stream, text string) {
	// Dry-runs (RunDry) have no run row — skip the run-scoped log publish
	// rather than nil-deref and crash the whole control plane.
	if run == nil {
		return
	}
	line := store.LogLine{RunID: run.ID, Ts: time.Now(), Stream: stream, Text: text}
	r.store.AppendLog(line)
	r.bus.Publish(store.Event{
		Channel: "run:log:" + run.ID,
		UserID:  task.UserID,
		TaskID:  task.ID,
		RunID:   run.ID,
		Payload: map[string]any{"line": line},
	})
}

func (r *NodeRunner) pipe(task *store.Task, run *store.Run, stream string, src io.Reader) {
	scan := bufio.NewScanner(src)
	scan.Buffer(make([]byte, 0, 8192), 1<<20)
	for scan.Scan() {
		line := store.LogLine{
			RunID:  run.ID,
			Ts:     time.Now(),
			Stream: stream,
			Text:   scan.Text(),
		}
		r.store.AppendLog(line)
		r.bus.Publish(store.Event{
			Channel: "run:log:" + run.ID,
			UserID:  task.UserID,
			TaskID:  task.ID,
			RunID:   run.ID,
			Payload: map[string]any{"line": line},
		})
	}
}

func (r *NodeRunner) fail(task *store.Task, run *store.Run, msg string) {
	r.transition(task, run, store.RunFailed, func(rr *store.Run) {
		now := time.Now()
		rr.FinishedAt = &now
		if rr.StartedAt != nil {
			rr.DurationMs = int(now.Sub(*rr.StartedAt).Milliseconds())
		}
		rr.Error = msg
	})
	r.afterTerminal(task, run)
}

func (r *NodeRunner) transition(task *store.Task, run *store.Run, state store.RunState, mutate func(*store.Run)) {
	updated, _ := r.store.UpdateRun(run.ID, func(rr *store.Run) {
		rr.State = state
		if mutate != nil {
			mutate(rr)
		}
	})
	r.bus.Publish(store.Event{
		Channel: "run:state:" + run.ID,
		UserID:  task.UserID,
		TaskID:  task.ID,
		RunID:   run.ID,
		Payload: map[string]any{"state": string(state), "run": updated},
	})
}
