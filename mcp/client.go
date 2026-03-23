package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

// mcpClient wraps the mcp-go SDK client.
type mcpClient struct {
	inner mcpclient.MCPClient
}

// NewClient connects to an MCP server at the given URL.
// It tries streamable HTTP first, then falls back to SSE.
func NewClient(ctx context.Context, url string) (Client, error) {
	inner, err := connectMCP(ctx, url)
	if err != nil {
		return nil, err
	}
	return &mcpClient{inner: inner}, nil
}

func connectMCP(ctx context.Context, url string) (mcpclient.MCPClient, error) {
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

func (c *mcpClient) ListTools(ctx context.Context) ([]mcpgo.Tool, error) {
	result, err := c.inner.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil {
		return nil, fmt.Errorf("list tools: %w", err)
	}
	return result.Tools, nil
}

func (c *mcpClient) CallTool(ctx context.Context, name string, arguments json.RawMessage) (*ToolResult, error) {
	var args map[string]any
	if len(arguments) > 0 {
		if err := json.Unmarshal(arguments, &args); err != nil {
			return nil, fmt.Errorf("unmarshal tool arguments: %w", err)
		}
	}

	req := mcpgo.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.inner.CallTool(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("call tool %s: %w", name, err)
	}

	var text strings.Builder
	for _, content := range result.Content {
		if tc, ok := mcpgo.AsTextContent(content); ok {
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(tc.Text)
		}
	}

	return &ToolResult{
		Content: text.String(),
		IsError: result.IsError,
	}, nil
}

func (c *mcpClient) Close() error {
	return c.inner.Close()
}
