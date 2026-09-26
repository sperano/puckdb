package draftrank_test

// PostgreSQL-backed test of the TEMPORARY stand-in draft pool: a league
// whose latest rules snapshot is draft.SourceTemporaryStandIn has no
// yahoo_league_players rows (Yahoo never runs for it), so its pool must come
// from NHL rosters instead (internal/draft/standin_pool.go). Skips unless
// PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md "Database-backed
// tests").

import (
	"context"
	"strings"
	"testing"

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
	standInTeamID           = homeTeamID
	standInTeamAbbrev       = "EDM"
	standInSecondTeamID     = awayTeamID
	standInSecondTeamAbbrev = "TOR"
	standInRookieNHLID      = firstNHLID + 1000
	standInRookieName       = "No History Rookie"
	// standInOldPlayerNHLID has NHL games, but only before the projection
	// model's history floor for target season 2026-27 (2023-24 by default),
	// so it must be excluded exactly like a true rookie.
	standInOldPlayerNHLID   = firstNHLID + 2000
	standInOldPlayerName    = "Old Timer"
	standInOldGameID        = 2018020001
	standInOldHistorySeason = 20182019
	standInShootsCatches    = "L"
	standInSweaterNumber    = 91
	standInHeightInches     = 72
	standInWeightPounds     = 190
	standInBirthDate        = "2000-01-01"
	standInBirthCountry     = "CAN"
	standInFixturePosition  = draft.PositionCenter
)

// seedStandInRules stores the fixture's rules as a TEMPORARY stand-in
// version (see draft.SourceTemporaryStandIn) instead of the Yahoo API
// version seedLeague stores.
func seedStandInRules(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rules := draftfixtures.Rules().Rules
	hash, data, err := rules.Hash()
	require.NoError(t, err)
	_, err = sqlcdb.New(pool).UpsertYahooLeagueRuleSnapshot(ctx, sqlcdb.UpsertYahooLeagueRuleSnapshotParams{
		Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID, LeagueKey: draftfixtures.LeagueKey,
		GameKey: pgtype.Int4{Int32: int32(rules.GameKey), Valid: true}, Source: string(draft.SourceTemporaryStandIn),
		SourceSeason: draftfixtures.Season, SourceLeagueKey: draftfixtures.LeagueKey,
		FetchedAt: pgtype.Timestamptz{Time: draftfixtures.FetchedAt, Valid: true}, RulesHash: hash, Rules: data,
	})
	require.NoError(t, err)
}

// ensureStandInSeason inserts a seasons row if one isn't there yet: FK target
// for season_teams and season_rosters. The dates are placeholders; nothing
// in these tests reads them for a season other than the league's own.
func ensureStandInSeason(t *testing.T, pool *pgxpool.Pool, season int) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `INSERT INTO seasons (id, standings_start, standings_end)
		VALUES ($1, '2020-10-01', '2021-04-01') ON CONFLICT (id) DO NOTHING`, season)
	require.NoError(t, err)
}

func seedStandInSeasonTeam(t *testing.T, pool *pgxpool.Pool, season int, teamID int64, abbrev string) {
	t.Helper()
	ensureStandInSeason(t, pool, season)
	_, err := pool.Exec(context.Background(), `INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
		VALUES ($1, $2, 'Fixture Team', $3, 'Fixture Division', 'FD')`, season, teamID, abbrev)
	require.NoError(t, err)
}

// insertStandInRoster gives a player a season_rosters row. An empty
// position inserts a NULL roster position (season_rosters.position is NULL
// for nearly every real row; see standInFallbackPositionNHLID below).
func insertStandInRoster(t *testing.T, pool *pgxpool.Pool, season int, teamID, playerID int64, position string) {
	t.Helper()
	if position == "" {
		_, err := pool.Exec(context.Background(), `INSERT INTO season_rosters (season, team_id, player_id, position, shoots_catches,
			sweater_number, height_inches, weight_pounds, birth_date, birth_country)
			VALUES ($1, $2, $3, NULL, $4, $5, $6, $7, $8, $9)`,
			season, teamID, playerID, standInShootsCatches,
			standInSweaterNumber, standInHeightInches, standInWeightPounds, standInBirthDate, standInBirthCountry)
		require.NoError(t, err)
		return
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO season_rosters (season, team_id, player_id, position, shoots_catches,
		sweater_number, height_inches, weight_pounds, birth_date, birth_country)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		season, teamID, playerID, position, standInShootsCatches,
		standInSweaterNumber, standInHeightInches, standInWeightPounds, standInBirthDate, standInBirthCountry)
	require.NoError(t, err)
}

// insertStandInPlayer gives a player a players row. An empty position
// leaves players.position NULL.
func insertStandInPlayer(t *testing.T, pool *pgxpool.Pool, id int64, name, position string) {
	t.Helper()
	if position == "" {
		_, err := pool.Exec(context.Background(), `INSERT INTO players (id, first_name, last_name) VALUES ($1, $2, 'Fixture')`, id, name)
		require.NoError(t, err)
		return
	}
	_, err := pool.Exec(context.Background(), `INSERT INTO players (id, first_name, last_name, position) VALUES ($1, $2, 'Fixture', $3)`,
		id, name, position)
	require.NoError(t, err)
}

// seedStandInOldHistoryGame gives standInOldPlayerNHLID one completed
// regular-season game well before the projection model's history floor.
func seedStandInOldHistoryGame(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state)
		VALUES ($1, $2, 'regular_season', '2018-11-01', $3, $4, 'OFF')`,
		standInOldGameID, standInOldHistorySeason, homeTeamID, awayTeamID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position,
		goals, assists, points, toi_seconds) VALUES ($1, $2, $3, true, $4, $5, 1, 1, 2, $6)`,
		standInOldGameID, standInOldPlayerNHLID, homeTeamID, standInSweaterNumber, standInFixturePosition, skaterTOI)
	require.NoError(t, err)
}

// seedStandInRosters gives every fixture history player (seedHistory) an NHL
// roster row for the league's draft season, so LoadStandInPool can build a
// pool from them, plus a rookie with no NHL history at all and an old timer
// whose only game is before the model's history floor — both must be
// excluded rather than fail the whole league.
func seedStandInRosters(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	nhlSeason := draftrank.SeasonID(draftfixtures.Season)
	seedStandInSeasonTeam(t, pool, nhlSeason, standInTeamID, standInTeamAbbrev)
	for _, p := range draftfixtures.Pool() {
		insertStandInRoster(t, pool, nhlSeason, standInTeamID, p.NHLPlayerID, p.EligiblePositions[0])
	}

	insertStandInPlayer(t, pool, standInRookieNHLID, standInRookieName, standInFixturePosition)
	insertStandInRoster(t, pool, nhlSeason, standInTeamID, standInRookieNHLID, standInFixturePosition)

	insertStandInPlayer(t, pool, standInOldPlayerNHLID, standInOldPlayerName, standInFixturePosition)
	insertStandInRoster(t, pool, nhlSeason, standInTeamID, standInOldPlayerNHLID, standInFixturePosition)
	seedStandInOldHistoryGame(t, pool)
}

// resetStandInRosters clears everything seedStandInRosters, seedStandInRules
// and the fallback test add beyond what resetHistory already clears. It
// covers both the league's own season and the prior season (the fallback
// test seeds both), so it is safe to call whether or not the fallback ran.
func resetStandInRosters(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, season := range []int{draftrank.SeasonID(draftfixtures.Season), historySeason} {
		_, err := pool.Exec(ctx, `DELETE FROM season_rosters WHERE season = $1`, season)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `DELETE FROM season_teams WHERE season = $1`, season)
		require.NoError(t, err)
	}
	// historySeason (the fallback test's prior season) has no other owner of
	// its seasons row; the league's own season's is resetHistory's job.
	_, err := pool.Exec(ctx, `DELETE FROM seasons WHERE id = $1`, historySeason)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM game_skater_stats WHERE game_id = $1`, standInOldGameID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM games WHERE id = $1`, standInOldGameID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM players WHERE id IN ($1, $2)`, standInRookieNHLID, standInOldPlayerNHLID)
	require.NoError(t, err)
}

func assumptionContains(t *testing.T, assumptions []string, substr string) {
	t.Helper()
	for _, a := range assumptions {
		if strings.Contains(a, substr) {
			return
		}
	}
	t.Fatalf("no assumption contains %q; got %v", substr, assumptions)
}

// resetStandIn clears everything a stand-in test seeds, in FK-safe order:
// season_rosters/season_teams (resetStandInRosters) before seasons and
// history games/players (resetHistory).
func resetStandIn(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	resetStandInRosters(t, pool)
	resetHistory(t, pool)
}

func TestRefresher_StandInLeagueBuildsPoolFromNHLRosters(t *testing.T) {
	pool := openDraftTestDB(t)
	resetStandIn(t, pool)
	t.Cleanup(func() { resetStandIn(t, pool) })
	seedStandInRules(t, pool)
	seedHistory(t, pool)
	seedStandInRosters(t, pool)

	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err, outcome.Error)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)

	snapshot, err := draftrank.NewPGStore(pool).LoadSnapshot(context.Background(), outcome.SnapshotID)
	require.NoError(t, err)
	assert.True(t, snapshot.League.Provisional, "stand-in rules mark the league provisional")
	assumptionContains(t, snapshot.Assumptions, "provisional pool from NHL 2026-27 rosters")
	assumptionContains(t, snapshot.Assumptions, "excluded 2 of 10 roster players with no NHL regular-season games in 2023-24")

	require.NotEmpty(t, snapshot.Players)
	for _, p := range snapshot.Players {
		assert.True(t, strings.HasPrefix(p.PlayerKey, "nhl.p."), "pool player key %q should be a stand-in key", p.PlayerKey)
		assert.NotEqual(t, standInRookieNHLID, p.NHLPlayerID, "the no-history rookie must not be ranked")
		assert.NotEqual(t, standInOldPlayerNHLID, p.NHLPlayerID, "a player with only pre-floor history must not be ranked")
	}
}

// TestRefresher_StandInLeagueFallsBackWhenRostersIncomplete covers a partial
// import of the league's own season (one of its two NHL clubs has no
// season_rosters row): the pool must fall back to the complete prior
// season's rosters instead of silently ranking half a league, and
// ListSeasonRosterPoolCandidates's join to season_teams must still resolve
// for that prior season.
func TestRefresher_StandInLeagueFallsBackWhenRostersIncomplete(t *testing.T) {
	pool := openDraftTestDB(t)
	resetStandIn(t, pool)
	t.Cleanup(func() { resetStandIn(t, pool) })
	seedStandInRules(t, pool)
	seedHistory(t, pool) // fixture players' history lands in historySeason, the fallback season

	ownSeason := draftrank.SeasonID(draftfixtures.Season)
	seedStandInSeasonTeam(t, pool, ownSeason, standInTeamID, standInTeamAbbrev)
	seedStandInSeasonTeam(t, pool, ownSeason, standInSecondTeamID, standInSecondTeamAbbrev)
	for _, p := range draftfixtures.Pool() {
		insertStandInRoster(t, pool, ownSeason, standInTeamID, p.NHLPlayerID, p.EligiblePositions[0])
	}
	// standInSecondTeamID has no roster rows: 1 of 2 NHL teams covered.

	seedStandInSeasonTeam(t, pool, historySeason, standInTeamID, standInTeamAbbrev)
	for _, p := range draftfixtures.Pool() {
		insertStandInRoster(t, pool, historySeason, standInTeamID, p.NHLPlayerID, p.EligiblePositions[0])
	}

	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err, outcome.Error)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)

	snapshot, err := draftrank.NewPGStore(pool).LoadSnapshot(context.Background(), outcome.SnapshotID)
	require.NoError(t, err)
	assumptionContains(t, snapshot.Assumptions, "provisional pool from NHL 2025-26 rosters")
	assumptionContains(t, snapshot.Assumptions, "used the prior season's rosters: 2026-27 rosters incomplete: 1 of 2 teams")
	assert.Len(t, snapshot.Players, len(draftfixtures.Pool()), "every fixture player comes from the fallback season's roster")
}

const (
	// standInFallbackPositionNHLID has no roster position but a players.position,
	// and valid history: it must be included with the players.position fallback.
	standInFallbackPositionNHLID = firstNHLID + 3000
	standInFallbackPositionName  = "Fallback Position"
	// standInNoPositionNHLID has neither a roster position nor a
	// players.position: it must be excluded regardless of its history.
	standInNoPositionNHLID = firstNHLID + 3001
	standInNoPositionName  = "No Position"
	// standInTargetSeasonOnlyNHLID's only game is in the league's own target
	// season, which the model never reads as history (it is what is being
	// projected): it must be excluded exactly like a player with no games.
	standInTargetSeasonOnlyNHLID = firstNHLID + 3002
	standInTargetSeasonOnlyName  = "Target Season Only"
	standInTargetSeasonGameID    = 2026020001
)

// resetStandInExtra clears the players and games
// TestRefresher_StandInLeaguePositionFallbackAndHistoryUpperBound adds on
// top of seedStandInRosters, plus everything resetStandIn clears. The target
// season's game is deleted first so its ON DELETE CASCADE clears its
// game_skater_stats row before the players it references are deleted.
func resetStandInExtra(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `DELETE FROM games WHERE id = $1`, standInTargetSeasonGameID)
	require.NoError(t, err)
	resetStandIn(t, pool)
	_, err = pool.Exec(context.Background(), `DELETE FROM players WHERE id IN ($1, $2, $3)`,
		standInFallbackPositionNHLID, standInNoPositionNHLID, standInTargetSeasonOnlyNHLID)
	require.NoError(t, err)
}

// TestRefresher_StandInLeaguePositionFallbackAndHistoryUpperBound covers the
// position fallback (season_rosters.position is NULL for nearly every real
// row, so players.position must carry the pool) and the history window's
// upper bound (a player whose only game is in the league's own target season
// must not pass just because it is recent).
func TestRefresher_StandInLeaguePositionFallbackAndHistoryUpperBound(t *testing.T) {
	pool := openDraftTestDB(t)
	resetStandInExtra(t, pool)
	t.Cleanup(func() { resetStandInExtra(t, pool) })
	seedStandInRules(t, pool)
	seedHistory(t, pool)
	seedStandInRosters(t, pool)

	nhlSeason := draftrank.SeasonID(draftfixtures.Season)
	ctx := context.Background()

	// Roster position NULL, players.position set, valid history (an extra
	// skater in an already-seeded historySeason game): falls back to
	// players.position and is included.
	insertStandInPlayer(t, pool, standInFallbackPositionNHLID, standInFallbackPositionName, draft.PositionDefense)
	insertStandInRoster(t, pool, nhlSeason, standInTeamID, standInFallbackPositionNHLID, "")
	_, err := pool.Exec(ctx, `INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position,
		goals, assists, points, toi_seconds) VALUES ($1, $2, $3, true, $4, $5, 1, 1, 2, $6)`,
		historyGameID, standInFallbackPositionNHLID, homeTeamID, standInSweaterNumber, draft.PositionDefense, skaterTOI)
	require.NoError(t, err)

	// Neither the roster row nor players.position names a position: excluded.
	insertStandInPlayer(t, pool, standInNoPositionNHLID, standInNoPositionName, "")
	insertStandInRoster(t, pool, nhlSeason, standInTeamID, standInNoPositionNHLID, "")

	// A resolvable position, but the only game is in the target season
	// itself: excluded by the history window's upper bound.
	insertStandInPlayer(t, pool, standInTargetSeasonOnlyNHLID, standInTargetSeasonOnlyName, draft.PositionCenter)
	insertStandInRoster(t, pool, nhlSeason, standInTeamID, standInTargetSeasonOnlyNHLID, draft.PositionCenter)
	_, err = pool.Exec(ctx, `INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state)
		VALUES ($1, $2, 'regular_season', '2026-11-01', $3, $4, 'OFF')`, standInTargetSeasonGameID, nhlSeason, homeTeamID, awayTeamID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position,
		goals, assists, points, toi_seconds) VALUES ($1, $2, $3, true, $4, $5, 1, 1, 2, $6)`,
		standInTargetSeasonGameID, standInTargetSeasonOnlyNHLID, homeTeamID, standInSweaterNumber, draft.PositionCenter, skaterTOI)
	require.NoError(t, err)

	outcome, err := refreshFixture(t, pool, refreshRunID)
	require.NoError(t, err, outcome.Error)
	require.Equal(t, draftrank.RefreshSucceeded, outcome.State, outcome.Error)

	snapshot, err := draftrank.NewPGStore(pool).LoadSnapshot(context.Background(), outcome.SnapshotID)
	require.NoError(t, err)

	var sawFallback bool
	for _, p := range snapshot.Players {
		assert.NotEqual(t, standInNoPositionNHLID, p.NHLPlayerID, "a player with no resolvable position must not be ranked")
		assert.NotEqual(t, standInTargetSeasonOnlyNHLID, p.NHLPlayerID, "a player whose only game is in the target season must not be ranked")
		if p.NHLPlayerID == standInFallbackPositionNHLID {
			sawFallback = true
			assert.Equal(t, []string{draft.PositionDefense}, p.EligiblePositions, "falls back to players.position when the roster row has none")
		}
	}
	assert.True(t, sawFallback, "the roster-NULL/players.position-set player must be ranked")
	// 8 fixture + rookie + old timer (seedStandInRosters) + the 3 players
	// above: 9 included, 1 excluded for position, 3 excluded for history.
	assumptionContains(t, snapshot.Assumptions, "excluded 1 of 13 roster players with no known position")
	assumptionContains(t, snapshot.Assumptions, "excluded 3 of 13 roster players with no NHL regular-season games in 2023-24")
}
