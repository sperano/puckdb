package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSession is a partial mcpclient.MCPClient. The embedded interface leaves
// the methods the wrapper never calls trapped as nil; tests override only
// ListTools, CallTool and Close.
type fakeSession struct {
	mcpclient.MCPClient

	listTools func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error)
	callTool  func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error)
	closeErr  error

	listCalls int
	callCalls int
	closes    int
}

func (f *fakeSession) ListTools(ctx context.Context, req mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
	f.listCalls++
	if f.listTools == nil {
		return &mcpgo.ListToolsResult{}, nil
	}
	return f.listTools(ctx, req)
}

func (f *fakeSession) CallTool(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	f.callCalls++
	if f.callTool == nil {
		return &mcpgo.CallToolResult{}, nil
	}
	return f.callTool(ctx, req)
}

func (f *fakeSession) Close() error {
	f.closes++
	return f.closeErr
}

func sessionError() error   { return errors.New("session not found") }
func operationError() error { return errors.New("operation failed") }
func listResult(name string) *mcpgo.ListToolsResult {
	return &mcpgo.ListToolsResult{Tools: []mcpgo.Tool{mcpgo.NewTool(name)}}
}

// dialSequence returns queued dial results in order and records the call count.
// errs takes precedence over clients at the same index so a test can make the
// second dial fail while the first succeeds.
type dialSequence struct {
	clients []mcpclient.MCPClient
	errs    []error
	calls   int
}

func (d *dialSequence) dial(_ context.Context, _ string) (mcpclient.MCPClient, error) {
	i := d.calls
	d.calls++
	if i < len(d.errs) && d.errs[i] != nil {
		return nil, d.errs[i]
	}
	if i < len(d.clients) {
		return d.clients[i], nil
	}
	return nil, errors.New("unexpected dial call")
}

func newSessionTestClient(dial *dialSequence) *mcpClient {
	return &mcpClient{url: "http://mcp.test", dial: dial.dial}
}

func TestMCPClient_ListTools_ConnectFailure(t *testing.T) {
	dial := &dialSequence{errs: []error{errors.New("no route")}}
	c := newSessionTestClient(dial)

	_, err := c.ListTools(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "connect:")
	assert.ErrorContains(t, err, "no route")
	assert.Equal(t, 1, dial.calls)
}

func TestMCPClient_ListTools_InitialSuccess(t *testing.T) {
	inner := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return listResult("search"), nil
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{inner}}
	c := newSessionTestClient(dial)

	tools, err := c.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, "search", tools[0].Name)
	assert.Equal(t, 1, dial.calls)
	assert.Equal(t, 1, inner.listCalls)
	assert.Equal(t, 0, inner.closes)
}

func TestMCPClient_ListTools_ReusesConnection(t *testing.T) {
	inner := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return listResult("search"), nil
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{inner}}
	c := newSessionTestClient(dial)

	for range 2 {
		_, err := c.ListTools(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 1, dial.calls, "the connection should be dialed once")
	assert.Equal(t, 2, inner.listCalls)
}

func TestMCPClient_ListTools_ReconnectsOnceAfterSessionError(t *testing.T) {
	dead := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, sessionError()
	}}
	live := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return listResult("search"), nil
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{dead, live}}
	c := newSessionTestClient(dial)

	tools, err := c.ListTools(context.Background())
	require.NoError(t, err)
	require.Len(t, tools, 1)
	assert.Equal(t, 2, dial.calls)
	assert.Equal(t, 1, dead.listCalls)
	assert.Equal(t, 1, live.listCalls)
	assert.Equal(t, 1, dead.closes, "reconnect should close the dead session")
}

func TestMCPClient_ListTools_ReconnectFailure(t *testing.T) {
	dead := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, sessionError()
	}}
	dial := &dialSequence{
		clients: []mcpclient.MCPClient{dead},
		errs:    []error{nil, errors.New("dial refused")},
	}
	c := newSessionTestClient(dial)

	_, err := c.ListTools(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "reconnect")
	assert.ErrorContains(t, err, "dial refused")
	assert.Equal(t, 2, dial.calls)
}

func TestMCPClient_ListTools_RetryAfterReconnectFails(t *testing.T) {
	dead := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, sessionError()
	}}
	live := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, operationError()
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{dead, live}}
	c := newSessionTestClient(dial)

	_, err := c.ListTools(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "list tools")
	assert.ErrorContains(t, err, "operation failed")
	assert.Equal(t, 2, dial.calls)
	assert.Equal(t, 1, live.listCalls)
}

func TestMCPClient_ListTools_RetriesAtMostOnce(t *testing.T) {
	first := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, sessionError()
	}}
	second := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, sessionError()
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{first, second}}
	c := newSessionTestClient(dial)

	_, err := c.ListTools(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "list tools")
	assert.Equal(t, 2, dial.calls, "a second session error must not trigger another reconnect")
	assert.Equal(t, 1, second.listCalls)
}

func TestMCPClient_ListTools_NonSessionErrorDoesNotReconnect(t *testing.T) {
	inner := &fakeSession{listTools: func(context.Context, mcpgo.ListToolsRequest) (*mcpgo.ListToolsResult, error) {
		return nil, operationError()
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{inner}}
	c := newSessionTestClient(dial)

	_, err := c.ListTools(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "list tools")
	assert.ErrorContains(t, err, "operation failed")
	assert.Equal(t, 1, dial.calls)
	assert.Equal(t, 1, inner.listCalls)
}

func TestMCPClient_CallTool_InitialSuccess(t *testing.T) {
	inner := &fakeSession{callTool: func(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		assert.Equal(t, "search", req.Params.Name)
		assert.Equal(t, map[string]any{"q": "x"}, req.Params.Arguments)
		return &mcpgo.CallToolResult{Content: []mcpgo.Content{mcpgo.NewTextContent("hello")}}, nil
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{inner}}
	c := newSessionTestClient(dial)

	result, err := c.CallTool(context.Background(), "search", json.RawMessage(`{"q":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "hello", result.Content)
	assert.False(t, result.IsError)
	assert.Equal(t, 1, dial.calls)
}

func TestMCPClient_CallTool_ReconnectsOnceAfterSessionError(t *testing.T) {
	dead := &fakeSession{callTool: func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return nil, sessionError()
	}}
	live := &fakeSession{callTool: func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return &mcpgo.CallToolResult{Content: []mcpgo.Content{mcpgo.NewTextContent("ok")}}, nil
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{dead, live}}
	c := newSessionTestClient(dial)

	result, err := c.CallTool(context.Background(), "search", nil)
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Content)
	assert.Equal(t, 2, dial.calls)
	assert.Equal(t, 1, dead.callCalls)
	assert.Equal(t, 1, live.callCalls)
	assert.Equal(t, 1, dead.closes)
}

func TestMCPClient_CallTool_ReconnectFailure(t *testing.T) {
	dead := &fakeSession{callTool: func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return nil, sessionError()
	}}
	dial := &dialSequence{
		clients: []mcpclient.MCPClient{dead},
		errs:    []error{nil, errors.New("dial refused")},
	}
	c := newSessionTestClient(dial)

	_, err := c.CallTool(context.Background(), "search", nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "reconnect")
	assert.ErrorContains(t, err, "dial refused")
	assert.Equal(t, 2, dial.calls)
}

func TestMCPClient_CallTool_NonSessionErrorDoesNotReconnect(t *testing.T) {
	inner := &fakeSession{callTool: func(context.Context, mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
		return nil, operationError()
	}}
	dial := &dialSequence{clients: []mcpclient.MCPClient{inner}}
	c := newSessionTestClient(dial)

	_, err := c.CallTool(context.Background(), "search", nil)
	require.Error(t, err)
	assert.ErrorContains(t, err, "call tool search")
	assert.ErrorContains(t, err, "operation failed")
	assert.Equal(t, 1, dial.calls)
}

func TestMCPClient_CallTool_InvalidArguments(t *testing.T) {
	dial := &dialSequence{}
	c := newSessionTestClient(dial)

	_, err := c.CallTool(context.Background(), "search", json.RawMessage(`{`))
	require.Error(t, err)
	assert.ErrorContains(t, err, "unmarshal tool arguments")
	assert.Equal(t, 0, dial.calls, "invalid arguments should not open a connection")
}

func TestMCPClient_Close_NoConnection(t *testing.T) {
	c := newSessionTestClient(&dialSequence{})
	assert.NoError(t, c.Close())
}

func TestMCPClient_Close_ReturnsInnerErrorAndClears(t *testing.T) {
	inner := &fakeSession{closeErr: errors.New("close failed")}
	c := newSessionTestClient(&dialSequence{})
	c.inner = inner

	err := c.Close()
	require.Error(t, err)
	assert.EqualError(t, err, "close failed")
	assert.Equal(t, 1, inner.closes)
	assert.NoError(t, c.Close(), "the connection is cleared even when close fails")
	assert.Equal(t, 1, inner.closes)
}

func TestMCPClient_reconnect_DiscardsOldCloseError(t *testing.T) {
	old := &fakeSession{closeErr: errors.New("old close failed")}
	fresh := &fakeSession{}
	dial := &dialSequence{clients: []mcpclient.MCPClient{fresh}}
	c := newSessionTestClient(dial)
	c.inner = old

	inner, err := c.reconnect(context.Background())
	require.NoError(t, err)
	assert.Same(t, fresh, inner)
	assert.Equal(t, 1, old.closes)
	assert.Equal(t, 1, dial.calls)
}

func TestMCPClient_reconnect_CloseErrorDoesNotMaskDialError(t *testing.T) {
	old := &fakeSession{closeErr: errors.New("old close failed")}
	dial := &dialSequence{errs: []error{errors.New("dial failed")}}
	c := newSessionTestClient(dial)
	c.inner = old

	_, err := c.reconnect(context.Background())
	require.Error(t, err)
	assert.ErrorContains(t, err, "dial failed")
	assert.NotContains(t, err.Error(), "old close failed")
	assert.Equal(t, 1, old.closes)
	assert.Nil(t, c.inner)
}
