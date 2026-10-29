package draftwatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyTrySyncLock and legacySyncUnlock are the session-lock statements
// released binaries send, verbatim. A rolling restart runs them next to the
// current code, so both must name the same advisory lock.
const (
	legacyTrySyncLock = `SELECT pg_try_advisory_lock(hashtextextended('draft-session-sync:' || $1, 0))`
	legacySyncUnlock  = `SELECT pg_advisory_unlock(hashtextextended('draft-session-sync:' || $1, 0))`
)

func TestListObservationsReturnsNewestFirstWithEveryColumn(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	polledAt := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	// Yahoo declares two picks but returns one: a partial, non-authoritative
	// poll, so the declared and parsed counts differ.
	poll := replayPoll(true, 2, draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 1, PlayerID: 101})
	poll.PolledAt, poll.Duration = polledAt, 1500*time.Millisecond
	poll.DraftStatus, poll.ObservedHash = "draft", "observed-hash"
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity, poll)
	require.NoError(t, err)
	failedAt := polledAt.Add(time.Minute)
	require.NoError(t, fixture.repo.RecordFailure(fixture.ctx, fixture.identity, failedAt, -time.Second, errors.New("poll failed")))

	observations, err := fixture.repo.ListObservations(fixture.ctx, fixture.identity.LeagueKey, 10)
	require.NoError(t, err)
	assert.Equal(t, []Observation{
		{PolledAt: failedAt, ErrorClass: ErrorClassTransport, Error: "poll failed"},
		{PolledAt: polledAt, Duration: 1500 * time.Millisecond, Success: true,
			Changed: true, DraftStatus: "draft", DeclaredCount: 2, ParsedCount: 1, SnapshotHash: "observed-hash"},
	}, utcObservations(observations))

	observations, err = fixture.repo.ListObservations(fixture.ctx, fixture.identity.LeagueKey, 1)
	require.NoError(t, err)
	require.Len(t, observations, 1, "the limit bounds the history")
	assert.True(t, failedAt.Equal(observations[0].PolledAt))
}

func TestListObservationsBreaksPolledAtTieByNewestRow(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	at := time.Date(2026, 10, 4, 20, 0, 0, 0, time.UTC)
	require.NoError(t, fixture.repo.RecordFailure(fixture.ctx, fixture.identity, at, time.Second, errors.New("first")))
	require.NoError(t, fixture.repo.RecordFailure(fixture.ctx, fixture.identity, at, time.Second, errors.New("second")))

	observations, err := fixture.repo.ListObservations(fixture.ctx, fixture.identity.LeagueKey, 10)
	require.NoError(t, err)
	require.Len(t, observations, 2)
	assert.Equal(t, "second", observations[0].Error)
	assert.Equal(t, "first", observations[1].Error)
}

func TestSessionSyncLockExcludesLegacyLockStatement(t *testing.T) {
	pool := openDraftWatchTestDB(t)
	ctx := context.Background()
	leagueKey := testIdentityForRepository(t).LeagueKey

	lock, err := acquireSessionSync(ctx, pool, leagueKey)
	require.NoError(t, err)
	other := acquireTestConn(t, pool)
	assert.False(t, tryLegacySyncLock(t, other, leagueKey), "a released binary must not take a held lock")
	lock.Close()

	require.True(t, tryLegacySyncLock(t, other, leagueKey))
	_, err = acquireSessionSync(ctx, pool, leagueKey)
	require.ErrorIs(t, err, ErrSyncInProgress, "the current code must not take a lock a released binary holds")
	var unlocked bool
	require.NoError(t, other.QueryRow(ctx, legacySyncUnlock, leagueKey).Scan(&unlocked))
	require.True(t, unlocked)

	lock, err = acquireSessionSync(ctx, pool, leagueKey)
	require.NoError(t, err, "the lock is free once the released binary unlocks it")
	lock.Close()
}

func acquireTestConn(t *testing.T, pool *pgxpool.Pool) *pgxpool.Conn {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	require.NoError(t, err)
	t.Cleanup(conn.Release)
	return conn
}

func tryLegacySyncLock(t *testing.T, conn *pgxpool.Conn, leagueKey string) bool {
	t.Helper()
	var acquired bool
	require.NoError(t, conn.QueryRow(context.Background(), legacyTrySyncLock, leagueKey).Scan(&acquired))
	return acquired
}

// utcObservations normalizes the time zone pgx gives timestamptz values so
// whole observations compare with assert.Equal.
func utcObservations(observations []Observation) []Observation {
	for i := range observations {
		observations[i].PolledAt = observations[i].PolledAt.UTC()
	}
	return observations
}
