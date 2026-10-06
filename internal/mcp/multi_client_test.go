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

type routingClient struct {
	tools       []mcpgo.Tool
	listErr     error
	calledTools []string
}

func (c *routingClient) ListTools(context.Context) ([]mcpgo.Tool, error) {
	return c.tools, c.listErr
}

func (c *routingClient) CallTool(_ context.Context, name string, _ json.RawMessage) (*ToolResult, error) {
	c.calledTools = append(c.calledTools, name)
	return &ToolResult{Content: name}, nil
}

func (c *routingClient) Close() error { return nil }

func TestMultiClientListToolsPartialDiscoveryPublishesHealthyRoutes(t *testing.T) {
	offlineErr := errors.New("offline")
	healthy := &routingClient{tools: []mcpgo.Tool{mcpgo.NewTool("healthy")}}
	failed := &routingClient{listErr: offlineErr}
	client := NewMultiClient(
		MultiClientOption{Name: "stats", Client: healthy},
		MultiClientOption{Name: "fantasy", Client: failed},
	)

	tools, err := client.ListTools(t.Context())

	var discoveryErr *DiscoveryError
	require.ErrorAs(t, err, &discoveryErr)
	require.ErrorIs(t, err, offlineErr)
	require.Len(t, discoveryErr.Failures, 1)
	assert.Equal(t, "fantasy", discoveryErr.Failures[0].Server)
	require.Len(t, tools, 1)
	assert.Equal(t, "healthy", tools[0].Name)

	result, err := client.CallTool(t.Context(), "healthy", nil)
	require.NoError(t, err)
	assert.Equal(t, "healthy", result.Content)
	assert.Equal(t, []string{"healthy"}, healthy.calledTools)
}

func TestMultiClientListToolsRefreshRemovesFailedServerRoutes(t *testing.T) {
	first := &routingClient{tools: []mcpgo.Tool{mcpgo.NewTool("first")}}
	second := &routingClient{tools: []mcpgo.Tool{mcpgo.NewTool("second")}}
	client := NewMultiClient(
		MultiClientOption{Name: "first-server", Client: first},
		MultiClientOption{Name: "second-server", Client: second},
	)

	tools, err := client.ListTools(t.Context())
	require.NoError(t, err)
	require.Len(t, tools, 2)

	second.listErr = errors.New("refresh failed")
	tools, err = client.ListTools(t.Context())
	require.Error(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "first", tools[0].Name)

	_, err = client.CallTool(t.Context(), "second", nil)
	require.EqualError(t, err, "unknown tool: second")
	_, err = client.CallTool(t.Context(), "first", nil)
	require.NoError(t, err)
}

func TestMultiClientListToolsReportsEveryFailedServer(t *testing.T) {
	client := NewMultiClient(
		MultiClientOption{Name: "one", Client: &routingClient{listErr: errors.New("offline")}},
		MultiClientOption{Name: "two", Client: &routingClient{listErr: errors.New("timeout")}},
	)

	tools, err := client.ListTools(t.Context())

	assert.Empty(t, tools)
	var discoveryErr *DiscoveryError
	require.ErrorAs(t, err, &discoveryErr)
	require.Len(t, discoveryErr.Failures, 2)
	assert.Equal(t, "one", discoveryErr.Failures[0].Server)
	assert.Equal(t, "two", discoveryErr.Failures[1].Server)
}
