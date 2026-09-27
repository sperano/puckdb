package projection

import (
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/require"
)

func TestConfigHashChangesWithModelInputs(t *testing.T) {
	t.Parallel()

	first := DefaultConfig()
	second := first
	second.SeasonDecay = 0.5
	require.Equal(t, configHash(first), configHash(first))
	require.NotEqual(t, configHash(first), configHash(second))
}

func TestConfigHashPreservesLegacySnapshotIdentity(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ModelVersion = LegacyModelVersion
	cfg.LinemateRegressionStrength = 0
	require.Equal(t, "f65181e5f3856e50f4f71442d0aef10f2c69d37daf77f96476d1a3c81eebdcc6", configHash(cfg))
}

func TestConfigHashPreservesFaceoffSnapshotIdentity(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ModelVersion = FaceoffModelVersion
	cfg.LinemateRegressionStrength = 0
	require.Equal(t, "bdddb53fe417d350d27bc40bd6aba04188b752e5a5c74c1ae021bafab4567870", configHash(cfg))
}

func TestHistoryFloorSeason(t *testing.T) {
	t.Parallel()

	require.Equal(t, 20232024, historyFloorSeason(20262027, 3))
	require.Equal(t, 2025, seasonStartYear(20252026))
}

// TestConfig_HistoryFloorSeason checks the exported method against both the
// free function it delegates to (so they cannot drift) and seasonAge, the
// model's own window check: the floor season is the oldest one seasonAge
// accepts, and one season older than the floor is rejected.
func TestConfig_HistoryFloorSeason(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	const target = 20262027

	floor := cfg.HistoryFloorSeason(target)
	require.Equal(t, historyFloorSeason(target, cfg.LookbackSeasons), floor)

	age, ok := seasonAge(target, floor)
	require.True(t, ok)
	require.Equal(t, cfg.LookbackSeasons-1, age, "the floor season is the oldest one the model counts")

	floorStart := seasonStartYear(floor)
	beforeFloor := (floorStart-1)*10_000 + floorStart
	age, ok = seasonAge(target, beforeFloor)
	require.True(t, ok)
	require.GreaterOrEqual(t, age, cfg.LookbackSeasons, "one season older than the floor must be rejected")
}

func TestSourceHashChangesWhenHistoricalDataChanges(t *testing.T) {
	t.Parallel()

	first := Input{TargetSeason: 20262027, Skaters: []SkaterSeason{{
		PlayerID: 1, Season: 20252026, GamesPlayed: 10, TOISeconds: 10, Goals: 10,
	}}}
	second := first
	second.Skaters = []SkaterSeason{{
		PlayerID: 1, Season: 20252026, GamesPlayed: 10, TOISeconds: 10, Goals: 11,
	}}
	require.NotEqual(t, sourceDataHash(DefaultConfig(), first), sourceDataHash(DefaultConfig(), second))
}

func TestSourceHashPreservesLegacySkaterShape(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ModelVersion = LegacyModelVersion
	cfg.LinemateRegressionStrength = 0
	input := Input{TargetSeason: 20262027, Skaters: []SkaterSeason{{
		PlayerID: 1, Season: 20252026, Position: "C", GamesPlayed: 1,
		TOISeconds: 900, Goals: 1, Assists: 2,
		LinematePointsPer60: 3.5, LinemateTOISeconds: 1_800,
	}}}

	require.Equal(t, "765b917af52dfea010f88cac6269cbbbf599cdfc384c18202ca3613efeafb6af", sourceDataHash(cfg, input))
}

func TestSourceHashPreservesFaceoffSkaterShape(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.ModelVersion = FaceoffModelVersion
	cfg.LinemateRegressionStrength = 0
	input := Input{TargetSeason: 20262027, Skaters: []SkaterSeason{{
		PlayerID: 1, Season: 20252026, Position: "C", GamesPlayed: 1,
		TOISeconds: 900, Goals: 1, Assists: 2, FaceoffsWon: 8, FaceoffsLost: 7,
	}}}

	withoutContext := sourceDataHash(cfg, input)
	input.Skaters[0].LinematePointsPer60 = 3.5
	input.Skaters[0].LinemateTOISeconds = 1_800
	require.Equal(t, withoutContext, sourceDataHash(cfg, input))

	input.Skaters[0].FaceoffsWon++
	require.NotEqual(t, withoutContext, sourceDataHash(cfg, input))
}

func TestSourceHashIgnoresInputOrder(t *testing.T) {
	t.Parallel()

	first := Input{TargetSeason: 20262027, Skaters: []SkaterSeason{
		{PlayerID: 1, Season: 20252026, GamesPlayed: 1, TOISeconds: 1},
		{PlayerID: 2, Season: 20252026, GamesPlayed: 1, TOISeconds: 1},
	}}
	second := Input{TargetSeason: 20262027, Skaters: []SkaterSeason{
		{PlayerID: 2, Season: 20252026, GamesPlayed: 1, TOISeconds: 1},
		{PlayerID: 1, Season: 20252026, GamesPlayed: 1, TOISeconds: 1},
	}}
	require.Equal(t, sourceDataHash(DefaultConfig(), first), sourceDataHash(DefaultConfig(), second))
}

func TestSourceHashIgnoresRowsOutsideModelWindow(t *testing.T) {
	t.Parallel()

	first := Input{TargetSeason: 20262027, Skaters: []SkaterSeason{{PlayerID: 1, Season: 20252026, GamesPlayed: 10, TOISeconds: 10}}}
	second := first
	second.Skaters = append(slices.Clone(first.Skaters), SkaterSeason{
		PlayerID: 1, Season: 20262027, GamesPlayed: 82, TOISeconds: 82, Goals: 100,
	})
	require.Equal(t, sourceDataHash(DefaultConfig(), first), sourceDataHash(DefaultConfig(), second))
}

func TestApplyLinemateContextWeightsTeammateProductionBySharedTOI(t *testing.T) {
	t.Parallel()

	skaters := []SkaterSeason{
		{PlayerID: 1, Season: 20252026, TOISeconds: 10_000},
		{PlayerID: 2, Season: 20252026, TOISeconds: 36_000, Goals: 10, Assists: 20},
		{PlayerID: 3, Season: 20252026, TOISeconds: 36_000, Goals: 5, Assists: 5},
	}
	rows := []sqlcdb.ListProjectionSkaterLinemateContextRow{
		{PlayerID: 1, Season: 20252026, TeammateID: 2, SharedToiSeconds: 100,
			TeammateEvenStrengthPoints: 30, TeammateEvenStrengthToiSeconds: 36_000},
		{PlayerID: 1, Season: 20252026, TeammateID: 3, SharedToiSeconds: 300,
			TeammateEvenStrengthPoints: 10, TeammateEvenStrengthToiSeconds: 36_000},
	}
	applyLinemateContext(skaters, rows)

	require.Equal(t, 400, skaters[0].LinemateTOISeconds)
	require.InDelta(t, 1.5, skaters[0].LinematePointsPer60, 0.000_001)
}

func TestValidateEvaluationWindow(t *testing.T) {
	t.Parallel()

	season := sqlcdb.Season{
		StandingsStart: dateValue(time.Date(2025, time.October, 1, 0, 0, 0, 0, time.UTC)),
		StandingsEnd:   dateValue(time.Date(2026, time.April, 20, 0, 0, 0, 0, time.UTC)),
	}
	projectionAsOf := time.Date(2025, time.September, 20, 0, 0, 0, 0, time.UTC)
	observedAt := time.Date(2026, time.May, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, validateEvaluationWindow(projectionAsOf, observedAt, season))

	err := validateEvaluationWindow(season.StandingsStart.Time.Add(time.Hour), observedAt, season)
	require.EqualError(t, err, "projection as-of must not be after target season start")
	err = validateEvaluationWindow(projectionAsOf, season.StandingsEnd.Time.Add(-time.Hour), season)
	require.EqualError(t, err, "target season is incomplete at observation time")
}

func TestValidateEvaluationWindowRequiresSeasonDates(t *testing.T) {
	t.Parallel()

	err := validateEvaluationWindow(time.Now(), time.Now(), sqlcdb.Season{
		StandingsStart: pgtype.Date{}, StandingsEnd: pgtype.Date{},
	})
	require.EqualError(t, err, "target season has no evaluation window")
}
