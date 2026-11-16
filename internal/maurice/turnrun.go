package maurice

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/rs/zerolog/log"
)

const (
	// maxRunningTurns bounds the detached turns one process runs at once. A
	// turn no longer ends when its client disconnects, and one running turn
	// per conversation alone would let a user start any number of LLM tool
	// loops by opening new conversations.
	maxRunningTurns = 32
	// maxRunningTurnsPerUser bounds one user's share of maxRunningTurns.
	maxRunningTurnsPerUser = 4
)

var (
	// ErrServiceClosed is returned by Chat and StartChat once Close has begun.
	ErrServiceClosed = errors.New("maurice service is closed")
	// ErrTooManyTurns is returned by Chat and StartChat for a new turn while
	// the process or the user runs as many turns as it may; a key resent with
	// its prompt still returns its turn.
	ErrTooManyTurns = errors.New("too many maurice turns are running; retry once one finishes")
)

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

// turnRunner tracks the detached turns so Close can wait for them, refuses
// new ones once closed, and counts the reserved turn slots: maxTurns in all,
// maxUserTurns per user. The lock orders every wg.Go before the closing Wait.
type turnRunner struct {
	mu           sync.Mutex
	closed       bool
	wg           sync.WaitGroup
	maxTurns     int
	maxUserTurns int
	running      int
	userRunning  map[string]int
}

func newTurnRunner() *turnRunner {
	return &turnRunner{
		maxTurns: maxRunningTurns, maxUserTurns: maxRunningTurnsPerUser,
		userRunning: make(map[string]int),
	}
}

// reserve claims a slot for one of userID's turns and returns the function
// that frees it; calling that function again does nothing. It fails with
// ErrServiceClosed once closed and ErrTooManyTurns when no slot is left.
func (r *turnRunner) reserve(userID string) (func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.closed:
		return nil, ErrServiceClosed
	case r.running >= r.maxTurns, r.userRunning[userID] >= r.maxUserTurns:
		return nil, ErrTooManyTurns
	}
	r.running++
	r.userRunning[userID]++
	return sync.OnceFunc(func() { r.free(userID) }), nil
}

func (r *turnRunner) free(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.running--
	if r.userRunning[userID]--; r.userRunning[userID] == 0 {
		delete(r.userRunning, userID)
	}
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

func (r *turnRunner) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

// StartChat opens a turn like Chat but returns as soon as it runs; GetTurn
// reports its progress. A key resent with the same prompt returns its turn,
// running or finished, without running the prompt again; with another
// prompt it is ErrIdempotencyKeyReused.
func (s *service) StartChat(ctx context.Context, req ChatRequest) (*TurnRef, error) {
	start, release, err := s.beginTurn(ctx, req, true)
	if err != nil {
		return nil, err
	}
	ref := &TurnRef{TurnID: start.TurnID, ConversationID: start.ConversationID}
	if start.Replay != nil {
		return ref, nil
	}
	if _, err := s.launchTurn(ctx, req, start, release); err != nil {
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
// ErrTurnInProgress (BeginTurnParams.ReplayRunning). A new turn holds a
// runner slot, which the returned release frees (launchTurn); a replay holds
// none. Without a free slot only a replay is possible, else ErrTooManyTurns.
func (s *service) beginTurn(ctx context.Context, req ChatRequest, replayRunning bool) (*TurnStart, func(), error) {
	params, err := s.beginTurnParams(req)
	if err != nil {
		return nil, nil, err
	}
	params.ReplayRunning = replayRunning
	release, err := s.runner.reserve(req.UserID)
	switch {
	case errors.Is(err, ErrTooManyTurns):
		params.ReplayOnly, release = true, func() {}
	case err != nil:
		return nil, nil, err
	}
	start, err := s.db.BeginTurn(ctx, params)
	if errors.Is(err, ErrNoTurnUnderKey) {
		return nil, nil, ErrTooManyTurns
	}
	if err != nil {
		release()
		return nil, nil, fmt.Errorf("begin turn: %w", err)
	}
	if start.Replay != nil {
		release()
	}
	return start, release, nil
}

// launchTurn runs a begun turn detached from the request: the turn keeps
// ctx's values but not its cancellation, so a client that disconnects does
// not lose it. s.turnTimeout bounds it and Close cancels it; either way it
// commits as runTurn does, then frees its slot (release). The returned
// channel receives the turn's result once. If Close has begun, nothing runs
// and the turn is committed as cancelled.
func (s *service) launchTurn(ctx context.Context, req ChatRequest, start *TurnStart, release func()) (<-chan turnResult, error) {
	done := make(chan turnResult, 1)
	turnCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.turnTimeout)
	started := s.runner.goTurn(func() {
		defer cancel()
		stop := context.AfterFunc(s.shutdownCtx, cancel)
		defer stop()
		resp, err := s.runTurn(turnCtx, req, start)
		if err != nil {
			log.Warn().Err(err).Str("turn", start.TurnID).Str("conversation", start.ConversationID).
				Msg("maurice turn failed")
		}
		// Freed before the result is handed over, so a Chat that returned
		// left its slot free.
		release()
		done <- turnResult{resp: resp, err: err}
	})
	if !started {
		cancel()
		s.releaseTurn(ctx, TurnRecord{
			TurnID: start.TurnID, ConversationID: start.ConversationID,
			Status: TurnCancelled, ErrorClass: ErrorClassCancelled,
		})
		release()
		return nil, ErrServiceClosed
	}
	return done, nil
}
