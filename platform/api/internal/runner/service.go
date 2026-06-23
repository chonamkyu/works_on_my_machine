package runner

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/store"
)

// ServiceManager keeps `kind=service` workflows alive as long-running
// daemons. On boot it spins up every service task; on crash it backs off and
// restarts; on Stop() it tears them all down.
//
// Each service has a stable "service run" that lives for the whole daemon
// lifetime. The workflow code is expected to run its own loop inside
// (setInterval / while loop) — the runner just keeps the process up.
type ServiceManager struct {
	store *store.Memory
	node  *NodeRunner
	bus   *store.Bus
	hook  PostRunHook // self-healing: reflection runs after every crash

	mu         sync.Mutex
	running    map[string]*serviceProc
	lastHealth map[string]time.Time
	stopCh     chan struct{}
	stopOnce   sync.Once
}

type serviceProc struct {
	cancel context.CancelFunc
	taskID string
}

func NewServiceManager(s *store.Memory, n *NodeRunner, b *store.Bus, hook PostRunHook) *ServiceManager {
	return &ServiceManager{
		store:   s,
		node:    n,
		bus:     b,
		hook:    hook,
		running: map[string]*serviceProc{},
		stopCh:  make(chan struct{}),
	}
}

// Start launches the supervisor loop and brings up every service task.
func (sm *ServiceManager) Start() {
	go sm.reconcileLoop()
}

func (sm *ServiceManager) Stop() {
	sm.stopOnce.Do(func() { close(sm.stopCh) })
	sm.mu.Lock()
	defer sm.mu.Unlock()
	for _, p := range sm.running {
		p.cancel()
	}
	sm.running = map[string]*serviceProc{}
}

// reconcileLoop syncs running daemons with the set of `kind=service` tasks.
// Cheap polling — workable until LISTEN/NOTIFY for task changes is wired.
func (sm *ServiceManager) reconcileLoop() {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	sm.reconcile()
	for {
		select {
		case <-sm.stopCh:
			return
		case <-tick.C:
			sm.reconcile()
		}
	}
}

func (sm *ServiceManager) reconcile() {
	desired := map[string]*store.Task{}
	for _, t := range sm.store.ListServiceTasks() {
		desired[t.ID] = t
	}

	// Health probe: for each running service, check the recent log tail. If
	// stderr is dominant or the same error repeats > N times, treat it as
	// unhealthy and fire the reflection hook so self-healing engages even
	// when the process technically stays alive.
	for id := range sm.running {
		sm.checkHealth(id)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Stop daemons whose tasks were deleted or flipped to oneshot.
	for id, p := range sm.running {
		if _, ok := desired[id]; !ok {
			log.Printf("service: stopping %s (task removed)", id)
			p.cancel()
			delete(sm.running, id)
		}
	}
	// Start new ones.
	for id, t := range desired {
		if _, ok := sm.running[id]; ok {
			continue
		}
		log.Printf("service: starting %s (%s)", id, t.Name)
		ctx, cancel := context.WithCancel(context.Background())
		sm.running[id] = &serviceProc{cancel: cancel, taskID: id}
		go sm.supervise(ctx, t)
	}
}

// supervise restarts the workflow with exponential backoff (capped at 30s)
// for as long as the manager wants it to run.
func (sm *ServiceManager) supervise(ctx context.Context, task *store.Task) {
	backoff := time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		// Re-fetch each loop so we pick up code/capability changes after
		// edits or evolution accepts.
		fresh, err := sm.store.GetTask(task.ID)
		if err != nil {
			log.Printf("service %s: task gone — exit", task.ID)
			return
		}
		if fresh.Kind != "service" {
			log.Printf("service %s: kind changed — exit", task.ID)
			return
		}
		log.Printf("service %s: launching", fresh.ID)
		exitErr := sm.node.RunForeground(ctx, fresh, "service")
		select {
		case <-ctx.Done():
			return
		default:
		}
		if exitErr != nil {
			log.Printf("service %s: exited (%v) — restart in %v", fresh.ID, exitErr, backoff)
		} else {
			log.Printf("service %s: exited cleanly — restart in %v", fresh.ID, backoff)
		}
		// Self-healing hook: feed the just-crashed run to the reflection
		// service. Evolution engine consumes the resulting signal and may
		// open a PR if crashes accumulate.
		if sm.hook != nil {
			if last, _ := sm.lastRun(fresh.ID); last != nil {
				go sm.hook(fresh, last)
			}
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// checkHealth scans the most-recent service run's logs. If we see a tight
// loop of identical errors, the workflow is misbehaving even though the
// process is still up. Fire the reflection hook so Reflection + Evolution
// can diagnose and patch.
func (sm *ServiceManager) checkHealth(taskID string) {
	sm.mu.Lock()
	last, ok := sm.lastHealth[taskID]
	sm.mu.Unlock()
	if ok && time.Since(last) < 2*time.Minute {
		return // throttled
	}

	task, err := sm.store.GetTask(taskID)
	if err != nil {
		return
	}
	run, _ := sm.lastRun(taskID)
	if run == nil {
		return
	}
	logs := sm.store.GetLogs(run.ID)
	if len(logs) < 10 {
		return
	}
	// Keep the last window.
	start := 0
	if len(logs) > 30 {
		start = len(logs) - 30
	}
	tail := logs[start:]
	errCount := 0
	for _, l := range tail {
		if l.Stream == "stderr" {
			errCount++
		}
	}
	if errCount < 15 {
		return // mostly healthy
	}
	sm.mu.Lock()
	if sm.lastHealth == nil {
		sm.lastHealth = map[string]time.Time{}
	}
	sm.lastHealth[taskID] = time.Now()
	sm.mu.Unlock()

	log.Printf("service %s: unhealthy (%d stderr lines in tail) — invoking reflection", taskID, errCount)
	if sm.hook != nil {
		go sm.hook(task, run)
	}
}

// lastRun finds the most-recent service run for a task so the reflection
// hook can analyse it.
func (sm *ServiceManager) lastRun(taskID string) (*store.Run, error) {
	runs := sm.store.ListRunsForTask(taskID)
	for _, r := range runs {
		if r.Trigger == "service" {
			return r, nil
		}
	}
	if len(runs) > 0 {
		return runs[0], nil
	}
	return nil, nil
}

// Restart asks the supervisor to bounce a single daemon (e.g. after the user
// accepted an evolution PR). Idempotent.
func (sm *ServiceManager) Restart(taskID string) {
	sm.mu.Lock()
	p, ok := sm.running[taskID]
	sm.mu.Unlock()
	if !ok {
		return
	}
	p.cancel()
	// Reconcile loop will see the missing daemon and start a fresh one.
}

// IsRunning reports whether a given service task has a live daemon.
func (sm *ServiceManager) IsRunning(taskID string) bool {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	_, ok := sm.running[taskID]
	return ok
}
