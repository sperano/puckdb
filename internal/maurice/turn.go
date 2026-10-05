package maurice

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/llm/agentloop"
)

const (
	// turnTimeout bounds one turn's LLM/tool loop, including a forced final
	// completion.
	turnTimeout = 10 * time.Minute
	// turnPersistTimeout bounds committing a finished turn. It runs detached
	// from the request so a client that disconnects still releases its turn.
	turnPersistTimeout = 30 * time.Second
	// turnReleaseTimeout bounds the status-only release after a failed
	// commit. It gets its own deadline: a commit that failed by timing out has
	// used up turnPersistTimeout.
	turnReleaseTimeout = 5 * time.Second
	// staleTurnGrace is extra slack before a running turn counts as abandoned.
	staleTurnGrace = time.Minute
	// staleTurnAfter is how long a turn may stay running before a new request
	// fails it as abandoned. It outlasts any live turn (loop plus commit), so
	// only a turn whose process died is reconciled.
	staleTurnAfter = turnTimeout + turnPersistTimeout + turnReleaseTimeout + staleTurnGrace
	// maxIdempotencyKeyLen matches the length CHECK on maurice_turns.
	maxIdempotencyKeyLen = 200
)

var (
	// ErrInvalidIdempotencyKey is returned for a key longer than
	// maxIdempotencyKeyLen.
	ErrInvalidIdempotencyKey = fmt.Errorf("idempotency key must be at most %d characters", maxIdempotencyKeyLen)
	// ErrTurnNotCompleted is returned when an idempotency key names a turn
	// that failed or was cancelled; a retry needs a new key.
	ErrTurnNotCompleted = errors.New("turn did not complete")
)

func (s *service) beginTurnParams(req ChatRequest) (BeginTurnParams, error) {
	key := req.IdempotencyKey
	if key == "" {
		key = uuid.NewString()
	}
	if len(key) > maxIdempotencyKeyLen {
		return BeginTurnParams{}, ErrInvalidIdempotencyKey
	}
	hash := sha256.Sum256([]byte(req.Message))
	params := BeginTurnParams{
		UserID:         req.UserID,
		IdempotencyKey: key,
		RequestHash:    hash[:],
		StaleAfter:     staleTurnAfter,
	}
	if req.ConversationID != nil {
		params.ConversationID = *req.ConversationID
	}
	return params, nil
}

func replayResponse(start *TurnStart) (*ChatResponse, error) {
	r := start.Replay
	if r.Status != TurnSucceeded {
		return nil, fmt.Errorf("%w: turn %s %s (%s); send the prompt again with a new idempotency key",
			ErrTurnNotCompleted, start.TurnID, r.Status, r.ErrorClass)
	}
	return &ChatResponse{
		ConversationID: start.ConversationID,
		MessageID:      r.MessageID,
		Content:        r.Content,
		ToolsUsed:      r.ToolsUsed,
	}, nil
}

// turnAnswer is what a successful tool loop produced.
type turnAnswer struct {
	content   string
	toolsUsed []string
}

func (s *service) runTurn(ctx context.Context, req ChatRequest, start *TurnStart) (*ChatResponse, error) {
	log.Debug().Str("conversation", start.ConversationID).Bool("new", start.NewConversation).
		Str("message", truncateLog(req.Message, maxLogMessageLen)).Msg("maurice chat started")

	turnCtx, cancel := context.WithTimeout(ctx, turnTimeout)
	defer cancel()
	rec := newTurnRecorder(s.llmClient, s.provider, s.model, req.Message, s.now)
	answer, runErr := s.executeTurn(turnCtx, req.UserID, start.ConversationID, rec)

	record := rec.turnRecord(start, answer, runErr)
	ids, persistErr := s.finishTurn(ctx, record)
	if runErr != nil {
		// runErr already carries the "LLM completion (round N)" / "tool
		// executor" context from agentloop.
		return nil, runErr
	}
	if persistErr != nil {
		return nil, persistErr
	}

	userMessageID, answerID := ids[0], ids[len(ids)-1]
	if start.NewConversation && answer.content != "" {
		s.startTitleGeneration(ctx, titleJob{
			userID: req.UserID, conversationID: start.ConversationID,
			userMessageID: userMessageID, answerID: answerID,
			userMessage: req.Message, answer: answer.content,
		})
	}
	log.Debug().Str("conversation", start.ConversationID).Int("tools_used", len(answer.toolsUsed)).
		Strs("tools", answer.toolsUsed).Msg("maurice chat completed")
	return &ChatResponse{
		ConversationID: start.ConversationID,
		MessageID:      answerID,
		Content:        answer.content,
		ToolsUsed:      answer.toolsUsed,
	}, nil
}

// executeTurn loads history, runs the tool loop through the recorder, and
// resolves the final answer.
func (s *service) executeTurn(ctx context.Context, userID, convID string, rec *turnRecorder) (*turnAnswer, error) {
	history, historyIDs, err := s.loadHistory(ctx, userID, convID, rec.prompt())
	if err != nil {
		return nil, classify(ErrorClassInternal, fmt.Errorf("load history: %w", err))
	}
	rec.setHistory(historyIDs)
	log.Debug().Str("conversation", convID).Int("history_messages", len(history)).Msg("loaded conversation history")

	llmTools, err := s.toolCache.GetLLMTools(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to load MCP tools, continuing without tools")
		llmTools = nil
	} else {
		log.Debug().Int("tools", len(llmTools)).Msg("loaded MCP tools for LLM")
	}

	res, err := agentloop.Run(ctx, rec, history, s.mcpToolExecutor(convID, rec), agentloop.Config{
		Tools:         llmTools,
		MaxToolRounds: s.maxToolRounds,
		MaxTokens:     s.maxTokens,
	})
	if err != nil {
		return nil, err
	}
	if res.Final != nil {
		logLLMResponse(convID, res.Rounds, res.Final)
	}
	answer := &turnAnswer{}
	for _, a := range res.Audit {
		answer.toolsUsed = append(answer.toolsUsed, a.Call.Function.Name)
	}
	if answer.content, err = s.finalAnswer(ctx, convID, res, rec); err != nil {
		return nil, err
	}
	return answer, nil
}

// finalAnswer returns the turn's answer. A terminating response with no tool
// calls IS the final answer, even when its content is empty. Only a genuine
// max-rounds termination, where res.Final still requests tools, triggers one
// more completion without tools to force a text answer.
func (s *service) finalAnswer(ctx context.Context, convID string, res *agentloop.Result, rec *turnRecorder) (string, error) {
	if res.Final != nil && !res.Final.HasToolCalls() {
		log.Debug().Str("conversation", convID).Str("content", truncateLog(res.Final.Content, maxLogMessageLen)).Msg("LLM final response (no tool calls)")
		return res.Final.Content, nil
	}
	log.Warn().Str("conversation", convID).Int("max_rounds", s.maxToolRounds).Msg("tool rounds exhausted, forcing final response without tools")
	rec.nextCallIsForcedFinal()
	resp, err := rec.Complete(ctx, &llm.Request{Messages: res.Messages, MaxTokens: s.maxTokens})
	if err != nil {
		return "", fmt.Errorf("LLM forced final completion: %w", err)
	}
	return resp.Content, nil
}

// finishTurn commits the turn detached from the request context, so a client
// that disconnects still releases its turn. If the commit fails it falls back
// to a status-only failure so the conversation is not blocked until the turn
// goes stale; the turn's usage is then lost and logged as such.
func (s *service) finishTurn(ctx context.Context, record TurnRecord) ([]string, error) {
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.persistTimeout)
	defer cancel()
	ids, err := s.db.FinishTurn(persistCtx, record)
	if err == nil {
		return ids, nil
	}
	log.Error().Err(err).Str("turn", record.TurnID).Int("llm_calls", len(record.Calls)).
		Msg("persist turn failed; its messages and usage are lost")
	if !errors.Is(err, ErrTurnNotRunning) {
		s.releaseTurn(ctx, record)
	}
	return nil, fmt.Errorf("persist turn: %w", err)
}

// releaseTurn fails the turn without its messages or usage, under its own
// deadline.
func (s *service) releaseTurn(ctx context.Context, record TurnRecord) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.releaseTimeout)
	defer cancel()
	fallback := TurnRecord{
		TurnID: record.TurnID, ConversationID: record.ConversationID,
		Status: TurnFailed, ErrorClass: ErrorClassInternal,
	}
	if _, err := s.db.FinishTurn(releaseCtx, fallback); err != nil {
		log.Error().Err(err).Str("turn", record.TurnID).Msg("release failed turn")
	}
}

// classifiedError attaches an ErrorClass to an error without changing its
// message.
type classifiedError struct {
	class ErrorClass
	err   error
}

func (e *classifiedError) Error() string { return e.err.Error() }
func (e *classifiedError) Unwrap() error { return e.err }

func classify(class ErrorClass, err error) error {
	return &classifiedError{class: class, err: err}
}

// errorClass maps an error to its bounded class: context errors first, then
// a class attached where the error arose, else internal_error.
func errorClass(err error) ErrorClass {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return ErrorClassTimeout
	case errors.Is(err, context.Canceled):
		return ErrorClassCancelled
	}
	var ce *classifiedError
	if errors.As(err, &ce) {
		return ce.class
	}
	return ErrorClassInternal
}

// failedStatus is the terminal status of a turn that ended with class.
func failedStatus(class ErrorClass) TurnStatus {
	if class == ErrorClassCancelled {
		return TurnCancelled
	}
	return TurnFailed
}

// Logging helpers

const (
	maxLogMessageLen = 200  // truncation limit for Debug-level message content
	maxLogTraceLen   = 2000 // truncation limit for Trace-level full payloads
)

func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func logLLMResponse(convID string, round int, resp *llm.Response) {
	evt := log.Debug().Str("conversation", convID).Int("round", round)
	if resp.Usage != nil {
		evt = evt.Int("prompt_tokens", resp.Usage.PromptTokens).
			Int("completion_tokens", resp.Usage.CompletionTokens).
			Int("total_tokens", resp.Usage.TotalTokens)
	}
	if resp.FinishReason != "" {
		evt = evt.Str("finish_reason", resp.FinishReason)
	}
	evt.Str("model", resp.Model).Msg("LLM response received")
}
