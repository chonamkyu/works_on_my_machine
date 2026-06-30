package runner

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/nk/platform/api/internal/reflection"
	"github.com/nk/platform/api/internal/store"
)

// SkillEvolver runs a periodic pass that asks reflection to propose new
// per-user skills based on accumulated memory + corrections. Phase 4 of
// the Hermes-style self-tuning plan: agent itself adapts to the user.
//
// Proposed skills are NOT auto-applied — they land as
// store.EvolutionPR-style proposals (kind="skill") that the user accepts in
// the UI. This stays safe: the agent's behavior never silently mutates.
type SkillEvolver struct {
	reflect   *reflection.Client
	memStore  *store.MemoryStore
	taskStore *store.Memory
	bus       *store.Bus
	tick      time.Duration
	stop      chan struct{}
	// loadExisting returns the user's current overlay skills.
	loadExisting func(userID string) []reflection.ExistingSkill
	// writeProposal records the proposal so the UI can show + accept it.
	writeProposal func(userID string, res *reflection.SkillProposeResult)
}

func NewSkillEvolver(
	r *reflection.Client,
	ms *store.MemoryStore,
	ts *store.Memory,
	bus *store.Bus,
	loadExisting func(string) []reflection.ExistingSkill,
	writeProposal func(string, *reflection.SkillProposeResult),
	tick time.Duration,
) *SkillEvolver {
	if tick == 0 {
		tick = 12 * time.Hour
	}
	return &SkillEvolver{
		reflect:       r,
		memStore:      ms,
		taskStore:     ts,
		bus:           bus,
		tick:          tick,
		stop:          make(chan struct{}),
		loadExisting:  loadExisting,
		writeProposal: writeProposal,
	}
}

func (s *SkillEvolver) Start() {
	go s.loop()
}

func (s *SkillEvolver) Stop() {
	close(s.stop)
}

func (s *SkillEvolver) loop() {
	// First pass after a short warm-up so a fresh boot doesn't immediately
	// hit reflection. Then settle into `tick` cadence.
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.runOnce(store.GlobalScope)
			t.Reset(s.tick)
		}
	}
}

// RunOnce kicks an immediate proposal pass (used by the manual "Evolve now"
// admin button).
func (s *SkillEvolver) RunOnce(userID string) {
	s.runOnce(userID)
}

func (s *SkillEvolver) runOnce(userID string) {
	if s.reflect == nil || s.memStore == nil {
		return
	}
	mems := s.memStore.List(userID, store.ScopeUser, "")
	// Don't fire unless there's enough signal to be worth a skill — small
	// accumulations are better captured as memory_entries.
	if len(mems) < 5 {
		return
	}
	memMeta := make([]reflection.MemoryWithMeta, 0, len(mems))
	for _, m := range mems {
		memMeta = append(memMeta, reflection.MemoryWithMeta{Topic: m.Topic, Content: m.Content, Pinned: m.Pinned})
	}
	// Recent corrections: pending profile suggestions are the strongest signal.
	corrections := []string{}
	if s.taskStore != nil {
		for _, sg := range s.taskStore.ListProfileSuggestions(userID, "pending") {
			corrections = append(corrections, sg.Field+" -> "+sg.Value)
		}
	}
	existing := []reflection.ExistingSkill{}
	if s.loadExisting != nil {
		existing = s.loadExisting(userID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	res, err := s.reflect.ProposeSkill(ctx, reflection.SkillProposeRequest{
		UserID:            userID,
		Memories:          memMeta,
		RecentCorrections: corrections,
		ExistingSkills:    existing,
	})
	if err != nil {
		log.Printf("skill evolve failed for %s: %v", userID, err)
		return
	}
	if res == nil || strings.TrimSpace(res.SkillBody) == "" || res.Confidence == "low" {
		log.Printf("skill evolve: nothing high-confidence to propose for %s", userID)
		return
	}
	log.Printf("skill evolve: proposed %s (%s) — %s", res.SkillName, res.Confidence, res.Rationale)
	if s.writeProposal != nil {
		s.writeProposal(userID, res)
	}
	if s.bus != nil {
		s.bus.Publish(store.Event{
			Channel: "skill:proposed:" + userID,
			UserID:  userID,
			Payload: map[string]any{"skill": res},
		})
	}
}
