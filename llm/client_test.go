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

func TestChatCompletion_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req ChatCompletionRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-model", req.Model)
		assert.False(t, req.Stream)
		assert.Len(t, req.Messages, 1)
		assert.Equal(t, "user", req.Messages[0].Role)

		resp := ChatCompletionResponse{
			ID:    "chatcmpl-123",
			Model: "test-model",
			Choices: []Choice{
				{
					Index:        0,
					Message:      Message{Role: "assistant", Content: "Hello!"},
					FinishReason: "stop",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "test-model")
	resp, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "Hello!", resp.FirstContent())
	assert.False(t, resp.HasToolCalls())
}

func TestChatCompletion_WithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := ChatCompletionResponse{
			Choices: []Choice{
				{
					Message: Message{
						Role: "assistant",
						ToolCalls: []ToolCall{
							{
								ID:   "call_abc",
								Type: "function",
								Function: ToolCallFunction{
									Name:      "pg_read_query",
									Arguments: `{"sql":"SELECT * FROM players LIMIT 5"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "test-model")
	resp, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "Show me players"}},
	})

	require.NoError(t, err)
	assert.True(t, resp.HasToolCalls())
	calls := resp.FirstToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "pg_read_query", calls[0].Function.Name)
}

func TestChatCompletion_APIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk-test-key", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "sk-test-key", "model")
	_, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
}

func TestChatCompletion_NoAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "model")
	_, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
}

func TestChatCompletion_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "model")
	_, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 429")
}

func TestChatCompletion_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "model")
	_, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestChatCompletion_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't respond — let context cancel
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "model")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := client.ChatCompletion(ctx, &ChatCompletionRequest{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.Error(t, err)
}

func TestChatCompletion_SetsModelAndStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ChatCompletionRequest
		json.NewDecoder(r.Body).Decode(&req)
		// Verify the client overrides model and stream
		assert.Equal(t, "override-model", req.Model)
		assert.False(t, req.Stream)
		json.NewEncoder(w).Encode(ChatCompletionResponse{
			Choices: []Choice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewClient(server.URL, "", "override-model")
	_, err := client.ChatCompletion(context.Background(), &ChatCompletionRequest{
		Model:    "should-be-overridden",
		Stream:   true, // should be forced to false
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
}
