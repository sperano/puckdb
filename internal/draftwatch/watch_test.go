package draftwatch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNextBackoffIsBounded(t *testing.T) {
	const (
		interval = 30 * time.Second
		maximum  = 5 * time.Minute
	)
	assert.Equal(t, time.Minute, nextBackoff(interval, interval, maximum))
	assert.Equal(t, maximum, nextBackoff(4*time.Minute, interval, maximum))
	assert.Equal(t, maximum, nextBackoff(maximum, interval, maximum))
}

func TestValidateWatchOptions(t *testing.T) {
	valid := WatchOptions{Interval: time.Second, MaxBackoff: time.Minute, FinalTimeout: time.Second}
	require.NoError(t, validateWatchOptions(valid))
	assert.Error(t, validateWatchOptions(WatchOptions{MaxBackoff: time.Minute, FinalTimeout: time.Second}))
	assert.Error(t, validateWatchOptions(WatchOptions{Interval: time.Minute, MaxBackoff: time.Second, FinalTimeout: time.Second}))
	assert.Error(t, validateWatchOptions(WatchOptions{Interval: time.Second, MaxBackoff: time.Minute}))
}

func TestSyncOncePersistsTokenFailureWithoutReplacingBoard(t *testing.T) {
	upstreamErr := errors.New("token unavailable")
	repository := &fakeSessionRepository{}
	runner := Runner{
		Repository: repository,
		Source: sourceFunc(func(context.Context, Identity) (PollResult, error) {
			return PollResult{}, upstreamErr
		}),
	}

	_, _, err := runner.syncOnce(context.Background(), testDraftIdentity)

	assert.ErrorIs(t, err, upstreamErr)
	assert.ErrorIs(t, repository.failure, upstreamErr)
	assert.False(t, repository.reconciled)
}

func TestSyncOnceCarriesDraftCompletion(t *testing.T) {
	repository := &fakeSessionRepository{session: Session{Complete: true}}
	runner := Runner{
		Repository: repository,
		Source: sourceFunc(func(context.Context, Identity) (PollResult, error) {
			return PollResult{DraftComplete: true}, nil
		}),
	}

	session, _, err := runner.syncOnce(context.Background(), testDraftIdentity)

	require.NoError(t, err)
	assert.True(t, session.Complete)
	assert.True(t, repository.reconciled)
}

func TestSyncOnceRequiresSynchronizationPool(t *testing.T) {
	runner := Runner{}

	_, _, err := runner.SyncOnce(context.Background(), testDraftIdentity)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "database pool is required")
}

const (
	watchTestTimeout = 5 * time.Second
	// watchTestIdle keeps the loop waiting so only a refresh request polls.
	watchTestIdle = time.Hour
)

func TestWatchLoopPollsImmediatelyOnRefreshAndAnswersWithThatPoll(t *testing.T) {
	source := newCountingSource()
	refresh := make(chan RefreshRequest)
	runner := Runner{Repository: &countingRepository{}, Source: source}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.watchLoop(ctx, testDraftIdentity, idleWatchOptions(refresh)) }()
	source.awaitPoll(t)

	reply := make(chan Outcome, 1)
	sendRefresh(t, refresh, RefreshRequest{Reply: reply})
	outcome := awaitOutcome(t, reply)

	require.NoError(t, outcome.Err)
	assert.Equal(t, uint64(2), outcome.Session.SyncVersion, "the answer must come from the requested poll")
	assert.Equal(t, int32(2), source.polls.Load())
	cancel()
	assert.ErrorIs(t, awaitDone(t, done), context.Canceled)
	assert.Equal(t, int32(3), source.polls.Load(), "cancellation still runs the final reconciliation")
}

func TestWaitForNextPollCoalescesQueuedRefreshes(t *testing.T) {
	refresh := make(chan RefreshRequest, 2)
	first, second := make(chan Outcome, 1), make(chan Outcome, 1)
	refresh <- RefreshRequest{Reply: first}
	refresh <- RefreshRequest{Reply: second}

	requests, err := waitForNextPoll(context.Background(), watchTestIdle, refresh)

	require.NoError(t, err)
	require.Len(t, requests, 2)
	answerRefreshes(append(requests, RefreshRequest{Reply: make(chan Outcome)}), Outcome{Session: Session{SyncVersion: 4}})
	assert.Equal(t, uint64(4), (<-first).Session.SyncVersion)
	assert.Equal(t, uint64(4), (<-second).Session.SyncVersion)
}

func TestSyncOnceReportsSyncInProgressWhileWatchHoldsLock(t *testing.T) {
	pool := openDraftWatchTestDB(t)
	identity := testIdentityForRepository(t)
	source := newCountingSource()
	refresh := make(chan RefreshRequest)
	runner := Runner{Pool: pool, Repository: &countingRepository{}, Source: source}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Watch(ctx, identity, idleWatchOptions(refresh)) }()
	source.awaitPoll(t)

	_, _, err := runner.SyncOnce(context.Background(), identity)
	require.ErrorIs(t, err, ErrSyncInProgress)
	assert.Contains(t, err.Error(), identity.LeagueKey)
	assert.Equal(t, int32(1), source.polls.Load(), "a refused one-shot must not poll Yahoo")

	reply := make(chan Outcome, 1)
	sendRefresh(t, refresh, RefreshRequest{Reply: reply})
	require.NoError(t, awaitOutcome(t, reply).Err, "the lock holder still polls on request")

	cancel()
	assert.ErrorIs(t, awaitDone(t, done), context.Canceled)
	_, _, err = runner.SyncOnce(context.Background(), identity)
	assert.NoError(t, err, "the lock is released when the watch stops")
}

func idleWatchOptions(refresh <-chan RefreshRequest) WatchOptions {
	return WatchOptions{Interval: watchTestIdle, MaxBackoff: watchTestIdle, FinalTimeout: watchTestTimeout, Refresh: refresh}
}

func sendRefresh(t *testing.T, refresh chan<- RefreshRequest, request RefreshRequest) {
	t.Helper()
	select {
	case refresh <- request:
	case <-time.After(watchTestTimeout):
		t.Fatal("watch did not take the refresh request")
	}
}

func awaitOutcome(t *testing.T, reply <-chan Outcome) Outcome {
	t.Helper()
	select {
	case outcome := <-reply:
		return outcome
	case <-time.After(watchTestTimeout):
		t.Fatal("watch did not answer the refresh request")
		return Outcome{}
	}
}

func awaitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(watchTestTimeout):
		t.Fatal("watch did not stop")
		return nil
	}
}

// countingSource reports every poll on polled so tests can wait for one.
type countingSource struct {
	polls  atomic.Int32
	polled chan struct{}
}

func newCountingSource() *countingSource {
	const pollSignalBuffer = 16
	return &countingSource{polled: make(chan struct{}, pollSignalBuffer)}
}

func (s *countingSource) Poll(context.Context, Identity) (PollResult, error) {
	s.polls.Add(1)
	s.polled <- struct{}{}
	return PollResult{PolledAt: time.Now().UTC()}, nil
}

func (s *countingSource) awaitPoll(t *testing.T) {
	t.Helper()
	select {
	case <-s.polled:
	case <-time.After(watchTestTimeout):
		t.Fatal("watch did not poll")
	}
}

// countingRepository advances the sync version on every reconciliation.
type countingRepository struct{ syncVersion atomic.Uint64 }

func (r *countingRepository) Reconcile(context.Context, Identity, PollResult) (Session, draftsession.Report, error) {
	return Session{SyncVersion: r.syncVersion.Add(1)}, draftsession.Report{}, nil
}

func (r *countingRepository) RecordFailure(context.Context, Identity, time.Time, time.Duration, error) error {
	return nil
}

type sourceFunc func(context.Context, Identity) (PollResult, error)

func (f sourceFunc) Poll(ctx context.Context, id Identity) (PollResult, error) {
	return f(ctx, id)
}

type fakeSessionRepository struct {
	session    Session
	failure    error
	reconciled bool
}

func (f *fakeSessionRepository) Reconcile(_ context.Context, _ Identity, _ PollResult) (Session, draftsession.Report, error) {
	f.reconciled = true
	return f.session, draftsession.Report{}, nil
}

func (f *fakeSessionRepository) RecordFailure(_ context.Context, _ Identity, _ time.Time, _ time.Duration, err error) error {
	f.failure = err
	return nil
}
