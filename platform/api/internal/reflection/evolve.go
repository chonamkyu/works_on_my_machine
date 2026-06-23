package reflection

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type EvolveRequest struct {
	WorkflowID         string          `json:"workflowId"`
	WorkflowName       string          `json:"workflowName"`
	CurrentMainTs      string          `json:"currentMainTs"`
	CurrentWorkflowYaml string         `json:"currentWorkflowYaml"`
	RecentReflections  []Result        `json:"recentReflections"`
	RecentMemory       []MemorySnippet `json:"recentMemory"`
	Reason             string          `json:"reason"`
}

type EvolveResult struct {
	Rationale        string `json:"rationale"`
	DiffSummary      string `json:"diff_summary"`
	NewMainTs        string `json:"new_main_ts"`
	NewWorkflowYaml  string `json:"new_workflow_yaml"`
	SelfReview       string `json:"self_review"`
}

func (c *Client) Evolve(ctx context.Context, req EvolveRequest) (*EvolveResult, error) {
	body, _ := json.Marshal(req)
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/evolve", bytes.NewReader(body))
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
		return nil, fmt.Errorf("evolve status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out EvolveResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("evolve decode: %w", err)
	}
	return &out, nil
}
