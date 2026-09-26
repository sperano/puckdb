package workflow

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/draftranking"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const (
	draftTestSeason  = 2026
	draftTestLeagueA = 1001
	draftTestLeagueB = 1002
	draftTestPenalty = 0.5
	// draftTestActivityDelay keeps a refresh running long enough to cancel.
	draftTestActivityDelay = time.Minute
)

func newRefreshDraftEnv(t *testing.T, leagues ...int) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	withFixedNewsSources(t)
	configured := make([]config.League, 0, len(leagues))
	for _, id := range leagues {
		configured = append(configured, config.League{LeagueID: id})
	}
	withYahooSnapshot(t, shared.YahooSeasonsSnapshot{Seasons: config.YahooSeasonsMap{draftTestSeason: {Leagues: configured}}})
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflow(RefreshDraftRankingsWorkflow)
	env.SetStartTime(newsTestNow)
	mockProgressSaves(env)
	return env
}

func succeeded(leagueID int) draftrank.Refresh {
	return draftrank.Refresh{State: draftrank.RefreshSucceeded, SnapshotID: uuid.NewSHA1(uuid.NameSpaceOID, []byte{byte(leagueID)})}
}

func draftInputFor(leagueID int) any {
	return mock.MatchedBy(func(input draftranking.RefreshInput) bool {
		return input.LeagueID == leagueID && input.Season == draftTestSeason && input.Keep == config.DefaultDraftKeepSnapshots
	})
}

func TestRefreshDraftRankings_RefreshesConfiguredLeaguesAndKeepsGoingOnFailure(t *testing.T) {
	env := newRefreshDraftEnv(t, draftTestLeagueB, draftTestLeagueA)
	var act *draftranking.Activities
	env.OnActivity(act.RefreshDraftRanking, mock.Anything, draftInputFor(draftTestLeagueA)).
		Return(draftrank.Refresh{}, errors.New("database unavailable"))
	env.OnActivity(act.RefreshDraftRanking, mock.Anything, draftInputFor(draftTestLeagueB)).
		Return(succeeded(draftTestLeagueB), nil).Once()

	env.ExecuteWorkflow(RefreshDraftRankingsWorkflow, &model.RefreshDraftRankingsInput{})
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var result RefreshDraftRankingsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, draftTestSeason, result.Season)
	require.Len(t, result.Leagues, 2)
	assert.Equal(t, draftTestLeagueA, result.Leagues[0].LeagueID, "leagues run in ID order")
	assert.Contains(t, result.Leagues[0].Error, "database unavailable")
	assert.Equal(t, draftrank.RefreshSucceeded, result.Leagues[1].Refresh.State)
	report := queryProgress(t, env)
	assert.Equal(t, 2, report.Groups[0].Bars[0].Current)
}

func TestRefreshDraftRankings_NamedLeaguesAndOptions(t *testing.T) {
	env := newRefreshDraftEnv(t, draftTestLeagueA)
	var act *draftranking.Activities
	env.OnActivity(act.RefreshDraftRanking, mock.Anything, mock.MatchedBy(func(input draftranking.RefreshInput) bool {
		return input.LeagueID == draftTestLeagueB && input.Options == draftrank.Options{
			BenchPolicy: "excluded", WorkloadCapPolicy: "per-player", UncertaintyPenalty: draftTestPenalty,
		} && input.RunID != "" && len(input.Sources) == len(fixedNewsSources())
	})).Return(succeeded(draftTestLeagueB), nil).Once()

	env.ExecuteWorkflow(RefreshDraftRankingsWorkflow, &model.RefreshDraftRankingsInput{
		LeagueIds: []int{draftTestLeagueB}, BenchPolicy: new(model.DraftBenchPolicyExcluded),
		WorkloadCapPolicy: new(model.DraftWorkloadCapPolicyPerPlayer), UncertaintyPenalty: new(draftTestPenalty),
	})
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestRefreshDraftRankings_RejectsInvalidInput(t *testing.T) {
	for name, input := range map[string]*model.RefreshDraftRankingsInput{
		"negative penalty": {UncertaintyPenalty: new(-1.0)},
		"bad league":       {LeagueIds: []int{0}},
	} {
		t.Run(name, func(t *testing.T) {
			env := newRefreshDraftEnv(t, draftTestLeagueA)
			env.ExecuteWorkflow(RefreshDraftRankingsWorkflow, input)
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, env.GetWorkflowError(), &appErr)
			assert.True(t, appErr.NonRetryable())
		})
	}
	t.Run("unknown bench policy", func(t *testing.T) {
		env := newRefreshDraftEnv(t, draftTestLeagueA)
		env.ExecuteWorkflow(RefreshDraftRankingsWorkflow, &model.RefreshDraftRankingsInput{BenchPolicy: new(model.DraftBenchPolicy("SOMETIMES"))})
		assert.Error(t, env.GetWorkflowError(), "an enum outside the schema never reaches an activity")
	})
}

func TestRefreshDraftRankings_NoLeaguesCompletes(t *testing.T) {
	env := newRefreshDraftEnv(t)
	env.ExecuteWorkflow(RefreshDraftRankingsWorkflow, nil)
	require.NoError(t, env.GetWorkflowError())
	var result RefreshDraftRankingsResult
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Empty(t, result.Leagues)
}

func TestRefreshDraftRankings_CancellationMarksUnfinishedAttempts(t *testing.T) {
	env := newRefreshDraftEnv(t, draftTestLeagueA, draftTestLeagueB)
	var act *draftranking.Activities
	env.OnActivity(act.RefreshDraftRanking, mock.Anything, mock.Anything).
		After(draftTestActivityDelay).Return(succeeded(draftTestLeagueA), nil)
	env.OnActivity(act.CancelDraftRankingRefreshes, mock.Anything, mock.Anything).Return(int64(1), nil).Once()
	env.RegisterDelayedCallback(env.CancelWorkflow, draftTestActivityDelay/2)

	env.ExecuteWorkflow(RefreshDraftRankingsWorkflow, nil)
	require.True(t, env.IsWorkflowCompleted())
	assert.True(t, temporal.IsCanceledError(env.GetWorkflowError()))
	env.AssertExpectations(t)
}
