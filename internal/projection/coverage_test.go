package projection

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPlayerPoolRetainsPlayerWithoutHistory(t *testing.T) {
	t.Parallel()

	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027,
		AsOf:         time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC),
		PlayerPool: []PoolPlayer{{
			PlayerKey: "yahoo:rookie", Kind: PlayerKindSkater, Position: "C",
		}},
	})
	require.NoError(t, err)
	require.Len(t, snapshot.Players, 1)
	require.True(t, snapshot.Players[0].InsufficientHistory)
	require.Empty(t, snapshot.Players[0].Values)
	require.Equal(t, supportedStats(PlayerKindSkater), snapshot.Players[0].MissingStats)
}

func TestPlayerPoolMatchesHistoryByNHLPlayerID(t *testing.T) {
	t.Parallel()

	playerID := int64(73)
	pool := []PoolPlayer{{
		PlayerKey: "yahoo:73", PlayerID: &playerID, Kind: PlayerKindSkater, Position: "LW",
	}}
	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(), PlayerPool: pool,
		Skaters: []SkaterSeason{skaterSeason(playerID, 20252026, "C", 82, 30, 40)},
	})
	require.NoError(t, err)
	require.Len(t, snapshot.Players, 1)
	require.Equal(t, "yahoo:73", snapshot.Players[0].PlayerKey)
	require.Equal(t, "LW", snapshot.Players[0].Position)
	require.Contains(t, snapshot.Players[0].Values, StatGoals)
}

func TestPlayerPoolRetainsSkaterWithGamesButMissingTOI(t *testing.T) {
	t.Parallel()

	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters: []SkaterSeason{{
			PlayerID: 71, Season: 20252026, Position: "D", GamesPlayed: 4,
		}},
	})
	require.NoError(t, err)
	require.Len(t, snapshot.Players, 1)
	require.Contains(t, snapshot.Players[0].Values, StatGamesPlayed)
	require.Contains(t, snapshot.Players[0].MissingStats, StatTOISeconds)
	require.Contains(t, snapshot.Players[0].MissingStats, StatGoals)
}

func TestValidateCoverageReportsMissingAndUnsupportedCategories(t *testing.T) {
	t.Parallel()

	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		PlayerPool: []PoolPlayer{{
			PlayerKey: "yahoo:rookie", Kind: PlayerKindSkater, Position: "C",
		}},
	})
	require.NoError(t, err)

	pool := []PoolPlayer{{PlayerKey: "yahoo:rookie", Kind: PlayerKindSkater, Position: "C"}}
	err = ValidateCoverage(snapshot, []LeagueCategory{
		{LeagueID: 1001, StatID: 1, Name: "Goals"},
		{LeagueID: 1002, StatID: 100, Name: "Faceoff Percentage"},
		{LeagueID: 1002, StatID: 999, Name: "Display", DisplayOnly: true},
	}, pool)
	var coverageErr *CoverageError
	require.ErrorAs(t, err, &coverageErr)
	require.Len(t, coverageErr.Issues, 2)
	require.Equal(t, CoverageMissingProjection, coverageErr.Issues[0].Code)
	require.Equal(t, CoverageUnsupportedCategory, coverageErr.Issues[1].Code)
	require.True(t, errors.Is(err, coverageErr))
}

func TestValidateCoverageAcceptsProjectedCategories(t *testing.T) {
	t.Parallel()

	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(),
		Skaters: []SkaterSeason{
			skaterSeason(72, 20252026, "C", 82, 30, 40),
			{PlayerID: 74, Season: 20252026, Position: "D", GamesPlayed: 2},
		},
	})
	require.NoError(t, err)
	pool := []PoolPlayer{{PlayerKey: playerKey(72), Kind: PlayerKindSkater, Position: "C"}}
	require.NoError(t, ValidateCoverage(snapshot, []LeagueCategory{
		{LeagueID: 1001, StatID: 1, Name: "Goals"},
		{LeagueID: 1001, StatID: 2, Name: "Assists"},
	}, pool))
}

func TestValidateCoverageAcceptsFaceoffCategories(t *testing.T) {
	t.Parallel()

	const faceoffPlayerID = 75
	snapshot, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(),
		Skaters: []SkaterSeason{skaterFaceoffSeason(faceoffPlayerID, 20252026, "C", 82, 900, 800)},
	})
	require.NoError(t, err)
	pool := []PoolPlayer{{PlayerKey: playerKey(faceoffPlayerID), Kind: PlayerKindSkater, Position: "C"}}
	require.NoError(t, ValidateCoverage(snapshot, []LeagueCategory{
		{LeagueID: 1001, StatID: 16, Name: "Faceoffs Won"},
		{LeagueID: 1001, StatID: 17, Name: "Faceoffs Lost"},
	}, pool))
}

func TestValidateCoverageRequiresDraftablePool(t *testing.T) {
	t.Parallel()

	err := ValidateCoverage(Snapshot{}, []LeagueCategory{{LeagueID: 1001, StatID: 1, Name: "Goals"}}, nil)
	var coverageErr *CoverageError
	require.ErrorAs(t, err, &coverageErr)
	require.Equal(t, CoverageMissingPlayerPool, coverageErr.Issues[0].Code)
}

func TestValidateCoverageDoesNotRequirePoolForDisplayOnlyStats(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateCoverage(Snapshot{}, []LeagueCategory{{
		LeagueID: 1001, StatID: 999, Name: "Display", DisplayOnly: true,
	}}, nil))
}
