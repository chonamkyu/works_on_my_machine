package runner

import (
	"fmt"
	"sync"
	"time"

	"github.com/nk/platform/api/internal/store"
)

// MockRunner pretends to execute a workflow. It pushes log lines and state
// transitions to the bus exactly the same way the real runner will, so the
// web UI can be driven end-to-end before pods exist.
type MockRunner struct {
	store *store.Memory
	bus   *store.Bus

	mu     sync.Mutex
	cancel map[string]chan struct{}
}

func NewMock(s *store.Memory, b *store.Bus) *MockRunner {
	return &MockRunner{store: s, bus: b, cancel: map[string]chan struct{}{}}
}

func (r *MockRunner) Start(task *store.Task, trigger string) *store.Run {
	run := r.store.CreateRun(task.ID, trigger)
	cancelCh := make(chan struct{})

	r.mu.Lock()
	r.cancel[run.ID] = cancelCh
	r.mu.Unlock()

	go r.simulate(task, run, cancelCh)

	r.bus.Publish(store.Event{
		Channel: "trigger:fired:" + task.ID,
		UserID:  task.UserID,
		TaskID:  task.ID,
		RunID:   run.ID,
		Payload: map[string]any{"trigger": trigger},
	})
	return run
}

func (r *MockRunner) Cancel(runID string) bool {
	r.mu.Lock()
	ch, ok := r.cancel[runID]
	delete(r.cancel, runID)
	r.mu.Unlock()
	if !ok {
		return false
	}
	close(ch)
	return true
}

func (r *MockRunner) simulate(task *store.Task, run *store.Run, cancelCh chan struct{}) {
	r.transition(run, store.RunRunning, func(rr *store.Run) {
		now := time.Now()
		rr.StartedAt = &now
	})

	steps := []string{
		fmt.Sprintf("starting workflow %s", task.Name),
		"loading credentials from vault: ok",
		"connector handshake: github ok",
		"fetching issues...",
		"summarizing with claude-sonnet-4-6...",
		"posting to slack #me...",
		"done",
	}

	for i, line := range steps {
		select {
		case <-cancelCh:
			r.transition(run, store.RunCancelled, func(rr *store.Run) {
				now := time.Now()
				rr.FinishedAt = &now
				if rr.StartedAt != nil {
					rr.DurationMs = int(now.Sub(*rr.StartedAt).Milliseconds())
				}
				rr.Error = "cancelled by user"
			})
			r.publishLog(task, run, "stderr", "cancelled by user")
			return
		case <-time.After(time.Duration(300+i*200) * time.Millisecond):
			r.publishLog(task, run, "stdout", line)
		}
	}

	r.transition(run, store.RunSuccess, func(rr *store.Run) {
		now := time.Now()
		rr.FinishedAt = &now
		if rr.StartedAt != nil {
			rr.DurationMs = int(now.Sub(*rr.StartedAt).Milliseconds())
		}
		rr.Output = map[string]any{"issues_processed": 4, "summary": "ok"}
	})
}

func (r *MockRunner) transition(run *store.Run, state store.RunState, mutate func(*store.Run)) {
	updated, _ := r.store.UpdateRun(run.ID, func(rr *store.Run) {
		rr.State = state
		if mutate != nil {
			mutate(rr)
		}
	})
	r.bus.Publish(store.Event{
		Channel: "run:state:" + run.ID,
		UserID:  store.DefaultUserID,
		TaskID:  run.TaskID,
		RunID:   run.ID,
		Payload: map[string]any{"state": string(state), "run": updated},
	})
}

func (r *MockRunner) publishLog(task *store.Task, run *store.Run, stream, text string) {
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
