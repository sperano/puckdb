package workflow

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// registerGameListReplayActivities registers the game list activities under
// their production names: two seasons, the older kept, the newer downloaded
// in two pages.
func registerGameListReplayActivities(t *testing.T, w worker.Worker) {
	registerReplayCommonActivities(t, w)
	activities := []struct {
		name string
		fn   any
	}{
		{"ListYahooNHLGameSeasons", func(context.Context) ([]int, error) {
			return []int{gameListKeptSeason, gameListCurrentSeason}, nil
		}},
		{"PlanYahooGamePlayerList", func(_ context.Context, in yahoo.PlanYahooGamePlayerListInput) (yahoo.YahooGamePlayerListPlan, error) {
			if in.Season == gameListKeptSeason {
				return yahoo.YahooGamePlayerListPlan{Reason: "final"}, nil
			}
			return yahoo.YahooGamePlayerListPlan{Refresh: true, GameKey: gameListCurrentGameKey}, nil
		}},
		{"FetchYahooGamePlayersPage", func(_ context.Context, in yahoo.FetchYahooGamePlayersPageInput) (yahoo.FetchYahooGamePlayersPageResult, error) {
			if in.Start == 0 {
				return yahoo.FetchYahooGamePlayersPageResult{Players: resource.GamePlayersPageSize}, nil
			}
			return yahoo.FetchYahooGamePlayersPageResult{Players: gameListShortPagePlayers}, nil
		}},
		{"CommitYahooGamePlayerList", func(context.Context, yahoo.CommitYahooGamePlayerListInput) error { return nil }},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

// TestFetchYahooPlayersGameListsReplay runs the game list download on a
// Temporal dev server and replays its history with the same code.
func TestFetchYahooPlayersGameListsReplay(t *testing.T) {
	temporalClient, stop := startSeasonReplayServer(t)
	defer stop()
	taskQueue := seasonReplayName("fetch-yahoo-players-game-lists")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflow(FetchYahooPlayersWorkflow)
	registerGameListReplayActivities(t, w)
	require.NoError(t, w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayRunTimeout)
	defer cancel()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: seasonReplayName(WorkflowIDFetchYahooPlayers), TaskQueue: taskQueue,
	}, FetchYahooPlayersWorkflow, (*FetchYahooPlayersInput)(nil))
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))
	history := readSeasonReplayHistory(t, temporalClient, run.GetID(), run.GetRunID())

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(FetchYahooPlayersWorkflow)
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, history))
}
