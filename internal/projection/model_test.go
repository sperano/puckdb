package projection

import (
	"math"
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
