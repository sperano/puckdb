package maurice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
	"github.com/sperano/puckdb/internal/mcp"
)

// turnRecorder wraps the LLM client for one turn. It records every provider
// call — kind, round, timing, usage, the exact request as references to
// stored and turn messages — and every tool call, so FinishTurn can commit
// them with the turn, including a turn that failed. agentloop calls it
// sequentially, so it needs no locking.
type turnRecorder struct {
	client   llm.Client
	provider string
	model    string
	now      func() time.Time

	// historyIDs holds the stored ID of each request message before the
	// prompt ("" for the system prompt); promptIndex is the prompt's position
	// in every request of the turn.
	historyIDs  []string
	promptIndex int
	// messages are the turn's messages in request order; [0] is the prompt.
	messages []TurnMessage
	calls    []LLMCallRecord
	// nextKind is the kind of the next call; toolsRun counts the tool calls
	// of the latest call that have run.
	nextKind CallKind
	toolsRun int
}

var _ llm.Client = (*turnRecorder)(nil)

func newTurnRecorder(client llm.Client, provider, model, prompt string, now func() time.Time) *turnRecorder {
	return &turnRecorder{
		client:   client,
		provider: provider,
		model:    model,
		now:      now,
		messages: []TurnMessage{{Role: "user", Content: prompt}},
		nextKind: CallChatRound,
	}
}

func (r *turnRecorder) prompt() string { return r.messages[0].Content }

// setHistory takes the IDs aligned with the request messages loadHistory
// built; the last entry is the unsaved prompt.
func (r *turnRecorder) setHistory(ids []string) {
	r.historyIDs = ids
	r.promptIndex = len(ids) - 1
}

func (r *turnRecorder) nextCallIsForcedFinal() { r.nextKind = CallForcedFinal }

// Complete forwards the request and records it. A provider error is returned
// with its class attached and unchanged text.
func (r *turnRecorder) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	round := len(r.calls)
	call := newCallRecord(r.nextKind, &round, r.provider, r.model)
	r.captureRequest(&call, req)
	call.StartedAt = r.now()
	resp, err := r.client.Complete(ctx, req)
	call.CompletedAt = r.now()
	completeCallRecord(&call, resp, err)
	r.calls = append(r.calls, call)
	r.toolsRun = 0
	if err != nil {
		return nil, classify(ErrorClassProvider, err)
	}
	return resp, nil
}

// captureRequest stores the system prompt, the tool definitions and a
// reference for every other request message, adding messages the turn
// produced since the previous call.
func (r *turnRecorder) captureRequest(call *LLMCallRecord, req *llm.Request) {
	call.ToolDefinitions = req.Tools
	for i, m := range req.Messages {
		if i == 0 && m.Role == "system" {
			prompt := m.Content
			call.SystemPrompt = &prompt
			continue
		}
		if i < r.promptIndex {
			call.Inputs = append(call.Inputs, MessageRef{MessageID: r.historyIDs[i]})
			continue
		}
		turnIndex := i - r.promptIndex
		if turnIndex == len(r.messages) {
			r.messages = append(r.messages, TurnMessage{
				Role: m.Role, Content: m.Content, ToolCalls: m.ToolCalls, ToolCallID: m.ToolCallID,
			})
		}
		call.Inputs = append(call.Inputs, MessageRef{TurnIndex: turnIndex})
	}
}

// recordTool fills the next requested tool call of the latest LLM call.
// agentloop runs tool calls in the order the response listed them.
func (r *turnRecorder) recordTool(result string, status ToolCallStatus, started, completed time.Time) {
	if len(r.calls) == 0 {
		return
	}
	call := &r.calls[len(r.calls)-1]
	if r.toolsRun >= len(call.ToolCalls) {
		return
	}
	tc := &call.ToolCalls[r.toolsRun]
	tc.Result, tc.Status, tc.StartedAt, tc.CompletedAt = &result, status, &started, &completed
	r.toolsRun++
}

// turnRecord assembles what FinishTurn commits. A succeeded turn adds its
// final answer; a failed one keeps the messages its provider calls were sent.
func (r *turnRecorder) turnRecord(start *TurnStart, answer *turnAnswer, runErr error) TurnRecord {
	record := TurnRecord{
		TurnID:         start.TurnID,
		ConversationID: start.ConversationID,
		Status:         TurnSucceeded,
		Messages:       r.messages,
		Calls:          r.calls,
	}
	if runErr != nil {
		record.ErrorClass = errorClass(runErr)
		record.Status = failedStatus(record.ErrorClass)
		return record
	}
	record.Warnings = answer.warnings
	record.Messages = append(record.Messages, TurnMessage{Role: "assistant", Content: answer.content})
	return record
}

func newCallRecord(kind CallKind, round *int, provider, model string) LLMCallRecord {
	return LLMCallRecord{Kind: kind, Round: round, Provider: provider, Model: model}
}

// completeCallRecord records a call's outcome. Tool calls the response
// requested start as cancelled until recordTool fills them.
func completeCallRecord(call *LLMCallRecord, resp *llm.Response, err error) {
	if err != nil {
		call.Status = CallFailed
		call.ErrorClass = callErrorClass(err)
		return
	}
	call.Status = CallSucceeded
	if resp.Model != "" {
		call.Model = resp.Model
	}
	call.ProviderRequestID = resp.ID
	call.FinishReason = resp.FinishReason
	call.Usage = tokenUsage(resp.Usage)
	for _, tc := range resp.ToolCalls {
		call.ToolCalls = append(call.ToolCalls, ToolCallRecord{
			ProviderToolCallID: tc.ID,
			Name:               tc.Function.Name,
			ArgumentsRaw:       tc.Function.Arguments,
			Status:             ToolCancelled,
		})
	}
}

func callErrorClass(err error) ErrorClass {
	if class := errorClass(err); class != ErrorClassInternal {
		return class
	}
	return ErrorClassProvider
}

// tokenUsage keeps "not reported" (nil) apart from zero. Cache counts are
// set only when the provider reports cache accounting.
func tokenUsage(u *llm.Usage) TokenUsage {
	if u == nil {
		return TokenUsage{}
	}
	usage := TokenUsage{Input: int64Ptr(u.PromptTokens), Output: int64Ptr(u.CompletionTokens)}
	if u.CacheReported {
		usage.CacheCreation = int64Ptr(u.CacheCreationInputTokens)
		usage.CacheRead = int64Ptr(u.CacheReadInputTokens)
	}
	return usage
}

func int64Ptr(v int) *int64 {
	n := int64(v)
	return &n
}

// mcpToolExecutor returns an agentloop.ToolExecutor that resolves each tool call
// against the MCP client and records it. A tool-call error is formatted into
// the returned result string with a nil error, so the failure is surfaced to
// the model on the next round rather than aborting the turn.
func (s *service) mcpToolExecutor(convID string, rec *turnRecorder, caller mcp.ToolCaller) agentloop.ToolExecutor {
	return func(ctx context.Context, tc llm.ToolCall) (string, error) {
		log.Debug().Str("conversation", convID).Str("tool", tc.Function.Name).Str("call_id", tc.ID).Msg("calling MCP tool")
		log.Trace().Str("conversation", convID).Str("tool", tc.Function.Name).Str("arguments", tc.Function.Arguments).Msg("MCP tool arguments")

		started := s.now()
		result, err := caller.CallTool(ctx, tc.Function.Name, json.RawMessage(tc.Function.Arguments))
		completed := s.now()
		if err != nil {
			resultContent := fmt.Sprintf("Error calling tool %s: %s", tc.Function.Name, err.Error())
			log.Warn().Err(err).Str("tool", tc.Function.Name).Msg("tool call failed")
			rec.recordTool(resultContent, toolStatus(tc.Function.Arguments, true), started, completed)
			return resultContent, nil
		}
		log.Debug().Str("conversation", convID).Str("tool", tc.Function.Name).Bool("is_error", result.IsError).Int("result_len", len(result.Content)).Msg("MCP tool returned")
		log.Trace().Str("conversation", convID).Str("tool", tc.Function.Name).Str("result", truncateLog(result.Content, maxLogTraceLen)).Msg("MCP tool result content")
		rec.recordTool(result.Content, toolStatus(tc.Function.Arguments, result.IsError), started, completed)
		return result.Content, nil
	}
}

// toolStatus classifies a tool call that ran. Arguments that are not JSON
// are a parse error whatever the tool did with them; empty arguments mean a
// call without arguments.
func toolStatus(arguments string, failed bool) ToolCallStatus {
	switch {
	case arguments != "" && !json.Valid([]byte(arguments)):
		return ToolParseError
	case failed:
		return ToolError
	default:
		return ToolSucceeded
	}
}
