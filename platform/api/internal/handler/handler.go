package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/generator"
	"github.com/nk/platform/api/internal/oauth"
	"github.com/nk/platform/api/internal/runner"
	"github.com/nk/platform/api/internal/store"
)

type Handler struct {
	Store       *store.Memory
	Bus         *store.Bus
	Runner      runner.Runner
	Files       *store.WorkflowFS
	Generator   *generator.Client
	Memory      *store.MemoryStore
	Evolution   *store.EvolutionStore
	Engine      *runner.EvolutionEngine
	Credentials *store.CredentialStore
	OAuth       *oauth.Manager
	Services    *runner.ServiceManager
}

func New(
	s *store.Memory,
	b *store.Bus,
	r runner.Runner,
	files *store.WorkflowFS,
	gen *generator.Client,
	mem *store.MemoryStore,
	evoStore *store.EvolutionStore,
	engine *runner.EvolutionEngine,
	creds *store.CredentialStore,
	oauthMgr *oauth.Manager,
) *Handler {
	return &Handler{
		Store:       s,
		Bus:         b,
		Runner:      r,
		Files:       files,
		Generator:   gen,
		Memory:      mem,
		Evolution:   evoStore,
		Engine:      engine,
		Credentials: creds,
		OAuth:       oauthMgr,
	}
}

// --- OAuth ------------------------------------------------------------

func (h *Handler) ListOAuthProviders(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"providers": h.OAuth.ProvidersJSON()})
}

type oauthStartBody struct {
	Provider string   `json:"provider"`
	Label    string   `json:"label"`
	Scopes   []string `json:"scopes"`
}

func (h *Handler) StartOAuth(w http.ResponseWriter, r *http.Request) {
	var body oauthStartBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Label == "" {
		body.Label = "oauth"
	}
	url, err := h.OAuth.Start(body.Provider, body.Label, store.DefaultUserID, body.Scopes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": url})
}

// RefreshSlackScopes updates the existing Slack app's manifest with the
// platform's full default scope set and returns a fresh OAuth URL the user
// can click to grant the broader permissions.
func (h *Handler) RefreshSlackScopes(w http.ResponseWriter, r *http.Request) {
	var req oauth.SlackUpdateScopesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := h.OAuth.UpdateSlackAppScopes(ctx, req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ProvisionSlackApp accepts a Slack app-configuration token (xoxe.xoxp-…)
// and uses `apps.manifest.create` to mint a real Slack OAuth app — saving
// the issued client_id/secret into the running config + .env so the
// "Connect with slack" button immediately works.
func (h *Handler) ProvisionSlackApp(w http.ResponseWriter, r *http.Request) {
	var req oauth.SlackProvisionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	out, err := h.OAuth.ProvisionSlackApp(ctx, req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// OAuthCallback is the redirect target the provider sends users back to.
// On success it renders a tiny HTML page that posts a message to the opener
// window (so the Settings page knows to refresh) and closes itself.
func (h *Handler) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	cred, err := h.OAuth.Callback(r.Context(), provider, r.URL.Query())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err != nil {
		fmt.Fprintf(w, `<html><body style="font-family:system-ui;padding:24px;color:#e4e4e7;background:#1c1c1f"><h2 style="color:#ef4444">OAuth failed</h2><pre>%s</pre><p>You can close this window.</p></body></html>`, err.Error())
		return
	}
	fmt.Fprintf(w, `<html><body style="font-family:system-ui;padding:24px;color:#e4e4e7;background:#1c1c1f">
<h2 style="color:#22c55e">Connected!</h2>
<p>Provider: <b>%s</b> · Label: <b>%s</b></p>
<p>This window will close automatically.</p>
<script>
if (window.opener) {
  window.opener.postMessage({ type: 'platform-oauth-complete', provider: %q, credentialId: %q }, '*');
}
setTimeout(() => window.close(), 800);
</script>
</body></html>`, cred.Connector, cred.Label, cred.Connector, cred.ID)
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

// --- tasks ------------------------------------------------------------

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": h.Store.ListTasks(store.DefaultUserID)})
}

type createTaskBody struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	Triggers     []store.Trigger `json:"triggers"`
	Capabilities []string        `json:"capabilities"`
}

func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	var body createTaskBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Name == "" {
		body.Name = "draft"
	}
	t := h.Store.CreateTask(&store.Task{
		UserID:       store.DefaultUserID,
		Name:         body.Name,
		Description:  body.Description,
		Triggers:     body.Triggers,
		Capabilities: body.Capabilities,
	})
	h.Bus.Publish(store.Event{
		Channel: "task:created:" + store.DefaultUserID,
		UserID:  store.DefaultUserID,
		TaskID:  t.ID,
		Payload: map[string]any{"task": t},
	})
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := h.Store.GetTask(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type updateTaskBody struct {
	Description  *string           `json:"description,omitempty"`
	Triggers     *[]store.Trigger  `json:"triggers,omitempty"`
	Capabilities *[]string         `json:"capabilities,omitempty"`
	Model        *store.ModelConfig `json:"model,omitempty"`
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body updateTaskBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	t, err := h.Store.UpdateTask(id, func(t *store.Task) {
		if body.Description != nil {
			t.Description = *body.Description
		}
		if body.Triggers != nil {
			t.Triggers = *body.Triggers
		}
		if body.Capabilities != nil {
			t.Capabilities = *body.Capabilities
		}
		if body.Model != nil {
			t.Model = *body.Model
		}
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) DeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Store.DeleteTask(id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	// Best-effort: remove the on-disk source so the supervisor can't pick
	// the task back up.
	_ = os.RemoveAll(h.Files.Dir(id))
	h.Bus.Publish(store.Event{
		Channel: "task:deleted:" + store.DefaultUserID,
		UserID:  store.DefaultUserID,
		TaskID:  id,
	})
	w.WriteHeader(http.StatusNoContent)
}

// SetServiceEnabled flips a service-kind task on or off. When disabled, the
// supervisor stops the daemon on the next reconcile (≤5s).
func (h *Handler) SetServiceEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	t, err := h.Store.UpdateTask(id, func(t *store.Task) {
		if body.Enabled {
			if t.Status == "disabled" {
				t.Status = "idle"
			}
		} else {
			t.Status = "disabled"
		}
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// ServiceStatus reports whether the supervisor currently has a daemon
// running for this task.
func (h *Handler) ServiceStatus(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	running := false
	if h.Services != nil {
		running = h.Services.IsRunning(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"running": running,
	})
}

func (h *Handler) SetTriggerEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	trigID := chi.URLParam(r, "trigId")
	var body struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	t, err := h.Store.UpdateTask(id, func(t *store.Task) {
		for i := range t.Triggers {
			if t.Triggers[i].ID == trigID {
				t.Triggers[i].Enabled = body.Enabled
			}
		}
	})
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- runs -------------------------------------------------------------

type triggerRunBody struct {
	Trigger string `json:"trigger"`
}

func (h *Handler) TriggerRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := h.Store.GetTask(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	var body triggerRunBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Trigger == "" {
		body.Trigger = "manual"
	}
	run := h.Runner.Start(t, body.Trigger)
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{"runs": h.Store.ListRunsForTask(id)})
}

func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := h.Store.GetRun(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "logs": h.Store.GetLogs(id)})
}

func (h *Handler) CancelRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.Runner.Cancel(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no active run"})
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// --- code -------------------------------------------------------------

func (h *Handler) GetTaskCode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	yaml, errY := h.Files.ReadFile(id, "workflow.yaml")
	main, errM := h.Files.ReadFile(id, "main.ts")
	if errors.Is(errY, store.ErrNotFound) && errors.Is(errM, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"workflow.yaml": yaml,
		"main.ts":       main,
	})
}

// --- SSE --------------------------------------------------------------

func (h *Handler) RunStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	logsCh, cancelLogs := h.Bus.Subscribe("run:log:" + id)
	stateCh, cancelState := h.Bus.Subscribe("run:state:" + id)
	defer cancelLogs()
	defer cancelState()

	for _, line := range h.Store.GetLogs(id) {
		writeSSE(w, "run:log:"+id, map[string]any{"line": line})
	}
	if run, err := h.Store.GetRun(id); err == nil {
		writeSSE(w, "run:state:"+id, map[string]any{"state": string(run.State), "run": run})
	}
	flush(w)

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flush(w)
		case ev := <-logsCh:
			writeSSE(w, ev.Channel, ev.Payload)
			flush(w)
		case ev := <-stateCh:
			writeSSE(w, ev.Channel, ev.Payload)
			flush(w)
		}
	}
}

func (h *Handler) TaskEventsStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	triggers, c1 := h.Bus.Subscribe("trigger:fired:" + id)
	defer c1()
	flush(w)
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flush(w)
		case ev := <-triggers:
			writeSSE(w, ev.Channel, ev.Payload)
			flush(w)
		}
	}
}

func (h *Handler) ActivityStream(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	patterns := []string{
		"trigger:fired:*", "run:state:*", "task:created:*",
		"evolution:proposed:*", "memory:written:*", "reflection:complete:*",
	}
	cancels := make([]func(), 0, len(patterns))
	merged := make(chan store.Event, 64)
	for _, p := range patterns {
		ch, cancel := h.Bus.Subscribe(p)
		cancels = append(cancels, cancel)
		go func(c <-chan store.Event) {
			for ev := range c {
				if ev.UserID != "" && ev.UserID != userID {
					continue
				}
				merged <- ev
			}
		}(ch)
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	flush(w)
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flush(w)
		case ev := <-merged:
			writeSSE(w, ev.Channel, ev.Payload)
			flush(w)
		}
	}
}

// --- chat -------------------------------------------------------------

type chatMessageBody struct {
	Content string `json:"content"`
}

func (h *Handler) ChatMessage(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	var body chatMessageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Persist user message.
	_, _ = h.Store.AppendMessage(sessionID, "user", body.Content, nil)

	if looksLikeGreeting(body.Content) {
		reply := "👋"
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, nil)
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": reply})
		return
	}
	if h.Generator == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "generator not configured"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()

	connectors := []string{}
	for _, c := range h.Credentials.List(store.DefaultUserID) {
		connectors = append(connectors, c.Connector)
	}

	historyMsgs := h.Store.ListMessages(sessionID, 200)
	history := make([]generator.HistoryMessage, 0, len(historyMsgs))
	for _, m := range historyMsgs {
		history = append(history, generator.HistoryMessage{Role: m.Role, Content: m.Content})
	}

	existing := []generator.ExistingTask{}
	for _, t := range h.Store.ListTasks(store.DefaultUserID) {
		mainTs, _ := h.Files.ReadFile(t.ID, "main.ts")
		gtTriggers := make([]generator.Trigger, 0, len(t.Triggers))
		for _, tr := range t.Triggers {
			gtTriggers = append(gtTriggers, generator.Trigger{
				ID: tr.ID, Type: tr.Type, Enabled: tr.Enabled, Config: tr.Config,
			})
		}
		// Attach the latest run's state + tail logs so the generator can
		// diagnose problems from chat without the user pasting logs.
		var lastState, lastError string
		var tail []string
		runs := h.Store.ListRunsForTask(t.ID)
		if len(runs) > 0 {
			lastState = string(runs[0].State)
			lastError = runs[0].Error
			logs := h.Store.GetLogs(runs[0].ID)
			start := 0
			if len(logs) > 60 {
				start = len(logs) - 60
			}
			for _, l := range logs[start:] {
				tail = append(tail, fmt.Sprintf("[%s] %s", l.Stream, l.Text))
			}
		}
		existing = append(existing, generator.ExistingTask{
			ID: t.ID, Name: t.Name, Description: t.Description, Kind: t.Kind,
			Capabilities: t.Capabilities, Triggers: gtTriggers, MainTs: mainTs,
			LastRunState: lastState, LastRunError: lastError, RecentLogTail: tail,
		})
	}

	// In the 1:1 chat-per-task model, sessionID == taskID. Surface whether
	// that task is a blank draft so the generator can fill it in-place
	// instead of spawning yet another sibling.
	activeTaskID := sessionID
	activeIsDraft := false
	if t, err := h.Store.GetTask(activeTaskID); err == nil {
		main, _ := h.Files.ReadFile(activeTaskID, "main.ts")
		if main == "" && len(t.Triggers) == 0 && len(t.Capabilities) == 0 {
			activeIsDraft = true
		}
	} else {
		activeTaskID = ""
	}

	outcome, err := h.Generator.Generate(ctx, generator.Request{
		Prompt:              body.Content,
		SessionID:           sessionID,
		UserID:              store.DefaultUserID,
		ActiveTaskID:        activeTaskID,
		ActiveTaskIsDraft:   activeIsDraft,
		ConnectorsAvailable: connectors,
		History:             history,
		ExistingTasks:       existing,
	})
	if err != nil {
		log.Printf("generator failed: %v", err)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}

	// Backend guard: if the generator chose to create but the user is
	// chatting inside a blank draft task, redirect the create into an
	// update on that draft. Same code, same metadata, but no orphan.
	if outcome.Intent == generator.IntentCreate && activeIsDraft && activeTaskID != "" {
		outcome.Intent = generator.IntentUpdate
		outcome.TargetTaskID = activeTaskID
	}

	switch outcome.Intent {
	case generator.IntentChat:
		reply := outcome.Reply
		if reply == "" {
			reply = "ok"
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, nil)
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": reply})

	case generator.IntentManageCredential:
		if outcome.ManageCredential == nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "manage_credential without payload"})
			return
		}
		mc := outcome.ManageCredential
		switch mc.Action {
		case "delete":
			// Best-effort delete by connector+label match.
			for _, c := range h.Credentials.List(store.DefaultUserID) {
				if c.Connector == mc.Connector && (mc.Label == "" || c.Label == mc.Label) {
					_ = h.Credentials.Delete(c.ID)
				}
			}
		default: // upsert
			_, err := h.Credentials.Upsert(&store.Credential{
				UserID:    store.DefaultUserID,
				Connector: mc.Connector,
				Label:     mc.Label,
				Value:     mc.Value,
			})
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
				return
			}
		}
		reply := outcome.Reply
		if reply == "" {
			reply = fmt.Sprintf("Credential %s for %s.", mc.Action, mc.Connector)
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, map[string]any{
			"managedCredential": mc,
		})
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": reply})

	case generator.IntentRequestCredential:
		reply := outcome.Reply
		if reply == "" && outcome.CredentialRequest != nil {
			reply = fmt.Sprintf("Need a %s credential to continue.", outcome.CredentialRequest.Connector)
		}
		meta := map[string]any{}
		if outcome.CredentialRequest != nil {
			meta["credentialRequest"] = outcome.CredentialRequest
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, meta)
		writeJSON(w, http.StatusOK, map[string]any{
			"sessionId":         sessionID,
			"reply":             reply,
			"credentialRequest": outcome.CredentialRequest,
		})

	case generator.IntentDelete:
		deleted := []string{}
		for _, id := range outcome.TargetTaskIDs {
			if err := h.Store.DeleteTask(id); err == nil {
				_ = os.RemoveAll(h.Files.Dir(id))
				deleted = append(deleted, id)
				h.Bus.Publish(store.Event{
					Channel: "task:deleted:" + store.DefaultUserID,
					UserID:  store.DefaultUserID,
					TaskID:  id,
				})
			}
		}
		reply := outcome.Reply
		if reply == "" {
			reply = fmt.Sprintf("Deleted %d task(s): %v.", len(deleted), deleted)
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, map[string]any{"deleted": deleted})
		writeJSON(w, http.StatusOK, map[string]any{
			"sessionId": sessionID,
			"reply":     reply,
			"deleted":   deleted,
		})

	case generator.IntentUpdate:
		taskID := outcome.TargetTaskID
		if taskID == "" {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "update without target task id"})
			return
		}
		task, err := h.Store.UpdateTask(taskID, func(t *store.Task) {
			if outcome.Name != "" {
				t.Name = outcome.Name
			}
			if outcome.Description != "" {
				t.Description = outcome.Description
			}
			if outcome.Capabilities != nil {
				t.Capabilities = outcome.Capabilities
			}
			if outcome.Triggers != nil {
				t.Triggers = translateTriggers(outcome.Triggers)
			}
			if outcome.Kind != "" {
				t.Kind = outcome.Kind
			}
		})
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "target task not found: " + taskID})
			return
		}
		if len(outcome.Files) > 0 {
			if err := h.Files.WriteFiles(taskID, outcome.Files); err != nil {
				log.Printf("update write failed: %v", err)
			}
		}
		h.Bus.Publish(store.Event{
			Channel: "task:updated:" + store.DefaultUserID,
			UserID:  store.DefaultUserID,
			TaskID:  taskID,
			Payload: map[string]any{"task": task},
		})
		reply := outcome.Reply
		if reply == "" {
			reply = fmt.Sprintf("Updated task '%s'.", task.Name)
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, map[string]any{"taskId": taskID, "intent": "update"})
		writeJSON(w, http.StatusOK, map[string]any{
			"sessionId":   sessionID,
			"reply":       reply,
			"updatedTask": task,
		})

	default: // create
		kind := outcome.Kind
		if kind != "service" {
			kind = "oneshot"
		}
		task := h.Store.CreateTask(&store.Task{
			UserID:       store.DefaultUserID,
			Name:         outcome.Name,
			Description:  outcome.Description,
			Capabilities: outcome.Capabilities,
			Triggers:     translateTriggers(outcome.Triggers),
			Kind:         kind,
		})
		if err := h.Files.WriteFiles(task.ID, outcome.Files); err != nil {
			log.Printf("workflow write failed: %v", err)
		}
		h.Bus.Publish(store.Event{
			Channel: "task:created:" + store.DefaultUserID,
			UserID:  store.DefaultUserID,
			TaskID:  task.ID,
			Payload: map[string]any{"task": task},
		})
		reply := outcome.Reply
		if reply == "" {
			reply = fmt.Sprintf("Created task '%s'. Capabilities: %v.", task.Name, task.Capabilities)
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, map[string]any{"taskId": task.ID, "intent": "create"})
		writeJSON(w, http.StatusOK, map[string]any{
			"sessionId":   sessionID,
			"reply":       reply,
			"createdTask": task,
		})
	}
}

func (h *Handler) ListChatMessages(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "sessionId")
	writeJSON(w, http.StatusOK, map[string]any{"messages": h.Store.ListMessages(sessionID, 200)})
}

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": h.Store.ListSessions(store.DefaultUserID)})
}

type createSessionBody struct{ Title string `json:"title"` }

func (h *Handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var body createSessionBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Title == "" {
		body.Title = "Untitled"
	}
	s := h.Store.CreateSession(&store.Session{UserID: store.DefaultUserID, Title: body.Title})
	writeJSON(w, http.StatusCreated, s)
}

// --- memory -----------------------------------------------------------

func (h *Handler) ListMemory(w http.ResponseWriter, r *http.Request) {
	scope := store.MemoryScope(r.URL.Query().Get("scope"))
	scopeKey := r.URL.Query().Get("scopeKey")
	entries := h.Memory.List(store.DefaultUserID, scope, scopeKey)
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) DeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.Memory.Delete(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- evolution --------------------------------------------------------

func (h *Handler) ListEvolutionPRs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prs": h.Evolution.List(store.DefaultUserID)})
}

func (h *Handler) GetEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pr, err := h.Evolution.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (h *Handler) AcceptEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pr, err := h.Evolution.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if err := h.Engine.ApplyAccepted(pr); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	pr, _ = h.Evolution.Resolve(id, store.EvolutionAccepted)
	writeJSON(w, http.StatusOK, pr)
}

func (h *Handler) RejectEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pr, err := h.Evolution.Resolve(id, store.EvolutionRejected)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (h *Handler) ProposeEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, err := h.Store.GetTask(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	h.Engine.ManualPropose(task)
	w.WriteHeader(http.StatusAccepted)
}

// --- credentials ------------------------------------------------------

type createCredentialBody struct {
	Connector string         `json:"connector"`
	Label     string         `json:"label"`
	Secret    string         `json:"secret,omitempty"` // legacy single-string payload
	Value     map[string]any `json:"value,omitempty"`  // new: arbitrary JSON
	Scopes    []string       `json:"scopes"`
}

func (h *Handler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"credentials": h.Credentials.List(store.DefaultUserID)})
}

func (h *Handler) CreateCredential(w http.ResponseWriter, r *http.Request) {
	var body createCredentialBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Connector == "" || body.Label == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "connector and label required"})
		return
	}
	if body.Secret == "" && len(body.Value) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "either 'secret' or 'value' is required"})
		return
	}
	c, err := h.Credentials.Upsert(&store.Credential{
		UserID:    store.DefaultUserID,
		Connector: body.Connector,
		Label:     body.Label,
		Secret:    body.Secret,
		Value:     body.Value,
		Scopes:    body.Scopes,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Credentials.Delete(id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- connectors catalog / models --------------------------------------

func (h *Handler) ListConnectors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"connectors": connectorCatalog()})
}

func (h *Handler) ListModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"models": []map[string]any{
		{"id": "anthropic.claude-opus-4-7", "tier": "frontier", "use": "generate, evolve"},
		{"id": "anthropic.claude-opus-4-8", "tier": "frontier"},
		{"id": "anthropic.claude-haiku-4-5", "tier": "fast", "use": "reflect, run"},
	}})
}

func connectorCatalog() []map[string]any {
	return []map[string]any{
		{"id": "github", "name": "GitHub", "functions": []string{"issues.list", "issues.create", "pulls.list", "pulls.review"}, "triggers": []string{"github.issue_opened", "github.pr_opened", "github.push", "github.release"}, "auth": "PAT or OAuth"},
		{"id": "slack", "name": "Slack", "functions": []string{"chat.postMessage", "users.lookupByEmail"}, "triggers": []string{"slack.channel_message", "slack.mention", "slack.reaction_added"}, "auth": "Bot token or OAuth"},
		{"id": "jira", "name": "Jira", "functions": []string{"search", "issue.create", "issue.transition"}, "triggers": []string{"jira.issue_transition"}, "auth": "Email + API token"},
		{"id": "notion", "name": "Notion", "functions": []string{"pages.search", "pages.update"}, "triggers": []string{"notion.page_updated"}, "auth": "Integration token"},
		{"id": "linear", "name": "Linear", "functions": []string{"issues.list"}, "triggers": []string{"linear.issue_state"}, "auth": "API key"},
		{"id": "http", "name": "HTTP", "functions": []string{"request"}, "triggers": []string{"webhook"}, "auth": "none"},
	}
}

// --- webhook ----------------------------------------------------------

func (h *Handler) Webhook(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	token := chi.URLParam(r, "token")
	t, err := h.Store.GetTask(taskID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}

	// Find a webhook trigger and validate token.
	var matched *store.Trigger
	for i := range t.Triggers {
		if t.Triggers[i].Type != "webhook" || !t.Triggers[i].Enabled {
			continue
		}
		expected := webhookToken(t.ID, t.Triggers[i].ID)
		if hmac.Equal([]byte(expected), []byte(token)) {
			matched = &t.Triggers[i]
			break
		}
	}
	if matched == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid token"})
		return
	}

	// Optional HMAC payload signature if configured.
	if secret, ok := matched.Config["hmac_secret"].(string); ok && secret != "" {
		body, _ := io.ReadAll(r.Body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		want := hex.EncodeToString(mac.Sum(nil))
		got := r.Header.Get("X-Platform-Signature")
		if !hmac.Equal([]byte(want), []byte(got)) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "bad signature"})
			return
		}
	}

	run := h.Runner.Start(t, "webhook:"+matched.ID)
	writeJSON(w, http.StatusAccepted, run)
}

func webhookToken(taskID, triggerID string) string {
	secret := os.Getenv("PLATFORM_WEBHOOK_SECRET")
	if secret == "" {
		secret = "dev-webhook-secret"
	}
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%s|%s", taskID, triggerID)
	return hex.EncodeToString(mac.Sum(nil))[:32]
}

// --- discover / fork --------------------------------------------------

func (h *Handler) DiscoverTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}
	// Memory-search-driven for now (lexical+vector hybrid via memory store
	// is also reasonable). For Phase 1 simplicity, surface tasks whose
	// description fuzzy-matches the query.
	tasks := h.Store.ListTasks(store.DefaultUserID)
	results := []map[string]any{}
	lo := strings.ToLower(q)
	for _, t := range tasks {
		score := 0
		if strings.Contains(strings.ToLower(t.Name), lo) {
			score += 3
		}
		if strings.Contains(strings.ToLower(t.Description), lo) {
			score += 2
		}
		for _, c := range t.Capabilities {
			if strings.Contains(strings.ToLower(c), lo) {
				score++
			}
		}
		if score > 0 {
			results = append(results, map[string]any{
				"taskId":       t.ID,
				"name":         t.Name,
				"description":  t.Description,
				"capabilities": t.Capabilities,
				"score":        score,
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (h *Handler) ForkTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	src, err := h.Store.GetTask(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	yaml, _ := h.Files.ReadFile(id, "workflow.yaml")
	main, _ := h.Files.ReadFile(id, "main.ts")
	dup := h.Store.CreateTask(&store.Task{
		UserID:       store.DefaultUserID,
		Name:         src.Name + "-fork",
		Description:  "Fork of " + src.ID + ": " + src.Description,
		Capabilities: src.Capabilities,
		Triggers:     append([]store.Trigger{}, src.Triggers...),
	})
	_ = h.Files.WriteFiles(dup.ID, map[string]string{"workflow.yaml": yaml, "main.ts": main})
	writeJSON(w, http.StatusCreated, dup)
}

// --- dashboards -------------------------------------------------------

// ServeDashboard maps /d/<task_id>/<rest> to the dashboard build output for
// that task. Phase 1: serves a built static bundle from
// $PLATFORM_WORKFLOW_ROOT/<task_id>/dashboard/dist.
func (h *Handler) ServeDashboard(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	rest := chi.URLParam(r, "*")
	if rest == "" {
		rest = "index.html"
	}
	base := h.Files.Dir(taskID)
	dashDir := filepath.Join(base, "dashboard", "dist")
	full := filepath.Join(dashDir, rest)

	// Refuse path escapes.
	rel, err := filepath.Rel(dashDir, full)
	if err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}

	if info, err := os.Stat(full); err != nil || info.IsDir() {
		// Fallback to index.html for SPA-style routing.
		full = filepath.Join(dashDir, "index.html")
		if _, err := os.Stat(full); err != nil {
			http.Error(w, "no dashboard for this task", http.StatusNotFound)
			return
		}
	}
	http.ServeFile(w, r, full)
}

// --- helpers ----------------------------------------------------------

// looksLikeGreeting returns true for tiny chit-chat-only messages we should
// answer with a canned reply instead of round-tripping to the generator.
// Everything else hits the generator so the LLM can decide whether to build
// a workflow or just respond conversationally.
func looksLikeGreeting(s string) bool {
	t := strings.TrimSpace(strings.ToLower(s))
	if t == "" {
		return true
	}
	if len(t) > 24 {
		return false
	}
	greetings := []string{
		"hi", "hello", "hey", "yo", "안녕", "하이", "헬로",
		"ping", "test", "?", "help", "도움",
	}
	for _, g := range greetings {
		if t == g || t == g+"." || t == g+"!" || t == g+"?" {
			return true
		}
	}
	return false
}

func translateTriggers(in []generator.Trigger) []store.Trigger {
	out := make([]store.Trigger, 0, len(in))
	for _, t := range in {
		out = append(out, store.Trigger{
			ID: t.ID, Type: t.Type, Enabled: t.Enabled, Config: t.Config,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeSSE(w http.ResponseWriter, channel string, data any) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	envelope := map[string]any{"channel": channel, "payload": data}
	b, _ := json.Marshal(envelope)
	fmt.Fprintf(w, "data: %s\n\n", b)
}

func flush(w http.ResponseWriter) {
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(sub))
}
