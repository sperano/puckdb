package draftwatch

import (
	"context"
	"errors"
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
