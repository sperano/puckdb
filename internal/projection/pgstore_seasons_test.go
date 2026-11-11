package projection

import (
	"testing"
	"time"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

func TestSeasonWindowParams(t *testing.T) {
	cutoff := dateValue(time.Date(2024, time.September, 1, 0, 0, 0, 0, time.UTC))
	projection := projectionWindow(20242025, 20202021, cutoff)
	require.Equal(t, sqlcdb.ListProjectionSkaterSeasonsParams{
		MinSeason: 20202021, TargetSeason: 20242025, IncludeTargetSeason: false, GameDateCutoff: cutoff,
	}, projection.skaterParams())
	require.Equal(t, sqlcdb.ListProjectionGoalieSeasonsParams{
		MinSeason: 20202021, TargetSeason: 20242025, IncludeTargetSeason: false, GameDateCutoff: cutoff,
		MinimumShutoutToiSeconds: DefaultGoalieShutoutMinTOI,
	}, projection.goalieParams(DefaultGoalieShutoutMinTOI))

	evaluation := evaluationWindow(20242025, 20202021)
	require.Equal(t, sqlcdb.ListProjectionSkaterSeasonsParams{
		MinSeason: 20202021, TargetSeason: 20242025, IncludeTargetSeason: true,
	}, evaluation.skaterParams())
	require.Equal(t, sqlcdb.ListProjectionGoalieSeasonsParams{
		MinSeason: 20202021, TargetSeason: 20242025, IncludeTargetSeason: true,
		MinimumShutoutToiSeconds: DefaultGoalieShutoutMinTOI,
	}, evaluation.goalieParams(DefaultGoalieShutoutMinTOI))
}

// Every row column reaches its season field: each carries a distinct value.
func TestSeasonsFromRowsMapsEveryColumn(t *testing.T) {
	birth := time.Date(1997, time.January, 13, 0, 0, 0, 0, time.UTC)
	goalieBirth := time.Date(1992, time.May, 29, 0, 0, 0, 0, time.UTC)
	skaters, goalies := seasonsFromRows(
		[]sqlcdb.ListProjectionSkaterSeasonsRow{{
			PlayerID: 8478402, BirthDate: dateValue(birth), Season: 20232024, TeamID: 22, Position: "C",
			GamesPlayed: 81, TOISeconds: 102, Goals: 32, Assists: 100, PlusMinus: -3, PenaltyMinutes: 18,
			PowerPlayPoints: 44, ShotsOnGoal: 300, Hits: 51, BlockedShots: 27, FaceoffsWon: 650, FaceoffsLost: 700,
		}},
		nil,
		[]sqlcdb.ListProjectionGoalieSeasonsRow{{
			PlayerID: 8476945, BirthDate: dateValue(goalieBirth), Season: 20232024, TeamID: 6, GamesPlayed: 60,
			GamesAppeared: 58, GamesStarted: 57, TOISeconds: 3_400, Wins: 35, Shutouts: 4,
			ShotsAgainst: 1_700, Saves: 1_560, GoalsAgainst: 140,
		}},
	)
	require.Equal(t, []SkaterSeason{{
		PlayerID: 8478402, BirthDate: birth, Season: 20232024, TeamID: 22, Position: "C",
		GamesPlayed: 81, TOISeconds: 102, Goals: 32, Assists: 100, PlusMinus: -3, PenaltyMinutes: 18,
		PowerPlayPoints: 44, ShotsOnGoal: 300, Hits: 51, BlockedShots: 27, FaceoffsWon: 650, FaceoffsLost: 700,
	}}, skaters)
	require.Equal(t, []GoalieSeason{{
		PlayerID: 8476945, BirthDate: goalieBirth, Season: 20232024, TeamID: 6, GamesPlayed: 60,
		GamesAppeared: 58, GamesStarted: 57, TOISeconds: 3_400, Wins: 35, Shutouts: 4,
		ShotsAgainst: 1_700, Saves: 1_560, GoalsAgainst: 140,
	}}, goalies)
}

func TestSeasonsFromRowsReturnsEmptySlices(t *testing.T) {
	skaters, goalies := seasonsFromRows(nil, nil, nil)
	require.NotNil(t, skaters)
	require.Empty(t, skaters)
	require.NotNil(t, goalies)
	require.Empty(t, goalies)
}
