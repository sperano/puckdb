package agentloop

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptedClient returns successive responses. If errs[i] is non-nil that
// round errors instead. Out-of-range rounds return a default no-tool reply
// so a misconfigured test doesn't deadlock — failures should surface as
// assertion failures, not infinite loops.
type scriptedClient struct {
	responses []*llm.Response
	errs      []error
	calls     int
	requests  []*llm.Request
}

func (c *scriptedClient) Complete(_ context.Context, req *llm.Request) (*llm.Response, error) {
	idx := c.calls
	c.calls++
	c.requests = append(c.requests, req)
	if idx < len(c.errs) && c.errs[idx] != nil {
		return nil, c.errs[idx]
	}
	if idx < len(c.responses) {
		return c.responses[idx], nil
	}
	return &llm.Response{Content: "scripted-default"}, nil
}

func toolCallResp(name, id string) *llm.Response {
	return &llm.Response{
		ToolCalls: []llm.ToolCall{{
			ID:       id,
			Type:     "function",
			Function: llm.ToolCallFunction{Name: name, Arguments: "{}"},
		}},
		Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
	}
}

func textResp(content string) *llm.Response {
	return &llm.Response{
		Content: content,
		Usage:   &llm.Usage{PromptTokens: 10, CompletionTokens: 4, TotalTokens: 14},
	}
}

func passThroughExec(_ context.Context, call llm.ToolCall) (string, error) {
	return fmt.Sprintf("result for %s", call.Function.Name), nil
}

func TestRun_NoToolTermination(t *testing.T) {
	client := &scriptedClient{
		responses: []*llm.Response{textResp("just a text answer")},
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hi"},
		},
		passThroughExec,
		Config{MaxToolRounds: 5},
	)

	require.NoError(t, err)
	require.NotNil(t, res.Final)
	assert.Equal(t, "just a text answer", res.Final.Content)
	assert.Empty(t, res.Audit)
	assert.Equal(t, 0, res.Rounds, "no-tool terminator means zero tool rounds executed")
	assert.Equal(t, 1, client.calls)
	// Messages slice should equal the input (no assistant/tool messages
	// appended when there are no tool calls — the final response is in
	// res.Final, not in res.Messages).
	require.Len(t, res.Messages, 2)
}

func TestRun_ToolCallLoop(t *testing.T) {
	client := &scriptedClient{
		responses: []*llm.Response{
			toolCallResp("pg_count", "c1"),
			toolCallResp("pg_read_query", "c2"),
			textResp("done"),
		},
	}

	var execCalls []string
	exec := func(_ context.Context, call llm.ToolCall) (string, error) {
		execCalls = append(execCalls, call.Function.Name)
		return "{\"ok\":true}", nil
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "investigate"}},
		exec,
		Config{MaxToolRounds: 5},
	)

	require.NoError(t, err)
	assert.Equal(t, "done", res.Final.Content)
	assert.Equal(t, []string{"pg_count", "pg_read_query"}, execCalls)
	assert.Equal(t, 3, client.calls)
	require.Len(t, res.Audit, 2)
	assert.Equal(t, 0, res.Audit[0].Round)
	assert.Equal(t, "c1", res.Audit[0].Call.ID)
	assert.Equal(t, 1, res.Audit[1].Round)
	assert.Equal(t, "c2", res.Audit[1].Call.ID)

	// History should be: user, assistant(tool_call c1), tool(c1 result),
	// assistant(tool_call c2), tool(c2 result). The final text response
	// is NOT appended to res.Messages — it's in res.Final.
	require.Len(t, res.Messages, 5)
	assert.Equal(t, "user", res.Messages[0].Role)
	assert.Equal(t, "assistant", res.Messages[1].Role)
	require.Len(t, res.Messages[1].ToolCalls, 1)
	assert.Equal(t, "tool", res.Messages[2].Role)
	assert.Equal(t, "c1", res.Messages[2].ToolCallID)
}

func TestRun_MaxRoundsTermination(t *testing.T) {
	const maxRounds = 3
	client := &scriptedClient{
		responses: []*llm.Response{
			toolCallResp("t", "c1"),
			toolCallResp("t", "c2"),
			toolCallResp("t", "c3"),
			textResp("never reached"),
		},
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{MaxToolRounds: maxRounds},
	)

	require.NoError(t, err, "max rounds is a graceful termination, not an error")
	assert.Equal(t, maxRounds, res.Rounds)
	assert.Equal(t, maxRounds, client.calls, "must not call LLM beyond MaxToolRounds")
	require.NotNil(t, res.Final)
	assert.True(t, res.Final.HasToolCalls(),
		"final response on max-rounds termination should still be a tool-call request — caller decides whether to coerce a text reply")
	assert.Len(t, res.Audit, maxRounds)
}

func TestRun_CallbackErrorPropagation(t *testing.T) {
	client := &scriptedClient{
		responses: []*llm.Response{toolCallResp("bad_tool", "c1")},
	}

	wantErr := errors.New("disk on fire")
	exec := func(_ context.Context, _ llm.ToolCall) (string, error) {
		return "", wantErr
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		exec,
		Config{MaxToolRounds: 5},
	)

	require.Error(t, err)
	require.NotNil(t, res, "partial result must be returned so callers can bill accumulated usage")
	assert.ErrorIs(t, err, wantErr, "executor errors must be wrapped, not swallowed")
	assert.Contains(t, err.Error(), "bad_tool")
	// The failing round's LLM response accumulated usage before the executor
	// ran — that usage must appear in the partial result.
	assert.Greater(t, res.Usage.PromptTokens, 0, "round-0 prompt tokens must appear in partial result")
}

func TestRun_LLMErrorPropagation(t *testing.T) {
	wantErr := errors.New("model overloaded")
	client := &scriptedClient{
		responses: []*llm.Response{nil},
		errs:      []error{wantErr},
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{MaxToolRounds: 5},
	)

	require.Error(t, err)
	// A round-0 LLM failure still returns a partial result (with zero usage,
	// since the provider returned no response). The result is non-nil so all
	// error paths are uniform — callers don't need to nil-guard differently
	// for first-round vs. later-round failures.
	require.NotNil(t, res)
	assert.Equal(t, 0, res.Usage.PromptTokens, "no usage when round 0 fails before completion")
	assert.ErrorIs(t, err, wantErr)
	assert.Contains(t, err.Error(), "round 0")
}

// When a multi-round loop fails mid-way through, the partial result must
// carry usage from all successfully completed rounds so callers can bill it.
func TestRun_LLMErrorAfterSuccessfulRounds_PartialUsageBilled(t *testing.T) {
	round0Err := errors.New("model overloaded on round 1")
	client := &scriptedClient{
		responses: []*llm.Response{
			// Round 0 succeeds with a tool call and usage.
			{
				ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "t"}}},
				Usage:     &llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
			},
			// Round 1 fails at the LLM level.
			nil,
		},
		errs: []error{nil, round0Err},
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{MaxToolRounds: 5},
	)

	require.Error(t, err)
	assert.ErrorIs(t, err, round0Err)
	require.NotNil(t, res, "partial result must be returned")
	// Round 0's usage must be in the partial result even though round 1 failed.
	assert.Equal(t, 100, res.Usage.PromptTokens, "round-0 prompt tokens must be in partial result")
	assert.Equal(t, 20, res.Usage.CompletionTokens, "round-0 completion tokens must be in partial result")
}

// When exec fails mid-loop, the partial result must carry usage from the round
// in which the executor failed (the LLM completed successfully before exec ran).
func TestRun_ExecErrorPartialUsageBilled(t *testing.T) {
	execErr := errors.New("disk on fire — second tool")
	client := &scriptedClient{
		responses: []*llm.Response{
			// Round 0: two tool calls; exec will fail on the second.
			{
				ToolCalls: []llm.ToolCall{
					{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "ok_tool"}},
					{ID: "c2", Type: "function", Function: llm.ToolCallFunction{Name: "bad_tool"}},
				},
				Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 40, TotalTokens: 240},
			},
		},
	}

	callCount := 0
	exec := func(_ context.Context, _ llm.ToolCall) (string, error) {
		callCount++
		if callCount == 2 {
			return "", execErr
		}
		return "{}", nil
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		exec,
		Config{MaxToolRounds: 5},
	)

	require.Error(t, err)
	assert.ErrorIs(t, err, execErr)
	require.NotNil(t, res, "partial result must be returned")
	// Usage from the round where exec failed must be present (LLM completed).
	assert.Equal(t, 200, res.Usage.PromptTokens)
	assert.Equal(t, 40, res.Usage.CompletionTokens)
}

func TestRun_AggregatesUsage(t *testing.T) {
	client := &scriptedClient{
		responses: []*llm.Response{
			{
				ToolCalls: []llm.ToolCall{{ID: "c1", Type: "function", Function: llm.ToolCallFunction{Name: "t"}}},
				Usage: &llm.Usage{
					PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120,
					CacheCreationInputTokens: 500, CacheReadInputTokens: 0,
				},
			},
			{
				Content: "done",
				Usage: &llm.Usage{
					PromptTokens: 150, CompletionTokens: 30, TotalTokens: 180,
					CacheCreationInputTokens: 0, CacheReadInputTokens: 600,
				},
			},
		},
	}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{MaxToolRounds: 5},
	)

	require.NoError(t, err)
	assert.Equal(t, 250, res.Usage.PromptTokens)
	assert.Equal(t, 50, res.Usage.CompletionTokens)
	assert.Equal(t, 300, res.Usage.TotalTokens)
	assert.Equal(t, 500, res.Usage.CacheCreationInputTokens)
	assert.Equal(t, 600, res.Usage.CacheReadInputTokens)
}

func TestRun_DefaultsMaxRoundsWhenZero(t *testing.T) {
	// Construct a sequence longer than DefaultMaxToolRounds to confirm the
	// default actually caps the loop. The 10th response is text so we
	// terminate normally — we just want to assert the default kicked in
	// instead of running unbounded or capping at zero.
	responses := make([]*llm.Response, DefaultMaxToolRounds+1)
	for i := range DefaultMaxToolRounds {
		responses[i] = toolCallResp("t", fmt.Sprintf("c%d", i))
	}
	responses[DefaultMaxToolRounds] = textResp("never reached")

	client := &scriptedClient{responses: responses}

	res, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{MaxToolRounds: 0},
	)

	require.NoError(t, err)
	assert.Equal(t, DefaultMaxToolRounds, res.Rounds)
	assert.Equal(t, DefaultMaxToolRounds, client.calls)
}

// Run must NOT mutate the caller's input slice — defensive copy invariant.
// A future refactor that drops the copy would silently corrupt callers
// that reuse the slice across calls (e.g. a long-lived conversation).
func TestRun_DoesNotMutateInputMessages(t *testing.T) {
	input := []llm.Message{
		{Role: "system", Content: "rules"},
		{Role: "user", Content: "go"},
	}
	original := append([]llm.Message(nil), input...)

	client := &scriptedClient{
		responses: []*llm.Response{
			toolCallResp("t", "c1"),
			textResp("done"),
		},
	}

	_, err := Run(
		context.Background(),
		client,
		input,
		passThroughExec,
		Config{MaxToolRounds: 5},
	)
	require.NoError(t, err)

	assert.Equal(t, original, input, "caller's input slice must not be mutated")
}

// A pre-cancelled context must short-circuit before the first LLM call.
// Pin this so the cancellation guard at the top of each round can't
// regress to "only check after the first round."
func TestRun_RespectsCancelledContext(t *testing.T) {
	client := &scriptedClient{responses: []*llm.Response{textResp("never reached")}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := Run(
		ctx,
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{MaxToolRounds: 5},
	)

	require.Error(t, err)
	assert.Nil(t, res)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, client.calls, "must not call the LLM after the context is cancelled")
}

// The Tools and Temperature config fields must reach the wire request
// untouched — pin this so a refactor doesn't accidentally drop them.
func TestRun_PassesToolsAndTemperature(t *testing.T) {
	client := &scriptedClient{responses: []*llm.Response{textResp("ok")}}

	temp := 0.2
	tools := []llm.Tool{{Type: "function", Function: llm.ToolFunction{Name: "t1"}}}

	_, err := Run(
		context.Background(),
		client,
		[]llm.Message{{Role: "user", Content: "go"}},
		passThroughExec,
		Config{Tools: tools, MaxToolRounds: 5, MaxTokens: 1234, Temperature: &temp},
	)
	require.NoError(t, err)

	require.Len(t, client.requests, 1)
	got := client.requests[0]
	assert.Equal(t, tools, got.Tools)
	assert.Equal(t, 1234, got.MaxTokens)
	require.NotNil(t, got.Temperature)
	assert.InDelta(t, temp, *got.Temperature, 0)
}
