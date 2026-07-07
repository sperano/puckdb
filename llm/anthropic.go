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

const (
	anthropicVersion          = "2023-06-01"
	anthropicDefaultMaxTokens = 4096
)

// anthropicClient implements Client for the Anthropic Messages API.
type anthropicClient struct {
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewAnthropicClient creates an LLM client targeting the Anthropic Messages API.
// baseURL should be "https://api.anthropic.com" (no trailing /v1).
func NewAnthropicClient(baseURL, apiKey, model string, opts ...Option) Client {
	return &anthropicClient{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		httpClient: applyOptions(opts),
	}
}

// --- Anthropic wire types (unexported) ---

type anthropicRequest struct {
	Model     string                 `json:"model"`
	Messages  []anthropicMessage     `json:"messages"`
	System    []anthropicSystemBlock `json:"system,omitempty"`
	Tools     []anthropicTool        `json:"tools,omitempty"`
	MaxTokens int                    `json:"max_tokens"`
}

// anthropicSystemBlock is one block of the system prompt. Anthropic accepts
// the system field as either a bare string or an array of blocks; the array
// form is required to attach cache_control.
type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicCacheControl marks a block as the end of a cacheable prefix.
// Type is always "ephemeral" today; if Anthropic adds more types later
// the field can grow.
type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicMessage struct {
	Role    string             `json:"role"`
	Content []anthropicContent `json:"content,omitempty"`
}

type anthropicContent struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`

	// caching (any block type)
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicResponse struct {
	ID         string             `json:"id"`
	Model      string             `json:"model"`
	Content    []anthropicContent `json:"content"`
	StopReason string             `json:"stop_reason"`
	Usage      *anthropicUsage    `json:"usage,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

// ephemeralCacheControl is the singleton cache_control marker we attach
// to the final cacheable block. Anthropic only supports "ephemeral" today,
// so a shared pointer is safe.
var ephemeralCacheControl = &anthropicCacheControl{Type: "ephemeral"}

// --- Translation ---

func (c *anthropicClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	wireReq := c.translateRequest(req)

	body, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + "/v1/messages"
	log.Debug().Str("url", url).Str("model", c.model).Int("messages", len(wireReq.Messages)).Int("tools", len(wireReq.Tools)).Int("body_bytes", len(body)).Msg("LLM HTTP request")
	log.Trace().Str("url", url).RawJSON("request_body", body).Msg("LLM HTTP request body")

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

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

	var wireResp anthropicResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return wireResp.toResponse(), nil
}

func (c *anthropicClient) translateRequest(req *Request) anthropicRequest {
	ar := anthropicRequest{
		Model:     c.model,
		MaxTokens: req.MaxTokens,
	}
	if ar.MaxTokens <= 0 {
		ar.MaxTokens = anthropicDefaultMaxTokens
	}

	// Anthropic supports a single cache breakpoint per request (per the
	// public spec — up to four are allowed but a single one suffices for
	// the system+tools cached-prefix pattern this codebase uses). Pick a
	// single winner among Cacheable hints with precedence message > tool
	// > system, since a marker placed later in wire order yields a longer
	// cached prefix. Indices below are ORDINALS within their section
	// (sysOrd, msgOrd) — they are stable across translation.
	winSystem, winTool, winMessage := -1, -1, -1
	sysOrd, msgOrd := 0, 0
	for _, m := range req.Messages {
		if m.Role == "system" {
			if m.Cacheable {
				winSystem = sysOrd
			}
			sysOrd++
		} else {
			if m.Cacheable {
				winMessage = msgOrd
			}
			msgOrd++
		}
	}
	for i, t := range req.Tools {
		if t.Cacheable {
			winTool = i
		}
	}
	applyToMessage := winMessage >= 0
	applyToTool := !applyToMessage && winTool >= 0
	applyToSystem := !applyToMessage && !applyToTool && winSystem >= 0

	// Extract system messages and translate the rest.
	sysOrd, msgOrd = 0, 0
	for _, m := range req.Messages {
		if m.Role == "system" {
			block := anthropicSystemBlock{Type: "text", Text: m.Content}
			if applyToSystem && sysOrd == winSystem {
				block.CacheControl = ephemeralCacheControl
			}
			ar.System = append(ar.System, block)
			sysOrd++
			continue
		}
		translated := translateMessage(m)
		if applyToMessage && msgOrd == winMessage && len(translated.Content) > 0 {
			// Mark the LAST content block of the winning message. Coalesce
			// preserves block order within a same-role run, so this stays
			// in place.
			translated.Content[len(translated.Content)-1].CacheControl = ephemeralCacheControl
		}
		ar.Messages = append(ar.Messages, translated)
		msgOrd++
	}

	// Coalesce consecutive same-role messages (Anthropic requires alternating).
	ar.Messages = coalesceMessages(ar.Messages)

	// Translate tools.
	for i, t := range req.Tools {
		at := anthropicTool{
			Name:        t.Function.Name,
			Description: t.Function.Description,
			InputSchema: t.Function.Parameters,
		}
		if applyToTool && i == winTool {
			at.CacheControl = ephemeralCacheControl
		}
		ar.Tools = append(ar.Tools, at)
	}

	return ar
}

func translateMessage(m Message) anthropicMessage {
	switch m.Role {
	case "assistant":
		var content []anthropicContent
		if m.Content != "" {
			content = append(content, anthropicContent{
				Type: "text",
				Text: m.Content,
			})
		}
		for _, tc := range m.ToolCalls {
			content = append(content, anthropicContent{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(tc.Function.Arguments),
			})
		}
		return anthropicMessage{Role: "assistant", Content: content}

	case "tool":
		return anthropicMessage{
			Role: "user",
			Content: []anthropicContent{{
				Type:      "tool_result",
				ToolUseID: m.ToolCallID,
				Content:   m.Content,
			}},
		}

	default: // "user"
		return anthropicMessage{
			Role: "user",
			Content: []anthropicContent{{
				Type: "text",
				Text: m.Content,
			}},
		}
	}
}

// coalesceMessages merges consecutive messages with the same role.
// Anthropic requires strictly alternating user/assistant messages.
func coalesceMessages(msgs []anthropicMessage) []anthropicMessage {
	if len(msgs) == 0 {
		return msgs
	}
	result := []anthropicMessage{msgs[0]}
	for _, m := range msgs[1:] {
		last := &result[len(result)-1]
		if last.Role == m.Role {
			last.Content = append(last.Content, m.Content...)
		} else {
			result = append(result, m)
		}
	}
	return result
}

func (r *anthropicResponse) toResponse() *Response {
	resp := &Response{
		ID:           r.ID,
		Model:        r.Model,
		FinishReason: r.StopReason,
	}
	if r.Usage != nil {
		resp.Usage = &Usage{
			PromptTokens:             r.Usage.InputTokens,
			CompletionTokens:         r.Usage.OutputTokens,
			TotalTokens:              r.Usage.InputTokens + r.Usage.OutputTokens,
			CacheCreationInputTokens: r.Usage.CacheCreationInputTokens,
			CacheReadInputTokens:     r.Usage.CacheReadInputTokens,
		}
	}

	for _, c := range r.Content {
		switch c.Type {
		case "text":
			resp.Content += c.Text
		case "tool_use":
			resp.ToolCalls = append(resp.ToolCalls, ToolCall{
				ID:   c.ID,
				Type: "function",
				Function: ToolCallFunction{
					Name:      c.Name,
					Arguments: string(c.Input),
				},
			})
		}
	}

	return resp
}
