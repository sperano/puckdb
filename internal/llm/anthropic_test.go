package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnthropicTranslateRequest_SystemExtracted(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "system", Content: "You are Maurice."},
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 100,
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.System, 1)
	assert.Equal(t, "text", wire.System[0].Type)
	assert.Equal(t, "You are Maurice.", wire.System[0].Text)
	assert.Nil(t, wire.System[0].CacheControl, "uncached system must not carry a cache_control marker")
	require.Len(t, wire.Messages, 1)
	assert.Equal(t, "user", wire.Messages[0].Role)
	assert.Equal(t, "Hello", wire.Messages[0].Content[0].Text)
}

func TestAnthropicTranslateRequest_ToolCallsToToolUse(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "user", Content: "query"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: ToolCallFunction{
						Name:      "pg_read_query",
						Arguments: `{"sql":"SELECT 1"}`,
					},
				}},
			},
			{
				Role:       "tool",
				Content:    `[{"result":1}]`,
				ToolCallID: "call_1",
			},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.Messages, 3)

	// Assistant message has tool_use block
	assistantMsg := wire.Messages[1]
	assert.Equal(t, "assistant", assistantMsg.Role)
	require.Len(t, assistantMsg.Content, 1)
	assert.Equal(t, "tool_use", assistantMsg.Content[0].Type)
	assert.Equal(t, "call_1", assistantMsg.Content[0].ID)
	assert.Equal(t, "pg_read_query", assistantMsg.Content[0].Name)

	// Tool result becomes user message with tool_result block
	toolMsg := wire.Messages[2]
	assert.Equal(t, "user", toolMsg.Role)
	require.Len(t, toolMsg.Content, 1)
	assert.Equal(t, "tool_result", toolMsg.Content[0].Type)
	assert.Equal(t, "call_1", toolMsg.Content[0].ToolUseID)
}

func TestAnthropicCoalesceMessages(t *testing.T) {
	// Simulate: assistant (tool_use) + tool result 1 (user) + tool result 2 (user)
	// Should coalesce the two user messages into one
	msgs := []anthropicMessage{
		{Role: "user", Content: []anthropicContent{{Type: "text", Text: "query"}}},
		{Role: "assistant", Content: []anthropicContent{{Type: "tool_use", ID: "c1", Name: "t1"}}},
		{Role: "user", Content: []anthropicContent{{Type: "tool_result", ToolUseID: "c1", Content: "r1"}}},
		{Role: "user", Content: []anthropicContent{{Type: "tool_result", ToolUseID: "c2", Content: "r2"}}},
	}

	coalesced := coalesceMessages(msgs)
	require.Len(t, coalesced, 3)
	assert.Equal(t, "user", coalesced[0].Role)
	assert.Equal(t, "assistant", coalesced[1].Role)
	assert.Equal(t, "user", coalesced[2].Role)
	// The coalesced user message should have both tool_result blocks
	assert.Len(t, coalesced[2].Content, 2)
}

func TestAnthropicTranslateRequest_DefaultMaxTokens(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{{Role: "user", Content: "Hi"}},
	}
	wire := c.translateRequest(req)
	assert.Equal(t, anthropicDefaultMaxTokens, wire.MaxTokens)
}

func TestAnthropicTranslateRequest_ToolDefinitions(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{{Role: "user", Content: "Hi"}},
		Tools: []Tool{{
			Type: "function",
			Function: ToolFunction{
				Name:        "pg_count",
				Description: "Count rows",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
		}},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.Tools, 1)
	assert.Equal(t, "pg_count", wire.Tools[0].Name)
	assert.Equal(t, "Count rows", wire.Tools[0].Description)
	assert.JSONEq(t, `{"type":"object"}`, string(wire.Tools[0].InputSchema))
}

func TestAnthropicResponseTranslation(t *testing.T) {
	t.Run("text response", func(t *testing.T) {
		ar := &anthropicResponse{
			ID:         "msg_123",
			Model:      "claude-test",
			StopReason: "end_turn",
			Content: []anthropicContent{
				{Type: "text", Text: "Wayne Gretzky scored 894 goals."},
			},
			Usage: &anthropicUsage{InputTokens: 10, OutputTokens: 20},
		}

		resp := ar.toResponse()
		assert.Equal(t, "msg_123", resp.ID)
		assert.Equal(t, "Wayne Gretzky scored 894 goals.", resp.Content)
		assert.False(t, resp.HasToolCalls())
		assert.Equal(t, "end_turn", resp.FinishReason)
		assert.Equal(t, 10, resp.Usage.PromptTokens)
		assert.Equal(t, 20, resp.Usage.CompletionTokens)
		assert.Equal(t, 30, resp.Usage.TotalTokens)
	})

	t.Run("tool use response", func(t *testing.T) {
		ar := &anthropicResponse{
			ID:         "msg_456",
			StopReason: "tool_use",
			Content: []anthropicContent{
				{Type: "tool_use", ID: "toolu_1", Name: "pg_read_query", Input: json.RawMessage(`{"sql":"SELECT 1"}`)},
			},
		}

		resp := ar.toResponse()
		assert.True(t, resp.HasToolCalls())
		require.Len(t, resp.ToolCalls, 1)
		assert.Equal(t, "toolu_1", resp.ToolCalls[0].ID)
		assert.Equal(t, "function", resp.ToolCalls[0].Type)
		assert.Equal(t, "pg_read_query", resp.ToolCalls[0].Function.Name)
		assert.Equal(t, `{"sql":"SELECT 1"}`, resp.ToolCalls[0].Function.Arguments)
	})

	t.Run("mixed text and tool_use", func(t *testing.T) {
		ar := &anthropicResponse{
			Content: []anthropicContent{
				{Type: "text", Text: "Let me check."},
				{Type: "tool_use", ID: "toolu_2", Name: "pg_count", Input: json.RawMessage(`{}`)},
			},
		}

		resp := ar.toResponse()
		assert.Equal(t, "Let me check.", resp.Content)
		assert.True(t, resp.HasToolCalls())
		require.Len(t, resp.ToolCalls, 1)
	})
}

func TestAnthropicComplete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "test-key", r.Header.Get("x-api-key"))
		assert.Equal(t, anthropicVersion, r.Header.Get("anthropic-version"))

		var req anthropicRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "claude-test", req.Model)
		require.Len(t, req.System, 1)
		assert.Equal(t, "You are Maurice.", req.System[0].Text)

		resp := anthropicResponse{
			ID:         "msg_test",
			Model:      "claude-test",
			StopReason: "end_turn",
			Content:    []anthropicContent{{Type: "text", Text: "Bonjour!"}},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewAnthropicClient(server.URL, "test-key", "claude-test")
	resp, err := client.Complete(context.Background(), &Request{
		Messages: []Message{
			{Role: "system", Content: "You are Maurice."},
			{Role: "user", Content: "Hello"},
		},
	})

	require.NoError(t, err)
	assert.Equal(t, "Bonjour!", resp.Content)
	assert.Equal(t, "end_turn", resp.FinishReason)
}

func TestAnthropicComplete_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	client := NewAnthropicClient(server.URL, "bad-key", "claude-test")
	_, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 401")
}

func TestAnthropicTranslateRequest_AssistantWithTextAndToolCalls(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "user", Content: "query"},
			{
				Role:    "assistant",
				Content: "Let me check that.",
				ToolCalls: []ToolCall{{
					ID:       "call_1",
					Type:     "function",
					Function: ToolCallFunction{Name: "pg_count", Arguments: `{}`},
				}},
			},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.Messages, 2)
	assistantContent := wire.Messages[1].Content
	require.Len(t, assistantContent, 2)
	assert.Equal(t, "text", assistantContent[0].Type)
	assert.Equal(t, "Let me check that.", assistantContent[0].Text)
	assert.Equal(t, "tool_use", assistantContent[1].Type)
}

// --- Prompt caching ---

func TestAnthropicTranslateRequest_CacheableSystemOnly(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "system", Content: "You are Maurice.", Cacheable: true},
			{Role: "user", Content: "Hello"},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.System, 1)
	require.NotNil(t, wire.System[0].CacheControl, "cacheable system must carry a cache_control marker")
	assert.Equal(t, "ephemeral", wire.System[0].CacheControl.Type)
}

func TestAnthropicTranslateRequest_CacheableTool_LastTool(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{{Role: "user", Content: "go"}},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "t1", Parameters: json.RawMessage(`{}`)}, Cacheable: true},
			{Type: "function", Function: ToolFunction{Name: "t2", Parameters: json.RawMessage(`{}`)}, Cacheable: true},
			{Type: "function", Function: ToolFunction{Name: "t3", Parameters: json.RawMessage(`{}`)}, Cacheable: false},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.Tools, 3)
	assert.Nil(t, wire.Tools[0].CacheControl, "earlier cacheable tool must not be marked")
	assert.NotNil(t, wire.Tools[1].CacheControl, "last cacheable tool gets the marker")
	assert.Nil(t, wire.Tools[2].CacheControl, "later non-cacheable tool must not be marked")
}

// Tools come AFTER system in wire order, so a cacheable tool overrides a
// cacheable system — only the last cacheable hint in wire order wins, since
// putting the marker later yields a longer cached prefix.
func TestAnthropicTranslateRequest_ToolBeatsSystem(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "system", Content: "rules", Cacheable: true},
			{Role: "user", Content: "go"},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "t1", Parameters: json.RawMessage(`{}`)}, Cacheable: true},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.System, 1)
	require.Len(t, wire.Tools, 1)
	assert.Nil(t, wire.System[0].CacheControl, "later tool marker preempts the system marker")
	assert.NotNil(t, wire.Tools[0].CacheControl)
}

// Messages come LAST in wire order, so a cacheable message overrides
// cacheable system/tools. The marker lands on the message's last content
// block.
func TestAnthropicTranslateRequest_MessageBeatsToolAndSystem(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "system", Content: "rules", Cacheable: true},
			{Role: "user", Content: "context dump", Cacheable: true},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "t1", Parameters: json.RawMessage(`{}`)}, Cacheable: true},
		},
	}

	wire := c.translateRequest(req)
	assert.Nil(t, wire.System[0].CacheControl)
	assert.Nil(t, wire.Tools[0].CacheControl)
	require.Len(t, wire.Messages, 1)
	require.Len(t, wire.Messages[0].Content, 1)
	require.NotNil(t, wire.Messages[0].Content[0].CacheControl)
	assert.Equal(t, "ephemeral", wire.Messages[0].Content[0].CacheControl.Type)
}

func TestAnthropicTranslateRequest_NoCacheableHints(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "system", Content: "rules"},
			{Role: "user", Content: "go"},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "t1", Parameters: json.RawMessage(`{}`)}},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.System, 1)
	assert.Nil(t, wire.System[0].CacheControl)
	assert.Nil(t, wire.Tools[0].CacheControl)
	for _, content := range wire.Messages[0].Content {
		assert.Nil(t, content.CacheControl)
	}
}

func TestAnthropicResponse_CacheTokensSurfaced(t *testing.T) {
	ar := &anthropicResponse{
		Usage: &anthropicUsage{
			InputTokens:              100,
			OutputTokens:             50,
			CacheCreationInputTokens: 1200,
			CacheReadInputTokens:     800,
		},
	}

	resp := ar.toResponse()
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 100, resp.Usage.PromptTokens)
	assert.Equal(t, 50, resp.Usage.CompletionTokens)
	assert.Equal(t, 150, resp.Usage.TotalTokens)
	assert.Equal(t, 1200, resp.Usage.CacheCreationInputTokens)
	assert.Equal(t, 800, resp.Usage.CacheReadInputTokens)
}

// Two consecutive same-role messages get coalesced into a single
// anthropicMessage with concatenated content blocks. If the FIRST one is
// Cacheable and the second is not, the marker lands on the first input's
// last block — which after coalesce sits in the MIDDLE of the merged
// content array, not the end. This is intentional: Anthropic caches "up
// to and including the marked block", so the cached prefix correctly
// stops at the cacheable input's boundary, exactly as the caller asked.
// Pin this invariant so a refactor can't accidentally move the marker
// (e.g. always-tail-of-merged-message) without explicit intent.
func TestAnthropicTranslateRequest_CacheableMessage_SurvivesCoalesceInMiddle(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "user", Content: "static-prefix", Cacheable: true},
			{Role: "user", Content: "dynamic-suffix"},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.Messages, 1, "two same-role user messages must coalesce")
	require.Len(t, wire.Messages[0].Content, 2)
	assert.Equal(t, "static-prefix", wire.Messages[0].Content[0].Text)
	require.NotNil(t, wire.Messages[0].Content[0].CacheControl, "marker stays on the cacheable input's block")
	assert.Equal(t, "dynamic-suffix", wire.Messages[0].Content[1].Text)
	assert.Nil(t, wire.Messages[0].Content[1].CacheControl, "non-cacheable suffix must not get a marker")
}

// When BOTH messages in a same-role run are Cacheable, the winner is the
// LAST one in input order, so the marker lands on the merged content's
// final block (the natural tail of the cached prefix).
func TestAnthropicTranslateRequest_CacheableMessage_LastWinnerLandsAtTail(t *testing.T) {
	c := &anthropicClient{model: "claude-test"}
	req := &Request{
		Messages: []Message{
			{Role: "user", Content: "first", Cacheable: true},
			{Role: "user", Content: "second", Cacheable: true},
		},
	}

	wire := c.translateRequest(req)
	require.Len(t, wire.Messages, 1)
	require.Len(t, wire.Messages[0].Content, 2)
	assert.Nil(t, wire.Messages[0].Content[0].CacheControl)
	require.NotNil(t, wire.Messages[0].Content[1].CacheControl)
}

// The OpenAI-compatible client must silently ignore Cacheable hints —
// they are an Anthropic-only concept and the openai wire request type
// has no field to receive them. Pin this so a refactor can't accidentally
// leak Cacheable into openaiRequest.
func TestOpenAIClient_IgnoresCacheableHint(t *testing.T) {
	// Build a request with cacheable hints and translate via the JSON the
	// OpenAI wire path would emit. The Cacheable field on Message/Tool is
	// `json:"-"`, so it must not appear in the marshaled body.
	req := openaiRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "system", Content: "rules", Cacheable: true},
			{Role: "user", Content: "go", Cacheable: true},
		},
		Tools: []Tool{
			{Type: "function", Function: ToolFunction{Name: "t1"}, Cacheable: true},
		},
	}
	body, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(body), "cacheable")
	assert.NotContains(t, string(body), "cache_control")
}
