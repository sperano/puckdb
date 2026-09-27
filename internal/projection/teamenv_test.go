package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	envTargetSeason = 20262027
	envLastSeason   = 20252026
	envOlderSeason  = 20242025
	envOldTeam      = 1
	envNewTeam      = 2
	envUnknownTeam  = 99
	envMover        = 7
	envSeasonGames  = 82
	// uncappedChange lets a test see the raw index ratio.
	uncappedChange = 0.99
)

// envTeams gives the old club a 0.8 and the new club a 1.2 goals index
// (unregressed), a 2400/2500 vs 2600/2500 shots index and a 0.8 vs 1.2
// power-play index.
func envTeams(season int) []TeamSeason {
	return []TeamSeason{
		{TeamID: envOldTeam, Abbrev: "OLD", Season: season, GamesPlayed: envSeasonGames, GoalsFor: 200, ShotsFor: 2400,
			PowerPlayGames: envSeasonGames, PowerPlayOpportunities: 200},
		{TeamID: envNewTeam, Abbrev: "NEW", Season: season, GamesPlayed: envSeasonGames, GoalsFor: 300, ShotsFor: 2600,
			PowerPlayGames: envSeasonGames, PowerPlayOpportunities: 300},
	}
}

func envSkater(playerID, teamID int64, season int) SkaterSeason {
	row := skaterSeason(playerID, season, "C", envSeasonGames, 20, 30)
	row.TeamID, row.PowerPlayPoints, row.Hits = teamID, 15, 50
	return row
}

func envInput(skaters []SkaterSeason, targets []PlayerTeam) Input {
	return Input{
		TargetSeason: envTargetSeason, AsOf: time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC),
		Skaters: skaters, TeamSeasons: envTeams(envLastSeason), TargetTeams: targets,
	}
}

func envConfig(prior, maxChange float64) Config {
	cfg := DefaultConfig()
	cfg.TeamEnvironmentPriorGames, cfg.TeamEnvironmentMaxChange = prior, maxChange
	return cfg
}

func generateMover(t *testing.T, cfg Config, input Input) (adjusted, unadjusted PlayerProjection) {
	t.Helper()
	snapshot, err := Generate(cfg, input)
	require.NoError(t, err)
	cfg.TeamEnvironmentMaxChange = 0
	baseline, err := Generate(cfg, input)
	require.NoError(t, err)
	key := playerKey(envMover)
	return findProjection(t, snapshot, key), findProjection(t, baseline, key)
}

func TestRegressedIndexShrinksTowardLeagueAverage(t *testing.T) {
	t.Parallel()

	require.InDelta(t, 0.8, regressedIndex(200, 82, 500, 164, 0), 1e-9)
	require.InDelta(t, 0.9, regressedIndex(200, 82, 500, 164, 82), 1e-9, "82 prior games halve the gap")
	require.Equal(t, 1.0, regressedIndex(200, 82, 0, 164, 82), "no league rate is league average")
	require.Equal(t, 1.0, regressedIndex(0, 0, 500, 164, 0), "no exposure is league average")
}

func TestGenerateScalesMoverTeamDependentStats(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)},
		[]PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}})
	adjusted, unadjusted := generateMover(t, envConfig(0, uncappedChange), input)

	const goalsFactor, shotsFactor, powerPlayFactor = 1.5, 2600.0 / 2400.0, 1.5
	require.InDelta(t, unadjusted.Value(StatGoals).Mean*goalsFactor, adjusted.Value(StatGoals).Mean, 1e-9)
	require.InDelta(t, unadjusted.Value(StatAssists).High*goalsFactor, adjusted.Value(StatAssists).High, 1e-9)
	require.InDelta(t, unadjusted.Value(StatShotsOnGoal).Mean*shotsFactor, adjusted.Value(StatShotsOnGoal).Mean, 1e-9)
	require.InDelta(t, unadjusted.Value(StatPowerPlayPoints).Mean*powerPlayFactor, adjusted.Value(StatPowerPlayPoints).Mean, 1e-9)
	require.InDelta(t, adjusted.Value(StatGoals).Mean+adjusted.Value(StatAssists).Mean, adjusted.Value(StatPoints).Mean, 1e-9)
	require.Equal(t, unadjusted.Value(StatHits), adjusted.Value(StatHits))
	require.Equal(t, unadjusted.Value(StatPlusMinus), adjusted.Value(StatPlusMinus))
	require.Equal(t, int64(envNewTeam), *adjusted.TeamID)
	expected := map[Stat]float64{
		StatGoals: goalsFactor, StatAssists: goalsFactor, StatShotsOnGoal: shotsFactor, StatPowerPlayPoints: powerPlayFactor,
	}
	env := *adjusted.TeamEnvironment
	require.Len(t, env.Factors, len(expected))
	for stat, factor := range expected {
		require.InDelta(t, factor, env.Factors[stat], 1e-9, stat)
	}
	env.Factors = nil
	require.Equal(t, TeamEnvironment{
		FromTeamID: envOldTeam, FromTeam: "OLD", ToTeamID: envNewTeam, ToTeam: "NEW", OtherClubShare: 1,
	}, env)
}

func TestGenerateCapsTeamEnvironmentChange(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envNewTeam, envLastSeason)},
		[]PlayerTeam{{PlayerID: envMover, TeamID: envOldTeam}})
	adjusted, unadjusted := generateMover(t, envConfig(0, DefaultTeamEnvironmentMaxChange), input)

	require.InDelta(t, unadjusted.Value(StatGoals).Mean*(1-DefaultTeamEnvironmentMaxChange), adjusted.Value(StatGoals).Mean, 1e-9)
	require.InDelta(t, 1-DefaultTeamEnvironmentMaxChange, adjusted.TeamEnvironment.Factors[StatPowerPlayPoints], 1e-9)
}

func TestGenerateLeavesSameClubPlayerUnadjusted(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)},
		[]PlayerTeam{{PlayerID: envMover, TeamID: envOldTeam}})
	adjusted, unadjusted := generateMover(t, envConfig(0, uncappedChange), input)

	require.Nil(t, adjusted.TeamEnvironment)
	require.Equal(t, unadjusted.Values, adjusted.Values)
}

func TestGenerateWeighsHistoryPlayedForOtherClubs(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{
		envSkater(envMover, envOldTeam, envOlderSeason),
		envSkater(envMover, envNewTeam, envLastSeason),
	}, []PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}})
	input.TeamSeasons = append(envTeams(envOlderSeason), envTeams(envLastSeason)...)
	adjusted, _ := generateMover(t, envConfig(0, uncappedChange), input)

	env := adjusted.TeamEnvironment
	require.NotNil(t, env)
	require.Equal(t, int64(envNewTeam), env.FromTeamID, "the most recent club")
	olderWeight := DefaultSeasonDecay
	require.InDelta(t, olderWeight/(1+olderWeight), env.OtherClubShare, 1e-9)
	historyIndex := (olderWeight*0.8 + 1.2) / (1 + olderWeight)
	require.InDelta(t, 1.2/historyIndex, env.Factors[StatGoals], 1e-9)
}

func TestGenerateReportsNoFactorsForSkaterWithoutTOI(t *testing.T) {
	t.Parallel()

	row := envSkater(envMover, envOldTeam, envLastSeason)
	row.TOISeconds = 0
	adjusted, unadjusted := generateMover(t, envConfig(0, uncappedChange),
		envInput([]SkaterSeason{row}, []PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}}))

	require.Nil(t, adjusted.TeamEnvironment, "no rate was scaled, so none is explained")
	require.Equal(t, unadjusted.Values, adjusted.Values)
	require.Equal(t, int64(envNewTeam), *adjusted.TeamID)
}

func TestGenerateCreditsSplitSeasonToClubsPlayedFor(t *testing.T) {
	t.Parallel()

	// 60 games for the old club, then a trade: the season row names only
	// the club the last 20 were played for.
	const beforeTrade, afterTrade = 60, 20
	row := envSkater(envMover, envNewTeam, envLastSeason)
	row.GamesPlayed = beforeTrade + afterTrade
	input := envInput([]SkaterSeason{row}, []PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}})
	input.SkaterClubSeasons = []SkaterClubSeason{
		{PlayerID: envMover, Season: envLastSeason, TeamID: envNewTeam, GamesPlayed: afterTrade},
		{PlayerID: envMover, Season: envLastSeason, TeamID: envOldTeam, GamesPlayed: beforeTrade},
	}
	adjusted, _ := generateMover(t, envConfig(0, uncappedChange), input)

	env := adjusted.TeamEnvironment
	require.NotNil(t, env, "most of the season was played for another club")
	require.Equal(t, int64(envNewTeam), env.FromTeamID)
	require.InDelta(t, 0.75, env.OtherClubShare, 1e-9)
	require.InDelta(t, 1.2/((beforeTrade*0.8+afterTrade*1.2)/(beforeTrade+afterTrade)), env.Factors[StatGoals], 1e-9)

	withoutSplit := input
	withoutSplit.SkaterClubSeasons = nil
	unsplit, _ := generateMover(t, envConfig(0, uncappedChange), withoutSplit)
	require.Nil(t, unsplit.TeamEnvironment, "without the split the season reads as all with the target club")
}

func TestGenerateTreatsUnknownClubAsLeagueAverage(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)},
		[]PlayerTeam{{PlayerID: envMover, TeamID: envUnknownTeam}})
	adjusted, _ := generateMover(t, envConfig(0, uncappedChange), input)

	require.InDelta(t, 1/0.8, adjusted.TeamEnvironment.Factors[StatGoals], 1e-9)
	require.Empty(t, adjusted.TeamEnvironment.ToTeam)
}

func TestGenerateWithoutTargetClubKeepsHistoryContext(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)}, nil)
	adjusted, unadjusted := generateMover(t, DefaultConfig(), input)

	require.Nil(t, adjusted.TeamEnvironment)
	require.Equal(t, int64(envOldTeam), *adjusted.TeamID)
	require.Equal(t, unadjusted.Values, adjusted.Values)
}

func TestGenerateIgnoresTargetSeasonTeamRows(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)},
		[]PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}})
	withFuture := input
	withFuture.TeamSeasons = append(envTeams(envTargetSeason), input.TeamSeasons...)
	withFuture.TeamSeasons[0].GoalsFor *= 10
	adjusted, _ := generateMover(t, envConfig(0, uncappedChange), input)
	future, _ := generateMover(t, envConfig(0, uncappedChange), withFuture)

	require.Equal(t, adjusted.Values, future.Values)
}

func TestSourceHashIncludesTeamEnvironmentInputs(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)}, nil)
	withTarget := input
	withTarget.TargetTeams = []PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}}
	changedTeam := input
	changedTeam.TeamSeasons = envTeams(envLastSeason)
	changedTeam.TeamSeasons[0].GoalsFor++

	require.NotEqual(t, sourceDataHash(cfg, input), sourceDataHash(cfg, withTarget))
	require.NotEqual(t, sourceDataHash(cfg, input), sourceDataHash(cfg, changedTeam))
}

// Models before nhl-baseline-v5 do not read team-environment inputs, so
// neither their output nor their source-data hashes may depend on them.
func TestPreTeamEnvironmentModelsIgnoreTeamInputs(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{envSkater(envMover, envOldTeam, envLastSeason)}, nil)
	input.TeamSeasons = nil
	withTeams := input
	withTeams.TeamSeasons = envTeams(envLastSeason)
	withTeams.TargetTeams = []PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}}
	withTeams.SkaterClubSeasons = []SkaterClubSeason{{PlayerID: envMover, Season: envLastSeason, TeamID: envOldTeam, GamesPlayed: 1}}

	for _, version := range []string{LegacyModelVersion, FaceoffModelVersion, LinemateModelVersion, AgingModelVersion} {
		cfg := versionConfig(version)
		if !supportsLinemateContext(version) {
			cfg.LinemateRegressionStrength = 0
		}
		require.Equal(t, sourceDataHash(cfg, input), sourceDataHash(cfg, withTeams), version)
		require.Equal(t, evaluationDataHash(cfg, input), evaluationDataHash(cfg, withTeams), version)
		without, err := Generate(cfg, input)
		require.NoError(t, err, version)
		with, err := Generate(cfg, withTeams)
		require.NoError(t, err, version)
		require.Equal(t, without, with, version)
	}
}

func TestConfigValidateTeamEnvironment(t *testing.T) {
	t.Parallel()

	require.NoError(t, envConfig(0, 0).Validate())
	require.ErrorContains(t, envConfig(-1, DefaultTeamEnvironmentMaxChange).Validate(), "prior games")
	require.ErrorContains(t, envConfig(DefaultTeamEnvironmentPriorGames, 1).Validate(), "max change")
	require.ErrorContains(t, envConfig(DefaultTeamEnvironmentPriorGames, -0.1).Validate(), "max change")

	v4 := versionConfig(AgingModelVersion)
	require.NoError(t, v4.Validate())
	v4.TeamEnvironmentMaxChange = DefaultTeamEnvironmentMaxChange
	require.ErrorContains(t, v4.Validate(), "does not support the team-environment adjustment")
	v4.TeamEnvironmentMaxChange, v4.TeamEnvironmentPriorGames = 0, DefaultTeamEnvironmentPriorGames
	require.ErrorContains(t, v4.Validate(), "does not support the team-environment adjustment")
}

func TestEvaluateTeamChangesScoresMoversAgainstBothComparisons(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{
		envSkater(envMover, envOldTeam, envLastSeason),
		envSkater(envMover, envNewTeam, envTargetSeason),
		envSkater(envMover+1, envOldTeam, envLastSeason),
		envSkater(envMover+1, envOldTeam, envTargetSeason),
	}, []PlayerTeam{{PlayerID: envMover, TeamID: envNewTeam}, {PlayerID: envMover + 1, TeamID: envOldTeam}})
	input.ObservedAt = input.AsOf.AddDate(1, 0, 0)

	evaluations, err := EvaluateTeamChanges(DefaultConfig(), input)
	require.NoError(t, err)
	require.Len(t, evaluations, 2)
	require.Equal(t, NoTeamEnvironmentComparison, evaluations[0].ComparisonModel)
	require.Equal(t, PreviousSeasonComparison, evaluations[1].ComparisonModel)
	for _, evaluation := range evaluations {
		require.NoError(t, validateEvaluation(evaluation))
		require.Len(t, evaluation.Metrics, len(teamDependentStats))
		require.Equal(t, 1, evaluation.Metrics[0].SampleSize, "only the mover is scored")
	}
}

func TestEvaluateTeamChangesRequiresAdjustmentAndMovers(t *testing.T) {
	t.Parallel()

	input := envInput([]SkaterSeason{
		envSkater(envMover, envOldTeam, envLastSeason),
		envSkater(envMover, envOldTeam, envTargetSeason),
	}, []PlayerTeam{{PlayerID: envMover, TeamID: envOldTeam}})
	input.ObservedAt = input.AsOf.AddDate(1, 0, 0)

	_, err := EvaluateTeamChanges(envConfig(0, 0), input)
	require.ErrorContains(t, err, "requires the team-environment adjustment")
	_, err = EvaluateTeamChanges(DefaultConfig(), input)
	require.ErrorContains(t, err, "no skaters changed teams")
}
