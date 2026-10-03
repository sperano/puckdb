package workflow

import (
	"bytes"
	"context"
	"encoding/gob"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

const (
	standInTestLeagueID = 2001

	mixedNoTeamsLeagueID  = 3001
	mixedStandInLeagueID  = 3002
	mixedStandInSourceID  = 3003
	mixedStandInSourceSsn = preseasonTestYear - 1

	// fetchEstimateNoTeams is an API league's initial fetch Total without
	// teams: metadata + transactions + draft results + 26 matchup calls
	// (default 25 weeks + the end probe) + 52 pool units (plan + 50 default
	// pages + commit).
	fetchEstimateNoTeams = 81
	// importUnitsNoTeams is an API league's import Total without teams:
	// metadata + league data + player pool.
	importUnitsNoTeams = 3
)

// barPoint is one bar's progress in one saved report.
type barPoint struct{ Current, Total int }

// recordBarSaves captures every bar of the child's group on every saved report.
func recordBarSaves(env *testsuite.TestWorkflowEnvironment) *[][]barPoint {
	saves := &[][]barPoint{}
	env.OnActivity(((*shared.ProgressActivities)(nil)).Save,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _, _ string, data []byte) error {
			var report shared.ProgressReport
			if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&report); err != nil {
				return err
			}
			points := make([]barPoint, len(report.Groups[0].Bars))
			for i, bar := range report.Groups[0].Bars {
				points[i] = barPoint{bar.Current, bar.Total}
			}
			*saves = append(*saves, points)
			return nil
		}).Maybe()
	return saves
}

// barHistory is one bar's distinct successive states across the saves.
func barHistory(saves [][]barPoint, bar int) []barPoint {
	var history []barPoint
	for _, save := range saves {
		point := save[bar]
		if len(history) == 0 || history[len(history)-1] != point {
			history = append(history, point)
		}
	}
	return history
}

// counting returns states from {from, total} to {to, total}, one per unit.
func counting(from, to, total int) []barPoint {
	points := make([]barPoint, 0, to-from+1)
	for current := from; current <= to; current++ {
		points = append(points, barPoint{current, total})
	}
	return points
}

func concatPoints(parts ...[]barPoint) []barPoint {
	var all []barPoint
	for _, part := range parts {
		all = append(all, part...)
	}
	return all
}

func standInLeague(leagueID int) config.League {
	return config.League{
		LeagueID:              leagueID,
		TemporaryMetadataFrom: &config.LeagueMetadataSource{Season: mixedStandInSourceSsn, LeagueID: mixedStandInSourceID},
	}
}

func TestYahooLeagueUnits(t *testing.T) {
	t.Parallel()
	twoTeams := config.League{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1, 2}}
	noTeams := config.League{LeagueID: mixedNoTeamsLeagueID}
	tests := []struct {
		name   string
		league config.League
		mode   seasonSyncMode
		want   int
	}{
		{"fetch with teams", twoTeams, seasonSyncFetch, fetchEstimateNoTeams + 2},
		{"fetch without teams", noTeams, seasonSyncFetch, fetchEstimateNoTeams},
		{"fetch stand-in", standInLeague(standInTestLeagueID), seasonSyncFetch, 1},
		{"import with teams", twoTeams, seasonSyncImport, importUnitsNoTeams + 1},
		{"import without teams", noTeams, seasonSyncImport, importUnitsNoTeams},
		{"import stand-in", standInLeague(standInTestLeagueID), seasonSyncImport, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, yahooLeagueUnits(tt.league, tt.mode))
		})
	}
}

func TestYahooMatchupCallsAndPoolPages(t *testing.T) {
	t.Parallel()
	pageSize := resource.LeaguePlayersPageSize
	assert.Equal(t, defaultYahooMatchupEndWeek+1, yahooMatchupCalls(unknownEndWeek), "unknown end week")
	assert.Equal(t, 4, yahooMatchupCalls(3), "every week plus the rejected probe")
	assert.Equal(t, yahoo.MaxMatchupWeeks, yahooMatchupCalls(yahoo.MaxMatchupWeeks), "capped at the loop bound")
	assert.Equal(t, defaultYahooPoolPages, yahooPoolPages(0), "no previous snapshot")
	assert.Equal(t, 3, yahooPoolPages(2*pageSize+5), "ends on a short page")
	assert.Equal(t, 3, yahooPoolPages(2*pageSize), "an exact multiple ends on an empty page")
	assert.Equal(t, yahoo.MaxLeaguePlayerPoolPages, yahooPoolPages(yahoo.MaxLeaguePlayerPoolPages*pageSize))
}

// mixedLeagueYahooInput mixes an API league with teams, an API league without
// teams, and a stand-in league.
func mixedLeagueYahooInput() YahooSeasonWorkflowInput {
	return YahooSeasonWorkflowInput{
		StartYear: preseasonTestYear,
		Season: config.Season{Leagues: []config.League{
			{LeagueID: preseasonTestLeagueID, TeamIDs: []int{1, 2}},
			{LeagueID: mixedNoTeamsLeagueID},
			standInLeague(mixedStandInLeagueID),
		}},
	}
}

// expectPoolPlan makes the league's pool plan ask for a download sized by a
// previous snapshot of previousPlayers.
func expectPoolPlan(env *testsuite.TestWorkflowEnvironment, leagueID, previousPlayers int) {
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.PlanYahooLeaguePlayerPool, mock.Anything,
		yahoo.PlanYahooLeaguePlayerPoolInput{Season: preseasonTestYear, LeagueID: leagueID}).
		Return(yahoo.YahooLeaguePlayerPoolPlan{Refresh: true, PreviousPlayerCount: previousPlayers}, nil).Once()
}

// expectPoolDownload mocks the pages (one per player count) and the commit
// of the first test league's pool download.
func expectPoolDownload(env *testsuite.TestWorkflowEnvironment, pagePlayers []int) {
	for page, players := range pagePlayers {
		expectPoolPage(env, page*resource.LeaguePlayersPageSize, players)
	}
	var activities *yahoo.FetchActivities
	env.OnActivity(activities.CommitYahooLeaguePlayerPool, mock.Anything, mock.Anything).Return(nil).Once()
}

func TestFetchYahooSeasonWorkflow_PerLeagueBarsAdvancePerDownload(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(FetchYahooSeasonWorkflow)
	saves := recordBarSaves(env)
	pageSize := resource.LeaguePlayersPageSize
	// League 1001: end week 3 (4 matchup calls), pool estimated at 3 pages
	// from a 55-player snapshot and delivered in 3.
	expectLeagueFetch(env, preseasonTestYear, preseasonTestLeagueID, 3)
	expectTeamFetches(env, preseasonTestLeagueID, 1, 2)
	expectLeagueData(env, leagueDataMock{leagueID: preseasonTestLeagueID, lastWeek: 3})
	expectPoolPlan(env, preseasonTestLeagueID, 2*pageSize+5)
	expectPoolDownload(env, []int{pageSize, pageSize, 5})
	// League 3001: no end week, no teams, matchups end after week 1, pool up to date.
	expectLeagueFetch(env, preseasonTestYear, mixedNoTeamsLeagueID, unknownEndWeek)
	expectLeagueData(env, leagueDataMock{leagueID: mixedNoTeamsLeagueID, lastWeek: 1})
	expectPoolUpToDate(env, mixedNoTeamsLeagueID)
	// League 3002: stand-in, one source settings download.
	expectLeagueFetch(env, mixedStandInSourceSsn, mixedStandInSourceID, unknownEndWeek)

	env.ExecuteWorkflow(FetchYahooSeasonWorkflow, mixedLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
	// 83 = 81 + 2 teams; end week 3 shrinks 26 matchup calls to 4 (61); the
	// plan sizes the pool at 1 + 3 + 1 instead of 52 (14).
	assert.Equal(t, concatPoints(
		counting(0, 1, 83), counting(1, 10, 61), counting(10, 14, 14),
	), barHistory(*saves, 0), "league 1001")
	// Matchups stop after 2 calls of 26 (57); the up-to-date pool keeps only its plan (6).
	assert.Equal(t, concatPoints(
		counting(0, 5, fetchEstimateNoTeams), counting(5, 6, 57), []barPoint{{6, 6}},
	), barHistory(*saves, 1), "league 3001")
	assert.Equal(t, counting(0, 1, 1), barHistory(*saves, 2), "stand-in league 3002")

	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []int{14, 6, 1}, result.LeagueTotals)
	report := queryProgress(t, env)
	assert.Equal(t, 14+6+1, report.Total)
	assert.Equal(t, []string{"1001", "3001", "3002"}, barLabels(report.Groups[0].Bars))
}

func barLabels(bars []shared.ProgressBar) []string {
	labels := make([]string, len(bars))
	for i, bar := range bars {
		labels[i] = bar.Label
	}
	return labels
}

func TestImportYahooSeasonWorkflow_PerLeagueBars(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(ImportYahooSeasonWorkflow)
	saves := recordBarSaves(env)
	var activities *yahoo.ImportActivities
	env.OnActivity(activities.ImportYahooStandInLeague, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeague, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueResult{}, nil).Twice()
	env.OnActivity(activities.ImportYahooTeams, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooTeamsResult{}, nil).Once()
	env.OnActivity(activities.ImportYahooLeagueData, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeagueDataResult{}, nil).Twice()
	env.OnActivity(activities.ImportYahooLeaguePlayers, mock.Anything, mock.Anything).
		Return(yahoo.ImportYahooLeaguePlayersResult{}, nil).Twice()

	env.ExecuteWorkflow(ImportYahooSeasonWorkflow, mixedLeagueYahooInput())

	require.NoError(t, env.GetWorkflowError())
	withTeams := importUnitsNoTeams + 1
	assert.Equal(t, counting(0, withTeams, withTeams), barHistory(*saves, 0),
		"league, its share of the teams batch, league data, pool")
	assert.Equal(t, counting(0, importUnitsNoTeams, importUnitsNoTeams), barHistory(*saves, 1))
	assert.Equal(t, counting(0, 1, 1), barHistory(*saves, 2))
	var result YahooSeasonSyncResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, []int{withTeams, importUnitsNoTeams, 1}, result.LeagueTotals)
	report := queryProgress(t, env)
	assert.Equal(t, "Importing Yahoo 2026 metadata...", report.Groups[0].Header)
	assert.Contains(t, report.Groups[0].CompletedMsg, "Done in")
}
