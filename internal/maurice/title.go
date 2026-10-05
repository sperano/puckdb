package maurice

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/sperano/puckdb/internal/llm"
)

const (
	titleGenerationHint = "Summarize this conversation in 5 words or fewer. Reply with only the title, no quotes."
	// titleMaxTokens caps the title completion; five words fit easily.
	titleMaxTokens = 20
	// titleGenTimeout caps how long a detached title generation may run before
	// its context is cancelled.
	titleGenTimeout = 30 * time.Second
)

// titleJob is the first exchange of a new conversation, with the stored IDs
// of its two messages so the title call's exact request can be recorded.
type titleJob struct {
	userID         string
	conversationID string
	userMessageID  string
	answerID       string
	userMessage    string
	answer         string
}

// startTitleGeneration launches a bounded, detached title generation. It
// derives the context from context.WithoutCancel(ctx) so the title survives the
// request returning, but bounds it with titleGenTimeout and ties it to
// shutdownCtx so Close can abandon it. Concurrency is capped by the errgroup's
// limit; if the cap is reached the (best-effort) title is skipped.
func (s *service) startTitleGeneration(ctx context.Context, job titleJob) {
	titleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), titleGenTimeout)
	started := s.titleGroup.TryGo(func() error {
		stop := context.AfterFunc(s.shutdownCtx, cancel)
		defer stop()
		defer cancel()
		s.generateTitle(titleCtx, job)
		return nil
	})
	if !started {
		cancel()
		log.Debug().Str("conversation", job.conversationID).Msg("title generation skipped: concurrency limit reached")
	}
}

// generateTitle asks the LLM for a short title and records the call, failed
// or not: its tokens count toward the user's usage.
func (s *service) generateTitle(ctx context.Context, job titleJob) {
	instruction := titleGenerationHint
	req := &llm.Request{
		Messages: []llm.Message{
			{Role: "user", Content: job.userMessage},
			{Role: "assistant", Content: job.answer},
			{Role: "user", Content: instruction},
		},
		MaxTokens: titleMaxTokens,
	}
	call := newCallRecord(CallTitleGeneration, nil, s.provider, s.model)
	call.Instruction = &instruction
	call.Inputs = []MessageRef{{MessageID: job.userMessageID}, {MessageID: job.answerID}}
	call.StartedAt = s.now()
	resp, err := s.llmClient.Complete(ctx, req)
	call.CompletedAt = s.now()
	completeCallRecord(&call, resp, err)

	record := TitleRecord{UserID: job.userID, ConversationID: job.conversationID, Call: call}
	if err != nil {
		log.Warn().Err(err).Msg("failed to generate conversation title")
	} else {
		record.Title = resp.Content
	}

	// Recording outlives a cancelled generation so the usage is kept.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.persistTimeout)
	defer cancel()
	if err := s.db.RecordTitle(persistCtx, record); err != nil {
		log.Warn().Err(err).Msg("failed to record conversation title")
	}
}
