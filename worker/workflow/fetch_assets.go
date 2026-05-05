package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/workflow"
)

const (
	// fetchAssetsGroupIdx is the index of the single progress group used by
	// the parent FetchAssetsWorkflow.
	fetchAssetsGroupIdx = 0

	// fetchAssetsGroupHeader labels the parent's progress group in UI output.
	fetchAssetsGroupHeader = "Fetching all assets..."
)

// fetchAssetsClassFunc is the signature shared by the nine entry-point
// workflows. Constraining the registry's Workflow field to this type catches
// signature drift at compile time rather than at Temporal replay.
type fetchAssetsClassFunc = func(workflow.Context, FetchAssetsInput) (core.OriginCounts, error)

// assetClassEntry binds an asset class to the parent-side metadata needed to
// size its progress bar and dispatch its child workflow:
//   - Class.Label drives the bar label
//   - CountName names the per-class count activity (registered on the asset
//     task queue) that returns the row count used to size bar.Total
//   - Workflow is the entry-point workflow function the parent spawns
//   - Slug feeds shared.WorkflowIDFetchAssetsClass to produce both the child
//     workflow ID and the bar's ProgressSourceKey, so the GraphQL resolver
//     can merge live per-class progress from Redis at query time
type assetClassEntry struct {
	Class     assetClass
	CountName string
	Workflow  fetchAssetsClassFunc
	Slug      string
}

// parentAssetClasses is the ordered registry of asset classes the parent
// workflow processes. Order drives bar layout in the UI; reordering here
// reorders the displayed bars.
var parentAssetClasses = []assetClassEntry{
	{Class: classPlayerHeadshots, CountName: "CountPlayerHeadshotAssets", Workflow: FetchPlayerHeadshotsWorkflow, Slug: "player-headshots"},
	{Class: classPlayerHeroImages, CountName: "CountPlayerHeroImageAssets", Workflow: FetchPlayerHeroImagesWorkflow, Slug: "player-hero-images"},
	{Class: classPlayerYahooImage, CountName: "CountPlayerYahooImageAssets", Workflow: FetchPlayerYahooImagesWorkflow, Slug: "player-yahoo-images"},
	{Class: classTeamLogos, CountName: "CountTeamLogoAssets", Workflow: FetchTeamLogosWorkflow, Slug: "team-logos"},
	{Class: classYahooTeamLogos, CountName: "CountYahooTeamLogoAssets", Workflow: FetchYahooTeamLogosWorkflow, Slug: "yahoo-team-logos"},
	{Class: classYahooLeagueLogos, CountName: "CountYahooLeagueLogoAssets", Workflow: FetchYahooLeagueLogosWorkflow, Slug: "yahoo-league-logos"},
	{Class: classYahooManagerImages, CountName: "CountYahooManagerImageAssets", Workflow: FetchYahooManagerImagesWorkflow, Slug: "yahoo-manager-images"},
}

// FetchAssetsWorkflow is the parent workflow that orchestrates the nine
// per-class asset fetch child workflows. It:
//  1. Builds a single ProgressGroup with one bar per class.
//  2. Runs nine count activities concurrently to size each bar's Total.
//     Counts return rows; bar.Total is converted to batches because each
//     child workflow tracks batches (not rows) to keep Temporal history
//     event counts small.
//  3. Sets each bar's ProgressSourceKey to its child's stable workflow ID so
//     the GraphQL resolver merges live progress from Redis.
//  4. Dispatches the nine children via RunWorkerPoolMultiBar bounded by
//     MaxAssetClassConcurrencyParam.
//  5. Aggregates core.OriginCounts from every child.
//
// On a child error, returns whatever children completed before the failure
// alongside the error (matches processSeasonGroup's contract).
func FetchAssetsWorkflow(ctx workflow.Context, input *FetchAssetsInput) (core.OriginCounts, error) {
	if input == nil {
		input = &FetchAssetsInput{}
	}
	logger := workflow.GetLogger(ctx)
	logger.Info("FetchAssetsWorkflow started", "refreshCurrent", input.refreshCurrent())

	tracker, err := shared.InitTracker(ctx, newFetchAssetsProgressReport())
	if err != nil {
		return nil, err
	}

	if err := populateClassBarTotals(ctx, tracker); err != nil {
		return nil, err
	}

	tracker.StartGroup(ctx, fetchAssetsGroupIdx)

	concurrency := shared.ResolveConfigInt(logger, shared.MaxAssetClassConcurrencyParam, input.MaxClassConcurrency)
	counts, err := dispatchClassChildren(ctx, tracker, input, concurrency)
	if err != nil {
		return counts, err
	}

	tracker.CompleteGroup(ctx, fetchAssetsGroupIdx,
		fmt.Sprintf("Fetched all assets in %s. %d files downloaded.",
			tracker.GetElapsed(ctx, fetchAssetsGroupIdx), counts.Total()))

	logger.Info("FetchAssetsWorkflow completed", "totalDownloads", counts.Total())
	return counts, nil
}

// newFetchAssetsProgressReport builds the initial ProgressReport with one bar
// per class. Bar Totals start at zero; populateClassBarTotals fills them once
// the count activities return. ProgressSourceKey is set up-front so the
// GraphQL resolver can begin polling each child as soon as it spawns.
func newFetchAssetsProgressReport() *shared.ProgressReport {
	bars := make([]shared.ProgressBar, len(parentAssetClasses))
	for i, e := range parentAssetClasses {
		bars[i] = shared.ProgressBar{
			Label:             e.Class.Label,
			ProgressSourceKey: shared.WorkflowIDFetchAssetsClass(e.Slug),
		}
	}
	return &shared.ProgressReport{
		Groups: []shared.ProgressGroup{
			{Header: fetchAssetsGroupHeader, Bars: bars},
		},
	}
}

// populateClassBarTotals runs the nine count activities concurrently and sets
// each bar's Total to the resulting batch count. Counts return rows; we
// convert to batches so the bar Total matches each child's own progress
// tracker (which counts batches).
func populateClassBarTotals(ctx workflow.Context, tracker *shared.ReportTracker) error {
	queryCtx := workflow.WithActivityOptions(ctx, assetQueryActivityOptions())

	futures := make([]workflow.Future, len(parentAssetClasses))
	for i, e := range parentAssetClasses {
		futures[i] = workflow.ExecuteActivity(queryCtx, e.CountName)
	}

	batchSize := shared.ViperIntOrDefault(config.FlagAssetBatchSize, config.DefaultAssetBatchSize)
	for i, f := range futures {
		var rowCount int
		if err := f.Get(ctx, &rowCount); err != nil {
			return fmt.Errorf("count %s: %w", parentAssetClasses[i].Class.Label, err)
		}
		tracker.SetBarTotal(fetchAssetsGroupIdx, i, batchesForRows(rowCount, batchSize))
	}
	return nil
}

// batchesForRows returns the number of batches a row count produces given
// batchSize, matching splitAssetBatches' partitioning semantics.
func batchesForRows(rowCount, batchSize int) int {
	return (rowCount + batchSize - 1) / batchSize
}

// dispatchClassChildren spawns the nine class child workflows via
// RunWorkerPoolMultiBar and aggregates their OriginCounts. The pool is bounded
// by concurrency (the resolved cross-class concurrency); each child runs on
// the inherited puckdb-asset-tasks queue.
//
// The parent's input is forwarded wholesale; children consult only the fields
// they care about (currently RefreshCurrent), and forwarding-by-default keeps
// the path open for future fields without parent-side bookkeeping.
func dispatchClassChildren(ctx workflow.Context, tracker *shared.ReportTracker, input *FetchAssetsInput, concurrency int) (core.OriginCounts, error) {
	counts := core.OriginCounts{}
	err := tracker.RunWorkerPoolMultiBar(ctx, fetchAssetsGroupIdx, 0, len(parentAssetClasses), concurrency,
		func(wfCtx workflow.Context, i int) workflow.Future {
			e := parentAssetClasses[i]
			return workflow.ExecuteChildWorkflow(
				shared.WithChildOptions(wfCtx, shared.WorkflowIDFetchAssetsClass(e.Slug)),
				e.Workflow, *input)
		},
		func(wfCtx workflow.Context, i int, f workflow.Future) error {
			var classCounts core.OriginCounts
			if err := f.Get(wfCtx, &classCounts); err != nil {
				return fmt.Errorf("fetch %s: %w", parentAssetClasses[i].Class.Label, err)
			}
			counts.Add(classCounts)
			return nil
		})
	return counts, err
}
