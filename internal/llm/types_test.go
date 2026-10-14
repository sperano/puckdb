package llm

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestJSON(t *testing.T) {
	req := Request{
		Messages: []Message{
			{Role: "user", Content: "Hello"},
		},
		MaxTokens: 100,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded Request
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, req.Messages[0].Content, decoded.Messages[0].Content)
	assert.Equal(t, req.MaxTokens, decoded.MaxTokens)
}

func TestRequestOmitsEmptyTools(t *testing.T) {
	req := Request{
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

func TestResponseHelpers(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		resp := &Response{}
		assert.False(t, resp.HasToolCalls())
		assert.Empty(t, resp.Content)
	})

	t.Run("text response", func(t *testing.T) {
		resp := &Response{
			Content: "Wayne Gretzky scored 894 goals.",
		}
		assert.False(t, resp.HasToolCalls())
		assert.Equal(t, "Wayne Gretzky scored 894 goals.", resp.Content)
	})

	t.Run("tool call response", func(t *testing.T) {
		resp := &Response{
			ToolCalls: []ToolCall{
				{ID: "tc_1", Type: "function", Function: ToolCallFunction{Name: "get_stats"}},
			},
		}
		assert.True(t, resp.HasToolCalls())
		assert.Len(t, resp.ToolCalls, 1)
		assert.Equal(t, "get_stats", resp.ToolCalls[0].Function.Name)
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

// Every token class Usage carries must be summed by Add. The fields are
// enumerated by reflection so a token class added to Usage later fails
// here until Add accumulates it too.
func TestUsageAddSumsEveryTokenClass(t *testing.T) {
	var part Usage
	v := reflect.ValueOf(&part).Elem()
	tokenFields := 0
	for i := range v.NumField() {
		if f := v.Field(i); f.Kind() == reflect.Int {
			tokenFields++
			f.SetInt(int64(tokenFields))
		}
	}
	require.Positive(t, tokenFields)

	var sum Usage
	sum.Add(&part)
	sum.Add(&part)

	got := reflect.ValueOf(sum)
	for i := range got.NumField() {
		if f := got.Field(i); f.Kind() == reflect.Int {
			name := got.Type().Field(i).Name
			assert.Equal(t, 2*v.Field(i).Int(), f.Int(), "Add must sum %s", name)
		}
	}
}

func TestUsageAddCacheReportedAndNil(t *testing.T) {
	var sum Usage
	sum.Add(nil)
	assert.Equal(t, Usage{}, sum, "a response without usage adds nothing")

	sum.Add(&Usage{PromptTokens: 1})
	assert.False(t, sum.CacheReported)
	sum.Add(&Usage{PromptTokens: 1, CacheReported: true})
	assert.True(t, sum.CacheReported, "one part reporting cache accounting makes the sum report it")
	sum.Add(&Usage{PromptTokens: 1})
	assert.True(t, sum.CacheReported, "a later part without cache accounting does not clear it")
	assert.Equal(t, 3, sum.PromptTokens)
}
