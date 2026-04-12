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

func TestComplete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/chat/completions", r.URL.Path)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		var req openaiRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		assert.Equal(t, "test-model", req.Model)
		assert.False(t, req.Stream)
		assert.Len(t, req.Messages, 1)
		assert.Equal(t, "user", req.Messages[0].Role)

		resp := openaiResponse{
			ID:    "chatcmpl-123",
			Model: "test-model",
			Choices: []openaiChoice{
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

	client := NewOpenAIClient(server.URL, "", "test-model")
	resp, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "Hi"}},
	})

	require.NoError(t, err)
	assert.Equal(t, "Hello!", resp.Content)
	assert.False(t, resp.HasToolCalls())
}

func TestComplete_WithToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openaiResponse{
			Choices: []openaiChoice{
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

	client := NewOpenAIClient(server.URL, "", "test-model")
	resp, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "Show me players"}},
	})

	require.NoError(t, err)
	assert.True(t, resp.HasToolCalls())
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "pg_read_query", resp.ToolCalls[0].Function.Name)
}

func TestComplete_APIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer sk-test-key", r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(openaiResponse{
			Choices: []openaiChoice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "sk-test-key", "model")
	_, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
}

func TestComplete_NoAPIKeyHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Authorization"))
		json.NewEncoder(w).Encode(openaiResponse{
			Choices: []openaiChoice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "", "model")
	_, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
}

func TestComplete_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "", "model")
	_, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "status 429")
}

func TestComplete_MalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{invalid json`))
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "", "model")
	_, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshal")
}

func TestComplete_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "", "model")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := client.Complete(ctx, &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.Error(t, err)
}

func TestComplete_SetsModelAndStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openaiRequest
		json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "override-model", req.Model)
		assert.False(t, req.Stream)
		json.NewEncoder(w).Encode(openaiResponse{
			Choices: []openaiChoice{{Message: Message{Content: "ok"}}},
		})
	}))
	defer server.Close()

	client := NewOpenAIClient(server.URL, "", "override-model")
	_, err := client.Complete(context.Background(), &Request{
		Messages: []Message{{Role: "user", Content: "test"}},
	})
	require.NoError(t, err)
}
