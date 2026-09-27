package projection

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFitAgingCurveWeightsConsecutivePlayerSeasons(t *testing.T) {
	t.Parallel()
	birth := time.Date(2000, time.July, 1, 0, 0, 0, 0, time.UTC)
	input := Input{TargetSeason: 20252026, Skaters: []SkaterSeason{
		{PlayerID: 1, BirthDate: birth, Season: 20222023, Position: "LW", TOISeconds: 3600, Goals: 1},
		{PlayerID: 1, BirthDate: birth, Season: 20232024, Position: "RW", TOISeconds: 7200, Goals: 4},
		{PlayerID: 2, BirthDate: birth, Season: 20222023, Position: "F", TOISeconds: 3600, Goals: 2},
		{PlayerID: 2, BirthDate: birth, Season: 20232024, Position: "LW", TOISeconds: 3600, Goals: 2},
		{PlayerID: 3, BirthDate: birth, Season: 20212022, Position: "LW", TOISeconds: 3600, Goals: 99},
		{PlayerID: 3, BirthDate: birth, Season: 20232024, Position: "LW", TOISeconds: 3600, Goals: 99},
	}}
	curve := fitAgingCurve(input)
	step := findAgingStep(t, curve, "W", StatGoals, 22)
	require.Equal(t, 2, step.Pairs)
	require.InDelta(t, 4800.0/8400.0, step.DeltaPer60, 1e-9)
	require.InDelta(t, 8400, step.HarmonicTOISeconds, 1e-9)
	input.Skaters = append(input.Skaters, SkaterSeason{
		PlayerID: 1, BirthDate: birth, Season: 20252026, Position: "LW", TOISeconds: 3600, Goals: 100,
	})
	require.Equal(t, curve, fitAgingCurve(input), "held-out season must not enter the curve")
}

func TestFitAgingCurveIsIndependentOfInputOrder(t *testing.T) {
	t.Parallel()
	birth := time.Date(1998, time.March, 1, 0, 0, 0, 0, time.UTC)
	rows := []SkaterSeason{
		{PlayerID: 2, BirthDate: birth, Season: 20232024, Position: "D", TOISeconds: 4100, Goals: 3},
		{PlayerID: 1, BirthDate: birth, Season: 20222023, Position: "D", TOISeconds: 3700, Goals: 1},
		{PlayerID: 2, BirthDate: birth, Season: 20222023, Position: "D", TOISeconds: 3900, Goals: 1},
		{PlayerID: 1, BirthDate: birth, Season: 20232024, Position: "D", TOISeconds: 4300, Goals: 2},
	}
	first := fitAgingCurve(Input{TargetSeason: 20252026, Skaters: rows})
	slices.Reverse(rows)
	second := fitAgingCurve(Input{TargetSeason: 20252026, Skaters: rows})
	require.Equal(t, first, second)
}

func TestAgingCurveChainsFromLastHistorySeason(t *testing.T) {
	t.Parallel()
	birth := time.Date(2000, time.July, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.SkaterPriorTOISeconds = 0
	cfg.AgingCurve = &AgingCurve{
		Version: AgingCurveVersion, TrainingFloorSeason: AgingTrainingFloorSeason,
		ThroughSeason: 20232024, AgeReference: AgingAgeReference,
		Steps: []AgingStep{
			{Group: "C", Stat: StatGoals, Age: 23, DeltaPer60: 1, Pairs: 1, HarmonicTOISeconds: 3600},
			{Group: "C", Stat: StatGoals, Age: 24, DeltaPer60: 2, Pairs: 1, HarmonicTOISeconds: 3600},
		},
	}
	input := Input{TargetSeason: 20252026, AsOf: time.Date(2025, time.September, 1, 0, 0, 0, 0, time.UTC),
		Skaters: []SkaterSeason{{PlayerID: 1, BirthDate: birth, Season: 20232024, Position: "C",
			GamesPlayed: 4, TOISeconds: 3600, Goals: 1}},
	}
	snapshot, err := Generate(cfg, input)
	require.NoError(t, err)
	player := snapshot.Players[0]
	require.InDelta(t, 4, player.Value(StatGoals).Mean, 1e-9)
	require.InDelta(t, 4, player.Value(StatPoints).Mean, 1e-9)
	require.Equal(t, 4.0, player.Value(StatGamesPlayed).Mean)
	require.Equal(t, 3600.0, player.Value(StatTOISeconds).Mean)
}

func TestV4CombinesLinemateAndAgeAdjustments(t *testing.T) {
	t.Parallel()
	birth := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	strong := SkaterSeason{
		PlayerID: 1, BirthDate: birth, Season: 20232024, Position: "C",
		GamesPlayed: 82, TOISeconds: 36_000, Goals: 20, Assists: 30,
		LinematePointsPer60: 4, LinemateTOISeconds: 144_000,
	}
	average := strong
	average.PlayerID = 2
	average.LinematePointsPer60 = 2
	input := Input{TargetSeason: 20252026, AsOf: time.Date(2025, time.September, 1, 0, 0, 0, 0, time.UTC),
		Skaters: []SkaterSeason{strong, average}}
	curve := testAgingCurve(
		AgingStep{Group: "C", Stat: StatGoals, Age: 24, DeltaPer60: 1},
		AgingStep{Group: "C", Stat: StatGoals, Age: 25, DeltaPer60: 1},
	)
	v3Config := versionConfig(LinemateModelVersion)
	v3Snapshot, err := Generate(v3Config, input)
	require.NoError(t, err)
	v4Config := versionConfig(AgingModelVersion)
	v4Config.AgingCurve = curve
	v4Snapshot, err := Generate(v4Config, input)
	require.NoError(t, err)

	v3Player := findProjection(t, v3Snapshot, playerKey(strong.PlayerID))
	v4Player := findProjection(t, v4Snapshot, playerKey(strong.PlayerID))
	require.NotNil(t, v3Player.LinemateContext)
	require.NotNil(t, v4Player.LinemateContext)
	require.NotEqual(t, v3Player.Value(StatGoals), v4Player.Value(StatGoals))
	require.Equal(t, v4Player.Value(StatGoals).Mean+v4Player.Value(StatAssists).Mean,
		v4Player.Value(StatPoints).Mean)
}

func TestAgingCurveValidationRejectsMalformedSteps(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.AgingCurve = &AgingCurve{
		Version: AgingCurveVersion, TrainingFloorSeason: AgingTrainingFloorSeason,
		ThroughSeason: 20242025, AgeReference: AgingAgeReference,
		Steps: []AgingStep{{Group: "X", Stat: StatGoals, Age: 25, DeltaPer60: 1, Pairs: 1, HarmonicTOISeconds: 1}},
	}
	require.EqualError(t, cfg.Validate(), "invalid aging curve step X/goals/25")
}

func TestGenerateRejectsAgingCurveContainingTargetSeason(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.AgingCurve = testAgingCurve()
	cfg.AgingCurve.ThroughSeason = 20252026
	_, err := Generate(cfg, Input{TargetSeason: 20252026, AsOf: time.Now()})
	require.EqualError(t, err, "aging curve through season must precede target season")
}

func TestGoalieAgingKeepsDerivedRatesConsistent(t *testing.T) {
	t.Parallel()
	birth := time.Date(2000, time.July, 1, 0, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.GoaliePriorShots = 0
	cfg.AgingCurve = testAgingCurve(
		AgingStep{Group: "G", Stat: StatShotsAgainst, Age: 23, DeltaPer60: 10},
		AgingStep{Group: "G", Stat: StatSaves, Age: 23, DeltaPer60: 9},
		AgingStep{Group: "G", Stat: StatGoalsAgainst, Age: 23, DeltaPer60: 1},
	)
	input := Input{
		TargetSeason: 20242025, AsOf: time.Date(2024, time.September, 1, 0, 0, 0, 0, time.UTC),
		Goalies: []GoalieSeason{{PlayerID: 1, BirthDate: birth, Season: 20232024,
			GamesPlayed: 4, GamesAppeared: 4, GamesStarted: 4, TOISeconds: 3600,
			ShotsAgainst: 100, Saves: 90, GoalsAgainst: 10}},
	}
	snapshot, err := Generate(cfg, input)
	require.NoError(t, err)
	values := snapshot.Players[0].Values
	require.InDelta(t, 110, values[StatShotsAgainst].Mean, 1e-9)
	require.InDelta(t, 99, values[StatSaves].Mean, 1e-9)
	require.InDelta(t, 11, values[StatGoalsAgainst].Mean, 1e-9)
	require.InDelta(t, .9, values[StatSavePercentage].Mean, 1e-9)
	require.InDelta(t, 11, values[StatGoalsAgainstAvg].Mean, 1e-9)
}

func TestPreAgingConfigHashesOmitAgingCurve(t *testing.T) {
	t.Parallel()
	for _, version := range []string{LegacyModelVersion, FaceoffModelVersion} {
		cfg := versionConfig(version)
		legacy := struct {
			ModelVersion          string
			LookbackSeasons       int
			SeasonDecay           float64
			SkaterPriorTOISeconds float64
			GoaliePriorShots      float64
			GoalieShutoutMinTOI   int
			MaxGames              float64
			IntervalZ             float64
			MinimumUncertainty    float64
			MaximumUncertainty    float64
			MinimumHistoryGames   int
		}{cfg.ModelVersion, cfg.LookbackSeasons, cfg.SeasonDecay, cfg.SkaterPriorTOISeconds,
			cfg.GoaliePriorShots, cfg.GoalieShutoutMinTOI, cfg.MaxGames, cfg.IntervalZ,
			cfg.MinimumUncertainty, cfg.MaximumUncertainty, cfg.MinimumHistoryGames}
		require.Equal(t, hashValue(legacy), configHash(cfg), version)
	}
}

func findAgingStep(t *testing.T, curve *AgingCurve, group string, stat Stat, age int) AgingStep {
	t.Helper()
	for _, step := range curve.Steps {
		if step.Group == group && step.Stat == stat && step.Age == age {
			return step
		}
	}
	t.Fatalf("missing aging step %s/%s/%d", group, stat, age)
	return AgingStep{}
}

func testAgingCurve(steps ...AgingStep) *AgingCurve {
	for index := range steps {
		steps[index].Pairs = 1
		steps[index].HarmonicTOISeconds = 3600
	}
	return &AgingCurve{
		Version: AgingCurveVersion, TrainingFloorSeason: AgingTrainingFloorSeason,
		ThroughSeason: 20232024, AgeReference: AgingAgeReference, Steps: steps,
	}
}
