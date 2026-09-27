package projection

// PostgreSQL-backed test of games_appeared in the goalie history and
// evaluation queries: a boxscore lists the dressed backup with no time on
// ice, and only games with positive time on ice are appearances. Skips unless
// PUCKDB_TEST_PG_URL names a test database (see CLAUDE.md "Database-backed
// tests"). Fixtures use a season and IDs no other test touches, and results
// are filtered to them.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

const (
	appearanceSeason     = 20132014
	appearanceNextSeason = 20142015
	appearanceFirstGame  = 3_900_200_001
	appearanceLastGame   = 3_900_200_004
	appearanceStarterID  = 9_200_001
	appearanceBackupID   = 9_200_002
	appearanceHomeTeamID = 1
	appearanceAwayTeamID = 2
)

// appearanceWant is each fixture goalie's expected dressed games,
// appearances and starts.
var appearanceWant = map[int64][3]int32{
	appearanceStarterID: {4, 3, 3},
	appearanceBackupID:  {4, 2, 1},
}

// seedGoalieAppearances dresses both goalies in four games: the starter
// plays three (pulled in the second, where the backup relieves him) and sits
// the third, which the backup starts; the backup sits the other two.
func seedGoalieAppearances(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		execAll(t, pool,
			fmt.Sprintf(`DELETE FROM games WHERE id BETWEEN %d AND %d`, appearanceFirstGame, appearanceLastGame),
			fmt.Sprintf(`DELETE FROM players WHERE id IN (%d, %d)`, appearanceStarterID, appearanceBackupID))
	})
	execAll(t, pool,
		fmt.Sprintf(`INSERT INTO players (id, first_name, last_name, position) VALUES
			(%d, 'Starter', 'Fixture', 'G'), (%d, 'Backup', 'Fixture', 'G')`, appearanceStarterID, appearanceBackupID),
		fmt.Sprintf(`INSERT INTO games (id, season, game_type, game_date, home_team_id, away_team_id, game_state)
			SELECT id, %d, 'regular_season', DATE '2013-11-01' + (id - %d)::int, %d, %d, 'FINAL'
			FROM generate_series(%d::bigint, %d::bigint) AS id`,
			appearanceSeason, appearanceFirstGame, appearanceHomeTeamID, appearanceAwayTeamID,
			appearanceFirstGame, appearanceLastGame),
		fmt.Sprintf(`INSERT INTO game_goalie_stats
			(game_id, player_id, team_id, is_home, sweater_number, starter, toi_seconds, shots_against, saves) VALUES
			(%[1]d, %[5]d, 1, true, 30, true, 3600, 30, 28), (%[1]d, %[6]d, 1, true, 35, false, 0, 0, 0),
			(%[2]d, %[5]d, 1, true, 30, true, 2400, 25, 20), (%[2]d, %[6]d, 1, true, 35, false, 1200, 10, 10),
			(%[3]d, %[5]d, 1, true, 30, false, 0, 0, 0),     (%[3]d, %[6]d, 1, true, 35, true, 3600, 31, 29),
			(%[4]d, %[5]d, 1, true, 30, true, 3600, 27, 27), (%[4]d, %[6]d, 1, true, 35, false, 0, 0, 0)`,
			appearanceFirstGame, appearanceFirstGame+1, appearanceFirstGame+2, appearanceLastGame,
			appearanceStarterID, appearanceBackupID),
	)
}

func TestGoalieQueriesCountAppearancesSeparatelyFromDressedGames(t *testing.T) {
	pool := openFaceoffTestDB(t)
	seedGoalieAppearances(t, pool)
	ctx := context.Background()
	queries := sqlcdb.New(pool)

	history, err := queries.ListProjectionGoalieHistory(ctx, sqlcdb.ListProjectionGoalieHistoryParams{
		Season: appearanceNextSeason, Season_2: appearanceSeason,
		GameDate:                 dateValue(time.Date(2014, time.June, 1, 0, 0, 0, 0, time.UTC)),
		MinimumShutoutToiSeconds: DefaultGoalieShutoutMinTOI,
	})
	require.NoError(t, err)
	seen := 0
	for _, row := range history {
		want, fixture := appearanceWant[row.PlayerID]
		if !fixture {
			continue
		}
		seen++
		require.Equal(t, want, [3]int32{row.GamesPlayed, row.GamesAppeared, row.GamesStarted}, "history %d", row.PlayerID)
		season := goalieSeasonFromRow(row)
		require.Equal(t, int(want[0]), season.GamesPlayed)
		require.Equal(t, int(want[1]), season.GamesAppeared)
	}
	require.Equal(t, len(appearanceWant), seen)

	evaluation, err := queries.ListProjectionGoalieEvaluationData(ctx, sqlcdb.ListProjectionGoalieEvaluationDataParams{
		Season: appearanceSeason, Season_2: appearanceSeason, MinimumShutoutToiSeconds: DefaultGoalieShutoutMinTOI,
	})
	require.NoError(t, err)
	input := evaluationInput(appearanceSeason, time.Time{}, time.Time{}, nil, nil, evaluation)
	seen = 0
	for _, season := range input.Goalies {
		want, fixture := appearanceWant[season.PlayerID]
		if !fixture {
			continue
		}
		seen++
		require.Equal(t, [3]int{int(want[0]), int(want[1]), int(want[2])},
			[3]int{season.GamesPlayed, season.GamesAppeared, season.GamesStarted}, "evaluation %d", season.PlayerID)
	}
	require.Equal(t, len(appearanceWant), seen)
}

// Migration 000016 extends the aging-curve requirement to nhl-baseline-v6:
// a v6 snapshot with its curve stores and loads back, one without it is
// rejected by the database.
func TestProjectionSnapshotsRequireV6AgingCurve(t *testing.T) {
	pool := openFaceoffTestDB(t)
	ctx := context.Background()
	t.Cleanup(func() {
		execAll(t, pool, fmt.Sprintf(`DELETE FROM projection_snapshots WHERE target_season = %d`, appearanceSeason))
	})
	cfg := DefaultConfig()
	require.Equal(t, ModelVersion, cfg.ModelVersion)
	cfg.AgingCurve = testAgingCurve(AgingStep{Group: agingGroupGoalie, Stat: StatSaves, Age: 25, DeltaPer60: 1})
	cfg.AgingCurve.ThroughSeason = appearanceSeason - 10_001
	snapshot := Snapshot{
		TargetSeason: appearanceSeason, AsOf: time.Date(2013, time.September, 1, 0, 0, 0, 0, time.UTC),
		SourceDataHash: "goalie-appearances-v6", Config: cfg,
	}
	queries := sqlcdb.New(pool)
	id, err := StoreSnapshotWith(ctx, queries, snapshot)
	require.NoError(t, err)
	loaded, err := NewRepository(pool).LoadSnapshot(ctx, id)
	require.NoError(t, err)
	require.Equal(t, cfg, loaded.Config)

	snapshot.Config.AgingCurve = nil
	snapshot.SourceDataHash = "goalie-appearances-v6-no-curve"
	_, err = StoreSnapshotWith(ctx, queries, snapshot)
	require.ErrorContains(t, err, "projection_snapshots_aging_curve_check")
}
