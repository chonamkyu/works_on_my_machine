package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

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

	mu     sync.Mutex
	cancel map[string]context.CancelFunc
}

func NewNode(s *store.Memory, b *store.Bus, files *store.WorkflowFS, sdkPath string, hook PostRunHook, creds *store.CredentialStore) *NodeRunner {
	return &NodeRunner{
		store:   s,
		bus:     b,
		files:   files,
		sdkPath: sdkPath,
		node:    nodeBinary(),
		hook:    hook,
		creds:   creds,
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

func (r *NodeRunner) execute(ctx context.Context, task *store.Task, run *store.Run) {
	dir := r.files.Dir(task.ID)
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
	env = append(env,
		"PLATFORM_RUN_ID="+run.ID,
		"PLATFORM_TASK_ID="+task.ID,
		"PLATFORM_USER_ID="+task.UserID,
	)
	if r.creds != nil {
		for _, conn := range task.Capabilities {
			if _, secret, err := r.creds.ResolveDefault(task.UserID, conn); err == nil && secret != "" {
				env = append(env, fmt.Sprintf("PLATFORM_TOKEN_%s=%s", strings.ToUpper(conn), secret))
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
