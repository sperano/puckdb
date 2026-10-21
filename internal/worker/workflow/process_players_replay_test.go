package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/store"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

// The ProcessPlayers chain used by the replay tests: five players, two per
// execution, one per batch, so phase 2 runs three executions.
const (
	replayPlayers        = 5
	replayPlayersPerExec = 2
	replayBatchSize      = 1
	replayConcurrency    = 2
	// replayFailingPlayer is the player whose batch reports an error, so the
	// histories cover error carrying and the failed final run.
	replayFailingPlayer = 8_400_003
	replayFirstPlayerID = 8_400_001
	replayYahooID       = 77
)

// replayBoxscorePlayers returns the chain's players.
func replayBoxscorePlayers() []store.BoxscorePlayer {
	players := make([]store.BoxscorePlayer, replayPlayers)
	for i := range players {
		id := int64(replayFirstPlayerID + i)
		players[i] = store.BoxscorePlayer{ID: id, FirstName: "First", LastName: fmt.Sprintf("Last%d", i), Position: "C"}
	}
	return players
}

// replayBatchResult is what a batch of players returns in the replay chain.
func replayBatchResult(playerIDs []int64) workplayer.ProcessPlayerBatchResult {
	result := workplayer.ProcessPlayerBatchResult{Origins: core.OriginCounts{core.OriginFileSystem: len(playerIDs)}}
	for _, id := range playerIDs {
		if id == replayFailingPlayer {
			result.Errors = append(result.Errors, fmt.Sprintf("player %d: read error: boom", id))
			continue
		}
		result.Imported++
	}
	return result
}

// registerReplayCommonActivities registers the activities both the legacy and
// the manifest chain call, under their production names, plus progress
// persistence on a private miniredis.
func registerReplayCommonActivities(t *testing.T, w worker.Worker) {
	t.Helper()
	mr := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	progress := &shared.ProgressActivities{RedisClient: redisClient}
	w.RegisterActivity(progress.Save)
	w.RegisterActivity(progress.Load)

	unmatched := []workplayer.UnmatchedYahooPlayer{{YahooID: replayYahooID, FirstName: "Un", LastName: "Matched"}}
	activities := []struct {
		name string
		fn   any
	}{
		{"ListYahooPlayerFiles", func(context.Context) ([]store.YahooPlayerID, error) {
			return []store.YahooPlayerID{replayYahooID}, nil
		}},
		{"ParseYahooPlayerBatch", func(_ context.Context, ids []store.YahooPlayerID) ([]store.YahooPlayer, error) {
			players := make([]store.YahooPlayer, len(ids))
			for i, id := range ids {
				players[i] = store.YahooPlayer{YahooID: id, FirstName: "Un", LastName: "Matched"}
			}
			return players, nil
		}},
		{"SaveYahooPlayersToRedis", func(_ context.Context, players []store.YahooPlayer) (*workplayer.SaveYahooIDPoolResult, error) {
			return &workplayer.SaveYahooIDPoolResult{TotalPlayers: len(players), AvailablePlayers: len(players)}, nil
		}},
		{"LoadUnmatchedYahooPlayers", func(context.Context) ([]workplayer.UnmatchedYahooPlayer, error) {
			return unmatched, nil
		}},
		{"VerifyUnmatchedBatch", func(_ context.Context, players []workplayer.UnmatchedYahooPlayer) (*workplayer.VerifyUnmatchedResult, error) {
			result := &workplayer.VerifyUnmatchedResult{}
			for _, p := range players {
				result.VerifiedNonNHL = append(result.VerifiedNonNHL, p.YahooID)
			}
			return result, nil
		}},
		{"CleanupYahooIDPoolData", func(context.Context) error { return nil }},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

// readProcessPlayersChain returns the history of every execution of the chain
// that started with runID, in order, following ContinueAsNew.
func readProcessPlayersChain(t *testing.T, temporalClient client.Client, workflowID, runID string) []*historypb.History {
	t.Helper()
	var chain []*historypb.History
	for runID != "" {
		history := readSeasonReplayHistory(t, temporalClient, workflowID, runID)
		chain = append(chain, history)
		runID = ""
		last := history.Events[len(history.Events)-1]
		if last.GetEventType() == enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW {
			runID = last.GetWorkflowExecutionContinuedAsNewEventAttributes().GetNewExecutionRunId()
		}
	}
	return chain
}

// processPlayersLegacyHistories are the five executions of a legacy chain
// (run 1, three phase-2 continuations, phase 3), recorded on a Temporal dev
// server with the code before the player manifest (commit b0bb2c2), with
// the activities registerReplayCommonActivities registers plus
// LoadAllBoxscorePlayers returning replayBoxscorePlayers() and
// ProcessPlayerBatch returning replayBatchResult of its players' IDs.
var processPlayersLegacyHistories = []string{
	"testdata/process_players_legacy_1.json",
	"testdata/process_players_legacy_2.json",
	"testdata/process_players_legacy_3.json",
	"testdata/process_players_legacy_4.json",
	"testdata/process_players_legacy_5.json",
}

// TestProcessPlayersReplaysLegacyHistories replays a chain recorded before
// the manifest: run 1 must keep the legacy path (no version marker), and the
// legacy continuation type must still decode and run its input.
func TestProcessPlayersReplaysLegacyHistories(t *testing.T) {
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ProcessPlayersWorkflow)
	replayer.RegisterWorkflow(ProcessPlayersWorkflowContinue)
	for _, path := range processPlayersLegacyHistories {
		require.NoError(t, replayer.ReplayWorkflowHistoryFromJSONFile(nil, path), path)
	}
}

// registerReplayManifestActivities registers the activities only the
// manifest chain calls.
func registerReplayManifestActivities(w worker.Worker) {
	players := replayBoxscorePlayers()
	manifest := workplayer.ManifestRef{ID: "replay", Count: len(players), Hash: "replay-hash"}
	activities := []struct {
		name string
		fn   any
	}{
		{"CreateProcessPlayersManifest", func(context.Context, workplayer.CreateManifestInput) (workplayer.ManifestRef, error) {
			return manifest, nil
		}},
		{"EnsureYahooIDPool", func(_ context.Context, input workplayer.EnsureYahooIDPoolInput) (workplayer.EnsureYahooIDPoolResult, error) {
			return workplayer.EnsureYahooIDPoolResult{FoundSize: input.PoolSize, PoolSize: input.PoolSize}, nil
		}},
		{"ProcessPlayerManifestBatch", func(_ context.Context, input workplayer.ManifestBatchInput) (workplayer.ManifestBatchResult, error) {
			var ids []int64
			for _, p := range players[input.From:input.To] {
				ids = append(ids, p.ID)
			}
			result := replayBatchResult(ids)
			batch := workplayer.ManifestBatchResult{ProcessPlayerBatchResult: result}
			for _, message := range result.Errors {
				batch.PlayerErrors = append(batch.PlayerErrors, workplayer.PlayerError{
					PlayerID: replayFailingPlayer, Stage: workplayer.StageRead, Message: message,
				})
			}
			batch.Errors = nil
			return batch, nil
		}},
		{"RecordProcessPlayersErrors", func(context.Context, workplayer.RecordErrorsInput) error { return nil }},
		{"FinishProcessPlayersManifest", func(context.Context, string) (int, error) { return 1, nil }},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

// TestProcessPlayersManifestChainReplays runs a manifest chain on a Temporal
// dev server and replays every execution with the same code.
func TestProcessPlayersManifestChainReplays(t *testing.T) {
	setViperInt(t, config.FlagPlayerLandingPlayersPerExec, replayPlayersPerExec)
	temporalClient, stop := startSeasonReplayServer(t)
	defer stop()
	taskQueue := seasonReplayName("process-players-manifest")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflow(ProcessPlayersWorkflow)
	w.RegisterWorkflow(ProcessPlayersWorkflowContinueV2)
	registerReplayCommonActivities(t, w)
	registerReplayManifestActivities(w)
	require.NoError(t, w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayRunTimeout)
	defer cancel()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: seasonReplayName(WorkflowIDProcessPlayers), TaskQueue: taskQueue,
	}, ProcessPlayersWorkflow, &ProcessPlayersInput{BatchSize: intPtr(replayBatchSize), Concurrency: intPtr(replayConcurrency)})
	require.NoError(t, err)
	err = temporalClient.GetWorkflow(ctx, run.GetID(), "").Get(ctx, nil)
	require.ErrorContains(t, err, "finished with 1 per-player error(s)")
	chain := readProcessPlayersChain(t, temporalClient, run.GetID(), run.GetRunID())
	require.Len(t, chain, len(processPlayersLegacyHistories), "run 1, three phase-2 executions, phase 3")

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflow(ProcessPlayersWorkflow)
	replayer.RegisterWorkflow(ProcessPlayersWorkflowContinueV2)
	for i, history := range chain {
		require.NoError(t, replayer.ReplayWorkflowHistory(nil, history), "execution %d", i+1)
	}
}
