package newsadjust

// PostgreSQL-backed tests of override history and adjustment runs: the real
// migrations and queries, and a replay that rebuilds a stored run from its
// stored inputs. They skip unless PUCKDB_TEST_PG_URL names a test database
// (see CLAUDE.md "Database-backed tests").

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/newsevent"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

func openAdjustmentTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL news adjustment tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", envTestPGURL, testDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `TRUNCATE news_adjustment_overrides, news_adjustment_runs, projection_snapshots CASCADE`)
	require.NoError(t, err)
	return pool
}

func TestRepository_OverrideHistory(t *testing.T) {
	repo := NewRepository(openAdjustmentTestDB(t))
	ctx := context.Background()
	override := testOverride(NewOverrideID(), OverrideMissedGames, 10)
	override.LeagueKey, override.ExpiresAt = testLeague1001, testOverrideAt.Add(30*24*time.Hour)
	require.NoError(t, repo.CreateOverride(ctx, override))

	resetAt := testOverrideAt.Add(48 * time.Hour)
	require.NoError(t, repo.ResetOverride(ctx, override.ID, resetAt, "suspension confirmed long"))
	assert.ErrorIs(t, repo.ResetOverride(ctx, override.ID, resetAt, "again"), ErrOverrideNotResettable)

	stored, err := repo.ListOverrides(ctx)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	expected := override
	expected.ResetAt, expected.ResetReason = resetAt, "suspension confirmed long"
	assert.Equal(t, expected, stored[0], "a reset keeps the original values")
}

func TestRepository_SaveRunIsIdempotentAndReplays(t *testing.T) {
	pool := openAdjustmentTestDB(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	baseline := testBaseline(testGoalie(testGoalieKey, testGoalieStarts), testSkater(testSkaterKey))
	baselineID, err := projection.StoreSnapshotWith(ctx, sqlcdb.New(pool), baseline)
	require.NoError(t, err)

	override := testOverride(NewOverrideID(), OverrideMissedGames, 20)
	override.Scenario = ScenarioConservative
	require.NoError(t, repo.CreateOverride(ctx, override))
	overrides, err := repo.ListOverrides(ctx)
	require.NoError(t, err)
	newTeam := int64(9)
	trade := roleEvent("trade", testSkaterKey, EventTrade, RoleChange{TeamID: &newTeam, PowerPlay: DirectionUp})
	trade.IncidentID = 5
	req := testRequest(baseline, testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite}), trade)
	req.Overrides, req.CoverageWarnings = overrides, []string{"rotowire-nhl stale"}
	result := mustApply(t, req)

	runID, err := repo.SaveRun(ctx, baselineID, result)
	require.NoError(t, err)
	again, err := repo.SaveRun(ctx, baselineID, result)
	require.NoError(t, err)
	assert.Equal(t, runID, again, "saving the same adjustment again reuses its run")

	require.NoError(t, repo.ResetOverride(ctx, override.ID, testDraftAt.Add(time.Hour), "no longer needed"))
	replayed, err := repo.Replay(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, result.ID, replayed.ID)
	assert.Equal(t, result.Players, replayed.Players, "explanations are reconstructed from stored versions")

	loaded, err := repo.LoadRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, result.Players, loaded.Result.Players)
	assert.Len(t, loaded.Result.Decisions, len(result.Decisions))
	assert.Len(t, loaded.ScenarioSnapshotIDs, len(Scenarios))
}

// Players of the exclusion ownership test: one matched to a Yahoo ID
// through players.yahoo_id, and one known to news only by Yahoo ID.
const (
	testMatchedNHLID   int64 = 8899001
	testMatchedYahooID       = 99001
	testYahooOnlyID          = 99002
	testMissingEventID       = "news-event:999999999"
)

func insertNewsEvent(t *testing.T, pool *pgxpool.Pool, nhlID pgtype.Int8, yahooID pgtype.Int4) string {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(context.Background(), `INSERT INTO news_events (nhl_player_id, yahoo_player_id, player_name,
		event_type, report_status, first_reported_at, last_reported_at) VALUES ($1, $2, 'Fixture', 'suspension', 'confirmed', $3, $3)
		RETURNING id`, nhlID, yahooID, testReportedAt).Scan(&id))
	return extractedID(id)
}

func TestRepository_ExclusionMustNameAStoredEventAboutItsPlayer(t *testing.T) {
	pool := openAdjustmentTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE news_events CASCADE`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO players (id, yahoo_id, first_name, last_name) VALUES ($1, $2, 'Pool', 'Fixture')
		ON CONFLICT (id) DO UPDATE SET yahoo_id = EXCLUDED.yahoo_id`, testMatchedNHLID, testMatchedYahooID)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM players WHERE id = $1`, testMatchedNHLID) })

	goalieEvent := insertNewsEvent(t, pool, pgtype.Int8{Int64: testGoalieNHLID, Valid: true}, pgtype.Int4{})
	matchedEvent := insertNewsEvent(t, pool, pgtype.Int8{Int64: testMatchedNHLID, Valid: true}, pgtype.Int4{})
	yahooEvent := insertNewsEvent(t, pool, pgtype.Int8{}, pgtype.Int4{Int32: testYahooOnlyID, Valid: true})
	matchedKey := fmt.Sprintf("465.p.%d", testMatchedYahooID)
	yahooKey := fmt.Sprintf("465.p.%d", testYahooOnlyID)

	repo := NewRepository(pool)
	for name, tc := range map[string]struct {
		player, event string
		accepted      bool
	}{
		"NHL key":                       {player: testGoalieKey, event: goalieEvent, accepted: true},
		"pool key through NHL match":    {player: matchedKey, event: matchedEvent, accepted: true},
		"pool key through Yahoo ID":     {player: yahooKey, event: yahooEvent, accepted: true},
		"another NHL player's event":    {player: testSkaterKey, event: goalieEvent},
		"another pool player's event":   {player: matchedKey, event: goalieEvent},
		"missing event":                 {player: testGoalieKey, event: testMissingEventID},
		"event that is not stored":      {player: testGoalieKey, event: "susp"},
		"key naming no player identity": {player: "someone", event: goalieEvent},
	} {
		t.Run(name, func(t *testing.T) {
			exclusion := exclusionOf(NewOverrideID(), tc.event)
			exclusion.PlayerKey = tc.player
			err := repo.CreateOverride(ctx, exclusion)
			if tc.accepted {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, ErrExclusionTarget)
			}
		})
	}
}

func TestRepository_RefusesExclusionsOfStoredRelationships(t *testing.T) {
	pool := openAdjustmentTestDB(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE news_articles, news_events CASCADE`)
	require.NoError(t, err)
	quote := func(text string) []newsevent.Quote { return []newsevent.Quote{{Doc: "D1", Text: text}} }
	reconcileReport(t, pool, "suspension", testReportedAt, newsevent.Event{
		Player: goalieIdentity(), Type: newsevent.TypeSuspension, Status: newsevent.StatusConfirmed,
		Duration: newsevent.Duration{Kind: newsevent.DurationIndefinite}, Evidence: quote("suspended indefinitely"),
	})
	reconcileReport(t, pool, "reinstatement", testReinstatedAt, newsevent.Event{
		Player: goalieIdentity(), Type: newsevent.TypeReinstatement, Status: newsevent.StatusConfirmed,
		Duration: newsevent.Duration{Kind: newsevent.DurationUnknown}, Evidence: quote("has been reinstated"),
	})
	var suspension, reinstatement int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT
		(SELECT id FROM news_events WHERE event_type = 'suspension'), (SELECT id FROM news_events WHERE event_type = 'reinstatement')`,
	).Scan(&suspension, &reinstatement))
	goalie := pgtype.Int8{Int64: testGoalieNHLID, Valid: true}
	original := insertNewsEvent(t, pool, goalie, pgtype.Int4{})
	correction := insertNewsEvent(t, pool, goalie, pgtype.Int4{})
	_, err = pool.Exec(ctx, `UPDATE news_events SET lifecycle = 'superseded', superseded_by = $1 WHERE id = $2`,
		strings.TrimPrefix(correction, extractedIDPrefix), strings.TrimPrefix(original, extractedIDPrefix))
	require.NoError(t, err)

	repo := NewRepository(pool)
	for event, want := range map[string]error{
		extractedID(reinstatement): ErrExclusionUnsupported,
		correction:                 ErrExclusionUnsupported,
		extractedID(suspension):    nil,
		original:                   nil,
	} {
		err := repo.CreateOverride(ctx, exclusionOf(NewOverrideID(), event))
		if want == nil {
			assert.NoError(t, err, event)
		} else {
			assert.ErrorIs(t, err, want, event)
		}
	}
}
