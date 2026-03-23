package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockClient struct {
	tools []mcpgo.Tool
	err   error
	calls int
}

func (m *mockClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	m.calls++
	return m.tools, m.err
}

func (m *mockClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*ToolResult, error) {
	return &ToolResult{Content: "mock result"}, nil
}

func (m *mockClient) Close() error { return nil }

func TestConvertTool(t *testing.T) {
	mcpTool := mcpgo.NewTool("pg_read_query",
		mcpgo.WithDescription("Execute a read-only SQL query"),
		mcpgo.WithString("sql", mcpgo.Required(), mcpgo.Description("The SQL query")),
	)

	llmTool := ConvertTool(mcpTool)
	assert.Equal(t, "function", llmTool.Type)
	assert.Equal(t, "pg_read_query", llmTool.Function.Name)
	assert.Equal(t, "Execute a read-only SQL query", llmTool.Function.Description)
	assert.Contains(t, string(llmTool.Function.Parameters), "sql")
}

func TestConvertTools(t *testing.T) {
	mcpTools := []mcpgo.Tool{
		mcpgo.NewTool("tool_a", mcpgo.WithDescription("A")),
		mcpgo.NewTool("tool_b", mcpgo.WithDescription("B")),
	}

	llmTools := ConvertTools(mcpTools)
	require.Len(t, llmTools, 2)
	assert.Equal(t, "tool_a", llmTools[0].Function.Name)
	assert.Equal(t, "tool_b", llmTools[1].Function.Name)
}

func TestToolCache_CachesAfterFirstCall(t *testing.T) {
	mock := &mockClient{
		tools: []mcpgo.Tool{mcpgo.NewTool("cached_tool")},
	}

	cache := NewToolCache(mock)

	tools1, err := cache.GetTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools1, 1)

	tools2, err := cache.GetTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools2, 1)

	assert.Equal(t, 1, mock.calls, "ListTools should only be called once")
}

func TestToolCache_CachesError(t *testing.T) {
	mock := &mockClient{err: errors.New("connection failed")}
	cache := NewToolCache(mock)

	_, err := cache.GetTools(context.Background())
	require.Error(t, err)

	// Second call returns same cached error
	_, err = cache.GetTools(context.Background())
	require.Error(t, err)
	assert.Equal(t, 1, mock.calls)
}

func TestToolCache_GetLLMTools(t *testing.T) {
	mock := &mockClient{
		tools: []mcpgo.Tool{
			mcpgo.NewTool("pg_count", mcpgo.WithDescription("Count rows")),
		},
	}

	cache := NewToolCache(mock)
	llmTools, err := cache.GetLLMTools(context.Background())
	require.NoError(t, err)
	require.Len(t, llmTools, 1)
	assert.Equal(t, "pg_count", llmTools[0].Function.Name)
	assert.Equal(t, "function", llmTools[0].Type)
}

func TestToolCache_GetLLMTools_Error(t *testing.T) {
	mock := &mockClient{err: errors.New("fail")}
	cache := NewToolCache(mock)

	_, err := cache.GetLLMTools(context.Background())
	require.Error(t, err)
}
