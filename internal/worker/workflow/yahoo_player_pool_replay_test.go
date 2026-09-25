package workflow

// Replay test for the player pool version gate: histories of the Yahoo season
// workflows recorded before the pool steps existed must replay against the
// current workflows, and new histories must record the pool steps.

import (
	"context"
	"fmt"
	"testing"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/require"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	temporalworkflow "go.temporal.io/sdk/workflow"
)

const (
	fetchYahooSeasonWorkflowType  = "FetchYahooSeasonWorkflow"
	importYahooSeasonWorkflowType = "ImportYahooSeasonWorkflow"
	replayPoolPlayers             = 3
)

// legacyFetchYahooSeasonWorkflow is FetchYahooSeasonWorkflow as it was before
// the player pool steps.
func legacyFetchYahooSeasonWorkflow(ctx temporalworkflow.Context, input YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	ctx = temporalworkflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	snapshot := shared.YahooSeasonsSnapshot{Seasons: config.YahooSeasonsMap{input.StartYear: input.Season}}
	metadata, err := fetchYahooSeasonMetadata(ctx, snapshot, input.StartYear)
	if err != nil {
		return YahooSeasonSyncResult{}, fmt.Errorf("yahoo resources for season %d are unavailable: %w", input.StartYear, err)
	}
	return YahooSeasonSyncResult{UnavailableResources: metadata.UnavailableResources}, nil
}

// legacyImportYahooSeasonWorkflow is ImportYahooSeasonWorkflow as it was
// before the player pool steps.
func legacyImportYahooSeasonWorkflow(ctx temporalworkflow.Context, input YahooSeasonWorkflowInput) (YahooSeasonSyncResult, error) {
	ctx = temporalworkflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	if _, err := importYahooLeaguesAndTeams(ctx, input.Season, input.StartYear); err != nil {
		return YahooSeasonSyncResult{}, err
	}
	unavailable, err := importYahooSeasonLeagueData(ctx, input)
	return YahooSeasonSyncResult{UnavailableResources: unavailable}, err
}

func poolStubActivities() []namedActivity {
	return []namedActivity{
		{"PlanYahooLeaguePlayerPool", func(context.Context, yahoo.PlanYahooLeaguePlayerPoolInput) (yahoo.YahooLeaguePlayerPoolPlan, error) {
			return yahoo.YahooLeaguePlayerPoolPlan{Refresh: true}, nil
		}},
		{"FetchYahooLeaguePlayersPage", func(context.Context, yahoo.FetchYahooLeaguePlayersPageInput) (yahoo.FetchYahooLeaguePlayersPageResult, error) {
			return yahoo.FetchYahooLeaguePlayersPageResult{Players: replayPoolPlayers}, nil
		}},
		{"CommitYahooLeaguePlayerPool", func(context.Context, yahoo.CommitYahooLeaguePlayerPoolInput) error { return nil }},
		{"ImportYahooLeaguePlayers", func(context.Context, yahoo.ImportYahooLeaguePlayersInput) (yahoo.ImportYahooLeaguePlayersResult, error) {
			return yahoo.ImportYahooLeaguePlayersResult{Players: replayPoolPlayers}, nil
		}},
	}
}

func startYahooSeasonReplayWorker(t *testing.T, c client.Client, taskQueue string, legacy bool) func() {
	t.Helper()
	w := worker.New(c, taskQueue, worker.Options{})
	if legacy {
		w.RegisterWorkflowWithOptions(legacyFetchYahooSeasonWorkflow, temporalworkflow.RegisterOptions{Name: fetchYahooSeasonWorkflowType})
		w.RegisterWorkflowWithOptions(legacyImportYahooSeasonWorkflow, temporalworkflow.RegisterOptions{Name: importYahooSeasonWorkflowType})
	} else {
		w.RegisterWorkflow(FetchYahooSeasonWorkflow)
		w.RegisterWorkflow(ImportYahooSeasonWorkflow)
	}
	for _, a := range append(seasonStubActivities(), poolStubActivities()...) {
		w.RegisterActivityWithOptions(a.fn, activity.RegisterOptions{Name: a.name})
	}
	require.NoError(t, w.Start())
	return w.Stop
}

func recordYahooSeasonHistories(t *testing.T, c client.Client, legacy bool) (*historypb.History, *historypb.History) {
	t.Helper()
	taskQueue := uniqueReplayName("yahoo-pool-replay-tq")
	stop := startYahooSeasonReplayWorker(t, c, taskQueue, legacy)
	defer stop()
	input := YahooSeasonWorkflowInput{
		StartYear: replaySeasonStartYear,
		Season:    config.Season{Leagues: recordedLeagues()},
	}
	fetch := runSeasonWorkflow(t, c, taskQueue, fetchYahooSeasonWorkflowType, "yahoo-pool-fetch", input)
	imp := runSeasonWorkflow(t, c, taskQueue, importYahooSeasonWorkflowType, "yahoo-pool-import", input)
	return fetch, imp
}

func TestYahooSeasonWorkflowsReplayAcrossPlayerPoolGate(t *testing.T) {
	c, stopServer := startTemporalForReplayTest(t)
	defer stopServer()

	legacyFetch, legacyImport := recordYahooSeasonHistories(t, c, true)
	requireActivityScheduledCount(t, legacyFetch, "PlanYahooLeaguePlayerPool", 0)
	currentFetch, currentImport := recordYahooSeasonHistories(t, c, false)
	requireActivityScheduledCount(t, currentFetch, "PlanYahooLeaguePlayerPool", 1)
	requireActivityScheduledCount(t, currentFetch, "FetchYahooLeaguePlayersPage", 1)
	requireActivityScheduledCount(t, currentFetch, "CommitYahooLeaguePlayerPool", 1)
	requireActivityScheduledCount(t, currentImport, "ImportYahooLeaguePlayers", 1)

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FetchYahooSeasonWorkflow)
	replayer.RegisterWorkflow(ImportYahooSeasonWorkflow)
	for _, hist := range []*historypb.History{legacyFetch, legacyImport, currentFetch, currentImport} {
		require.NoError(t, replayer.ReplayWorkflowHistory(nil, hist))
	}
}
