package nhl

import (
	"context"
	"testing"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// scheduleGame builds a ScheduleGame for homePlayoffGames tests.
func scheduleGame(id int64, gameType nhlapi.GameType, state nhlapi.GameState, home, away, date string) nhlapi.ScheduleGame {
	return nhlapi.ScheduleGame{
		ID:        nhlapi.GameID(id),
		GameType:  gameType,
		GameState: state,
		GameDate:  &date,
		HomeTeam:  nhlapi.ScheduleTeam{Abbrev: home},
		AwayTeam:  nhlapi.ScheduleTeam{Abbrev: away},
	}
}

func TestHomePlayoffGames_FiltersToFinalHomePlayoffGames(t *testing.T) {
	t.Parallel()

	schedule := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-20"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateOff, "TOR", "MTL", "2025-04-22"),
		// Away playoff game: belongs to MTL's partition, not TOR's.
		scheduleGame(3, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "MTL", "TOR", "2025-04-24"),
		// Regular-season home game: wrong game type.
		scheduleGame(4, nhlapi.GameTypeRegularSeason, nhlapi.GameStateFinal, "TOR", "MTL", "2025-01-10"),
		// Not-final playoff home game: excluded until it finishes.
		scheduleGame(5, nhlapi.GameTypePlayoffs, nhlapi.GameStateLive, "TOR", "MTL", "2025-04-26"),
	}}

	games := homePlayoffGames(schedule, "TOR")

	assert.Len(t, games, 2)
	assert.Equal(t, nhlapi.GameID(1), games[0].ID)
	assert.Equal(t, nhlapi.GameID(2), games[1].ID)
}

// TestScheduleIncomplete pins the cache-staleness predicate: a schedule with
// any non-final game must be refetched (mid-season, mid-playoffs), while a
// fully final schedule is complete and cacheable forever. This replaced the
// calendar gate on nhl.Current(), which froze mid-playoff caches once
// nhl.Current() rolled over on July 1.
func TestScheduleIncomplete(t *testing.T) {
	t.Parallel()

	complete := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, "TOR", "MTL", "2026-01-10"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2026-04-20"),
	}}
	assert.False(t, scheduleIncomplete(complete))

	midPlayoffs := &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2026-04-20"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateFuture, "TOR", "MTL", "2026-04-22"),
	}}
	assert.True(t, scheduleIncomplete(midPlayoffs))

	assert.False(t, scheduleIncomplete(&nhlapi.TeamScheduleResponse{}))
}

// TestHomePlayoffGames_PartitionsSeriesAcrossTeams pins the dedup invariant the
// per-team playoff activities rely on: both teams in a series see every game in
// their schedules, but the home-game filter assigns each game to exactly one team.
func TestHomePlayoffGames_PartitionsSeriesAcrossTeams(t *testing.T) {
	t.Parallel()

	// A five-game series in the 2-2-1 home/away format, as it appears
	// identically in both teams' club schedules.
	series := []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-20"),
		scheduleGame(2, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-22"),
		scheduleGame(3, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "MTL", "TOR", "2025-04-24"),
		scheduleGame(4, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "MTL", "TOR", "2025-04-26"),
		scheduleGame(5, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, "TOR", "MTL", "2025-04-28"),
	}
	schedule := &nhlapi.TeamScheduleResponse{Games: series}

	seen := make(map[nhlapi.GameID]int)
	for _, team := range []string{"TOR", "MTL"} {
		for _, g := range homePlayoffGames(schedule, team) {
			seen[g.ID]++
		}
	}

	assert.Len(t, seen, len(series))
	for id, count := range seen {
		assert.Equal(t, 1, count, "game %d assigned to more than one team", id)
	}
}

func TestNewPlayoffActivitiesWiresEveryQueryField(t *testing.T) {
	queries := struct{ PlayoffQueries }{}
	mem := store.NewMemStorage()
	client := &MockNHLClient{}

	got := NewPlayoffActivities(queries, PlayoffActivities{Storage: mem, NHLClient: client})

	assert.Empty(t, unwiredQueryFields(got, queries, "Storage", "NHLClient"))
	assert.Same(t, mem, got.Storage)
	assert.Same(t, client, got.NHLClient)
}

// regularSeasonOnlySchedule is a settled club schedule without playoff games,
// so FetchTeamPlayoffGames reads only the schedule.
func regularSeasonOnlySchedule(state nhlapi.GameState) *nhlapi.TeamScheduleResponse {
	return &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, state, testAggregateTeam, "UTA", testLastRegularSeasonDay),
	}}
}

func runFetchTeamPlayoffGames(t *testing.T, mem *store.MemStorage, client *MockNHLClient) FetchTeamPlayoffGamesResult {
	t.Helper()
	activities := &PlayoffActivities{Storage: mem, GobCache: newImportTestGobCache(), NHLClient: client}
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	env.RegisterActivity(activities.FetchTeamPlayoffGames)
	encoded, err := env.ExecuteActivity(activities.FetchTeamPlayoffGames,
		FetchTeamPlayoffGamesInput{Season: testAggregateSeason, TeamAbbrev: testAggregateTeam})
	require.NoError(t, err)
	var result FetchTeamPlayoffGamesResult
	require.NoError(t, encoded.Get(&result))
	return result
}

func seedClubSchedule(t *testing.T, mem *store.MemStorage, schedule *nhlapi.TeamScheduleResponse) {
	t.Helper()
	require.NoError(t, resource.WriteParsed(context.Background(), mem,
		resource.ClubScheduleSeason{Season: nhlapi.NewSeason(testAggregateSeason), TeamAbbrev: testAggregateTeam}, schedule))
}

// FetchTeamPlayoffGames counts the club schedule read under the origin it
// came from: the cache for a settled schedule, the NHL API for a missing or
// incomplete one, nothing for a team the NHL API has no schedule for.
func TestFetchTeamPlayoffGames_CountsClubScheduleOrigin(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		cached    *nhlapi.TeamScheduleResponse
		remoteErr error
		want      core.OriginCounts
	}{
		{"cached settled schedule", regularSeasonOnlySchedule(nhlapi.GameStateOff), nil,
			core.OriginCounts{core.OriginFileSystem: 1}},
		{"downloaded schedule", nil, nil, core.OriginCounts{core.OriginRemoteNHLAPI: 1}},
		{"incomplete cached schedule refetched", regularSeasonOnlySchedule(nhlapi.GameStateFuture), nil,
			core.OriginCounts{core.OriginRemoteNHLAPI: 1}},
		{"schedule not found", nil, nhlapi.ErrNotFound, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			mem := store.NewMemStorage()
			if tc.cached != nil {
				seedClubSchedule(t, mem, tc.cached)
			}
			client := new(MockNHLClient)
			if tc.cached == nil || scheduleIncomplete(tc.cached) {
				var remote *nhlapi.TeamScheduleResponse
				if tc.remoteErr == nil {
					remote = regularSeasonOnlySchedule(nhlapi.GameStateOff)
				}
				client.On("ClubScheduleSeason", mock.Anything, testAggregateTeam, nhlapi.NewSeason(testAggregateSeason)).
					Return(remote, tc.remoteErr).Once()
			}

			result := runFetchTeamPlayoffGames(t, mem, client)

			assert.Equal(t, tc.want, result.Origins)
			assert.Zero(t, result.GamesFound)
			client.AssertExpectations(t)
		})
	}
}

// testPlayoffGameID is the home playoff game of settledPlayoffSchedule.
const testPlayoffGameID = 2

// settledPlayoffSchedule is a settled club schedule with one final home
// playoff game, so FetchTeamPlayoffGames also fetches that game's files.
func settledPlayoffSchedule() *nhlapi.TeamScheduleResponse {
	return &nhlapi.TeamScheduleResponse{Games: []nhlapi.ScheduleGame{
		scheduleGame(1, nhlapi.GameTypeRegularSeason, nhlapi.GameStateOff, testAggregateTeam, "UTA", testLastRegularSeasonDay),
		scheduleGame(testPlayoffGameID, nhlapi.GameTypePlayoffs, nhlapi.GameStateFinal, testAggregateTeam, "UTA",
			"2026-04-20"),
	}}
}

// The club schedule is the only origin counted: the playoff game's files,
// downloaded here, add none.
func TestFetchTeamPlayoffGames_CountsOnlyClubScheduleOriginForPlayoffGames(t *testing.T) {
	t.Parallel()
	mem := store.NewMemStorage()
	seedClubSchedule(t, mem, settledPlayoffSchedule())
	gameID := nhlapi.GameID(testPlayoffGameID)
	client := new(MockNHLClient)
	client.On("Boxscore", mock.Anything, gameID).Return(nhlapi.FixtureBoxscore(), nil).Once()
	client.On("PlayByPlay", mock.Anything, gameID).Return(nhlapi.FixturePlayByPlay(), nil).Once()
	client.On("ShiftChart", mock.Anything, gameID).Return(nhlapi.FixtureShiftChart(), nil).Once()
	client.On("GameStory", mock.Anything, gameID).Return(nhlapi.FixtureGameStory(), nil).Once()
	client.On("SeasonSeries", mock.Anything, gameID).Return(nhlapi.FixtureSeasonSeriesMatchup(), nil).Once()

	result := runFetchTeamPlayoffGames(t, mem, client)

	assert.Equal(t, FetchTeamPlayoffGamesResult{
		GamesFound:   1,
		GamesFetched: 1,
		Origins:      core.OriginCounts{core.OriginFileSystem: 1},
	}, result)
	client.AssertExpectations(t)
}
