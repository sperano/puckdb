package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// ToolResult holds the text output from a single MCP tool call.
type ToolResult struct {
	Content string
	IsError bool
}

// Client provides access to MCP server tools.
type Client interface {
	ListTools(ctx context.Context) ([]mcpgo.Tool, error)
	CallTool(ctx context.Context, name string, arguments json.RawMessage) (*ToolResult, error)
	Close() error
}

// noopClient is a Client that has no tools and does nothing.
type noopClient struct{}

// NewNoopClient creates a Client with no tools.
func NewNoopClient() Client { return &noopClient{} }

func (c *noopClient) ListTools(_ context.Context) ([]mcpgo.Tool, error) { return nil, nil }
func (c *noopClient) CallTool(_ context.Context, name string, _ json.RawMessage) (*ToolResult, error) {
	return nil, fmt.Errorf("no MCP server configured for tool: %s", name)
}
func (c *noopClient) Close() error { return nil }

// mcpClient wraps the mcp-go SDK client with lazy connection and automatic reconnection.
// The MCP server uses in-memory sessions that expire quickly, so we connect on first use
// and reconnect transparently when a session becomes invalid.
type mcpClient struct {
	url   string
	mu    sync.Mutex
	inner mcpclient.MCPClient
}

// NewClient creates an MCP client that connects lazily on first use.
func NewClient(url string) Client {
	return &mcpClient{url: url}
}

// connect establishes a new MCP connection, closing any existing one.
func (c *mcpClient) connect(ctx context.Context) error {
	if c.inner != nil {
		c.inner.Close()
		c.inner = nil
	}

	inner, err := dialMCP(ctx, c.url)
	if err != nil {
		return err
	}
	c.inner = inner
	return nil
}

// ensureConnected returns the existing connection or creates a new one.
func (c *mcpClient) ensureConnected(ctx context.Context) (mcpclient.MCPClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.inner != nil {
		return c.inner, nil
	}
	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c.inner, nil
}

// reconnect forces a new connection (called after session errors).
func (c *mcpClient) reconnect(ctx context.Context) (mcpclient.MCPClient, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.connect(ctx); err != nil {
		return nil, err
	}
	return c.inner, nil
}

func dialMCP(ctx context.Context, url string) (mcpclient.MCPClient, error) {
	// Try streamable HTTP first (newer protocol)
	if !strings.HasSuffix(url, "/sse") {
		c, err := mcpclient.NewStreamableHttpClient(url)
		if err == nil {
			if _, err := c.Initialize(ctx, mcpgo.InitializeRequest{}); err == nil {
				return c, nil
			}
			c.Close()
		}
	}

	// Fall back to SSE
	sseURL := url
	if !strings.HasSuffix(sseURL, "/sse") {
		sseURL = strings.TrimSuffix(sseURL, "/") + "/sse"
	}
	c, err := mcpclient.NewSSEMCPClient(sseURL)
	if err != nil {
		return nil, fmt.Errorf("create SSE client for %s: %w", sseURL, err)
	}
	if _, err := c.Initialize(ctx, mcpgo.InitializeRequest{}); err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize MCP at %s: %w", sseURL, err)
	}
	return c, nil
}

// isSessionError returns true if the error indicates an expired/invalid MCP session.
func isSessionError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "session") ||
		strings.Contains(msg, "Session") ||
		strings.Contains(msg, "404") ||
		strings.Contains(msg, "terminated")
}

func (c *mcpClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	inner, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	result, err := inner.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil && isSessionError(err) {
		// Session expired — reconnect and retry once
		inner, err = c.reconnect(ctx)
		if err != nil {
			return nil, fmt.Errorf("reconnect: %w", err)
		}
		result, err = inner.ListTools(ctx, mcpgo.ListToolsRequest{})
	}
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return result.Tools, nil
}

func (c *mcpClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*ToolResult, error) {
	inner, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	var args map[string]any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return nil, fmt.Errorf("unmarshal tool arguments: %w", err)
		}
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	callResult, err := inner.CallTool(ctx, req)
	if err != nil && isSessionError(err) {
		inner, err = c.reconnect(ctx)
		if err != nil {
			return nil, fmt.Errorf("reconnect: %w", err)
		}
		callResult, err = inner.CallTool(ctx, req)
	}
	if err != nil {
		return nil, fmt.Errorf("call tool %s: %w", name, err)
	}

	var text strings.Builder
	for _, content := range callResult.Content {
		if tc, ok := mcpgo.AsTextContent(content); ok {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(tc.Text)
		}
	}

	return &ToolResult{
		Content: text.String(),
		IsError: callResult.IsError,
	}, nil
}

func (c *mcpClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.inner != nil {
		err := c.inner.Close()
		c.inner = nil
		return err
	}
	return nil
}
