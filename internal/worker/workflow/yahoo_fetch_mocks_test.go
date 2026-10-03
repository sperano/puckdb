package workflow

import (
	"context"

	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/testsuite"
)

// unknownEndWeek is a league whose settings carry no <end_week>.
const unknownEndWeek = 0

// expectLeagueFetch mocks one league settings download reporting endWeek.
func expectLeagueFetch(env *testsuite.TestWorkflowEnvironment, season, leagueID, endWeek int) {
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchLeague, mock.Anything, season, leagueID).
		Return(yahoo.FetchLeagueResult{EndWeek: endWeek}, nil).Once()
}

// expectTeamFetches mocks one single-team download per team of the league.
func expectTeamFetches(env *testsuite.TestWorkflowEnvironment, leagueID int, teamIDs ...int) {
	var activities *yahoo.FetchActivities
	for _, teamID := range teamIDs {
		env.OnActivity(activities.FetchTeams, mock.Anything, yahoo.FetchTeamsInput{
			StartSeason: preseasonTestYear, Teams: []yahoo.TeamInfo{{LeagueID: leagueID, TeamID: teamID}},
		}).Return(nil).Once()
	}
}

// leagueDataMock describes the league-level downloads of one league:
// whether transactions and draft results are published, and the last week
// Yahoo serves (the week after it is rejected, ending the series).
type leagueDataMock struct {
	leagueID                 int
	lastWeek                 int
	noTransactions, noDrafts bool
}

// expectLeagueData mocks transactions, draft results, and every matchup week
// up to and including the rejected one.
func expectLeagueData(env *testsuite.TestWorkflowEnvironment, m leagueDataMock) {
	var activities *yahoo.FetchActivities
	input := yahoo.FetchYahooLeagueDataInput{Season: preseasonTestYear, LeagueID: m.leagueID}
	env.OnActivity(activities.FetchYahooTransactions, mock.Anything, input).
		Return(yahoo.FetchYahooLeagueResourceResult{Unavailable: m.noTransactions}, nil).Once()
	env.OnActivity(activities.FetchYahooDraftResults, mock.Anything, input).
		Return(yahoo.FetchYahooLeagueResourceResult{Unavailable: m.noDrafts}, nil).Once()
	for week := 1; week <= min(m.lastWeek+1, yahoo.MaxMatchupWeeks); week++ {
		env.OnActivity(activities.FetchYahooMatchupWeek, mock.Anything, yahoo.FetchYahooMatchupWeekInput{
			Season: preseasonTestYear, LeagueID: m.leagueID, Week: week,
		}).Return(yahoo.FetchYahooMatchupWeekResult{EndOfSeries: week > m.lastWeek}, nil).Once()
	}
}

// mockAnyLeagueData answers every league-level download for any league: all
// resources published, matchups ending after week 1.
func mockAnyLeagueData(env *testsuite.TestWorkflowEnvironment) {
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.FetchYahooTransactions, mock.Anything, mock.Anything).
		Return(yahoo.FetchYahooLeagueResourceResult{}, nil)
	env.OnActivity(activities.FetchYahooDraftResults, mock.Anything, mock.Anything).
		Return(yahoo.FetchYahooLeagueResourceResult{}, nil)
	env.OnActivity(activities.FetchYahooMatchupWeek, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input yahoo.FetchYahooMatchupWeekInput) (yahoo.FetchYahooMatchupWeekResult, error) {
			return yahoo.FetchYahooMatchupWeekResult{EndOfSeries: input.Week > 1}, nil
		})
}
