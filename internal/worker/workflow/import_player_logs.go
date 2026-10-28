package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/graph/model"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// importPlayerLogsManifestChangeID gates the skater manifest: runs that
// record it list each season's skaters once (CollectSeasonSkaters), size the
// season's bar from that list and pass it to ImportSeasonPlayerLogsV2Workflow.
// Histories without the marker replay importPlayerLogsLegacy.
const (
	importPlayerLogsManifestChangeID = "import-player-logs-skater-manifest"
	importPlayerLogsManifestVersion  = 1
)

// Progress groups of ImportPlayerLogsWorkflow runs with the skater manifest.
// The scan group counts seasons, the import group's bars count players (one
// bar per season, sized by its manifest).
const (
	GroupImportPlayerLogsScan   = 0
	GroupImportPlayerLogsImport = 1
)

// NewImportPlayerLogsProgressReport creates the initial progress structure
// for import player logs: the scan bar is sized once the seasons are known,
// the import bars once their manifests are.
func NewImportPlayerLogsProgressReport() *shared.ProgressReport {
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: "Listing skaters from cached boxscores...", Bars: []shared.ProgressBar{{Label: "Seasons"}}},
			{Header: "Importing player logs...", Bars: []shared.ProgressBar{}},
		},
	}
}

// ImportPlayerLogsWorkflow imports player game logs from cached files into the database.
// It lists each season's skaters from the cached boxscores, then spawns one
// ImportSeasonPlayerLogsV2Workflow per season with that list: the season's
// bar and the child's own bar are both sized from it.
func ImportPlayerLogsWorkflow(ctx workflow.Context, input *model.SeasonsInput) error {
	input = normalizeSeasonsInput(input)
	if workflow.GetVersion(ctx, importPlayerLogsManifestChangeID, workflow.DefaultVersion,
		importPlayerLogsManifestVersion) == workflow.DefaultVersion {
		return importPlayerLogsLegacy(ctx, input)
	}
	logger := workflow.GetLogger(ctx)

	concurrency, err := shared.SnapshotConfigInt(ctx, logger, shared.SeasonConcurrencyParam, input.SeasonConcurrency)
	if err != nil {
		return err
	}
	tracker, err := shared.InitTracker(ctx, NewImportPlayerLogsProgressReport())
	if err != nil {
		return err
	}
	logger.Info("ImportPlayerLogsWorkflow started",
		"startSeason", input.StartSeason,
		"endSeason", input.EndSeason,
		"concurrency", concurrency)

	ctx = workflow.WithActivityOptions(ctx, shared.DefaultActivityOptions())
	seasons, err := loadSeasonsManifest(ctx, logger, input)
	if err != nil {
		return err
	}
	if len(seasons) == 0 {
		return nil
	}

	manifests, err := collectSeasonSkaters(ctx, tracker, seasons, concurrency)
	if err != nil {
		return err
	}

	_, err = processSeasonGroup(ctx, tracker, seasons, concurrency, SeasonGroupConfig{
		GroupIdx:      GroupImportPlayerLogsImport,
		Counter:       manifestCounter(seasons, manifests),
		SourceKeyFunc: WorkflowIDImportSeasonPlayerLogs,
		GroupLabel:    "Imported player logs for",
		CountLabel:    "cache reads",
	}, func(ctx workflow.Context, i int) workflow.Future {
		season := seasons[i]
		return workflow.ExecuteChildWorkflow(
			shared.WithChildOptions(ctx, WorkflowIDImportSeasonPlayerLogs(season.ID.StartYear())),
			ImportSeasonPlayerLogsV2Workflow,
			ImportSeasonPlayerLogsInput{Season: season, PlayerIDs: manifests[i].PlayerIDs})
	})
	return err
}

// collectSeasonSkaters runs CollectSeasonSkaters for every season, at most
// concurrency at a time, advancing the scan bar per season; manifests[i] is
// seasons[i]'s. The scan group's completion message reports the schedules
// and boxscores the scan could not use.
func collectSeasonSkaters(ctx workflow.Context, tracker *shared.ReportTracker, seasons []nhl.SeasonInfo,
	concurrency int) ([]worknhl.SeasonSkaterManifest, error) {
	bar := shared.BarRef{Group: GroupImportPlayerLogsScan}
	tracker.ResizeBar(ctx, bar, len(seasons))
	tracker.StartGroup(ctx, GroupImportPlayerLogsScan)

	var ia *worknhl.ImportActivities
	manifests := make([]worknhl.SeasonSkaterManifest, len(seasons))
	err := tracker.RunWorkerPool(ctx, shared.PoolOptions{
		Total: len(seasons), Concurrency: concurrency,
		Progress: shared.SingleBar{Bar: bar},
		Start: func(activityCtx workflow.Context, i int) workflow.Future {
			return workflow.ExecuteActivity(activityCtx, ia.CollectSeasonSkaters, worknhl.CollectSeasonPlayerIDsInput{
				Season:  seasons[i],
				EndDate: shared.EffectiveEndDate(ctx, seasons[i].StandingsEnd.Time),
			})
		},
		Handle: func(resultCtx workflow.Context, i int, f workflow.Future) error {
			if err := f.Get(resultCtx, &manifests[i]); err != nil {
				return fmt.Errorf("list skaters of %s: %w", seasons[i].Label(), err)
			}
			warnIncompleteManifest(resultCtx, seasons[i], manifests[i])
			return nil
		},
	})
	if err != nil {
		return nil, err
	}
	tracker.CompleteGroup(ctx, GroupImportPlayerLogsScan,
		scanSummary(manifests, tracker.GetElapsed(ctx, GroupImportPlayerLogsScan)))
	return manifests, nil
}

// warnIncompleteManifest logs a season whose scan skipped cached files.
func warnIncompleteManifest(ctx workflow.Context, season nhl.SeasonInfo, m worknhl.SeasonSkaterManifest) {
	if !m.Incomplete() {
		return
	}
	workflow.GetLogger(ctx).Warn("Skater list built from an incomplete cache",
		"season", season.Label(),
		"skaters", len(m.PlayerIDs),
		"unreadableSchedules", m.UnreadableSchedules,
		"missingBoxscores", m.MissingBoxscores,
		"unreadableBoxscores", m.UnreadableBoxscores)
}

// scanSummary is the scan group's completion message: the skaters listed
// and, when any, the cached files the scan could not use.
func scanSummary(manifests []worknhl.SeasonSkaterManifest, elapsed string) string {
	var skaters, schedules, missing, unreadable int
	for _, m := range manifests {
		skaters += len(m.PlayerIDs)
		schedules += m.UnreadableSchedules
		missing += m.MissingBoxscores
		unreadable += m.UnreadableBoxscores
	}
	msg := fmt.Sprintf("Listed %d season skaters from %d seasons in %s.", skaters, len(manifests), elapsed)
	if schedules > 0 {
		msg += fmt.Sprintf(" %d unreadable schedules.", schedules)
	}
	if missing > 0 {
		msg += fmt.Sprintf(" %d boxscores not cached.", missing)
	}
	if unreadable > 0 {
		msg += fmt.Sprintf(" %d unreadable boxscores.", unreadable)
	}
	return msg
}

// manifestCounter sizes each season's import bar from its manifest, the
// list its child imports.
func manifestCounter(seasons []nhl.SeasonInfo, manifests []worknhl.SeasonSkaterManifest) shared.SeasonCounterFunc {
	counts := make(map[nhl.Season]int, len(seasons))
	for i, s := range seasons {
		counts[s.ID] = len(manifests[i].PlayerIDs)
	}
	return func(_ workflow.Context, season nhl.SeasonInfo) (int, error) {
		return counts[season.ID], nil
	}
}
