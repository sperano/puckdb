package nhl

import (
	"context"
	"testing"
	"time"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAggregateSeason = 2025
	testAggregateTeam   = "SJS"
	// testLastRegularSeasonDay is the last regular-season game day of the
	// fixture schedule; the playoff fixture game is later.
	testLastRegularSeasonDay = "2026-04-16"
	testPlayoffDay           = "2026-04-20"
)

var (
	// testSettledAt is comfortably after every fixture game plus the settle delay.
	testSettledAt = mustDay("2026-07-01")
	// testMidSeasonAt is the prod incident: club stats fetched on April 1 for
	// a regular season that ended April 16.
	testMidSeasonAt = mustDay("2026-04-01")
)

func mustDay(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

// seedSettledClubSchedule caches a club schedule for season/team whose games
// all finished long ago, so aggregates written now count as settled. Tests of
// the cache-hit path need it: without a schedule every aggregate is stale.
func seedSettledClubSchedule(t *testing.T, mem *store.MemStorage, season int, team string) {
	t.Helper()
	lastGame := time.Date(season+1, time.April, 16, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
	schedule := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, team, "MTL", lastGame),
	}}
	require.NoError(t, resource.WriteParsed(context.Background(), mem,
		resource.ClubScheduleSeason{Season: season, TeamAbbrev: team}, schedule))
}

// completeSchedule is a season where every game, regular and playoff, is final.
func completeSchedule() *nhlapi.TeamScheduleResponse {
	return &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, testAggregateTeam, "UTA", "2025-10-17"),
		scheduleGame(2, nhlapi.GameTypeRegularSeason, nhlapi.GameStateFinal, "UTA", testAggregateTeam, testLastRegularSeasonDay),
		scheduleGame(3, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, testAggregateTeam, "UTA", testPlayoffDay),
	}}
}

func TestSeasonAggregateStale(t *testing.T) {
	t.Parallel()

	regularSeasonOnly := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, testAggregateTeam, "UTA", "2025-10-17"),
		scheduleGame(2, nhlapi.GameTypeRegularSeason, nhlapi.GameStateFinal, "UTA", testAggregateTeam, testLastRegularSeasonDay),
	}}
	midSeason := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, testAggregateTeam, "UTA", "2025-10-17"),
		scheduleGame(2, nhlapi.GameTypeRegularSeason, nhlapi.GameStateFuture, "UTA", testAggregateTeam, testLastRegularSeasonDay),
	}}
	noDate := ""
	unparseable := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		{ID: 1, GameType: nhlapi.GameTypeRegularSeason, GameState: nhlapi.GameStateOff, GameDate: &noDate},
	}}

	tests := []struct {
		name      string
		schedule  *nhlapi.TeamScheduleResponse
		cachedAt  time.Time
		gameTypes []nhlapi.GameType
		want      bool
	}{
		{"no schedule cached is stale", nil, testSettledAt, nil, true},
		{"non-final game of the counted type is stale", midSeason, testSettledAt, []nhlapi.GameType{nhlapi.GameTypeRegularSeason}, true},
		{"non-final game of another type does not matter", midSeason, testSettledAt, []nhlapi.GameType{nhlapi.GameTypePlayoffs}, false},
		{"file predating the last game is stale", completeSchedule(), testMidSeasonAt, []nhlapi.GameType{nhlapi.GameTypeRegularSeason}, true},
		{"file inside the settle delay is stale", completeSchedule(), mustDay(testLastRegularSeasonDay).Add(aggregateSettleDelay - time.Second), []nhlapi.GameType{nhlapi.GameTypeRegularSeason}, true},
		{"file at the settle delay is fresh", completeSchedule(), mustDay(testLastRegularSeasonDay).Add(aggregateSettleDelay), []nhlapi.GameType{nhlapi.GameTypeRegularSeason}, false},
		{"regular season settled while playoffs are later", completeSchedule(), mustDay(testLastRegularSeasonDay).Add(aggregateSettleDelay), []nhlapi.GameType{nhlapi.GameTypeRegularSeason}, false},
		{"playoffs not settled by the regular-season date", completeSchedule(), mustDay(testLastRegularSeasonDay).Add(aggregateSettleDelay), []nhlapi.GameType{nhlapi.GameTypePlayoffs}, true},
		{"no game types counts every game", completeSchedule(), mustDay(testLastRegularSeasonDay).Add(aggregateSettleDelay), nil, true},
		{"no game types settled after the last game", completeSchedule(), testSettledAt, nil, false},
		{"no games of the counted type is fresh", regularSeasonOnly, testMidSeasonAt, []nhlapi.GameType{nhlapi.GameTypePlayoffs}, false},
		{"unparseable game date is stale", unparseable, testSettledAt, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, seasonAggregateStale(tt.schedule, tt.cachedAt, tt.gameTypes...))
		})
	}
}

func TestSeasonAggregateNeedsRefetch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	res := resource.ClubStatsResource{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam, GameType: nhlapi.GameTypeRegularSeason.Int()}

	t.Run("stale file needs a refetch", func(t *testing.T) {
		t.Parallel()
		mem := store.NewMemStorage()
		mem.SetFileWithTime(res.Path(), []byte("{}"), testMidSeasonAt)
		assert.True(t, seasonAggregateNeedsRefetch(ctx, mem, res, completeSchedule(), nhlapi.GameTypeRegularSeason))
	})

	t.Run("settled file is served from cache", func(t *testing.T) {
		t.Parallel()
		mem := store.NewMemStorage()
		mem.SetFileWithTime(res.Path(), []byte("{}"), testSettledAt)
		assert.False(t, seasonAggregateNeedsRefetch(ctx, mem, res, completeSchedule(), nhlapi.GameTypeRegularSeason))
	})

	t.Run("missing file is left to FetchOrCache", func(t *testing.T) {
		t.Parallel()
		mem := store.NewMemStorage()
		assert.False(t, seasonAggregateNeedsRefetch(ctx, mem, res, nil, nhlapi.GameTypeRegularSeason))
	})
}
