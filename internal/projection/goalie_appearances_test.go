package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	benchTargetSeason = 20262027
	benchLastSeason   = 20252026
	benchGoalieID     = 8_101
	benchOnlyGoalieID = 8_102
	benchOldClub      = 11
	benchNewClub      = 12
)

func benchConfig(version string) Config {
	cfg := versionConfig(version)
	cfg.GoaliePriorShots = 0
	return cfg
}

func benchInput(goalies ...GoalieSeason) Input {
	return Input{
		TargetSeason: benchTargetSeason, AsOf: time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC),
		ObservedAt: time.Date(2027, time.May, 1, 0, 0, 0, 0, time.UTC), Goalies: goalies,
	}
}

func TestV6GoalieWorkloadComesFromAppearances(t *testing.T) {
	t.Parallel()

	// Dressed for 77 games, played 43 (42 starts): Dobes, 2025-26.
	row := pinGoalie(benchGoalieID, time.Time{}, benchLastSeason, 77, 43, 42)
	input := benchInput(row)

	v6 := findProjection(t, mustGenerate(t, benchConfig(ModelVersion), input), playerKey(benchGoalieID))
	require.InDelta(t, 43, v6.Value(StatGamesPlayed).Mean, 1e-9)
	require.InDelta(t, 42, v6.Value(StatGamesStarted).Mean, 1e-9)
	require.Equal(t, 43, v6.HistoryGames)
	require.InDelta(t, float64(row.ShotsAgainst)/43, v6.Value(StatShotsAgainst).Mean/v6.Value(StatGamesPlayed).Mean, 1e-9)
	require.InDelta(t, float64(row.TOISeconds)/43, v6.Value(StatTOISeconds).Mean/v6.Value(StatGamesPlayed).Mean, 1e-9)

	v5 := findProjection(t, mustGenerate(t, benchConfig(TeamEnvironmentModelVersion), input), playerKey(benchGoalieID))
	require.InDelta(t, 77, v5.Value(StatGamesPlayed).Mean, 1e-9)
	require.Equal(t, 77, v5.HistoryGames)
	require.InDelta(t, float64(row.ShotsAgainst)/77, v5.Value(StatShotsAgainst).Mean/v5.Value(StatGamesPlayed).Mean, 1e-9)
}

// Regressing toward the peer rate weighs a goalie's own games against the
// prior; counting bench games gave a backup's diluted rate the weight of games
// he never played, pulling every peer's projection toward it.
func TestV6PeerRatesIgnoreBenchGames(t *testing.T) {
	t.Parallel()

	starter := pinGoalie(benchGoalieID, time.Time{}, benchLastSeason, 60, 60, 60)
	backup := pinGoalie(benchOnlyGoalieID, time.Time{}, benchLastSeason, 70, 20, 20)
	input := benchInput(starter, backup)
	perGame := func(version string) float64 {
		cfg := versionConfig(version)
		projected := findProjection(t, mustGenerate(t, cfg, input), playerKey(benchOnlyGoalieID))
		return projected.Value(StatShotsAgainst).Mean / projected.Value(StatGamesPlayed).Mean
	}
	const shotsPerAppearance = 28
	require.InDelta(t, shotsPerAppearance, perGame(ModelVersion), 1e-9)
	require.Less(t, perGame(TeamEnvironmentModelVersion), float64(shotsPerAppearance)/2)
}

func TestV6SkipsSeasonsWithoutAppearances(t *testing.T) {
	t.Parallel()

	input := benchInput(pinGoalie(benchOnlyGoalieID, time.Time{}, benchLastSeason, 12, 0, 0))

	v6 := mustGenerate(t, benchConfig(ModelVersion), input)
	require.Empty(t, v6.Players, "a goalie who only sat on the bench has no history")

	v5 := mustGenerate(t, benchConfig(TeamEnvironmentModelVersion), input)
	require.Len(t, v5.Players, 1)
}

// A goalie traded mid-season has one row per club at the same age; the
// context club is the one he played more games for.
func TestV6GoalieContextClubUsesAppearances(t *testing.T) {
	t.Parallel()

	sat := pinGoalie(benchGoalieID, time.Time{}, benchLastSeason, 40, 5, 5)
	sat.TeamID = benchOldClub
	played := pinGoalie(benchGoalieID, time.Time{}, benchLastSeason, 20, 18, 18)
	played.TeamID = benchNewClub
	input := benchInput(sat, played)

	v6 := findProjection(t, mustGenerate(t, benchConfig(ModelVersion), input), playerKey(benchGoalieID))
	require.Equal(t, int64(benchNewClub), *v6.TeamID)
	v5 := findProjection(t, mustGenerate(t, benchConfig(TeamEnvironmentModelVersion), input), playerKey(benchGoalieID))
	require.Equal(t, int64(benchOldClub), *v5.TeamID)
}

func TestV6EvaluatesGoalieGamesAsAppearances(t *testing.T) {
	t.Parallel()

	input := benchInput(
		pinGoalie(benchGoalieID, time.Time{}, benchLastSeason, 60, 40, 38),
		pinGoalie(benchGoalieID, time.Time{}, benchTargetSeason, 70, 45, 44),
	)
	gamesMAE := func(version string) float64 {
		reports, err := Evaluate(benchConfig(version), input)
		require.NoError(t, err)
		require.Len(t, reports, 1)
		return findMetric(t, reports[0], StatGamesPlayed).ModelMAE
	}
	require.InDelta(t, 45-40, gamesMAE(ModelVersion), 1e-9)
	require.InDelta(t, 70-60, gamesMAE(TeamEnvironmentModelVersion), 1e-9)
}

func TestGoalieSourceHashCoversAppearancesFromV6Only(t *testing.T) {
	t.Parallel()

	row := pinGoalie(benchGoalieID, time.Time{}, benchLastSeason, 60, 40, 38)
	changed := row
	changed.GamesAppeared++
	for _, version := range []string{AgingModelVersion, TeamEnvironmentModelVersion, ModelVersion} {
		cfg := versionConfig(version)
		same := sourceDataHash(cfg, benchInput(row)) == sourceDataHash(cfg, benchInput(changed))
		sameEvaluation := evaluationDataHash(cfg, benchInput(row)) == evaluationDataHash(cfg, benchInput(changed))
		require.Equal(t, !countsGoalieAppearances(version), same, version)
		require.Equal(t, !countsGoalieAppearances(version), sameEvaluation, version)
	}
}

func TestV6ConfigBuildsOnV5(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	require.Equal(t, "nhl-baseline-v6", cfg.ModelVersion)
	require.NoError(t, cfg.Validate())
	cfg.AgingCurve = testAgingCurve(AgingStep{Group: agingGroupGoalie, Stat: StatSaves, Age: 25, DeltaPer60: 1})
	require.NoError(t, cfg.Validate())
	for _, supports := range []func(string) bool{
		supportsFaceoffs, supportsLinemateContext, supportsAgingCurve, supportsTeamEnvironment,
	} {
		require.True(t, supports(ModelVersion))
		require.True(t, supports(TeamEnvironmentModelVersion))
	}
	require.True(t, countsGoalieAppearances(ModelVersion))
	for version := range publishedPins {
		require.False(t, countsGoalieAppearances(version), version)
		cfg := versionConfig(version)
		if !supportsLinemateContext(version) {
			cfg.LinemateRegressionStrength = 0
		}
		require.NoError(t, cfg.Validate(), version)
	}
	require.NotEqual(t, configHash(versionConfig(TeamEnvironmentModelVersion)), configHash(DefaultConfig()))
}

func mustGenerate(t *testing.T, cfg Config, input Input) Snapshot {
	t.Helper()
	snapshot, err := Generate(cfg, input)
	require.NoError(t, err)
	return snapshot
}
