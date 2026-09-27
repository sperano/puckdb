package projection

// PostgreSQL-backed coverage for the shift segmentation query. It skips unless
// PUCKDB_TEST_PG_URL names a dedicated test database.

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

const (
	projectionTestPGURL = "PUCKDB_TEST_PG_URL"
	testDBMarker        = "test"
	testLinemateSeason  = 20252026
	testLinemateGame    = 990001
	testHomeTeam        = 100
	testAwayTeam        = 200
	testPlayerBase      = 9_900_000
)

var projectionMigrateOnce struct {
	sync.Once
	err error
}

func openProjectionTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(projectionTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run PostgreSQL projection tests", projectionTestPGURL)
	}
	require.Contains(t, dbURL, testDBMarker, "test database URL must contain %q", testDBMarker)
	projectionMigrateOnce.Do(func() { projectionMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, projectionMigrateOnce.err)
	pool, err := pgxpool.New(context.Background(), dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func TestListProjectionSkaterLinemateContext_SegmentsEvenStrength(t *testing.T) {
	pool := openProjectionTestDB(t)
	cleanupLinemateFixture(t, pool)
	t.Cleanup(func() { cleanupLinemateFixture(t, pool) })
	seedLinemateSegments(t, pool)

	rows, err := sqlcdb.New(pool).ListProjectionSkaterLinemateContext(
		context.Background(),
		sqlcdb.ListProjectionSkaterLinemateContextParams{
			MinSeason: testLinemateSeason,
			MaxSeason: testLinemateSeason,
			GameDate:  dateValue(time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)),
		},
	)
	require.NoError(t, err)

	overlaps := make(map[int64]int64)
	var teammateTwo sqlcdb.ListProjectionSkaterLinemateContextRow
	var teammateFive sqlcdb.ListProjectionSkaterLinemateContextRow
	for _, row := range rows {
		if row.PlayerID == testPlayerBase+1 {
			overlaps[row.TeammateID] = row.SharedToiSeconds
			if row.TeammateID == testPlayerBase+2 {
				teammateTwo = row
			}
			if row.TeammateID == testPlayerBase+5 {
				teammateFive = row
			}
		}
	}
	require.Equal(t, map[int64]int64{
		testPlayerBase + 2: 30, testPlayerBase + 3: 30,
		testPlayerBase + 4: 20, testPlayerBase + 5: 10,
	}, overlaps)
	require.NotContains(t, overlaps, int64(testPlayerBase+20), "goalies without skater stats must be excluded")
	require.EqualValues(t, 1, teammateTwo.TeammateEvenStrengthPoints)
	require.EqualValues(t, 30, teammateTwo.TeammateEvenStrengthToiSeconds)
	require.EqualValues(t, 1, teammateFive.TeammateEvenStrengthPoints)
	require.EqualValues(t, 10, teammateFive.TeammateEvenStrengthToiSeconds)
}

func cleanupLinemateFixture(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
DELETE FROM shifts WHERE game_id = $1;
DELETE FROM game_skater_stats WHERE game_id = $1;
DELETE FROM play_events WHERE game_id = $1;
DELETE FROM games WHERE id = $1;
DELETE FROM players WHERE id BETWEEN $2 + 1 AND $2 + 21;
`, testLinemateGame, testPlayerBase)
	require.NoError(t, err)
}

func seedLinemateSegments(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
INSERT INTO players (id, first_name, last_name)
SELECT $5 + id, 'Player', id::text FROM generate_series(1, 21) AS id;

INSERT INTO games (
    id, season, game_type, game_date, game_state, home_team_id, away_team_id
) VALUES ($1, $2, 'regular_season', '2026-03-01', 'FINAL', $3, $4);

INSERT INTO game_skater_stats (
    game_id, player_id, team_id, is_home, sweater_number, position, toi_seconds
)
SELECT $1, $5 + id, CASE WHEN id <= 5 THEN $3 ELSE $4 END,
       id <= 5, id::smallint, 'C', 40
FROM generate_series(1, 10) AS id;

INSERT INTO game_goalie_stats (
    game_id, player_id, team_id, is_home, sweater_number, starter, toi_seconds
) VALUES
    ($1, $5 + 20, $3, TRUE, 30, TRUE, 40),
    ($1, $5 + 21, $4, FALSE, 31, TRUE, 30);

INSERT INTO play_events (
    game_id, event_id, period, period_type, time_in_period, time_remaining,
    situation_code, type_desc_key, sort_order, scoring_player_id, assist1_player_id
) VALUES
    ($1, 1, 1, 'REG', '00:10', '19:50', 1551, 'goal', 1, $5 + 5, $5 + 2),
    ($1, 2, 1, 'REG', '00:15', '19:45', 1541, 'goal', 2, $5 + 2, NULL),
    ($1, 3, 1, 'REG', '00:35', '19:25', 1551, 'goal', 3, $5 + 2, NULL);
`, testLinemateGame, testLinemateSeason, testHomeTeam, testAwayTeam, testPlayerBase)
	require.NoError(t, err)
	seedShiftRows(t, pool)
}

func seedShiftRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
INSERT INTO shifts (
    id, game_id, player_id, team_id, period, start_time, end_time, duration,
    shift_number, type_code, detail_code, event_number
) VALUES
    ($4+101, $1, $4+1,  $2, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+102, $1, $4+2,  $2, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+103, $1, $4+3,  $2, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+104, $1, $4+4,  $2, 1, '00:00', '00:20', '00:20', 1, '517', '0', 0),
    ($4+105, $1, $4+5,  $2, 1, '00:00', '00:10', '00:10', 1, '517', '0', 0),
    ($4+106, $1, $4+6,  $3, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+107, $1, $4+7,  $3, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+108, $1, $4+8,  $3, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+109, $1, $4+9,  $3, 1, '00:00', '00:20', '00:20', 1, '517', '0', 0),
    ($4+110, $1, $4+10, $3, 1, '00:00', '00:10', '00:10', 1, '517', '0', 0),
    ($4+111, $1, $4+1,  $2, 1, '00:00', '00:40', '00:40', 2, '517', '0', 0),
    ($4+112, $1, $4+2,  $2, 1, 'bad',   '00:40', '00:40', 2, '517', '0', 0),
    ($4+113, $1, $4+20, $2, 1, '00:00', '00:40', '00:40', 1, '517', '0', 0),
    ($4+114, $1, $4+21, $3, 1, '00:00', '00:30', '00:30', 1, '517', '0', 0);
`, testLinemateGame, testHomeTeam, testAwayTeam, testPlayerBase)
	require.NoError(t, err)
}
