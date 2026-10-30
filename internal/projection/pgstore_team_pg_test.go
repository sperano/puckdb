package projection

// PostgreSQL-backed tests of the team-environment queries: club rates from
// games and play_events, target clubs from season_rosters, and the
// upcoming-season carry-forward of season_teams. Skips unless
// PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md "Database-backed
// tests"). Fixtures use seasons and IDs no other test touches, and results
// are filtered to them, so packages sharing the database cannot interfere.

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

const (
	teamEnvSeason     = 20152016
	teamEnvNextSeason = 20162017
	teamEnvHome       = 9_001
	teamEnvAway       = 9_002
	teamEnvNational   = 9_003
	teamEnvRenamed    = 9_004
	teamEnvPlayer     = 9_100_001
)

func execAll(t *testing.T, pool *pgxpool.Pool, statements ...string) {
	t.Helper()
	for _, statement := range statements {
		_, err := pool.Exec(context.Background(), statement)
		require.NoError(t, err, statement)
	}
}

func seedTeamEnvironment(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		execAll(t, pool,
			`DELETE FROM season_rosters WHERE season IN (20152016, 20162017)`,
			`DELETE FROM play_events WHERE game_id BETWEEN 3900000001 AND 3900000004`,
			`DELETE FROM games WHERE id BETWEEN 3900000001 AND 3900000004`,
			`DELETE FROM season_teams WHERE season IN (20152016, 20162017)`,
			`DELETE FROM players WHERE id IN (9100001, 9100002)`,
			`DELETE FROM seasons WHERE id IN (20152016, 20162017)`)
	})
	execAll(t, pool,
		`INSERT INTO seasons (id, standings_start, standings_end) VALUES
			(20152016, '2015-10-07', '2016-04-10'), (20162017, '2016-10-12', '2017-04-09')`,
		`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev) VALUES
			(20152016, 9001, 'Home Club', 'HOM', 'Atlantic', 'A'),
			(20152016, 9002, 'Away Club', 'AWY', 'Atlantic', 'A'),
			(20162017, 9002, 'Already There', 'AWY', 'Metropolitan', 'M')`,
		`INSERT INTO season_teams (season, team_id, full_name, abbrev, team_kind) VALUES
			(20152016, 9003, 'National Team', 'NAT', 'international')`,
		// 3-2 home win: the away club draws a minor and the home club two
		// minors and a bench minor (the major is no power play count).
		`INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state,
			home_team_score, away_team_score, home_team_sog, away_team_sog) VALUES
			(3900000001, 20152016, 'regular_season', '2015-11-01', 9001, 9002, 'FINAL', 3, 2, 30, 25)`,
		// 4-3 shootout win for the away-listed 9002 at home: the winner's
		// extra goal is dropped, and the game has no play-by-play.
		`INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state,
			period_type, home_team_score, away_team_score, home_team_sog, away_team_sog) VALUES
			(3900000002, 20152016, 'regular_season', '2015-11-03', 9002, 9001, 'OFF', 'SO', 4, 3, 28, 31)`,
		`INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state,
			home_team_score, away_team_score, home_team_sog, away_team_sog) VALUES
			(3900000003, 20152016, 'preseason', '2015-09-20', 9001, 9002, 'FINAL', 9, 0, 50, 10),
			(3900000004, 20152016, 'regular_season', '2016-03-01', 9001, 9002, 'FINAL', 9, 0, 50, 10)`,
		`INSERT INTO play_events (game_id, event_id, period, period_type, time_in_period, time_remaining,
			type_desc_key, sort_order, event_owner_team_id, penalty_type_code) VALUES
			(3900000001, 1, 1, 'REG', '01:00', '19:00', 'penalty', 1, 9002, 'MIN'),
			(3900000001, 2, 1, 'REG', '02:00', '18:00', 'penalty', 2, 9002, 'MIN'),
			(3900000001, 3, 1, 'REG', '03:00', '17:00', 'penalty', 3, 9002, 'BEN'),
			(3900000001, 4, 2, 'REG', '04:00', '16:00', 'penalty', 4, 9002, 'MAJ'),
			(3900000001, 5, 2, 'REG', '05:00', '15:00', 'penalty', 5, 9001, 'MIN'),
			(3900000001, 6, 3, 'REG', '06:00', '14:00', 'faceoff', 6, 9001, NULL)`,
	)
}

func TestListProjectionTeamSeasonsDerivesClubRatesFromGames(t *testing.T) {
	pool := openFaceoffTestDB(t)
	seedTeamEnvironment(t, pool)

	rows, err := sqlcdb.New(pool).ListProjectionTeamSeasons(context.Background(), sqlcdb.ListProjectionTeamSeasonsParams{
		Season: teamEnvNextSeason, Season_2: teamEnvSeason,
		GameDate: dateValue(time.Date(2016, time.February, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	byTeam := make(map[int64]sqlcdb.ListProjectionTeamSeasonsRow)
	for _, row := range rows {
		if row.Season == teamEnvSeason && (row.TeamID == teamEnvHome || row.TeamID == teamEnvAway) {
			byTeam[row.TeamID] = row
		}
	}

	require.Equal(t, sqlcdb.ListProjectionTeamSeasonsRow{
		TeamID: teamEnvHome, Season: teamEnvSeason, Abbrev: "HOM", GamesPlayed: 2, GoalsFor: 3 + 3, ShotsFor: 30 + 31,
		PowerPlayGames: 1, PowerPlayOpportunities: 3,
	}, byTeam[teamEnvHome])
	require.Equal(t, sqlcdb.ListProjectionTeamSeasonsRow{
		TeamID: teamEnvAway, Season: teamEnvSeason, Abbrev: "AWY", GamesPlayed: 2, GoalsFor: 2 + 3, ShotsFor: 25 + 28,
		PowerPlayGames: 1, PowerPlayOpportunities: 1,
	}, byTeam[teamEnvAway])
}

func TestListProjectionSkaterClubGamesReturnsOnlySplitSeasons(t *testing.T) {
	pool := openFaceoffTestDB(t)
	seedTeamEnvironment(t, pool)
	execAll(t, pool,
		`INSERT INTO players (id, first_name, last_name, position) VALUES
			(9100001, 'Traded', 'Fixture', 'C'), (9100002, 'Stayed', 'Fixture', 'C')`,
		// The traded player plays game one for the home club and the
		// shootout game for the same club as the visitor; the other
		// player plays both games for the away club.
		`INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position) VALUES
			(3900000001, 9100001, 9001, true, 9, 'C'),
			(3900000002, 9100001, 9002, true, 9, 'C'),
			(3900000001, 9100002, 9002, false, 10, 'C'),
			(3900000002, 9100002, 9002, true, 10, 'C')`)

	rows, err := sqlcdb.New(pool).ListProjectionSkaterClubGames(context.Background(), sqlcdb.ListProjectionSkaterClubGamesParams{
		Season: teamEnvNextSeason, Season_2: teamEnvSeason,
		GameDate: dateValue(time.Date(2016, time.February, 1, 0, 0, 0, 0, time.UTC)),
	})
	require.NoError(t, err)
	var fixtures []sqlcdb.ListProjectionSkaterClubGamesRow
	for _, row := range rows {
		if row.PlayerID == teamEnvPlayer || row.PlayerID == teamEnvPlayer+1 {
			fixtures = append(fixtures, row)
		}
	}
	require.Equal(t, []sqlcdb.ListProjectionSkaterClubGamesRow{
		{PlayerID: teamEnvPlayer, Season: teamEnvSeason, TeamID: teamEnvHome, GamesPlayed: 1},
		{PlayerID: teamEnvPlayer, Season: teamEnvSeason, TeamID: teamEnvAway, GamesPlayed: 1},
	}, fixtures)
}

func TestListProjectionTargetTeamsKeepsMostRecentRosterRow(t *testing.T) {
	pool := openFaceoffTestDB(t)
	seedTeamEnvironment(t, pool)
	execAll(t, pool,
		`INSERT INTO players (id, first_name, last_name, position) VALUES (9100001, 'Target', 'Fixture', 'C')`,
		`INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev) VALUES
			(20162017, 9001, 'Home Club', 'HOM', 'Atlantic', 'A')`,
		`INSERT INTO season_rosters (season, team_id, player_id, shoots_catches, sweater_number, height_inches,
			weight_pounds, birth_date, birth_country, updated_at) VALUES
			(20162017, 9001, 9100001, 'L', 9, 72, 190, '1995-01-01', 'CAN', '2016-09-01'),
			(20162017, 9002, 9100001, 'L', 9, 72, 190, '1995-01-01', 'CAN', '2016-09-10')`)

	rows, err := sqlcdb.New(pool).ListProjectionTargetTeams(context.Background(), teamEnvNextSeason)
	require.NoError(t, err)
	require.Contains(t, rows, sqlcdb.ListProjectionTargetTeamsRow{PlayerID: teamEnvPlayer, TeamID: teamEnvAway})
	for _, row := range rows {
		if row.PlayerID == teamEnvPlayer {
			require.Equal(t, int64(teamEnvAway), row.TeamID, "one club per player: the most recently updated row")
		}
	}
}

func TestCarryForwardSeasonTeamCopiesOneNHLClubUnderItsNewID(t *testing.T) {
	pool := openFaceoffTestDB(t)
	seedTeamEnvironment(t, pool)
	queries := sqlcdb.New(pool)
	carry := func(fromTeam, toTeam int64) int64 {
		copied, err := queries.CarryForwardSeasonTeam(context.Background(), sqlcdb.CarryForwardSeasonTeamParams{
			ToSeason: teamEnvNextSeason, ToTeamID: toTeam, FromSeason: teamEnvSeason, FromTeamID: fromTeam,
		})
		require.NoError(t, err)
		return copied
	}

	require.EqualValues(t, 1, carry(teamEnvHome, teamEnvRenamed), "copied under the resolved ID")
	require.EqualValues(t, 0, carry(teamEnvAway, teamEnvAway), "an existing row is left as is")
	require.EqualValues(t, 0, carry(teamEnvNational, teamEnvNational), "not an NHL club")

	teams, err := queries.GetSeasonTeamAbbrevs(context.Background(), teamEnvNextSeason)
	require.NoError(t, err)
	require.ElementsMatch(t, []sqlcdb.GetSeasonTeamAbbrevsRow{
		{TeamID: teamEnvRenamed, Abbrev: "HOM"}, {TeamID: teamEnvAway, Abbrev: "AWY"},
	}, teams)
	var name, division string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT full_name, division_name FROM season_teams WHERE season = $1 AND team_id = $2`,
		teamEnvNextSeason, teamEnvAway).Scan(&name, &division))
	require.Equal(t, []string{"Already There", "Metropolitan"}, []string{name, division})
}
