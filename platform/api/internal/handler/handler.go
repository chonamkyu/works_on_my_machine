package handler

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"bytes"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nk/platform/api/internal/auth"
	"github.com/nk/platform/api/internal/generator"
	"github.com/nk/platform/api/internal/oauth"
	"github.com/nk/platform/api/internal/portreg"
	"github.com/nk/platform/api/internal/reflection"
	"github.com/nk/platform/api/internal/runner"
	"github.com/nk/platform/api/internal/store"
	"github.com/nk/platform/api/internal/verify"
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
	Services     *runner.ServiceManager
	Ports        *portreg.Registry
	Verify       *verify.Client
	Reflection   *reflection.Client
	SkillEvolver *runner.SkillEvolver

	verifMu     sync.Mutex
	verifByTask map[string]*verify.BrowseResult

	dryMu     sync.Mutex
	dryByTask map[string]*runner.DryRunResult

	// NodeRunner exposed for direct dry-run calls (RunDry).
	Node *runner.NodeRunner
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
		verifByTask: map[string]*verify.BrowseResult{},
		dryByTask:   map[string]*runner.DryRunResult{},
	}
}

// audit records a best-effort audit event. Errors are silently ignored so
// audit failures never block a request.
func (h *Handler) audit(r *http.Request, action, resType, resID, resName string) {
	ctx := r.Context()
	ws := auth.MustWorkspaceID(ctx)
	sub := auth.MustUserID(ctx)
	email := ""
	if c := auth.ClaimsFromCtx(ctx); c != nil {
		email = c.Email
	}
	_ = h.Store.RecordAudit(store.AuditEntry{
		ActorSub:     sub,
		ActorEmail:   email,
		Action:       action,
		ResourceType: resType,
		ResourceID:   resID,
		ResourceName: resName,
		WorkspaceID:  ws,
	})
}

// scheduleDryRun spawns the workflow with PLATFORM_DRY_RUN=1 right after
// files are written so generator hallucinations surface within seconds
// instead of at first real run. Result is stored per-task and injected into
// the next chat turn's generator context.
func (h *Handler) scheduleDryRun(taskID, sessionID, wsID string) {
	if h.Node == nil {
		return
	}
	go func() {
		t, err := h.Store.GetTask(taskID)
		if err != nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		res := h.Node.RunDry(ctx, t, 20*time.Second)
		h.dryMu.Lock()
		h.dryByTask[taskID] = &res
		h.dryMu.Unlock()
		h.Bus.Publish(store.Event{
			Channel: "chat:progress:" + sessionID,
			UserID:  wsID,
			Payload: map[string]any{"channel": "workflow:dryrun", "data": res},
		})
		if res.OK {
			log.Printf("dry-run %s: ok in %dms", taskID, res.DurationMs)
		} else {
			log.Printf("dry-run %s: FAILED %s (exit=%d timedOut=%v)", taskID, res.FatalError, res.ExitCode, res.TimedOut)
		}
	}()
}

func (h *Handler) lastDryRun(taskID string) *runner.DryRunResult {
	h.dryMu.Lock()
	defer h.dryMu.Unlock()
	return h.dryByTask[taskID]
}

func (h *Handler) GetDryRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
	v := h.lastDryRun(id)
	if v == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no dry-run yet"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// TriggerDryRun runs a dry-run synchronously and returns the result. Useful
// for the "Verify code" admin button and for tests.
func (h *Handler) TriggerDryRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if h.Node == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "node runner not wired"})
		return
	}
	t, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res := h.Node.RunDry(ctx, t, 20*time.Second)
	h.dryMu.Lock()
	h.dryByTask[id] = &res
	h.dryMu.Unlock()
	writeJSON(w, http.StatusOK, res)
}

// scheduleVerification spawns an async browser probe of the dashboard for a
// kind=service task. We wait up to ~20s for the supervisor to start the
// daemon and bind a port, then call the verify service. The result is stored
// in memory (latest per task) and published on chat:progress so the UI shows
// status; on the next chat turn the generator sees it via the task summary.
func (h *Handler) scheduleVerification(taskID, sessionID, wsID string) {
	if h.Verify == nil {
		return
	}
	go func() {
		// Wait for the port to be allocated by the supervisor (≤ ~10s).
		var port int
		for i := 0; i < 30; i++ {
			if h.Ports != nil {
				if p, ok := h.Ports.Lookup(taskID); ok {
					port = p
					break
				}
			}
			time.Sleep(400 * time.Millisecond)
		}
		if port == 0 {
			log.Printf("verify: no port allocated for %s within timeout", taskID)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		url := fmt.Sprintf("http://127.0.0.1:%d/", port)
		res, err := h.Verify.Browse(ctx, url, taskID, 15)
		if err != nil {
			log.Printf("verify call failed for %s: %v", taskID, err)
			return
		}
		h.verifMu.Lock()
		h.verifByTask[taskID] = res
		h.verifMu.Unlock()
		h.Bus.Publish(store.Event{
			Channel: "chat:progress:" + sessionID,
			UserID:  wsID,
			Payload: map[string]any{"channel": "workflow:verify", "data": res},
		})
		log.Printf("verify %s: ok=%v errors=%v", taskID, res.OK, res.Errors)
	}()
}

// lastVerification returns the most recent browser verification result for a
// task (nil if none yet).
func (h *Handler) lastVerification(taskID string) *verify.BrowseResult {
	h.verifMu.Lock()
	defer h.verifMu.Unlock()
	return h.verifByTask[taskID]
}

// gatherUserContext retrieves the top-K user-scope memories relevant to the
// current chat turn. Pinned memories are always surfaced first; the rest fill
// up to `topK` slots via the memory store's hybrid vector+lexical search.
func (h *Handler) gatherUserContext(userID, query string, history []generator.HistoryMessage) []*store.MemoryEntry {
	if h.Memory == nil {
		return nil
	}
	const topK = 8
	// Combine the latest message with the previous user message so retrieval
	// sees a tiny bit of conversational context. Strict cap so we don't pass
	// 10KB+ histories into the embedding call.
	q := query
	for i := len(history) - 1; i >= 0 && len(q) < 600; i-- {
		if history[i].Role == "user" {
			q = history[i].Content + "\n" + q
			break
		}
	}
	results := h.Memory.Search(userID, q, topK)

	// Always include any pinned memory the user explicitly anchored, even if
	// it didn't match the query — those represent hard rules.
	pinned := []*store.MemoryEntry{}
	for _, m := range h.Memory.List(userID, store.ScopeUser, "") {
		if m.Pinned {
			pinned = append(pinned, m)
		}
	}
	seen := map[string]bool{}
	merged := make([]*store.MemoryEntry, 0, topK)
	for _, m := range pinned {
		if !seen[m.ID] {
			merged = append(merged, m)
			seen[m.ID] = true
		}
	}
	for _, m := range results {
		if len(merged) >= topK {
			break
		}
		if !seen[m.ID] {
			merged = append(merged, m)
			seen[m.ID] = true
		}
	}
	// Utility tracking — bump read_count + last_read_at so future pruning can
	// distinguish actually-useful memories from dead weight.
	if len(merged) > 0 && h.Store != nil {
		ids := make([]string, 0, len(merged))
		for _, m := range merged {
			ids = append(ids, m.ID)
		}
		go h.Store.NoteMemoryRead(ids)
	}
	return merged
}

// looksLikeCorrection heuristically flags a user message as pushing back on
// the previous agent reply. Used as a hint for the chat reflector.
func looksLikeCorrection(text string) bool {
	if text == "" {
		return false
	}
	low := strings.ToLower(text)
	for _, m := range []string{
		"아니", "그게 아니", "다시", "잘못", "틀렸",
		"no, ", "no.", "nope", "that's not", "wrong",
		"don't ", "stop ", "again", "redo",
	} {
		if strings.HasPrefix(low, m) || strings.Contains(low, " "+m) {
			return true
		}
	}
	return false
}

// scheduleChatReflection fires the per-turn chat reflector in the background.
// The result becomes user-scope memory + (later) profile suggestions. Failures
// are non-fatal — the user already got their reply.
func (h *Handler) scheduleChatReflection(userMsg, agentReply, sessionID, userID string, history []generator.HistoryMessage, known []*store.MemoryEntry) {
	if h.Reflection == nil || h.Reflection.BaseURL == "" || h.Memory == nil {
		return
	}
	if strings.TrimSpace(userMsg) == "" || strings.TrimSpace(agentReply) == "" {
		return
	}
	// Snapshot inputs so the goroutine doesn't race with later mutations.
	historyCopy := append([]generator.HistoryMessage(nil), history...)
	knownSnips := make([]reflection.MemorySnippet, 0, len(known))
	for _, m := range known {
		knownSnips = append(knownSnips, reflection.MemorySnippet{Topic: m.Topic, Content: m.Content})
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		recent := make([]reflection.ChatTurn, 0, len(historyCopy))
		for _, m := range historyCopy {
			recent = append(recent, reflection.ChatTurn{Role: m.Role, Content: m.Content})
		}
		res, err := h.Reflection.ReflectChat(ctx, reflection.ChatReflectRequest{
			UserID:       userID,
			SessionID:    sessionID,
			UserMessage:  userMsg,
			AgentReply:   agentReply,
			IsCorrection: looksLikeCorrection(userMsg),
			RecentTurns:  recent,
			KnownMemory:  knownSnips,
		})
		if err != nil {
			log.Printf("chat reflection failed: %v", err)
			return
		}
		written := 0
		for _, upd := range res.MemoryUpdates {
			entry := h.Memory.Write(&store.MemoryEntry{
				UserID:      userID,
				WorkspaceID: userID,
				Scope:       store.ScopeUser,
				Topic:       upd.Topic,
				Content:     upd.Content,
				Pinned:      upd.Pinned,
			})
			written++
			h.Bus.Publish(store.Event{
				Channel: "memory:written:" + userID,
				UserID:  userID,
				Payload: map[string]any{"entry": entry, "source": "chat-reflect", "signal": res.Signal},
			})
		}
		// Phase 2: profile suggestions become a row in user_profile_suggestions
		// for the user to accept/reject. We persist but don't auto-apply.
		if h.Store != nil {
			for k, v := range res.ProfileSuggestions {
				_ = h.Store.AppendProfileSuggestion(userID, k, v, sessionID)
			}
		}
		log.Printf("chat reflection: session=%s signal=%s memories=%d", sessionID, res.Signal, written)
	}()
}

// --- Phase 3: skill proposal + per-user overlay ---

// ProposeSkill asks the reflection service to look at the user's accumulated
// memories + recent corrections and propose a NEW skill file. The user
// reviews and (if accepted) the file is dropped into the per-user overlay
// so the generator loads it from then on.
func (h *Handler) ProposeSkill(w http.ResponseWriter, r *http.Request) {
	if h.Reflection == nil || h.Memory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "reflection or memory not wired"})
		return
	}
	userID := auth.MustWorkspaceID(r.Context())
	mems := h.Memory.List(userID, store.ScopeUser, "")
	memMeta := make([]reflection.MemoryWithMeta, 0, len(mems))
	for _, m := range mems {
		memMeta = append(memMeta, reflection.MemoryWithMeta{Topic: m.Topic, Content: m.Content, Pinned: m.Pinned})
	}
	// Recent corrections from suggestions (which are downstream of corrections).
	corrections := []string{}
	for _, s := range h.Store.ListProfileSuggestions(userID, "pending") {
		corrections = append(corrections, fmt.Sprintf("%s -> %s", s.Field, s.Value))
	}
	existing := loadOverlaySkills(userID)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := h.Reflection.ProposeSkill(ctx, reflection.SkillProposeRequest{
		UserID:            userID,
		Memories:          memMeta,
		RecentCorrections: corrections,
		ExistingSkills:    existing,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	if res.Confidence == "low" || strings.TrimSpace(res.SkillBody) == "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"skipped":    true,
			"confidence": res.Confidence,
			"rationale":  res.Rationale,
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// AcceptSkill writes the proposed skill into the per-user overlay directory.
// The next /generate call will pick it up via the hot-reload loader.
func (h *Handler) AcceptSkill(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		Body        string `json:"body"`
		AppliesWhen string `json:"appliesWhen"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if body.Name == "" || body.Body == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and body required"})
		return
	}
	// Sanitize: only kebab-case slug allowed as filename.
	slug := strings.ToLower(strings.TrimSpace(body.Name))
	cleaned := []rune{}
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			cleaned = append(cleaned, r)
		}
	}
	slug = string(cleaned)
	if slug == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name must be kebab-case slug"})
		return
	}
	dir := perUserSkillsDir(auth.MustWorkspaceID(r.Context()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	content := body.Body
	if body.AppliesWhen != "" && body.AppliesWhen != "always" {
		content = "---\nactivate_when: " + body.AppliesWhen + "\n---\n\n" + content
	}
	path := filepath.Join(dir, slug+".md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": path, "slug": slug})
}

func (h *Handler) MemoryStats(w http.ResponseWriter, r *http.Request) {
	if h.Memory == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, h.Memory.Stats(auth.MustWorkspaceID(r.Context())))
}

func (h *Handler) PruneMemory(w http.ResponseWriter, r *http.Request) {
	if h.Memory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "memory not wired"})
		return
	}
	n, err := h.Memory.PruneStale(auth.MustWorkspaceID(r.Context()), 90, 60, 0)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"archived": n})
}

// ListUserSkills returns the per-user skill overlay files.
func (h *Handler) ListUserSkills(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"skills": loadOverlaySkills(auth.MustWorkspaceID(r.Context()))})
}

// --- Skill proposal lifecycle (Phase 4 nightly evolver writes here) ---

func (h *Handler) ListSkillProposals(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	writeJSON(w, http.StatusOK, map[string]any{
		"proposals": h.Store.ListSkillProposals(auth.MustWorkspaceID(r.Context()), status),
	})
}

func (h *Handler) AcceptSkillProposal(w http.ResponseWriter, r *http.Request) {
	// Accepting a global skill proposal is a platform-admin action.
	if !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "platform-admin required"})
		return
	}
	id := chi.URLParam(r, "id")
	p, err := h.Store.GetSkillProposal(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	// Write the file to the global skills dir — proposals are global scope,
	// so we must not write to an attacker-controlled per-user path.
	dir := perUserSkillsDir(store.GlobalScope)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	content := p.SkillBody
	if p.AppliesWhen != "" && p.AppliesWhen != "always" {
		content = "---\nactivate_when: " + p.AppliesWhen + "\n---\n\n" + content
	}
	if err := os.WriteFile(filepath.Join(dir, p.SkillName+".md"), []byte(content), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	resolved, _ := h.Store.ResolveSkillProposal(id, "accepted")
	writeJSON(w, http.StatusOK, resolved)
}

func (h *Handler) RejectSkillProposal(w http.ResponseWriter, r *http.Request) {
	// Rejecting a global skill proposal is a platform-admin action.
	if !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "platform-admin required"})
		return
	}
	id := chi.URLParam(r, "id")
	resolved, err := h.Store.ResolveSkillProposal(id, "rejected")
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resolved)
}

// TriggerSkillEvolve kicks the periodic evolver immediately ("Evolve now" UX).
func (h *Handler) TriggerSkillEvolve(w http.ResponseWriter, r *http.Request) {
	if h.SkillEvolver == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "skill evolver not wired"})
		return
	}
	go h.SkillEvolver.RunOnce(store.GlobalScope)
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "running"})
}

func (h *Handler) DeleteUserSkill(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" || strings.Contains(name, "..") || strings.Contains(name, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad name"})
		return
	}
	path := filepath.Join(perUserSkillsDir(auth.MustWorkspaceID(r.Context())), name+".md")
	if err := os.Remove(path); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func perUserSkillsDir(userID string) string {
	// The generator process owns the canonical prompts/ dir. We write to the
	// same on-disk location so the next /generate request reloads from there.
	root := os.Getenv("PLATFORM_GENERATOR_PROMPTS_DIR")
	if root == "" {
		// Default to ../generator/prompts relative to the api binary's CWD.
		wd, _ := os.Getwd()
		root = filepath.Join(filepath.Dir(wd), "generator", "prompts")
	}
	return filepath.Join(root, "users", userID, "skills")
}

func loadOverlaySkills(userID string) []reflection.ExistingSkill {
	dir := perUserSkillsDir(userID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []reflection.ExistingSkill{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, reflection.ExistingSkill{
			Name: strings.TrimSuffix(e.Name(), ".md"),
			Body: string(body),
		})
	}
	return out
}

// --- Profile + suggestion HTTP surface ---

func (h *Handler) GetUserProfile(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	wsID := auth.MustWorkspaceID(r.Context())
	p, err := h.Store.GetUserProfile(wsID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if p == nil {
		writeJSON(w, http.StatusOK, &store.UserProfile{UserID: wsID})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (h *Handler) PutUserProfile(w http.ResponseWriter, r *http.Request) {
	var p store.UserProfile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	p.UserID = auth.MustWorkspaceID(r.Context())
	if err := h.Store.UpsertUserProfile(&p); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, &p)
}

func (h *Handler) ListProfileSuggestions(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	writeJSON(w, http.StatusOK, map[string]any{
		"suggestions": h.Store.ListProfileSuggestions(auth.MustWorkspaceID(r.Context()), status),
	})
}

func (h *Handler) ResolveProfileSuggestion(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Status != "accepted" && body.Status != "rejected" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "status must be accepted or rejected"})
		return
	}
	s, err := h.Store.ResolveProfileSuggestion(id, body.Status, auth.MustWorkspaceID(r.Context()))
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
		return
	}
	// On accept, write the value into user_profile so the next chat uses it.
	if body.Status == "accepted" {
		p, _ := h.Store.GetUserProfile(s.UserID)
		if p == nil {
			p = &store.UserProfile{UserID: s.UserID}
		}
		switch s.Field {
		case "preferred_language":
			p.PreferredLanguage = s.Value
		case "reply_tone":
			p.ReplyTone = s.Value
		case "expertise_level":
			p.ExpertiseLevel = s.Value
		default:
			// Unknown field — store as a hard rule prefixed with the field name.
			p.HardRules = append(p.HardRules, fmt.Sprintf("%s: %s", s.Field, s.Value))
		}
		_ = h.Store.UpsertUserProfile(p)
	}
	writeJSON(w, http.StatusOK, s)
}

// loadUserProfileMap returns the stable user-profile fields the generator
// should honor verbatim. Includes any accepted suggestions; pending ones live
// in user_profile_suggestions.
func (h *Handler) loadUserProfileMap(userID string) map[string]any {
	if h.Store == nil {
		return nil
	}
	p, err := h.Store.GetUserProfile(userID)
	if err != nil || p == nil {
		return nil
	}
	out := map[string]any{}
	if p.PreferredLanguage != "" {
		out["preferred_language"] = p.PreferredLanguage
	}
	if p.ReplyTone != "" {
		out["reply_tone"] = p.ReplyTone
	}
	if p.ExpertiseLevel != "" {
		out["expertise_level"] = p.ExpertiseLevel
	}
	if len(p.HardRules) > 0 {
		out["hard_rules"] = p.HardRules
	}
	if len(p.DefaultRepos) > 0 {
		out["default_repos"] = p.DefaultRepos
	}
	if len(p.DefaultChannels) > 0 {
		out["default_channels"] = p.DefaultChannels
	}
	if len(p.DefaultModels) > 0 {
		out["default_models"] = p.DefaultModels
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (h *Handler) GetVerification(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
	v := h.lastVerification(id)
	if v == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no verification yet"})
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// GetVerificationScreenshot streams the screenshot PNG from disk. The path is
// captured in the verification result; we only serve files under the
// platform's verifications directory.
func (h *Handler) GetVerificationScreenshot(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
	v := h.lastVerification(id)
	if v == nil || v.ScreenshotPath == "" {
		http.Error(w, "no screenshot", http.StatusNotFound)
		return
	}
	home, _ := os.UserHomeDir()
	root := filepath.Join(home, ".platform", "verifications")
	abs, err := filepath.Abs(v.ScreenshotPath)
	if err != nil || !strings.HasPrefix(abs, root) {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		http.Error(w, "read failed", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/png")
	_, _ = io.Copy(w, f)
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
	url, err := h.OAuth.Start(body.Provider, body.Label, auth.MustWorkspaceID(r.Context()), body.Scopes)
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

// --- ownership helpers ------------------------------------------------

// taskForRequest fetches a task by id and enforces that it belongs to the
// caller's active workspace (admins bypass). Writes 404 and returns ok=false
// otherwise — 404, not 403, so we don't leak existence across tenants.
func (h *Handler) taskForRequest(w http.ResponseWriter, r *http.Request, id string) (*store.Task, bool) {
	t, err := h.Store.GetTask(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	ws := auth.MustWorkspaceID(r.Context())
	if t.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	return t, true
}

// runForRequest fetches a run by id and enforces workspace ownership via its
// parent task. Writes 404 and returns ok=false when the run doesn't exist or
// belongs to a different workspace.
func (h *Handler) runForRequest(w http.ResponseWriter, r *http.Request, id string) (*store.Run, bool) {
	run, err := h.Store.GetRun(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	ws := auth.MustWorkspaceID(r.Context())
	if run.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	return run, true
}

// sessionForRequest fetches a session by id and enforces workspace ownership.
// Writes 404 and returns ok=false when the session doesn't exist or belongs
// to a different workspace.
func (h *Handler) sessionForRequest(w http.ResponseWriter, r *http.Request, id string) (*store.Session, bool) {
	sess, err := h.Store.GetSession(id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	ws := auth.MustWorkspaceID(r.Context())
	if sess.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return nil, false
	}
	return sess, true
}

// sessionOwned is a non-writing session lookup that distinguishes three cases:
//   - (sess, true,  true)  — session exists and belongs to caller's workspace
//   - (nil,  true,  false) — session exists but is owned by a different workspace
//   - (nil,  false, false) — session row does not exist at all
//
// It never writes to the ResponseWriter; callers decide what to do with each case.
func (h *Handler) sessionOwned(r *http.Request, id string) (*store.Session, bool, bool) {
	sess, err := h.Store.GetSession(id)
	if err != nil {
		return nil, false, false // not found
	}
	ws := auth.MustWorkspaceID(r.Context())
	if sess.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		return nil, true, false // found but wrong workspace
	}
	return sess, true, true
}

// --- tasks ------------------------------------------------------------

func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"tasks": h.Store.ListTasks(auth.MustWorkspaceID(r.Context()))})
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
	wsID := auth.MustWorkspaceID(r.Context())
	t := h.Store.CreateTask(&store.Task{
		UserID:       wsID,
		WorkspaceID:  wsID,
		Name:         body.Name,
		Description:  body.Description,
		Triggers:     body.Triggers,
		Capabilities: body.Capabilities,
	})
	h.Bus.Publish(store.Event{
		Channel: "task:created:" + wsID,
		UserID:  wsID,
		TaskID:  t.ID,
		Payload: map[string]any{"task": t},
	})
	h.audit(r, "task.create", "task", t.ID, t.Name)
	writeJSON(w, http.StatusCreated, t)
}

func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, t)
}

type updateTaskBody struct {
	Name         *string            `json:"name,omitempty"`
	Description  *string            `json:"description,omitempty"`
	Triggers     *[]store.Trigger   `json:"triggers,omitempty"`
	Capabilities *[]string          `json:"capabilities,omitempty"`
	Model        *store.ModelConfig `json:"model,omitempty"`
}

func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
	var body updateTaskBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	t, err := h.Store.UpdateTask(id, func(t *store.Task) {
		if body.Name != nil {
			name := strings.TrimSpace(*body.Name)
			if name != "" {
				t.Name = name
			}
		}
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
	task, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	h.archiveBeforeDelete(id)
	if err := h.Store.DeleteTask(id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	// Best-effort: remove the on-disk source so the supervisor can't pick
	// the task back up. The archive table already has a copy.
	_ = os.RemoveAll(h.Files.Dir(task.WorkspaceID, id))
	h.Bus.Publish(store.Event{
		Channel: "task:archived:" + auth.MustWorkspaceID(r.Context()),
		UserID:  auth.MustWorkspaceID(r.Context()),
		TaskID:  id,
	})
	h.audit(r, "task.delete", "task", id, "")
	w.WriteHeader(http.StatusNoContent)
}

// archiveBeforeDelete snapshots the task row + on-disk source files into the
// task_archive table. The live `tasks` row and disk files are deleted right
// after this returns — the archive copy is what stays around.
func (h *Handler) archiveBeforeDelete(id string) {
	t, err := h.Store.GetTask(id)
	if err != nil {
		return
	}
	files := map[string]string{}
	dir := h.Files.Dir(t.WorkspaceID, id)
	for _, name := range []string{"main.ts", "workflow.yaml"} {
		if body, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
			files[name] = string(body)
		}
	}
	if err := h.Store.ArchiveTask(t, files); err != nil {
		log.Printf("archive task %s failed: %v", id, err)
	}
}

func (h *Handler) ListArchivedTasks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"tasks": h.Store.ListArchivedTasks(auth.MustWorkspaceID(r.Context())),
	})
}

func (h *Handler) GetArchivedTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := h.Store.GetArchivedTask(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	ws := auth.MustWorkspaceID(r.Context())
	if a.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// SetServiceEnabled flips a service-kind task on or off. When disabled, the
// supervisor stops the daemon on the next reconcile (≤5s).
func (h *Handler) SetServiceEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
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
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
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
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
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
	t, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	var body triggerRunBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Trigger == "" {
		body.Trigger = "manual"
	}
	run := h.Runner.Start(t, body.Trigger)
	h.audit(r, "run.trigger", "run", run.ID, "")
	writeJSON(w, http.StatusAccepted, run)
}

func (h *Handler) ListRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": h.Store.ListRunsForTask(id)})
}

func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, ok := h.runForRequest(w, r, id)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "logs": h.Store.GetLogs(id)})
}

func (h *Handler) CancelRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.runForRequest(w, r, id); !ok {
		return
	}
	if !h.Runner.Cancel(id) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no active run"})
		return
	}
	h.audit(r, "run.cancel", "run", id, "")
	w.WriteHeader(http.StatusAccepted)
}

// --- code -------------------------------------------------------------

// GetTaskCode returns every authored source file in the task tree. It walks
// the workflow's on-disk dir but excludes runtime cruft (node_modules,
// generated dist, the launcher we inject, lockfiles) so the UI shows what
// the agent actually shipped, not what the runner created.
func (h *Handler) GetTaskCode(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	dir := h.Files.Dir(task.WorkspaceID, id)
	if _, err := os.Stat(dir); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no code"})
		return
	}

	files := map[string]string{}
	skipDir := map[string]bool{"node_modules": true, "dist": true, ".git": true, ".turbo": true}
	skipFile := map[string]bool{"_launcher.mjs": true, "pnpm-lock.yaml": true, "package-lock.json": true, "yarn.lock": true}
	const maxBytes = 256 * 1024 // 256KB per file; bigger gets truncated

	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		// Walk skips by returning filepath.SkipDir.
		if info.IsDir() {
			if skipDir[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if skipFile[info.Name()] {
			return nil
		}
		// Read up to maxBytes so a stray giant blob doesn't blow up the
		// response — we trim and mark with a sentinel.
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		body := string(b)
		if len(body) > maxBytes {
			body = body[:maxBytes] + "\n\n… (truncated; file is " + strconv.Itoa(len(b)) + " bytes)"
		}
		files[filepath.ToSlash(rel)] = body
		return nil
	})

	if len(files) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no code"})
		return
	}
	writeJSON(w, http.StatusOK, files)
}

// --- SSE --------------------------------------------------------------

func (h *Handler) RunStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.runForRequest(w, r, id); !ok {
		return
	}
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
	// If the caller is authenticated, enforce workspace ownership.
	// Unauthenticated SSE connections (e.g. iframe polling) pass through.
	if auth.ClaimsFromCtx(r.Context()) != nil {
		if _, ok := h.taskForRequest(w, r, id); !ok {
			return
		}
	}
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
	ws := auth.MustWorkspaceID(r.Context())
	patterns := []string{
		"trigger:fired:*", "run:state:*",
		"task:created:*", "task:updated:*", "task:archived:*",
		"evolution:proposed:*", "memory:written:*", "reflection:complete:*",
		"chat:progress:*",
	}
	cancels := make([]func(), 0, len(patterns))
	merged := make(chan store.Event, 64)
	for _, p := range patterns {
		ch, cancel := h.Bus.Subscribe(p)
		cancels = append(cancels, cancel)
		go func(c <-chan store.Event) {
			for ev := range c {
				if ev.UserID != "" && ev.UserID != ws {
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
	// Verify session/task belongs to the caller's workspace BEFORE appending
	// any message — prevents ghost sessions with workspace_id="" from being
	// created for arbitrary IDs. The 1:1 session==task model means a task
	// match is sufficient when a bare session row doesn't exist yet.
	//
	// Use the non-writing sessionOwned so that when the session row simply
	// doesn't exist yet (first message to a task), we can fall through to the
	// task ownership check without having already sent a 404 response.
	_, found, owned := h.sessionOwned(r, sessionID)
	if found && !owned {
		// Session exists but belongs to a different workspace — hard 404.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !found {
		// Session row doesn't exist yet (first-message-to-task flow where
		// sessionID == taskID). Fall back to task ownership check.
		if _, taskOK := h.taskForRequest(w, r, sessionID); !taskOK {
			return
		}
	}
	var body chatMessageBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	// Persist user message.
	_, _ = h.Store.AppendMessage(sessionID, "user", body.Content, nil)

	// session == task in our 1:1 model. If the task is a chat-handler
	// agent (workflow.chatHandler), route the message directly to the
	// daemon UNLESS the user prefixed a slash command (/edit, /code, …),
	// which means "talk to the meta-agent about this task instead".
	content := strings.TrimSpace(body.Content)
	if t, err := h.Store.GetTask(sessionID); err == nil && t.HasChatHandler && !strings.HasPrefix(content, "/") {
		h.routeChatToTaskHandler(w, t, sessionID, body.Content)
		return
	}

	// Slash command shortcuts. Cheap regex routes — bypass generator for
	// the common ones, fall through to generator for unknown slashes (so
	// `/edit do X` reaches it with the `/edit ` stripped).
	if strings.HasPrefix(content, "/") && handleSlashCommand(h, w, r, sessionID, content) {
		return
	}

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

	wsID := auth.MustWorkspaceID(r.Context())

	connectors := []string{}
	for _, c := range h.Credentials.List(wsID) {
		connectors = append(connectors, c.Connector)
	}

	historyMsgs := h.Store.ListMessages(sessionID, 200)
	history := make([]generator.HistoryMessage, 0, len(historyMsgs))
	for _, m := range historyMsgs {
		history = append(history, generator.HistoryMessage{Role: m.Role, Content: m.Content})
	}

	existing := []generator.ExistingTask{}
	for _, t := range h.Store.ListTasks(wsID) {
		mainTs, _ := h.Files.ReadFile(t.WorkspaceID, t.ID, "main.ts")
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
		ex := generator.ExistingTask{
			ID: t.ID, Name: t.Name, Description: t.Description, Kind: t.Kind,
			Capabilities: t.Capabilities, Triggers: gtTriggers, MainTs: mainTs,
			LastRunState: lastState, LastRunError: lastError, RecentLogTail: tail,
		}
		if v := h.lastVerification(t.ID); v != nil {
			ex.LastVerification = &generator.VerificationSnapshot{
				OK:            v.OK,
				Errors:        v.Errors,
				ConsoleErrors: v.ConsoleErrors,
				PageErrors:    v.PageErrors,
				Status:        v.Status,
				TextChars:     v.TextChars,
				DurationMs:    v.DurationMs,
			}
		}
		if d := h.lastDryRun(t.ID); d != nil {
			// Keep only the tail of stderr so we don't blow up the prompt
			// — three lines is usually enough to point the agent at the
			// failing call.
			tail := d.Stderr
			if len(tail) > 8 {
				tail = tail[len(tail)-8:]
			}
			ex.LastDryRun = &generator.DryRunSnapshot{
				OK:         d.OK,
				ExitCode:   d.ExitCode,
				FatalError: d.FatalError,
				Stderr:     tail,
				DurationMs: d.DurationMs,
				TimedOut:   d.TimedOut,
			}
		}
		existing = append(existing, ex)
	}

	// In the 1:1 chat-per-task model, sessionID == taskID. Surface whether
	// that task is a blank draft so the generator can fill it in-place
	// instead of spawning yet another sibling.
	activeTaskID := sessionID
	activeIsDraft := false
	if t, err := h.Store.GetTask(activeTaskID); err == nil {
		main, _ := h.Files.ReadFile(t.WorkspaceID, activeTaskID, "main.ts")
		if main == "" && len(t.Triggers) == 0 && len(t.Capabilities) == 0 {
			activeIsDraft = true
		}
	} else {
		activeTaskID = ""
	}

	// Phase 1 self-tuning: retrieve user-scope memories that match this turn
	// and any pinned memories the user explicitly anchored. Both go into the
	// generator request so the agent can adapt to known preferences.
	retrievedMem := h.gatherUserContext(wsID, body.Content, history)
	memHints := make([]generator.MemoryHint, 0, len(retrievedMem))
	for _, m := range retrievedMem {
		memHints = append(memHints, generator.MemoryHint{Topic: m.Topic, Content: m.Content, Pinned: m.Pinned})
	}
	// Phase 2 self-tuning: load the user's stable profile (preferred language,
	// reply tone, hard rules, etc.) and pass verbatim.
	userProfile := h.loadUserProfileMap(wsID)

	outcome, err := h.Generator.Generate(ctx, generator.Request{
		Prompt:              body.Content,
		SessionID:           sessionID,
		UserID:              wsID,
		ActiveTaskID:        activeTaskID,
		ActiveTaskIsDraft:   activeIsDraft,
		ConnectorsAvailable: connectors,
		History:             history,
		ExistingTasks:       existing,
		UserContext:         memHints,
		UserProfile:         userProfile,
	}, func(channel string, payload json.RawMessage) {
		var data any
		_ = json.Unmarshal(payload, &data)
		h.Bus.Publish(store.Event{
			Channel: "chat:progress:" + sessionID,
			UserID:  wsID,
			Payload: map[string]any{"channel": channel, "data": data},
		})
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

	// Phase 1 self-tuning: kick off chat reflection asynchronously after the
	// reply is computed. It extracts user-level preferences/corrections from
	// (user msg, agent reply) and persists them as memory_entries; future
	// turns will see them via gatherUserContext.
	h.scheduleChatReflection(body.Content, outcome.Reply, sessionID, wsID, history, retrievedMem)

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
			for _, c := range h.Credentials.List(wsID) {
				if c.Connector == mc.Connector && (mc.Label == "" || c.Label == mc.Label) {
					_ = h.Credentials.Delete(c.ID, wsID)
				}
			}
		default: // upsert
			_, err := h.Credentials.Upsert(&store.Credential{
				UserID:      wsID,
				WorkspaceID: wsID,
				Connector:   mc.Connector,
				Label:       mc.Label,
				Value:       mc.Value,
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
			h.archiveBeforeDelete(id)
			if err := h.Store.DeleteTask(id); err == nil {
				_ = os.RemoveAll(h.Files.Dir(wsID, id))
				deleted = append(deleted, id)
				h.Bus.Publish(store.Event{
					Channel: "task:archived:" + wsID,
					UserID:  wsID,
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
			if outcome.Ports != nil {
				t.Ports = translatePorts(outcome.Ports)
			}
			if mainTs, ok := outcome.Files["main.ts"]; ok {
				t.HasChatHandler = detectChatHandler(mainTs)
				if t.HasChatHandler {
					// Chat agents must run as services and bind a chat port.
					if t.Kind != "service" {
						t.Kind = "service"
					}
					t.Ports = ensureChatPort(t.Ports)
				}
			}
		})
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "target task not found: " + taskID})
			return
		}
		if len(outcome.Files) > 0 {
			if err := h.Files.WriteFiles(task.WorkspaceID, taskID, outcome.Files); err != nil {
				log.Printf("update write failed: %v", err)
			}
			h.scheduleDryRun(taskID, sessionID, wsID)
			if task.Kind == "service" && h.Services != nil {
				h.Services.Restart(taskID)
				h.scheduleVerification(taskID, sessionID, wsID)
			}
		}
		h.Bus.Publish(store.Event{
			Channel: "task:updated:" + wsID,
			UserID:  wsID,
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
		ports := translatePorts(outcome.Ports)
		hasChat := detectChatHandler(outcome.Files["main.ts"])
		if hasChat {
			if kind != "service" {
				kind = "service"
			}
			ports = ensureChatPort(ports)
		}
		task := h.Store.CreateTask(&store.Task{
			UserID:         wsID,
			WorkspaceID:    wsID,
			Name:           outcome.Name,
			Description:    outcome.Description,
			Capabilities:   outcome.Capabilities,
			Triggers:       translateTriggers(outcome.Triggers),
			Kind:           kind,
			Ports:          ports,
			HasChatHandler: hasChat,
		})
		if err := h.Files.WriteFiles(task.WorkspaceID, task.ID, outcome.Files); err != nil {
			log.Printf("workflow write failed: %v", err)
		}
		h.scheduleDryRun(task.ID, sessionID, wsID)
		if task.Kind == "service" {
			h.scheduleVerification(task.ID, sessionID, wsID)
		}
		h.Bus.Publish(store.Event{
			Channel: "task:created:" + wsID,
			UserID:  wsID,
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
	// Use non-writing sessionOwned to avoid double-write when the session row
	// doesn't exist yet (first-message-to-task flow where sessionID == taskID).
	_, found, owned := h.sessionOwned(r, sessionID)
	if found && !owned {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if !found {
		// In 1:1 model session == task; fall back to task ownership.
		if _, taskOK := h.taskForRequest(w, r, sessionID); !taskOK {
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": h.Store.ListMessages(sessionID, 200)})
}

func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": h.Store.ListSessions(auth.MustWorkspaceID(r.Context()))})
}

type createSessionBody struct{ Title string `json:"title"` }

func (h *Handler) CreateSession(w http.ResponseWriter, r *http.Request) {
	var body createSessionBody
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Title == "" {
		body.Title = "Untitled"
	}
	wsID := auth.MustWorkspaceID(r.Context())
	s := h.Store.CreateSession(&store.Session{UserID: wsID, WorkspaceID: wsID, Title: body.Title})
	writeJSON(w, http.StatusCreated, s)
}

// --- memory -----------------------------------------------------------

func (h *Handler) ListMemory(w http.ResponseWriter, r *http.Request) {
	scope := store.MemoryScope(r.URL.Query().Get("scope"))
	scopeKey := r.URL.Query().Get("scopeKey")
	entries := h.Memory.List(auth.MustWorkspaceID(r.Context()), scope, scopeKey)
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (h *Handler) DeleteMemory(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !h.Memory.Delete(id, auth.MustWorkspaceID(r.Context())) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- evolution --------------------------------------------------------

func (h *Handler) ListEvolutionPRs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"prs": h.Evolution.List(auth.MustWorkspaceID(r.Context()))})
}

func (h *Handler) GetEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pr, err := h.Evolution.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ws := auth.MustWorkspaceID(r.Context())
	if pr.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
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
	ws := auth.MustWorkspaceID(r.Context())
	if pr.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	if err := h.Engine.ApplyAccepted(pr); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if h.Services != nil {
		if t, err := h.Store.GetTask(pr.TaskID); err == nil && t.Kind == "service" {
			h.Services.Restart(pr.TaskID)
		}
	}
	pr, _ = h.Evolution.Resolve(id, store.EvolutionAccepted)
	writeJSON(w, http.StatusOK, pr)
}

func (h *Handler) RejectEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	pr, err := h.Evolution.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	ws := auth.MustWorkspaceID(r.Context())
	if pr.WorkspaceID != ws && !auth.IsAdmin(r.Context()) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	pr, err = h.Evolution.Resolve(id, store.EvolutionRejected)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	writeJSON(w, http.StatusOK, pr)
}

func (h *Handler) ProposeEvolutionPR(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	task, ok := h.taskForRequest(w, r, id)
	if !ok {
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
	writeJSON(w, http.StatusOK, map[string]any{"credentials": h.Credentials.List(auth.MustWorkspaceID(r.Context()))})
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
	wsID := auth.MustWorkspaceID(r.Context())
	c, err := h.Credentials.Upsert(&store.Credential{
		UserID:      wsID,
		WorkspaceID: wsID,
		Connector:   body.Connector,
		Label:       body.Label,
		Secret:      body.Secret,
		Value:       body.Value,
		Scopes:      body.Scopes,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	h.audit(r, "credential.create", "credential", c.ID, c.Label)
	writeJSON(w, http.StatusCreated, c)
}

func (h *Handler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.Credentials.Delete(id, auth.MustWorkspaceID(r.Context())); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
		return
	}
	h.audit(r, "credential.delete", "credential", id, "")
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
	tasks := h.Store.ListTasks(auth.MustWorkspaceID(r.Context()))
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
	src, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	yaml, _ := h.Files.ReadFile(src.WorkspaceID, id, "workflow.yaml")
	main, _ := h.Files.ReadFile(src.WorkspaceID, id, "main.ts")
	forkWsID := auth.MustWorkspaceID(r.Context())
	dup := h.Store.CreateTask(&store.Task{
		UserID:       forkWsID,
		WorkspaceID:  forkWsID,
		Name:         src.Name + "-fork",
		Description:  "Fork of " + src.ID + ": " + src.Description,
		Capabilities: src.Capabilities,
		Triggers:     append([]store.Trigger{}, src.Triggers...),
	})
	_ = h.Files.WriteFiles(dup.WorkspaceID, dup.ID, map[string]string{"workflow.yaml": yaml, "main.ts": main})
	writeJSON(w, http.StatusCreated, dup)
}

// --- dashboards -------------------------------------------------------

// ServeDashboard reverse-proxies /d/<task_id>/[<name>/]<rest> to the
// workflow's HTTP server. The workflow can declare multiple named ports
// (UI, API, websocket, …) via task.Ports; each is exposed under its name.
// The bare `/d/<task_id>/` form forwards to the "ui" role port (iframe
// default).
//
// Unregistered names → 404. We never forward to ports the agent didn't
// explicitly declare.
//
// WebSockets and SSE pass through. If the task has no live port mapping yet
// (e.g. service still booting), we briefly retry — the workflow usually
// binds within a second of spawn.
func (h *Handler) ServeDashboard(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Path shape: /d/<task>/<rest...>. Peek the first segment of <rest> —
	// if it matches a registered binding name, route there with a longer
	// prefix strip; otherwise fall through to the "ui" port.
	publicPrefix := "/d/" + taskID
	rest := strings.TrimPrefix(r.URL.Path, publicPrefix)
	rest = strings.TrimPrefix(rest, "/")

	var port int
	var name string
	if rest != "" {
		first := rest
		if i := strings.Index(rest, "/"); i >= 0 {
			first = rest[:i]
		}
		if b, ok := h.Ports.LookupNamed(taskID, first); ok {
			port = b.Port
			name = first
		}
	}
	if port == 0 {
		// No named match — try the "ui" default. If neither, 404.
		if p, ok := h.Ports.Lookup(taskID); ok {
			port = p
		}
	}
	if port == 0 {
		http.Error(w, "no dashboard running for this task. Declare a port (role:\"ui\") in the workflow's ports[].", http.StatusNotFound)
		return
	}

	// The prefix to strip from the inbound URL so the workflow sees a clean
	// path. When we routed via a name, the name itself is part of the
	// prefix (the workflow doesn't know it lives under /<name>/).
	stripPrefix := publicPrefix
	publicBase := publicPrefix
	if name != "" {
		stripPrefix = publicPrefix + "/" + name
		publicBase = stripPrefix
	}

	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", port))
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			req.URL.Path = strings.TrimPrefix(req.URL.Path, stripPrefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			req.Header.Set("X-Forwarded-Prefix", publicBase)
			req.Header.Set("X-Forwarded-Proto", "http")
			if _, hasUA := req.Header["User-Agent"]; !hasUA {
				req.Header.Set("User-Agent", "")
			}
		},
		Transport: &retryTransport{base: http.DefaultTransport, attempts: 3, gap: 400 * time.Millisecond},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Printf("dashboard proxy error for %s (name=%q port=%d): %v", taskID, name, port, err)
			http.Error(w, "dashboard upstream unreachable: "+err.Error(), http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, r)
}

// ListTaskPorts returns the live port-forwarding map for a task — what names
// are registered, what roles they have, what loopback port the platform
// allocated. Used by the UIPane "ports" view.
func (h *Handler) ListTaskPorts(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := h.taskForRequest(w, r, id); !ok {
		return
	}
	out := h.Ports.ListForTask(id)
	writeJSON(w, http.StatusOK, map[string]any{"ports": out})
}

type retryTransport struct {
	base     http.RoundTripper
	attempts int
	gap      time.Duration
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var lastErr error
	for i := 0; i < t.attempts; i++ {
		resp, err := t.base.RoundTrip(req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		var ne net.Error
		if errors.As(err, &ne) || strings.Contains(err.Error(), "connection refused") {
			time.Sleep(t.gap)
			continue
		}
		break
	}
	return nil, lastErr
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

// routeChatToTaskHandler forwards the user's chat message to the running
// daemon's chat port (PLATFORM_PORT_CHAT, registered as ports[name=chat]).
// Returns the daemon's reply to the user verbatim and persists both sides
// to chat history.
func (h *Handler) routeChatToTaskHandler(w http.ResponseWriter, t *store.Task, sessionID, content string) {
	binding, ok := h.Ports.LookupNamed(t.ID, "chat")
	if !ok {
		// Handler is declared but the daemon hasn't bound yet (booting or
		// crashed). Tell the user; the supervisor will recover.
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"sessionId": sessionID,
			"reply":     "this agent isn't responding yet — give the supervisor a few seconds and try again.",
		})
		return
	}

	// Build the message envelope. Use the LAST ~12 turns of session history
	// so the chat handler can keep context without re-querying.
	histRows := h.Store.ListMessages(sessionID, 24)
	hist := make([]map[string]string, 0, len(histRows))
	for _, m := range histRows {
		role := "user"
		if m.Role != "user" {
			role = "assistant"
		}
		hist = append(hist, map[string]string{"role": role, "content": m.Content})
	}
	// Drop the latest user message we just persisted — the handler gets it
	// as `content`, not in history.
	if n := len(hist); n > 0 && hist[n-1]["role"] == "user" && hist[n-1]["content"] == content {
		hist = hist[:n-1]
	}
	if len(hist) > 12 {
		hist = hist[len(hist)-12:]
	}

	body, _ := json.Marshal(map[string]any{
		"content":   content,
		"history":   hist,
		"sessionId": sessionID,
	})
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/", binding.Port), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("chat handler %s unreachable: %v", t.ID, err)
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"sessionId": sessionID,
			"reply":     "the agent crashed mid-reply — try again in a moment.",
		})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		log.Printf("chat handler %s returned %d: %s", t.ID, resp.StatusCode, string(buf))
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"sessionId": sessionID,
			"reply":     fmt.Sprintf("agent error (HTTP %d): %s", resp.StatusCode, strings.TrimSpace(string(buf))),
		})
		return
	}
	var out struct {
		Reply       string `json:"reply"`
		Attachments any    `json:"attachments,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{
			"sessionId": sessionID,
			"reply":     "agent returned malformed JSON.",
		})
		return
	}
	reply := strings.TrimSpace(out.Reply)
	if reply == "" {
		reply = "(empty reply)"
	}
	_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, map[string]any{"chatHandler": true})
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": reply})
}

// handleSlashCommand intercepts `/cmd …` messages and runs cheap routes
// directly instead of paying a generator round trip. Returns true if it
// handled the message; false to let generator pick it up.
func handleSlashCommand(h *Handler, w http.ResponseWriter, r *http.Request, sessionID, content string) bool {
	parts := strings.SplitN(strings.TrimPrefix(content, "/"), " ", 2)
	cmd := strings.ToLower(parts[0])
	rest := ""
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}
	switch cmd {
	case "help":
		reply := strings.TrimSpace(`
Slash commands:
  /edit <prompt>   ask the meta-agent to update this task's code
  /rename <name>   rename this task (kebab-case)
  /archive         archive this task (code preserved)
  /run             trigger a manual run (oneshot tasks)
  /code            switch the right panel to the Code tab
  /help            this message
Anything else without a slash is sent straight to the task's chat handler
(if declared) or to the meta-agent.`)
		_, _ = h.Store.AppendMessage(sessionID, "assistant", reply, nil)
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": reply})
		return true
	case "rename":
		if rest == "" {
			writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "usage: /rename <new-name>"})
			return true
		}
		if _, err := h.Store.UpdateTask(sessionID, func(t *store.Task) { t.Name = rest }); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "rename failed: " + err.Error()})
			return true
		}
		_, _ = h.Store.AppendMessage(sessionID, "assistant", "renamed to "+rest, nil)
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "renamed to " + rest})
		return true
	case "archive":
		h.archiveBeforeDelete(sessionID)
		_ = h.Store.DeleteTask(sessionID)
		archiveWsID := auth.MustWorkspaceID(r.Context())
		_ = os.RemoveAll(h.Files.Dir(archiveWsID, sessionID))
		h.Bus.Publish(store.Event{Channel: "task:archived:" + archiveWsID, UserID: archiveWsID, TaskID: sessionID})
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "archived."})
		return true
	case "run":
		if t, err := h.Store.GetTask(sessionID); err == nil && t.Kind != "service" {
			run := h.Store.CreateRun(sessionID, "manual")
			go h.Runner.Start(t, "manual")
			writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "started run " + run.ID})
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "service tasks run themselves — use the panel to restart."})
		return true
	case "code":
		writeJSON(w, http.StatusOK, map[string]any{"sessionId": sessionID, "reply": "open the right panel and switch to the Code tab (⌘. → Code).", "hint": "code"})
		return true
	case "edit":
		// Hand the rest to the meta-generator as if the user typed it
		// without the slash. Return false to fall through.
		// We mutate the message we already persisted is tricky — so
		// instead we treat this as a real generator turn here.
		return false
	}
	return false
}

// detectChatHandler scans main.ts source for a `workflow.chatHandler(` call.
// We strip line comments first so a `// uses workflow.chatHandler` comment
// doesn't trigger a false positive. Block comments are too rare in
// practice to worry about here.
var chatHandlerCall = regexp.MustCompile(`workflow\s*\.\s*chatHandler\s*\(`)

func detectChatHandler(mainTs string) bool {
	stripped := stripLineComments(mainTs)
	return chatHandlerCall.MatchString(stripped)
}

func stripLineComments(s string) string {
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// ensureChatPort adds a `chat` port entry (role="chat") to the task's
// declared ports when the task has a chat handler. Idempotent — safe to
// call on every create/update.
func ensureChatPort(ports []store.TaskPort) []store.TaskPort {
	for _, p := range ports {
		if p.Name == "chat" {
			return ports
		}
	}
	return append(ports, store.TaskPort{Name: "chat", Role: "chat"})
}

// TaskChat is the programmatic API for invoking a task's chat handler from
// outside the chat UI (CLI, other tasks, MCP server).
func (h *Handler) TaskChat(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, ok := h.taskForRequest(w, r, id)
	if !ok {
		return
	}
	if !t.HasChatHandler {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "task is not a chat agent (no workflow.chatHandler)"})
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "message required"})
		return
	}
	// Append to session history so the chat panel shows external calls
	// alongside in-app conversation.
	_, _ = h.Store.AppendMessage(id, "user", body.Message, map[string]any{"external": true})
	h.routeChatToTaskHandler(w, t, id, body.Message)
}

func translatePorts(in []generator.Port) []store.TaskPort {
	out := make([]store.TaskPort, 0, len(in))
	seenUI := false
	for _, p := range in {
		name := strings.TrimSpace(p.Name)
		if name == "" {
			continue
		}
		role := p.Role
		if role == "" {
			role = "other"
		}
		// Defensive: collapse multiple ui-roles to the first one so the
		// reverse-proxy default stays unambiguous.
		if role == "ui" {
			if seenUI {
				role = "other"
			}
			seenUI = true
		}
		out = append(out, store.TaskPort{Name: name, Role: role})
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
