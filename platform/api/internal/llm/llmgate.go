package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// (strings imported above for path detection)

// LlmgateEmbedder produces vector embeddings by calling llmgate's
// OpenAI-compatible /api/v1/embeddings endpoint. The default model is
// Amazon Titan v2 (1024 dim) — make sure pgvector column matches.
type LlmgateEmbedder struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func NewLlmgateEmbedder(baseURL, apiKey, model string) *LlmgateEmbedder {
	return &LlmgateEmbedder{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (e *LlmgateEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	if e.APIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY (or PLATFORM_LLMGATE_KEY) unset")
	}
	body, _ := json.Marshal(map[string]any{
		"model": e.Model,
		"input": text,
	})
	// llmgate uses /api/v1/embeddings; vanilla OpenAI uses /v1/embeddings.
	path := "/v1/embeddings"
	if strings.Contains(e.BaseURL, "llmgate") || strings.Contains(e.BaseURL, "doosanrobotics") {
		path = "/api/v1/embeddings"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.APIKey)

	resp, err := e.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		buf := make([]byte, 2048)
		n, _ := resp.Body.Read(buf)
		return nil, fmt.Errorf("embed status %d: %s", resp.StatusCode, string(buf[:n]))
	}
	var out struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, fmt.Errorf("embed: empty response")
	}
	return out.Data[0].Embedding, nil
}
