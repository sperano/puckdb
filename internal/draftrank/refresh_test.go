package draftrank_test

// PostgreSQL-backed tests of the complete refresh: rules and pool from the
// Yahoo tables, a baseline projection from seeded NHL games, news
// adjustments, ranking and storage, and every explicit failure state. They
// skip unless PUCKDB_TEST_PG_URL names a test database.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	historySeason   = 20252026
	historyGames    = 20
	historyGameID   = 2025020001
	firstNHLID      = 8470000
	homeTeamID      = 1
	awayTeamID      = 2
	skaterTOI       = 1200
	goalieTOI       = 3600
	goalieShots     = 30
	goalieSaves     = 27
	sweater         = 9
	refreshRunID    = "refresh-run"
	secondRefreshID = "refresh-run-2"
)

var historyStart = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

type seed struct {
	rules, pool, history, seasonDates bool
	scoringType                       string
}

func seedLeague(t *testing.T, pool *pgxpool.Pool, s seed) {
	t.Helper()
	ctx := context.Background()
	q := sqlcdb.New(pool)
	if s.rules {
		rules := draftfixtures.Rules().Rules
		if s.scoringType != "" {
			rules.ScoringType = s.scoringType
		}
		hash, data, err := rules.Hash()
		require.NoError(t, err)
		_, err = q.UpsertYahooLeagueRuleSnapshot(ctx, sqlcdb.UpsertYahooLeagueRuleSnapshotParams{
			Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID, LeagueKey: draftfixtures.LeagueKey,
			GameKey: pgtype.Int4{Int32: int32(rules.GameKey), Valid: true}, Source: string(draft.SourceYahooAPI),
			SourceSeason: draftfixtures.Season, SourceLeagueKey: draftfixtures.LeagueKey,
			FetchedAt: pgtype.Timestamptz{Time: draftfixtures.FetchedAt, Valid: true}, RulesHash: hash, Rules: data,
		})
		require.NoError(t, err)
	}
	if s.pool {
		for i, p := range draftfixtures.Pool() {
			_, err := pool.Exec(ctx, `INSERT INTO yahoo_league_players (league_key, season, league_id, game_key, player_id, player_key,
				full_name, editorial_team_abbr, eligible_positions, fetched_at) VALUES ($1, $2, $3, 465, $4, $5, $6, $7, $8, $9)`,
				draftfixtures.LeagueKey, draftfixtures.Season, draftfixtures.LeagueID, i+1, p.PlayerKey, p.Name, p.Team,
				p.EligiblePositions, draftfixtures.FetchedAt)
			require.NoError(t, err)
		}
	}
	if s.history {
		seedHistory(t, pool)
	}
	if s.seasonDates {
		_, err := pool.Exec(ctx, `INSERT INTO seasons (id, standings_start, standings_end) VALUES ($1, '2026-10-07', '2027-04-15')`,
			draftrank.SeasonID(draftfixtures.Season))
		require.NoError(t, err)
	}
}

// seedHistory gives every pool player a season of completed games: skaters
// score by their position in the pool, goalies win every other game.
func seedHistory(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for game := range historyGames {
		_, err := pool.Exec(ctx, `INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state)
			VALUES ($1, $2, 'regular_season', $3, $4, $5, 'OFF')`,
			historyGameID+game, historySeason, historyStart.AddDate(0, 0, game), homeTeamID, awayTeamID)
		require.NoError(t, err)
	}
	for i, p := range draftfixtures.Pool() {
		nhlID := firstNHLID + i
		position := p.EligiblePositions[0]
		_, err := pool.Exec(ctx, `INSERT INTO players (id, yahoo_id, first_name, last_name, position) VALUES ($1, $2, $3, 'Fixture', $4)`,
			nhlID, i+1, p.Name, position)
		require.NoError(t, err)
		for game := range historyGames {
			if position == draft.PositionGoalie {
				decision := "L"
				if game%(i+1) == 0 {
					decision = "W"
				}
				_, err = pool.Exec(ctx, `INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, decision, starter,
					shots_against, saves, goals_against, toi_seconds) VALUES ($1, $2, $3, true, $4, $5, true, $6, $7, $8, $9)`,
					historyGameID+game, nhlID, homeTeamID, sweater, decision, goalieShots, goalieSaves, goalieShots-goalieSaves, goalieTOI)
			} else {
				goals := (len(draftfixtures.Pool()) - i + game) % 3
				_, err = pool.Exec(ctx, `INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position,
					goals, assists, points, toi_seconds) VALUES ($1, $2, $3, true, $4, $5, $6::smallint, 1, $6::smallint + 1, $7)`,
					historyGameID+game, nhlID, homeTeamID, sweater, position, goals, skaterTOI)
			}
			require.NoError(t, err)
		}
	}
}

func resetHistory(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), fmt.Sprintf(`DELETE FROM games WHERE season = %d; DELETE FROM players WHERE id >= %d AND id < %d;
		DELETE FROM seasons WHERE id = %d`, historySeason, firstNHLID, firstNHLID+len(draftfixtures.Pool()), draftrank.SeasonID(draftfixtures.Season)))
	require.NoError(t, err)
}

func refreshFixture(t *testing.T, pool *pgxpool.Pool, runID string) (draftrank.Refresh, error) {
	t.Helper()
	return refreshFixtureAt(t, pool, runID, draftfixtures.AsOf)
}

func refreshFixtureAt(t *testing.T, pool *pgxpool.Pool, runID string, at time.Time) (draftrank.Refresh, error) {
	t.Helper()
	refresher := draftrank.NewRefresher(pool)
	refresher.Now = func() time.Time { return at }
	return refresher.Refresh(context.Background(), draftrank.RefreshRequest{
		RunID: runID, Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID,
		Options: draftrank.Options{BenchPolicy: string(draft.BenchIncluded)}, Keep: testKeep,
	})
}

func TestRefresher_BuildsServesAndKeepsSnapshotAcrossFailures(t *testing.T) {
	pool := openDraftTestDB(t)
	resetHistory(t, pool)
	t.Cleanup(func() { resetHistory(t, pool) })
	seedLeague(t, pool, seed{rules: true, pool: true, history: true, seasonDates: true})

	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)
	require.NotEqual(t, uuid.Nil, outcome.SnapshotID)

	store := draftrank.NewPGStore(pool)
	svc := draftrank.NewService(store, nil, draftrank.ServiceOptions{Now: func() time.Time { return draftfixtures.AsOf }})
	page, err := svc.Rankings(context.Background(), draftrank.LeagueRef{LeagueKey: draftfixtures.LeagueKey}, uuid.Nil,
		draftrank.Query{Positions: []string{"C", "LW"}})
	require.NoError(t, err)
	assert.Equal(t, draftrank.StatusReady, page.Status)
	assert.Equal(t, outcome.SnapshotID, page.Snapshot.ID)
	assert.Equal(t, draftrank.Scenarios, page.Snapshot.Scenarios, "season dates enable the news scenarios")
	assert.Equal(t, 5, page.Total, "C/LW players listed once each")

	_, err = pool.Exec(context.Background(), `DELETE FROM yahoo_league_players`)
	require.NoError(t, err)
	failed, err := refreshFixtureAt(t, pool, secondRefreshID, draftfixtures.AsOf.Add(time.Hour))
	var coded *draftrank.RefreshError
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, draftrank.RefreshFailed, failed.State)
	assert.Equal(t, draftrank.IssueMissingPool, failed.Code)
	latest, err := store.LatestSnapshotID(context.Background(), draftfixtures.Season, draftfixtures.LeagueID)
	require.NoError(t, err)
	assert.Equal(t, outcome.SnapshotID, latest, "a failed refresh keeps the last good snapshot")
	after, err := svc.Rankings(context.Background(), draftrank.LeagueRef{LeagueKey: draftfixtures.LeagueKey}, uuid.Nil, draftrank.Query{})
	require.NoError(t, err)
	assert.Equal(t, draftrank.StatusReady, after.Status)
	assert.Contains(t, issueCodes(after.Issues), draftrank.IssueRefreshFailed)
}

func TestRefresher_WithoutSeasonDatesRanksBaselineOnly(t *testing.T) {
	pool := openDraftTestDB(t)
	resetHistory(t, pool)
	t.Cleanup(func() { resetHistory(t, pool) })
	seedLeague(t, pool, seed{rules: true, pool: true, history: true})

	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)
	snapshot, err := draftrank.NewPGStore(pool).LoadSnapshot(context.Background(), outcome.SnapshotID)
	require.NoError(t, err)
	assert.Equal(t, []draftrank.Scenario{draftrank.ScenarioBaseline}, snapshot.Scenarios)
	require.Len(t, snapshot.Unavailable, 1)
	assert.Equal(t, draftrank.IssueNewsAdjustmentsDown, snapshot.Unavailable[0].Code)
}

func TestRefresher_FailureStates(t *testing.T) {
	tests := []struct {
		name string
		seed seed
		code draftrank.IssueCode
	}{
		{"no rules", seed{}, draftrank.IssueMissingRules},
		{"unsupported scoring", seed{rules: true, pool: true, scoringType: "vegas"}, draftrank.IssueUnsupportedScoring},
		{"no pool", seed{rules: true}, draftrank.IssueMissingPool},
		{"no projections", seed{rules: true, pool: true}, draftrank.IssueMissingProjections},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := openDraftTestDB(t)
			resetHistory(t, pool)
			seedLeague(t, pool, tt.seed)
			outcome, err := refreshFixture(t, pool, refreshRunID)
			var coded *draftrank.RefreshError
			require.ErrorAs(t, err, &coded)
			assert.Equal(t, tt.code, coded.Code)
			assert.Equal(t, draftrank.RefreshFailed, outcome.State)
			assert.Equal(t, tt.code, outcome.Code, outcome.Error)
			recorded, err := draftrank.NewPGStore(pool).LatestRefresh(context.Background(), draftfixtures.Season, draftfixtures.LeagueID)
			require.NoError(t, err)
			assert.Equal(t, tt.code, recorded.Code)
			assert.False(t, recorded.FinishedAt.IsZero())
		})
	}
}

func TestRefresher_CancellationStoresNothingAndIsRecorded(t *testing.T) {
	pool := openDraftTestDB(t)
	resetHistory(t, pool)
	t.Cleanup(func() { resetHistory(t, pool) })
	seedLeague(t, pool, seed{rules: true, pool: true, history: true, seasonDates: true})

	ctx, cancel := context.WithCancel(context.Background())
	refresher := draftrank.NewRefresher(pool)
	refresher.Now = func() time.Time { return draftfixtures.AsOf }
	var steps []string
	refresher.Heartbeat = func(step string) {
		steps = append(steps, step)
		if step == "rankings" {
			cancel()
		}
	}
	outcome, err := refresher.Refresh(ctx, draftrank.RefreshRequest{
		RunID: refreshRunID, Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID,
		Options: draftrank.Options{BenchPolicy: string(draft.BenchIncluded)},
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, draftrank.RefreshCanceled, outcome.State)
	assert.Equal(t, []string{"projections", "news", "rankings", "store"}, steps)
	store := draftrank.NewPGStore(pool)
	latest, err := store.LatestSnapshotID(context.Background(), draftfixtures.Season, draftfixtures.LeagueID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, latest)
	recorded, err := store.LatestRefresh(context.Background(), draftfixtures.Season, draftfixtures.LeagueID)
	require.NoError(t, err)
	assert.Equal(t, draftrank.RefreshCanceled, recorded.State)
}

func TestSnapshotMetaIsJSONStable(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	data, err := json.Marshal(snapshot.Meta)
	require.NoError(t, err)
	var decoded draftrank.Meta
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, snapshot.Meta, decoded)
}
