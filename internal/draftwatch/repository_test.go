package draftwatch

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/draftsession"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envDraftWatchTestPGURL = "PUCKDB_TEST_PG_URL"
	draftWatchTestDBMarker = "test"
)

var draftWatchMigrateOnce struct {
	sync.Once
	err error
}

func openDraftWatchTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envDraftWatchTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL draft watch tests", envDraftWatchTestPGURL)
	}
	require.Contains(t, dbURL, draftWatchTestDBMarker,
		"%s must name a dedicated test database", envDraftWatchTestPGURL)
	draftWatchMigrateOnce.Do(func() { draftWatchMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, draftWatchMigrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestRepositorySyncVersionAndExpectedManualState(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity, PollResult{PolledAt: time.Now().UTC()})
	require.NoError(t, err)
	assertSessionVersions(t, fixture, 0, 1)

	pollErr := errors.New("test poll failure")
	require.NoError(t, fixture.repo.RecordFailure(fixture.ctx, fixture.identity, time.Now().UTC(), time.Second, pollErr))
	assertSessionVersions(t, fixture, 0, 2)

	key := draftsession.PickKey{Round: 1, Pick: 1}
	manual := manualOperation(key, draftsession.ManualAdd, 11)
	session, _, err := fixture.repo.ApplyManualAtVersion(fixture.ctx, fixture.identity, manual, 0, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, uint64(1), session.State.Version)
	assert.Equal(t, uint64(3), session.SyncVersion)
	assertManualVersionMismatch(t, fixture, manual, 0, 1)
}

func TestRepositoryConflictVersionAndOrderedEvents(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	key := draftsession.PickKey{Round: 1, Pick: 1}
	prepareConflict(t, fixture, key)
	assertResolutionVersionMismatch(t, fixture, key)

	session, _, err := fixture.repo.ResolveConflictAtVersion(fixture.ctx, fixture.identity, key,
		draftsession.AcceptUpstream, 3, time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, uint64(4), session.State.Version)
	assert.Equal(t, uint64(5), session.SyncVersion)
	assertEventsInVersionOrder(t, fixture)
}

func TestRepositoryRestartKeepsCompleteBoardAcrossDuplicateAndPartialPolls(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	complete := replayPoll(true, 4,
		draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 1, PlayerID: 101},
		draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: 2, PlayerID: 102},
		draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 3}, TeamID: 3, PlayerID: 103},
		draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 4}, TeamID: 4, PlayerID: 104},
	)
	first, firstReport, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity, complete)
	require.NoError(t, err)
	require.True(t, firstReport.SafeToRecommend)

	restarted := NewRepository(fixture.repo.pool)
	recovered, err := restarted.Get(fixture.ctx, fixture.identity.LeagueKey)
	require.NoError(t, err)
	assert.Equal(t, first.State, recovered.State)
	assert.Equal(t, draftsession.EffectiveBoard(first.State), draftsession.EffectiveBoard(recovered.State))

	duplicate, duplicateReport, err := restarted.Reconcile(fixture.ctx, fixture.identity, complete)
	require.NoError(t, err)
	assert.False(t, duplicateReport.Changed)
	assert.Equal(t, recovered.State.Version, duplicate.State.Version)

	partial := replayPoll(false, 4,
		draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 1}, TeamID: 1, PlayerID: 101},
		draftsession.ObservedPick{Key: draftsession.PickKey{Round: 1, Pick: 2}, TeamID: 2, PlayerID: 102},
	)
	afterPartial, partialReport, err := restarted.Reconcile(fixture.ctx, fixture.identity, partial)
	require.NoError(t, err)
	assert.False(t, partialReport.SafeToRecommend)
	assert.Len(t, afterPartial.State.Upstream, len(complete.Snapshot.Picks), "partial replay must not delete unseen picks")
}

func TestRepositoryPersistsPlayersYahooCorrectedAway(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	key := draftsession.PickKey{Round: 1, Pick: 1}
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity,
		replayPoll(true, 1, draftsession.ObservedPick{Key: key, TeamID: 3, PlayerID: 102}))
	require.NoError(t, err)
	_, _, err = fixture.repo.Reconcile(fixture.ctx, fixture.identity,
		replayPoll(true, 1, draftsession.ObservedPick{Key: key, TeamID: 3, PlayerID: 103}))
	require.NoError(t, err)

	recovered, err := NewRepository(fixture.repo.pool).Get(fixture.ctx, fixture.identity.LeagueKey)

	require.NoError(t, err)
	assert.Equal(t, map[int]bool{102: true, 103: true}, recovered.State.UpstreamPlayers)
}

func TestRepositorySeedsUpstreamPlayersOfLegacyBoard(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	key := draftsession.PickKey{Round: 1, Pick: 1}
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity,
		replayPoll(true, 1, draftsession.ObservedPick{Key: key, TeamID: 3, PlayerID: 102}))
	require.NoError(t, err)
	_, err = fixture.repo.pool.Exec(fixture.ctx,
		`UPDATE draft_sessions SET board = board - 'upstreamPlayers' WHERE league_key=$1`, fixture.identity.LeagueKey)
	require.NoError(t, err)

	session, err := fixture.repo.Get(fixture.ctx, fixture.identity.LeagueKey)

	require.NoError(t, err)
	assert.Equal(t, map[int]bool{102: true}, session.State.UpstreamPlayers)
}

func TestRepositoryRejectsManualDuplicateWithoutWriting(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	first, second := draftsession.PickKey{Round: 1, Pick: 1}, draftsession.PickKey{Round: 1, Pick: 2}
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity,
		replayPoll(true, 1, draftsession.ObservedPick{Key: first, TeamID: 1, PlayerID: 101}))
	require.NoError(t, err)
	assertSessionVersions(t, fixture, 1, 1)

	_, _, err = fixture.repo.ApplyManualAtVersion(fixture.ctx, fixture.identity,
		manualOperation(second, draftsession.ManualAdd, 101), 1, time.Now().UTC())

	require.ErrorIs(t, err, draftsession.ErrDuplicatePlayer)
	assertSessionVersions(t, fixture, 1, 1)
	session, err := fixture.repo.Get(fixture.ctx, fixture.identity.LeagueKey)
	require.NoError(t, err)
	assert.Empty(t, session.State.Manual)
	assert.True(t, session.RecommendationsSafe)
}

func TestRepositoryPersistsUpstreamDuplicateAsUnsafeConflict(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	first, second := draftsession.PickKey{Round: 1, Pick: 1}, draftsession.PickKey{Round: 1, Pick: 2}
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity, replayPoll(true, 0))
	require.NoError(t, err)
	_, _, err = fixture.repo.ApplyManualAtVersion(fixture.ctx, fixture.identity,
		manualOperation(second, draftsession.ManualAdd, 101), 1, time.Now().UTC())
	require.NoError(t, err)

	session, report, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity,
		replayPoll(true, 1, draftsession.ObservedPick{Key: first, TeamID: 1, PlayerID: 101}))

	require.NoError(t, err)
	assert.Len(t, report.Duplicates, 1)
	assert.False(t, session.RecommendationsSafe, "the stored flag must reflect the duplicate")
	assert.True(t, session.State.Manual[second].Conflict, "the conflict flag must survive the board round trip")

	session, _, err = fixture.repo.ResolveConflictAtVersion(fixture.ctx, fixture.identity, second,
		draftsession.AcceptUpstream, session.State.Version, time.Now().UTC())
	require.NoError(t, err)
	assert.True(t, session.RecommendationsSafe)
	assert.Empty(t, session.State.Manual)
}

func replayPoll(authoritative bool, expected int, picks ...draftsession.ObservedPick) PollResult {
	return PollResult{PolledAt: time.Now().UTC(), Snapshot: draftsession.Snapshot{
		Authoritative: authoritative, HasExpectedCount: true, ExpectedCount: expected,
		RawCount: len(picks), Picks: picks,
	}}
}

func TestSessionSafeToRecommendRequiresStoredFlagAndBoardInvariants(t *testing.T) {
	first, second := draftsession.PickKey{Round: 1, Pick: 1}, draftsession.PickKey{Round: 1, Pick: 2}
	valid := draftsession.State{
		Upstream: map[draftsession.PickKey]draftsession.Pick{first: {Key: first, TeamID: 1, PlayerID: 101}},
		Manual:   map[draftsession.PickKey]draftsession.ManualChange{}, UpstreamComplete: true,
	}
	duplicate := valid
	duplicate.Manual = map[draftsession.PickKey]draftsession.ManualChange{second: {Kind: draftsession.ManualAdd,
		Pick: &draftsession.Pick{Key: second, TeamID: 2, PlayerID: 101}}}

	assert.True(t, Session{State: valid, RecommendationsSafe: true}.SafeToRecommend())
	assert.False(t, Session{State: valid}.SafeToRecommend(), "a failed poll clears the stored flag")
	assert.False(t, Session{State: duplicate, RecommendationsSafe: true}.SafeToRecommend(),
		"a row stored before the duplicate invariant must not stay safe")
}

func TestListEventsValidatesCursorAndLimitBeforeDatabaseAccess(t *testing.T) {
	var repo *Repository
	_, err := repo.ListEvents(context.Background(), "league", 0, 0)
	assert.ErrorContains(t, err, "limit must be positive")
	_, err = repo.ListEvents(context.Background(), "league", uint64(^uint64(0)>>1)+1, 1)
	assert.ErrorContains(t, err, "cursor exceeds PostgreSQL bigint")
}

type repositoryTestFixture struct {
	ctx      context.Context
	identity Identity
	repo     *Repository
}

func newRepositoryTestFixture(t *testing.T) repositoryTestFixture {
	t.Helper()
	pool := openDraftWatchTestDB(t)
	ctx := context.Background()
	identity := testIdentityForRepository(t)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM draft_sessions WHERE league_key=$1`, identity.LeagueKey) })
	return repositoryTestFixture{ctx: ctx, identity: identity, repo: NewRepository(pool)}
}

func assertSessionVersions(t *testing.T, fixture repositoryTestFixture, stateVersion, syncVersion uint64) {
	t.Helper()
	session, err := fixture.repo.Get(fixture.ctx, fixture.identity.LeagueKey)
	require.NoError(t, err)
	assert.Equal(t, stateVersion, session.State.Version)
	assert.Equal(t, syncVersion, session.SyncVersion)
}

func manualOperation(key draftsession.PickKey, kind draftsession.ManualKind, playerID int) draftsession.ManualOperation {
	return draftsession.ManualOperation{Kind: kind, Key: key,
		Pick: &draftsession.Pick{Key: key, TeamID: 1, PlayerID: playerID}}
}

func assertManualVersionMismatch(t *testing.T, fixture repositoryTestFixture, operation draftsession.ManualOperation, expected, actual uint64) {
	t.Helper()
	_, _, err := fixture.repo.ApplyManualAtVersion(fixture.ctx, fixture.identity, operation, expected, time.Now().UTC())
	assertVersionMismatch(t, err, expected, actual)
}

func assertResolutionVersionMismatch(t *testing.T, fixture repositoryTestFixture, key draftsession.PickKey) {
	t.Helper()
	_, _, err := fixture.repo.ResolveConflictAtVersion(fixture.ctx, fixture.identity, key,
		draftsession.AcceptUpstream, 2, time.Now().UTC())
	assertVersionMismatch(t, err, 2, 3)
}

func assertVersionMismatch(t *testing.T, err error, expected, actual uint64) {
	t.Helper()
	var stale *StaleStateVersionError
	require.ErrorAs(t, err, &stale)
	assert.Equal(t, expected, stale.Expected)
	assert.Equal(t, actual, stale.Actual)
	assert.ErrorIs(t, err, ErrStaleStateVersion)
}

func prepareConflict(t *testing.T, fixture repositoryTestFixture, key draftsession.PickKey) {
	t.Helper()
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity, PollResult{PolledAt: time.Now().UTC()})
	require.NoError(t, err)
	add := manualOperation(key, draftsession.ManualAdd, 11)
	_, _, err = fixture.repo.ApplyManualAtVersion(fixture.ctx, fixture.identity, add, 0, time.Now().UTC())
	require.NoError(t, err)
	correct := manualOperation(key, draftsession.ManualCorrect, 12)
	_, _, err = fixture.repo.ApplyManualAtVersion(fixture.ctx, fixture.identity, correct, 1, time.Now().UTC())
	require.NoError(t, err)
	reconcileConflict(t, fixture, key)
	assertSessionVersions(t, fixture, 3, 4)
}

func reconcileConflict(t *testing.T, fixture repositoryTestFixture, key draftsession.PickKey) {
	t.Helper()
	_, _, err := fixture.repo.Reconcile(fixture.ctx, fixture.identity, PollResult{
		PolledAt: time.Now().UTC(),
		Snapshot: draftsession.Snapshot{
			Authoritative: true, HasExpectedCount: true, ExpectedCount: 1, RawCount: 1,
			Picks: []draftsession.ObservedPick{{Key: key, TeamID: 1, PlayerID: 13}},
		},
	})
	require.NoError(t, err)
}

func assertEventsInVersionOrder(t *testing.T, fixture repositoryTestFixture) {
	t.Helper()
	events, err := fixture.repo.ListEvents(fixture.ctx, fixture.identity.LeagueKey, 0, maxDraftEventPageSize+5)
	require.NoError(t, err)
	require.Len(t, events, 4)
	wantKinds := []string{"manual_add", "manual_correct", "upstream_reconcile", "resolve_accept_upstream"}
	for i, kind := range wantKinds {
		assert.Equal(t, uint64(i+1), events[i].StateVersion)
		assert.Equal(t, kind, events[i].Kind)
	}
	events, err = fixture.repo.ListEvents(fixture.ctx, fixture.identity.LeagueKey, 1, 5)
	require.NoError(t, err)
	require.Len(t, events, 3)
	for i := range events {
		assert.Equal(t, uint64(i+2), events[i].StateVersion)
	}
}

func testIdentityForRepository(t *testing.T) Identity {
	t.Helper()
	leagueID := int(time.Now().UnixNano()%999_999_999) + 1
	return Identity{
		LeagueKey: fmt.Sprintf("999.l.%d", leagueID),
		Season:    2026,
		LeagueID:  leagueID,
		GameKey:   999,
	}
}

func TestRepositoryGetMapsMissingSessionToPackageSentinel(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	_, err := fixture.repo.Get(fixture.ctx, fixture.identity.LeagueKey)
	require.ErrorIs(t, err, ErrSessionNotFound)
	assert.NotErrorIs(t, err, pgx.ErrNoRows, "callers must not depend on pgx semantics")
	assert.ErrorContains(t, err, fixture.identity.LeagueKey)
}

func TestResolveIdentityMapsMissingRuleSnapshotToPackageSentinel(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	_, err := ResolveIdentity(fixture.ctx, fixture.repo.pool, fixture.identity.LeagueKey, fixture.identity.Season)
	require.ErrorIs(t, err, ErrLeagueNotImported)
	assert.NotErrorIs(t, err, pgx.ErrNoRows)
	assert.ErrorContains(t, err, "sync its league metadata first")

	_, err = ResolveIdentity(fixture.ctx, fixture.repo.pool, strconv.Itoa(fixture.identity.LeagueID), fixture.identity.Season)
	require.ErrorIs(t, err, ErrLeagueNotImported)
	assert.NotErrorIs(t, err, pgx.ErrNoRows)
}

func TestRecordFailureStoresTypedErrorClass(t *testing.T) {
	fixture := newRepositoryTestFixture(t)
	pollErr := fmt.Errorf("poll draft: %w", &httpx.HTTPError{StatusCode: http.StatusTooManyRequests})
	require.NoError(t, fixture.repo.RecordFailure(fixture.ctx, fixture.identity, time.Now().UTC(), time.Second, pollErr))

	observations, err := fixture.repo.ListObservations(fixture.ctx, fixture.identity.LeagueKey, 1)
	require.NoError(t, err)
	require.Len(t, observations, 1)
	assert.Equal(t, ErrorClassRateLimited, observations[0].ErrorClass)
}
