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

	mu     sync.Mutex
	tools  []mcpgo.Tool
	caller ToolCaller
	ready  bool
}

// ToolCaller invokes a tool from a previously advertised discovery snapshot.
type ToolCaller interface {
	CallTool(ctx context.Context, name string, arguments json.RawMessage) (*ToolResult, error)
}

// LLMToolSet keeps advertised LLM tools and their routes in one snapshot.
type LLMToolSet struct {
	Tools  []llm.Tool
	Caller ToolCaller
}

type snapshotLister interface {
	listToolsSnapshot(ctx context.Context) ([]mcpgo.Tool, ToolCaller, error)
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
	tc.caller = tc.client
	tc.ready = true
	return tc.tools, nil
}

// GetLLMTools converts MCP tools to the llm.Tool format suitable for OpenAI-compatible APIs.
func (tc *ToolCache) GetLLMTools(ctx context.Context) ([]llm.Tool, error) {
	mcpTools, err := tc.GetTools(ctx)
	return ConvertTools(mcpTools), err
}

// GetLLMToolSet returns tools and a caller bound to the same discovery
// snapshot. Partial discoveries are returned but not cached.
func (tc *ToolCache) GetLLMToolSet(ctx context.Context) (*LLMToolSet, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if tc.ready {
		return newLLMToolSet(tc.tools, tc.caller), nil
	}

	tools, caller, err := tc.listToolsSnapshot(ctx)
	if err != nil {
		return newLLMToolSet(tools, caller), err
	}
	tc.tools = tools
	tc.caller = caller
	tc.ready = true
	return newLLMToolSet(tc.tools, tc.caller), nil
}

func (tc *ToolCache) listToolsSnapshot(ctx context.Context) ([]mcpgo.Tool, ToolCaller, error) {
	if client, ok := tc.client.(snapshotLister); ok {
		return client.listToolsSnapshot(ctx)
	}
	tools, err := tc.client.ListTools(ctx)
	return tools, tc.client, err
}

func newLLMToolSet(tools []mcpgo.Tool, caller ToolCaller) *LLMToolSet {
	return &LLMToolSet{Tools: ConvertTools(tools), Caller: caller}
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
