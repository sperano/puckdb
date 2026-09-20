// Package agentloop runs an LLM tool-use conversation loop until the model
// produces a final text reply or a configured round limit is hit.
//
// The package is deliberately DB-, MCP-, and persistence-agnostic: the
// caller supplies a ToolExecutor callback that resolves each tool call
// however it wants (call MCP, query Postgres, run a workflow activity),
// and the caller persists the audit trail / message list returned in
// Result. This is the same loop that lives inside maurice.service.Chat;
// extracting it lets the simulation package consume it on day one and
// lets Maurice migrate to it as a separate follow-up without changing
// behavior.
package agentloop

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/internal/llm"
)

// DefaultMaxToolRounds is the loop cap when Config.MaxToolRounds is zero or
// negative. It matches maurice's historical default so a Maurice migration
// is a no-op behaviorally.
const DefaultMaxToolRounds = 10

// ToolExecutor resolves a single tool call.
//
// Returning a non-nil error aborts the loop and Run wraps and returns it.
// To surface a recoverable tool failure to the LLM (so it can react to the
// error in the next round), format the failure into resultJSON and return
// nil — the same pattern maurice uses today.
type ToolExecutor func(ctx context.Context, call llm.ToolCall) (resultJSON string, err error)

// Audit records one tool call and its serialized result, in execution order.
type Audit struct {
	Round  int
	Call   llm.ToolCall
	Result string
}

// AggregateUsage sums per-round Usage across the whole loop. Cache fields
// stay zero for providers that don't report cache accounting (OpenAI,
// Ollama).
type AggregateUsage struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	CacheCreationInputTokens int
	CacheReadInputTokens     int
}

// Result captures everything the loop produced.
//
// Final is the last *llm.Response observed — either the no-tool-call
// terminating response, or the response from the last round if MaxToolRounds
// was hit while the model was still requesting tools.
//
// Messages is the full conversation slice, including the caller's input plus
// every assistant message and tool-result message appended during the loop.
// Callers that persist messages real-time can diff against the input length;
// callers that persist post-loop can iterate the suffix.
type Result struct {
	Final    *llm.Response
	Messages []llm.Message
	Audit    []Audit
	Usage    AggregateUsage
	Rounds   int
}

// Config controls a single Run.
type Config struct {
	Tools         []llm.Tool
	MaxToolRounds int
	MaxTokens     int
	Temperature   *float64
}

// Run drives the tool-use loop. The caller supplies the initial messages
// (including the system prompt and any prior conversation history). Run
// appends its own messages and returns them in Result.Messages.
//
// Termination conditions, in priority order:
//  1. The model returns a response with no tool calls — return success.
//  2. exec returns a non-nil error — abort and return the wrapped error.
//  3. client.Complete returns an error — abort and return the wrapped error.
//  4. The loop has executed Config.MaxToolRounds rounds — return success
//     with the final tool-requesting response in Result.Final and the full
//     audit trail. The caller can decide whether to coerce a final text
//     reply (e.g. by sending a follow-up Complete with no tools).
func Run(ctx context.Context, client llm.Client, messages []llm.Message, exec ToolExecutor, cfg Config) (*Result, error) {
	if cfg.MaxToolRounds <= 0 {
		cfg.MaxToolRounds = DefaultMaxToolRounds
	}

	// Defensive copy — the caller's slice should not be mutated.
	history := append([]llm.Message(nil), messages...)
	var audit []Audit
	var usage AggregateUsage
	var lastResp *llm.Response

	for round := 0; round < cfg.MaxToolRounds; round++ {
		// Honor cancellation between rounds. client.Complete and exec are
		// expected to honor ctx themselves, but a misbehaving executor that
		// ignores ctx would otherwise let the loop run unbounded once the
		// caller cancels.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req := &llm.Request{
			Messages:    history,
			Tools:       cfg.Tools,
			MaxTokens:   cfg.MaxTokens,
			Temperature: cfg.Temperature,
		}

		resp, err := client.Complete(ctx, req)
		if err != nil {
			// Return partial result so callers can bill accumulated usage from
			// prior rounds even when this round's completion fails. Usage from
			// the failing round itself is not included because the provider
			// did not return a response.
			return &Result{
				Final:    lastResp,
				Messages: history,
				Audit:    audit,
				Usage:    usage,
				Rounds:   round,
			}, fmt.Errorf("LLM completion (round %d): %w", round, err)
		}
		lastResp = resp
		accumulateUsage(&usage, resp.Usage)

		if !resp.HasToolCalls() {
			return &Result{
				Final:    resp,
				Messages: history,
				Audit:    audit,
				Usage:    usage,
				Rounds:   round,
			}, nil
		}

		history = append(history, llm.Message{
			Role:      "assistant",
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, tc := range resp.ToolCalls {
			result, err := exec(ctx, tc)
			if err != nil {
				// Return partial result so callers can bill accumulated usage
				// from all rounds including the current one (already accumulated
				// above before entering the tool loop).
				return &Result{
					Final:    lastResp,
					Messages: history,
					Audit:    audit,
					Usage:    usage,
					Rounds:   round,
				}, fmt.Errorf("tool executor %q (round %d): %w", tc.Function.Name, round, err)
			}
			audit = append(audit, Audit{Round: round, Call: tc, Result: result})
			history = append(history, llm.Message{
				Role:       "tool",
				Content:    result,
				ToolCallID: tc.ID,
			})
		}
	}

	return &Result{
		Final:    lastResp,
		Messages: history,
		Audit:    audit,
		Usage:    usage,
		Rounds:   cfg.MaxToolRounds,
	}, nil
}

func accumulateUsage(agg *AggregateUsage, u *llm.Usage) {
	if u == nil {
		return
	}
	agg.PromptTokens += u.PromptTokens
	agg.CompletionTokens += u.CompletionTokens
	agg.TotalTokens += u.TotalTokens
	agg.CacheCreationInputTokens += u.CacheCreationInputTokens
	agg.CacheReadInputTokens += u.CacheReadInputTokens
}
