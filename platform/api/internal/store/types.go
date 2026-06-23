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
	CreatedAt    time.Time   `json:"createdAt"`
	UpdatedAt    time.Time   `json:"updatedAt"`
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
