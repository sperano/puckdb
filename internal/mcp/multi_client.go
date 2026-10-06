package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

const serverNumberOffset = 1

// MultiClient merges tools from multiple MCP servers and routes calls by tool name.
type MultiClient struct {
	entries []clientEntry

	mu     sync.Mutex
	routes map[string]Client // tool name → owning client
}

type clientEntry struct {
	name      string
	client    Client
	allowList map[string]bool // nil = allow all tools
}

// MultiClientOption configures a client entry in the MultiClient.
type MultiClientOption struct {
	Name   string
	Client Client
	Tools  []string // whitelist; nil/empty = all tools
}

// DiscoveryError reports MCP servers whose tools could not be discovered.
// A ListTools call may return both healthy tools and a DiscoveryError.
type DiscoveryError struct {
	Failures []ServerDiscoveryFailure
}

// ServerDiscoveryFailure identifies one MCP server that failed discovery.
type ServerDiscoveryFailure struct {
	Server string
	Err    error
}

func (e *DiscoveryError) Error() string {
	failures := make([]string, len(e.Failures))
	for i, failure := range e.Failures {
		failures[i] = fmt.Sprintf("%s: %v", failure.Server, failure.Err)
	}
	return "MCP tool discovery failed for " + strings.Join(failures, "; ")
}

// Unwrap exposes every underlying server error to errors.Is/errors.As.
func (e *DiscoveryError) Unwrap() []error {
	errs := make([]error, len(e.Failures))
	for i, failure := range e.Failures {
		errs[i] = failure.Err
	}
	return errs
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
		name := o.Name
		if name == "" {
			name = fmt.Sprintf("server %d", i+serverNumberOffset)
		}
		entries[i] = clientEntry{name: name, client: o.Client, allowList: allow}
	}
	return &MultiClient{entries: entries}
}

func (mc *MultiClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	mc.mu.Lock()
	defer mc.mu.Unlock()

	routes := make(map[string]Client)
	var all []mcpgo.Tool
	var failures []ServerDiscoveryFailure

	for _, e := range mc.entries {
		tools, err := e.client.ListTools(ctx)
		if err != nil {
			failures = append(failures, ServerDiscoveryFailure{Server: e.name, Err: err})
			continue
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

	// Publish the routes from the same discovery snapshot as the returned
	// tools. Routes belonging to a server that failed this refresh are removed.
	mc.routes = routes
	if len(failures) > 0 {
		return all, &DiscoveryError{Failures: failures}
	}
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
