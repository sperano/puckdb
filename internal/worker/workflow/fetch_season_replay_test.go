package workflow

import (
	"context"
	"fmt"
	"testing"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const fetchNHLSeasonReplayName = "FetchNHLSeasonWorkflow"

// TestFetchNHLSeasonWorkflowReplaysLegacyHistory records the workflow from
// before progress saves were batched, then verifies that the current version
// preserves its per-day progress-save command sequence during replay.
func TestFetchNHLSeasonWorkflowReplaysLegacyHistory(t *testing.T) {
	previousLoader := loadYahooSeasons
	defer func() { loadYahooSeasons = previousLoader }()
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return shared.YahooSeasonsSnapshot{} }

	temporalClient, stopServer := startSeasonReplayServer(t)
	defer stopServer()
	taskQueue := seasonReplayName("fetch-nhl-season-replay")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(replayLegacyFetchNHLSeasonWorkflow,
		workflow.RegisterOptions{Name: fetchNHLSeasonReplayName})
	registerFetchNHLSeasonReplayActivities(w)
	require.NoError(t, w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayRunTimeout)
	defer cancel()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: seasonReplayName(fetchNHLSeasonReplayName), TaskQueue: taskQueue,
	}, fetchNHLSeasonReplayName, testSeasonW(2023, daysTestStart, daysTestEnd))
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))
	history := readSeasonReplayHistory(t, temporalClient, run.GetID(), run.GetRunID())

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(FetchNHLSeasonWorkflow,
		workflow.RegisterOptions{Name: fetchNHLSeasonReplayName})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, history))
}

func registerFetchNHLSeasonReplayActivities(w worker.Worker) {
	activities := []struct {
		name string
		fn   any
	}{
		{"Save", func(context.Context, string, string, []byte) error { return nil }},
		{"FetchSeasonRosters", func(context.Context, worknhl.FetchSeasonRostersInput) error { return nil }},
		{"FetchClubStats", func(context.Context, worknhl.FetchClubStatsInput) error { return nil }},
		{"FetchDay", func(context.Context, worknhl.FetchDayInput) (core.OriginCounts, error) {
			return core.OriginCounts{core.OriginFileSystem: 1}, nil
		}},
		{"ListSeasonTeams", func(context.Context, int) ([]string, error) { return []string{"TOR"}, nil }},
		{"FetchTeamPlayoffGames", func(context.Context, worknhl.FetchTeamPlayoffGamesInput) (worknhl.FetchTeamPlayoffGamesResult, error) {
			return worknhl.FetchTeamPlayoffGamesResult{GamesFetched: 1}, nil
		}},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

// replayLegacyFetchNHLSeasonWorkflow is FetchNHLSeasonWorkflow as it was
// before batched progress saves, kept verbatim to record legacy histories.
func replayLegacyFetchNHLSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)

	logger.Debug("FetchNHLSeasonWorkflow started",
		"seasonID", season.ID,
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	cfg, err := snapshotSeasonConfig(ctx)
	if err != nil {
		return nil, err
	}

	tracker, err := shared.InitTracker(ctx, NewFetchSeasonProgressReport(ctx, season))
	if err != nil {
		return nil, err
	}
	tracker.StartGroup(ctx, GroupFetchSeasonData)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	yahooCfg, _ := cfg.Yahoo.Season(season.ID.StartYear())
	teamIDs := yahooTeamIDsForSeason(yahooCfg)

	var sa *worknhl.SeasonsActivities
	rosterInput := worknhl.FetchSeasonRostersInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.FetchSeasonRosters, rosterInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("fetch season rosters: %w", err)
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.FetchClubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("fetch club stats: %w", err)
	}

	endDate := shared.EffectiveEndDate(ctx, season.StandingsEnd.Time)
	numDays := core.CountDays(season.StandingsStart.Time, endDate)
	concurrency := cfg.DayConcurrency
	dayCtx := workflow.WithActivityOptions(ctx, shared.YahooDownloadActivityOptions())
	startDate := season.StandingsStart.Time
	startSeason := season.ID.StartYear()
	counts := core.OriginCounts{}
	err = tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: numDays, Concurrency: concurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: GroupFetchSeasonData}},
		Start: func(_ workflow.Context, i int) workflow.Future {
			dayInput := worknhl.FetchDayInput{
				Day:         startDate.AddDate(0, 0, i),
				StartSeason: startSeason,
				TeamIDs:     teamIDs,
				DayIndex:    i,
				TotalDays:   numDays,
			}
			var dsa *worknhl.DailyScheduleActivities
			return workflow.ExecuteActivity(dayCtx, dsa.FetchDay, dayInput)
		},
		Handle: shared.AggregateInto[core.OriginCounts](counts),
	})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonData,
		fmt.Sprintf("Fetched %d days for %s in %s.", numDays, season.Label(), tracker.GetElapsed(ctx, GroupFetchSeasonData)))
	tracker.StartGroup(ctx, GroupFetchSeasonPlayoff)

	var pa *worknhl.PlayoffActivities
	var teams []string
	if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, startSeason).Get(ctx, &teams); err != nil {
		return nil, fmt.Errorf("list season teams: %w", err)
	}
	tracker.ResizeBar(ctx, shared.BarRef{Group: GroupFetchSeasonPlayoff}, len(teams))

	playoffGames := 0
	err = tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: len(teams), Concurrency: concurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: GroupFetchSeasonPlayoff}},
		Start: func(_ workflow.Context, i int) workflow.Future {
			input := worknhl.FetchTeamPlayoffGamesInput{Season: startSeason, TeamAbbrev: teams[i]}
			return workflow.ExecuteActivity(ctx, pa.FetchTeamPlayoffGames, input)
		},
		Handle: func(ctx workflow.Context, i int, f workflow.Future) error {
			var res worknhl.FetchTeamPlayoffGamesResult
			if err := f.Get(ctx, &res); err != nil {
				return fmt.Errorf("fetch playoff games for %s: %w", teams[i], err)
			}
			playoffGames += res.GamesFetched
			return nil
		},
	})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupFetchSeasonPlayoff,
		fmt.Sprintf("Fetched %d playoff games for %s in %s.", playoffGames, season.Label(), tracker.GetElapsed(ctx, GroupFetchSeasonPlayoff)))
	return counts, nil
}
