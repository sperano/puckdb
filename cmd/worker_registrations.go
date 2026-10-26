package cmd

import (
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/asset"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	workplayer "github.com/sperano/puckdb/internal/worker/player"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/sperano/puckdb/internal/worker/simulation"
	"github.com/sperano/puckdb/internal/worker/workflow"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/worker"
)

// registerTasksWorkflows registers all workflows for the tasks queue.
func registerTasksWorkflows(w worker.Worker) {
	// Fetch workflows
	w.RegisterWorkflow(workflow.FetchSeasonsWorkflow)
	w.RegisterWorkflow(workflow.FetchNHLSeasonWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooSeasonWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.FetchSeasonPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooPlayersWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerLandingsWorkflow)
	w.RegisterWorkflow(workflow.FetchEdgeSeasonsWorkflow)
	w.RegisterWorkflow(workflow.FetchEdgeWorkflow)

	// Import workflows
	w.RegisterWorkflow(workflow.ImportSeasonsWorkflow)
	w.RegisterWorkflow(workflow.ImportPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.ImportNHLSeasonWorkflow)
	w.RegisterWorkflow(workflow.ImportYahooSeasonWorkflow)
	w.RegisterWorkflow(workflow.ImportSeasonPlayerLogsWorkflow)
	w.RegisterWorkflow(workflow.ImportEdgeSeasonsWorkflow)
	w.RegisterWorkflow(workflow.ImportEdgeWorkflow)

	// Processing workflows
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflow)
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflowContinueV2)
	// Legacy continuation: chains started before the player manifest.
	w.RegisterWorkflow(workflow.ProcessPlayersWorkflowContinue)
	w.RegisterWorkflow(workflow.InitializeWorkflow)
	w.RegisterWorkflow(workflow.ExtractBoxscorePlayersWorkflow)

	// Simulation workflow
	simulation.RegisterSimPoolWorkflow(w)

	// Player news and rankings (draft helper)
	w.RegisterWorkflow(workflow.RefreshNewsWorkflow)
	w.RegisterWorkflow(workflow.RefreshDraftRankingsWorkflow)
}

// taskActivityDeps holds the dependencies shared by every per-domain
// activity registration helper below. Building it once in
// registerTasksActivities and threading it through avoids repeating the
// same five-argument signature on each helper.
type taskActivityDeps struct {
	storage         store.Storage
	pool            *pgxpool.Pool
	queries         *sqlcdb.Queries
	nhlClient       shared.NHLClient
	gobCache        *cache.GobCache
	redisClient     *redis.Client
	yahooDownloader shared.Downloader
	// yahooPublicDownloader fetches public Yahoo Sports player pages. It
	// shares yahooDownloader's rate limiter: both count against Yahoo.
	yahooPublicDownloader  shared.Downloader
	boxscorePlayerCacheTTL time.Duration
}

// registerTasksActivities registers all activities for the tasks queue.
func registerTasksActivities(w worker.Worker, pool *pgxpool.Pool, redisClient *redis.Client) error {
	gobCache, err := newGobCache(redisClient)
	if err != nil {
		return err
	}
	yahooLimiter, err := newYahooRateLimiter()
	if err != nil {
		return err
	}

	d := taskActivityDeps{
		storage:                newDefaultStorage(),
		pool:                   pool,
		queries:                sqlcdb.New(pool),
		nhlClient:              shared.NewNHLClient(),
		gobCache:               gobCache,
		redisClient:            redisClient,
		yahooDownloader:        newYahooDownloader(redisClient, yahooLimiter),
		yahooPublicDownloader:  shared.RateLimited(yahooLimiter, httpx.DownloadPublic),
		boxscorePlayerCacheTTL: boxscorePlayerCacheTTLFrom(viper.GetViper()),
	}

	registerTaskQueueActivities(w, d)
	return nil
}

// registerTaskQueueActivities registers every activity of the tasks queue.
// TestRegisteredActivitiesAreScheduledOrRecorded calls it with zero deps, so
// it must only register, never use, the dependencies.
func registerTaskQueueActivities(w worker.Worker, d taskActivityDeps) {
	registerSimulationActivities(w, d.pool, d.queries)
	registerProgressActivities(w, d.redisClient)
	registerYahooFetchActivities(w, d)

	importYahooActivities := registerYahooImportActivities(w, d)
	registerNHLActivities(w, d, importYahooActivities)
	registerPlayerActivities(w, d)
	registerNewsActivities(w, d.pool, d.queries)
	registerDraftRankingActivities(w, d.pool)
}

// registerAssetQueueActivities registers every activity of the asset queue;
// like registerTaskQueueActivities, the test calls it with nil dependencies.
func registerAssetQueueActivities(w worker.Worker, queries *sqlcdb.Queries, redisClient *redis.Client) {
	registerAssetActivities(w, queries)
	registerProgressActivities(w, redisClient)
}

// registerSimulationActivities registers the simulation pool activities.
// See worker/simulation. AgentFactory is nil so each activity falls back
// to defaultAgentFactory (NewAgent).
func registerSimulationActivities(w worker.Worker, pool *pgxpool.Pool, queries *sqlcdb.Queries) {
	simActs := &simulation.Activities{
		Queries:         queries,
		Tx:              simulation.NewPgxTransactor(pool),
		ProviderConfigs: configuredLLMProviders(),
	}
	simulation.RegisterActivities(w, simActs)
}

// registerYahooFetchActivities registers the Yahoo Fantasy API download activities.
func registerYahooFetchActivities(w worker.Worker, d taskActivityDeps) {
	fetchYahooActivities := &yahoo.FetchActivities{
		Storage:          d.storage,
		Download:         d.yahooDownloader,
		GobCache:         d.gobCache,
		PublicDownloader: d.yahooPublicDownloader,
	}
	w.RegisterActivity(fetchYahooActivities.FetchLeague)
	w.RegisterActivity(fetchYahooActivities.FetchTeams)
	w.RegisterActivity(fetchYahooActivities.FetchYahooPlayerBatch)
	w.RegisterActivity(fetchYahooActivities.FetchYahooTransactions)
	w.RegisterActivity(fetchYahooActivities.FetchYahooDraftResults)
	w.RegisterActivity(fetchYahooActivities.FetchYahooLeagueStandings)
	w.RegisterActivity(fetchYahooActivities.FetchYahooMatchupWeek)
	w.RegisterActivity(fetchYahooActivities.PlanYahooLeaguePlayerPool)
	w.RegisterActivity(fetchYahooActivities.FetchYahooLeaguePlayersPage)
	w.RegisterActivity(fetchYahooActivities.CommitYahooLeaguePlayerPool)
}

// registerYahooImportActivities registers the Yahoo cached-file-to-Postgres
// import activities. Returns the activities struct since worknhl.ImportActivities
// embeds it as its Yahoo field.
func registerYahooImportActivities(w worker.Worker, d taskActivityDeps) *yahoo.ImportActivities {
	importYahooActivities := &yahoo.ImportActivities{
		Storage:  d.storage,
		GobCache: d.gobCache,
		Queries:  d.queries,
		Tx:       yahoo.NewPgxTransactor(d.pool),
	}
	w.RegisterActivity(importYahooActivities.ImportYahooLeague)
	w.RegisterActivity(importYahooActivities.ImportYahooStandInLeague) // TEMPORARY: see config.LeagueMetadataSource
	w.RegisterActivity(importYahooActivities.ImportYahooTeams)
	w.RegisterActivity(importYahooActivities.ImportYahooLeagueData)
	w.RegisterActivity(importYahooActivities.ImportYahooLeaguePlayers)
	return importYahooActivities
}

// registerNHLActivities registers all NHL-domain activities: franchises,
// seasons/Edge tracking, cached-file import, daily schedule, playoffs, and
// boxscore extraction. importYahooActivities is threaded in because
// worknhl.ImportActivities delegates Yahoo-specific import steps to it.
func registerNHLActivities(w worker.Worker, d taskActivityDeps, importYahooActivities *yahoo.ImportActivities) {
	franchiseActivities := &worknhl.FranchiseActivities{
		Storage:   d.storage,
		GobCache:  d.gobCache,
		Upserter:  d.queries,
		NHLClient: d.nhlClient,
	}
	w.RegisterActivity(franchiseActivities.FetchFranchises)
	w.RegisterActivity(franchiseActivities.UpsertFranchises)

	seasonsActivities := worknhl.NewSeasonsActivities(d.queries, worknhl.SeasonsActivities{
		Storage:     d.storage,
		GobCache:    d.gobCache,
		NHLClient:   d.nhlClient,
		RedisClient: d.redisClient,
	})
	w.RegisterActivity(seasonsActivities.FetchSeasonsManifest)
	w.RegisterActivity(seasonsActivities.UpsertSeasons)
	w.RegisterActivity(seasonsActivities.InitializeSeasonTeamsActivity)
	w.RegisterActivity(seasonsActivities.FetchSeasonRosters)
	w.RegisterActivity(seasonsActivities.FetchClubStats)
	w.RegisterActivity(seasonsActivities.ImportSeasonRosters)
	w.RegisterActivity(seasonsActivities.FetchUpcomingSeasonRosters)
	w.RegisterActivity(seasonsActivities.ImportUpcomingSeasonRosters)
	w.RegisterActivity(seasonsActivities.ImportClubStats)
	w.RegisterActivity(seasonsActivities.FetchEdgeLandings)
	w.RegisterActivity(seasonsActivities.GetEdgeSeasonTeams)
	w.RegisterActivity(seasonsActivities.FetchEdgeTeam)
	w.RegisterActivity(seasonsActivities.FetchEdgeTeamSkaters)
	w.RegisterActivity(seasonsActivities.FetchEdgeTeamGoalies)
	// Season-wide Edge imports: no workflow schedules them since the
	// per-team activities below replaced them; ImportEdgeWorkflow histories
	// from before that change recorded these names.
	w.RegisterActivity(seasonsActivities.ImportEdgeSkaters)
	w.RegisterActivity(seasonsActivities.ImportEdgeGoalies)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeams)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeamZoneTimeDetails)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeam)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeamSkaters)
	w.RegisterActivity(seasonsActivities.ImportEdgeTeamGoalies)

	importActivities := worknhl.NewImportActivities(d.queries, worknhl.ImportActivities{
		Storage:  d.storage,
		GobCache: d.gobCache,
		Tx:       worknhl.NewPgxTransactor(d.pool),
		Yahoo:    importYahooActivities,
	})
	// The per-file day imports (ImportBoxscoresForDate, ...) and
	// Yahoo.ImportYahooDataForDate run in-process inside ImportDay; no
	// workflow ever scheduled them by name, so they are not registered.
	w.RegisterActivity(importActivities.ImportDay)
	w.RegisterActivity(importActivities.ImportDayIfChanged)
	w.RegisterActivity(importActivities.ImportPlayerGameLogsBatch)
	w.RegisterActivity(importActivities.CollectSeasonPlayerIDs)

	dailyScheduleActivities := &worknhl.DailyScheduleActivities{
		Storage:     d.storage,
		NHLClient:   d.nhlClient,
		GobCache:    d.gobCache,
		RedisClient: d.redisClient,
		Download:    d.yahooDownloader,
	}
	// FetchDay calls FetchDailySchedule in-process. The only workflow that
	// scheduled FetchDailySchedule by name, FetchDayWorkflow, is gone, so no
	// history this worker can run refers to it.
	w.RegisterActivity(dailyScheduleActivities.FetchDay)

	playoffActivities := worknhl.NewPlayoffActivities(d.queries, worknhl.PlayoffActivities{
		Storage:   d.storage,
		GobCache:  d.gobCache,
		NHLClient: d.nhlClient,
	})
	w.RegisterActivity(playoffActivities.ListSeasonTeams)
	w.RegisterActivity(playoffActivities.FetchTeamPlayoffGames)
	w.RegisterActivity(playoffActivities.ImportTeamPlayoffGames)

	boxscoreActivities := &worknhl.BoxscoreActivities{
		Storage:                d.storage,
		GobCache:               d.gobCache,
		RedisClient:            d.redisClient,
		BoxscorePlayerCacheTTL: d.boxscorePlayerCacheTTL,
	}
	// ExtractAndSaveBoxscorePlayers calls ExtractBoxscoreDataForSeason
	// in-process; no workflow scheduled the latter by name.
	w.RegisterActivity(boxscoreActivities.ExtractAndSaveBoxscorePlayers)
}

// registerPlayerActivities registers player-domain activities: landing page
// fetch, game log download/import, boxscore-player consolidation, and
// Yahoo/NHL player matching.
func registerPlayerActivities(w worker.Worker, d taskActivityDeps) {
	playerActivities := workplayer.NewActivities(d.queries, workplayer.Activities{
		Storage:                d.storage,
		NHLClient:              d.nhlClient,
		RedisClient:            d.redisClient,
		GobCache:               d.gobCache,
		BoxscorePlayerCacheTTL: d.boxscorePlayerCacheTTL,
		Manifests:              workplayer.NewPgProcessPlayersStore(d.pool),
	})
	w.RegisterActivity(playerActivities.FetchPlayerLandingsBatch)
	w.RegisterActivity(playerActivities.DownloadPlayerGameLogsBatch)
	w.RegisterActivity(playerActivities.LoadSeasonBoxscorePlayers)
	w.RegisterActivity(playerActivities.LoadAllBoxscorePlayers)
	w.RegisterActivity(playerActivities.ConsolidateBoxscorePlayersActivity)
	w.RegisterActivity(playerActivities.CountPlayersForAllSeasons)
	w.RegisterActivity(playerActivities.ProcessPlayerBatch) // legacy chains only
	w.RegisterActivity(playerActivities.CreateProcessPlayersManifest)
	w.RegisterActivity(playerActivities.ProcessPlayerManifestBatch)
	w.RegisterActivity(playerActivities.RecordProcessPlayersErrors)
	w.RegisterActivity(playerActivities.FinishProcessPlayersManifest)
	w.RegisterActivity(playerActivities.EnsureYahooIDPool)
	w.RegisterActivity(playerActivities.ListYahooPlayerFiles)
	w.RegisterActivity(playerActivities.ParseYahooPlayerBatch)
	w.RegisterActivity(playerActivities.SaveYahooPlayersToRedis)
	w.RegisterActivity(playerActivities.LoadUnmatchedYahooPlayers)
	w.RegisterActivity(playerActivities.VerifyUnmatchedBatch)
	w.RegisterActivity(playerActivities.CleanupYahooIDPoolData)
}

// registerAssetWorkflows registers all asset download workflows on the given
// worker (expected to be polling shared.TaskQueueAssets). The generic
// FetchAssetsClassWorkflow is intentionally not registered: it is invoked as a
// regular Go function from the seven entry-point wrappers below, never via
// ExecuteChildWorkflow, so Temporal does not need to resolve it by name.
func registerAssetWorkflows(w worker.Worker) {
	// Parent: orchestrates the seven class children
	w.RegisterWorkflow(workflow.FetchAssetsWorkflow)

	// Per-class entry-point wrappers — each is a distinct workflow type in the Temporal UI
	w.RegisterWorkflow(workflow.FetchPlayerHeadshotsWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerHeroImagesWorkflow)
	w.RegisterWorkflow(workflow.FetchPlayerYahooImagesWorkflow)
	w.RegisterWorkflow(workflow.FetchTeamLogosWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooTeamLogosWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooLeagueLogosWorkflow)
	w.RegisterWorkflow(workflow.FetchYahooManagerImagesWorkflow)
}

// registerProgressActivities registers the ProgressReport Save/Load/DeleteBatch
// local activities on the given worker. Every worker that runs a workflow
// invoking shared.InitTracker (or any other progress-tracker call) needs these
// registered, otherwise local-activity dispatch falls through to the typed-nil
// receiver baked into the call site and panics on a.RedisClient.
func registerProgressActivities(w worker.Worker, redisClient *redis.Client) {
	progressActivities := &shared.ProgressActivities{RedisClient: redisClient}
	w.RegisterActivity(progressActivities.Save)
	w.RegisterActivity(progressActivities.Load)
	w.RegisterActivity(progressActivities.DeleteBatch)
}

// registerAssetActivities registers all asset download and query activities on
// the given worker.
func registerAssetActivities(w worker.Worker, queries *sqlcdb.Queries) {
	storage := newDefaultStorage()
	assetActivities := &asset.Activities{
		Storage:  storage,
		Download: asset.NewDownloader(),
		Queries:  queries,
	}

	// Download activity
	w.RegisterActivity(assetActivities.FetchAssetBatch)

	// Query (loader) activities — one per asset class
	w.RegisterActivity(assetActivities.LoadPlayerHeadshotAssets)
	w.RegisterActivity(assetActivities.LoadPlayerHeroImageAssets)
	w.RegisterActivity(assetActivities.LoadPlayerYahooImageAssets)
	w.RegisterActivity(assetActivities.LoadTeamLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooTeamLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooLeagueLogoAssets)
	w.RegisterActivity(assetActivities.LoadYahooManagerImageAssets)

	// Count activities — one per asset class. Used by the parent
	// FetchAssetsWorkflow to size its progress bars before dispatch.
	w.RegisterActivity(assetActivities.CountPlayerHeadshotAssets)
	w.RegisterActivity(assetActivities.CountPlayerHeroImageAssets)
	w.RegisterActivity(assetActivities.CountPlayerYahooImageAssets)
	w.RegisterActivity(assetActivities.CountTeamLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooTeamLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooLeagueLogoAssets)
	w.RegisterActivity(assetActivities.CountYahooManagerImageAssets)
}
