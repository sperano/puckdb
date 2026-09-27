package llm

import "encoding/json"

// Request is a provider-neutral LLM completion request.
// Provider-specific fields (model, stream) are set by the client implementation.
type Request struct {
	Messages    []Message `json:"messages"`
	Tools       []Tool    `json:"tools,omitempty"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
	// ReasoningEffort is sent as-is to OpenAI-compatible providers ("none"
	// turns off an Ollama model's thinking); empty leaves the provider's
	// default. The Anthropic client ignores it.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// Message represents a single chat message.
//
// Cacheable is a provider-translation hint: when true and the underlying
// client supports prompt caching (Anthropic), this message MAY be marked
// as a cache breakpoint. Only the final cacheable block in wire order
// receives the actual marker; earlier hints are coalesced into the same
// cached prefix. Other clients silently ignore the hint.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Cacheable  bool       `json:"-"`
}

// Tool describes a function tool the model can call.
//
// Cacheable: see the same field on Message.
type Tool struct {
	Type      string       `json:"type"`
	Function  ToolFunction `json:"function"`
	Cacheable bool         `json:"-"`
}

// ToolFunction describes the function within a Tool definition.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// ToolCall represents a tool invocation requested by the model.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction contains the name and arguments of a tool call.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Response is a provider-neutral LLM completion response.
// Flattened — no Choice wrapper since we always use index 0.
type Response struct {
	ID           string     `json:"id"`
	Model        string     `json:"model"`
	Content      string     `json:"content"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	FinishReason string     `json:"finish_reason"`
	Usage        *Usage     `json:"usage,omitempty"`
}

// Usage reports token consumption.
//
// CacheCreationInputTokens and CacheReadInputTokens are populated only by
// providers that report cache accounting (Anthropic). For other providers
// they remain zero.
type Usage struct {
	PromptTokens             int `json:"prompt_tokens"`
	CompletionTokens         int `json:"completion_tokens"`
	TotalTokens              int `json:"total_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens,omitempty"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens,omitempty"`
}

// HasToolCalls returns true if the response contains tool calls.
func (r *Response) HasToolCalls() bool {
	return len(r.ToolCalls) > 0
}
