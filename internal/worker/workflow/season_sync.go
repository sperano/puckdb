package workflow

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

const (
	seasonYearUnset = 0

	groupYahooMetadata  = 0
	groupNHLSeasons     = 1
	groupUpcomingSeason = 2
)

type seasonSyncMode int

const (
	seasonSyncFetch seasonSyncMode = iota
	seasonSyncImport
)

type seasonSyncConfig struct {
	Yahoo       shared.YahooSeasonsSnapshot `json:"yahoo"`
	Concurrency int                         `json:"concurrency"`
}

// ValidateSeasonsInput validates inclusive season bounds used by CLI and GraphQL syncs.
func ValidateSeasonsInput(input *model.SeasonsInput) error {
	if input == nil {
		return nil
	}
	if input.StartSeason != nil && *input.StartSeason <= seasonYearUnset {
		return fmt.Errorf("start season must be positive: %d", *input.StartSeason)
	}
	if input.EndSeason != nil && *input.EndSeason <= seasonYearUnset {
		return fmt.Errorf("end season must be positive: %d", *input.EndSeason)
	}
	if input.StartSeason != nil && input.EndSeason != nil && *input.StartSeason > *input.EndSeason {
		return fmt.Errorf("invalid season range %d-%d: start season is after end season",
			*input.StartSeason, *input.EndSeason)
	}
	return nil
}

func runSeasonSyncWorkflow(ctx workflow.Context, input *model.SeasonsInput, mode seasonSyncMode) error {
	input = normalizeSeasonsInput(input)
	if err := ValidateSeasonsInput(input); err != nil {
		return err
	}

	cfg, err := snapshotSeasonSyncConfig(ctx, input)
	if err != nil {
		return err
	}
	report := newSeasonSyncProgressReport(mode)
	tracker, err := shared.InitTracker(ctx, report)
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	return executeSeasonSync(ctx, tracker, input, cfg, mode)
}

func executeSeasonSync(ctx workflow.Context, tracker *shared.ReportTracker, input *model.SeasonsInput,
	cfg seasonSyncConfig, mode seasonSyncMode) error {
	rangeLabel := formatSeasonRange(input)
	yahooSeasons := selectYahooSeasons(cfg.Yahoo, input)
	if err := processYahooSeasonGroup(ctx, tracker, yahooSeasons, cfg.Concurrency, rangeLabel, mode); err != nil {
		return err
	}

	seasons, err := loadSeasonsManifest(ctx, workflow.GetLogger(ctx), input)
	if err != nil {
		tracker.SetMessage(ctx, fmt.Sprintf("Yahoo metadata finished, but NHL season selection failed for %s: %v", rangeLabel, err))
		return err
	}
	if err := processNHLSeasonGroup(ctx, tracker, seasons, cfg.Concurrency, rangeLabel, mode); err != nil {
		return err
	}
	if err := processUpcomingSeason(ctx, tracker, input, rangeLabel, mode); err != nil {
		return err
	}
	setSeasonSyncOutcome(ctx, tracker, len(yahooSeasons), len(seasons), rangeLabel)
	return nil
}

func snapshotSeasonSyncConfig(ctx workflow.Context, input *model.SeasonsInput) (seasonSyncConfig, error) {
	logger := workflow.GetLogger(ctx)
	cfg, err := shared.SnapshotConfig(ctx, func() seasonSyncConfig {
		return seasonSyncConfig{
			Yahoo:       loadYahooSeasons(),
			Concurrency: shared.ResolveConfigInt(logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency),
		}
	})
	if err != nil {
		return cfg, err
	}
	if err := cfg.Yahoo.LoadError(); err != nil {
		return cfg, fmt.Errorf("load Yahoo seasons config: %w", err)
	}
	return cfg, nil
}

func selectYahooSeasons(snapshot shared.YahooSeasonsSnapshot, input *model.SeasonsInput) []YahooSeasonWorkflowInput {
	years := make([]int, 0, len(snapshot.Seasons))
	for year, season := range snapshot.Seasons {
		if len(season.Leagues) > 0 && seasonInRange(year, input) {
			years = append(years, year)
		}
	}
	sort.Ints(years)
	selected := make([]YahooSeasonWorkflowInput, 0, len(years))
	for _, year := range years {
		selected = append(selected, YahooSeasonWorkflowInput{StartYear: year, Season: snapshot.Seasons[year]})
	}
	return selected
}

func seasonInRange(year int, input *model.SeasonsInput) bool {
	if input.StartSeason != nil && year < *input.StartSeason {
		return false
	}
	return input.EndSeason == nil || year <= *input.EndSeason
}

func normalizeSeasonsInput(input *model.SeasonsInput) *model.SeasonsInput {
	if input == nil {
		return &model.SeasonsInput{}
	}
	return input
}

func formatSeasonRange(input *model.SeasonsInput) string {
	switch {
	case input.StartSeason != nil && input.EndSeason != nil:
		return fmt.Sprintf("seasons %d-%d", *input.StartSeason, *input.EndSeason)
	case input.StartSeason != nil:
		return fmt.Sprintf("seasons %d onward", *input.StartSeason)
	case input.EndSeason != nil:
		return fmt.Sprintf("seasons through %d", *input.EndSeason)
	default:
		return "all seasons"
	}
}

func newSeasonSyncProgressReport(mode seasonSyncMode) *shared.ProgressReport {
	verb := seasonSyncVerb(mode)
	return &shared.ProgressReport{Groups: []shared.ProgressGroup{
		{Header: verb + " Yahoo season metadata...", Bars: []shared.ProgressBar{}},
		{Header: verb + " started NHL seasons...", Bars: []shared.ProgressBar{}},
		{Header: verb + " upcoming NHL season rosters...", Bars: []shared.ProgressBar{{}}},
	}}
}

func seasonSyncVerb(mode seasonSyncMode) string {
	if mode == seasonSyncImport {
		return "Importing"
	}
	return "Fetching"
}

func seasonSyncPastVerb(mode seasonSyncMode) string {
	if mode == seasonSyncImport {
		return "Imported"
	}
	return "Fetched"
}

func processYahooSeasonGroup(ctx workflow.Context, tracker *shared.ReportTracker, seasons []YahooSeasonWorkflowInput,
	concurrency int, rangeLabel string, mode seasonSyncMode) error {
	cleanupStaleYahooReports(ctx, seasons, mode)
	bars := addYahooSeasonBars(tracker, seasons, mode)
	tracker.StartGroup(ctx, groupYahooMetadata)
	if len(seasons) == 0 {
		tracker.CompleteGroup(ctx, groupYahooMetadata,
			fmt.Sprintf("Skipped Yahoo metadata: no configured Yahoo seasons match %s.", rangeLabel))
		return nil
	}
	results := make([]YahooSeasonSyncResult, len(seasons))
	err := tracker.RunWorkerPoolBarRangesSuccessful(ctx, groupYahooMetadata, bars, concurrency,
		func(ctx workflow.Context, i int) workflow.Future { return startYahooSeasonChild(ctx, seasons[i], mode) },
		func(ctx workflow.Context, i int, future workflow.Future) error {
			if err := future.Get(ctx, &results[i]); err != nil {
				return err
			}
			applyYahooLeagueTotals(tracker, bars[i], results[i].LeagueTotals)
			return nil
		})
	if err != nil {
		tracker.SetMessage(ctx, fmt.Sprintf("Yahoo metadata partially completed for %s: %v", rangeLabel, err))
		return err
	}
	tracker.CompleteGroup(ctx, groupYahooMetadata, yahooCompletionMessage(mode, seasons, results,
		tracker.GetElapsed(ctx, groupYahooMetadata), rangeLabel))
	return nil
}

func yahooCompletionMessage(mode seasonSyncMode, seasons []YahooSeasonWorkflowInput, results []YahooSeasonSyncResult,
	elapsed, rangeLabel string) string {
	var unavailable []string
	for i, result := range results {
		for _, resourceName := range result.UnavailableResources {
			unavailable = append(unavailable, fmt.Sprintf("%d %s", seasons[i].StartYear, resourceName))
		}
	}
	message := fmt.Sprintf("%s Yahoo metadata for %d seasons in %s (%s).",
		seasonSyncPastVerb(mode), len(seasons), elapsed, rangeLabel)
	if len(unavailable) > 0 {
		message += " Temporarily unavailable: " + strings.Join(unavailable, ", ") + "."
	}
	return message
}

func startYahooSeasonChild(ctx workflow.Context, input YahooSeasonWorkflowInput, mode seasonSyncMode) workflow.ChildWorkflowFuture {
	if mode == seasonSyncImport {
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportYahooSeason(input.StartYear)), ImportYahooSeasonWorkflow, input)
	}
	return workflow.ExecuteChildWorkflow(
		shared.WithChildOptions(ctx, WorkflowIDFetchYahooSeason(input.StartYear)), FetchYahooSeasonWorkflow, input)
}

func processNHLSeasonGroup(ctx workflow.Context, tracker *shared.ReportTracker, seasons []nhl.SeasonInfo,
	concurrency int, rangeLabel string, mode seasonSyncMode) error {
	if len(seasons) == 0 {
		tracker.StartGroup(ctx, groupNHLSeasons)
		tracker.CompleteGroup(ctx, groupNHLSeasons,
			fmt.Sprintf("Skipped NHL data: no started NHL seasons match %s.", rangeLabel))
		return nil
	}
	cfg := SeasonGroupConfig{
		GroupIdx: groupNHLSeasons, Counter: daysWithPlayoffTeamsCounter(),
		SourceKeyFunc: nhlSeasonSourceKey(mode), GroupLabel: seasonSyncPastVerb(mode),
		CountLabel: "cache reads", CompleteOnlyOnSuccess: true,
	}
	_, err := processSeasonGroup(ctx, tracker, seasons, concurrency, cfg,
		func(ctx workflow.Context, i int) workflow.Future { return startNHLSeasonChild(ctx, seasons[i], mode) })
	if err != nil {
		tracker.SetMessage(ctx, fmt.Sprintf("NHL data partially completed for %s: %v", rangeLabel, err))
	}
	return err
}

func startNHLSeasonChild(ctx workflow.Context, season nhl.SeasonInfo, mode seasonSyncMode) workflow.ChildWorkflowFuture {
	if mode == seasonSyncImport {
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportNHLSeason(season.ID.StartYear())), ImportNHLSeasonWorkflow, season)
	}
	return workflow.ExecuteChildWorkflow(
		shared.WithChildOptions(ctx, WorkflowIDFetchNHLSeason(season.ID.StartYear())), FetchNHLSeasonWorkflow, season)
}

func nhlSeasonSourceKey(mode seasonSyncMode) shared.ProgressSourceKeyFunc {
	if mode == seasonSyncImport {
		return WorkflowIDImportNHLSeason
	}
	return WorkflowIDFetchNHLSeason
}

func setSeasonSyncOutcome(ctx workflow.Context, tracker *shared.ReportTracker, yahooCount, nhlCount int, rangeLabel string) {
	if yahooCount == 0 && nhlCount == 0 {
		tracker.SetMessage(ctx, fmt.Sprintf(
			"No work matched %s: no configured Yahoo season matches the range and no started NHL season matches the range.",
			rangeLabel))
		return
	}
	tracker.SetMessage(ctx, fmt.Sprintf("Completed %s: %d Yahoo season(s), %d started NHL season(s).",
		rangeLabel, yahooCount, nhlCount))
}
