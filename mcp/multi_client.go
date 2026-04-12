package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// MultiClient merges tools from multiple MCP servers and routes calls by tool name.
type MultiClient struct {
	entries []clientEntry

	mu     sync.Mutex
	routes map[string]Client // tool name → owning client
}

type clientEntry struct {
	client    Client
	allowList map[string]bool // nil = allow all tools
}

// MultiClientOption configures a client entry in the MultiClient.
type MultiClientOption struct {
	Client Client
	Tools  []string // whitelist; nil/empty = all tools
}

// NewMultiClient creates a client that unions tools from multiple MCP servers.
func NewMultiClient(opts ...MultiClientOption) *MultiClient {
	entries := make([]clientEntry, len(opts))
	for i, o := range opts {
		var allow map[string]bool
		if len(o.Tools) > 0 {
			allow = make(map[string]bool, len(o.Tools))
			for _, t := range o.Tools {
				allow[t] = true
			}
		}
		entries[i] = clientEntry{client: o.Client, allowList: allow}
	}
	return &MultiClient{entries: entries}
}

func (mc *MultiClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	routes := make(map[string]Client)
	var all []mcpgo.Tool

	for _, e := range mc.entries {
		tools, err := e.client.ListTools(ctx)
		if err != nil {
			return nil, fmt.Errorf("list tools: %w", err)
		}
		for _, t := range tools {
			if e.allowList != nil && !e.allowList[t.Name] {
				continue
			}
			if _, exists := routes[t.Name]; exists {
				continue // first server wins on name collision
			}
			routes[t.Name] = e.client
			all = append(all, t)
		}
	}

	mc.routes = routes
	return all, nil
}

func (mc *MultiClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*ToolResult, error) {
	mc.mu.Lock()
	client, ok := mc.routes[name]
	mc.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
	return client.CallTool(ctx, name, arguments)
}

func (mc *MultiClient) Close() error {
	var firstErr error
	for _, e := range mc.entries {
		if err := e.client.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
