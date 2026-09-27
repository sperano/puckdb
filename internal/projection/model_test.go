package projection

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGenerateIsDeterministicAndExcludesFutureSeasons(t *testing.T) {
	t.Parallel()

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Date(2026, time.September, 20, 12, 0, 0, 0, time.UTC),
		Skaters: []SkaterSeason{
			skaterSeason(10, 20252026, "C", 82, 30, 50),
			skaterSeason(10, 20242025, "C", 60, 20, 40),
			skaterSeason(10, 20262027, "C", 1, 99, 99),
		},
	}

	first, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	second, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.Len(t, first.Players, 1)
	require.Less(t, first.Players[0].Value(StatGoals).Mean, 99.0)
}

func TestGenerateAggregatesTradedPlayerRows(t *testing.T) {
	t.Parallel()

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters: []SkaterSeason{
			skaterSeason(20, 20252026, "D", 40, 5, 10),
			skaterSeason(20, 20252026, "D", 30, 4, 8),
		},
	}

	snapshot, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	require.Len(t, snapshot.Players, 1)
	require.Equal(t, 70, snapshot.Players[0].HistoryGames)
	require.Equal(t, 1, snapshot.Players[0].HistorySeasons)
	require.Equal(t, float64(70), snapshot.Players[0].Value(StatGamesPlayed).Mean)
}

func TestGenerateRetainsMostRecentTeamAndPositionContext(t *testing.T) {
	t.Parallel()

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters: []SkaterSeason{
			{PlayerID: 21, TeamID: 1, Season: 20242025, Position: "D", GamesPlayed: 82, TOISeconds: 82_000},
			{PlayerID: 21, TeamID: 2, Season: 20252026, Position: "C", GamesPlayed: 70, TOISeconds: 70_000},
		},
	}

	snapshot, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	projection := snapshot.Players[0]
	require.Equal(t, int64(2), *projection.TeamID)
	require.Equal(t, "C", projection.Position)
}

func TestSmallSamplesRegressTowardPeerRate(t *testing.T) {
	t.Parallel()

	star := skaterSeason(30, 20252026, "C", 82, 40, 40)
	smallSample := skaterSeason(31, 20252026, "C", 1, 2, 0)
	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters:      []SkaterSeason{star, smallSample},
	}

	snapshot, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	projection := findProjection(t, snapshot, playerKey(31))
	require.True(t, projection.InsufficientHistory)
	require.Less(t, projection.Value(StatGoals).Mean, 2.0)
	require.Greater(t, projection.Value(StatGoals).Mean, 0.0)
}

func TestGoalieRatiosComeFromProjectedComponents(t *testing.T) {
	t.Parallel()

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Goalies: []GoalieSeason{{
			PlayerID: 40, Season: 20252026, GamesPlayed: 50, GamesStarted: 45,
			TOISeconds: 162_000, Wins: 30, Shutouts: 5,
			ShotsAgainst: 1_500, Saves: 1_380, GoalsAgainst: 120,
		}},
	}

	snapshot, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	projection := snapshot.Players[0]
	saves := projection.Value(StatSaves).Mean
	shots := projection.Value(StatShotsAgainst).Mean
	goalsAgainst := projection.Value(StatGoalsAgainst).Mean
	toi := projection.Value(StatTOISeconds).Mean
	require.InDelta(t, saves/shots, projection.Value(StatSavePercentage).Mean, 0.000_001)
	require.InDelta(t, goalsAgainst/toi*secondsPerHour, projection.Value(StatGoalsAgainstAvg).Mean, 0.000_001)
	require.LessOrEqual(t, projection.Value(StatGamesStarted).Mean, projection.Value(StatGamesPlayed).Mean)
	require.LessOrEqual(t, projection.Value(StatGamesPlayed).High, float64(DefaultMaxGames))
	require.LessOrEqual(t, projection.Value(StatGamesStarted).High, projection.Value(StatGamesPlayed).High)
	require.LessOrEqual(t, projection.Value(StatWins).High, projection.Value(StatGamesStarted).High)
	require.LessOrEqual(t, projection.Value(StatShutouts).High, projection.Value(StatGamesStarted).High)
	require.LessOrEqual(t, projection.Value(StatSavePercentage).High, 1.0)
}

func TestManualOverrideKeepsRookieVisible(t *testing.T) {
	t.Parallel()

	rookieID := int64(50)
	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Overrides: []Override{{
			PlayerKey: "nhl:50", PlayerID: &rookieID, Kind: PlayerKindSkater,
			Position: "C", Source: SourceManual,
			Values: map[Stat]Estimate{
				StatGamesPlayed: {Mean: 60, Low: 40, High: 75},
				StatGoals:       {Mean: 18, Low: 8, High: 28},
			},
		}},
	}

	snapshot, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	require.Len(t, snapshot.Players, 1)
	require.Equal(t, SourceManual, snapshot.Players[0].Source)
	require.Equal(t, 18.0, snapshot.Players[0].Value(StatGoals).Mean)
}

func TestInvalidConfigurationFailsClearly(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.SeasonDecay = 2
	_, err := Generate(cfg, Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, "season decay must be in (0, 1]")
}

func TestConfigurationRejectsNonFiniteValues(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.SeasonDecay = math.NaN()
	_, err := Generate(cfg, Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, "projection config values must be finite")
}

func TestConfigurationRejectsInvalidLinemateRegressionStrength(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.LinemateRegressionStrength = math.NaN()
	_, err := Generate(cfg, Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, "projection config values must be finite")

	cfg.LinemateRegressionStrength = 1.01
	_, err = Generate(cfg, Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, "linemate regression strength must be in [0, 1]")

	cfg = DefaultConfig()
	cfg.ModelVersion = LegacyModelVersion
	_, err = Generate(cfg, Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, "legacy model does not support linemate regression")

	cfg.ModelVersion = FaceoffModelVersion
	_, err = Generate(cfg, Input{TargetSeason: 20262027, AsOf: time.Now()})
	require.EqualError(t, err, `model "nhl-baseline-v2" does not support linemate regression`)
}

func TestLinemateContextRegressesStrongAndWeakContexts(t *testing.T) {
	t.Parallel()

	strong := skaterSeason(61, 20252026, "C", 82, 20, 30)
	strong.LinematePointsPer60 = 4
	strong.LinemateTOISeconds = strong.TOISeconds
	weak := skaterSeason(62, 20252026, "C", 82, 20, 30)
	weak.LinematePointsPer60 = 1
	weak.LinemateTOISeconds = weak.TOISeconds / 2
	defense := skaterSeason(64, 20252026, "D", 82, 20, 30)
	defense.LinematePointsPer60 = 100
	defense.LinemateTOISeconds = defense.TOISeconds

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters:      []SkaterSeason{strong, weak, defense},
	}
	adjusted, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	cfg := DefaultConfig()
	cfg.LinemateRegressionStrength = 0
	unadjusted, err := Generate(cfg, input)
	require.NoError(t, err)

	strongProjection := findProjection(t, adjusted, playerKey(strong.PlayerID))
	weakProjection := findProjection(t, adjusted, playerKey(weak.PlayerID))
	require.Less(t, strongProjection.Value(StatGoals).Mean,
		findProjection(t, unadjusted, playerKey(strong.PlayerID)).Value(StatGoals).Mean)
	require.Greater(t, weakProjection.Value(StatAssists).Mean,
		findProjection(t, unadjusted, playerKey(weak.PlayerID)).Value(StatAssists).Mean)
	require.NotNil(t, strongProjection.LinemateContext)
	require.NotNil(t, weakProjection.LinemateContext)
	require.Equal(t, float64(strong.LinemateTOISeconds), strongProjection.LinemateContext.SharedTOISeconds)
	require.Equal(t, 4.0, strongProjection.LinemateContext.ObservedPointsPer60)
	require.Equal(t, 3.0, strongProjection.LinemateContext.AveragePointsPer60)
	require.Less(t, strongProjection.LinemateContext.AdjustmentFactor, 1.0)
	require.Greater(t, weakProjection.LinemateContext.AdjustmentFactor, 1.0)
	require.Equal(t,
		strongProjection.Value(StatGoals).Mean+strongProjection.Value(StatAssists).Mean,
		strongProjection.Value(StatPoints).Mean,
	)
}

func TestLinemateAdjustmentHasNoEffectWithoutContext(t *testing.T) {
	t.Parallel()

	input := Input{
		TargetSeason: 20262027,
		AsOf:         time.Now(),
		Skaters:      []SkaterSeason{skaterSeason(63, 20252026, "C", 82, 20, 30)},
	}
	adjusted, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	cfg := DefaultConfig()
	cfg.LinemateRegressionStrength = 0
	unadjusted, err := Generate(cfg, input)
	require.NoError(t, err)

	adjustedPlayer := adjusted.Players[0]
	unadjustedPlayer := unadjusted.Players[0]
	require.Nil(t, adjustedPlayer.LinemateContext)
	require.Equal(t, unadjustedPlayer.Values, adjustedPlayer.Values)
}

func TestLinemateAdjustmentPreservesPowerPlayProduction(t *testing.T) {
	t.Parallel()

	strong := skaterSeason(65, 20252026, "C", 82, 20, 30)
	strong.PowerPlayPoints = 45
	strong.LinematePointsPer60 = 4
	strong.LinemateTOISeconds = strong.TOISeconds * evenStrengthLinemates
	average := skaterSeason(66, 20252026, "C", 82, 20, 30)
	average.PowerPlayPoints = 45
	average.LinematePointsPer60 = 2
	average.LinemateTOISeconds = average.TOISeconds * evenStrengthLinemates
	input := Input{TargetSeason: 20262027, AsOf: time.Now(), Skaters: []SkaterSeason{strong, average}}

	adjusted, err := Generate(DefaultConfig(), input)
	require.NoError(t, err)
	cfg := DefaultConfig()
	cfg.LinemateRegressionStrength = 0
	unadjusted, err := Generate(cfg, input)
	require.NoError(t, err)
	player := findProjection(t, adjusted, playerKey(strong.PlayerID))
	baseline := findProjection(t, unadjusted, playerKey(strong.PlayerID))
	require.Equal(t, baseline.Value(StatPowerPlayPoints), player.Value(StatPowerPlayPoints))
	points, powerPlay := player.Value(StatPoints), player.Value(StatPowerPlayPoints)
	require.GreaterOrEqual(t, points.Low, powerPlay.Low)
	require.GreaterOrEqual(t, points.Mean, powerPlay.Mean)
	require.GreaterOrEqual(t, points.High, powerPlay.High)
}

func TestZeroLinemateStrengthPreservesAllScoringIntervals(t *testing.T) {
	t.Parallel()

	player := skaterSeason(67, 20252026, "C", 82, 20, 30)
	player.PowerPlayPoints = 0
	player.LinematePointsPer60 = 4
	player.LinemateTOISeconds = player.TOISeconds * evenStrengthLinemates
	peer := skaterSeason(68, 20252026, "C", 82, 20, 30)
	peer.LinematePointsPer60 = 2
	peer.LinemateTOISeconds = peer.TOISeconds * evenStrengthLinemates
	input := Input{TargetSeason: 20262027, AsOf: time.Now(), Skaters: []SkaterSeason{player, peer}}

	cfg := DefaultConfig()
	cfg.LinemateRegressionStrength = 0
	withContext, err := Generate(cfg, input)
	require.NoError(t, err)
	withoutContext := input
	withoutContext.Skaters = slices.Clone(input.Skaters)
	for index := range withoutContext.Skaters {
		withoutContext.Skaters[index].LinematePointsPer60 = 0
		withoutContext.Skaters[index].LinemateTOISeconds = 0
	}
	without, err := Generate(cfg, withoutContext)
	require.NoError(t, err)

	contextPlayer := findProjection(t, withContext, playerKey(player.PlayerID))
	plainPlayer := findProjection(t, without, playerKey(player.PlayerID))
	require.Equal(t, plainPlayer.Values, contextPlayer.Values)
	require.Equal(t, plainPlayer.Value(StatPowerPlayPoints), contextPlayer.Value(StatPowerPlayPoints))
}

func TestNeutralLinemateFactorIsIdentityWhenPowerPlayEstimateExceedsPoints(t *testing.T) {
	t.Parallel()

	goals := Estimate{Mean: 1, Low: 0.5, High: 2}
	assists := Estimate{Mean: 1, Low: 0.5, High: 2}
	powerPlay := Estimate{Mean: 3, Low: 2, High: 5}
	values := map[Stat]Estimate{
		StatGoals: goals, StatAssists: assists, StatPowerPlayPoints: powerPlay,
	}

	adjustNonPowerPlayScoring(values, neutralLinemateAdjustment)

	require.Equal(t, goals, values[StatGoals])
	require.Equal(t, assists, values[StatAssists])
	require.Equal(t, powerPlay, values[StatPowerPlayPoints])
}

func TestLegacyModelIgnoresLinemateContext(t *testing.T) {
	t.Parallel()

	player := skaterSeason(69, 20252026, "C", 82, 20, 30)
	input := Input{TargetSeason: 20262027, AsOf: time.Now(), Skaters: []SkaterSeason{player}}
	cfg := DefaultConfig()
	cfg.ModelVersion = LegacyModelVersion
	cfg.LinemateRegressionStrength = 0

	without, err := Generate(cfg, input)
	require.NoError(t, err)
	input.Skaters[0].LinematePointsPer60 = 4
	input.Skaters[0].LinemateTOISeconds = input.Skaters[0].TOISeconds * evenStrengthLinemates
	with, err := Generate(cfg, input)
	require.NoError(t, err)

	require.Equal(t, without.SourceDataHash, with.SourceDataHash)
	require.Equal(t, without.Players, with.Players)
	require.NotContains(t, with.Players[0].Values, StatFaceoffsWon)
	require.NotContains(t, with.Players[0].Values, StatFaceoffsLost)
	require.NotContains(t, with.Players[0].MissingStats, StatFaceoffsWon)
	require.NotContains(t, with.Players[0].MissingStats, StatFaceoffsLost)
	require.Nil(t, with.Players[0].LinemateContext)
}

func TestFaceoffModelKeepsFaceoffsAndIgnoresLinemateContext(t *testing.T) {
	t.Parallel()

	player := skaterSeason(70, 20252026, "C", 82, 20, 30)
	player.FaceoffsWon = 500
	player.FaceoffsLost = 400
	input := Input{TargetSeason: 20262027, AsOf: time.Now(), Skaters: []SkaterSeason{player}}
	cfg := DefaultConfig()
	cfg.ModelVersion = FaceoffModelVersion
	cfg.LinemateRegressionStrength = 0

	without, err := Generate(cfg, input)
	require.NoError(t, err)
	input.Skaters[0].LinematePointsPer60 = 4
	input.Skaters[0].LinemateTOISeconds = input.Skaters[0].TOISeconds * evenStrengthLinemates
	with, err := Generate(cfg, input)
	require.NoError(t, err)

	require.Equal(t, without.SourceDataHash, with.SourceDataHash)
	require.Equal(t, without.Players, with.Players)
	require.Positive(t, with.Players[0].Value(StatFaceoffsWon).Mean)
	require.Positive(t, with.Players[0].Value(StatFaceoffsLost).Mean)
	require.Nil(t, with.Players[0].LinemateContext)
}

func TestLinemateAdjustmentRegressesSmallContextSample(t *testing.T) {
	t.Parallel()

	const (
		observed      = 4.0
		average       = 2.0
		strength      = 0.25
		exposurePrior = 36_000.0
		smallExposure = 60.0
		largeExposure = 360_000.0
		minimumFactor = 1 - strength
		neutralFactor = 1.0
	)
	small := linemateAdjustmentFactor(observed, average, strength, smallExposure, exposurePrior)
	large := linemateAdjustmentFactor(observed, average, strength, largeExposure, exposurePrior)

	require.Less(t, large, small)
	require.Less(t, small, neutralFactor)
	require.GreaterOrEqual(t, large, minimumFactor)
	require.InDelta(t, neutralFactor, small, 0.001)
}

func TestImportedOverrideRequiresSourceMetadata(t *testing.T) {
	t.Parallel()

	_, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(),
		Overrides: []Override{{
			PlayerKey: "provider:rookie", Kind: PlayerKindSkater,
			Source: SourceImported, Values: map[Stat]Estimate{StatGoals: {Mean: 10}},
		}},
	})
	require.EqualError(t, err, `imported override "provider:rookie" requires a provider`)

	override := Override{
		PlayerKey: "provider:rookie", Kind: PlayerKindSkater, Position: "C",
		Source: SourceImported, Provider: "example",
		Values: map[Stat]Estimate{StatGoals: {Mean: 10}},
	}
	_, err = Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(), Overrides: []Override{override},
	})
	require.EqualError(t, err, `imported override "provider:rookie" requires a provider version`)
}

func TestOverrideRejectsNonFiniteEstimate(t *testing.T) {
	t.Parallel()

	_, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(),
		Overrides: []Override{{
			PlayerKey: "manual:rookie", Kind: PlayerKindSkater, Source: SourceManual,
			Position: "C",
			Values:   map[Stat]Estimate{StatGoals: {Mean: math.Inf(1)}},
		}},
	})
	require.EqualError(t, err, `override "manual:rookie" stat "goals" has a non-finite estimate`)
}

func TestOverrideRejectsFutureProviderData(t *testing.T) {
	t.Parallel()

	snapshotAsOf := time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC)
	_, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: snapshotAsOf,
		Overrides: []Override{{
			PlayerKey: "provider:rookie", Kind: PlayerKindSkater, Source: SourceImported,
			Position: "C", Provider: "example", ProviderVersion: "2026.09",
			SourceAsOf: snapshotAsOf.Add(time.Hour),
			Values:     map[Stat]Estimate{StatGoals: {Mean: 10}},
		}},
	})
	require.EqualError(t, err, `override "provider:rookie" source as-of is after the snapshot cutoff`)
}

func TestOverrideRejectsUnsupportedKindStat(t *testing.T) {
	t.Parallel()

	_, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(),
		Overrides: []Override{{
			PlayerKey: "manual:skater", Kind: PlayerKindSkater, Position: "C", Source: SourceManual,
			Values: map[Stat]Estimate{StatWins: {Mean: 10}},
		}},
	})
	require.EqualError(t, err, `override "manual:skater" has unsupported skater stat "wins"`)
}

func TestPlayerPoolRejectsConflictingNHLIdentity(t *testing.T) {
	t.Parallel()

	conflictingID := int64(81)
	_, err := Generate(DefaultConfig(), Input{
		TargetSeason: 20262027, AsOf: time.Now(),
		Skaters: []SkaterSeason{skaterSeason(80, 20252026, "C", 82, 30, 40)},
		PlayerPool: []PoolPlayer{{
			PlayerKey: playerKey(80), PlayerID: &conflictingID, Kind: PlayerKindSkater, Position: "C",
		}},
	})
	require.EqualError(t, err, `pool player "nhl:80" NHL ID 81 conflicts with historical NHL ID 80`)
}

func skaterSeason(playerID int64, season int, position string, games, goals, assists int) SkaterSeason {
	return SkaterSeason{
		PlayerID: playerID, Season: season, Position: position,
		GamesPlayed: games, TOISeconds: games * typicalSkaterTOIPerGame,
		Goals: goals, Assists: assists, ShotsOnGoal: goals * 8,
	}
}

func findProjection(t *testing.T, snapshot Snapshot, key string) PlayerProjection {
	t.Helper()
	for _, projection := range snapshot.Players {
		if projection.PlayerKey == key {
			return projection
		}
	}
	t.Fatalf("projection %q not found", key)
	return PlayerProjection{}
}
