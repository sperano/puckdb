package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

const httpTimeout = 120 * time.Second

// Client sends chat completion requests to an OpenAI-compatible API.
type Client interface {
	ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error)
}

// httpClient implements Client using net/http.
type httpClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewClient creates an LLM client targeting an OpenAI-compatible endpoint.
// baseURL should include the scheme and host (e.g. "http://localhost:11434/v1").
// apiKey may be empty for local providers like Ollama.
func NewClient(baseURL, apiKey, model string) Client {
	return &httpClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		httpClient: &http.Client{
			Timeout: httpTimeout,
		},
	}
}

func (c *httpClient) ChatCompletion(ctx context.Context, req *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	req.Model = c.model
	req.Stream = false

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	log.Debug().Str("url", url).Str("model", c.model).Int("messages", len(req.Messages)).Int("tools", len(req.Tools)).Int("body_bytes", len(body)).Msg("LLM HTTP request")
	log.Trace().Str("url", url).RawJSON("request_body", body).Msg("LLM HTTP request body")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	elapsed := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	log.Debug().Int("status", resp.StatusCode).Dur("elapsed", elapsed).Int("body_bytes", len(respBody)).Msg("LLM HTTP response")
	log.Trace().RawJSON("response_body", respBody).Msg("LLM HTTP response body")

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, truncate(respBody, 500))
	}

	var result ChatCompletionResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return &result, nil
}

func truncate(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
