package maurice

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
)

// ErrServiceClosed is returned by Chat and StartChat once Close has begun.
var ErrServiceClosed = errors.New("maurice service is closed")

// TurnRef names a turn StartChat started or found under its idempotency key.
type TurnRef struct {
	TurnID         string
	ConversationID string
}

// turnResult is what a detached turn hands to the Chat waiting on it.
type turnResult struct {
	resp *ChatResponse
	err  error
}

// turnRunner tracks the detached turns so Close can wait for them, and
// refuses new ones once closed. The lock orders every wg.Go before the
// closing Wait.
type turnRunner struct {
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

// goTurn runs fn on its own goroutine; it reports false, running nothing,
// once the runner is closed.
func (r *turnRunner) goTurn(fn func()) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	r.wg.Go(fn)
	return true
}

func (r *turnRunner) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *turnRunner) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

// StartChat opens a turn like Chat but returns as soon as it runs; GetTurn
// reports its progress. A key already used returns that turn, running or
// finished, without running the prompt again.
func (s *service) StartChat(ctx context.Context, req ChatRequest) (*TurnRef, error) {
	start, err := s.beginTurn(ctx, req, true)
	if err != nil {
		return nil, err
	}
	ref := &TurnRef{TurnID: start.TurnID, ConversationID: start.ConversationID}
	if start.Replay != nil {
		return ref, nil
	}
	if _, err := s.launchTurn(ctx, req, start); err != nil {
		return nil, err
	}
	return ref, nil
}

// GetTurn returns one of userID's turns, or ErrTurnNotFound.
func (s *service) GetTurn(ctx context.Context, userID, turnID string) (*Turn, error) {
	return s.db.GetTurn(ctx, userID, turnID, staleTurnAfter)
}

// beginTurn opens the turn of req, or finds the one its idempotency key
// names. replayRunning returns a running turn under that key instead of
// ErrTurnInProgress (BeginTurnParams.ReplayRunning).
func (s *service) beginTurn(ctx context.Context, req ChatRequest, replayRunning bool) (*TurnStart, error) {
	if s.runner.isClosed() {
		return nil, ErrServiceClosed
	}
	params, err := s.beginTurnParams(req)
	if err != nil {
		return nil, err
	}
	params.ReplayRunning = replayRunning
	start, err := s.db.BeginTurn(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("begin turn: %w", err)
	}
	return start, nil
}

// launchTurn runs a begun turn detached from the request: the turn keeps
// ctx's values but not its cancellation, so a client that disconnects does
// not lose it. turnTimeout bounds it and Close cancels it; either way it
// commits as runTurn does. The returned channel receives the turn's result
// once. If Close has begun, nothing runs and the turn is committed as
// cancelled.
func (s *service) launchTurn(ctx context.Context, req ChatRequest, start *TurnStart) (<-chan turnResult, error) {
	done := make(chan turnResult, 1)
	turnCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), turnTimeout)
	started := s.runner.goTurn(func() {
		defer cancel()
		stop := context.AfterFunc(s.shutdownCtx, cancel)
		defer stop()
		resp, err := s.runTurn(turnCtx, req, start)
		if err != nil {
			log.Warn().Err(err).Str("turn", start.TurnID).Str("conversation", start.ConversationID).
				Msg("maurice turn failed")
		}
		done <- turnResult{resp: resp, err: err}
	})
	if !started {
		cancel()
		s.releaseTurn(ctx, TurnRecord{
			TurnID: start.TurnID, ConversationID: start.ConversationID,
			Status: TurnCancelled, ErrorClass: ErrorClassCancelled,
		})
		return nil, ErrServiceClosed
	}
	return done, nil
}
