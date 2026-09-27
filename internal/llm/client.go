package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rs/zerolog/log"
)

// Client sends completion requests to an LLM provider.
type Client interface {
	Complete(ctx context.Context, req *Request) (*Response, error)
}

// openaiClient implements Client for OpenAI-compatible APIs (OpenAI, Ollama, etc).
type openaiClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIClient creates an LLM client targeting an OpenAI-compatible endpoint.
// baseURL should include the scheme and host (e.g. "http://localhost:11434/v1").
// apiKey may be empty for local providers like Ollama.
func NewOpenAIClient(baseURL, apiKey, model string, opts ...Option) Client {
	httpClient := applyOptions(opts)
	return &openaiClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		httpClient: httpClient,
	}
}

// openaiRequest is the wire format for OpenAI /v1/chat/completions.
type openaiRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	// ReasoningEffort is omitted when empty so providers keep their default.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	Stream          bool   `json:"stream"`
}

// openaiResponse is the wire format for OpenAI /v1/chat/completions response.
type openaiResponse struct {
	ID      string         `json:"id"`
	Model   string         `json:"model"`
	Choices []openaiChoice `json:"choices"`
	Usage   *Usage         `json:"usage,omitempty"`
}

type openaiChoice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

func (c *openaiClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	wireReq := openaiRequest{
		Model:           c.model,
		Messages:        req.Messages,
		Tools:           req.Tools,
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: req.ReasoningEffort,
		Stream:          false,
	}

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/chat/completions"
	log.Debug().Str("url", url).Str("model", c.model).Int("messages", len(req.Messages)).Int("tools", len(req.Tools)).Int("body_bytes", len(body)).Msg("LLM HTTP request")
	headers := make(http.Header)
	if c.apiKey != "" {
		headers.Set("Authorization", "Bearer "+c.apiKey)
	}

	var wireResp openaiResponse
	if err := postJSON(ctx, c.httpClient, url, headers, body, &wireResp); err != nil {
		return nil, err
	}

	return wireResp.toResponse(), nil
}

func (r *openaiResponse) toResponse() *Response {
	resp := &Response{
		ID:    r.ID,
		Model: r.Model,
		Usage: r.Usage,
	}
	if len(r.Choices) > 0 {
		resp.Content = r.Choices[0].Message.Content
		resp.ToolCalls = r.Choices[0].Message.ToolCalls
		resp.FinishReason = r.Choices[0].FinishReason
	}
	return resp
}

func truncate(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "..."
}
