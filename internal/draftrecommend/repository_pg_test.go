package draftrecommend

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/fixtures/simfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	// storedRunLatency is the latency stored with every test run; Evaluate
	// measures a real one, which would make the column check flaky.
	storedRunLatency = 42
	// storedRunMicros is a sub-second part with microsecond precision, the
	// precision of a timestamptz column.
	storedRunMicros = 123456 * time.Microsecond
)

// storedRunInput evaluates the shared recommendation fixture generated at
// generatedAt and returns its input and result as Save receives them.
func storedRunInput(t *testing.T, generatedAt time.Time) (Input, Result) {
	t.Helper()
	input := recommendationInput(t)
	input.GeneratedAt = generatedAt
	result, err := Evaluate(input)
	require.NoError(t, err)
	result.LatencyMillis = storedRunLatency
	return input, result
}

func newRunStoreTestDB(t *testing.T) (*Repository, *pgxpool.Pool) {
	t.Helper()
	pool := simfixtures.NewPool(t, simfixtures.NewDatabase(t))
	return NewRepository(pool), pool
}

func TestRepositorySaveGetRoundTripsRunOverPostgreSQL(t *testing.T) {
	store, pool := newRunStoreTestDB(t)
	ctx := t.Context()
	generatedAt := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC).Add(storedRunMicros)
	input, result := storedRunInput(t, generatedAt)

	id, err := store.Save(ctx, input, result)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, id)

	run, err := store.Get(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, id, run.ID)
	assertSameJSON(t, decodedInput(t, input), run.Input)
	assertSameJSON(t, result, run.Result)
	assertStoredRunColumns(t, pool, id, input, result)
}

func TestRepositoryLatestPicksNewestRunOfSessionVersion(t *testing.T) {
	store, _ := newRunStoreTestDB(t)
	ctx := t.Context()
	earlier := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	older, olderResult := storedRunInput(t, earlier)
	_, err := store.Save(ctx, older, olderResult)
	require.NoError(t, err)
	newer, newerResult := storedRunInput(t, earlier.Add(time.Minute))
	newestID, err := store.Save(ctx, newer, newerResult)
	require.NoError(t, err)
	// A later run for another session version must not be the latest.
	other, otherResult := storedRunInput(t, earlier.Add(time.Hour))
	other.Session.Version++
	otherResult.SessionVersion = other.Session.Version
	_, err = store.Save(ctx, other, otherResult)
	require.NoError(t, err)

	run, err := store.Latest(ctx, newer.SessionLeagueKey, newer.Session.Version)
	require.NoError(t, err)
	assert.Equal(t, newestID, run.ID)
	assert.True(t, run.Result.GeneratedAt.Equal(newerResult.GeneratedAt))
}

func TestRepositoryLatestBreaksGeneratedAtTieByID(t *testing.T) {
	store, _ := newRunStoreTestDB(t)
	ctx := t.Context()
	generatedAt := time.Date(2026, 10, 4, 12, 30, 0, 0, time.UTC)
	input, result := storedRunInput(t, generatedAt)
	first, err := store.Save(ctx, input, result)
	require.NoError(t, err)
	second, err := store.Save(ctx, input, result)
	require.NoError(t, err)

	run, err := store.Latest(ctx, input.SessionLeagueKey, input.Session.Version)
	require.NoError(t, err)
	want := first
	if second.String() > first.String() {
		want = second
	}
	assert.Equal(t, want, run.ID, "equal generated_at must fall back to id DESC")
}

func TestRepositoryMapsMissingRunsToPackageSentinel(t *testing.T) {
	store, _ := newRunStoreTestDB(t)
	ctx := t.Context()

	_, err := store.Get(ctx, uuid.New())
	require.ErrorIs(t, err, ErrRunNotFound)
	assert.NotErrorIs(t, err, pgx.ErrNoRows, "callers must not depend on pgx semantics")

	_, err = store.Latest(ctx, "nhl.l.1", 7)
	require.ErrorIs(t, err, ErrRunNotFound)
	assert.NotErrorIs(t, err, pgx.ErrNoRows)
	assert.ErrorContains(t, err, "league nhl.l.1 session 7")
}

// assertStoredRunColumns checks the metadata columns Save derives from the
// input and result, which audit queries read without decoding the JSON.
func assertStoredRunColumns(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, input Input, result Result) {
	t.Helper()
	var (
		leagueKey, rankingIdentity, rankingVersion, projectionVersion, rulesHash, scenario string
		sessionVersion, latency                                                            int64
		rankingSnapshotID, projectionSnapshotID                                            uuid.UUID
		strategy                                                                           []byte
		generatedAt                                                                        time.Time
	)
	require.NoError(t, pool.QueryRow(context.Background(), `
SELECT league_key, session_version, ranking_snapshot_id, ranking_identity, ranking_version,
       projection_snapshot_id, projection_version, rules_hash, scenario, strategy,
       generated_at, latency_milliseconds
FROM draft_recommendation_runs WHERE id = $1`, id).Scan(
		&leagueKey, &sessionVersion, &rankingSnapshotID, &rankingIdentity, &rankingVersion,
		&projectionSnapshotID, &projectionVersion, &rulesHash, &scenario, &strategy,
		&generatedAt, &latency))
	ranking := input.Ranking
	assert.Equal(t, ranking.League.LeagueKey, leagueKey)
	assert.Equal(t, int64(input.Session.Version), sessionVersion)
	assert.Equal(t, ranking.ID, rankingSnapshotID)
	assert.Equal(t, ranking.Identity, rankingIdentity)
	assert.Equal(t, result.RankingVersion, rankingVersion)
	assert.Equal(t, ranking.Projection.SnapshotID, projectionSnapshotID)
	assert.Equal(t, ranking.Projection.ModelVersion, projectionVersion)
	assert.Equal(t, ranking.League.RulesHash, rulesHash)
	assert.Equal(t, string(result.Scenario), scenario)
	assertSameJSON(t, input.Strategy, json.RawMessage(strategy))
	assert.True(t, result.GeneratedAt.Equal(generatedAt), "generated_at = %s, want %s", generatedAt, result.GeneratedAt)
	assert.Equal(t, int64(storedRunLatency), latency)
}

// decodedInput is input after the JSON round trip every stored input
// takes; decoding derives the session's upstream player set, which the
// hand-built fixture state leaves empty.
func decodedInput(t *testing.T, input Input) Input {
	t.Helper()
	raw, err := json.Marshal(input)
	require.NoError(t, err)
	var decoded Input
	require.NoError(t, json.Unmarshal(raw, &decoded))
	return decoded
}

// assertSameJSON compares two values by their JSON encoding, the form the
// store keeps (maps and times have no other useful equality).
func assertSameJSON(t *testing.T, want, got any) {
	t.Helper()
	wantRaw, err := json.Marshal(want)
	require.NoError(t, err)
	gotRaw, err := json.Marshal(got)
	require.NoError(t, err)
	assert.JSONEq(t, string(wantRaw), string(gotRaw))
}
