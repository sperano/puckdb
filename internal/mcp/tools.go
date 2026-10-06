package mcp

import (
	"context"
	"encoding/json"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/sperano/puckdb/internal/llm"
)

// ToolCache wraps an MCP Client and caches the tool list after a successful fetch.
// Unlike sync.Once, a failed fetch is not cached — subsequent calls will retry.
type ToolCache struct {
	client Client

	mu    sync.Mutex
	tools []mcpgo.Tool
	ready bool
}

// NewToolCache creates a ToolCache that lazily fetches tools from the given Client.
func NewToolCache(client Client) *ToolCache {
	return &ToolCache{client: client}
}

// GetTools returns the cached tool list, fetching from the MCP server if not yet cached.
// Errors are not cached — a subsequent call will retry the fetch.
func (tc *ToolCache) GetTools(ctx context.Context) ([]mcpgo.Tool, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.ready {
		return tc.tools, nil
	}

	tools, err := tc.client.ListTools(ctx)
	if err != nil {
		// Preserve partial discovery results for this caller, but do not cache
		// them: failed servers must be retried on the next request.
		return tools, err
	}
	tc.tools = tools
	tc.ready = true
	return tc.tools, nil
}

// GetLLMTools converts MCP tools to the llm.Tool format suitable for OpenAI-compatible APIs.
func (tc *ToolCache) GetLLMTools(ctx context.Context) ([]llm.Tool, error) {
	mcpTools, err := tc.GetTools(ctx)
	return ConvertTools(mcpTools), err
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
