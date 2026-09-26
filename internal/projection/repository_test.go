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
