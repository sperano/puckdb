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

const importNHLSeasonReplayName = "ImportNHLSeasonWorkflow"

// TestImportNHLSeasonWorkflowReplaysLegacyHistory records the workflow from
// before days could be skipped or progress saves were batched (one argument,
// ImportDay and one progress save for every day). The current version gates
// must preserve both command sequences when replaying it.
func TestImportNHLSeasonWorkflowReplaysLegacyHistory(t *testing.T) {
	previousLoader := loadYahooSeasons
	defer func() { loadYahooSeasons = previousLoader }()
	loadYahooSeasons = func() shared.YahooSeasonsSnapshot { return shared.YahooSeasonsSnapshot{} }

	temporalClient, stopServer := startSeasonReplayServer(t)
	defer stopServer()
	taskQueue := seasonReplayName("import-nhl-season-replay")
	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflowWithOptions(replayLegacyImportNHLSeasonWorkflow,
		workflow.RegisterOptions{Name: importNHLSeasonReplayName})
	registerImportNHLSeasonReplayActivities(w)
	require.NoError(t, w.Start())
	defer w.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), seasonReplayRunTimeout)
	defer cancel()
	run, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID: seasonReplayName(importNHLSeasonReplayName), TaskQueue: taskQueue,
	}, importNHLSeasonReplayName, testSeasonW(2023, daysTestStart, daysTestEnd))
	require.NoError(t, err)
	require.NoError(t, run.Get(ctx, nil))
	history := readSeasonReplayHistory(t, temporalClient, run.GetID(), run.GetRunID())

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(ImportNHLSeasonWorkflow,
		workflow.RegisterOptions{Name: importNHLSeasonReplayName})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, history))
}

func registerImportNHLSeasonReplayActivities(w worker.Worker) {
	activities := []struct {
		name string
		fn   any
	}{
		{"Save", func(context.Context, string, string, []byte) error { return nil }},
		{"ImportSeasonRosters", func(context.Context, worknhl.FetchSeasonRostersInput) error { return nil }},
		{"ImportClubStats", func(context.Context, worknhl.FetchClubStatsInput) error { return nil }},
		{"ImportDay", func(context.Context, worknhl.ImportDayInput) (core.OriginCounts, error) {
			return core.OriginCounts{core.OriginFileSystem: 1}, nil
		}},
		{"ListSeasonTeams", func(context.Context, int) ([]string, error) { return []string{"TOR"}, nil }},
		{"ImportTeamPlayoffGames", func(context.Context, worknhl.ImportTeamPlayoffGamesInput) (worknhl.ImportTeamPlayoffGamesResult, error) {
			return worknhl.ImportTeamPlayoffGamesResult{Origins: core.OriginCounts{}}, nil
		}},
	}
	for _, item := range activities {
		w.RegisterActivityWithOptions(item.fn, activity.RegisterOptions{Name: item.name})
	}
}

// replayLegacyImportNHLSeasonWorkflow is ImportNHLSeasonWorkflow as it was
// before ImportDayIfChanged and batched progress saves, kept verbatim to
// record legacy histories.
func replayLegacyImportNHLSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)

	logger.Info("ImportNHLSeasonWorkflow started",
		"startYear", season.ID.StartYear(),
		"startDate", season.StandingsStart.Format(config.DateFormat),
		"endDate", season.StandingsEnd.Format(config.DateFormat))

	cfg, err := snapshotSeasonConfig(ctx)
	if err != nil {
		return nil, err
	}

	tracker, err := shared.InitTracker(ctx, NewImportSeasonProgressReport(ctx, season))
	if err != nil {
		return nil, err
	}

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())

	// Per-day Yahoo team IDs, drawn from the season's snapshotted Yahoo
	// config. No Yahoo activities run here: league/team metadata import is
	// ImportYahooSeasonWorkflow's job, and must complete before this
	// season's day loop starts (day-level roster rows FK to yahoo_teams).
	yahooCfg, _ := cfg.Yahoo.Season(season.ID.StartYear())
	teamIDs := yahooTeamIDsForSeason(yahooCfg)

	// Import season-level data (rosters, club stats)
	var sa *worknhl.SeasonsActivities
	var ia *worknhl.ImportActivities
	rosterInput := worknhl.FetchSeasonRostersInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.ImportSeasonRosters, rosterInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("import season rosters: %w", err)
	}
	clubStatsInput := worknhl.FetchClubStatsInput{Season: season.ID.StartYear()}
	if err := workflow.ExecuteActivity(ctx, sa.ImportClubStats, clubStatsInput).Get(ctx, nil); err != nil {
		return nil, fmt.Errorf("import club stats: %w", err)
	}

	// --- Import days ---
	tracker.StartGroup(ctx, GroupImportDays)

	endDate := shared.EffectiveEndDate(ctx, season.StandingsEnd.Time)
	numDays := core.CountDays(season.StandingsStart.Time, endDate)
	dayConcurrency := cfg.DayConcurrency
	startDate := season.StandingsStart.Time
	startYear := season.ID.StartYear()
	seasonID := season.ID.ID()

	counts := core.OriginCounts{}
	err = tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: numDays, Concurrency: dayConcurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: GroupImportDays}},
		Start: func(_ workflow.Context, i int) workflow.Future {
			day := startDate.AddDate(0, 0, i)
			input := worknhl.ImportDayInput{
				Date:      day,
				Season:    startYear,
				SeasonID:  seasonID,
				TeamIDs:   teamIDs,
				TotalDays: numDays,
			}
			return workflow.ExecuteActivity(ctx, ia.ImportDay, input)
		},
		Handle: shared.AggregateInto[core.OriginCounts](counts),
	})
	if err != nil {
		return nil, err
	}

	tracker.CompleteGroup(ctx, GroupImportDays,
		fmt.Sprintf("Imported %d days for %s in %s.", numDays, season.Label(), tracker.GetElapsed(ctx, GroupImportDays)))

	// Import playoff games after regular-season day loop: one activity per team,
	// each covering the team's home playoff games, so the bar ticks per team.
	tracker.StartGroup(ctx, GroupImportPlayoff)

	var pa *worknhl.PlayoffActivities
	var teams []string
	if err := workflow.ExecuteActivity(ctx, pa.ListSeasonTeams, startYear).Get(ctx, &teams); err != nil {
		return nil, fmt.Errorf("list season teams: %w", err)
	}
	tracker.SetBarTotal(shared.BarRef{Group: GroupImportPlayoff}, len(teams))
	tracker.RecalcTotal()
	tracker.Save(ctx)

	playoffTotals := worknhl.ImportTeamPlayoffGamesResult{Origins: core.OriginCounts{}}
	err = tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: len(teams), Concurrency: dayConcurrency,
		Progress: shared.SingleBar{Bar: shared.BarRef{Group: GroupImportPlayoff}},
		Start: func(_ workflow.Context, i int) workflow.Future {
			input := worknhl.ImportTeamPlayoffGamesInput{Season: startYear, TeamAbbrev: teams[i]}
			return workflow.ExecuteActivity(ctx, pa.ImportTeamPlayoffGames, input)
		},
		Handle: func(ctx workflow.Context, i int, f workflow.Future) error {
			var res worknhl.ImportTeamPlayoffGamesResult
			if err := f.Get(ctx, &res); err != nil {
				return fmt.Errorf("import playoff games for %s: %w", teams[i], err)
			}
			playoffTotals.Origins.Add(res.Origins)
			playoffTotals.GamesImported += res.GamesImported
			playoffTotals.SkatersImported += res.SkatersImported
			playoffTotals.GoaliesImported += res.GoaliesImported
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	counts.Add(playoffTotals.Origins)

	tracker.CompleteGroup(ctx, GroupImportPlayoff,
		fmt.Sprintf("Imported %d playoff games (%d skaters, %d goalies) for %s in %s.",
			playoffTotals.GamesImported, playoffTotals.SkatersImported, playoffTotals.GoaliesImported,
			season.Label(), tracker.GetElapsed(ctx, GroupImportPlayoff)))

	logger.Info("ImportNHLSeasonWorkflow completed",
		"startYear", season.ID.StartYear(),
		"playoffGames", playoffTotals.GamesImported)

	return counts, nil
}
