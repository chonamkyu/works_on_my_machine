package handler_test

// TestSupervisorRoutingTriggerRun verifies that TriggerRun dispatches via
// supervisor.Client.StartRun with the correct workspace ID.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/handler"
	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/supervisor"
)

// fakeSupervisor implements supervisor.Client and records the ws argument
// passed to StartRun so tests can assert workspace routing.
type fakeSupervisor struct {
	store       *store.Memory
	lastStartWS string
}

func (f *fakeSupervisor) StartRun(ws string, task *store.Task, trigger string) *store.Run {
	f.lastStartWS = ws
	return f.store.CreateRun(task.ID, trigger)
}

func (f *fakeSupervisor) CancelRun(ws, runID string) bool { return false }

func (f *fakeSupervisor) StopService(ws, taskID string) error { return nil }

func (f *fakeSupervisor) EnsureService(ws string, task *store.Task) error { return nil }

func (f *fakeSupervisor) Endpoint(ws, taskID string) (string, map[string]int, bool) {
	return "", nil, false
}

func (f *fakeSupervisor) Health(ws string) supervisor.Status {
	return supervisor.Status{Up: true}
}

func (f *fakeSupervisor) ServiceRunning(ws, taskID string) bool { return false }

func (f *fakeSupervisor) RestartService(ws, taskID string) error { return nil }

func (f *fakeSupervisor) BootServices() error { return nil }

func (f *fakeSupervisor) ReapIdle() error { return nil }

// TestSupervisorRoutingTriggerRun creates a task in workspace "wsX",
// POSTs /tasks/{id}/runs, and asserts that the fake supervisor received
// ws == "wsX".
func TestSupervisorRoutingTriggerRun(t *testing.T) {
	s := openTestDB(t)
	auth.SetAdminEnv("")
	auth.SetAdminChecker(s)

	fake := &fakeSupervisor{store: s}

	h := handler.New(s, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	h.Sup = fake

	wsX := "wsX-" + randSuffix()
	task := s.CreateTask(&store.Task{
		UserID:      wsX,
		WorkspaceID: wsX,
		Name:        "routing-test-task",
		Kind:        "oneshot",
	})

	r := chi.NewRouter()
	r.Post("/tasks/{id}/runs", h.TriggerRun)

	body := strings.NewReader(`{"trigger":"test"}`)
	req := httptest.NewRequest(http.MethodPost, "/tasks/"+task.ID+"/runs", body)
	req = injectClaims(req, wsX, nil)
	req = withWorkspace(req, wsX)

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("TriggerRun: want 202, got %d body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["id"] == "" || resp["id"] == nil {
		t.Errorf("expected a run ID in response, got %v", resp)
	}

	if fake.lastStartWS != wsX {
		t.Errorf("StartRun called with ws=%q, want %q", fake.lastStartWS, wsX)
	}
}
