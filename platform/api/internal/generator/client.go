package generator

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(base string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(base, "/"),
		HTTP:    &http.Client{Timeout: 180 * time.Second},
	}
}

type Trigger struct {
	ID      string         `json:"id"`
	Type    string         `json:"type"`
	Enabled bool           `json:"enabled"`
	Config  map[string]any `json:"config,omitempty"`
}

type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ExistingTask struct {
	ID               string                `json:"id"`
	Name             string                `json:"name"`
	Description      string                `json:"description"`
	Kind             string                `json:"kind,omitempty"`
	Capabilities     []string              `json:"capabilities"`
	Triggers         []Trigger             `json:"triggers"`
	MainTs           string                `json:"mainTs,omitempty"`
	LastRunState     string                `json:"lastRunState,omitempty"`
	LastRunError     string                `json:"lastRunError,omitempty"`
	RecentLogTail    []string              `json:"recentLogTail,omitempty"`
	LastVerification *VerificationSnapshot `json:"lastVerification,omitempty"`
	LastDryRun       *DryRunSnapshot       `json:"lastDryRun,omitempty"`
}

// DryRunSnapshot mirrors runner.DryRunResult so the generator can see if the
// last automated smoke-run with mocked connectors passed.
type DryRunSnapshot struct {
	OK         bool     `json:"ok"`
	ExitCode   int      `json:"exitCode"`
	FatalError string   `json:"fatalError,omitempty"`
	Stderr     []string `json:"stderr,omitempty"`
	DurationMs int      `json:"durationMs"`
	TimedOut   bool     `json:"timedOut,omitempty"`
}

// VerificationSnapshot is what the most-recent browser probe saw. Mirrors
// verify.BrowseResult but only the fields the generator can act on.
type VerificationSnapshot struct {
	OK            bool     `json:"ok"`
	Errors        []string `json:"errors,omitempty"`
	ConsoleErrors []string `json:"consoleErrors,omitempty"`
	PageErrors    []string `json:"pageErrors,omitempty"`
	Status        int      `json:"status,omitempty"`
	TextChars     int      `json:"textChars,omitempty"`
	DurationMs    int      `json:"durationMs,omitempty"`
}

type Request struct {
	Prompt              string           `json:"prompt"`
	SessionID           string           `json:"sessionId,omitempty"`
	UserID              string           `json:"userId,omitempty"`
	ActiveTaskID        string           `json:"activeTaskId,omitempty"`
	ActiveTaskIsDraft   bool             `json:"activeTaskIsDraft,omitempty"`
	ConnectorsAvailable []string         `json:"connectorsAvailable,omitempty"`
	History             []HistoryMessage `json:"history,omitempty"`
	ExistingTasks       []ExistingTask   `json:"existingTasks,omitempty"`
	// Phase 1 self-tuning: retrieved user-scope memories the agent should
	// consider this turn (preferences, recurring corrections, domain facts).
	UserContext []MemoryHint `json:"userContext,omitempty"`
	// Phase 2 self-tuning: stable user profile fields (preferred language,
	// reply tone, expertise, hard rules) the agent should always honor.
	UserProfile map[string]any `json:"userProfile,omitempty"`
}

type MemoryHint struct {
	Topic   string `json:"topic"`
	Content string `json:"content"`
	Pinned  bool   `json:"pinned,omitempty"`
}

type Intent string

const (
	IntentCreate            Intent = "create"
	IntentUpdate            Intent = "update"
	IntentChat              Intent = "chat"
	IntentDelete            Intent = "delete"
	IntentRequestCredential Intent = "request_credential"
	IntentManageCredential  Intent = "manage_credential"
)

// ManageCredential is the payload the agent issues when it wants to write or
// remove a credential on the user's behalf.
type ManageCredential struct {
	Action    string         `json:"action"` // "upsert" or "delete"
	Connector string         `json:"connector"`
	Label     string         `json:"label"`
	Value     map[string]any `json:"value"`
}

// CredentialRequest carries an agent-issued "I need a credential" tool call.
type CredentialRequest struct {
	Connector      string `json:"connector"`
	Label          string `json:"label"`
	Kind           string `json:"kind"` // "token" or "oauth"
	HelpURL        string `json:"helpUrl"`
	Instructions   string `json:"instructions"`
	FollowUpPrompt string `json:"followUpPrompt"`
}

// Outcome captures every branch the generator can return.
// Port mirrors store.TaskPort so the generator client doesn't need to import
// the store package directly.
type Port struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type Outcome struct {
	Intent        Intent
	Reply         string
	TargetTaskID  string
	TargetTaskIDs []string // for delete

	// For create / update with code changes.
	Name         string
	Description  string
	Kind         string
	Capabilities []string
	Triggers     []Trigger
	Files        map[string]string
	Ports        []Port

	// For request_credential
	CredentialRequest *CredentialRequest
	// For manage_credential
	ManageCredential *ManageCredential
}

type envelope struct {
	Channel string          `json:"channel"`
	Payload json.RawMessage `json:"payload"`
}

// ProgressFn is called for every non-terminal SSE event the generator emits
// (stream deltas, harness turn announcements). Pass nil to ignore them.
type ProgressFn func(channel string, payload json.RawMessage)

func (c *Client) Generate(ctx context.Context, req Request, onProgress ProgressFn) (*Outcome, error) {
	if c.BaseURL == "" {
		return nil, errors.New("generator base URL not configured")
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/generate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("generator http: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b := make([]byte, 2048)
		n, _ := resp.Body.Read(b)
		return nil, fmt.Errorf("generator status %d: %s", resp.StatusCode, string(b[:n]))
	}

	scan := bufio.NewScanner(resp.Body)
	scan.Buffer(make([]byte, 0, 8192), 1<<20)
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var env envelope
		if err := json.Unmarshal([]byte(payload), &env); err != nil {
			continue
		}
		switch env.Channel {
		case "workflow:draft":
			var p struct {
				Name         string            `json:"name"`
				Description  string            `json:"description"`
				Kind         string            `json:"kind"`
				Capabilities []string          `json:"capabilities"`
				Triggers     []Trigger         `json:"triggers"`
				Files        map[string]string `json:"files"`
				Ports        []Port            `json:"ports"`
				Reply        string            `json:"reply"`
			}
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return nil, fmt.Errorf("draft decode: %w", err)
			}
			return &Outcome{
				Intent: IntentCreate, Reply: p.Reply,
				Name: p.Name, Description: p.Description, Kind: p.Kind,
				Capabilities: p.Capabilities, Triggers: p.Triggers, Files: p.Files,
				Ports: p.Ports,
			}, nil
		case "workflow:update":
			var p struct {
				TargetTaskID string            `json:"target_task_id"`
				Name         string            `json:"name"`
				Description  string            `json:"description"`
				Kind         string            `json:"kind"`
				Capabilities []string          `json:"capabilities"`
				Triggers     []Trigger         `json:"triggers"`
				Files        map[string]string `json:"files"`
				Ports        []Port            `json:"ports"`
				Reply        string            `json:"reply"`
			}
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return nil, fmt.Errorf("update decode: %w", err)
			}
			return &Outcome{
				Intent: IntentUpdate, Reply: p.Reply, TargetTaskID: p.TargetTaskID,
				Name: p.Name, Description: p.Description, Kind: p.Kind,
				Capabilities: p.Capabilities, Triggers: p.Triggers, Files: p.Files,
				Ports: p.Ports,
			}, nil
		case "workflow:chat":
			var p struct {
				Reply string `json:"reply"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			return &Outcome{Intent: IntentChat, Reply: p.Reply}, nil
		case "workflow:delete":
			var p struct {
				TargetTaskIDs []string `json:"target_task_ids"`
				Reply         string   `json:"reply"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			return &Outcome{Intent: IntentDelete, TargetTaskIDs: p.TargetTaskIDs, Reply: p.Reply}, nil
		case "workflow:request_credential":
			var p struct {
				Connector      string `json:"connector"`
				Label          string `json:"label"`
				Kind           string `json:"kind"`
				HelpURL        string `json:"help_url"`
				Instructions   string `json:"instructions"`
				FollowUpPrompt string `json:"follow_up_prompt"`
				Reply          string `json:"reply"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			return &Outcome{
				Intent: IntentRequestCredential,
				Reply:  p.Reply,
				CredentialRequest: &CredentialRequest{
					Connector: p.Connector, Label: p.Label, Kind: p.Kind,
					HelpURL: p.HelpURL, Instructions: p.Instructions, FollowUpPrompt: p.FollowUpPrompt,
				},
			}, nil
		case "workflow:manage_credential":
			var p struct {
				Action    string         `json:"action"`
				Connector string         `json:"connector"`
				Label     string         `json:"label"`
				Value     map[string]any `json:"value"`
				Reply     string         `json:"reply"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			return &Outcome{
				Intent: IntentManageCredential,
				Reply:  p.Reply,
				ManageCredential: &ManageCredential{
					Action: p.Action, Connector: p.Connector, Label: p.Label, Value: p.Value,
				},
			}, nil
		case "workflow:error":
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			return nil, fmt.Errorf("generator error: %s", p.Message)
		default:
			if onProgress != nil {
				onProgress(env.Channel, env.Payload)
			}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("generator stream: %w", err)
	}
	return nil, errors.New("generator stream ended without a terminal event")
}
