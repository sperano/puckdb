package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChatCompletionRequestJSON(t *testing.T) {
	req := ChatCompletionRequest{
		Model: "test-model",
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 100,
		Stream:    false,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded ChatCompletionRequest
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, req.Model, decoded.Model)
	assert.Equal(t, req.Messages[0].Content, decoded.Messages[0].Content)
	assert.Equal(t, req.MaxTokens, decoded.MaxTokens)
	assert.False(t, decoded.Stream)
}

func TestChatCompletionRequestOmitsEmptyTools(t *testing.T) {
	req := ChatCompletionRequest{
		Model:    "test-model",
		Messages: []Message{{Role: "user", Content: "Hi"}},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)
	assert.NotContains(t, string(data), "tools")
}

func TestMessageWithToolCalls(t *testing.T) {
	msg := Message{
		Role: "assistant",
		ToolCalls: []ToolCall{
			{
				ID:   "call_1",
				Type: "function",
				Function: ToolCallFunction{
					Name:      "get_player",
					Arguments: `{"name":"Gretzky"}`,
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Len(t, decoded.ToolCalls, 1)
	assert.Equal(t, "call_1", decoded.ToolCalls[0].ID)
	assert.Equal(t, "get_player", decoded.ToolCalls[0].Function.Name)
}

func TestMessageWithToolCallID(t *testing.T) {
	msg := Message{
		Role:       "tool",
		Content:    `{"goals": 894}`,
		ToolCallID: "call_1",
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var decoded Message
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "tool", decoded.Role)
	assert.Equal(t, "call_1", decoded.ToolCallID)
}

func TestToolJSON(t *testing.T) {
	tool := Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "search",
			Description: "Search for players",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"}}}`),
		},
	}

	data, err := json.Marshal(tool)
	require.NoError(t, err)

	var decoded Tool
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, "function", decoded.Type)
	assert.Equal(t, "search", decoded.Function.Name)
	assert.Contains(t, string(decoded.Function.Parameters), "query")
}

func TestChatCompletionResponseHelpers(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		resp := &ChatCompletionResponse{}
		assert.False(t, resp.HasToolCalls())
		assert.Empty(t, resp.FirstContent())
		assert.Nil(t, resp.FirstToolCalls())
	})

	t.Run("text response", func(t *testing.T) {
		resp := &ChatCompletionResponse{
			Choices: []Choice{
				{Message: Message{Content: "Wayne Gretzky scored 894 goals."}},
			},
		}
		assert.False(t, resp.HasToolCalls())
		assert.Equal(t, "Wayne Gretzky scored 894 goals.", resp.FirstContent())
	})

	t.Run("tool call response", func(t *testing.T) {
		resp := &ChatCompletionResponse{
			Choices: []Choice{
				{
					Message: Message{
						ToolCalls: []ToolCall{
							{ID: "tc_1", Type: "function", Function: ToolCallFunction{Name: "get_stats"}},
						},
					},
				},
			},
		}
		assert.True(t, resp.HasToolCalls())
		calls := resp.FirstToolCalls()
		assert.Len(t, calls, 1)
		assert.Equal(t, "get_stats", calls[0].Function.Name)
	})
}

func TestUsageJSON(t *testing.T) {
	usage := Usage{
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}

	data, err := json.Marshal(usage)
	require.NoError(t, err)

	var decoded Usage
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, usage, decoded)
}
