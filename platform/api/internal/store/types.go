package store

import (
	"errors"
	"time"
)

var ErrNotFound = errors.New("not found")

const DefaultUserID = "u_local"

type RunState string

const (
	RunQueued    RunState = "queued"
	RunRunning   RunState = "running"
	RunSuccess   RunState = "success"
	RunFailed    RunState = "failed"
	RunCancelled RunState = "cancelled"
	RunTimeout   RunState = "timeout"
)

type Trigger struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Enabled bool           `json:"enabled"`
	Config  map[string]any `json:"config,omitempty"`
}

type ModelConfig struct {
	Generate string `json:"generate,omitempty"`
	Run      string `json:"run,omitempty"`
	Reflect  string `json:"reflect,omitempty"`
	Evolve   string `json:"evolve,omitempty"`
}

type Task struct {
	ID           string      `json:"id"`
	UserID       string      `json:"userId"`
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	Status       string      `json:"status"`
	Triggers     []Trigger   `json:"triggers"`
	Capabilities []string    `json:"capabilities"`
	Model        ModelConfig `json:"model"`
	Kind         string      `json:"kind"` // "oneshot" (default) | "service"
	// Ports declared by the agent for kind=service tasks. The platform
	// allocates a loopback port per entry and exposes them at
	// /d/<task>/<name>/. Exactly one entry should have role="ui" — that's
	// the iframe default.
	Ports     []TaskPort `json:"ports"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// TaskPort is one declared HTTP surface for a service task.
type TaskPort struct {
	// Name is a kebab-case slug used in the proxy URL (/d/<task>/<name>/).
	Name string `json:"name"`
	// Role hints how this port is used:
	//   "ui"    — primary user-facing UI. Exactly one task should have this.
	//             /d/<task>/ defaults to this port.
	//   "api"   — JSON / Swagger surface, programmatic use.
	//   "other" — websocket, SSE, debug endpoint, etc.
	Role string `json:"role"`
	// Allocated is the loopback port the platform bound for this entry.
	// Stored at runtime; not authored by the agent.
	Allocated int `json:"allocated,omitempty"`
}

type Run struct {
	ID         string     `json:"id"`
	TaskID     string     `json:"taskId"`
	UserID     string     `json:"userId"`
	State      RunState   `json:"state"`
	Trigger    string     `json:"trigger"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	DurationMs int        `json:"durationMs"`
	Output     any        `json:"output,omitempty"`
	Error      string     `json:"error,omitempty"`
}

type LogLine struct {
	RunID  string    `json:"runId"`
	Ts     time.Time `json:"ts"`
	Stream string    `json:"stream"`
	Text   string    `json:"text"`
}

type Session struct {
	ID           string     `json:"id"`
	UserID       string     `json:"userId"`
	Title        string     `json:"title"`
	PinnedTasks  []string   `json:"pinnedTasks"`
	ModelID      string     `json:"modelId,omitempty"`
	MessageCount int        `json:"messageCount"`
	LastMsgAt    time.Time  `json:"lastMsgAt"`
	ArchivedAt   *time.Time `json:"archivedAt,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
}

type Message struct {
	ID        int64          `json:"id"`
	SessionID string         `json:"sessionId"`
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	Ts        time.Time      `json:"ts"`
}

type Event struct {
	Channel string         `json:"channel"`
	UserID  string         `json:"userId"`
	TaskID  string         `json:"taskId,omitempty"`
	RunID   string         `json:"runId,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
	Ts      time.Time      `json:"ts"`
}
