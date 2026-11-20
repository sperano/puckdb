package maurice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
)

// maxFinalAnswerAttempts is how many completions a turn's final answer gets
// in total: the first final response (the loop's terminating response or the
// forced final answer) and then retries without tools (nudgedFinalAnswer)
// while the answer has no text.
const maxFinalAnswerAttempts = 3

// emptyAnswerNudge is the trailing instruction of each retry a turn gets
// when its final response has no text (nudgedFinalAnswer).
const emptyAnswerNudge = "Your last response contained no text. Using the information gathered above, " +
	"write your answer to my last question now, as text for me to read, in the language I asked it in. " +
	"Do not call tools and do not leave the response empty."

// ErrEmptyAnswer fails a turn whose final response had no text in every one
// of its maxFinalAnswerAttempts attempts.
var ErrEmptyAnswer = errors.New("LLM returned no answer text")

// finalAnswer returns the turn's answer. A terminating response with no tool
// calls is the final answer; a max-rounds termination, where res.Final still
// requests tools, forces one more completion (forcedFinalAnswer). While the
// answer has no text, completions without tools ask for it
// (nudgedFinalAnswer), up to maxFinalAnswerAttempts attempts in all; the tool
// chain never runs again. When every attempt is empty the turn fails as
// ErrorClassEmptyAnswer instead of succeeding with no content.
func (s *service) finalAnswer(ctx context.Context, convID string, res *agentloop.Result, rec *turnRecorder) (string, error) {
	resp := res.Final
	if resp == nil || resp.HasToolCalls() {
		forced, err := s.forcedFinalAnswer(ctx, convID, res.Messages, rec)
		if err != nil {
			return "", err
		}
		resp = forced
	}
	for attempt := 1; ; attempt++ {
		if hasAnswer(convID, attempt, resp) {
			return resp.Content, nil
		}
		if attempt == maxFinalAnswerAttempts {
			return "", classify(ErrorClassEmptyAnswer, ErrEmptyAnswer)
		}
		nudged, err := s.nudgedFinalAnswer(ctx, res.Messages, rec)
		if err != nil {
			return "", err
		}
		resp = nudged
	}
}

// hasAnswer reports whether resp, the final answer's attempt-th attempt,
// carries answer text, logging why not, with the stop reason and the
// attempt, when it does not, and warning of an answer cut off at MaxTokens.
func hasAnswer(convID string, attempt int, resp *llm.Response) bool {
	if strings.TrimSpace(resp.Content) == "" {
		evt := log.Warn().Str("conversation", convID).Str("finish_reason", resp.FinishReason).
			Int("tool_calls", len(resp.ToolCalls)).Int("attempt", attempt).
			Int("max_attempts", maxFinalAnswerAttempts)
		if resp.Usage != nil {
			evt = evt.Int("completion_tokens", resp.Usage.CompletionTokens)
		}
		evt.Msg("LLM final response has no text")
		return false
	}
	if resp.Truncated() {
		log.Warn().Str("conversation", convID).Str("finish_reason", resp.FinishReason).
			Msg("LLM final response cut off at max tokens")
	}
	log.Debug().Str("conversation", convID).Str("content", truncateLog(resp.Content, maxLogMessageLen)).Msg("LLM final response")
	return true
}

// forcedFinalAnswer asks for a text answer without tools after the tool
// rounds ran out. messages is the loop's whole request history, so the call
// records the last round's tool calls and results as turn messages before the
// answer, and is recorded as CallForcedFinal.
func (s *service) forcedFinalAnswer(ctx context.Context, convID string, messages []llm.Message, rec *turnRecorder) (*llm.Response, error) {
	log.Warn().Str("conversation", convID).Int("max_rounds", s.maxToolRounds).Msg("tool rounds exhausted, forcing final response without tools")
	rec.nextCallIsForcedFinal()
	resp, err := rec.Complete(ctx, &llm.Request{Messages: messages, MaxTokens: s.maxTokens})
	if err != nil {
		return nil, fmt.Errorf("LLM forced final completion: %w", err)
	}
	return resp, nil
}

// nudgedFinalAnswer retries an empty final answer, without tools,
// sending the loop's request history followed by emptyAnswerNudge. The empty
// response itself is left out: a provider may reject an assistant message
// without content. The call is recorded as CallForcedFinal with the nudge as
// its instruction, so the nudge never becomes a turn message.
func (s *service) nudgedFinalAnswer(ctx context.Context, messages []llm.Message, rec *turnRecorder) (*llm.Response, error) {
	rec.nextCallIsForcedFinal()
	resp, err := rec.completeWithInstruction(ctx, messages, emptyAnswerNudge, s.maxTokens)
	if err != nil {
		return nil, fmt.Errorf("LLM empty answer retry: %w", err)
	}
	return resp, nil
}
