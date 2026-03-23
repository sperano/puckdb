package mcp

import (
	"context"
	"encoding/json"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/llm"
)

// ToolCache wraps an MCP Client and caches the tool list after the first fetch.
type ToolCache struct {
	client Client

	once  sync.Once
	tools []mcpgo.Tool
	err   error
}

// NewToolCache creates a ToolCache that lazily fetches tools from the given Client.
func NewToolCache(client Client) *ToolCache {
	return &ToolCache{client: client}
}

// GetTools returns the cached tool list, fetching from the MCP server on first call.
func (tc *ToolCache) GetTools(ctx context.Context) ([]mcpgo.Tool, error) {
	tc.once.Do(func() {
		tc.tools, tc.err = tc.client.ListTools(ctx)
	})
	return tc.tools, tc.err
}

// GetLLMTools converts MCP tools to the llm.Tool format suitable for OpenAI-compatible APIs.
func (tc *ToolCache) GetLLMTools(ctx context.Context) ([]llm.Tool, error) {
	mcpTools, err := tc.GetTools(ctx)
	if err != nil {
		return nil, err
	}
	return ConvertTools(mcpTools), nil
}

// ConvertTools converts MCP tools to the llm.Tool format.
func ConvertTools(mcpTools []mcpgo.Tool) []llm.Tool {
	result := make([]llm.Tool, len(mcpTools))
	for i, t := range mcpTools {
		result[i] = ConvertTool(t)
	}
	return result
}

// ConvertTool converts a single MCP tool to llm.Tool format.
func ConvertTool(t mcpgo.Tool) llm.Tool {
	params, _ := json.Marshal(t.InputSchema)

	return llm.Tool{
		Type: "function",
		Function: llm.ToolFunction{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		},
	}
}
