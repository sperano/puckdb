package workflow

import (
	"fmt"
	"time"

	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/worker/asset"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// assetActivityStartToClose is the start-to-close timeout for
	// FetchAssetBatch activities. Image downloads are sub-second, but a full
	// batch of 100 images at slow CDN rates needs headroom.
	assetActivityStartToClose = 90 * time.Second

	// assetActivityHeartbeat is the heartbeat timeout for FetchAssetBatch.
	// The activity records a heartbeat every heartbeatEveryNRows rows.
	assetActivityHeartbeat = 30 * time.Second

	// assetQueryActivityStartToClose is the start-to-close timeout for loader
	// and count query activities. A full table scan of ~10K rows is fast in
	// Postgres but can be slow on a loaded system.
	assetQueryActivityStartToClose = 5 * time.Minute

	assetRetryInitialInterval    = 1 * time.Second
	assetRetryBackoffCoefficient = 2.0
	assetRetryMaxInterval        = 1 * time.Minute
	assetRetryMaxAttempts        = 3
)

// assetRetryPolicy returns a fresh *temporal.RetryPolicy so callers can't
// mutate shared state (the embedded pointer would otherwise be shared across
// every workflow that picks up the parent ActivityOptions).
func assetRetryPolicy() *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:    assetRetryInitialInterval,
		BackoffCoefficient: assetRetryBackoffCoefficient,
		MaximumInterval:    assetRetryMaxInterval,
		MaximumAttempts:    assetRetryMaxAttempts,
	}
}

// assetActivityOptions returns Temporal activity options for FetchAssetBatch
// invocations. Uses puckdb-asset-tasks so asset downloads cannot starve the
// main puckdb-tasks queue (architectural delta 4 / 5). Returned as a fresh
// value rather than a package-level var so workflows can't mutate it.
func assetActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: assetActivityStartToClose,
		HeartbeatTimeout:    assetActivityHeartbeat,
		TaskQueue:           shared.TaskQueueAssets,
		RetryPolicy:         assetRetryPolicy(),
	}
}

// assetQueryActivityOptions returns Temporal activity options for loader query
// activities. The timeout is longer than assetActivityOptions because a DB scan
// of ~10K rows may take longer than downloading a single image batch.
func assetQueryActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: assetQueryActivityStartToClose,
		TaskQueue:           shared.TaskQueueAssets,
		RetryPolicy:         assetRetryPolicy(),
	}
}

// assetClass describes one category of image assets. It drives both the generic
// workflow and the progress bar label.
type assetClass struct {
	// FileType is the core.FileType constant for this class.
	FileType core.FileType
	// Label is the human-readable name shown in progress headers.
	Label string
	// LoaderName is the registered Temporal activity name for the query loader
	// (e.g., "LoadPlayerHeadshotAssets"). Keeping it as a string lets the
	// generic workflow invoke any loader without importing the asset package's
	// concrete function pointers.
	LoaderName string
}

// Package-level assetClass vars — one per FileType. These are the single source
// of truth wiring FileType → loader name. The nine entry-point wrappers below
// each reference one of these.
var (
	classPlayerHeadshots = assetClass{
		FileType:   core.PlayerHeadshot,
		Label:      "Player Headshots",
		LoaderName: "LoadPlayerHeadshotAssets",
	}
	classPlayerHeroImages = assetClass{
		FileType:   core.PlayerHeroImage,
		Label:      "Player Hero Images",
		LoaderName: "LoadPlayerHeroImageAssets",
	}
	classPlayerYahooImage = assetClass{
		FileType:   core.PlayerYahooImage,
		Label:      "Player Yahoo Images",
		LoaderName: "LoadPlayerYahooImageAssets",
	}
	classTeamLogos = assetClass{
		FileType:   core.TeamLogo,
		Label:      "Team Logos",
		LoaderName: "LoadTeamLogoAssets",
	}
	classYahooTeamLogos = assetClass{
		FileType:   core.YahooTeamLogo,
		Label:      "Yahoo Team Logos",
		LoaderName: "LoadYahooTeamLogoAssets",
	}
	classYahooLeagueLogos = assetClass{
		FileType:   core.YahooLeagueLogo,
		Label:      "Yahoo League Logos",
		LoaderName: "LoadYahooLeagueLogoAssets",
	}
	classYahooManagerImages = assetClass{
		FileType:   core.YahooManagerImage,
		Label:      "Yahoo Manager Images",
		LoaderName: "LoadYahooManagerImageAssets",
	}
)

// FetchAssetsInput is the unified input type used by both the per-class
// entry-point workflows and the parent FetchAssetsWorkflow. Pointer fields let
// callers express "use the configured default" via nil; the parent uses all
// three knobs while entry-point invocations only consult RefreshCurrent.
//
// Mirrors the pattern of model.SeasonsInput (gqlgen-generated nullable fields
// → Go pointers). The Phase 5 GraphQL schema will emit a structurally
// identical model.FetchAssetsInput; the two are intended to be interchangeable
// across the workflow boundary.
type FetchAssetsInput struct {
	// RefreshCurrent forces re-download even when the local cache file exists.
	// nil is treated as false.
	RefreshCurrent *bool
	// ClassConcurrency overrides FlagAssetClassConcurrency for the within-class
	// FetchAssetBatch worker pool. Only honoured when set on the parent's input;
	// entry-point invocations ignore it.
	ClassConcurrency *int
	// MaxClassConcurrency overrides FlagMaxAssetClassConcurrency for the
	// parent's cross-class worker pool. Parent-only.
	MaxClassConcurrency *int
	// AssetBatchSize overrides FlagAssetBatchSize for splitting the loaded
	// assets into FetchAssetBatch batches. The parent sets this from its own
	// snapshotted config before dispatching children, so children partition
	// assets exactly the way the parent sized its progress bars; nil on a
	// direct entry-point invocation (not spawned by the parent), which falls
	// back to the configured default.
	AssetBatchSize *int
}

// refreshCurrent reports whether RefreshCurrent is non-nil and true.
func (i FetchAssetsInput) refreshCurrent() bool {
	return i.RefreshCurrent != nil && *i.RefreshCurrent
}

// FetchAssetsClassInput is the input for the generic per-class child workflow.
type FetchAssetsClassInput struct {
	Class          assetClass
	RefreshCurrent bool
	// BatchSize overrides FlagAssetBatchSize for splitting the loaded assets
	// into FetchAssetBatch batches. nil uses the configured default.
	BatchSize *int
}

// fetchAssetsClassConfig is the configuration FetchAssetsClassWorkflow
// snapshots once at start: BatchSize decides how the loaded assets are split
// and Concurrency decides how many FetchAssetBatch activities run in
// parallel, so neither may be re-read on replay.
type fetchAssetsClassConfig struct {
	BatchSize   int `json:"batchSize"`
	Concurrency int `json:"concurrency"`
}

// loadFetchAssetsClassConfig resolves fetchAssetsClassConfig. Only the batch
// size takes an input override (forwarded by the parent); concurrency comes
// from configuration alone.
func loadFetchAssetsClassConfig(logger log.Logger, batchOverride *int) fetchAssetsClassConfig {
	return fetchAssetsClassConfig{
		BatchSize:   shared.ResolveConfigInt(nil, shared.AssetBatchSizeParam, batchOverride),
		Concurrency: shared.ResolveConfigInt(logger, shared.AssetClassConcurrencyParam, nil),
	}
}

// snapshotFetchAssetsClassConfig records fetchAssetsClassConfig in history
// once per execution (see shared.SnapshotConfig).
func snapshotFetchAssetsClassConfig(ctx workflow.Context, logger log.Logger, batchOverride *int) (fetchAssetsClassConfig, error) {
	return shared.SnapshotConfig(ctx, func() fetchAssetsClassConfig {
		return loadFetchAssetsClassConfig(logger, batchOverride)
	})
}

const (
	// fetchAssetsProgressGroupIdx is the index of the single progress group
	// used by FetchAssetsClassWorkflow.
	fetchAssetsProgressGroupIdx = 0

	// fetchAssetsProgressBarIdx is the index of the single progress bar
	// within that group. The bar tracks completed batches, not rows.
	fetchAssetsProgressBarIdx = 0
)

// FetchAssetsClassWorkflow is the generic per-class child workflow that:
//  1. Executes the loader activity to retrieve all assets for the class.
//  2. Splits them into batches of DefaultAssetBatchSize.
//  3. Dispatches FetchAssetBatch activities concurrently via RunWorkerPool.
//  4. Aggregates per-row origins into an OriginCounts result.
//  5. Logs per-row errors but does not fail the workflow for them.
func FetchAssetsClassWorkflow(ctx workflow.Context, input FetchAssetsClassInput) (core.OriginCounts, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("FetchAssetsClassWorkflow started",
		"class", input.Class.Label,
		"refreshCurrent", input.RefreshCurrent)

	cfg, err := snapshotFetchAssetsClassConfig(ctx, logger, input.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("snapshot config for %s: %w", input.Class.Label, err)
	}

	// Step 1: load all asset rows from the database.
	queryCtx := workflow.WithActivityOptions(ctx, assetQueryActivityOptions())
	var assets []asset.Asset
	if err := workflow.ExecuteActivity(queryCtx, input.Class.LoaderName).Get(ctx, &assets); err != nil {
		return nil, fmt.Errorf("load %s assets: %w", input.Class.Label, err)
	}

	if len(assets) == 0 {
		logger.Info("FetchAssetsClassWorkflow: no assets found, nothing to do", "class", input.Class.Label)
		return core.OriginCounts{}, nil
	}

	// Step 2: split assets into batches.
	batchSize := cfg.BatchSize
	batches := splitAssetBatches(assets, batchSize)
	numBatches := len(batches)

	// Step 3: initialise the progress tracker. The bar tracks batches (not rows)
	// to keep history-event counts small (see plan v2 §Phase 3).
	tracker, err := shared.InitTracker(ctx, &shared.ProgressReport{
		Total: numBatches,
		Groups: []shared.ProgressGroup{
			{
				Header: fmt.Sprintf("Fetching %s...", input.Class.Label),
				Bars:   []shared.ProgressBar{{Total: numBatches}},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("init tracker for %s: %w", input.Class.Label, err)
	}
	tracker.StartGroup(ctx, fetchAssetsProgressGroupIdx)

	// Step 4: dispatch FetchAssetBatch activities via RunWorkerPool.
	concurrency := cfg.Concurrency
	batchCtx := workflow.WithActivityOptions(ctx, assetActivityOptions())
	counts := core.OriginCounts{}

	err = tracker.RunWorkerPool(
		ctx,
		fetchAssetsProgressGroupIdx,
		fetchAssetsProgressBarIdx,
		numBatches,
		concurrency,
		func(_ workflow.Context, i int) workflow.Future {
			var act *asset.Activities
			return workflow.ExecuteActivity(batchCtx, act.FetchAssetBatch, asset.FetchAssetBatchInput{
				Assets:         batches[i],
				RefreshCurrent: input.RefreshCurrent,
			})
		},
		func(wfCtx workflow.Context, _ int, f workflow.Future) error {
			var result asset.FetchAssetBatchResult
			if err := f.Get(wfCtx, &result); err != nil {
				return err
			}
			// Step 5: accumulate per-row origins; log per-row errors without
			// failing the workflow.
			for _, r := range result.Results {
				if r.Err != "" {
					logger.Warn("asset fetch error", "class", input.Class.Label, "err", r.Err)
					continue
				}
				if r.Origin != core.OriginUnknown {
					counts[r.Origin]++
				}
			}
			return nil
		},
	)
	if err != nil {
		return counts, fmt.Errorf("run worker pool for %s: %w", input.Class.Label, err)
	}

	tracker.CompleteGroup(ctx, fetchAssetsProgressGroupIdx,
		fmt.Sprintf("Fetched %s in %s.", input.Class.Label, tracker.GetElapsed(ctx, fetchAssetsProgressGroupIdx)))

	logger.Info("FetchAssetsClassWorkflow completed",
		"class", input.Class.Label,
		"batches", numBatches)
	return counts, nil
}

// splitAssetBatches divides assets into consecutive slices of at most size
// elements. The last batch may be smaller than size.
func splitAssetBatches(assets []asset.Asset, size int) [][]asset.Asset {
	if size <= 0 {
		size = config.DefaultAssetBatchSize
	}
	batches := make([][]asset.Asset, 0, (len(assets)+size-1)/size)
	for len(assets) > 0 {
		end := size
		if end > len(assets) {
			end = len(assets)
		}
		batches = append(batches, assets[:end])
		assets = assets[end:]
	}
	return batches
}

// ─── Nine entry-point wrappers ───────────────────────────────────────────────
//
// Each is a thin wrapper around FetchAssetsClassWorkflow with a fixed
// assetClass. Temporal registers these as nine distinct workflow types, making
// them individually visible in the UI without parameterisation confusion.

// newFetchAssetsClassInput builds the child input for one asset class from a
// wrapper's FetchAssetsInput, forwarding the fields entry-point invocations
// honour: RefreshCurrent and the AssetBatchSize override (nil on a direct
// entry-point invocation, which then falls back to the configured default).
func newFetchAssetsClassInput(class assetClass, input FetchAssetsInput) FetchAssetsClassInput {
	return FetchAssetsClassInput{
		Class:          class,
		RefreshCurrent: input.refreshCurrent(),
		BatchSize:      input.AssetBatchSize,
	}
}

// FetchPlayerHeadshotsWorkflow downloads all NHL player headshot images.
func FetchPlayerHeadshotsWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classPlayerHeadshots, input))
}

// FetchPlayerHeroImagesWorkflow downloads all NHL player hero images.
func FetchPlayerHeroImagesWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classPlayerHeroImages, input))
}

// FetchPlayerYahooImagesWorkflow downloads all Yahoo player images.
func FetchPlayerYahooImagesWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classPlayerYahooImage, input))
}

// FetchTeamLogosWorkflow downloads all NHL team logos (one per team, latest season).
func FetchTeamLogosWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classTeamLogos, input))
}

// FetchYahooTeamLogosWorkflow downloads all Yahoo fantasy team logos.
func FetchYahooTeamLogosWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classYahooTeamLogos, input))
}

// FetchYahooLeagueLogosWorkflow downloads all Yahoo fantasy league logos.
func FetchYahooLeagueLogosWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classYahooLeagueLogos, input))
}

// FetchYahooManagerImagesWorkflow downloads all Yahoo fantasy team manager images.
func FetchYahooManagerImagesWorkflow(ctx workflow.Context, input FetchAssetsInput) (core.OriginCounts, error) {
	return FetchAssetsClassWorkflow(ctx, newFetchAssetsClassInput(classYahooManagerImages, input))
}
