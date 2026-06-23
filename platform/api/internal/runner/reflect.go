package runner

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/nk/platform/api/internal/reflection"
	"github.com/nk/platform/api/internal/store"
)

// PostRunHook fires after a run reaches a terminal state. The dispatcher
// wires it into NodeRunner so reflection runs every successful or failed
// workflow.
type PostRunHook func(task *store.Task, run *store.Run)

// ReflectionHook returns a PostRunHook that calls the reflection service,
// writes the produced memory entries into the user's memory store, and
// hands the reflection result to the evolution engine for trigger-policy
// evaluation.
func ReflectionHook(
	reflectClient *reflection.Client,
	memoryStore *store.MemoryStore,
	bus *store.Bus,
	taskStore *store.Memory,
	evo *EvolutionEngine,
) PostRunHook {
	return func(task *store.Task, run *store.Run) {
		if reflectClient == nil || reflectClient.BaseURL == "" {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		logs := taskStore.GetLogs(run.ID)
		tail := make([]reflection.LogLineIn, 0, len(logs))
		for _, l := range logs {
			tail = append(tail, reflection.LogLineIn{Stream: l.Stream, Text: l.Text})
		}

		// Pull recent memory for this user as context — Phase 1 uses a
		// keyword search against the workflow name + outcome.
		mem := memoryStore.Search(task.UserID, task.Name+" "+string(run.State), 5)
		recent := make([]reflection.MemorySnippet, 0, len(mem))
		for _, m := range mem {
			recent = append(recent, reflection.MemorySnippet{Topic: m.Topic, Content: m.Content})
		}

		out, err := reflectClient.Reflect(ctx, reflection.Request{
			RunID:        run.ID,
			WorkflowID:   task.ID,
			WorkflowName: task.Name,
			Outcome:      string(run.State),
			DurationMs:   run.DurationMs,
			LogsTail:     tail,
			RecentMemory: recent,
		})
		if err != nil {
			log.Printf("reflection failed for run %s: %v", run.ID, err)
			return
		}

		for _, upd := range out.MemoryUpdates {
			scope := store.ScopeUser
			scopeKey := ""
			if upd.Scope == "task" {
				scope = store.ScopeTask
				scopeKey = task.ID
			}
			entry := memoryStore.Write(&store.MemoryEntry{
				UserID:    task.UserID,
				Scope:     scope,
				ScopeKey:  scopeKey,
				Topic:     upd.Topic,
				Content:   upd.Content,
				Pinned:    upd.Pinned,
				SourceRun: run.ID,
			})
			bus.Publish(store.Event{
				Channel: "memory:written:" + task.UserID,
				UserID:  task.UserID,
				TaskID:  task.ID,
				RunID:   run.ID,
				Payload: map[string]any{"entry": entry},
			})
		}

		bus.Publish(store.Event{
			Channel: "reflection:complete:" + run.ID,
			UserID:  task.UserID,
			TaskID:  task.ID,
			RunID:   run.ID,
			Payload: map[string]any{
				"reflection": out,
				"summary":    strings.TrimSpace(out.Summary),
			},
		})

		log.Printf("reflection: run=%s score=%d summary=%q", run.ID, out.Score, out.Summary)

		if evo != nil {
			evo.OnReflection(task, run, out)
		}
	}
}
