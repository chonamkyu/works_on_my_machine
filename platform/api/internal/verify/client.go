// Package verify talks to the standalone verification service that runs
// chromium against generated dashboards and reports back what it saw.
package verify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

type BrowseResult struct {
	OK             bool     `json:"ok"`
	URL            string   `json:"url"`
	Status         int      `json:"status,omitempty"`
	Title          string   `json:"title,omitempty"`
	TextChars      int      `json:"textChars,omitempty"`
	ConsoleErrors  []string `json:"consoleErrors,omitempty"`
	PageErrors     []string `json:"pageErrors,omitempty"`
	ScreenshotPath string   `json:"screenshotPath,omitempty"`
	Errors         []string `json:"errors,omitempty"`
	DurationMs     int      `json:"durationMs"`
}

func (c *Client) Browse(ctx context.Context, url, taskID string, waitSec int) (*BrowseResult, error) {
	if c.BaseURL == "" {
		return nil, fmt.Errorf("verify base URL not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"url":         url,
		"taskId":      taskID,
		"waitSeconds": waitSec,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/browse", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("verify status %d", resp.StatusCode)
	}
	var out BrowseResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}
