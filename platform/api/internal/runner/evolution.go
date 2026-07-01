package runner

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/reflection"
	"github.com/nk/platform/api/internal/store"
)

// EvolutionEngine watches reflection signals per task and, when a trigger
// policy fires, asks the reflection service's `/evolve` endpoint for a code
// patch. The result is stored as a pending EvolutionPR.
//
// Trigger policy (Phase 1, conservative):
//   - 2 consecutive failures, OR
//   - 5 accumulated evolution_hints since last PR, OR
//   - manual user request
//
// Per-task cooldown: 1 minute between PRs for the same task.
type EvolutionEngine struct {
	reflectClient *reflection.Client
	evoStore      *store.EvolutionStore
	taskStore     *store.Memory
	files         *store.WorkflowFS
	memoryStore   *store.MemoryStore
	bus           *store.Bus

	mu        sync.Mutex
	failures  map[string]int            // task_id -> consecutive failure count
	hints     map[string][]reflection.EvolutionHint
	lastPR    map[string]time.Time      // task_id -> last PR time (cooldown)
	refsByTask map[string][]reflection.Result // recent reflections per task
}

func NewEvolutionEngine(
	rc *reflection.Client,
	es *store.EvolutionStore,
	ts *store.Memory,
	files *store.WorkflowFS,
	ms *store.MemoryStore,
	bus *store.Bus,
) *EvolutionEngine {
	return &EvolutionEngine{
		reflectClient: rc,
		evoStore:      es,
		taskStore:     ts,
		files:         files,
		memoryStore:   ms,
		bus:           bus,
		failures:      map[string]int{},
		hints:         map[string][]reflection.EvolutionHint{},
		lastPR:        map[string]time.Time{},
		refsByTask:    map[string][]reflection.Result{},
	}
}

// OnReflection ingests a reflection result for a task. May fire an evolution
// PR if the policy triggers.
func (e *EvolutionEngine) OnReflection(task *store.Task, run *store.Run, r *reflection.Result) {
	e.mu.Lock()
	switch r.Outcome {
	case "failure", "timeout":
		e.failures[task.ID]++
	case "success":
		e.failures[task.ID] = 0
	}
	e.hints[task.ID] = append(e.hints[task.ID], r.EvolutionHints...)

	// Keep a rolling window of recent reflections per task.
	refs := append(e.refsByTask[task.ID], *r)
	if len(refs) > 10 {
		refs = refs[len(refs)-10:]
	}
	e.refsByTask[task.ID] = refs

	reason := e.checkTrigger(task.ID)
	if reason == "" {
		e.mu.Unlock()
		return
	}
	if t, ok := e.lastPR[task.ID]; ok && time.Since(t) < time.Minute {
		e.mu.Unlock()
		return
	}
	e.lastPR[task.ID] = time.Now()
	e.failures[task.ID] = 0
	e.hints[task.ID] = nil
	reflections := append([]reflection.Result(nil), refs...)
	e.mu.Unlock()

	go e.propose(task, reflections, reason)
}

// ManualPropose lets a user kick off evolution from the UI/CLI.
func (e *EvolutionEngine) ManualPropose(task *store.Task) {
	e.mu.Lock()
	refs := append([]reflection.Result(nil), e.refsByTask[task.ID]...)
	e.lastPR[task.ID] = time.Now()
	e.mu.Unlock()
	go e.propose(task, refs, "manual")
}

func (e *EvolutionEngine) checkTrigger(taskID string) string {
	if e.failures[taskID] >= 2 {
		return "failure_streak"
	}
	if len(e.hints[taskID]) >= 5 {
		return "hint_accumulation"
	}
	return ""
}

func (e *EvolutionEngine) propose(task *store.Task, refs []reflection.Result, reason string) {
	if e.reflectClient == nil || e.reflectClient.BaseURL == "" {
		return
	}
	if e.evoStore.PendingForTask(task.ID) > 0 {
		return
	}

	mainTs, _ := e.files.ReadFile(task.WorkspaceID, task.ID, "main.ts")
	yaml, _ := e.files.ReadFile(task.WorkspaceID, task.ID, "workflow.yaml")
	if mainTs == "" {
		return
	}

	mem := e.memoryStore.List(task.UserID, store.ScopeUser, "")
	if taskMem := e.memoryStore.List(task.UserID, store.ScopeTask, task.ID); len(taskMem) > 0 {
		mem = append(mem, taskMem...)
	}
	snippets := make([]reflection.MemorySnippet, 0, len(mem))
	for _, m := range mem {
		snippets = append(snippets, reflection.MemorySnippet{Topic: m.Topic, Content: m.Content})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	out, err := e.reflectClient.Evolve(ctx, reflection.EvolveRequest{
		WorkflowID:          task.ID,
		WorkflowName:        task.Name,
		CurrentMainTs:       mainTs,
		CurrentWorkflowYaml: yaml,
		RecentReflections:   refs,
		RecentMemory:        snippets,
		Reason:              reason,
	})
	if err != nil {
		log.Printf("evolution: propose failed for %s: %v", task.ID, err)
		return
	}

	refIDs := make([]string, 0, len(refs))
	for _, r := range refs {
		refIDs = append(refIDs, r.RunID)
	}

	pr := e.evoStore.Create(&store.EvolutionPR{
		TaskID:             task.ID,
		UserID:             task.UserID,
		Reason:             reason,
		Rationale:          out.Rationale,
		DiffSummary:        out.DiffSummary,
		SelfReview:         out.SelfReview,
		OldMainTs:          mainTs,
		NewMainTs:          out.NewMainTs,
		OldWorkflowYaml:    yaml,
		NewWorkflowYaml:    out.NewWorkflowYaml,
		BasedOnReflections: refIDs,
	})

	e.bus.Publish(store.Event{
		Channel: "evolution:proposed:" + task.ID,
		UserID:  task.UserID,
		TaskID:  task.ID,
		Payload: map[string]any{"pr": pr},
	})
	log.Printf("evolution PR created: id=%s task=%s reason=%s", pr.ID, task.ID, reason)
}

// ApplyAccepted overwrites the task's source files with the new code.
func (e *EvolutionEngine) ApplyAccepted(pr *store.EvolutionPR) error {
	patch := map[string]string{}
	if pr.NewMainTs != "" {
		patch["main.ts"] = pr.NewMainTs
	}
	if pr.NewWorkflowYaml != "" {
		patch["workflow.yaml"] = pr.NewWorkflowYaml
	}
	if len(patch) == 0 {
		return nil
	}
	return e.files.WriteFiles(pr.WorkspaceID, pr.TaskID, patch)
}
