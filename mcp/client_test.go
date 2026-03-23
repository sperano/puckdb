package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockClient_CallTool(t *testing.T) {
	mock := &mockClient{}
	result, err := mock.CallTool(context.Background(), "test", json.RawMessage(`{}`))
	require.NoError(t, err)
	assert.Equal(t, "mock result", result.Content)
	assert.False(t, result.IsError)
}

func TestMockClient_Close(t *testing.T) {
	mock := &mockClient{}
	assert.NoError(t, mock.Close())
}
