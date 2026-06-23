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
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Kind          string    `json:"kind,omitempty"`
	Capabilities  []string  `json:"capabilities"`
	Triggers      []Trigger `json:"triggers"`
	MainTs        string    `json:"mainTs,omitempty"`
	LastRunState  string    `json:"lastRunState,omitempty"`
	LastRunError  string    `json:"lastRunError,omitempty"`
	RecentLogTail []string  `json:"recentLogTail,omitempty"`
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
}

type Intent string

const (
	IntentCreate            Intent = "create"
	IntentUpdate            Intent = "update"
	IntentChat              Intent = "chat"
	IntentDelete            Intent = "delete"
	IntentRequestCredential Intent = "request_credential"
)

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

	// For request_credential
	CredentialRequest *CredentialRequest
}

type envelope struct {
	Channel string          `json:"channel"`
	Payload json.RawMessage `json:"payload"`
}

func (c *Client) Generate(ctx context.Context, req Request) (*Outcome, error) {
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
				Reply        string            `json:"reply"`
			}
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return nil, fmt.Errorf("draft decode: %w", err)
			}
			return &Outcome{
				Intent: IntentCreate, Reply: p.Reply,
				Name: p.Name, Description: p.Description, Kind: p.Kind,
				Capabilities: p.Capabilities, Triggers: p.Triggers, Files: p.Files,
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
				Reply        string            `json:"reply"`
			}
			if err := json.Unmarshal(env.Payload, &p); err != nil {
				return nil, fmt.Errorf("update decode: %w", err)
			}
			return &Outcome{
				Intent: IntentUpdate, Reply: p.Reply, TargetTaskID: p.TargetTaskID,
				Name: p.Name, Description: p.Description, Kind: p.Kind,
				Capabilities: p.Capabilities, Triggers: p.Triggers, Files: p.Files,
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
		case "workflow:error":
			var p struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(env.Payload, &p)
			return nil, fmt.Errorf("generator error: %s", p.Message)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("generator stream: %w", err)
	}
	return nil, errors.New("generator stream ended without a terminal event")
}
