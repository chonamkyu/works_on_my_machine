package reflection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client calls the reflection microservice over HTTP/JSON.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(base string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		HTTP:    &http.Client{Timeout: 60 * time.Second},
	}
}

type LogLineIn struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

type MemorySnippet struct {
	Topic   string `json:"topic"`
	Content string `json:"content"`
}

type Request struct {
	RunID        string          `json:"runId"`
	WorkflowID   string          `json:"workflowId"`
	WorkflowName string          `json:"workflowName"`
	Outcome      string          `json:"outcome"`
	DurationMs   int             `json:"durationMs"`
	LogsTail     []LogLineIn     `json:"logsTail"`
	RecentMemory []MemorySnippet `json:"recentMemory,omitempty"`
}

type Issue struct {
	Issue    string `json:"issue"`
	Evidence string `json:"evidence"`
	Severity string `json:"severity"`
}

type MemoryUpdate struct {
	Scope   string `json:"scope"`
	Topic   string `json:"topic"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned,omitempty"`
}

type EvolutionHint struct {
	Category        string `json:"category"`
	Suggestion      string `json:"suggestion"`
	EstimatedImpact string `json:"estimated_impact"`
}

type Result struct {
	RunID           string          `json:"run_id"`
	WorkflowID      string          `json:"workflow_id"`
	Outcome         string          `json:"outcome"`
	Summary         string          `json:"summary"`
	WhatWorked      []string        `json:"what_worked"`
	WhatDidnt       []Issue         `json:"what_didnt"`
	MemoryUpdates   []MemoryUpdate  `json:"memory_updates"`
	EvolutionHints  []EvolutionHint `json:"evolution_hints"`
	Score           int             `json:"score"`
}

// --- Chat-turn reflection (Phase 1 self-tuning) ---

type ChatTurn struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"`
}

type ChatReflectRequest struct {
	UserID       string         `json:"userId"`
	SessionID    string         `json:"sessionId"`
	UserMessage  string         `json:"userMessage"`
	AgentReply   string         `json:"agentReply"`
	IsCorrection bool           `json:"isCorrection,omitempty"`
	RecentTurns  []ChatTurn     `json:"recentTurns,omitempty"`
	KnownMemory  []MemorySnippet `json:"knownMemory,omitempty"`
}

type ChatMemoryUpdate struct {
	Topic   string `json:"topic"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned,omitempty"`
}

type ChatReflectResult struct {
	MemoryUpdates      []ChatMemoryUpdate `json:"memory_updates"`
	ProfileSuggestions map[string]string  `json:"profile_suggestions,omitempty"`
	Signal             string             `json:"signal"`
	Notes              string             `json:"notes,omitempty"`
}

// --- Skill proposal (Phase 3 self-tuning) ---

type SkillProposeRequest struct {
	UserID            string            `json:"userId"`
	Memories          []MemoryWithMeta  `json:"memories"`
	RecentCorrections []string          `json:"recentCorrections,omitempty"`
	ExistingSkills    []ExistingSkill   `json:"existingSkills,omitempty"`
}

type MemoryWithMeta struct {
	Topic   string `json:"topic"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned,omitempty"`
}

type ExistingSkill struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

type SkillProposeResult struct {
	SkillName   string `json:"skill_name"`
	SkillBody   string `json:"skill_body"`
	Rationale   string `json:"rationale"`
	Confidence  string `json:"confidence"`
	AppliesWhen string `json:"applies_when"`
}

func (c *Client) ProposeSkill(ctx context.Context, req SkillProposeRequest) (*SkillProposeResult, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("reflection base URL not configured")
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/propose-skill", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 2048)
		n, _ := resp.Body.Read(buf)
		return nil, fmt.Errorf("propose-skill status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out SkillProposeResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("propose-skill decode: %w", err)
	}
	return &out, nil
}

func (c *Client) ReflectChat(ctx context.Context, req ChatReflectRequest) (*ChatReflectResult, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("reflection base URL not configured")
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/reflect-chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 2048)
		n, _ := resp.Body.Read(buf)
		return nil, fmt.Errorf("reflect-chat status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out ChatReflectResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("reflect-chat decode: %w", err)
	}
	return &out, nil
}

func (c *Client) Reflect(ctx context.Context, req Request) (*Result, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("reflection base URL not configured")
	}
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/reflect", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 2048)
		n, _ := resp.Body.Read(buf)
		return nil, fmt.Errorf("reflection status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out Result
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("reflection decode: %w", err)
	}
	return &out, nil
}
